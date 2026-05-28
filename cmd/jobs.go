package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dreamer/internal/analyzer"
	"dreamer/internal/backgroundjobs"
	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
)

const outputRootFlag = "output-root"

// defaultProviderFactory delegates to the real analyzer registry.
func defaultProviderFactory(id config.ProviderID, cfg analyzer.ProviderConfig) (analyzer.Provider, error) {
	return analyzer.NewProvider(id, cfg)
}

// resolveOutputRoot determines the output root from --output-root flag,
// falling back to config's output_root. Also returns the loaded config
// so callers that need it don't load config a second time.
func resolveOutputRoot(cmd *cobra.Command, resolvedConfigPath string) (string, *config.Config, error) {
	if flag := cmd.Flag(outputRootFlag); flag != nil && flag.Changed {
		abs, err := filepath.Abs(flag.Value.String())
		// Still need to load config for callers that use it.
		cfg, cfgErr := config.LoadConfig(resolvedConfigPath)
		if cfgErr != nil {
			return "", nil, fmt.Errorf("load config: %w", cfgErr)
		}
		return abs, cfg, err
	}

	cfg, err := config.LoadConfig(resolvedConfigPath)
	if err != nil {
		return "", nil, fmt.Errorf("load config: %w", err)
	}

	if cfg.Daemon.OutputRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, fmt.Errorf("resolve home dir: %w", err)
		}
		return filepath.Join(home, ".dreamer", "output"), cfg, nil
	}

	abs, err := filepath.Abs(cfg.Daemon.OutputRoot)
	return abs, cfg, err
}

func newJobsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "jobs",
		Short: "Manage background analysis jobs.",
		Long:  "Create, list, run, and manage recurring background analysis jobs.",
	}

	command.AddCommand(newJobsListCommand())
	command.AddCommand(newJobsCreateCommand())
	command.AddCommand(newJobsShowCommand())
	command.AddCommand(newJobsPauseCommand())
	command.AddCommand(newJobsResumeCommand())
	command.AddCommand(newJobsDeleteCommand())
	command.AddCommand(newJobsRunCommand())
	command.AddCommand(newJobsReconcileCommand())
	command.AddCommand(newJobsHealthCommand())

	return command
}

func newJobsListCommand() *cobra.Command {
	var (
		jsonOutput bool
		verbose    bool
		since      string
	)

	command := &cobra.Command{
		Use:   "list",
		Short: "List configured jobs.",
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
			store := backgroundjobs.NewStore(outputRoot, lg)
			state, err := store.Load()
			if err != nil {
				return fmt.Errorf("load jobs: %w", err)
			}

			// Parse --since filter.
			var sinceTime time.Time
			if since != "" {
				dur, err := time.ParseDuration(since)
				if err != nil {
					return fmt.Errorf("parse --since %q: %w", since, err)
				}
				sinceTime = time.Now().UTC().Add(-dur)
			}

			// Filter and collect.
			var jobs []*backgroundjobs.Job
			for _, j := range state.Jobs {
				if !sinceTime.IsZero() && j.CreatedAt.Before(sinceTime) {
					continue
				}
				jobs = append(jobs, j)
			}

			if jsonOutput {
				return printJobsJSON(cmd, jobs)
			}
			return printJobsTable(cmd, jobs, verbose)
		},
	}

	command.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON array.")
	command.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show additional columns (prompt, schedule).")
	command.Flags().StringVar(&since, "since", "", "Only show jobs created within this duration (e.g. 24h, 7d).")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")

	return command
}

