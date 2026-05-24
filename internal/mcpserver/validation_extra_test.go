package mcpserver

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestValidateFinding_Table exercises the per-field validation rules with a
// single table — keeps every rejected shape in one place where a future
// reviewer can scan invariants at a glance.
func TestValidateFinding_Table(t *testing.T) {
	base := func() FindingInput {
		return FindingInput{
			Category:   "test",
			Mistake:    "missing edge-case test",
			Confidence: 0.9,
			Guardrail:  GuardrailInput{Kind: "test", Tool: "go test", Rule: "TestEdgeCases"},
		}
	}

	cases := []struct {
		name      string
		mutate    func(*FindingInput)
		wantErrIn string // substring that must appear in error; empty = expect success
	}{
		{"valid", func(*FindingInput) {}, ""},
		{"empty category", func(f *FindingInput) { f.Category = "" }, "category"},
		{"unknown category", func(f *FindingInput) { f.Category = "nonexistent" }, "invalid category"},
		{"category mixed case", func(f *FindingInput) { f.Category = "Test" }, ""}, // normalize lowercases
		{"category with surrounding space", func(f *FindingInput) { f.Category = "  test  " }, ""},
		{"empty mistake", func(f *FindingInput) { f.Mistake = "" }, "mistake is required"},
		{"whitespace-only mistake", func(f *FindingInput) { f.Mistake = "   " }, "mistake is required"},
		{"mistake too long", func(f *FindingInput) { f.Mistake = strings.Repeat("x", maxMistakeLen+1) }, "exceeds"},
		{"confidence negative", func(f *FindingInput) { f.Confidence = -0.1 }, "confidence"},
		{"confidence over one", func(f *FindingInput) { f.Confidence = 1.1 }, "confidence"},
		{"confidence NaN", func(f *FindingInput) { f.Confidence = math.NaN() }, "confidence"},
		{"confidence +Inf", func(f *FindingInput) { f.Confidence = math.Inf(1) }, "confidence"},
		{"empty guardrail kind", func(f *FindingInput) { f.Guardrail.Kind = "" }, "guardrail.kind"},
		{"empty guardrail tool", func(f *FindingInput) { f.Guardrail.Tool = "" }, "guardrail.tool"},
		{"empty guardrail rule", func(f *FindingInput) { f.Guardrail.Rule = "" }, "guardrail.rule"},
		{"config snippet too long", func(f *FindingInput) { f.Guardrail.ConfigSnippet = strings.Repeat("x", maxConfigSnippetLen+1) }, "config_snippet"},
		{"too many evidence entries", func(f *FindingInput) {
			f.CodebaseEvidence = make([]EvidenceInput, maxEvidenceEntries+1)
		}, "codebase_evidence has"},
		{"evidence path too long", func(f *FindingInput) {
			f.CodebaseEvidence = []EvidenceInput{{Path: strings.Repeat("x", maxEvidenceFieldLen+1)}}
		}, "field exceeds"},
		{"apply snippet too long", func(f *FindingInput) {
			f.Guardrail.Apply = &ApplyInput{TargetFile: "x", Strategy: "append-section", Snippet: strings.Repeat("x", maxApplySnippetLen+1)}
		}, "apply.snippet exceeds"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := base()
			c.mutate(&f)
			normalizeFinding(&f)
			err := ValidateFinding(&f)
			if c.wantErrIn == "" {
				if err != nil {
					t.Fatalf("want valid, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", c.wantErrIn)
			}
			if !strings.Contains(err.Error(), c.wantErrIn) {
				t.Fatalf("want error containing %q, got %q", c.wantErrIn, err.Error())
			}
			if !IsValidationError(err) {
				t.Fatalf("want IsValidationError(err)==true, got false (err=%v)", err)
			}
		})
	}
}

// TestNormalizeFinding_TrimsEvidenceAndApply guards against a regression
// where evidence / apply fields kept their surrounding whitespace and broke
// downstream path containment checks in the web/apply layer.
func TestNormalizeFinding_TrimsEvidenceAndApply(t *testing.T) {
	f := FindingInput{
		Category:   "test",
		Mistake:    " mistake ",
		Confidence: 0.5,
		Guardrail: GuardrailInput{
			Kind: " test ", Tool: " go test ", Rule: " r ",
			Apply: &ApplyInput{TargetFile: " a ", Strategy: " s ", Anchor: " an ", Snippet: " sn "},
		},
		CodebaseEvidence: []EvidenceInput{{Path: " p ", Lines: " 1-2 ", Symbol: " sym "}},
	}
	normalizeFinding(&f)
	if f.Mistake != "mistake" || f.Guardrail.Kind != "test" {
		t.Fatalf("trim failed on top-level fields: %+v", f)
	}
	if f.Guardrail.Apply.TargetFile != "a" || f.Guardrail.Apply.Snippet != "sn" {
		t.Fatalf("trim failed on apply fields: %+v", f.Guardrail.Apply)
	}
	if f.CodebaseEvidence[0].Path != "p" || f.CodebaseEvidence[0].Symbol != "sym" {
		t.Fatalf("trim failed on evidence: %+v", f.CodebaseEvidence[0])
	}
}

