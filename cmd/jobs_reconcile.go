package cmd

import (
	"encoding/json"
	"fmt"

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/logging"

	"github.com/spf13/cobra"
)

func newJobsReconcileCommand() *cobra.Command {
	var (
		dryRun      bool
		verbose     bool
		jsonOutput  bool
	)

	command := &cobra.Command{
		Use:   "reconcile",
		Short: "Synchronize OS schedules with job store.",
		Long: "Inspect every scheduled job and every OS-level schedule artifact. " +
			"Idempotent. Reports drift and corrects it (missing installs, " +
			"orphaned schedules, spec hash mismatches).",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}

			outputRoot, _, err := resolveOutputRoot(cmd, resolvedConfigPath)
			if err != nil {
				return err
			}

			lg := logging.Silent()
			if verbose {
				lg, err = logging.New(outputRoot, "info", 10)
				if err != nil {
					return fmt.Errorf("create logger: %w", err)
				}
				defer lg.Close()
			}

			store := backgroundjobs.NewStore(outputRoot, lg)
			execPath, err := resolveSelfExecutable()
			if err != nil {
				return err
			}

			installID, err := backgroundjobs.ResolveInstallID(store.Dir())
			if err != nil {
				return fmt.Errorf("resolve install ID: %w", err)
			}

			cfg := backgroundjobs.SchedulerConfig{
				StoreDir:       store.Dir(),
				ExecutablePath: execPath,
				ConfigPath:     resolvedConfigPath,
				InstallID:      installID,
				ConfigHash:     backgroundjobs.HashConfigPath(resolvedConfigPath),
				ExecHash:       backgroundjobs.HashExecutablePath(execPath),
			}

			scheduler := backgroundjobs.NewScheduler(cfg, lg)
			reconciler := &backgroundjobs.Reconciler{
				Scheduler:  scheduler,
				Store:      store,
				Logger:     lg,
				ConfigHash: cfg.ConfigHash,
			}

			result, err := reconciler.ReconcileSchedules(cmd.Context(), dryRun)
			if err != nil {
				return fmt.Errorf("reconcile: %w", err)
			}

			return printReconcileResult(cmd, result, dryRun, jsonOutput)
		},
	}

	command.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would change without modifying anything.")
	command.Flags().BoolVarP(&verbose, "verbose", "v", false, "Log reconciliation actions to stderr.")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON.")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")

	return command
}

func printReconcileResult(cmd *cobra.Command, result backgroundjobs.ReconcileResult, dryRun bool, jsonOutput bool) error {
	if jsonOutput {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"dry_run":   dryRun,
			"installed": result.Installed,
			"removed":   result.Removed,
			"disabled":  result.Disabled,
			"errors":    result.Errors,
		})
	}

	if dryRun {
		cmd.Println("Dry run — no changes made.")
	}

	total := result.Installed + result.Removed + result.Disabled
	if total == 0 && len(result.Errors) == 0 {
		cmd.Println("Everything in sync.")
		return nil
	}

	if result.Installed > 0 {
		cmd.Printf("Installed: %d\n", result.Installed)
	}
	if result.Removed > 0 {
		cmd.Printf("Removed:   %d\n", result.Removed)
	}
	if result.Disabled > 0 {
		cmd.Printf("Disabled:  %d\n", result.Disabled)
	}

	if len(result.Errors) > 0 {
		cmd.Printf("\nErrors:\n")
		for _, e := range result.Errors {
			cmd.Printf("  %s: %v\n", e.JobID, e.Err)
		}
	}

	return nil
}
