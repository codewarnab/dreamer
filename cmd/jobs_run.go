package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/logging"

	"github.com/spf13/cobra"
)

// newJobsRunCommand returns "dreamer jobs run <job_id>".
// Executes a background job on demand, safe for use from OS schedulers.
// Returns exit code 0 on success, 1 on failure.
func newJobsRunCommand() *cobra.Command {
	var timeout time.Duration
	var runTokenFile string
	var force bool

	command := &cobra.Command{
		Use:   "run <job_id>",
		Short: "Execute a background job on demand.",
		Long: "Run a background job immediately. Designed for use from OS schedulers " +
			"(cron, Task Scheduler). Returns exit code 0 on success, 1 on failure.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return missingArgError(cmd, "job-id",
					"The ID of the job to run.",
					"dreamer jobs run <job-id>")
			}
			jobID := args[0]
			if err := backgroundjobs.ValidateJobID(jobID); err != nil {
				return err
			}

			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}

			outputRoot, _, err := resolveOutputRoot(cmd, resolvedConfigPath)
			if err != nil {
				return err
			}

			lg, err := logging.New(outputRoot, "info", 10)
			if err != nil {
				return fmt.Errorf("create logger: %w", err)
			}
			defer lg.Close()

			store := backgroundjobs.NewStore(outputRoot, lg)
			runStore := backgroundjobs.NewRunStore(store.Dir(), lg)
			audit := backgroundjobs.NewAuditWriter(store.Dir())

			// Validate run token unless --force is set.
			if !force {
				if runTokenFile == "" {
					return fmt.Errorf("run token not provided; use --run-token-file or --force to bypass")
				}
				providedToken, readErr := os.ReadFile(runTokenFile)
				if readErr != nil {
					return fmt.Errorf("read run token file: %w", readErr)
				}
				if validateErr := backgroundjobs.ValidateRunToken(store.Dir(), string(providedToken)); validateErr != nil {
					return fmt.Errorf("run token validation failed: %w", validateErr)
				}
			} else {
				// Audit forced runs so they are visible in the audit log.
				if auditErr := audit.Write(backgroundjobs.AuditEvent{
					Event: "job.run.forced",
					JobID: jobID,
					Actor: "cli",
				}); auditErr != nil {
					lg.Warn("audit write failed (forced run)", logging.Any("err", auditErr))
				}
			}

			// Build scheduler for self-repair (best-effort).
			var selfRepair *backgroundjobs.SelfRepairConfig
			if s, schedErr := buildScheduler(outputRoot, resolvedConfigPath); schedErr == nil {
				execPath, _ := os.Executable()
				execPath, _ = filepath.EvalSymlinks(execPath)
				installID, _ := backgroundjobs.ResolveInstallID(store.Dir())
				selfRepair = &backgroundjobs.SelfRepairConfig{
					Scheduler:      s,
					ExecutablePath: execPath,
					InstallID:      installID,
					ConfigHash:     backgroundjobs.HashConfigPath(resolvedConfigPath),
					ExecHash:       backgroundjobs.HashExecutablePath(execPath),
				}
			}

			executor := &backgroundjobs.Executor{
				Store:       store,
				RunStore:    runStore,
				AuditWriter: audit,
				ConfigPath:  resolvedConfigPath,
				Logger:      lg,
				NewProvider: defaultProviderFactory,
				SelfRepair:  selfRepair,
			}

			ctx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}

			result, err := executor.Run(ctx, jobID)
			if err != nil {
				return fmt.Errorf("job %q failed: %w", jobID, err)
			}

			cmd.Printf("job %q finished: %s (took %dms)\n",
				jobID, string(result.Record.Status), result.Record.DurationMillis)
			return nil
		},
	}

	command.Flags().DurationVar(&timeout, "timeout", 0, "Override job timeout (e.g. 30m, 1h). 0 = use schedule default.")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")
	command.Flags().StringVar(&runTokenFile, "run-token-file", "", "Path to file containing the per-install run token.")
	command.Flags().BoolVar(&force, "force", false, "Bypass run token validation (for interactive/CLI use).")

	return command
}
