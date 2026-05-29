package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/config"
	"dreamer/internal/logging"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// View modes for the interactive jobs TUI.
const (
	jobsViewList   = "list"
	jobsViewDetail = "detail"
)

// jobsInteractiveModel is the bubbletea model for the interactive jobs dashboard.
type jobsInteractiveModel struct {
	store      *backgroundjobs.Store
	runStore   *backgroundjobs.RunStore
	scheduler  backgroundjobs.Scheduler
	configPath string
	outputRoot string

	jobs   []*backgroundjobs.Job // sorted by name
	cursor int                   // selected row index
	width  int
	height int

	// View state.
	view      string // jobsViewList or jobsViewDetail
	detailJob *backgroundjobs.Job

	// Confirmation overlay.
	confirmActive  bool
	confirmMsg     string
	confirmAction  func() tea.Cmd // runs on 'y'
	confirmRunning bool           // true while a background action is in flight

	// Status flash message.
	statusMsg string

	// Quit signal.
	quit bool

	// Pending action to execute after TUI exits (create/edit wizard).
	pendingAction *jobsNextAction
}

// jobsNextAction describes a wizard to launch after the TUI exits.
type jobsNextAction struct {
	kind string // "create" or "edit"
	job  *backgroundjobs.Job
}

// jobsStatusMsg carries a flash message back to the model after an async action.
type jobsStatusMsg struct {
	msg string
}

// jobsRefreshMsg signals that the job list should be reloaded.
type jobsRefreshMsg struct{}

// jobsRunResultMsg carries the result of a background job execution.
type jobsRunResultMsg struct {
	jobID  string
	status string
	err    error
}

// runJobsInteractive launches the full-screen interactive jobs dashboard.
// After the TUI exits, if a pending action (create/edit) was requested,
// the wizard runs and the TUI relaunches — looping until the user quits.
func runJobsInteractive(cmd *cobra.Command) error {
	resolvedConfigPath, err := resolveConfigPath(configPath)
	if err != nil {
		return err
	}

	outputRoot, cfg, err := resolveOutputRoot(cmd, resolvedConfigPath)
	if err != nil {
		return err
	}

	defaultProvider := cfg.DefaultProvider
	if defaultProvider == "" {
		defaultProvider = string(config.DefaultProviderID)
	}

	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)
	runStore := backgroundjobs.NewRunStore(store.Dir(), lg)

	var sched backgroundjobs.Scheduler
	if s, schedErr := buildScheduler(outputRoot, resolvedConfigPath); schedErr == nil {
		sched = s
	}

	for {
		m := jobsInteractiveModel{
			store:      store,
			runStore:   runStore,
			scheduler:  sched,
			configPath: resolvedConfigPath,
			outputRoot: outputRoot,
			view:       jobsViewList,
		}

		// Load initial jobs.
		if err := m.reloadJobs(); err != nil {
			return fmt.Errorf("load jobs: %w", err)
		}

		final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
		if err != nil {
			return fmt.Errorf("run interactive: %w", err)
		}

		result := final.(jobsInteractiveModel)
		if result.quit || result.pendingAction == nil {
			return nil
		}

		// Execute the pending action (wizard), then loop back to TUI.
		action := result.pendingAction
		switch action.kind {
		case "create":
			if err := runJobWizardAndCreate(cmd, outputRoot, resolvedConfigPath, defaultProvider); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
		case "edit":
			if err := runEditWizard(cmd, outputRoot, resolvedConfigPath, defaultProvider, action.job); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
		}
		// Loop — TUI relaunches with fresh data.
	}
}

// runJobWizardAndCreate launches the job creation wizard and creates the job.
func runJobWizardAndCreate(cmd *cobra.Command, outputRoot, configPath, defaultProvider string) error {
	merged := jobWizardAnswers{}
	answers, confirmed, err := runJobWizard(merged)
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}
	return createAndSaveJob(cmd, outputRoot, configPath, jobAnswersToCreateInput(answers, defaultProvider), false)
}