func printJobsJSON(cmd *cobra.Command, jobs []*backgroundjobs.Job) error {
	if jobs == nil {
		jobs = []*backgroundjobs.Job{}
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(jobs)
}

func printJobsTable(cmd *cobra.Command, jobs []*backgroundjobs.Job, verbose bool) error {
	if len(jobs) == 0 {
		cmd.Println("No jobs found.")
		return nil
	}

	if verbose {
		cmd.Printf("%-18s %-20s %-12s %-8s %-19s %s\n",
			"ID", "NAME", "PROVIDER", "ENABLED", "NEXT_RUN", "PROMPT")
		for _, j := range jobs {
			nextRun := "-"
			if j.NextRunAt != nil {
				nextRun = j.NextRunAt.Format("2006-01-02 15:04 MST")
			}
			prompt := j.Prompt
			if len(prompt) > 40 {
				prompt = prompt[:40] + "..."
			}
			cmd.Printf("%-18s %-20s %-12s %-8t %-19s %s\n",
				j.ID, truncateWithEllipsis(j.Name, 20), j.ProviderID, j.Enabled, nextRun, prompt)
		}
	} else {
		cmd.Printf("%-18s %-20s %-12s %-8s %-19s\n",
			"ID", "NAME", "PROVIDER", "ENABLED", "NEXT_RUN")
		for _, j := range jobs {
			nextRun := "-"
			if j.NextRunAt != nil {
				nextRun = j.NextRunAt.Format("2006-01-02 15:04 MST")
			}
			cmd.Printf("%-18s %-20s %-12s %-8t %-19s\n",
				j.ID, truncateWithEllipsis(j.Name, 20), j.ProviderID, j.Enabled, nextRun)
		}
	}

	return nil
}

func formatSchedule(spec backgroundjobs.ScheduleSpec) string {
	s := string(spec.Kind)
	if spec.Every != "" {
		s += " every " + spec.Every
	} else if spec.TimeOfDay != "" {
		s += " at " + spec.TimeOfDay
	}
	if spec.DayOfWeek != "" {
		s = string(spec.Kind) + " " + spec.DayOfWeek
		if spec.TimeOfDay != "" {
			s += " at " + spec.TimeOfDay
		}
	}
	return s
}

func truncateWithEllipsis(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes-1]) + "~"
}

// buildScheduler creates a Scheduler from the resolved config path and output root.
// Returns nil + nil error if scheduler creation fails (non-fatal for CLI commands).
func buildScheduler(outputRoot, configPath string) (backgroundjobs.Scheduler, error) {
	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)

	execPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		execPath, _ = os.Executable()
	}

	installID, err := backgroundjobs.ResolveInstallID(store.Dir())
	if err != nil {
		return nil, fmt.Errorf("resolve install ID: %w", err)
	}

	cfg := backgroundjobs.SchedulerConfig{
		StoreDir:       store.Dir(),
		ExecutablePath: execPath,
		ConfigPath:     configPath,
		InstallID:      installID,
		ConfigHash:     backgroundjobs.HashConfigPath(configPath),
		ExecHash:       backgroundjobs.HashExecutablePath(execPath),
	}

	return backgroundjobs.NewScheduler(cfg, lg), nil
}

// createJobInput holds the resolved inputs for creating a background job.
type createJobInput struct {
	projectPath  string
	projectName  string
	name         string
	providerID   string
	model        string
	prompt       string
	fileAccess   string // read_only, selected_writes, full_workspace
	writablePaths string // comma-separated, for selected_writes
	schedule     backgroundjobs.ScheduleSpec
	timezone     string
}

