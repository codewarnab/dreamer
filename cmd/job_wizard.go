package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	_ "time/tzdata"

	"dreamer/internal/analyzer"
	"dreamer/internal/backgroundjobs"
	"dreamer/internal/pipeline"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Wizard steps — schedule sub-steps share "Step 5/8" visually.
const (
	wizStepPath = iota
	wizStepName
	wizStepProvider
	wizStepModel // model override (optional)
	wizStepPrompt
	wizStepPermissions   // file access level picker
	wizStepScheduleKind
	wizStepInterval  // sub-step for hourly
	wizStepTimeOfDay // sub-step for daily/weekly
	wizStepDayOfWeek // sub-step for weekly
	wizStepCron      // sub-step for cron
	wizStepTimezone
	wizStepCustomTz // sub-step for custom timezone text input
	wizStepSummary
)

// Layout constants — avoid magic numbers in View().
const (
	wizBoxPad      = 2  // lipgloss Padding horizontal
	wizListPad     = 20 // list width = terminal width - this
	wizBoxMinW     = 40 // minimum box inner width
	wizBoxMaxW     = 90 // maximum box inner width
	wizTerminalPad = 12 // margin for border+padding+outer breathing room
)

// jobWizardAnswers collects all values the wizard gathers.
type jobWizardAnswers struct {
	projectPath   string
	name          string
	providerID    string
	model         string
	prompt        string
	fileAccess    string // read_only, selected_writes, full_workspace
	writablePaths string // comma-separated, for selected_writes (CLI only)
	scheduleKind  string
	every         string
	timeOfDay     string
	dayOfWeek     string
	cron          string
	timezone      string
}

// jobWizardModel is the bubbletea model for the interactive job creator.
type jobWizardModel struct {
	step             int
	answers          jobWizardAnswers
	pathInput        textinput.Model
	nameInput        textinput.Model
	providerList     list.Model
	modelInput       textinput.Model
	promptInput      textarea.Model
	permissionsList  list.Model
	scheduleKindList list.Model
	intervalList     list.Model
	timeOfDayInput   textinput.Model
	dayOfWeekList    list.Model
	cronInput        textinput.Model
	timezoneList     list.Model
	customTzInput    textinput.Model
	pathErr          string
	promptErr        string
	quit             bool
	confirmed        bool
	width            int
	height           int
	customTimezone   bool // true when custom tz text input is active
}

// runJobWizard launches the interactive job creation wizard via bubbletea.
// Returns the collected answers, whether the user confirmed, and any error.
func runJobWizard(initial jobWizardAnswers) (jobWizardAnswers, bool, error) {
	m := newJobWizardModel(initial)
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return jobWizardAnswers{}, false, fmt.Errorf("run wizard: %w", err)
	}
	result := final.(jobWizardModel)
	if result.quit || !result.confirmed {
		return jobWizardAnswers{}, false, nil
	}
	return result.answers, true, nil
}