// runEditWizard launches the job creation wizard pre-filled from an existing job,
// then applies the changes as an edit.
func runEditWizard(cmd *cobra.Command, outputRoot, configPath, defaultProvider string, job *backgroundjobs.Job) error {
	answers, confirmed, err := runJobWizard(jobToWizardAnswers(job))
	if err != nil {
		return err
	}
	if !confirmed {
		return nil
	}

	input := jobAnswersToCreateInput(answers, defaultProvider)

	lg := logging.Silent()
	store := backgroundjobs.NewStore(outputRoot, lg)

	scheduleChanged := false

	if err := store.Update(context.Background(), func(s *backgroundjobs.State) error {
		j := s.Jobs[job.ID]
		if j == nil {
			return fmt.Errorf("job not found")
		}
		j.Name = input.name
		j.Prompt = input.prompt
		j.ProviderID = input.providerID
		j.Model = input.model
		j.Permissions.FileAccess = backgroundjobs.FileAccessMode(input.fileAccess)
		if input.writablePaths != "" {
			var paths []string
			for _, p := range strings.Split(input.writablePaths, ",") {
				if trimmed := strings.TrimSpace(p); trimmed != "" {
					paths = append(paths, trimmed)
				}
			}
			j.Permissions.WritablePaths = paths
		}
		if j.Schedule != input.schedule {
			j.Schedule = input.schedule
			scheduleChanged = true
		}
		j.UpdatedAt = time.Now().UTC()
		if scheduleChanged {
			nextRun, err := backgroundjobs.NextRun(input.schedule, time.Now().UTC())
			if err == nil {
				j.NextRunAt = &nextRun
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("update job: %w", err)
	}

	// Reinstall OS schedule if changed (best-effort).
	if scheduleChanged {
		if sched, schedErr := buildScheduler(outputRoot, configPath); schedErr == nil {
			params := backgroundjobs.ScheduleParams{
				JobID:    job.ID,
				Schedule: input.schedule,
				Name:     backgroundjobs.SanitizeScheduleName(input.name),
				Enabled:  job.Enabled,
			}
			osState, installErr := sched.Install(context.Background(), params)
			if installErr == nil {
				_ = store.Update(context.Background(), func(s *backgroundjobs.State) error {
					if j := s.Jobs[job.ID]; j != nil {
						j.OSSchedule = osState
					}
					return nil
				})
			}
		}
	}

	audit := backgroundjobs.NewAuditWriter(store.Dir())
	_ = audit.Write(backgroundjobs.AuditEvent{
		Event: "job.edit",
		JobID: job.ID,
		Actor: "interactive",
	})

	return nil
}

// jobToWizardAnswers converts a Job to wizard answers for pre-filling the edit wizard.
func jobToWizardAnswers(j *backgroundjobs.Job) jobWizardAnswers {
	return jobWizardAnswers{
		projectPath:   j.ProjectPath,
		name:          j.Name,
		providerID:    j.ProviderID,
		model:         j.Model,
		prompt:        j.Prompt,
		fileAccess:    string(j.Permissions.FileAccess),
		writablePaths: strings.Join(j.Permissions.WritablePaths, ","),
		scheduleKind:  string(j.Schedule.Kind),
		every:         j.Schedule.Every,
		timeOfDay:     j.Schedule.TimeOfDay,
		dayOfWeek:     j.Schedule.DayOfWeek,
		cron:          j.Schedule.Cron,
		timezone:      j.Schedule.Timezone,
	}
}

// reloadJobs loads the current job list from the store and sorts by name.
func (m *jobsInteractiveModel) reloadJobs() error {
	state, err := m.store.Load()
	if err != nil {
		return err
	}
	m.jobs = m.jobs[:0]
	for _, j := range state.Jobs {
		m.jobs = append(m.jobs, j)
	}
	sort.Slice(m.jobs, func(i, k int) bool {
		return strings.ToLower(m.jobs[i].Name) < strings.ToLower(m.jobs[k].Name)
	})
	// Clamp cursor.
	if m.cursor >= len(m.jobs) {
		m.cursor = len(m.jobs) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	return nil
}

// selectedJob returns the job under the cursor, or nil if the list is empty.
func (m *jobsInteractiveModel) selectedJob() *backgroundjobs.Job {
	if m.cursor < 0 || m.cursor >= len(m.jobs) {
		return nil
	}
	return m.jobs[m.cursor]
}

// Init implements tea.Model.
func (m jobsInteractiveModel) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m jobsInteractiveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		// Confirmation overlay intercepts all keys.
		if m.confirmActive {
			return m.updateConfirm(msg)
		}
		switch m.view {
		case jobsViewList:
			return m.updateList(msg)
		case jobsViewDetail:
			return m.updateDetail(msg)
		}

	case jobsStatusMsg:
		m.statusMsg = msg.msg
		return m, nil

	case jobsRefreshMsg:
		_ = m.reloadJobs()
		m.statusMsg = ""
		return m, nil

	case jobsRunResultMsg:
		m.confirmRunning = false
		if msg.err != nil {
			m.statusMsg = fmt.Sprintf("Run failed: %v", msg.err)
		} else {
			m.statusMsg = fmt.Sprintf("Job %s finished: %s", msg.jobID, msg.status)
		}
		_ = m.reloadJobs()
		return m, nil
	}

	return m, nil
}

// updateConfirm handles key input while the confirmation overlay is active.
func (m jobsInteractiveModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirmRunning {
		// Action in flight — ignore keys except ctrl+c.
		if msg.String() == "ctrl+c" {
			m.quit = true
			return m, tea.Quit
		}
		return m, nil
	}

	switch msg.String() {
	case "y", "Y":
		m.confirmActive = false
		if m.confirmAction != nil {
			cmd := m.confirmAction()
			m.confirmAction = nil
			return m, cmd
		}
		m.confirmAction = nil
		return m, nil
	case "n", "N", "esc":
		m.confirmActive = false
		m.confirmAction = nil
		m.statusMsg = "Cancelled."
		return m, nil
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit
	}
	return m, nil
}

// updateList handles key input in the list view.
func (m jobsInteractiveModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.quit = true
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		if m.cursor < len(m.jobs)-1 {
			m.cursor++
		}
		return m, nil

	case "enter":
		job := m.selectedJob()
		if job != nil {
			m.view = jobsViewDetail
			m.detailJob = job
			m.statusMsg = ""
		}
		return m, nil

	case " ":
		// Toggle enabled/paused.
		job := m.selectedJob()
		if job != nil {
			return m, m.makeToggleCmd(job)
		}
		return m, nil

	case "n":
		// Create new job — exits TUI, runs wizard, returns.
		return m, m.makeCreateCmd()

	case "e":
		// Edit selected job.
		job := m.selectedJob()
		if job != nil {
			return m, m.makeEditCmd(job)
		}
		return m, nil

	case "d":
		// Delete with confirmation.
		job := m.selectedJob()
		if job != nil {
			m.confirmActive = true
			m.confirmMsg = fmt.Sprintf("Delete job %q? This cannot be undone. [y/N]", job.Name)
			m.confirmAction = m.makeDeleteCmd(job)
		}
		return m, nil

	case "r":
		// Run job immediately.
		job := m.selectedJob()
		if job != nil {
			m.confirmActive = true
			m.confirmMsg = fmt.Sprintf("Run job %q now? [y/N]", job.Name)
			m.confirmAction = m.makeRunCmd(job)
		}
		return m, nil
	}

	return m, nil
}

// updateDetail handles key input in the detail view.
func (m jobsInteractiveModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit

	case "esc", "backspace":
		m.view = jobsViewList
		m.detailJob = nil
		return m, nil

	case " ":
		job := m.detailJob
		if job != nil {
			return m, m.makeToggleCmd(job)
		}
		return m, nil

	case "e":
		job := m.detailJob
		if job != nil {
			return m, m.makeEditCmd(job)
		}
		return m, nil

	case "d":
		job := m.detailJob
		if job != nil {
			m.confirmActive = true
			m.confirmMsg = fmt.Sprintf("Delete job %q? This cannot be undone. [y/N]", job.Name)
			m.confirmAction = m.makeDeleteCmd(job)
		}
		return m, nil

	case "r":
		job := m.detailJob
		if job != nil {
			m.confirmActive = true
			m.confirmMsg = fmt.Sprintf("Run job %q now? [y/N]", job.Name)
			m.confirmAction = m.makeRunCmd(job)
		}
		return m, nil
	}

	return m, nil
}

