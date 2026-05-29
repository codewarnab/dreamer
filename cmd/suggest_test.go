package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "b", 1},
		{"kitten", "kitten", 0},
		{"kitten", "sitting", 3},
		{"analyze", "analyez", 2},
		{"jobs", "shw", 4},
		{"show", "shw", 1},
		{"list", "lsit", 2},
		{"status", "stauts", 2},
	}
	for _, tt := range tests {
		got := levenshtein(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSuggestCommand_ExactMatch(t *testing.T) {
	cmds := []*cobra.Command{
		{Use: "analyze"},
		{Use: "jobs"},
		{Use: "status"},
	}
	// Should not suggest anything for a valid command.
	if got := suggestCommand("analyze", cmds); got != "" {
		t.Errorf("suggestCommand(%q) = %q, want empty", "analyze", got)
	}
}

func TestSuggestCommand_Typo(t *testing.T) {
	cmds := []*cobra.Command{
		{Use: "analyze"},
		{Use: "jobs"},
		{Use: "status"},
	}
	tests := []struct {
		typed string
		want  string
	}{
		{"analyez", "analyze"},
		{"stauts", "status"},
		{"statu", "status"},
		{"jbos", "jobs"},
	}
	for _, tt := range tests {
		got := suggestCommand(tt.typed, cmds)
		if got != tt.want {
			t.Errorf("suggestCommand(%q) = %q, want %q", tt.typed, got, tt.want)
		}
	}
}

func TestSuggestCommand_HiddenSkipped(t *testing.T) {
	cmds := []*cobra.Command{
		{Use: "analyze"},
		{Use: "secret", Hidden: true},
	}
	// "secrt" should not suggest the hidden command.
	if got := suggestCommand("secrt", cmds); got != "" {
		t.Errorf("suggestCommand should skip hidden commands, got %q", got)
	}
}

func TestSuggestCommand_NoMatch(t *testing.T) {
	cmds := []*cobra.Command{
		{Use: "analyze"},
		{Use: "jobs"},
	}
	// Completely unrelated input should return empty.
	if got := suggestCommand("xyzzy", cmds); got != "" {
		t.Errorf("suggestCommand(%q) = %q, want empty", "xyzzy", got)
	}
}

func TestSuggestFlag_Typo(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("output-root", "", "Output directory")
	flags.Bool("verbose", false, "Verbose output")
	flags.String("config", "", "Config path")

	tests := []struct {
		typed string
		want  string
	}{
		{"outpt", ""},
		{"verbos", "verbose"},
		{"confi", "config"},
	}
	for _, tt := range tests {
		got := suggestFlag(tt.typed, flags)
		if got != tt.want {
			t.Errorf("suggestFlag(%q) = %q, want %q", tt.typed, got, tt.want)
		}
	}
}

func TestSuggestFlag_HiddenSkipped(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	flags.String("secret-flag", "", "")
	_ = flags.MarkHidden("secret-flag")

	if got := suggestFlag("secrt-flag", flags); got != "" {
		t.Errorf("suggestFlag should skip hidden flags, got %q", got)
	}
}

func TestSuggestFromError_UnknownCommand(t *testing.T) {
	// Set up a root with a "jobs" subcommand.
	root := &cobra.Command{Use: "dreamer"}
	jobsCmd := &cobra.Command{Use: "jobs"}
	jobsCmd.AddCommand(&cobra.Command{Use: "show"})
	jobsCmd.AddCommand(&cobra.Command{Use: "list"})
	root.AddCommand(jobsCmd)

	// Temporarily set rootCmd for the test.
	orig := rootCmd
	rootCmd = root
	t.Cleanup(func() { rootCmd = orig })

	// Cobra's error format.
	err := fmt.Errorf(`unknown command "shw" for "dreamer jobs"`)

	if !suggestFromError(err) {
		t.Fatal("suggestFromError should have returned true")
	}
}

func TestSuggestFromError_UnknownCommand_NoMatch(t *testing.T) {
	root := &cobra.Command{Use: "dreamer"}
	root.AddCommand(&cobra.Command{Use: "jobs"})

	orig := rootCmd
	rootCmd = root
	t.Cleanup(func() { rootCmd = orig })

	// Completely unrelated command — no suggestion.
	err := fmt.Errorf(`unknown command "xyzzy" for "dreamer"`)

	if suggestFromError(err) {
		t.Fatal("suggestFromError should have returned false for unrelated input")
	}
}

func TestSuggestFromError_NonCommandError(t *testing.T) {
	// Non-command errors should be ignored.
	err := fmt.Errorf("some other error")
	if suggestFromError(err) {
		t.Fatal("suggestFromError should return false for non-command errors")
	}
}

func TestStyledSuggestionError_ContainsExpectedParts(t *testing.T) {
	s := styledSuggestionError("Unknown command", "analyez", "analyze", "dreamer")

	for _, want := range []string{
		"Unknown command",
		"analyez",
		"Did you mean this?",
		"analyze",
		"--help",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("styledSuggestionError missing %q\n got: %s", want, s)
		}
	}
}

func TestFindCommandByPath(t *testing.T) {
	root := &cobra.Command{Use: "dreamer"}
	jobs := &cobra.Command{Use: "jobs"}
	show := &cobra.Command{Use: "show"}
	jobs.AddCommand(show)
	root.AddCommand(jobs)

	tests := []struct {
		path string
		want string
	}{
		{"dreamer", "dreamer"},
		{"dreamer jobs", "jobs"},
		{"dreamer jobs show", "show"},
		{"dreamer nonexistent", ""},
	}
	for _, tt := range tests {
		got := findCommandByPath(root, tt.path)
		name := ""
		if got != nil {
			name = got.Name()
		}
		if name != tt.want {
			t.Errorf("findCommandByPath(%q) = %q, want %q", tt.path, name, tt.want)
		}
	}
}