// newJobWizardModel builds the wizard with all widgets initialized.
// Steps whose answers are already populated in prefilled are skipped
// automatically via advance().
func newJobWizardModel(prefilled jobWizardAnswers) jobWizardModel {
	const initialW = 80

	// Step 1: project path.
	pathIn := textinput.New()
	pathIn.CharLimit = 512
	pathIn.Focus()
	if prefilled.projectPath != "" {
		pathIn.SetValue(prefilled.projectPath)
	} else if cwd, err := os.Getwd(); err == nil {
		pathIn.SetValue(cwd)
	}

	// Step 2: job name.
	nameIn := textinput.New()
	nameIn.Placeholder = "my-project"
	nameIn.CharLimit = 128
	if prefilled.name != "" {
		nameIn.SetValue(prefilled.name)
	}

	// Step 3: provider picker (background-safe only).
	providers := backgroundSafeProviderItems()
	providerList := list.New(providers, compactDelegate{}, initialW-wizListPad, listHeight(len(providers)))
	providerList.Title = "Provider"
	providerList.SetShowHelp(false)
	providerList.SetShowStatusBar(false)

	// Step 3b: model override (optional).
	modelIn := textinput.New()
	modelIn.Placeholder = "default (provider auto-selects)"
	modelIn.CharLimit = 128
	if prefilled.model != "" {
		modelIn.SetValue(prefilled.model)
	}

	// Step 4: prompt textarea.
	promptTA := textarea.New()
	promptTA.Placeholder = "Search GitHub for open issues labeled 'bounty'..."
	promptTA.CharLimit = 8192
	promptTA.SetWidth(initialW - wizListPad)
	promptTA.SetHeight(4)
	if prefilled.prompt != "" {
		promptTA.SetValue(prefilled.prompt)
	}

	// Step 5: file access permissions picker.
	// Only read_only is currently supported by the executor.
	// selected_writes and full_workspace are planned but not yet implemented.
	permissionItems := []list.Item{
		providerItem{id: "read_only", title: "Read Only (Safe)", desc: "can read project files, cannot write anything"},
	}
	permList := list.New(permissionItems, compactDelegate{}, initialW-wizListPad, listHeight(len(permissionItems)))
	permList.Title = "File Access"
	permList.SetShowHelp(false)
	permList.SetShowStatusBar(false)

	// Step 5b: writable paths text input (for selected_writes).
	// TODO: implement @ mention-based file picker — typing '@' triggers a
	// fuzzy file search scoped to the project directory. Selected files
	// Step 6a: schedule kind.
	scheduleItems := []list.Item{
		providerItem{id: "interval", desc: "run every N minutes/hours"},
		providerItem{id: "daily", desc: "run once per day at a fixed time"},
		providerItem{id: "weekly", desc: "run once per week on a chosen day"},
	}
	if runtime.GOOS != "windows" {
		scheduleItems = append(scheduleItems, providerItem{id: "cron", title: "custom cron", desc: "advanced: 5-field cron expression"})
	}
	scheduleList := list.New(scheduleItems, compactDelegate{}, initialW-wizListPad, listHeight(len(scheduleItems)))
	scheduleList.Title = "Schedule"
	scheduleList.SetShowHelp(false)
	scheduleList.SetShowStatusBar(false)

	// Step 6a: interval picker (hourly).
	intervalItems := []list.Item{
		providerItem{id: "2h", desc: "every 2 hours"},
		providerItem{id: "1h", desc: "every hour (default)"},
		providerItem{id: "45m", desc: "every 45 minutes"},
		providerItem{id: "30m", desc: "every 30 minutes"},
		providerItem{id: "20m", desc: "every 20 minutes"},
		providerItem{id: "15m", desc: "every 15 minutes"},
		providerItem{id: "10m", desc: "every 10 minutes"},
		providerItem{id: "5m", desc: "every 5 minutes"},
	}
	intervalList := list.New(intervalItems, compactDelegate{}, initialW-wizListPad, listHeight(len(intervalItems)))
	intervalList.Title = "Repeat interval"
	intervalList.SetShowHelp(false)
	intervalList.SetShowStatusBar(false)

	// Step 6b: time-of-day input.
	todIn := textinput.New()
	todIn.Placeholder = "09:00"
	todIn.CharLimit = 5
	if prefilled.timeOfDay != "" {
		todIn.SetValue(prefilled.timeOfDay)
	} else {
		todIn.SetValue("09:00")
	}

	// Step 6c: day-of-week picker.
	dowItems := []list.Item{
		providerItem{id: "monday"},
		providerItem{id: "tuesday"},
		providerItem{id: "wednesday"},
		providerItem{id: "thursday"},
		providerItem{id: "friday"},
		providerItem{id: "saturday"},
		providerItem{id: "sunday"},
	}
	dowList := list.New(dowItems, compactDelegate{}, initialW-wizListPad, listHeight(len(dowItems)))
	dowList.Title = "Day of week"
	dowList.SetShowHelp(false)
	dowList.SetShowStatusBar(false)

	// Step 6d: cron expression input.
	cronIn := textinput.New()
	cronIn.Placeholder = "0 9 * * 1"
	cronIn.CharLimit = 64
	if prefilled.cron != "" {
		cronIn.SetValue(prefilled.cron)
	}

	// Step 6: timezone picker.
	tzItems := timezoneItems()
	tzList := list.New(tzItems, compactDelegate{}, initialW-wizListPad, listHeight(len(tzItems)))
	tzList.Title = "Timezone"
	tzList.SetShowHelp(false)
	tzList.SetShowStatusBar(false)

	// Step 6b: custom timezone text input.
	customTZIn := textinput.New()
	customTZIn.Placeholder = "America/Los_Angeles"
	customTZIn.CharLimit = 128

	m := jobWizardModel{
		step:             wizStepPath,
		answers:          prefilled,
		pathInput:        pathIn,
		nameInput:        nameIn,
		providerList:     providerList,
		modelInput:       modelIn,
		promptInput:      promptTA,
		permissionsList:  permList,
		scheduleKindList: scheduleList,
		intervalList:     intervalList,
		timeOfDayInput:   todIn,
		dayOfWeekList:    dowList,
		cronInput:        cronIn,
		timezoneList:     tzList,
		customTzInput:    customTZIn,
		width:            initialW,
	}

	// Set defaults for unset fields.
	if m.answers.timezone == "" {
		m.answers.timezone = "UTC"
	}
	if m.answers.scheduleKind == "" {
		m.answers.scheduleKind = "daily"
	}
	if m.answers.fileAccess == "" {
		m.answers.fileAccess = "read_only"
	}

	// Advance to the first unfilled step.
	m.step = m.firstUnfilledStep()
	return m
}

