package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dreamer/internal/analyzer"
	"dreamer/internal/diagnostics"
	"dreamer/internal/fsutil"
)

func newBugCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "bug",
		Short: "Create diagnostic reports and GitHub issues.",
		Long:  "Capture a redacted diagnostics bundle or file a GitHub issue with environment details attached.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return suggestSubcommandRunE()(cmd, args)
		},
	}

	command.AddCommand(newBugReportCommand())
	command.AddCommand(newBugIssueCommand())

	return command
}

// ── dreamer bug report ───────────────────────────────────────────────────────

func newBugReportCommand() *cobra.Command {
	var outputPath string

	command := &cobra.Command{
		Use:   "report",
		Short: "Write a redacted diagnostics bundle (zip) for debugging.",
		Long: "Collect system info, health checks, and log tails into a single zip archive. " +
			"All contents pass through the secret-redaction pipeline before being written.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctxData, err := resolveDiagContext(cmd)
			if err != nil {
				return err
			}

			dest := outputPath
			if dest == "" {
				dest = fmt.Sprintf("dreamer-diagnostics-%s.zip",
					time.Now().Format("20060102-150405"))
			}
			if ext := filepath.Ext(dest); ext != ".zip" {
				dest += ".zip"
			}
			if !filepath.IsAbs(dest) {
				if abs, absErr := filepath.Abs(dest); absErr == nil {
					dest = abs
				}
			}

			written, err := diagnostics.WriteBundle(
				ctxData.report, ctxData.input, ctxData.redactor,
				diagnostics.BundleOptions{}, dest)
			if err != nil {
				return err
			}

			fi, statErr := os.Stat(written)
			size := int64(0)
			if statErr == nil {
				size = fi.Size()
			}

			cmd.Printf("diagnostic bundle written: %s (%d bytes)\n", written, size)
			cmd.Println("secrets matching redaction patterns were replaced before writing")
			if ctxData.report.Failed() {
				cmd.Println("some checks failed — run 'dreamer doctor' for details, or 'dreamer bug issue' to file a report")
			}
			return nil
		},
	}

	command.Flags().StringVarP(&outputPath, "output", "o", "", "Output path for the zip bundle (default: ./dreamer-diagnostics-<timestamp>.zip).")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")

	return command
}

// ── dreamer bug issue ────────────────────────────────────────────────────────

func newBugIssueCommand() *cobra.Command {
	var (
		title  string
		body   string
		yes    bool
		dryRun bool
	)

	command := &cobra.Command{
		Use:   "issue --title <text>",
		Short: "File a GitHub issue pre-filled with your diagnostics.",
		Long: "Compose an issue body from your description plus the diagnostics report and create it " +
			"via the GitHub CLI (gh). When gh is missing or not authenticated, the composed body is " +
			"saved locally so it can be pasted into the browser instead.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if title == "" {
				return missingFlagError(cmd, "title",
					"A short summary of the problem, shown as the issue title.",
					"dreamer bug issue --title \"analyze fails on Windows\"")
			}

			ctxData, err := resolveDiagContext(cmd)
			if err != nil {
				return err
			}

			issueBody := composeIssueBody(ctxData.redactor, body, ctxData.report)

			if dryRun {
				cmd.Printf("TITLE: %s\n\n%s\n", title, issueBody)
				return nil
			}

			gh := diagnostics.DetectGh()
			if !gh.Installed {
				return bugIssueFallback(cmd, ctxData, title, issueBody,
					"The GitHub CLI (gh) is not installed or not on PATH.")
			}

			if !yes {
				printIssuePreview(cmd, title, issueBody)
				cmd.Print("Re-run with --yes to create the issue.\n")
				return nil
			}

			url, err := diagnostics.CreateIssue(cmd.Context(), gh.Path, title, issueBody)
			if err != nil {
				if isGhAuthFailure(err) {
					return bugIssueFallback(cmd, ctxData, title, issueBody,
						fmt.Sprintf("gh rejected the request: %v", err))
				}
				return err
			}

			cmd.Printf("issue created: %s\n", url)
			cmd.Println("tip: drag a diagnostic bundle onto the issue to attach logs:")
			cmd.Printf("  dreamer bug report && dreamer bug issue --title %q --yes\n", title)
			return nil
		},
	}

	command.Flags().StringVarP(&title, "title", "t", "", "Issue title (required).")
	command.Flags().StringVarP(&body, "body", "b", "", "Description of the problem (appended above the auto-collected diagnostics).")
	command.Flags().BoolVar(&yes, "yes", false, "Skip preview and create the issue immediately.")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Print the composed title and body without creating anything.")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")

	return command
}

// composeIssueBody merges the user's description with the rendered report and
// redacts the whole thing — user text may contain pasted logs.
func composeIssueBody(redactor *analyzer.Redactor, userBody string, report diagnostics.Report) string {
	var b strings.Builder
	if strings.TrimSpace(userBody) != "" {
		b.WriteString(strings.TrimSpace(userBody))
		b.WriteString("\n\n---\n\n")
	}
	b.WriteString(diagnostics.RenderText(report))

	text := b.String()
	if redactor != nil {
		text, _ = redactor.Redact(text)
	}
	return text
}

// printIssuePreview shows what would be submitted so the user can inspect the
// redacted content before it goes public.
func printIssuePreview(cmd *cobra.Command, title, body string) {
	cmd.Print("Ready to create the following issue:\n\n")
	cmd.Printf("Title: %s\n\n", title)
	cmd.Println(body)
}

// bugIssueFallback handles every path where the issue cannot be created from
// the terminal (gh missing, gh unauthenticated): saves the composed body to
// disk and prints manual instructions.
func bugIssueFallback(cmd *cobra.Command, ctxData *diagContext, title, body, reason string) error {
	var blk errBlock
	blk.label("Cannot create GitHub issue from here")
	blk.line(reason)
	blk.gap()

	savedPath := savePendingIssueBody(ctxData.input.OutputRoot, title, body)
	if savedPath != "" {
		blk.line("The composed issue body was saved to:")
		blk.code(savedPath)
		blk.gap()
	}

	blk.line("Options:")
	blk.line("  1. Install the GitHub CLI (https://cli.github.com), then run 'gh auth login' and retry.")
	blk.line("  2. Open the issue manually in a browser and paste the saved body:")
	blk.code(diagnostics.IssuesURL)
	blk.gap()
	blk.line("A redacted log bundle can be generated separately:")
	blk.code("dreamer bug report")

	fmt.Fprint(cmd.ErrOrStderr(), blk.String())
	return &alreadyPrintedError{fmt.Errorf("github issue not created")}
}

// savePendingIssueBody writes the composed body under
// <outputRoot>/diagnostics/ for later manual use. Returns "" on failure —
// the fallback instructions are still shown without it.
func savePendingIssueBody(outputRoot, title, body string) string {
	if outputRoot == "" {
		return ""
	}
	dir := filepath.Join(outputRoot, "diagnostics")
	if err := os.MkdirAll(dir, fsutil.DirPerms); err != nil {
		return ""
	}
	name := fmt.Sprintf("pending-issue-body-%s.md", time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	content := fmt.Sprintf("<!-- issue title: %s -->\n\n%s\n", title, body)
	if err := fsutil.WriteFileAtomic(path, []byte(content), fsutil.FilePerms); err != nil {
		return ""
	}
	return path
}

// isGhAuthFailure reports whether err came from the gh authentication check.
func isGhAuthFailure(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not authenticated")
}
