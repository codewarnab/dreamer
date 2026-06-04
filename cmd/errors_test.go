package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// captureStderr creates a cobra.Command wired to a buffer, calls fn(cmd),
// and returns the buffer contents so tests can assert on styled output.
func captureStderr(fn func(cmd *cobra.Command) error) (string, error) {
	cmd := &cobra.Command{Use: "test"}
	var buf bytes.Buffer
	cmd.SetErr(&buf)
	err := fn(cmd)
	return buf.String(), err
}

func TestMissingFlagError(t *testing.T) {
	// Test the pure format function — no I/O, testable directly.
	cmd := &cobra.Command{Use: "analyze"}
	s := formatMissingFlag(cmd, "path", "The project directory to analyze.", "dreamer analyze --path /my/project")

	if len(s) == 0 {
		t.Fatal("rendered error is empty")
	}
	for _, want := range []string{
		"Missing required flag",
		"--path",
		"The project directory to analyze.",
		"Usage:",
		"dreamer analyze --path /my/project",
		"--help",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("error output missing %q\n got: %s", want, s)
		}
	}
	if !strings.Contains(s, "\n") {
		t.Error("expected multi-line output from styled error")
	}

	// The imperative shell must print to stderr and return alreadyPrintedError.
	stderr, err := captureStderr(func(cmd *cobra.Command) error {
		return missingFlagError(cmd, "path", "The project directory to analyze.", "dreamer analyze --path /my/project")
	})
	if err == nil {
		t.Fatal("missingFlagError must return a non-nil error")
	}
	var already *alreadyPrintedError
	if !isAlreadyPrinted(err, &already) {
		t.Errorf("missingFlagError must return alreadyPrintedError, got %T", err)
	}
	if !strings.Contains(stderr, "Missing required flag") {
		t.Errorf("stderr missing styled output, got: %s", stderr)
	}
}

func TestMissingArgError(t *testing.T) {
	cmd := &cobra.Command{Use: "jobs show"}
	s := formatMissingArg(cmd, "job-id", "The ID of the job to show.", "jobs show abc123")

	if len(s) == 0 {
		t.Fatal("rendered error is empty")
	}
	for _, want := range []string{
		"Missing required argument",
		"job-id",
		"The ID of the job to show.",
		"Usage:",
		"jobs show abc123",
		"--help",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("error output missing %q\n got: %s", want, s)
		}
	}
	if !strings.Contains(s, "\n") {
		t.Error("expected multi-line output from styled error")
	}

	// Shell returns alreadyPrintedError and prints to stderr.
	stderr, err := captureStderr(func(cmd *cobra.Command) error {
		return missingArgError(cmd, "job-id", "The ID of the job to show.", "jobs show abc123")
	})
	if err == nil {
		t.Fatal("missingArgError must return a non-nil error")
	}
	var already *alreadyPrintedError
	if !isAlreadyPrinted(err, &already) {
		t.Errorf("missingArgError must return alreadyPrintedError, got %T", err)
	}
	if !strings.Contains(stderr, "Missing required argument") {
		t.Errorf("stderr missing styled output, got: %s", stderr)
	}
}

func TestInvalidFlagValueError(t *testing.T) {
	cmd := &cobra.Command{Use: "jobs create"}
	s := formatInvalidFlagValue(cmd, "schedule", "hourly", []string{"interval", "daily", "weekly", "cron"})

	if len(s) == 0 {
		t.Fatal("rendered error is empty")
	}
	for _, want := range []string{
		"Invalid value for",
		"--schedule",
		"hourly",
		"interval, daily, weekly, cron",
		"--help",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("error output missing %q\n got: %s", want, s)
		}
	}
	if !strings.Contains(s, "\n") {
		t.Error("expected multi-line output from styled error")
	}

	// Shell returns alreadyPrintedError and prints to stderr.
	stderr, err := captureStderr(func(cmd *cobra.Command) error {
		return invalidFlagValueError(cmd, "schedule", "hourly", []string{"interval", "daily", "weekly", "cron"})
	})
	if err == nil {
		t.Fatal("invalidFlagValueError must return a non-nil error")
	}
	var already *alreadyPrintedError
	if !isAlreadyPrinted(err, &already) {
		t.Errorf("invalidFlagValueError must return alreadyPrintedError, got %T", err)
	}
	if !strings.Contains(stderr, "Invalid value for") {
		t.Errorf("stderr missing styled output, got: %s", stderr)
	}
}

func TestMissingFlagError_EmptyDescription(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	s := formatMissingFlag(cmd, "output", "", "test --output /path")

	if !strings.Contains(s, "--output") {
		t.Errorf("error output missing flag name\n got: %s", s)
	}
	// Should not have empty description line creating triple newline.
	if strings.Contains(s, "\n\n\n") {
		t.Errorf("error output has extra blank line from empty description\n got: %s", s)
	}
}

// isAlreadyPrinted is a helper because errors.As needs a pointer-to-pointer.
func isAlreadyPrinted(err error, target **alreadyPrintedError) bool {
	if e, ok := err.(*alreadyPrintedError); ok {
		*target = e
		return true
	}
	return false
}