// firstUnfilledStep returns the first step whose answer is not yet populated.
func (m jobWizardModel) firstUnfilledStep() int {
	if m.answers.projectPath == "" {
		return wizStepPath
	}
	if m.answers.name == "" {
		return wizStepName
	}
	if m.answers.providerID == "" {
		return wizStepProvider
	}
	if m.answers.prompt == "" {
		return wizStepPrompt
	}
	if m.answers.fileAccess == "" {
		return wizStepPermissions
	}
	if m.answers.scheduleKind == "" {
		return wizStepScheduleKind
	}
	// Schedule kind is set — check kind-specific sub-fields.
	switch m.answers.scheduleKind {
	case "interval":
		if m.answers.every == "" {
			return wizStepInterval
		}
	case "daily":
		if m.answers.timeOfDay == "" {
			return wizStepTimeOfDay
		}
	case "weekly":
		if m.answers.dayOfWeek == "" {
			return wizStepDayOfWeek
		}
		if m.answers.timeOfDay == "" {
			return wizStepTimeOfDay
		}
	}
	if m.answers.timezone == "" {
		return wizStepTimezone
	}
	return wizStepSummary
}

func (m jobWizardModel) Init() tea.Cmd { return textinput.Blink }

func (m jobWizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.KeyMsg:
		key := typed.String()
		switch key {
		case "ctrl+c":
			m.quit = true
			return m, tea.Quit
		case "esc":
			return m.goBack()
		case "enter":
			// textarea: enter inserts newline; ctrl+enter advances.
			if m.step == wizStepPrompt {
				break
			}
			// Custom tz text input: enter advances.
			if m.step == wizStepCustomTz {
				return m.advance()
			}
			return m.advance()
		case "ctrl+enter", "tab":
			if m.step == wizStepPrompt {
				return m.advance()
			}
		}
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		inner := m.boxInnerWidth()
		m.providerList.SetWidth(inner)
		m.permissionsList.SetWidth(inner)
		m.scheduleKindList.SetWidth(inner)
		m.intervalList.SetWidth(inner)
		m.dayOfWeekList.SetWidth(inner)
		m.timezoneList.SetWidth(inner)
		m.promptInput.SetWidth(inner)
	}

	// Route message to the active widget.
	var cmd tea.Cmd
	switch m.step {
	case wizStepPath:
		m.pathInput, cmd = m.pathInput.Update(msg)
	case wizStepName:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case wizStepProvider:
		m.providerList, cmd = m.providerList.Update(msg)
	case wizStepModel:
		m.modelInput, cmd = m.modelInput.Update(msg)
	case wizStepPrompt:
		m.promptInput, cmd = m.promptInput.Update(msg)
	case wizStepPermissions:
		m.permissionsList, cmd = m.permissionsList.Update(msg)
	case wizStepScheduleKind:
		m.scheduleKindList, cmd = m.scheduleKindList.Update(msg)
	case wizStepInterval:
		m.intervalList, cmd = m.intervalList.Update(msg)
	case wizStepTimeOfDay:
		m.timeOfDayInput, cmd = m.timeOfDayInput.Update(msg)
	case wizStepDayOfWeek:
		m.dayOfWeekList, cmd = m.dayOfWeekList.Update(msg)
	case wizStepCron:
		m.cronInput, cmd = m.cronInput.Update(msg)
	case wizStepTimezone:
		m.timezoneList, cmd = m.timezoneList.Update(msg)
	case wizStepCustomTz:
		m.customTzInput, cmd = m.customTzInput.Update(msg)
	}
	return m, cmd
}

