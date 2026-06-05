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
	cmd        *cobra.Command

	jobs   []*backgroundjobs.Job // sorted by name
	cursor int                   // selected row index
	width  int
	height int

	// View state.
	view      string // jobsViewList or jobsViewDetail
	detailJob *backgroundjobs.Job

	// Confirmation overlay.
	confirmActive bool
	confirmMsg    string
	confirmAction func() tea.Cmd // runs on 'y'

	// Status flash message.
	statusMsg string

	// Quit signal.
	quit bool

	// Embedded wizard — non-nil when in create/edit mode.
	wizard     *jobWizardModel
	wizardEdit bool   // true = editing existing job
	wizardID   string // job ID being edited (for edit mode)
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
func runJobsInteractive(cmd *cobra.Command) error {
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
	runStore := backgroundjobs.NewRunStore(store.Dir(), lg)

	var sched backgroundjobs.Scheduler
	if s, schedErr := buildScheduler(outputRoot, resolvedConfigPath); schedErr == nil {
		sched = s
	}

	m := jobsInteractiveModel{
		store:      store,
		runStore:   runStore,
		scheduler:  sched,
		configPath: resolvedConfigPath,
		outputRoot: outputRoot,
		cmd:        cmd,
		view:       jobsViewList,
	}

	if err := m.reloadJobs(); err != nil {
		return fmt.Errorf("load jobs: %w", err)
	}

	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// reloadJobs loads the current job list from the store and sorts by name.
func (m *jobsInteractiveModel) reloadJobs() error {
	state, err := m.store.Load()
	if err != nil {
		return err
	}
	// Preallocate jobs slice to map size for efficient append
	m.jobs = make([]*backgroundjobs.Job, 0, len(state.Jobs))
	for _, j := range state.Jobs {
		m.jobs = append(m.jobs, j)
	}
	sort.Slice(m.jobs, func(i, k int) bool {
		return strings.ToLower(m.jobs[i].Name) < strings.ToLower(m.jobs[k].Name)
	})
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
		// Forward to embedded wizard so it can size and center correctly.
		if m.wizard != nil {
			var cmd tea.Cmd
			updated, cmd := m.wizard.Update(msg)
			if wm, ok := updated.(jobWizardModel); ok {
				m.wizard = &wm
			}
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		if m.confirmActive {
			return m.updateConfirm(msg)
		}
		if m.wizard != nil {
			return m.updateWizard(msg)
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

// ── Wizard mode ────────────────────────────────────────────────────────────

// enterWizard sets up the embedded wizard for create or edit.
func (m *jobsInteractiveModel) enterWizard(prefilled jobWizardAnswers) {
	wm := newJobWizardModel(prefilled)
	m.wizard = &wm
}

// updateWizard delegates key input to the embedded wizard and handles its lifecycle.
func (m jobsInteractiveModel) updateWizard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// ctrl+c always quits entirely.
	if key == "ctrl+c" {
		m.quit = true
		return m, tea.Quit
	}

	// esc at first step (path) cancels the wizard, returning to list.
	if key == "esc" && m.wizard.step == wizStepPath {
		m.wizard = nil
		m.statusMsg = "Cancelled."
		return m, nil
	}

	// Delegate all other keys to the wizard.
	var cmd tea.Cmd
	updated, cmd := m.wizard.Update(msg)
	if wm, ok := updated.(jobWizardModel); ok {
		m.wizard = &wm
	}

	// Check if wizard completed or quit.
	if m.wizard.confirmed {
		return m, m.applyWizardAnswers()
	}
	if m.wizard.quit {
		m.quit = true
		return m, tea.Quit
	}

	return m, cmd
}

// applyWizardAnswers applies the wizard's answers — creates or updates the job.
func (m *jobsInteractiveModel) applyWizardAnswers() tea.Cmd {
	return func() tea.Msg {
		answers := m.wizard.answers
		resolvedConfigPath := m.configPath
		outputRoot := m.outputRoot

		if m.wizardEdit {
			// Edit existing job.
			if err := applyJobEdit(m.store, m.scheduler, m.cmd, outputRoot, resolvedConfigPath, m.wizardID, answers); err != nil {
				return jobsStatusMsg{msg: fmt.Sprintf("Edit failed: %v", err)}
			}
			return jobsStatusMsg{msg: fmt.Sprintf("Job %q updated.", answers.name)}
		}

		// Create new job.
		cfg, _ := config.LoadConfigWithOverlay(resolvedConfigPath, "")
		defaultProvider := cfg.DefaultProvider
		if defaultProvider == "" {
			defaultProvider = string(config.DefaultProviderID)
		}
		input := jobAnswersToCreateInput(answers, defaultProvider)
		if err := createAndSaveJob(m.cmd, outputRoot, resolvedConfigPath, input, false); err != nil {
			return jobsStatusMsg{msg: fmt.Sprintf("Create failed: %v", err)}
		}
		return jobsStatusMsg{msg: fmt.Sprintf("Job %q created.", input.name)}
	}
}

// applyJobEdit updates an existing job from wizard answers.
func applyJobEdit(store *backgroundjobs.Store, sched backgroundjobs.Scheduler, cmd *cobra.Command, outputRoot, configPath, jobID string, answers jobWizardAnswers) error {
	cfg, _ := config.LoadConfigWithOverlay(configPath, "")
	defaultProvider := cfg.DefaultProvider
	if defaultProvider == "" {
		defaultProvider = string(config.DefaultProviderID)
	}
	input := jobAnswersToCreateInput(answers, defaultProvider)

	scheduleChanged := false

	if err := store.Update(context.Background(), func(s *backgroundjobs.State) error {
		j := s.Jobs[jobID]
		if j == nil {
			return fmt.Errorf("job not found")
		}
		j.Name = input.name
		j.Prompt = input.prompt
		j.ProviderID = input.providerID
		j.Model = input.model
		j.Permissions.FileAccess = backgroundjobs.FileAccessMode(input.fileAccess)
		if input.writablePaths != "" {
			// Preallocate paths slice to input length - splits produce at most len+1 elements
			paths := make([]string, 0, len(input.writablePaths))
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

	if scheduleChanged {
		if s, schedErr := buildScheduler(outputRoot, configPath); schedErr == nil {
			params := backgroundjobs.ScheduleParams{
				JobID:    jobID,
				Schedule: input.schedule,
				Name:     backgroundjobs.SanitizeScheduleName(input.name),
				Enabled:  true,
			}
			osState, installErr := s.Install(context.Background(), params)
			if installErr == nil {
				_ = store.Update(context.Background(), func(s *backgroundjobs.State) error {
					if j := s.Jobs[jobID]; j != nil {
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
		JobID: jobID,
		Actor: "interactive",
	})
	return nil
}

// ── Confirmation overlay ───────────────────────────────────────────────────

func (m jobsInteractiveModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

// ── List view ──────────────────────────────────────────────────────────────

func (m jobsInteractiveModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.quit = true
		return m, tea.Quit

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.jobs)-1 {
			m.cursor++
		}

	case "enter":
		job := m.selectedJob()
		if job != nil {
			m.view = jobsViewDetail
			m.detailJob = job
			m.statusMsg = ""
		}

	case " ":
		job := m.selectedJob()
		if job != nil {
			return m, m.makeToggleCmd(job)
		}

	case "n":
		m.enterWizard(jobWizardAnswers{})
		return m, nil

	case "e":
		job := m.selectedJob()
		if job != nil {
			m.wizardEdit = true
			m.wizardID = job.ID
			m.enterWizard(jobToWizardAnswers(job))
		}
		return m, nil

	case "d":
		job := m.selectedJob()
		if job != nil {
			m.confirmActive = true
			m.confirmMsg = fmt.Sprintf("Delete job %q? This cannot be undone. [y/N]", job.Name)
			m.confirmAction = m.makeDeleteCmd(job)
		}

	case "r":
		job := m.selectedJob()
		if job != nil {
			m.confirmActive = true
			m.confirmMsg = fmt.Sprintf("Run job %q now? [y/N]", job.Name)
			m.confirmAction = m.makeRunCmd(job)
		}
	}
	return m, nil
}

// ── Detail view ────────────────────────────────────────────────────────────

func (m jobsInteractiveModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quit = true
		return m, tea.Quit

	case "esc", "backspace":
		m.view = jobsViewList
		m.detailJob = nil

	case " ":
		job := m.detailJob
		if job != nil {
			return m, m.makeToggleCmd(job)
		}

	case "e":
		job := m.detailJob
		if job != nil {
			m.wizardEdit = true
			m.wizardID = job.ID
			m.enterWizard(jobToWizardAnswers(job))
		}
		return m, nil

	case "d":
		job := m.detailJob
		if job != nil {
			m.confirmActive = true
			m.confirmMsg = fmt.Sprintf("Delete job %q? This cannot be undone. [y/N]", job.Name)
			m.confirmAction = m.makeDeleteCmd(job)
		}

	case "r":
		job := m.detailJob
		if job != nil {
			m.confirmActive = true
			m.confirmMsg = fmt.Sprintf("Run job %q now? [y/N]", job.Name)
			m.confirmAction = m.makeRunCmd(job)
		}
	}
	return m, nil
}

// ── Action commands ────────────────────────────────────────────────────────

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

func (m *jobsInteractiveModel) makeDeleteCmd(job *backgroundjobs.Job) func() tea.Cmd {
	return func() tea.Cmd {
		return func() tea.Msg {
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

// ── View ───────────────────────────────────────────────────────────────────

func (m jobsInteractiveModel) View() string {
	if m.width == 0 {
		return ""
	}

	// Wizard mode — render the wizard view directly (no box wrapping).
	if m.wizard != nil {
		return m.wizard.View()
	}

	var content string
	switch m.view {
	case jobsViewList:
		content = m.viewList()
	case jobsViewDetail:
		content = m.viewDetail()
	}

	if m.confirmActive {
		content += "\n\n" + m.viewConfirm()
	}

	return m.wrapBox(content)
}

func (m jobsInteractiveModel) viewList() string {
	var b strings.Builder

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

	headerStyle := lipgloss.NewStyle().Foreground(colorDim).Bold(true)
	b.WriteString(headerStyle.Render(fmt.Sprintf("  %-22s %-16s %-20s %-10s %s",
		"NAME", "PROVIDER", "SCHEDULE", "STATUS", "NEXT/LAST")))
	b.WriteString("\n")

	enabledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b"))
	pausedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	selectedStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)

	for i, job := range m.jobs {
		cursor := "  "
		nameStyle := lipgloss.NewStyle()

		if i == m.cursor {
			cursor = selectedStyle.Render("> ")
			nameStyle = selectedStyle
		}

		status := enabledStyle.Render("enabled")
		if !job.Enabled {
			status = pausedStyle.Render("paused")
		}

		timeStr := "-"
		if t := effectiveNextRun(job); t != nil {
			timeStr = formatRelativeTime(*t)
		} else if job.LastRunAt != nil {
			timeStr = formatRelativeTime(*job.LastRunAt)
		}

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

	b.WriteString("\n")
	b.WriteString(labelStyle.Render("  Recent Runs:\n"))
	runs, err := m.runStore.List(job.ID)
	if err != nil || len(runs) == 0 {
		b.WriteString("    None recorded.\n")
	} else {
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

func (m jobsInteractiveModel) viewConfirm() string {
	confirmStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffb86c")).Bold(true)
	return confirmStyle.Render("  " + m.confirmMsg)
}

func (m jobsInteractiveModel) wrapBox(content string) string {
	var actions string
	switch m.view {
	case jobsViewList:
		actions = "[n] new  [enter] details  [space] toggle  [e] edit  [d] delete  [r] run  [q] quit"
	case jobsViewDetail:
		actions = "[esc] back  [space] toggle  [e] edit  [d] delete  [r] run  [q] quit"
	}
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)

	var statusLine string
	if m.statusMsg != "" {
		statusStyle := lipgloss.NewStyle().Foreground(colorAccent)
		statusLine = "\n" + statusStyle.Render("  "+m.statusMsg)
	}

	fullContent := content + statusLine + "\n" + dimStyle.Render("  "+actions)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(1, 2)

	innerW := m.width - 8
	if innerW < 40 {
		innerW = 40
	}
	if innerW > 100 {
		innerW = 100
	}
	boxStyle = boxStyle.Width(innerW)

	rendered := boxStyle.Render(fullContent)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, rendered)
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
