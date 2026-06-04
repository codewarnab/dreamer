// Package mcpserver implements the MCP server for Phase 2 finding recording
// and shared validation/JSONL logic used by both the MCP server and the
// record-finding CLI tool.
//
// Design note: validation.go validates finding fields (category, description,
// severity, etc.) — it does NOT filter or restrict shell commands passed to
// the Bash tool. Command-level restrictions (network egress, interpreter
// inline-exec, secret reads, persistence, etc.) are enforced by the OS
// sandbox layer (internal/sandbox/), not by this package. This is
// intentional: the sandbox operates at the kernel/syscall level and covers
// all providers uniformly, whereas an argv filter would be bypassable and
// would need per-platform maintenance.
package mcpserver

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"dreamer/internal/categories"
	"dreamer/internal/fsutil"
)

// ValidCategories is the set of valid rule categories (lowercase canonical
// IDs). Inputs are lowercased+trimmed before lookup so "Test" and " test "
// both match "test". Derived from the canonical category constants.
//
// User-defined packs loaded at runtime register their categories here via
// RegisterCategory so ValidateFinding accepts them without a binary rebuild.
var ValidCategories = func() map[string]bool {
	m := make(map[string]bool, len(categories.All()))
	for _, c := range categories.All() {
		m[string(c)] = true
	}
	return m
}()

// RegisterCategory adds a category to ValidCategories so that findings
// recorded by user-defined rule packs pass ValidateFinding. It is safe to
// call before any concurrent use of ValidateFinding (e.g. at startup in
// mergeRulePacks). Calling it after the MCP server has started is a data
// race and must be avoided.
func RegisterCategory(category string) {
	ValidCategories[strings.ToLower(strings.TrimSpace(category))] = true
}