// advance reads the current step's value and moves forward.
func (m jobWizardModel) advance() (tea.Model, tea.Cmd) {
	switch m.step {
	case wizStepPath:
		return m.advanceFromPath()
	case wizStepName:
		return m.advanceFromName()
	case wizStepProvider:
		return m.advanceFromProvider()
	case wizStepModel:
		return m.advanceFromModel()
	case wizStepPrompt:
		return m.advanceFromPrompt()
	case wizStepPermissions:
		return m.advanceFromPermissions()
	case wizStepScheduleKind:
		return m.advanceFromScheduleKind()
	case wizStepInterval:
		return m.advanceFromInterval()
	case wizStepTimeOfDay:
		return m.advanceFromTimeOfDay()
	case wizStepDayOfWeek:
		return m.advanceFromDayOfWeek()
	case wizStepCron:
		return m.advanceFromCron()
	case wizStepTimezone:
		return m.advanceFromTimezone()
	case wizStepCustomTz:
		return m.advanceFromCustomTz()
	case wizStepSummary:
		m.confirmed = true
		return m, tea.Quit
	}
	return m, nil
}

func (m jobWizardModel) advanceFromPath() (tea.Model, tea.Cmd) {
	p := strings.TrimSpace(m.pathInput.Value())
	if p == "" {
		m.pathErr = "path is required"
		return m, nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		m.pathErr = fmt.Sprintf("invalid path: %v", err)
		return m, nil
	}
	if err := validateProjectPath(abs); err != nil {
		m.pathErr = err.Error()
		return m, nil
	}
	m.pathErr = ""
	m.answers.projectPath = abs
	// Pre-fill name from path basename.
	if m.answers.name == "" {
		m.answers.name = filepath.Base(abs)
		m.nameInput.SetValue(m.answers.name)
	}
	m.nameInput.Focus()
	m.step = wizStepName
	return m, nil
}

func (m jobWizardModel) advanceFromName() (tea.Model, tea.Cmd) {
	n := strings.TrimSpace(m.nameInput.Value())
	if n != "" {
		m.answers.name = n
	}
	m.step = wizStepProvider
	return m, nil
}

func (m jobWizardModel) advanceFromProvider() (tea.Model, tea.Cmd) {
	if sel, ok := m.providerList.SelectedItem().(providerItem); ok {
		m.answers.providerID = sel.id
	}
	m.modelInput.Focus()
	m.step = wizStepModel
	return m, nil
}

func (m jobWizardModel) advanceFromModel() (tea.Model, tea.Cmd) {
	m.answers.model = strings.TrimSpace(m.modelInput.Value())
	m.promptInput.Focus()
	m.step = wizStepPrompt
	return m, nil
}

func (m jobWizardModel) advanceFromPrompt() (tea.Model, tea.Cmd) {
	p := strings.TrimSpace(m.promptInput.Value())
	if p == "" {
		m.promptErr = "prompt is required"
		m.promptInput.Focus()
		return m, nil
	}
	m.promptErr = ""
	m.answers.prompt = p
	m.step = wizStepPermissions
	return m, nil
}

