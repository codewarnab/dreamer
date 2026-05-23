// Package mcpserver implements the MCP server for Phase 2 finding recording
// and shared validation/JSONL logic used by both the MCP server and the
// record-finding CLI tool.
package mcpserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// ValidCategories is the set of valid rule categories.
var ValidCategories = map[string]bool{
	"lint-rule":         true,
	"test":              true,
	"ci-check":          true,
	"doc":               true,
	"config":            true,
	"refactor-boundary": true,
}

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

// normalizeFinding trims whitespace on all string fields.
func normalizeFinding(f *FindingInput) {
	f.Category = strings.TrimSpace(f.Category)
	f.Mistake = strings.TrimSpace(f.Mistake)
	f.Guardrail.Kind = strings.TrimSpace(f.Guardrail.Kind)
	f.Guardrail.Tool = strings.TrimSpace(f.Guardrail.Tool)
	f.Guardrail.Rule = strings.TrimSpace(f.Guardrail.Rule)
	f.Guardrail.ConfigSnippet = strings.TrimSpace(f.Guardrail.ConfigSnippet)
}

// ValidateFinding checks a FindingInput for correctness.
// Returns an error describing what's wrong, or nil if valid.
// Does not mutate f — call normalizeFinding first if trimming is needed.
func ValidateFinding(f *FindingInput) error {
	if f.Category == "" {
		return fmt.Errorf("category is required")
	}
	if !ValidCategories[f.Category] {
		return fmt.Errorf("invalid category %q (valid: lint-rule, test, ci-check, doc, config, refactor-boundary)", f.Category)
	}
	if f.Mistake == "" {
		return fmt.Errorf("mistake is required")
	}
	if f.Confidence < 0 || f.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0.0 and 1.0, got %v", f.Confidence)
	}
	if f.Guardrail.Kind == "" {
		return fmt.Errorf("guardrail.kind is required")
	}
	return nil
}

// FindingRecorder accumulates validated findings into a JSONL file.
// Thread-safe for concurrent writes.
type FindingRecorder struct {
	mu    sync.Mutex
	path  string
	file  *os.File
	count int
}

// NewFindingRecorder creates a recorder that appends to the given path.
func NewFindingRecorder(path string) (*FindingRecorder, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open findings file %q: %w", path, err)
	}
	return &FindingRecorder{path: path, file: f}, nil
}

// Record normalizes, validates, and writes one finding. Returns (total, error).
func (r *FindingRecorder) Record(f *FindingInput) (int, error) {
	normalizeFinding(f)
	if err := ValidateFinding(f); err != nil {
		return 0, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

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