// ── Action commands ────────────────────────────────────────────────────────

// makeToggleCmd returns a tea.Cmd that toggles a job's enabled state.
func (m *jobsInteractiveModel) makeToggleCmd(job *backgroundjobs.Job) tea.Cmd {
	return func() tea.Msg {
		newEnabled := !job.Enabled
		if err := m.store.Update(context.Background(), func(s *backgroundjobs.State) error {
			j := s.Jobs[job.ID]
			if j == nil {
				return fmt.Errorf("job not found")
			}
			j.Enabled = newEnabled
			j.UpdatedAt = time.Now().UTC()
			return nil
		}); err != nil {
			return jobsStatusMsg{msg: fmt.Sprintf("Toggle failed: %v", err)}
		}

		// Update OS schedule (best-effort).
		if m.scheduler != nil {
			if newEnabled {
				params := backgroundjobs.ScheduleParams{
					JobID:    job.ID,
					Schedule: job.Schedule,
					Name:     backgroundjobs.SanitizeScheduleName(job.Name),
					Enabled:  true,
				}
				osState, installErr := m.scheduler.Install(context.Background(), params)
				if installErr == nil {
					_ = m.store.Update(context.Background(), func(s *backgroundjobs.State) error {
						if j := s.Jobs[job.ID]; j != nil {
							j.OSSchedule = osState
						}
						return nil
					})
				}
			} else {
				_ = m.scheduler.Remove(context.Background(), job.ID)
				_ = m.store.Update(context.Background(), func(s *backgroundjobs.State) error {
					if j := s.Jobs[job.ID]; j != nil {
						j.OSSchedule = backgroundjobs.OSScheduleState{}
					}
					return nil
				})
			}
		}

		state := "paused"
		if newEnabled {
			state = "resumed"
		}
		return jobsStatusMsg{msg: fmt.Sprintf("Job %s: %s", job.Name, state)}
	}
}