func (m jobWizardModel) advanceFromPermissions() (tea.Model, tea.Cmd) {
	if sel, ok := m.permissionsList.SelectedItem().(providerItem); ok {
		m.answers.fileAccess = sel.id
	}
	m.step = wizStepScheduleKind
	return m, nil
}

func (m jobWizardModel) advanceFromScheduleKind() (tea.Model, tea.Cmd) {
	if sel, ok := m.scheduleKindList.SelectedItem().(providerItem); ok {
		m.answers.scheduleKind = sel.id
	}
	switch m.answers.scheduleKind {
	case "interval":
		m.step = wizStepInterval
	case "daily":
		m.timeOfDayInput.Focus()
		m.step = wizStepTimeOfDay
	case "weekly":
		m.step = wizStepDayOfWeek
	case "cron":
		m.cronInput.Focus()
		m.step = wizStepCron
	}
	return m, nil
}

func (m jobWizardModel) advanceFromInterval() (tea.Model, tea.Cmd) {
	if sel, ok := m.intervalList.SelectedItem().(providerItem); ok {
		m.answers.every = sel.id
	}
	m.step = wizStepTimezone
	return m, nil
}

func (m jobWizardModel) advanceFromTimeOfDay() (tea.Model, tea.Cmd) {
	v := strings.TrimSpace(m.timeOfDayInput.Value())
	if v != "" {
		m.answers.timeOfDay = v
	}
	m.step = wizStepTimezone
	return m, nil
}

func (m jobWizardModel) advanceFromDayOfWeek() (tea.Model, tea.Cmd) {
	if sel, ok := m.dayOfWeekList.SelectedItem().(providerItem); ok {
		m.answers.dayOfWeek = sel.id
	}
	m.timeOfDayInput.Focus()
	m.step = wizStepTimeOfDay
	return m, nil
}

func (m jobWizardModel) advanceFromCron() (tea.Model, tea.Cmd) {
	v := strings.TrimSpace(m.cronInput.Value())
	if v != "" {
		m.answers.cron = v
	}
	m.step = wizStepTimezone
	return m, nil
}

func (m jobWizardModel) advanceFromTimezone() (tea.Model, tea.Cmd) {
	if sel, ok := m.timezoneList.SelectedItem().(providerItem); ok {
		if sel.id == "custom" {
			m.customTimezone = true
			m.customTzInput.Focus()
			m.step = wizStepCustomTz
			return m, nil
		}
		m.answers.timezone = sel.id
	}
	m.step = wizStepSummary
	return m, nil
}

func (m jobWizardModel) advanceFromCustomTz() (tea.Model, tea.Cmd) {
	tz := strings.TrimSpace(m.customTzInput.Value())
	if tz == "" {
		return m, nil
	}
	// Validate IANA timezone.
	if _, err := time.LoadLocation(tz); err != nil {
		return m, nil // invalid tz — stay on this step
	}
	m.answers.timezone = tz
	m.step = wizStepSummary
	return m, nil
}

// goBack returns to the previous step.
func (m jobWizardModel) goBack() (tea.Model, tea.Cmd) {
	switch m.step {
	case wizStepPath:
		// First step — nowhere to go.
	case wizStepName:
		m.pathInput.Focus()
		m.step = wizStepPath
	case wizStepProvider:
		m.nameInput.Focus()
		m.step = wizStepName
	case wizStepModel:
		m.step = wizStepProvider
	case wizStepPrompt:
		m.step = wizStepModel
	case wizStepPermissions:
		m.promptInput.Focus()
		m.step = wizStepPrompt
	case wizStepScheduleKind:
		m.step = wizStepPermissions
	case wizStepInterval, wizStepTimeOfDay, wizStepDayOfWeek, wizStepCron:
		m.step = wizStepScheduleKind
	case wizStepTimezone:
		m.step = m.scheduleSubStep()
	case wizStepCustomTz:
		m.customTimezone = false
		m.step = wizStepTimezone
	case wizStepSummary:
		m.step = wizStepTimezone
	}
	return m, nil
}

