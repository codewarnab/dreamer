package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestMissingFlagError(t *testing.T) {
	cmd := &cobra.Command{Use: "analyze"}
	err := missingFlagError(cmd, "path", "The project directory to analyze.", "dreamer analyze --path /my/project")

	s := err.Error()
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
}

func TestMissingArgError(t *testing.T) {
	cmd := &cobra.Command{Use: "jobs show"}
	err := missingArgError(cmd, "job-id", "The ID of the job to show.", "jobs show abc123")

	s := err.Error()
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
}

func TestInvalidFlagValueError(t *testing.T) {
	cmd := &cobra.Command{Use: "jobs create"}
	err := invalidFlagValueError(cmd, "schedule", "hourly", []string{"interval", "daily", "weekly", "cron"})

	s := err.Error()
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
}

func TestMissingFlagError_EmptyDescription(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	err := missingFlagError(cmd, "output", "", "test --output /path")

	s := err.Error()
	if !strings.Contains(s, "--output") {
		t.Errorf("error output missing flag name\n got: %s", s)
	}
	// Should not have empty description line.
	if strings.Contains(s, "\n\n\n") {
		t.Errorf("error output has empty description line\n got: %s", s)
	}
}