// sortedValidCategories returns the valid category names in sorted order
// for use in error messages. Recomputed each call so it reflects any
// categories registered after init.
func sortedValidCategoriesStr() string {
	keys := make([]string, 0, len(ValidCategories))
	for k := range ValidCategories {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// Field length caps. Findings that exceed these limits bloat todos.md and
// usually indicate the model dumped an entire transcript into one field.
// Each cap is chosen to be comfortably larger than any legitimate use.
const (
	maxMistakeLen       = 1024
	maxConfigSnippetLen = 8 * 1024
	maxApplySnippetLen  = 8 * 1024
	maxEvidenceEntries  = 32
	maxEvidenceFieldLen = 512
)

// validationError marks a finding-rejection error so the MCP tool handler
// can route it back to the model via RecordResult.Error (instead of
// escalating to a protocol-level error the model never sees).
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func newValidationError(format string, args ...any) error {
	return &validationError{msg: fmt.Sprintf(format, args...)}
}

// IsValidationError reports whether err is a finding-validation error
// produced by ValidateFinding / Record. Used by the MCP server to decide
// whether to surface the error as a Go error (transport fault) or as
// RecordResult.Error (model-correctable fault).
func IsValidationError(err error) bool {
	var ve *validationError
	return errors.As(err, &ve)
}

// FindingSanitizer is an optional hook called on every FindingInput before
// it is written to the JSONL file. The pipeline wires the project's
// redactor here so the model cannot exfiltrate secrets it observed in the
// transcript by echoing them back into a finding field. nil = no-op.
type FindingSanitizer func(*FindingInput)

// FindingInput is the wire format accepted by both the MCP tool and the CLI.
type FindingInput struct {
	Category         string          `json:"category"          jsonschema:"the rule category (lint-rule, test, ci-check, doc, config, refactor-boundary)"`
	Mistake          string          `json:"mistake"           jsonschema:"one sentence describing the mistake the developer made"`
	Guardrail        GuardrailInput  `json:"guardrail"         jsonschema:"the guardrail to prevent this mistake from recurring"`
	CodebaseEvidence []EvidenceInput `json:"codebase_evidence" jsonschema:"evidence from the codebase that supports this finding"`
	Confidence       float64         `json:"confidence"        jsonschema:"confidence score between 0.0 and 1.0"`
}

// GuardrailInput describes a guardrail to prevent a mistake.
type GuardrailInput struct {
	Kind          string      `json:"kind"                     jsonschema:"guardrail kind matching the category"`
	Tool          string      `json:"tool"                     jsonschema:"tool name e.g. golangci-lint, go test"`
	Rule          string      `json:"rule"                     jsonschema:"specific rule or test name"`
	ConfigSnippet string      `json:"config_snippet,omitempty" jsonschema:"optional config snippet for the guardrail"`
	Apply         *ApplyInput `json:"apply,omitempty"          jsonschema:"optional apply spec for the web UI"`
}

// ApplyInput describes how the web UI can write a guardrail to disk.
type ApplyInput struct {
	TargetFile string `json:"target_file"          jsonschema:"target file path"`
	Strategy   string `json:"strategy"             jsonschema:"apply strategy (append-section, replace-section, etc.)"`
	Anchor     string `json:"anchor,omitempty"     jsonschema:"optional anchor for insertion"`
	Snippet    string `json:"snippet"              jsonschema:"the code/config snippet to apply"`
}

// EvidenceInput is one entry in a finding's evidence array.
type EvidenceInput struct {
	Path   string `json:"path"            jsonschema:"file path relative to project root"`
	Lines  string `json:"lines,omitempty" jsonschema:"line range e.g. 1-10"`
	Symbol string `json:"symbol,omitempty" jsonschema:"relevant symbol name"`
}

// RecordResult is the response from the record_finding tool / CLI.
type RecordResult struct {
	OK    bool   `json:"ok"`
	Total int    `json:"total"`
	Error string `json:"error,omitempty"`
}

// normalizeFinding trims whitespace on every string field and lowercases
// the category so case-only differences ("Test" vs "test") collapse before
// the registry lookup in ValidateFinding.
func normalizeFinding(f *FindingInput) {
	f.Category = strings.ToLower(strings.TrimSpace(f.Category))
	f.Mistake = strings.TrimSpace(f.Mistake)
	f.Guardrail.Kind = strings.TrimSpace(f.Guardrail.Kind)
	f.Guardrail.Tool = strings.TrimSpace(f.Guardrail.Tool)
	f.Guardrail.Rule = strings.TrimSpace(f.Guardrail.Rule)
	f.Guardrail.ConfigSnippet = strings.TrimSpace(f.Guardrail.ConfigSnippet)
	if f.Guardrail.Apply != nil {
		f.Guardrail.Apply.TargetFile = strings.TrimSpace(f.Guardrail.Apply.TargetFile)
		f.Guardrail.Apply.Strategy = strings.TrimSpace(f.Guardrail.Apply.Strategy)
		f.Guardrail.Apply.Anchor = strings.TrimSpace(f.Guardrail.Apply.Anchor)
		f.Guardrail.Apply.Snippet = strings.TrimSpace(f.Guardrail.Apply.Snippet)
	}
	for i := range f.CodebaseEvidence {
		f.CodebaseEvidence[i].Path = strings.TrimSpace(f.CodebaseEvidence[i].Path)
		f.CodebaseEvidence[i].Lines = strings.TrimSpace(f.CodebaseEvidence[i].Lines)
		f.CodebaseEvidence[i].Symbol = strings.TrimSpace(f.CodebaseEvidence[i].Symbol)
	}
}

// ValidateFinding checks a FindingInput for correctness. Returns a
// validation-tagged error (see IsValidationError) describing what's wrong,
// or nil if valid. Does not mutate f — call normalizeFinding first if
// trimming is needed.
func ValidateFinding(f *FindingInput) error {
	if f.Category == "" {
		return newValidationError("category is required")
	}
	if !ValidCategories[f.Category] {
		return newValidationError("invalid category %q (valid: %s)", f.Category, sortedValidCategoriesStr())
	}
	if f.Mistake == "" {
		return newValidationError("mistake is required")
	}
	if utf8.RuneCountInString(f.Mistake) > maxMistakeLen {
		return newValidationError("mistake exceeds %d runes", maxMistakeLen)
	}
	if math.IsNaN(f.Confidence) || math.IsInf(f.Confidence, 0) || f.Confidence < 0 || f.Confidence > 1 {
		return newValidationError("confidence must be between 0.0 and 1.0, got %v", f.Confidence)
	}
	if f.Guardrail.Kind == "" {
		return newValidationError("guardrail.kind is required")
	}
	if f.Guardrail.Tool == "" {
		return newValidationError("guardrail.tool is required")
	}
	if f.Guardrail.Rule == "" {
		return newValidationError("guardrail.rule is required")
	}
	if utf8.RuneCountInString(f.Guardrail.ConfigSnippet) > maxConfigSnippetLen {
		return newValidationError("guardrail.config_snippet exceeds %d runes", maxConfigSnippetLen)
	}
	if f.Guardrail.Apply != nil && utf8.RuneCountInString(f.Guardrail.Apply.Snippet) > maxApplySnippetLen {
		return newValidationError("guardrail.apply.snippet exceeds %d runes", maxApplySnippetLen)
	}
	if len(f.CodebaseEvidence) > maxEvidenceEntries {
		return newValidationError("codebase_evidence has %d entries, max %d", len(f.CodebaseEvidence), maxEvidenceEntries)
	}
	for i, e := range f.CodebaseEvidence {
		if utf8.RuneCountInString(e.Path) > maxEvidenceFieldLen ||
			utf8.RuneCountInString(e.Lines) > maxEvidenceFieldLen ||
			utf8.RuneCountInString(e.Symbol) > maxEvidenceFieldLen {
			return newValidationError("codebase_evidence[%d] field exceeds %d runes", i, maxEvidenceFieldLen)
		}
	}
	return nil
}

// FindingRecorder accumulates validated findings into a JSONL file.
// Thread-safe for concurrent writes within a single process. Cross-process
// safety is NOT provided — the MCP server runs as a single child, and the
// CLI tool path serializes calls via gemini-cli's sequential Bash
// invocations. If a future use case introduces parallel writers from
// separate processes, switch this to per-record files or add file locking.
//
// WARNING: Do not share a FindingRecorder across goroutines in separate
// processes (e.g., if the MCP server is later refactored for concurrent
// tool calls). The mutex protects in-process concurrency only.
type FindingRecorder struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	count    int
	sanitize FindingSanitizer
}

// ValidateOutputPath checks that the given path is safe for writing findings.
// The path must be absolute and live under os.TempDir(). Symlinks are rejected
// anywhere in the ancestor chain. This prevents prompt-injected transcripts
// from targeting arbitrary files (e.g. ~/.ssh/authorized_keys) via the --output flag.
func ValidateOutputPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("output path must be absolute, got %q", path)
	}
	tmpDir, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return fmt.Errorf("resolve temp dir: %w", err)
	}
	tmpDir = filepath.Clean(tmpDir)

	cleaned := filepath.Clean(path)
	resolved, err := fsutil.ResolveSymlinks(cleaned)
	if err != nil {
		return fmt.Errorf("resolve output path %q: %w", path, err)
	}
	if !fsutil.PathWithinRoot(resolved, tmpDir) {
		return fmt.Errorf("output path %q resolves to %q, outside temp directory %q", path, resolved, tmpDir)
	}
	return nil
}