// createAndSaveJob validates, builds, and persists a background job.
func createAndSaveJob(cmd *cobra.Command, outputRoot, configPath string, input createJobInput, dryRun bool) error {
	// Validate provider is background-safe.
	meta := backgroundjobs.ProviderMetaByID(input.providerID)
	if meta == nil {
		return fmt.Errorf("provider %q is not registered", input.providerID)
	}
	if !meta.BackgroundSafe {
		return fmt.Errorf("provider %q is not safe for background execution (requires interactive terminal)", input.providerID)
	}

	// Validate schedule.
	if err := backgroundjobs.ValidateSchedule(input.schedule); err != nil {
		return fmt.Errorf("invalid schedule: %w", err)
	}

	// Build job.
	now := time.Now().UTC()
	jobID, err := backgroundjobs.GenerateJobID()
	if err != nil {
		return fmt.Errorf("generate job id: %w", err)
	}

	// Resolve file access mode.
	fileAccess := backgroundjobs.FileAccessReadOnly
	switch input.fileAccess {
	case "selected_writes":
		fileAccess = backgroundjobs.FileAccessSelectedWrites
	case "full_workspace":
		fileAccess = backgroundjobs.FileAccessFullWorkspace
	}

	// Parse writable paths.
	var writablePaths []string
	if input.writablePaths != "" {
		for _, p := range strings.Split(input.writablePaths, ",") {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				writablePaths = append(writablePaths, trimmed)
			}
		}
	}

	job := &backgroundjobs.Job{
		ID:          jobID,
		Name:        input.name,
		Prompt:      input.prompt,
		ProjectName: input.projectName,
		ProjectPath: input.projectPath,
		ProviderID:  input.providerID,
		Model:       input.model,
		Schedule:    input.schedule,
		Enabled:     true,
		CreatedAt:   now,
		UpdatedAt:   now,
		Permissions: backgroundjobs.PermissionProfile{
			FileAccess:    fileAccess,
			WritablePaths: writablePaths,
			ReadScope:     "project_dir",
		},
		Health: backgroundjobs.HealthState{
			SystemScheduling: backgroundjobs.SchedulingNotInstalled,
			JobSchedule:      backgroundjobs.JobScheduleValid,
			RunState:         backgroundjobs.RunStatusIdle,
			PermissionState:  backgroundjobs.PermissionAllowed,
		},
	}

	if dryRun {
		return printJobDetail(cmd, job)
	}

	// Calculate next run.
	nextRun, err := backgroundjobs.NextRun(input.schedule, now)
	if err != nil {
		return fmt.Errorf("calculate next run: %w", err)
	}
	job.NextRunAt = &nextRun

	// Save.
	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	if err := store.AddJob(job); err != nil {
		return fmt.Errorf("save job: %w", err)
	}

	// Install OS schedule (best-effort).
	if sched, schedErr := buildScheduler(outputRoot, configPath); schedErr == nil {
		params := backgroundjobs.ScheduleParams{
			JobID:    jobID,
			Schedule: input.schedule,
			Name:     backgroundjobs.SanitizeScheduleName(input.name),
			Enabled:  true,
		}
		osState, installErr := sched.Install(cmd.Context(), params)
		if installErr != nil {
			lg.Warn("install OS schedule failed", logging.Any("error", installErr))
		} else {
			_ = store.Update(cmd.Context(), func(s *backgroundjobs.State) error {
				if j := s.Jobs[jobID]; j != nil {
					j.OSSchedule = osState
				}
				return nil
			})
		}
	}

	// Audit.
	audit := backgroundjobs.NewAuditWriter(store.Dir())
	_ = audit.Write(backgroundjobs.AuditEvent{
		Event: "job.created",
		JobID: jobID,
		Actor: "cli",
	})

	cmd.Printf("job created: %s\n", jobID)
	return nil
}

