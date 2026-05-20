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
		configPath     string
		projectPath    string
		providerID     string
		force          bool
		dryRun         bool
		permissive     bool
		outputDir      string
		since          string
		parallel       bool
		maxConcurrency int
		maxChunkBytes  int
	)

	command := &cobra.Command{
		Use:   "analyze",
		Short: "Run one analysis pass for a single project path.",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			cfg, err := config.LoadConfig(resolvedConfigPath)
			if err != nil {
				return fmt.Errorf("load config %q: %w", resolvedConfigPath, err)
			}

			logRoot := outputDir
			if strings.TrimSpace(logRoot) == "" {
				logRoot = cfg.Daemon.OutputRoot
			}
			logger, err := logging.New(logRoot, cfg.Logging.Level, cfg.Logging.MaxSizeMB)
			if err != nil {
				return err
			}
			defer func() { _ = logger.Close() }()

			logger.Info("analyze command started", logging.Any("config", resolvedConfigPath), logging.Any("path", projectPath), logging.Any("provider", providerID))
			logDefaultedSinceNotices(logger, cfg)

			// --force bypasses the conflict guard so an operator can re-run
			// even while a daemon-scheduled job is in flight.
			if !force {
				if conflict := checkJobConflict(cfg, projectPath); conflict != "" {
					return fmt.Errorf("%s", conflict)
				}
			}

			opts := pipeline.Options{
				Config:                 cfg,
				ProjectPath:            projectPath,
				ProviderID:             providerID,
				Force:                  force,
				DryRun:                 dryRun,
				Permissive:             permissive,
				OutputDir:              outputDir,
				Since:                  since,
				ParallelOverride:       parallel,
				MaxConcurrencyOverride: maxConcurrency,
			}
			if cmd.Flags().Changed("max-chunk-bytes") {
				opts.MaxChunkBytesOverride = maxChunkBytes
				opts.MaxChunkBytesOverrideSet = true
			}
			result, err := pipeline.Run(commandContext(cmd), opts, logger)
			if err != nil {
				logger.Error("analyze command failed", logging.Any("err", err))
				return err
			}

			if result.CacheHit {
				cmd.Printf("no changes (cache hit) provider=%s todos=%s\n", result.ProviderID, result.TodosPath)
				logger.Info("analyze cache hit", logging.Any("provider", result.ProviderID))
				return nil
			}

			if result.NoMistakes {
				cmd.Printf("no recurring mistakes found provider=%s todos=%s\n", result.ProviderID, result.TodosPath)
				logger.Info("analyze no mistakes", logging.Any("provider", result.ProviderID))
				return nil
			}
			if dryRun {
				cmd.Printf("dry-run complete provider=%s mistakes=%d\n", result.ProviderID, result.Mistakes)
				return nil
			}

			cmd.Printf(
				"analyze complete provider=%s sources=%d messages=%d mistakes=%d findings_added=%d warnings=%d todos=%s\n",
				result.ProviderID,
				result.SourcesAnalyzed,
				result.MessagesRead,
				result.Mistakes,
				result.Findings,
				result.Warnings,
				result.TodosPath,
			)
			logger.Info("analyze complete", logging.Any("provider", result.ProviderID), logging.Any("mistakes", result.Mistakes), logging.Any("findings", result.Findings), logging.Any("todos", result.TodosPath))
			return nil
		},
	}

	command.Flags().StringVar(&configPath, "config", "", "Path to global config file (default: <UserConfigDir>/dreamer/config.yaml)")
	command.Flags().StringVar(&projectPath, "path", "", "Absolute project directory to analyze (required)")
	command.Flags().StringVar(&providerID, "provider", "", "Override the configured provider id")
	command.Flags().BoolVar(&force, "force", false, "Skip the incremental cache and re-analyze every discovered chat")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Run phase 1 only (mistake extraction); do not synthesize guardrails or write todos.md")
	command.Flags().BoolVar(&permissive, "permissive", false, "Disable strict lint-rule allow-list; emit unrecognised rule ids tagged [unverified]")
	command.Flags().StringVar(&outputDir, "output-dir", "", "Override the per-project output directory")
	command.Flags().StringVar(&since, "since", config.DefaultSince, "Lookback window (e.g. 30m, 1h, 1d, 1w, 1mo, lifetime). 'lifetime' disables filtering.")
	command.Flags().BoolVar(&parallel, "parallel", false, "Force analyzer.execution.mode=parallel for this run (provider must implement ParallelCapable; otherwise falls back to sequential with a warning)")
	command.Flags().IntVar(&maxConcurrency, "max-concurrency", 0, "Cap parallel session count. 0 = len(chunks). Ignored when sequential.")
	command.Flags().IntVar(&maxChunkBytes, "max-chunk-bytes", 0, "Override analyzer.chunking.max_chunk_bytes for this run. 0 disables chunking (single chunk regardless of size).")
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
func checkJobConflict(cfg *config.Config, projectPath string) string {
	storePath := filepath.Join(cfg.Daemon.OutputRoot, "jobs.json")
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
	for _, p := range cfg.Projects {
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