// makeDeleteCmd returns a func that produces a tea.Cmd to delete a job.
func (m *jobsInteractiveModel) makeDeleteCmd(job *backgroundjobs.Job) func() tea.Cmd {
	return func() tea.Cmd {
		return func() tea.Msg {
			// Remove OS schedule (best-effort).
			if m.scheduler != nil {
				_ = m.scheduler.Remove(context.Background(), job.ID)
			}
			if err := m.store.DeleteJob(job.ID); err != nil {
				return jobsStatusMsg{msg: fmt.Sprintf("Delete failed: %v", err)}
			}
			audit := backgroundjobs.NewAuditWriter(m.store.Dir())
			_ = audit.Write(backgroundjobs.AuditEvent{
				Event: "job.deleted",
				JobID: job.ID,
				Actor: "interactive",
			})
			return jobsRefreshMsg{}
		}
	}
}

// makeRunCmd returns a func that produces a tea.Cmd to run a job immediately.
func (m *jobsInteractiveModel) makeRunCmd(job *backgroundjobs.Job) func() tea.Cmd {
	return func() tea.Cmd {
		return func() tea.Msg {
			lg := logging.Silent()
			runStore := backgroundjobs.NewRunStore(m.store.Dir(), lg)
			audit := backgroundjobs.NewAuditWriter(m.store.Dir())

			var selfRepair *backgroundjobs.SelfRepairConfig
			if m.scheduler != nil {
				execPath, _ := os.Executable()
				execPath, _ = filepath.EvalSymlinks(execPath)
				installID, _ := backgroundjobs.ResolveInstallID(m.store.Dir())
				selfRepair = &backgroundjobs.SelfRepairConfig{
					Scheduler:      m.scheduler,
					ExecutablePath: execPath,
					InstallID:      installID,
					ConfigHash:     backgroundjobs.HashConfigPath(m.configPath),
					ExecHash:       backgroundjobs.HashExecutablePath(execPath),
				}
			}

			executor := &backgroundjobs.Executor{
				Store:       m.store,
				RunStore:    runStore,
				AuditWriter: audit,
				ConfigPath:  m.configPath,
				Logger:      lg,
				NewProvider: defaultProviderFactory,
				SelfRepair:  selfRepair,
			}

			result, err := executor.Run(context.Background(), job.ID)
			if err != nil {
				return jobsRunResultMsg{jobID: job.ID, err: err}
			}
			return jobsRunResultMsg{
				jobID:  job.ID,
				status: string(result.Record.Status),
			}
		}
	}
}

// makeCreateCmd returns a tea.Cmd that sets the pending action and quits the TUI.
func (m *jobsInteractiveModel) makeCreateCmd() tea.Cmd {
	m.pendingAction = &jobsNextAction{kind: "create"}
	return tea.Quit
}

// makeEditCmd returns a tea.Cmd that sets the pending action and quits the TUI.
func (m *jobsInteractiveModel) makeEditCmd(job *backgroundjobs.Job) tea.Cmd {
	m.pendingAction = &jobsNextAction{kind: "edit", job: job}
	return tea.Quit
}

