package cmd

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestVersionReturnsBuildVariable(t *testing.T) {
	got := Version()
	if got != version {
		t.Errorf("Version() = %q, want %q", got, version)
	}
}

// TestRootCommandVersionFlag ensures `dreamer --version` works via the
// built-in cobra flag and matches the `version` subcommand's plain output.
func TestRootCommandVersionFlag(t *testing.T) {
	stdout, _, err := executeRootCommand("--version")
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	if got, want := strings.TrimSpace(stdout), "dreamer "+version; got != want {
		t.Errorf("--version output = %q, want %q", got, want)
	}
}

func TestVersionCommandOutput(t *testing.T) {
	cmd := newVersionCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "dreamer") {
		t.Errorf("output missing 'dreamer': %q", out)
	}
	if !strings.Contains(out, version) {
		t.Errorf("output missing version %q: %q", version, out)
	}
}

func TestVersionCommandVerboseOutput(t *testing.T) {
	cmd := newVersionCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	// Set --verbose flag.
	if err := cmd.Flags().Set("verbose", "true"); err != nil {
		t.Fatalf("set verbose: %v", err)
	}

	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	out := buf.String()
	wantContains := []string{"dreamer", "commit:", "built:", "go:", "os/arch:"}
	for _, want := range wantContains {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q:\n%s", want, out)
		}
	}
	// Verify actual Go version and os/arch are present.
	if !strings.Contains(out, runtime.Version()) {
		t.Errorf("verbose output missing Go version %q", runtime.Version())
	}
	if !strings.Contains(out, runtime.GOOS) {
		t.Errorf("verbose output missing GOOS %q", runtime.GOOS)
	}
}
