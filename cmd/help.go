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

// groupAssignments maps command Use strings to their group ID.
// Commands not listed here appear in a trailing "Other" section.
var groupAssignments = map[string]string{
	"analyze":  groupCore,
	"daemon":   groupCore,
	"start":    groupCore,
	"stop":     groupCore,
	"status":   groupCore,
	"setup":    groupSetup,
	"add":      groupSetup,
	"startup":  groupSetup,
	"ls-chats": groupInspect,
	"web":      groupInspect,
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

	var b strings.Builder

	// Banner — only on root command.
	if cmd.Parent() == nil {
		b.WriteString(bannerStyle.Render(dreamerBanner))
		b.WriteString("\n\n")
	}

	// Description.
	if cmd.Long != "" {
		b.WriteString(cmd.Long)
	} else if cmd.Short != "" {
		b.WriteString(cmd.Short)
	}
	b.WriteString("\n\n")

	// USAGE.
	b.WriteString(headerStyle.Render("USAGE"))
	b.WriteString("\n")
	b.WriteString("  " + cmd.UseLine())
	b.WriteString("\n")

	// Grouped commands.
	grouped := make(map[string][]*cobra.Command)
	var ungrouped []*cobra.Command
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() && c.Name() != "help" {
			continue
		}
		gid := c.GroupID
		if gid == "" {
			if assigned, ok := groupAssignments[c.Name()]; ok {
				gid = assigned
			}
		}
		if gid != "" {
			grouped[gid] = append(grouped[gid], c)
		} else {
			ungrouped = append(ungrouped, c)
		}
	}

	// Calculate padding: longest command name + 4 spaces.
	maxNameLen := 0
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() && c.Name() != "help" {
			continue
		}
		if n := len(c.Name()); n > maxNameLen {
			maxNameLen = n
		}
	}
	padding := maxNameLen + 4

	// Render each group.
	for _, g := range commandGroups {
		cmds := grouped[g.ID]
		if len(cmds) == 0 {
			continue
		}
		b.WriteString("\n")
		b.WriteString(headerStyle.Render(strings.ToUpper(g.Title)))
		b.WriteString("\n")
		for _, c := range cmds {
			padded := c.Name() + strings.Repeat(" ", padding-len(c.Name()))
			b.WriteString("  ")
			b.WriteString(cmdStyle.Render(padded))
			b.WriteString(descStyle.Render(c.Short))
			b.WriteString("\n")
		}
	}

	// Ungrouped commands (completion, help, etc.).
	if len(ungrouped) > 0 {
		b.WriteString("\n")
		b.WriteString(headerStyle.Render("OTHER"))
		b.WriteString("\n")
		for _, c := range ungrouped {
			padded := c.Name() + strings.Repeat(" ", padding-len(c.Name()))
			b.WriteString("  ")
			b.WriteString(cmdStyle.Render(padded))
			b.WriteString(descStyle.Render(c.Short))
			b.WriteString("\n")
		}
	}

	// FLAGS — styled per-flag with colored parts.
	renderFlagSection(&b, "FLAGS", cmd.LocalFlags(), headerStyle, flagNameStyle, typeStyle, defaultStyle, usageStyle)

	// Global flags.
	if cmd.HasPersistentFlags() {
		renderFlagSection(&b, "GLOBAL FLAGS", cmd.InheritedFlags(), headerStyle, flagNameStyle, typeStyle, defaultStyle, usageStyle)
	}

	// Footer hint.
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("Use %q for more information about a command.",
		cmd.CommandPath()+" [command] --help")))
	b.WriteString("\n")

	fmt.Fprintln(cmd.OutOrStdout(), b.String())
}

// renderFlagSection writes a styled FLAGS section from a pflag.FlagSet.
func renderFlagSection(
	b *strings.Builder,
	title string,
	flags *pflag.FlagSet,
	headerStyle, flagNameStyle, typeStyle, defaultStyle, usageStyle lipgloss.Style,
) {
	if !flags.HasAvailableFlags() {
		return
	}
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(title))
	b.WriteString("\n")

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

		b.WriteString("  ")

		// Short form.
		if f.Shorthand != "" {
			b.WriteString(flagNameStyle.Render("-" + f.Shorthand))
			b.WriteString(", ")
		} else {
			b.WriteString("   ")
		}

		// Long form.
		b.WriteString(flagNameStyle.Render("--" + f.Name))

		// Type (skip for bools).
		typeStr := ""
		if f.Value.Type() != "bool" {
			typeStr = " " + f.Value.Type()
			b.WriteString(typeStyle.Render(typeStr))
		}

		// Pad to alignment column.
		nameCol := len(f.Shorthand) + len(f.Name) + 6 + len(typeStr)
		if nameCol < maxCol {
			b.WriteString(strings.Repeat(" ", maxCol-nameCol))
		}

		// Description.
		b.WriteString(usageStyle.Render(f.Usage))

		// Default value (if non-empty and not "false"/"0"/"").
		def := f.DefValue
		if def != "" && def != "false" && def != "0" {
			b.WriteString(" ")
			b.WriteString(defaultStyle.Render("(default " + def + ")"))
		}

		b.WriteString("\n")
	})
}