// ── View ───────────────────────────────────────────────────────────────────

// View implements tea.Model.
func (m jobsInteractiveModel) View() string {
	if m.width == 0 {
		return ""
	}

	var content string
	switch m.view {
	case jobsViewList:
		content = m.viewList()
	case jobsViewDetail:
		content = m.viewDetail()
	}

	// Confirmation overlay.
	if m.confirmActive {
		content += "\n\n" + m.viewConfirm()
	}

	// Wrap in a bordered box.
	return m.wrapBox(content)
}

// viewList renders the job list view.
func (m jobsInteractiveModel) viewList() string {
	var b strings.Builder

	// Title.
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	b.WriteString(titleStyle.Render("Background Jobs"))
	b.WriteString("\n\n")

	if len(m.jobs) == 0 {
		dimStyle := lipgloss.NewStyle().Foreground(colorDim)
		b.WriteString(dimStyle.Render("  No jobs configured."))
		b.WriteString("\n\n")
		b.WriteString(dimStyle.Render("  Press [n] to create a new job."))
		return b.String()
	}

	// Table header.
	headerStyle := lipgloss.NewStyle().Foreground(colorDim).Bold(true)
	b.WriteString(headerStyle.Render(fmt.Sprintf("  %-22s %-16s %-20s %-10s %s",
		"NAME", "PROVIDER", "SCHEDULE", "STATUS", "NEXT/LAST")))
	b.WriteString("\n")

	// Rows.
	enabledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b"))  // green
	pausedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))   // red
	selectedStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)

	for i, job := range m.jobs {
		cursor := "  "
		nameStyle := lipgloss.NewStyle()

		if i == m.cursor {
			cursor = selectedStyle.Render("> ")
			nameStyle = selectedStyle
		}

		// Status.
		status := enabledStyle.Render("enabled")
		if !job.Enabled {
			status = pausedStyle.Render("paused")
		}

		// Next/last run.
		timeStr := "-"
		if t := effectiveNextRun(job); t != nil {
			timeStr = formatRelativeTime(*t)
		} else if job.LastRunAt != nil {
			timeStr = formatRelativeTime(*job.LastRunAt)
		}

		// Schedule display.
		schedStr := formatSchedule(job.Schedule)

		name := job.Name
		if len([]rune(name)) > 20 {
			name = string([]rune(name)[:19]) + "~"
		}

		row := fmt.Sprintf("%s%-22s %-16s %-20s %-10s %s",
			cursor,
			nameStyle.Render(name),
			dimStyle.Render(truncateWithEllipsis(job.ProviderID, 16)),
			dimStyle.Render(truncateWithEllipsis(schedStr, 20)),
			status,
			dimStyle.Render(timeStr),
		)
		b.WriteString(row)
		b.WriteString("\n")
	}

	return b.String()
}

