package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"dreamer/internal/diagnostics"
)

func newDoctorCommand() *cobra.Command {
	var (
		jsonOutput bool
	)

	command := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose common setup and runtime problems.",
		Long: "Check config validity, output-root writability, log files, OS sandbox " +
			"availability, and background-jobs health. Read-only.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := resolveDiagContext(cmd)
			if err != nil {
				return err
			}

			if jsonOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(ctx.report)
			}

			printDoctorReport(cmd, ctx.report)

			if ctx.report.Failed() {
				fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n",
					doctorHintStyle.Render("Fix the failed checks above, or run 'dreamer bug report' to capture a diagnostic bundle."))
				return &alreadyPrintedError{fmt.Errorf("doctor found failing checks")}
			}
			return nil
		},
	}

	command.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON.")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")

	return command
}

// doctorStatusStyles maps each check status to its glyph + color.
var doctorStatusStyles = map[diagnostics.Status]lipgloss.Style{
	diagnostics.StatusPass:    lipgloss.NewStyle().Foreground(lipgloss.Color("#3cffd0")).Bold(true),
	diagnostics.StatusWarn:    lipgloss.NewStyle().Foreground(lipgloss.Color("#ffb86c")).Bold(true),
	diagnostics.StatusFail:    lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true),
	diagnostics.StatusInfo:    lipgloss.NewStyle().Foreground(colorDim),
	diagnostics.StatusSkipped: lipgloss.NewStyle().Foreground(colorDim),
}

var doctorGlyphs = map[diagnostics.Status]string{
	diagnostics.StatusPass:    "✓ PASS   ",
	diagnostics.StatusWarn:    "! WARN   ",
	diagnostics.StatusFail:    "✗ FAIL   ",
	diagnostics.StatusInfo:    "· INFO   ",
	diagnostics.StatusSkipped: "- SKIP   ",
}

var (
	doctorHeaderStyle = lipgloss.NewStyle().Foreground(colorHeader).Bold(true)
	doctorNameStyle   = lipgloss.NewStyle().Foreground(colorAccent)
	doctorDetailStyle = lipgloss.NewStyle().Foreground(colorDesc)
	doctorHintStyle   = lipgloss.NewStyle().Foreground(colorDim)
)

// printDoctorReport renders the report with styled status glyphs.
func printDoctorReport(cmd *cobra.Command, report diagnostics.Report) {
	var b strings.Builder

	b.WriteString(doctorHeaderStyle.Render("DREAMER DOCTOR") + "\n")
	sys := report.System
	b.WriteString(fmt.Sprintf("%s %s (%s/%s, %s)\n\n",
		doctorDetailStyle.Render("dreamer"), doctorDetailStyle.Render(sys.Version),
		doctorDetailStyle.Render(sys.GOOS), doctorDetailStyle.Render(sys.GOARCH),
		doctorDetailStyle.Render(sys.GoVersion)))

	for _, c := range report.Checks {
		style, ok := doctorStatusStyles[c.Status]
		if !ok {
			style = doctorStatusStyles[diagnostics.StatusInfo]
		}
		glyph, ok := doctorGlyphs[c.Status]
		if !ok {
			glyph = doctorGlyphs[diagnostics.StatusInfo]
		}
		b.WriteString("  " + style.Render(glyph) + doctorNameStyle.Render(c.Name))
		if c.Detail != "" {
			b.WriteString(": " + doctorDetailStyle.Render(c.Detail))
		}
		b.WriteString("\n")
	}

	failed, warned := countCheckStatuses(report)
	b.WriteString("\n" + fmt.Sprintf("%d check(s): %d passed, %d warned, %d failed",
		len(report.Checks), len(report.Checks)-failed-warned, warned, failed))
	fmt.Fprint(cmd.OutOrStdout(), "\n"+b.String()+"\n")
}

// countCheckStatuses returns the number of failed and warned checks.
func countCheckStatuses(report diagnostics.Report) (failed, warned int) {
	for _, c := range report.Checks {
		switch c.Status {
		case diagnostics.StatusFail:
			failed++
		case diagnostics.StatusWarn:
			warned++
		}
	}
	return failed, warned
}
