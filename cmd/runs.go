package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"dreamer/internal/capture"
	"dreamer/internal/config"
)

// newRunsCommand returns "dreamer runs <project> [--run <run-id>]".
// Lists captured analysis runs for a project, or shows one run's calls.
func newRunsCommand() *cobra.Command {
	var runID string

	command := &cobra.Command{
		Use:   "runs <project>",
		Short: "List captured LLM calls for a project.",
		Long: "Every analyze run captures its prompts and raw model responses under the project's runs directory. " +
			"List them, or pass --run to inspect each captured call of one run. " +
			"Pair with 'dreamer replay' to re-parse failed responses or re-send them to another model.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName := args[0]

			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			outputRoot, appConfig, err := resolveOutputRoot(cmd, resolvedConfigPath)
			if err != nil {
				return err
			}
			if err := validateConfiguredProject(appConfig, projectName); err != nil {
				return err
			}

			runsRoot := capture.RunsRoot(outputRoot, projectName)
			if runID != "" {
				return printRunDetail(cmd, runsRoot, projectName, runID)
			}

			summaries, err := capture.ListRuns(runsRoot)
			if err != nil {
				return fmt.Errorf("list runs: %w", err)
			}
			if len(summaries) == 0 {
				cmd.Printf("No captured runs for %q yet. Captured calls appear after the next analyze run.\n", projectName)
				return nil
			}
			cmd.Printf("Captured runs for %s (newest first):\n\n", projectName)
			cmd.Printf("  %-14s %-9s %-20s %-30s %-6s %s\n", "RUN", "KIND", "STARTED (UTC)", "PROVIDER/MODEL", "CALLS", "STATUS")
			for _, s := range summaries {
				kind := s.Kind
				if kind == "" {
					kind = capture.KindAnalysis
				}
				pm := s.Provider + "/" + s.Model
				cmd.Printf("  %-14s %-9s %-20s %-30s %-6d %s\n",
					s.RunID, kind, s.StartedAt.UTC().Format("2006-01-02 15:04"), pm, s.Calls, s.Status)
			}
			cmd.Printf("\nInspect calls: dreamer runs %s --run <run-id>\n", projectName)
			return nil
		},
	}

	command.Flags().StringVar(&runID, "run", "", "Show every captured call of this run.")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")
	return command
}

func printRunDetail(cmd *cobra.Command, runsRoot, projectName, runID string) error {
	meta, records, err := capture.LoadRun(runsRoot, runID)
	if err != nil {
		if errors.Is(err, capture.ErrRunNotFound) {
			return fmt.Errorf("run %q not found (see: dreamer runs %s)", runID, projectName)
		}
		return fmt.Errorf("load run: %w", err)
	}

	kind := meta.Kind
	if kind == "" {
		kind = capture.KindAnalysis
	}
	cmd.Printf("Run %s (%s)\n", meta.RunID, kind)
	cmd.Printf("  Provider: %s  Model: %s  Sandbox: %s\n", meta.ProviderID, meta.Model, meta.Sandbox)
	cmd.Printf("  Project:  %s\n", meta.ProjectPath)
	if meta.ParentRunID != "" {
		cmd.Printf("  Replay of: %s call #%d (%s)\n", meta.ParentRunID, meta.ParentCallIdx, meta.ReplayMode)
	}
	cmd.Printf("  Started:  %s (UTC)\n\n", meta.StartedAt.UTC().Format("2006-01-02 15:04"))

	cmd.Printf("  %-4s %-7s %-7s %-13s %-8s %s\n", "IDX", "PHASE", "CHUNK", "STATUS", "SECONDS", "ERROR")
	for _, rec := range records {
		chunk := "-"
		if rec.ChunkIndex >= 0 {
			chunk = fmt.Sprintf("%d/%d", rec.ChunkIndex, rec.ChunkCount)
		}
		errHead := rec.Error
		if len(errHead) > 60 {
			errHead = errHead[:57] + "..."
		}
		errHead = strings.ReplaceAll(errHead, "\n", " ")
		cmd.Printf("  %-4d %-7s %-7s %-13s %-8.1f %s\n",
			rec.Index, rec.Phase, chunk, rec.Status, float64(rec.DurationMS)/1000.0, errHead)
	}
	cmd.Printf("\nRe-parse a call:    dreamer replay %s %s --call <idx>\n", projectName, runID)
	cmd.Printf("Re-send w/ model:  dreamer replay %s %s --call <idx> --resend --model <model>\n", projectName, runID)
	return nil
}

// validateConfiguredProject reports whether name matches a configured
// project, mirroring the web layer's resolveProjectPath guard against path
// traversal via crafted project names.
func validateConfiguredProject(appConfig *config.App, name string) error {
	for _, p := range appConfig.Projects {
		if p.Name == name {
			return nil
		}
	}
	known := make([]string, 0, len(appConfig.Projects))
	for _, p := range appConfig.Projects {
		known = append(known, p.Name)
	}
	return fmt.Errorf("project %q is not configured (known: %s)", name, strings.Join(known, ", "))
}