// viewDetail renders the job detail view.
func (m jobsInteractiveModel) viewDetail() string {
	job := m.detailJob
	if job == nil {
		return "No job selected."
	}

	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(colorDim)
	valueStyle := lipgloss.NewStyle()
	enabledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b"))
	pausedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))

	b.WriteString(titleStyle.Render(job.Name))
	b.WriteString("\n\n")

	// Fields.
	b.WriteString(labelStyle.Render("  ID:          "))
	b.WriteString(valueStyle.Render(job.ID))
	b.WriteString("\n")

	b.WriteString(labelStyle.Render("  Project:     "))
	b.WriteString(valueStyle.Render(job.ProjectPath))
	b.WriteString("\n")

	b.WriteString(labelStyle.Render("  Provider:    "))
	b.WriteString(valueStyle.Render(job.ProviderID))
	b.WriteString("\n")

	if job.Model != "" {
		b.WriteString(labelStyle.Render("  Model:       "))
		b.WriteString(valueStyle.Render(job.Model))
		b.WriteString("\n")
	}

	b.WriteString(labelStyle.Render("  Schedule:    "))
	b.WriteString(valueStyle.Render(formatSchedule(job.Schedule)))
	b.WriteString(fmt.Sprintf(" (%s)", job.Schedule.Timezone))
	b.WriteString("\n")

	b.WriteString(labelStyle.Render("  Status:      "))
	if job.Enabled {
		b.WriteString(enabledStyle.Render("enabled"))
	} else {
		b.WriteString(pausedStyle.Render("paused"))
	}
	b.WriteString("\n")

	nextRun := "-"
	if t := effectiveNextRun(job); t != nil {
		nextRun = t.Format("2006-01-02 15:04 MST") + " (" + formatRelativeTime(*t) + ")"
	}
	b.WriteString(labelStyle.Render("  Next Run:    "))
	b.WriteString(valueStyle.Render(nextRun))
	b.WriteString("\n")

	lastRun := "-"
	if job.LastRunAt != nil {
		lastRun = job.LastRunAt.Format("2006-01-02 15:04 MST") + " (" + formatRelativeTime(*job.LastRunAt) + ")"
	}
	b.WriteString(labelStyle.Render("  Last Run:    "))
	b.WriteString(valueStyle.Render(lastRun))
	b.WriteString("\n")

	b.WriteString(labelStyle.Render("  Permissions: "))
	b.WriteString(valueStyle.Render(string(job.Permissions.FileAccess)))
	if len(job.Permissions.WritablePaths) > 0 {
		b.WriteString(fmt.Sprintf(" (%s)", strings.Join(job.Permissions.WritablePaths, ", ")))
	}
	b.WriteString("\n")

	// Prompt (truncated).
	b.WriteString("\n")
	b.WriteString(labelStyle.Render("  Prompt:\n"))
	prompt := job.Prompt
	lines := strings.Split(prompt, "\n")
	maxLines := 6
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines = append(lines, "    ...")
	}
	for _, line := range lines {
		b.WriteString("    ")
		b.WriteString(valueStyle.Render(line))
		b.WriteString("\n")
	}

	// Recent runs.
	b.WriteString("\n")
	b.WriteString(labelStyle.Render("  Recent Runs:\n"))
	runs, err := m.runStore.List(job.ID)
	if err != nil || len(runs) == 0 {
		b.WriteString("    None recorded.\n")
	} else {
		b.WriteString(labelStyle.Render("    %-20s %-12s %-10s %s\n"))
		b.WriteString(labelStyle.Render(fmt.Sprintf("    %-20s %-12s %-10s %s",
			"STARTED", "STATUS", "DURATION", "RUN_ID")))
		b.WriteString("\n")
		limit := len(runs)
		if limit > 5 {
			limit = 5
		}
		for i := 0; i < limit; i++ {
			r := runs[i]
			started := r.StartedAt.Format("2006-01-02 15:04")
			duration := "-"
			if r.FinishedAt != nil {
				duration = fmt.Sprintf("%dms", r.DurationMillis)
			}
			statusStyle := valueStyle
			switch r.Status {
			case backgroundjobs.RunStatusCompleted:
				statusStyle = enabledStyle
			case backgroundjobs.RunStatusFailed, backgroundjobs.RunStatusTimedOut:
				statusStyle = pausedStyle
			}
			b.WriteString(fmt.Sprintf("    %-20s %-12s %-10s %s\n",
				started,
				statusStyle.Render(string(r.Status)),
				duration,
				r.ID[:8]))
		}
	}

	return b.String()
}

// viewConfirm renders the confirmation overlay.
func (m jobsInteractiveModel) viewConfirm() string {
	confirmStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffb86c")).Bold(true) // orange
	return confirmStyle.Render("  " + m.confirmMsg)
}

// wrapBox wraps content in a centered bordered box with the action bar.
func (m jobsInteractiveModel) wrapBox(content string) string {
	// Action bar.
	var actions string
	switch m.view {
	case jobsViewList:
		actions = "[n] new  [enter] details  [space] toggle  [e] edit  [d] delete  [r] run  [q] quit"
	case jobsViewDetail:
		actions = "[esc] back  [space] toggle  [e] edit  [d] delete  [r] run  [q] quit"
	}
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)

	// Status flash.
	var statusLine string
	if m.statusMsg != "" {
		statusStyle := lipgloss.NewStyle().Foreground(colorAccent)
		statusLine = "\n" + statusStyle.Render("  "+m.statusMsg)
	}

	fullContent := content + statusLine + "\n" + dimStyle.Render("  "+actions)

	// Box styling.
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2)

	// Responsive width.
	innerW := m.width - 8 // border + padding + margin
	if innerW < 40 {
		innerW = 40
	}
	if innerW > 100 {
		innerW = 100
	}
	boxStyle = boxStyle.Width(innerW)

	rendered := boxStyle.Render(fullContent)

	// Center vertically and horizontally.
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, rendered)
}