func newJobsCreateCommand() *cobra.Command {
	var (
		name         string
		prompt       string
		providerID   string
		model        string
		scheduleKind string
		every        string
		timeOfDay    string
		dayOfWeek    string
		cron         string
		timezone     string
		fileAccess   string
		writablePaths string
		dryRun       bool
		interactive  bool
	)

	command := &cobra.Command{
		Use:   "create [project-path]",
		Short: "Create a new background job.",
		Long:  "Create a new background job. Launches an interactive wizard when run with no arguments or with --interactive.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}

			outputRoot, cfg, err := resolveOutputRoot(cmd, resolvedConfigPath)
			if err != nil {
				return err
			}

			// Determine default provider for the wizard.
			defaultProvider := cfg.DefaultProvider
			if defaultProvider == "" {
				defaultProvider = string(config.DefaultProviderID)
			}

			// Decide whether to launch the interactive wizard.
			hasPath := len(args) > 0
			hasPrompt := cmd.Flags().Changed("prompt")
			launchWizard := interactive || (!hasPath && !hasPrompt)

			if launchWizard {
				return runInteractiveCreate(cmd, outputRoot, resolvedConfigPath, defaultProvider, args, jobWizardAnswers{
					providerID:   providerID,
					name:         name,
					prompt:       prompt,
					fileAccess:   fileAccess,
					writablePaths: writablePaths,
					scheduleKind: scheduleKind,
					every:        every,
					timeOfDay:    timeOfDay,
					dayOfWeek:    dayOfWeek,
					cron:         cron,
					timezone:     timezone,
				})
			}

			// Non-interactive path.
			if len(args) == 0 {
				return fmt.Errorf("project path is required (or use --interactive for the wizard)")
			}

			projectPath, err := resolveProjectPath(args[0])
			if err != nil {
				return err
			}
			if info, statErr := os.Stat(projectPath); statErr != nil || !info.IsDir() {
				return fmt.Errorf("project path %q is not a directory", projectPath)
			}

			if prompt == "" {
				return fmt.Errorf("--prompt is required (or use --interactive for the wizard)")
			}
			if name == "" {
				name = filepath.Base(projectPath)
			}
			if timezone == "" {
				timezone = "UTC"
			}
			if every != "" && scheduleKind != "interval" {
				return fmt.Errorf("--every is only valid with --schedule interval")
			}
			if scheduleKind == "cron" && runtime.GOOS == "windows" {
				return fmt.Errorf("cron schedules are not supported on Windows; use --schedule daily or --schedule weekly instead")
			}
			if fileAccess != "" {
				switch fileAccess {
				case "read_only", "selected_writes", "full_workspace":
				default:
					return fmt.Errorf("--file-access must be one of: read_only, selected_writes, full_workspace")
				}
			}
			if writablePaths != "" && fileAccess != "selected_writes" {
				return fmt.Errorf("--writable-paths requires --file-access selected_writes")
			}

			// Resolve provider.
			if providerID == "" {
				providerID = defaultProvider
			}

			// Build schedule.
			kind := backgroundjobs.ScheduleKind(scheduleKind)
			schedule := backgroundjobs.ScheduleSpec{
				Kind:      kind,
				Every:     every,
				TimeOfDay: timeOfDay,
				DayOfWeek: dayOfWeek,
				Cron:      cron,
				Timezone:  timezone,
			}

			projectName := pipeline.DeriveProjectName(projectPath, nil)

			return createAndSaveJob(cmd, outputRoot, resolvedConfigPath, createJobInput{
				projectPath:  projectPath,
				projectName:  projectName,
				name:         name,
				providerID:   providerID,
				model:        model,
				prompt:       prompt,
				fileAccess:   fileAccess,
				writablePaths: writablePaths,
				schedule:     schedule,
				timezone:     timezone,
			}, dryRun)
		},
	}

	command.Flags().StringVarP(&name, "name", "n", "", "Human-readable name (default: project directory name).")
	command.Flags().StringVarP(&prompt, "prompt", "p", "", "Prompt to execute on each run.")
	command.Flags().StringVar(&providerID, "provider", "", "Analyzer provider ID (default: config default).")
	command.Flags().StringVarP(&model, "model", "m", "", "Override model for this job.")
	command.Flags().StringVarP(&scheduleKind, "schedule", "s", "daily", "Schedule kind: interval|daily|weekly|cron.")
	command.Flags().StringVar(&every, "every", "", "Repeat interval for interval schedule (e.g. 5m, 15m, 2h). Default: 1h.")
	command.Flags().StringVar(&timeOfDay, "time-of-day", "09:00", "Time of day for daily/weekly (HH:MM).")
	command.Flags().StringVar(&dayOfWeek, "day-of-week", "", "Day of week for weekly schedule.")
	command.Flags().StringVar(&cron, "cron", "", "Cron expression for cron schedule (5 fields, e.g. '0 9 * * 1').")
	command.Flags().StringVar(&timezone, "timezone", "UTC", "Timezone for schedule (IANA format).")
	command.Flags().StringVar(&fileAccess, "file-access", "", "File access: read_only, selected_writes, full_workspace (default: read_only).")
	command.Flags().StringVar(&writablePaths, "writable-paths", "", "Comma-separated writable paths (for selected_writes).")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Validate inputs, resolve provider, print job definition without saving.")
	command.Flags().BoolVarP(&interactive, "interactive", "i", false, "Launch the interactive job creation wizard.")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")

	return command
}