// scheduleSubStep returns the sub-step that corresponds to the current
// schedule kind. Used by goBack() to reverse from timezone.
func (m jobWizardModel) scheduleSubStep() int {
	switch m.answers.scheduleKind {
	case "interval":
		return wizStepInterval
	case "daily":
		m.timeOfDayInput.Focus()
		return wizStepTimeOfDay
	case "weekly":
		m.timeOfDayInput.Focus()
		return wizStepTimeOfDay
	case "cron":
		m.cronInput.Focus()
		return wizStepCron
	default:
		return wizStepScheduleKind
	}
}

// boxInnerWidth computes the usable width inside the bordered box.
func (m jobWizardModel) boxInnerWidth() int {
	w := m.width - wizTerminalPad
	if w < wizBoxMinW {
		w = wizBoxMinW
	}
	if w > wizBoxMaxW {
		w = wizBoxMaxW
	}
	return w
}

// visualStepLabel returns "Step N/8 — Title" for the given step.
func visualStepLabel(step int) string {
	labels := map[int]string{
		wizStepPath:          "Step 1/9 — Project Path",
		wizStepName:          "Step 2/9 — Job Name",
		wizStepProvider:      "Step 3/9 — Provider",
		wizStepModel:         "Step 3b/9 — Model",
		wizStepPrompt:        "Step 4/9 — Prompt",
		wizStepPermissions:   "Step 5/9 — File Access",
		wizStepScheduleKind:  "Step 6/9 — Schedule",
		wizStepInterval:      "Step 6b/9 — Repeat Interval",
		wizStepTimeOfDay:     "Step 6b/9 — Time of Day",
		wizStepDayOfWeek:     "Step 6b/9 — Day of Week",
		wizStepCron:          "Step 6b/9 — Cron Expression",
		wizStepTimezone:      "Step 7/9 — Timezone",
		wizStepCustomTz:      "Step 7b/9 — Custom Timezone",
		wizStepSummary:       "Step 8/9 — Summary",
	}
	if l, ok := labels[step]; ok {
		return l
	}
	return ""
}

