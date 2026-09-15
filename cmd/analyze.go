package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/jobqueue"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"github.com/spf13/cobra"
)

func newAnalyzeCommand() *cobra.Command {
	var (
		projectPath         string
		providerID          string
		force               bool
		dryRun              bool
		permissive          bool
		jsonOutput          bool
		outputDir           string
		since               string
		outputInProjectRoot bool
		analyzerFlags       analyzerFlagVars
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
				return missingFlagError(cmd, "path",
					"The project directory to analyze.",
					"dreamer analyze --path /path/to/project")
			}
			if cmd.Flags().Changed("since") && strings.TrimSpace(since) == "" {
				return invalidFlagValueError(cmd, "since", "",
					[]string{"30m", "1h", "1d", "1w", "1mo", "lifetime"})
			}

			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			overlayPath, _ := config.GlobalOverlayPath()
			appConfig, err := config.LoadConfigWithOverlay(resolvedConfigPath, overlayPath)
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
			warnIfWSLInteropWorkspace(cmd, logger, projectPath)

			// --force bypasses the conflict guard so an operator can re-run
			// even while a daemon-scheduled job is in flight.
			if !force {
				if err := checkJobConflict(cmd, appConfig, projectPath); err != nil {
					return err
				}
			}

			resolvedSince := since
			if !cmd.Flags().Changed("since") {
				resolvedSince = resolveProjectSince(appConfig, projectPath, since)
			}
			effectiveOutputInProjectRoot := outputInProjectRoot
			if !cmd.Flags().Changed("output-in-project-root") {
				effectiveOutputInProjectRoot = resolveOutputInProjectRoot(appConfig, projectPath)
			}

			opts := pipeline.Options{
				Config:                 appConfig,
				ProjectPath:            projectPath,
				ProviderID:             providerID,
				Force:                  force,
				DryRun:                 dryRun,
				Permissive:             permissive,
				OutputDir:              outputDir,
				Since:                  resolvedSince,
				OutputInProjectRoot:    effectiveOutputInProjectRoot,
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

			if jsonOutput {
				return printAnalyzeJSON(cmd, runResult, dryRun)
			}

			if runResult.CacheHit {
				cmd.Printf("no changes (cache hit) provider=%s todos=%s\n", runResult.ProviderID, runResult.TodosPath)
				logger.Info("analyze cache hit", logging.Any("provider", runResult.ProviderID))
				return nil
			}

			if !runResult.MistakesFound {
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
	command.Flags().BoolVar(&outputInProjectRoot, "output-in-project-root", false, "Write todos.md directly into the target project directory")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Machine-readable JSON output")
	registerAnalyzerFlags(command.Flags(), &analyzerFlags)
	return command
}

func commandContext(cmd *cobra.Command) context.Context {
	if cmd == nil || cmd.Context() == nil {
		return context.Background()
	}
	return cmd.Context()
}

// analyzeJSONResult is the --json output structure for the analyze command.
type analyzeJSONResult struct {
	Provider        string `json:"provider"`
	SourcesAnalyzed int    `json:"sources_analyzed"`
	MessagesRead    int    `json:"messages_read"`
	Mistakes        int    `json:"mistakes"`
	FindingsAdded   int    `json:"findings_added"`
	Warnings        int    `json:"warnings"`
	TodosPath       string `json:"todos_path"`
	CacheHit        bool   `json:"cache_hit"`
	MistakesFound   bool   `json:"mistakes_found"`
	DryRun          bool   `json:"dry_run"`
}

// printAnalyzeJSON writes the analyze result as JSON to stdout.
func printAnalyzeJSON(cmd *cobra.Command, result pipeline.Result, dryRun bool) error {
	out := analyzeJSONResult{
		Provider:        result.ProviderID,
		SourcesAnalyzed: result.SourcesAnalyzed,
		MessagesRead:    result.MessagesRead,
		Mistakes:        result.Mistakes,
		FindingsAdded:   result.Findings,
		Warnings:        result.Warnings,
		TodosPath:       result.TodosPath,
		CacheHit:        result.CacheHit,
		MistakesFound:   result.MistakesFound,
		DryRun:          dryRun,
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// checkJobConflict loads the job queue and checks whether a running or
// pending job exists for the given project path. Returns nil if no conflict;
// otherwise returns a styled conflictError.
func checkJobConflict(cmd *cobra.Command, appConfig *config.App, projectPath string) error {
	storePath := filepath.Join(appConfig.Daemon.OutputRoot, "jobs.json")
	queue := jobqueue.New(jobqueue.Options{StorePath: storePath})
	if err := queue.Recover(); err != nil {
		return nil // best-effort; don't block analyze on a corrupt queue
	}

	// Derive the project name the same way config.LoadConfig does: expand
	// `~` first, then Abs+Clean. Without ExpandUserHome, `dreamer analyze
	// --path ~/foo` would compare a literal "~" against the canonicalised
	// project paths and silently bypass the guard.
	canonTarget := fsutil.CanonicalPath(projectPath)

	// Note: this is a snapshot read. With max_concurrent_jobs > 1 the daemon
	// may dequeue or enqueue between this check and pipeline.Run, so the
	// guard is best-effort dedup, not a hard lock. A per-project lockfile
	// would close the window if it ever becomes a problem in practice.
	for _, p := range appConfig.Projects {
		if fsutil.CanonicalPath(p.Path) == canonTarget {
			status := queue.Status()
			for _, j := range status.Jobs {
				if j.Project == p.Name && !j.Status.IsTerminal() {
					return conflictError(cmd, p.Name, j.ID, string(j.Status))
				}
			}
		}
	}
	return nil
}

func resolveProjectSince(appConfig *config.App, projectPath, defaultSince string) string {
	if appConfig == nil {
		return defaultSince
	}
	canonTarget := fsutil.CanonicalPath(projectPath)
	for _, p := range appConfig.Projects {
		if fsutil.CanonicalPath(p.Path) == canonTarget && strings.TrimSpace(p.Since) != "" {
			return strings.TrimSpace(p.Since)
		}
	}
	return defaultSince
}

func resolveOutputInProjectRoot(appConfig *config.App, projectPath string) bool {
	if appConfig == nil {
		return false
	}
	canonTarget := fsutil.CanonicalPath(projectPath)
	for _, p := range appConfig.Projects {
		if fsutil.CanonicalPath(p.Path) == canonTarget {
			return p.OutputInProjectRoot || appConfig.OutputInProjectRoot
		}
	}
	return appConfig.OutputInProjectRoot
}
