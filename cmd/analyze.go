package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"dreamer/internal/config"
	"dreamer/internal/jobqueue"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"github.com/spf13/cobra"
)

func newAnalyzeCommand() *cobra.Command {
	var (
		projectPath string
		providerID  string
		force       bool
		dryRun      bool
		permissive  bool
		outputDir   string
		since       string
		analyzerFlags analyzerFlagVars
	)

	command := &cobra.Command{
		Use:   "analyze",
		Short: "Run one analysis pass for a single project path.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Lazy-init note: analyze deliberately skips web server, fsnotify,
			// and job queue — those are daemon-only. SQLite readers open
			// per-source on demand (not eagerly). See daemon.go for the full
			// init-gating rationale.

			if strings.TrimSpace(projectPath) == "" {
				return fmt.Errorf("--path is required")
			}
			if cmd.Flags().Changed("since") && strings.TrimSpace(since) == "" {
				return fmt.Errorf("--since must not be empty")
			}

			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			appConfig, err := config.LoadConfig(resolvedConfigPath)
			if err != nil {
				return fmt.Errorf("load config %q: %w", resolvedConfigPath, err)
			}

			logRoot := outputDir
			if strings.TrimSpace(logRoot) == "" {
				logRoot = appConfig.Daemon.OutputRoot
			}
			logger, err := logging.New(logRoot, appConfig.Logging.Level, appConfig.Logging.MaxSizeMB)
			if err != nil {
				return err
			}
			defer func() { _ = logger.Close() }()

			logger.Info("analyze command started", logging.Any("config", resolvedConfigPath), logging.Any("path", projectPath), logging.Any("provider", providerID))
			logDefaultedSinceNotices(logger, appConfig)

			// --force bypasses the conflict guard so an operator can re-run
			// even while a daemon-scheduled job is in flight.
			if !force {
				if conflict := checkJobConflict(appConfig, projectPath); conflict != "" {
					return fmt.Errorf("%s", conflict)
				}
			}

			opts := pipeline.Options{
				Config:                 appConfig,
				ProjectPath:            projectPath,
				ProviderID:             providerID,
				Force:                  force,
				DryRun:                 dryRun,
				Permissive:             permissive,
				OutputDir:              outputDir,
				Since:                  since,
				ParallelOverride:       analyzerFlags.parallel,
				MaxConcurrencyOverride: analyzerFlags.jobs,
			}
			if cmd.Flags().Changed(flagChunkSize) {
				opts.MaxChunkBytesOverride = analyzerFlags.chunkSize
				opts.MaxChunkBytesOverrideSet = true
			}
			runResult, err := pipeline.Run(commandContext(cmd), opts, logger)
			if err != nil {
				logger.Error("analyze command failed", logging.Any("err", err))
				return err
			}

			if runResult.CacheHit {
				cmd.Printf("no changes (cache hit) provider=%s todos=%s\n", runResult.ProviderID, runResult.TodosPath)
				logger.Info("analyze cache hit", logging.Any("provider", runResult.ProviderID))
				return nil
			}

			if runResult.NoMistakes {
				cmd.Printf("no recurring mistakes found provider=%s todos=%s\n", runResult.ProviderID, runResult.TodosPath)
				logger.Info("analyze no mistakes", logging.Any("provider", runResult.ProviderID))
				return nil
			}
			if dryRun {
				cmd.Printf("dry-run complete provider=%s mistakes=%d\n", runResult.ProviderID, runResult.Mistakes)
				return nil
			}

			cmd.Printf(
				"analyze complete provider=%s sources=%d messages=%d mistakes=%d findings_added=%d warnings=%d todos=%s\n",
				runResult.ProviderID,
				runResult.SourcesAnalyzed,
				runResult.MessagesRead,
				runResult.Mistakes,
				runResult.Findings,
				runResult.Warnings,
				runResult.TodosPath,
			)
			logger.Info("analyze complete", logging.Any("provider", runResult.ProviderID), logging.Any("mistakes", runResult.Mistakes), logging.Any("findings", runResult.Findings), logging.Any("todos", runResult.TodosPath))
			return nil
		},
	}

	command.Flags().StringVar(&projectPath, "path", "", "Absolute project directory to analyze (required)")
	command.Flags().StringVarP(&providerID, "provider", "P", "", "Override the configured provider id")
	command.Flags().BoolVarP(&force, "force", "f", false, "Skip the incremental cache and re-analyze every discovered chat")
	command.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Run phase 1 only (mistake extraction); do not synthesize guardrails or write todos.md")
	command.Flags().BoolVar(&permissive, "permissive", false, "Disable strict lint-rule allow-list; emit unrecognised rule ids tagged [unverified]")
	command.Flags().StringVarP(&outputDir, "output-dir", "o", "", "Override the per-project output directory")
	command.Flags().StringVarP(&since, "since", "s", config.DefaultSince, "Lookback window for chat history (e.g. 30m, 1h, 1d, 1w, 1mo, lifetime)")
	registerAnalyzerFlags(command.Flags(), &analyzerFlags)
	_ = command.MarkFlagRequired("path")

	return command
}

func commandContext(cmd *cobra.Command) context.Context {
	if cmd == nil || cmd.Context() == nil {
		return context.Background()
	}
	return cmd.Context()
}

// checkJobConflict loads the job queue and checks whether a running or
// pending job exists for the given project path. Returns an empty string
// if no conflict; otherwise a human-readable error message.
func checkJobConflict(appConfig *config.Config, projectPath string) string {
	storePath := filepath.Join(appConfig.Daemon.OutputRoot, "jobs.json")
	queue := jobqueue.New(jobqueue.Options{StorePath: storePath})
	if err := queue.Recover(); err != nil {
		return "" // best-effort; don't block analyze on a corrupt queue
	}

	// Derive the project name the same way config.LoadConfig does: expand
	// `~` first, then Abs+Clean. Without ExpandUserHome, `dreamer analyze
	// --path ~/foo` would compare a literal "~" against the canonicalised
	// project paths and silently bypass the guard.
	expanded, err := config.ExpandUserHome(projectPath)
	if err != nil {
		return ""
	}
	absPath, err := filepath.Abs(expanded)
	if err != nil {
		return ""
	}
	absPath = filepath.Clean(absPath)

	// Note: this is a snapshot read. With max_concurrent_jobs > 1 the daemon
	// may dequeue or enqueue between this check and pipeline.Run, so the
	// guard is best-effort dedup, not a hard lock. A per-project lockfile
	// would close the window if it ever becomes a problem in practice.
	for _, p := range appConfig.Projects {
		if filepath.Clean(p.Path) == absPath {
			status := queue.Status()
			for _, j := range status.Jobs {
				if j.Project == p.Name && !j.Status.IsTerminal() {
					return fmt.Sprintf("analysis already in progress for %q (job %s, status: %s); pass --force to bypass this check", p.Name, j.ID, j.Status)
				}
			}
		}
	}
	return ""
}
