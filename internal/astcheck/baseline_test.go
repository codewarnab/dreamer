package astcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func TestFindingKey(t *testing.T) {
	key := FindingKey("nodirectlog", "myFunc", "direct use of log.Printf")
	want := "nodirectlog:myFunc:direct use of log.Printf"
	if key != want {
		t.Errorf("FindingKey = %q, want %q", key, want)
	}
}

func TestFindingKeyMethod(t *testing.T) {
	key := FindingKey("nodirectlog", "Server.Start", "direct use of log.Printf")
	want := "nodirectlog:Server.Start:direct use of log.Printf"
	if key != want {
		t.Errorf("FindingKey = %q, want %q", key, want)
	}
}

func TestBaselineRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".quality-baseline.json")

	findings := []Finding{
		{Check: "nodirectlog", Symbol: "main", Message: "direct use of log.Printf"},
		{Check: "settesthome", Symbol: "setTestHome", Message: "missing HOME"},
	}

	// Write baseline.
	if err := WriteBaseline(path, findings); err != nil {
		t.Fatalf("WriteBaseline: %v", err)
	}

	// Read back.
	keys, err := ReadBaseline(path)
	if err != nil {
		t.Fatalf("ReadBaseline: %v", err)
	}

	if len(keys) != 2 {
		t.Fatalf("ReadBaseline returned %d keys, want 2", len(keys))
	}
	for _, f := range findings {
		if !keys[f.Key()] {
			t.Errorf("missing key %q in baseline", f.Key())
		}
	}

	// Subtract: same findings should be fully subtracted.
	remaining := SubtractBaseline(findings, keys)
	if len(remaining) != 0 {
		t.Errorf("SubtractBaseline returned %d findings, want 0", len(remaining))
	}

	// Subtract: new finding should survive.
	newFinding := Finding{Check: "nodirectlog", Symbol: "other", Message: "direct use of log.Fatal"}
	remaining = SubtractBaseline([]Finding{newFinding}, keys)
	if len(remaining) != 1 {
		t.Errorf("SubtractBaseline with new finding returned %d, want 1", len(remaining))
	}
}

func TestReadBaselineNotExist(t *testing.T) {
	keys, err := ReadBaseline("/nonexistent/.quality-baseline.json")
	if err != nil {
		t.Fatalf("ReadBaseline on missing file: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected empty keys, got %d", len(keys))
	}
}

func TestReadBaselineBadVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte(`{"version":99,"findings":{}}`), 0o644)

	_, err := ReadBaseline(path)
	if err == nil {
		t.Fatal("expected error for bad version")
	}
}

func TestSubtractBaselineEmpty(t *testing.T) {
	findings := []Finding{
		{Check: "a", Symbol: "b", Message: "c"},
	}
	// Empty baseline should pass through all findings.
	remaining := SubtractBaseline(findings, nil)
	if len(remaining) != 1 {
		t.Errorf("nil baseline: got %d, want 1", len(remaining))
	}
	remaining = SubtractBaseline(findings, make(map[string]bool))
	if len(remaining) != 1 {
		t.Errorf("empty baseline: got %d, want 1", len(remaining))
	}
}

func TestEnclosingSymbol(t *testing.T) {
	src := `package main

import "log"

func main() {
	log.Printf("hello")
}

type Server struct{}

func (s *Server) Start() {
	log.Fatal("oops")
}

func init() {
	log.Println("init")
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	tests := []struct {
		line int
		want string
	}{
		{6, "main"},          // log.Printf in main()
		{12, "Server.Start"}, // log.Fatal in (s *Server).Start()
		{16, "init"},         // log.Println in init()
	}

	for _, tt := range tests {
		// Find the token position for the given line.
		var pos token.Pos
		for _, comment := range file.Comments {
			// skip
			_ = comment
		}
		// Walk the file to find a node on this line.
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				return false
			}
			if fset.Position(n.Pos()).Line == tt.line {
				if pos == token.NoPos {
					pos = n.Pos()
				}
			}
			return true
		})
		if pos == token.NoPos {
			t.Fatalf("no node found on line %d", tt.line)
			continue
		}
		got := EnclosingSymbol(fset, file, pos)
		if got != tt.want {
			t.Errorf("line %d: EnclosingSymbol = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestComputeSuppressedLines(t *testing.T) {
	src := `package main

import "log"

func main() {
	log.Printf("hello") //astcheck:ignore
	x := 1
	log.Fatal("bye") //astcheck:ignore[nodirectlog] // because reasons
	_ = x
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	suppressed := computeSuppressedLines(fset, file)

	// Line 6: //astcheck:ignore (blanket) — should suppress both line 6 and 7.
	if names, ok := suppressed[6]; !ok || len(names) == 0 {
		t.Error("line 6 should be suppressed")
	}

	// Line 9: //astcheck:ignore[nodirectlog] — should suppress line 9 and 10.
	if names, ok := suppressed[9]; !ok {
		t.Error("line 9 should be suppressed")
	} else {
		found := false
		for _, n := range names {
			if n == "nodirectlog" {
				found = true
			}
		}
		if !found {
			t.Errorf("line 9 should have nodirectlog suppression, got %v", names)
		}
	}
}

func TestIsSuppressed(t *testing.T) {
	suppressed := map[int][]string{
		6:  {""},            // blanket
		9:  {"nodirectlog"}, // specific
		10: {"settesthome"}, // different check
	}

	tests := []struct {
		line  int
		check string
		want  bool
	}{
		{6, "nodirectlog", true},   // blanket matches any
		{6, "settesthome", true},   // blanket matches any
		{9, "nodirectlog", true},   // specific match
		{9, "settesthome", false},  // specific, different check
		{10, "settesthome", true},  // specific match
		{10, "nodirectlog", false}, // specific, different check
		{99, "nodirectlog", false}, // not suppressed
	}

	for _, tt := range tests {
		got := isSuppressed(tt.line, tt.check, suppressed)
		if got != tt.want {
			t.Errorf("isSuppressed(%d, %q) = %v, want %v", tt.line, tt.check, got, tt.want)
		}
	}
}

func TestPositionStableKeyAcrossEdits(t *testing.T) {
	// The key should be the same regardless of line number.
	f1 := Finding{
		Pos:      token.Position{Filename: "test.go", Line: 10},
		Check:    "nodirectlog",
		Severity: SevError,
		Symbol:   "main",
		Message:  "direct use of log.Printf",
	}
	f2 := Finding{
		Pos:      token.Position{Filename: "test.go", Line: 42}, // different line
		Check:    "nodirectlog",
		Severity: SevError,
		Symbol:   "main",
		Message:  "direct use of log.Printf",
	}
	if f1.Key() != f2.Key() {
		t.Errorf("keys differ across line numbers: %q vs %q", f1.Key(), f2.Key())
	}
}
