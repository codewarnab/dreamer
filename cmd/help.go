package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Color palette — mirrors cmd/setup.go.
var (
	colorAccent = lipgloss.Color("#3cffd0") // mint green
	colorDesc   = lipgloss.Color("#949494") // gray
	colorHeader = lipgloss.Color("#63")     // purple/indigo
	colorDim    = lipgloss.Color("#666666") // dim
)

// Command group IDs — match cobra.Group.ID.
const (
	groupCore    = "core"
	groupSetup   = "setup"
	groupInspect = "inspect"
)

// commandGroups defines the logical grouping shown in help output.
var commandGroups = []*cobra.Group{
	{ID: groupCore, Title: "Core"},
	{ID: groupSetup, Title: "Setup & Config"},
	{ID: groupInspect, Title: "Inspect"},
}

// styledHelp renders a lipgloss-styled help message for cmd.
func styledHelp(cmd *cobra.Command, _ []string) {
	// Styles.
	bannerStyle := lipgloss.NewStyle().Foreground(colorAccent)
	headerStyle := lipgloss.NewStyle().Foreground(colorHeader).Bold(true)
	cmdStyle := lipgloss.NewStyle().Foreground(colorAccent)
	descStyle := lipgloss.NewStyle().Foreground(colorDesc)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	flagNameStyle := lipgloss.NewStyle().Foreground(colorAccent)
	typeStyle := lipgloss.NewStyle().Foreground(colorDesc)
	defaultStyle := lipgloss.NewStyle().Foreground(colorDim)
	usageStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cccccc"))

	var helpOutput strings.Builder

	// Banner — only on root command.
	if cmd.Parent() == nil {
		helpOutput.WriteString(bannerStyle.Render(dreamerBanner))
		helpOutput.WriteString("\n\n")
	}

	// Description.
	if cmd.Long != "" {
		helpOutput.WriteString(cmd.Long)
	} else if cmd.Short != "" {
		helpOutput.WriteString(cmd.Short)
	}
	helpOutput.WriteString("\n\n")

	// USAGE.
	helpOutput.WriteString(headerStyle.Render("USAGE"))
	helpOutput.WriteString("\n")
	helpOutput.WriteString("  " + cmd.UseLine())
	helpOutput.WriteString("\n")

	// Grouped commands.
	grouped := make(map[string][]*cobra.Command)
	var ungrouped []*cobra.Command
	for _, subcmd := range cmd.Commands() {
		if !subcmd.IsAvailableCommand() && subcmd.Name() != "help" {
			continue
		}
		if subcmd.GroupID != "" {
			grouped[subcmd.GroupID] = append(grouped[subcmd.GroupID], subcmd)
		} else {
			ungrouped = append(ungrouped, subcmd)
		}
	}

	// Calculate padding: longest command name + 4 spaces.
	maxNameLen := 0
	for _, subcmd := range cmd.Commands() {
		if !subcmd.IsAvailableCommand() && subcmd.Name() != "help" {
			continue
		}
		if nameLen := len(subcmd.Name()); nameLen > maxNameLen {
			maxNameLen = nameLen
		}
	}
	padding := maxNameLen + 4

	// Render each group.
	for _, group := range commandGroups {
		cmds := grouped[group.ID]
		if len(cmds) == 0 {
			continue
		}
		helpOutput.WriteString("\n")
		helpOutput.WriteString(headerStyle.Render(strings.ToUpper(group.Title)))
		helpOutput.WriteString("\n")
		for _, subcmd := range cmds {
			padded := subcmd.Name() + strings.Repeat(" ", padding-len(subcmd.Name()))
			helpOutput.WriteString("  ")
			helpOutput.WriteString(cmdStyle.Render(padded))
			helpOutput.WriteString(descStyle.Render(subcmd.Short))
			helpOutput.WriteString("\n")
		}
	}

	// Ungrouped commands (completion, help, etc.).
	if len(ungrouped) > 0 {
		helpOutput.WriteString("\n")
		helpOutput.WriteString(headerStyle.Render("OTHER"))
		helpOutput.WriteString("\n")
		for _, subcmd := range ungrouped {
			padded := subcmd.Name() + strings.Repeat(" ", padding-len(subcmd.Name()))
			helpOutput.WriteString("  ")
			helpOutput.WriteString(cmdStyle.Render(padded))
			helpOutput.WriteString(descStyle.Render(subcmd.Short))
			helpOutput.WriteString("\n")
		}
	}

	// FLAGS — styled per-flag with colored parts.
	renderFlagSection(&helpOutput, "FLAGS", cmd.LocalFlags(), headerStyle, flagNameStyle, typeStyle, defaultStyle, usageStyle)

	// Global flags.
	if cmd.HasPersistentFlags() {
		renderFlagSection(&helpOutput, "GLOBAL FLAGS", cmd.InheritedFlags(), headerStyle, flagNameStyle, typeStyle, defaultStyle, usageStyle)
	}

	// Footer hint.
	helpOutput.WriteString("\n")
	helpOutput.WriteString(dimStyle.Render(fmt.Sprintf("Use %q for more information about a command.",
		cmd.CommandPath()+" [command] --help")))
	helpOutput.WriteString("\n")

	fmt.Fprintln(cmd.OutOrStdout(), helpOutput.String())
}

// renderFlagSection writes a styled FLAGS section from a pflag.FlagSet.
func renderFlagSection(
	builder *strings.Builder,
	title string,
	flags *pflag.FlagSet,
	headerStyle, flagNameStyle, typeStyle, defaultStyle, usageStyle lipgloss.Style,
) {
	if !flags.HasAvailableFlags() {
		return
	}
	builder.WriteString("\n")
	builder.WriteString(headerStyle.Render(title))
	builder.WriteString("\n")

	// Find longest flag name column for alignment.
	maxCol := 0
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Deprecated != "" {
			return
		}
		col := len(f.Shorthand) + len(f.Name) + 6 // "-X, --name"
		if f.Value.Type() != "bool" {
			col += len(f.Value.Type()) + 1 // " type"
		}
		if col > maxCol {
			maxCol = col
		}
	})
	maxCol += 2 // breathing room before description

	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Deprecated != "" {
			return
		}

		builder.WriteString("  ")

		// Short form.
		if f.Shorthand != "" {
			builder.WriteString(flagNameStyle.Render("-" + f.Shorthand))
			builder.WriteString(", ")
		} else {
			builder.WriteString("   ")
		}

		// Long form.
		builder.WriteString(flagNameStyle.Render("--" + f.Name))

		// Type (skip for bools).
		typeStr := ""
		if f.Value.Type() != "bool" {
			typeStr = " " + f.Value.Type()
			builder.WriteString(typeStyle.Render(typeStr))
		}

		// Pad to alignment column.
		nameCol := len(f.Shorthand) + len(f.Name) + 6 + len(typeStr)
		if nameCol < maxCol {
			builder.WriteString(strings.Repeat(" ", maxCol-nameCol))
		}

		// Description.
		builder.WriteString(usageStyle.Render(f.Usage))

		// Default value (if non-empty and not "false"/"0"/"").
		def := f.DefValue
		if def != "" && def != "false" && def != "0" {
			builder.WriteString(" ")
			builder.WriteString(defaultStyle.Render("(default " + def + ")"))
		}

		builder.WriteString("\n")
	})
}
