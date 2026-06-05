package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// reUnknownCommand matches Cobra's "unknown command" error format.
var reUnknownCommand = regexp.MustCompile(`unknown command "([^"]+)" for "([^"]+)"`)

// reUnknownFlag matches pflag's "unknown flag" error format.
var reUnknownFlag = regexp.MustCompile(`unknown flag: --(\S+)`)

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Single-row DP — O(min(m,n)) space.
	if len(a) > len(b) {
		a, b = b, a
	}
	prev := make([]int, len(a)+1)
	for i := range prev {
		prev[i] = i
	}

	for j := 1; j <= len(b); j++ {
		curr := make([]int, len(a)+1)
		curr[0] = j
		for i := 1; i <= len(a); i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[i] = min(curr[i-1]+1, min(prev[i]+1, prev[i-1]+cost))
		}
		prev = curr
	}
	return prev[len(a)]
}

// suggestCommand returns the closest visible command name to typed, or "" if
// nothing falls within the similarity threshold.
func suggestCommand(typed string, cmds []*cobra.Command) string {
	best := ""
	bestDist := len(typed) // worst case

	for _, cmd := range cmds {
		if cmd.Hidden {
			continue
		}
		if cmd.Name() == typed {
			return "" // Exact match — no suggestion needed.
		}
		d := levenshtein(typed, cmd.Name())
		if d < bestDist {
			bestDist = d
			best = cmd.Name()
		}
		// Also check aliases.
		for _, alias := range cmd.Aliases {
			d = levenshtein(typed, alias)
			if d < bestDist {
				bestDist = d
				best = alias
			}
		}
	}

	// Threshold matches Git's leniency: max(2, len/3) keeps suggestions
	// useful even for short inputs where 1 edit is already significant.
	threshold := max(2, len(typed)/3)
	if bestDist > threshold {
		return ""
	}
	return best
}

// suggestFlag returns the closest flag name to typed, or "" if nothing falls
// within the similarity threshold.
func suggestFlag(typed string, flags *pflag.FlagSet) string {
	best := ""
	bestDist := len(typed)

	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Deprecated != "" {
			return
		}
		if f.Name == typed {
			best = ""
			bestDist = 0
			return
		}
		d := levenshtein(typed, f.Name)
		if d < bestDist {
			bestDist = d
			best = f.Name
		}
	})

	threshold := max(2, len(typed)/3)
	if bestDist > threshold {
		return ""
	}
	return best
}

// styledSuggestionError renders a styled "did you mean" error block.
func styledSuggestionError(label, typed, suggestion, hintCmd string) string {
	var b strings.Builder
	b.WriteString(errLabelStyle.Render("  "+label+": ") + errFlagStyle.Render(typed))
	b.WriteString("\n\n")
	b.WriteString(errDescStyle.Render("  Did you mean this?"))
	b.WriteString("\n")
	b.WriteString("    " + errFlagStyle.Render(suggestion))
	b.WriteString("\n\n")
	b.WriteString(errHintStyle.Render(fmt.Sprintf("  Run '%s --help' for available options.", hintCmd)))
	b.WriteString("\n")
	return b.String()
}

// suggestFromError inspects an error from Cobra and, if it's an unknown
// command, prints a styled suggestion to stderr. Returns true if a
// suggestion was printed.
func suggestFromError(err error) bool {
	msg := err.Error()

	if m := reUnknownCommand.FindStringSubmatch(msg); m != nil {
		typed := m[1]
		parentPath := m[2]

		// Find the parent command to get its subcommands.
		parent := findCommandByPath(rootCmd, parentPath)
		if parent == nil {
			return false
		}

		suggestion := suggestCommand(typed, parent.Commands())
		if suggestion == "" {
			return false
		}

		fmt.Fprintln(rootCmd.ErrOrStderr(),
			styledSuggestionError("Unknown command", typed, suggestion, parentPath))
		return true
	}

	return false
}

// alreadyPrintedError wraps an error whose styled output has already been
// written directly to stderr. Execute() recognizes this sentinel and skips
// the generic "Error: " prefix, preventing double-printing.
//
// All public error helpers in errors.go return this type after printing to
// cmd.ErrOrStderr(). The one exception is errors returned from store.Update
// callbacks (e.g. setJobEnabled), which cannot print before returning and
// fall through to Execute()'s plain fallback.
type alreadyPrintedError struct{ err error }

func (e *alreadyPrintedError) Error() string { return e.err.Error() }

func (e *alreadyPrintedError) Unwrap() error { return e.err }

// styledFlagError is set via root.SetFlagErrorFunc to intercept unknown flag
// errors and suggest the closest match.
func styledFlagError(cmd *cobra.Command, err error) error {
	msg := err.Error()

	if m := reUnknownFlag.FindStringSubmatch(msg); m != nil {
		typed := m[1]

		// Search both local and inherited flags.
		suggestion := suggestFlag(typed, cmd.Flags())
		if suggestion == "" {
			suggestion = suggestFlag(typed, cmd.InheritedFlags())
		}
		if suggestion != "" {
			fmt.Fprintln(cmd.ErrOrStderr(),
				styledSuggestionError("Unknown flag", "--"+typed, "--"+suggestion, cmd.CommandPath()))
			return &alreadyPrintedError{err}
		}
	}

	return err
}

// suggestSubcommandRunE returns a RunE for parent commands that have
// subcommands but no action of their own. When the user passes args
// (e.g. "dreamer jobs shw"), it suggests the closest subcommand.
func suggestSubcommandRunE() func(*cobra.Command, []string) error {
	return func(c *cobra.Command, args []string) error {
		if len(args) == 0 {
			return c.Help()
		}

		// First arg might be a mistyped subcommand.
		suggestion := suggestCommand(args[0], c.Commands())
		if suggestion != "" {
			fmt.Fprintln(c.ErrOrStderr(),
				styledSuggestionError("Unknown command", args[0], suggestion, c.CommandPath()))
			return &alreadyPrintedError{fmt.Errorf("unknown command %q for %q", args[0], c.CommandPath())}
		}

		return c.Help()
	}
}

// findCommandByPath walks the command tree to find the command at path.
// For "dreamer jobs" it returns the jobs subcommand of rootCmd.
func findCommandByPath(root *cobra.Command, path string) *cobra.Command {
	parts := strings.Split(path, " ")
	if len(parts) == 0 {
		return root
	}

	// First part is root's name; skip it.
	cmd := root
	for _, name := range parts[1:] {
		found := false
		for _, child := range cmd.Commands() {
			if child.Name() == name || child.HasAlias(name) {
				cmd = child
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	return cmd
}