func (m jobWizardModel) View() string {
	if m.quit {
		return ""
	}

	innerW := m.boxInnerWidth()
	boxOuter := innerW + 6

	headerStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	header := headerStyle.Render("░░ DREAMER ░░ Background Job Setup")

	style := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		Padding(wizBoxPad, wizBoxPad).
		Width(innerW)

	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff6666"))

	var body string
	title := titleStyle.Render(visualStepLabel(m.step))

	switch m.step {
	case wizStepPath:
		errLine := ""
		if m.pathErr != "" {
			errLine = "\n\n" + errStyle.Render("Error: "+m.pathErr)
		}
		body = fmt.Sprintf("%s\n\nWhere is the project?\n\n%s%s\n\n%s",
			title, m.pathInput.View(), errLine,
			dimStyle.Render("[enter] next  •  [esc] cancel"))

	case wizStepName:
		body = fmt.Sprintf("%s\n\nJob name:\n\n%s\n\n%s",
			title, m.nameInput.View(),
			dimStyle.Render("[enter] next  •  [esc] back"))

	case wizStepProvider:
		body = fmt.Sprintf("%s\n\n%s\n\n%s",
			title, m.providerList.View(),
			dimStyle.Render("[↑↓] navigate  •  [enter] select  •  [esc] back"))

	case wizStepModel:
		body = fmt.Sprintf("%s\n\nModel override (leave empty for provider default):\n\n%s\n\n%s",
			title, m.modelInput.View(),
			dimStyle.Render("[enter] next  •  [esc] back"))

	case wizStepPrompt:
		errLine := ""
		if m.promptErr != "" {
			errLine = "\n\n" + errStyle.Render("Error: "+m.promptErr)
		}
		body = fmt.Sprintf("%s\n\nWhat should this job do on each run?\n\n%s%s\n\n%s",
			title, m.promptInput.View(), errLine,
			dimStyle.Render("enter: new line  •  tab: done  •  esc: back"))

	case wizStepPermissions:
		body = fmt.Sprintf("%s\n\nWhat file access should this job have?\n\n%s\n\n%s",
			title, m.permissionsList.View(),
			dimStyle.Render("[↑↓] navigate  •  [enter] select  •  [esc] back"))

	case wizStepScheduleKind:
		body = fmt.Sprintf("%s\n\nHow often should this run?\n\n%s\n\n%s",
			title, m.scheduleKindList.View(),
			dimStyle.Render("[↑↓] navigate  •  [enter] select  •  [esc] back"))

	case wizStepInterval:
		body = fmt.Sprintf("%s\n\n%s\n\n%s",
			title, m.intervalList.View(),
			dimStyle.Render("[↑↓] navigate  •  [enter] select  •  [esc] back"))

	case wizStepTimeOfDay:
		body = fmt.Sprintf("%s\n\nWhat time should it run? (HH:MM)\n\n%s\n\n%s",
			title, m.timeOfDayInput.View(),
			dimStyle.Render("[enter] next  •  [esc] back"))

	case wizStepDayOfWeek:
		body = fmt.Sprintf("%s\n\nWhich day of the week?\n\n%s\n\n%s",
			title, m.dayOfWeekList.View(),
			dimStyle.Render("[↑↓] navigate  •  [enter] select  •  [esc] back"))

	case wizStepCron:
		body = fmt.Sprintf("%s\n\nCron expression (5 fields):\n\n%s\n\n%s",
			title, m.cronInput.View(),
			dimStyle.Render("[enter] next  •  [esc] back"))

	case wizStepTimezone:
		body = fmt.Sprintf("%s\n\n%s\n\n%s",
			title, m.timezoneList.View(),
			dimStyle.Render("[↑↓] navigate  •  [enter] select  •  [esc] back"))

	case wizStepCustomTz:
		body = fmt.Sprintf("%s\n\nEnter IANA timezone:\n\n%s\n\n%s",
			title, m.customTzInput.View(),
			dimStyle.Render("[enter] confirm  •  [esc] back"))

	case wizStepSummary:
		header = headerStyle.Render("░░ DREAMER ░░ Job Summary")
		scheduleDesc := m.describeSchedule()
		permDesc := m.describePermissions()
		body = fmt.Sprintf("%s\n\n"+
			"  Name:        %s\n"+
			"  Project:     %s\n"+
			"  Provider:    %s\n"+
			"  Schedule:    %s\n"+
			"  Permissions: %s\n\n"+
			"  Prompt:\n%s\n\n%s",
			title,
			m.answers.name,
			m.answers.projectPath,
			m.answers.providerID,
			scheduleDesc,
			permDesc,
			lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).Padding(0, 1).Width(innerW-4).Render(m.answers.prompt),
			dimStyle.Render("[enter] create  •  [esc] back"))
	}

	box := style.Render(body)

	centerW := m.width
	if centerW <= 0 {
		centerW = 80
	}
	targetW := boxOuter
	if targetW < 68 {
		targetW = 68
	}
	navHint := dimStyle.Render("  ← / esc: back    enter: next    ctrl+c: quit")
	if m.step == wizStepPrompt {
		navHint = dimStyle.Render("  esc: back    ctrl+enter: next    ctrl+c: quit")
	}
	if centerW >= targetW {
		header = lipgloss.PlaceHorizontal(centerW, lipgloss.Center, header)
		box = lipgloss.PlaceHorizontal(centerW, lipgloss.Center, box)
		navHint = lipgloss.PlaceHorizontal(centerW, lipgloss.Center, navHint)
	}
	layout := header + "\n" + box + "\n" + navHint

	centerH := m.height
	if centerH <= 0 {
		centerH = 40
	}
	if centerH > 0 {
		layout = lipgloss.PlaceVertical(centerH, lipgloss.Center, layout)
	}
	return layout
}

