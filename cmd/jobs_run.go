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

	command := &cobra.Command{
		Use:   "run <job_id>",
		Short: "Execute a background job on demand.",
		Long: "Run a background job immediately. Designed for use from OS schedulers " +
			"(cron, Task Scheduler). Returns exit code 0 on success, 1 on failure.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			jobID := args[0]
			if err := backgroundjobs.ValidateJobID(jobID); err != nil {
				return err
			}

			// Require DREAMER_RUN_TOKEN to prevent intra-user side-channel triggers.
			// OS scheduler entries set this; direct CLI invocations without it are rejected.
			if os.Getenv("DREAMER_RUN_TOKEN") == "" {
				return fmt.Errorf("DREAMER_RUN_TOKEN not set; this command is intended for use from OS schedulers (cron, Task Scheduler, launchd)")
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

	return command
}
