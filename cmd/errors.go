package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// Styled error output — reuses the palette from help.go.
var (
	errLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true) // red
	errFlagStyle  = lipgloss.NewStyle().Foreground(colorAccent)                          // mint green
	errDescStyle  = lipgloss.NewStyle().Foreground(colorDesc)                            // gray
	errHintStyle  = lipgloss.NewStyle().Foreground(colorDim)                             // dim
)

// missingFlagError returns a styled error for a missing required flag.
// The description explains what the flag does; example shows a sample invocation.
func missingFlagError(cmd *cobra.Command, flagName string, description string, example string) error {
	var b strings.Builder

	b.WriteString(errLabelStyle.Render("  Missing required flag: ") + errFlagStyle.Render("--"+flagName))
	b.WriteString("\n\n")
	if description != "" {
		b.WriteString("  " + errDescStyle.Render(description))
		b.WriteString("\n\n")
	}
	if example != "" {
		b.WriteString(errDescStyle.Render("  Usage:"))
		b.WriteString("\n")
		b.WriteString("    " + errFlagStyle.Render(example))
		b.WriteString("\n\n")
	}
	b.WriteString(errHintStyle.Render(fmt.Sprintf("  Run '%s --help' for all available flags.", cmd.CommandPath())))
	b.WriteString("\n")

	return fmt.Errorf("\n%s", b.String())
}

// missingArgError returns a styled error for a missing positional argument.
// The description explains what the argument is; example shows a sample invocation.
func missingArgError(cmd *cobra.Command, argName string, description string, example string) error {
	var b strings.Builder

	b.WriteString(errLabelStyle.Render("  Missing required argument: ") + errFlagStyle.Render(argName))
	b.WriteString("\n\n")
	if description != "" {
		b.WriteString("  " + errDescStyle.Render(description))
		b.WriteString("\n\n")
	}
	if example != "" {
		b.WriteString(errDescStyle.Render("  Usage:"))
		b.WriteString("\n")
		b.WriteString("    " + errFlagStyle.Render(example))
		b.WriteString("\n\n")
	}
	b.WriteString(errHintStyle.Render(fmt.Sprintf("  Run '%s --help' for all available options.", cmd.CommandPath())))
	b.WriteString("\n")

	return fmt.Errorf("\n%s", b.String())
}

// invalidFlagValueError returns a styled error for an invalid flag value.
// Shows what was provided and what values are valid.
func invalidFlagValueError(cmd *cobra.Command, flagName string, got string, valid []string) error {
	var b strings.Builder

	b.WriteString(errLabelStyle.Render("  Invalid value for ") + errFlagStyle.Render("--"+flagName) + errLabelStyle.Render(": ") + errFlagStyle.Render(got))
	b.WriteString("\n\n")
	b.WriteString(errDescStyle.Render("  Valid values: ") + errFlagStyle.Render(strings.Join(valid, ", ")))
	b.WriteString("\n\n")
	b.WriteString(errHintStyle.Render(fmt.Sprintf("  Run '%s --help' for all available options.", cmd.CommandPath())))
	b.WriteString("\n")

	return fmt.Errorf("\n%s", b.String())
}