// runInteractiveCreate launches the TUI wizard and creates the job from the result.
func runInteractiveCreate(cmd *cobra.Command, outputRoot, configPath, defaultProvider string, args []string, prefilled jobWizardAnswers) error {
	// Pre-fill path from arg if provided.
	if len(args) > 0 {
		projectPath, err := resolveProjectPath(args[0])
		if err != nil {
			return err
		}
		prefilled.projectPath = projectPath
	}

	answers, confirmed, err := runJobWizard(prefilled)
	if err != nil {
		return err
	}
	if !confirmed {
		cmd.Println("job creation cancelled.")
		return nil
	}

	return createAndSaveJob(cmd, outputRoot, configPath, jobAnswersToCreateInput(answers, defaultProvider), false)
}

func newJobsShowCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "show <job-id>",
		Short: "Show job details and recent runs.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}

			outputRoot, _, err := resolveOutputRoot(cmd, resolvedConfigPath)
			if err != nil {
				return err
			}

			jobID := args[0]
			lg := logging.Silent()
			store := backgroundjobs.NewStore(outputRoot, lg)
			state, err := store.Load()
			if err != nil {
				return fmt.Errorf("load jobs: %w", err)
			}

			job := state.Jobs[jobID]
			if job == nil {
				return fmt.Errorf("job %q not found", jobID)
			}

			if err := printJobDetail(cmd, job); err != nil {
				return err
			}

			// Show recent runs.
			runStore := backgroundjobs.NewRunStore(store.Dir(), lg)
			runs, err := runStore.List(jobID)
			if err != nil {
				return fmt.Errorf("load runs: %w", err)
			}

			if len(runs) == 0 {
				cmd.Println("\nNo runs recorded.")
			} else {
				cmd.Printf("\nRecent runs (last 10):\n")
				cmd.Printf("%-20s %-12s %-20s %-10s %s\n",
					"STARTED_AT", "STATUS", "FINISHED_AT", "DURATION", "RUN_ID")
				limit := len(runs)
				if limit > 10 {
					limit = 10
				}
				for i := 0; i < limit; i++ {
					r := runs[i]
					started := r.StartedAt.Format("2006-01-02 15:04:05")
					finished := "-"
					duration := "-"
					if r.FinishedAt != nil {
						finished = r.FinishedAt.Format("2006-01-02 15:04:05")
						duration = fmt.Sprintf("%dms", r.DurationMillis)
					}
					cmd.Printf("%-20s %-12s %-20s %-10s %s\n",
						started, r.Status, finished, duration, r.ID)
				}
			}

			return nil
		},
	}

	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")

	return command
}

func printJobDetail(cmd *cobra.Command, job *backgroundjobs.Job) error {
	nextRun := "-"
	if job.NextRunAt != nil {
		nextRun = job.NextRunAt.Format("2006-01-02 15:04:05 MST")
	}
	lastRun := "-"
	if job.LastRunAt != nil {
		lastRun = job.LastRunAt.Format("2006-01-02 15:04:05 MST")
	}

	cmd.Printf("ID:          %s\n", job.ID)
	cmd.Printf("Name:        %s\n", job.Name)
	cmd.Printf("Project:     %s\n", job.ProjectPath)
	cmd.Printf("Provider:    %s\n", job.ProviderID)
	if job.Model != "" {
		cmd.Printf("Model:       %s\n", job.Model)
	}
	cmd.Printf("Schedule:    %s", job.Schedule.Kind)
	if job.Schedule.Every != "" {
		cmd.Printf(" every %s", job.Schedule.Every)
	} else if job.Schedule.TimeOfDay != "" {
		cmd.Printf(" at %s", job.Schedule.TimeOfDay)
	}
	if job.Schedule.DayOfWeek != "" {
		cmd.Printf(" on %s", job.Schedule.DayOfWeek)
	}
	cmd.Printf(" (%s)\n", job.Schedule.Timezone)
	cmd.Printf("Enabled:     %t\n", job.Enabled)
	cmd.Printf("Next Run:    %s\n", nextRun)
	cmd.Printf("Last Run:    %s\n", lastRun)
	cmd.Printf("Permissions: %s", job.Permissions.FileAccess)
	if len(job.Permissions.WritablePaths) > 0 {
		cmd.Printf(" (%s)", strings.Join(job.Permissions.WritablePaths, ", "))
	}
	cmd.Printf("\n")
	cmd.Printf("\nPrompt:\n%s\n", job.Prompt)
	return nil
}

func newJobsPauseCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "pause <job-id>",
		Short: "Disable a job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setJobEnabled(cmd, args[0], false)
		},
	}
}

func newJobsResumeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "resume <job-id>",
		Short: "Re-enable a paused job.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setJobEnabled(cmd, args[0], true)
		},
	}
}

func setJobEnabled(cmd *cobra.Command, jobID string, enabled bool) error {
	resolvedConfigPath, err := resolveConfigPath(configPath)
	if err != nil {
		return err
	}

	outputRoot, _, err := resolveOutputRoot(cmd, resolvedConfigPath)
	if err != nil {
		return err
	}

	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)

	if err := store.Update(context.Background(), func(s *backgroundjobs.State) error {
		job := s.Jobs[jobID]
		if job == nil {
			return fmt.Errorf("job %q not found", jobID)
		}
		job.Enabled = enabled
		job.UpdatedAt = time.Now().UTC()
		return nil
	}); err != nil {
		return err
	}

	// Update OS schedule (best-effort).
	if sched, schedErr := buildScheduler(outputRoot, resolvedConfigPath); schedErr == nil {
		state, loadErr := store.Load()
		if loadErr == nil {
			job := state.Jobs[jobID]
			if job != nil {
				if enabled {
					// Reinstall schedule.
					params := backgroundjobs.ScheduleParams{
						JobID:    jobID,
						Schedule: job.Schedule,
						Name:     backgroundjobs.SanitizeScheduleName(job.Name),
						Enabled:  true,
					}
					osState, installErr := sched.Install(cmd.Context(), params)
					if installErr != nil {
						lg.Warn("install OS schedule failed", logging.Any("error", installErr))
					} else {
						_ = store.Update(cmd.Context(), func(s *backgroundjobs.State) error {
							if j := s.Jobs[jobID]; j != nil {
								j.OSSchedule = osState
							}
							return nil
						})
					}
				} else {
					// Remove OS schedule for paused jobs.
					if removeErr := sched.Remove(cmd.Context(), jobID); removeErr != nil {
						lg.Warn("remove OS schedule failed", logging.Any("error", removeErr))
					} else {
						_ = store.Update(cmd.Context(), func(s *backgroundjobs.State) error {
							if j := s.Jobs[jobID]; j != nil {
								j.OSSchedule = backgroundjobs.OSScheduleState{}
							}
							return nil
						})
					}
				}
			}
		}
	}

	if enabled {
		cmd.Printf("job resumed: %s\n", jobID)
	} else {
		cmd.Printf("job paused: %s\n", jobID)
	}
	return nil
}

func newJobsDeleteCommand() *cobra.Command {
	var yes bool

	command := &cobra.Command{
		Use:   "delete <job-id>",
		Short: "Permanently delete a job and its run history.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}

			outputRoot, _, err := resolveOutputRoot(cmd, resolvedConfigPath)
			if err != nil {
				return err
			}

			jobID := args[0]
			lg := logging.Silent()
			store := backgroundjobs.NewStore(outputRoot, lg)

			// Check job exists.
			state, err := store.Load()
			if err != nil {
				return fmt.Errorf("load jobs: %w", err)
			}
			if state.Jobs[jobID] == nil {
				return fmt.Errorf("job %q not found", jobID)
			}

			if !yes {
				cmd.Printf("Delete job %q? This cannot be undone.\n", jobID)
				cmd.Printf("Use --yes to confirm.\n")
				return nil
			}

			// Remove OS schedule before deleting the job (best-effort).
			if sched, schedErr := buildScheduler(outputRoot, resolvedConfigPath); schedErr == nil {
				if removeErr := sched.Remove(cmd.Context(), jobID); removeErr != nil {
					lg.Warn("remove OS schedule failed", logging.Any("error", removeErr))
				}
			}

			if err := store.DeleteJob(jobID); err != nil {
				return fmt.Errorf("delete job: %w", err)
			}

			// Audit.
			audit := backgroundjobs.NewAuditWriter(store.Dir())
			_ = audit.Write(backgroundjobs.AuditEvent{
				Event: "job.deleted",
				JobID: jobID,
				Actor: "cli",
			})

			cmd.Printf("job deleted: %s\n", jobID)
			return nil
		},
	}

	command.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt.")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")

	return command
}