// NewFindingRecorder creates a recorder that appends to the given path.
// File mode 0600 — findings may contain transcript snippets the redactor
// missed, so they're treated as sensitive even though the file lives under
// os.TempDir().
func NewFindingRecorder(path string) (*FindingRecorder, error) {
	if err := ValidateOutputPath(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open findings file %q: %w", path, err)
	}
	return &FindingRecorder{path: path, file: f}, nil
}

// WithSanitizer installs a hook called on every FindingInput between
// normalization and write. Callers wire the project's redactor here so a
// model that echoes a transcript secret into a finding field cannot persist
// it to disk.
func (r *FindingRecorder) WithSanitizer(s FindingSanitizer) *FindingRecorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sanitize = s
	return r
}

// Record normalizes, validates, sanitizes, and writes one finding. Returns
// (total, error). Validation errors are tagged so the MCP handler can route
// them back to the model; write errors are plain errors that escalate.
func (r *FindingRecorder) Record(f *FindingInput) (int, error) {
	normalizeFinding(f)
	if err := ValidateFinding(f); err != nil {
		return 0, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file == nil {
		return 0, fmt.Errorf("recorder is closed")
	}
	if r.sanitize != nil {
		r.sanitize(f)
	}

	line, err := json.Marshal(f)
	if err != nil {
		return 0, fmt.Errorf("marshal finding: %w", err)
	}
	if _, err := r.file.Write(append(line, '\n')); err != nil {
		return 0, fmt.Errorf("write finding: %w", err)
	}
	r.count++
	return r.count, nil
}

// Count returns the number of recorded findings.
func (r *FindingRecorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

// Close closes the underlying file. Safe to call multiple times.
func (r *FindingRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file != nil {
		err := r.file.Close()
		r.file = nil
		return err
	}
	return nil
}

// ReadFindingsJSONL reads all findings from a JSONL file.
func ReadFindingsJSONL(path string) ([]FindingInput, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open findings file %q: %w", path, err)
	}
	defer f.Close()
	return ReadFindingsFromReader(f)
}

// ReadFindingsFromReader reads findings from any reader.
// Returns nil, nil if the reader is empty. Returns nil, err on parse failure.
func ReadFindingsFromReader(r io.Reader) ([]FindingInput, error) {
	var findings []FindingInput
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max line
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var f FindingInput
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			return nil, fmt.Errorf("parse finding line: %w", err)
		}
		findings = append(findings, f)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read findings: %w", err)
	}
	return findings, nil
}