// describeSchedule returns a human-readable schedule description for the summary.
func (m jobWizardModel) describeSchedule() string {
	switch m.answers.scheduleKind {
	case "interval":
		every := m.answers.every
		if every == "" {
			every = "1h"
		}
		return fmt.Sprintf("every %s (%s)", every, m.answers.timezone)
	case "daily":
		tod := m.answers.timeOfDay
		if tod == "" {
			tod = "09:00"
		}
		return fmt.Sprintf("daily at %s (%s)", tod, m.answers.timezone)
	case "weekly":
		dow := m.answers.dayOfWeek
		tod := m.answers.timeOfDay
		if tod == "" {
			tod = "09:00"
		}
		return fmt.Sprintf("weekly on %s at %s (%s)", dow, tod, m.answers.timezone)
	case "cron":
		return fmt.Sprintf("cron: %s (%s)", m.answers.cron, m.answers.timezone)
	default:
		return m.answers.scheduleKind
	}
}

// describePermissions returns a human-readable permission description for the summary.
func (m jobWizardModel) describePermissions() string {
	switch m.answers.fileAccess {
	case "read_only":
		return "read-only"
	default:
		return m.answers.fileAccess
	}
}

// jobAnswersToCreateInput converts wizard answers into a createJobInput,
// applying defaults for fields the wizard doesn't collect (model, projectName).
func jobAnswersToCreateInput(a jobWizardAnswers, defaultProvider string) createJobInput {
	providerID := a.providerID
	if providerID == "" {
		providerID = defaultProvider
	}
	return createJobInput{
		projectPath:   a.projectPath,
		projectName:   pipeline.DeriveProjectName(a.projectPath, nil),
		name:          a.name,
		providerID:    providerID,
		model:         a.model,
		prompt:        a.prompt,
		fileAccess:    a.fileAccess,
		writablePaths: a.writablePaths,
		schedule: backgroundjobs.ScheduleSpec{
			Kind:      backgroundjobs.ScheduleKind(a.scheduleKind),
			Every:     a.every,
			TimeOfDay: a.timeOfDay,
			DayOfWeek: a.dayOfWeek,
			Cron:      a.cron,
			Timezone:  a.timezone,
		},
		timezone: a.timezone,
	}
}

// backgroundSafeProviderItems builds a list.Item slice of only
// background-safe providers from the analyzer registry.
func backgroundSafeProviderItems() []list.Item {
	meta := analyzer.RegisteredProviderMeta()
	items := make([]list.Item, 0, len(meta))
	for _, m := range meta {
		if m.Capabilities.BackgroundSafe {
			items = append(items, providerItem{id: string(m.ID), desc: m.DisplayName})
		}
	}
	return items
}

// timezoneItems returns common timezone entries plus a "custom..." option.
func timezoneItems() []list.Item {
	return []list.Item{
		providerItem{id: "Asia/Kolkata", desc: "IST, UTC+5:30"},
		providerItem{id: "America/New_York", desc: "ET, UTC-5 / EDT UTC-4"},
		providerItem{id: "America/Chicago", desc: "CT, UTC-6 / CDT UTC-5"},
		providerItem{id: "America/Denver", desc: "MT, UTC-7 / MDT UTC-6"},
		providerItem{id: "America/Los_Angeles", desc: "PT, UTC-8 / PDT UTC-7"},
		providerItem{id: "Europe/London", desc: "GMT, UTC+0 / BST UTC+1"},
		providerItem{id: "Europe/Berlin", desc: "CET, UTC+1 / CEST UTC+2"},
		providerItem{id: "Europe/Paris", desc: "CET, UTC+1 / CEST UTC+2"},
		providerItem{id: "Asia/Tokyo", desc: "JST, UTC+9"},
		providerItem{id: "Asia/Shanghai", desc: "CST, UTC+8"},
		providerItem{id: "Asia/Singapore", desc: "SGT, UTC+8"},
		providerItem{id: "Australia/Sydney", desc: "AEST, UTC+10 / AEDT UTC+11"},
		providerItem{id: "UTC", desc: "UTC+0"},
		providerItem{id: "custom", desc: "type any IANA timezone"},
	}
}