// TestFindingRecorder_ConcurrentRecord asserts the in-process mutex
// serializes writes correctly under `-race`. Verifies count and that every
// finding survived as a valid JSONL line.
func TestFindingRecorder_ConcurrentRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.jsonl")
	rec, err := NewFindingRecorder(path)
	if err != nil {
		t.Fatalf("NewFindingRecorder: %v", err)
	}
	defer rec.Close()

	const writers = 50
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			f := FindingInput{
				Category:   "test",
				Mistake:    "concurrent",
				Confidence: 0.5,
				Guardrail:  GuardrailInput{Kind: "test", Tool: "go test", Rule: "TestX"},
			}
			if _, err := rec.Record(&f); err != nil {
				t.Errorf("Record: %v", err)
			}
		}()
	}
	wg.Wait()
	if rec.Count() != writers {
		t.Fatalf("expected %d findings, got %d", writers, rec.Count())
	}

	readBack, err := ReadFindingsJSONL(path)
	if err != nil {
		t.Fatalf("ReadFindingsJSONL: %v", err)
	}
	if len(readBack) != writers {
		t.Fatalf("expected %d findings on disk, got %d", writers, len(readBack))
	}
}

// TestFindingRecorder_AfterClose verifies Record fails cleanly (no panic)
// after Close — guards against the nil *os.File deref the previous code
// would have hit.
func TestFindingRecorder_AfterClose(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewFindingRecorder(filepath.Join(dir, "f.jsonl"))
	if err != nil {
		t.Fatalf("NewFindingRecorder: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("idempotent Close should not error: %v", err)
	}
	f := FindingInput{Category: "test", Mistake: "x", Confidence: 0.5, Guardrail: GuardrailInput{Kind: "test", Tool: "t", Rule: "r"}}
	if _, err := rec.Record(&f); err == nil {
		t.Fatal("expected Record after Close to error")
	}
}

// TestFindingRecorder_Sanitizer verifies the redaction hook runs between
// validation and write — so secrets the model echoed into a finding never
// reach disk.
func TestFindingRecorder_Sanitizer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.jsonl")
	rec, err := NewFindingRecorder(path)
	if err != nil {
		t.Fatalf("NewFindingRecorder: %v", err)
	}
	defer rec.Close()

	rec.WithSanitizer(func(f *FindingInput) {
		f.Mistake = strings.ReplaceAll(f.Mistake, "sk-secret", "[REDACTED]")
	})

	f := FindingInput{
		Category:   "test",
		Mistake:    "leaked sk-secret in code",
		Confidence: 0.5,
		Guardrail:  GuardrailInput{Kind: "test", Tool: "t", Rule: "r"},
	}
	if _, err := rec.Record(&f); err != nil {
		t.Fatalf("Record: %v", err)
	}

	out, err := ReadFindingsJSONL(path)
	if err != nil {
		t.Fatalf("ReadFindingsJSONL: %v", err)
	}
	if len(out) != 1 || strings.Contains(out[0].Mistake, "sk-secret") {
		t.Fatalf("sanitizer did not redact: %+v", out)
	}
}

// TestBuildClientLaunchSpec asserts the launch spec round-trips through
// json.Unmarshal — path quoting, backslash escaping, and the tool-name
// constant all need to be exact for the spawning provider.
func TestBuildClientLaunchSpec(t *testing.T) {
	cases := []struct{ name, outputPath, binary string }{
		{"unix-style", "/tmp/findings.jsonl", "/usr/local/bin/dreamer"},
		{"windows-style", `C:\Users\User\AppData\Local\Temp\f.jsonl`, `C:\Program Files\dreamer\dreamer.exe`},
		{"path with quote", `/tmp/it's-quoted.jsonl`, "/usr/local/bin/dreamer"},
		{"unicode path", `/tmp/üñïçødé.jsonl`, `/usr/local/bin/dreamer`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec, err := BuildClientLaunchSpec(c.outputPath, c.binary)
			if err != nil {
				t.Fatalf("BuildClientLaunchSpec: %v", err)
			}
			defer os.Remove(spec.ConfigFilePath)
			if len(spec.ToolNames) != 1 || spec.ToolNames[0] != PrefixedToolName {
				t.Fatalf("unexpected tool names: %v", spec.ToolNames)
			}
			if spec.ConfigFilePath == "" {
				t.Fatal("ConfigFilePath is empty")
			}
			// Validate the temp file exists and its JSON parses back to the inputs.
			raw, readErr := os.ReadFile(spec.ConfigFilePath)
			if readErr != nil {
				t.Fatalf("read config temp file: %v", readErr)
			}
			parsed := struct {
				MCPServers struct {
					Dreamer struct {
						Command string   `json:"command"`
						Args    []string `json:"args"`
					} `json:"dreamer"`
				} `json:"mcpServers"`
			}{}
			if err := json.Unmarshal(raw, &parsed); err != nil {
				t.Fatalf("unmarshal config file: %v", err)
			}
			if parsed.MCPServers.Dreamer.Command != c.binary {
				t.Errorf("command: got %q want %q", parsed.MCPServers.Dreamer.Command, c.binary)
			}
			wantArgs := []string{"mcp-server", "--output", c.outputPath}
			if len(parsed.MCPServers.Dreamer.Args) != len(wantArgs) {
				t.Fatalf("args length: got %d want %d; args: %v", len(parsed.MCPServers.Dreamer.Args), len(wantArgs), parsed.MCPServers.Dreamer.Args)
			}
			for i, want := range wantArgs {
				if parsed.MCPServers.Dreamer.Args[i] != want {
					t.Errorf("args[%d]: got %q want %q", i, parsed.MCPServers.Dreamer.Args[i], want)
				}
			}
		})
	}
}

func TestBuildClientLaunchSpec_RejectsEmpty(t *testing.T) {
	if _, err := BuildClientLaunchSpec("", "dreamer"); err == nil {
		t.Fatal("expected error for empty outputPath")
	}
	if _, err := BuildClientLaunchSpec("/tmp/x", ""); err == nil {
		t.Fatal("expected error for empty binary path")
	}
}
