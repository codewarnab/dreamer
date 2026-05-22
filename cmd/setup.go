package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
	"dreamer/internal/fsutil"
)

const (
	stepProvider = iota
	stepModel
	stepFrequency
	stepOutputRoot
	stepStartupYN
	// Advanced (G.2) steps 6-10.
	stepLogLevel
	stepRuleTimeout
	stepParallelYN
	stepMaxConcurrency
	stepMaxChunkBytes
	stepFirstProjectYN
	stepProjectPath
	stepProjectName
	stepProjectSince
	stepSummary
)

type setupAnswers struct {
	provider       string
	model          string
	frequency      int // seconds
	outputRoot     string
	startupInstall bool

	// Advanced (G.2)
	logLevel       string
	ruleTimeout    int
	parallel       bool
	maxConcurrency int
	maxChunkBytes  int

	firstProject bool
	projectPath  string
	projectName  string
	projectSince string
}

type providerItem struct {
	id   string
	desc string
}

func (p providerItem) Title() string       { return p.id }
func (p providerItem) Description() string { return p.desc }
func (p providerItem) FilterValue() string { return p.id }

// compactDelegate renders one row per item ("id  description") so a list
// of N items occupies exactly N lines plus the title. Avoids the default
// delegate's 2-line + spacing layout that makes short menus look hollow.
type compactDelegate struct{}

func (compactDelegate) Height() int                             { return 1 }
func (compactDelegate) Spacing() int                            { return 0 }
func (compactDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (compactDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(providerItem)
	if !ok {
		return
	}
	line := it.id
	if it.desc != "" {
		line += "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#949494")).Render(it.desc)
	}
	cursor := "  "
	if index == m.Index() {
		cursor = lipgloss.NewStyle().Foreground(lipgloss.Color("#3cffd0")).Render("> ")
		line = lipgloss.NewStyle().Foreground(lipgloss.Color("#3cffd0")).Bold(true).Render(it.id)
		if it.desc != "" {
			line += "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#e9e9e9")).Render(it.desc)
		}
	}
	fmt.Fprint(w, cursor+line)
}

// listHeight returns the row count a compactDelegate list needs for n
// items, plus 2 rows for the title and a trailing spacer.
func listHeight(n int) int {
	if n < 1 {
		n = 1
	}
	return n + 2
}

type setupModel struct {
	step             int
	advanced         bool
	skipStartup      bool
	answers          setupAnswers
	providerList     list.Model
	modelList        list.Model
	freqInput        textinput.Model
	outputInput      textinput.Model
	logLevelList     list.Model
	ruleTimeoutInput textinput.Model
	maxConcInput     textinput.Model
	maxChunkInput    textinput.Model
	projectPathInput textinput.Model
	projectNameInput textinput.Model
	projectSinceList list.Model
	projectPathErr   string
	quit             bool
	confirmed        bool
	width            int
	height           int
}

// prefillFromConfig pulls defaults out of an existing config so re-running
// setup --force pre-fills every prompt with the prior value. Only fields the
// loader/wizard understand are read; everything else stays at its zero value.
func prefillFromConfig(prior *config.Config) setupAnswers {
	a := setupAnswers{
		provider:    config.DefaultProviderID,
		frequency:   3600,
		logLevel:    config.DefaultLogLevel,
		ruleTimeout: 120,
	}
	if prior == nil {
		return a
	}
	if prior.DefaultProvider != "" {
		a.provider = prior.DefaultProvider
	}
	if pb, ok := prior.Providers[a.provider]; ok && pb.Model != "" {
		a.model = pb.Model
	}
	if prior.Daemon.FrequencySeconds > 0 {
		a.frequency = prior.Daemon.FrequencySeconds
	}
	if prior.Daemon.OutputRoot != "" {
		a.outputRoot = prior.Daemon.OutputRoot
	}
	if prior.Logging.Level != "" {
		a.logLevel = prior.Logging.Level
	}
	if prior.Analyzer.RuleTimeoutSeconds > 0 {
		a.ruleTimeout = prior.Analyzer.RuleTimeoutSeconds
	}
	if prior.Analyzer.Execution.Mode == config.ExecutionModeParallel {
		a.parallel = true
	}
	if prior.Analyzer.Execution.MaxConcurrency > 0 {
		a.maxConcurrency = prior.Analyzer.Execution.MaxConcurrency
	}
	if prior.Analyzer.Chunking.MaxChunkBytes > 0 {
		a.maxChunkBytes = prior.Analyzer.Chunking.MaxChunkBytes
	}
	if len(prior.Projects) > 0 {
		a.firstProject = true
		a.projectPath = prior.Projects[0].Path
		a.projectName = prior.Projects[0].Name
		a.projectSince = prior.Projects[0].Since
	}
	return a
}

// providerItems builds the setup wizard's provider list from the analyzer
// registry so it stays in sync when new providers are added.
func providerItems() []list.Item {
	meta := analyzer.RegisteredProviderMeta()
	items := make([]list.Item, 0, len(meta))
	for _, m := range meta {
		items = append(items, providerItem{string(m.ID), m.DisplayName})
	}
	return items
}

func newSetupModel(advanced, skipStartup bool, initial setupAnswers) setupModel {
	providers := providerItems()
	// Use a sensible initial width; WindowSizeMsg will update it.
	initialW := 80
	pl := list.New(providers, compactDelegate{}, initialW-20, listHeight(len(providers)))
	pl.Title = "1/5 - Provider"
	pl.SetShowHelp(false)
	pl.SetShowStatusBar(false)

	freq := textinput.New()
	freq.Placeholder = "60"
	if initial.frequency > 0 {
		freq.SetValue(strconv.Itoa(initial.frequency / 60))
	} else {
		freq.SetValue("60")
	}

	defaultRoot, _ := config.UserConfigRoot()
	out := textinput.New()
	out.Placeholder = defaultRoot
	if initial.outputRoot != "" {
		out.SetValue(initial.outputRoot)
	}

	// Advanced inputs.
	levels := []list.Item{
		providerItem{"error", ""},
		providerItem{"warn", ""},
		providerItem{"info", ""},
		providerItem{"debug", ""},
	}
	ll := list.New(levels, compactDelegate{}, initialW-20, listHeight(len(levels)))
	ll.Title = "6/10 - Log level"
	ll.SetShowHelp(false)
	ll.SetShowStatusBar(false)

	rt := textinput.New()
	rt.Placeholder = "120"
	if initial.ruleTimeout > 0 {
		rt.SetValue(strconv.Itoa(initial.ruleTimeout))
	} else {
		rt.SetValue("120")
	}

	mc := textinput.New()
	mc.Placeholder = "0 (= len(chunks))"
	if initial.maxConcurrency > 0 {
		mc.SetValue(strconv.Itoa(initial.maxConcurrency))
	}

	mcb := textinput.New()
	mcb.Placeholder = "480000"
	if initial.maxChunkBytes > 0 {
		mcb.SetValue(strconv.Itoa(initial.maxChunkBytes))
	} else {
		mcb.SetValue("480000")
	}

	pp := textinput.New()
	pp.Placeholder = "/absolute/path/to/project"
	if initial.projectPath != "" {
		pp.SetValue(initial.projectPath)
	}

	pn := textinput.New()
	pn.Placeholder = "(default: basename of path)"
	if initial.projectName != "" {
		pn.SetValue(initial.projectName)
	}

	sinces := []list.Item{
		providerItem{"24h", ""},
		providerItem{"7d", ""},
		providerItem{"30d", ""},
		providerItem{"lifetime", ""},
	}
	sl := list.New(sinces, compactDelegate{}, 60, listHeight(len(sinces)))
	sl.Title = "10/10 - Project lookback (since)"
	sl.SetShowHelp(false)
	sl.SetShowStatusBar(false)

	m := setupModel{
		step:             stepProvider,
		advanced:         advanced,
		skipStartup:      skipStartup,
		providerList:     pl,
		freqInput:        freq,
		outputInput:      out,
		logLevelList:     ll,
		ruleTimeoutInput: rt,
		maxConcInput:     mc,
		maxChunkInput:    mcb,
		projectPathInput: pp,
		projectNameInput: pn,
		projectSinceList: sl,
		answers:          initial,
	}
	if m.answers.provider == "" {
		m.answers.provider = config.DefaultProviderID
	}
	if m.answers.frequency == 0 {
		m.answers.frequency = 3600
	}
	return m
}

func (m setupModel) Init() tea.Cmd { return textinput.Blink }

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch t := msg.(type) {
	case tea.KeyMsg:
		key := t.String()
		// Step-specific key handling for yes/no prompts.
		switch m.step {
		case stepStartupYN:
			switch key {
			case "enter":
				m.answers.startupInstall = true
				return m.advance()
			case "esc":
				m.answers.startupInstall = false
				return m.advance()
			case "y":
				m.answers.startupInstall = true
				return m.advance()
			case "n":
				m.answers.startupInstall = false
				return m.advance()
			}
		case stepParallelYN:
			switch strings.ToLower(key) {
			case "y":
				m.answers.parallel = true
				return m.advance()
			case "n":
				m.answers.parallel = false
				return m.advance()
			}
		case stepFirstProjectYN:
			switch strings.ToLower(key) {
			case "y":
				m.answers.firstProject = true
				return m.advance()
			case "n":
				m.answers.firstProject = false
				return m.advance()
			}
		}
		// Global keys for all other steps.
		switch key {
		case "ctrl+c":
			m.quit = true
			return m, tea.Quit
		case "left":
			return m.goBack()
		case "esc":
			// In yes/no steps, esc already means "skip/no"; for all
			// other steps it navigates back.
			return m.goBack()
		case "enter":
			return m.advance()
		}
	case tea.WindowSizeMsg:
		m.width = t.Width
		m.height = t.Height
		// Box inner width: terminal minus box border (2) + padding (4) minus some margin (6).
		// Clamp to a usable range so tiny and huge terminals both look fine.
		w := t.Width - 12
		if w < 40 {
			w = 40
		}
		if w > 90 {
			w = 90
		}
		m.providerList.SetWidth(w)
		if m.step == stepModel {
			m.modelList.SetWidth(w)
		}
		m.logLevelList.SetWidth(w)
		m.projectSinceList.SetWidth(w)
	}

	var cmd tea.Cmd
	switch m.step {
	case stepProvider:
		m.providerList, cmd = m.providerList.Update(msg)
	case stepModel:
		m.modelList, cmd = m.modelList.Update(msg)
	case stepFrequency:
		m.freqInput, cmd = m.freqInput.Update(msg)
	case stepOutputRoot:
		m.outputInput, cmd = m.outputInput.Update(msg)
	case stepLogLevel:
		m.logLevelList, cmd = m.logLevelList.Update(msg)
	case stepRuleTimeout:
		m.ruleTimeoutInput, cmd = m.ruleTimeoutInput.Update(msg)
	case stepMaxConcurrency:
		m.maxConcInput, cmd = m.maxConcInput.Update(msg)
	case stepMaxChunkBytes:
		m.maxChunkInput, cmd = m.maxChunkInput.Update(msg)
	case stepProjectPath:
		m.projectPathInput, cmd = m.projectPathInput.Update(msg)
	case stepProjectName:
		m.projectNameInput, cmd = m.projectNameInput.Update(msg)
	case stepProjectSince:
		m.projectSinceList, cmd = m.projectSinceList.Update(msg)
	}
	return m, cmd
}

// goBack returns to the previous wizard step.  The reverse path mirrors
// advance() but must account for skipped branches (startup, advanced,
// parallel, first-project).
func (m setupModel) goBack() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepProvider:
		// First step — nowhere to go.
	case stepModel:
		m.step = stepProvider
	case stepFrequency:
		m.step = stepModel
	case stepOutputRoot:
		m.freqInput.Focus()
		m.step = stepFrequency
	case stepStartupYN:
		m.outputInput.Focus()
		m.step = stepOutputRoot
	case stepLogLevel:
		if m.skipStartup {
			m.outputInput.Focus()
			m.step = stepOutputRoot
		} else {
			m.step = stepStartupYN
		}
	case stepRuleTimeout:
		m.step = stepLogLevel
	case stepParallelYN:
		m.ruleTimeoutInput.Focus()
		m.step = stepRuleTimeout
	case stepMaxConcurrency:
		m.step = stepParallelYN
	case stepMaxChunkBytes:
		if m.answers.parallel {
			m.maxConcInput.Focus()
			m.step = stepMaxConcurrency
		} else {
			m.step = stepParallelYN
		}
	case stepFirstProjectYN:
		m.maxChunkInput.Focus()
		m.step = stepMaxChunkBytes
	case stepProjectPath:
		m.step = stepFirstProjectYN
	case stepProjectName:
		m.projectPathInput.Focus()
		m.step = stepProjectPath
	case stepProjectSince:
		m.projectNameInput.Focus()
		m.step = stepProjectName
	case stepSummary:
		if m.answers.firstProject {
			m.step = stepProjectSince
		} else if m.advanced {
			m.maxChunkInput.Focus()
			m.step = stepMaxChunkBytes
		} else if !m.skipStartup {
			m.step = stepStartupYN
		} else {
			m.outputInput.Focus()
			m.step = stepOutputRoot
		}
	}
	return m, nil
}

func (m setupModel) advance() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepProvider:
		if sel, ok := m.providerList.SelectedItem().(providerItem); ok {
			m.answers.provider = sel.id
		}
		models := defaultModelsFor(m.answers.provider)
		items := make([]list.Item, len(models))
		for i, mm := range models {
			items[i] = providerItem{mm, ""}
		}
		ml := list.New(items, compactDelegate{}, m.providerList.Width(), listHeight(len(items)))
		ml.Title = "2/5 - Model for " + m.answers.provider
		ml.SetShowHelp(false)
		ml.SetShowStatusBar(false)
		m.modelList = ml
		if len(models) > 0 {
			m.answers.model = models[0]
		}
		m.step = stepModel
	case stepModel:
		if sel, ok := m.modelList.SelectedItem().(providerItem); ok {
			m.answers.model = sel.id
		}
		m.freqInput.Focus()
		m.step = stepFrequency
	case stepFrequency:
		if v, err := strconv.Atoi(strings.TrimSpace(m.freqInput.Value())); err == nil && v > 0 {
			m.answers.frequency = v * 60 // minutes -> seconds
		}
		m.outputInput.Focus()
		m.step = stepOutputRoot
	case stepOutputRoot:
		m.answers.outputRoot = strings.TrimSpace(m.outputInput.Value())
		if m.skipStartup {
			m.answers.startupInstall = false
			m.step = m.afterStartupStep()
		} else {
			m.step = stepStartupYN
		}
	case stepStartupYN:
		m.step = m.afterStartupStep()
	case stepLogLevel:
		if sel, ok := m.logLevelList.SelectedItem().(providerItem); ok {
			m.answers.logLevel = sel.id
		}
		m.ruleTimeoutInput.Focus()
		m.step = stepRuleTimeout
	case stepRuleTimeout:
		if v, err := strconv.Atoi(strings.TrimSpace(m.ruleTimeoutInput.Value())); err == nil && v > 0 {
			m.answers.ruleTimeout = v
		}
		m.step = stepParallelYN
	case stepParallelYN:
		if m.answers.parallel {
			m.maxConcInput.Focus()
			m.step = stepMaxConcurrency
		} else {
			m.maxChunkInput.Focus()
			m.step = stepMaxChunkBytes
		}
	case stepMaxConcurrency:
		if v, err := strconv.Atoi(strings.TrimSpace(m.maxConcInput.Value())); err == nil && v >= 0 {
			m.answers.maxConcurrency = v
		}
		m.maxChunkInput.Focus()
		m.step = stepMaxChunkBytes
	case stepMaxChunkBytes:
		if v, err := strconv.Atoi(strings.TrimSpace(m.maxChunkInput.Value())); err == nil && v > 0 {
			m.answers.maxChunkBytes = v
		}
		m.step = stepFirstProjectYN
	case stepFirstProjectYN:
		if m.answers.firstProject {
			m.projectPathInput.Focus()
			m.projectPathErr = ""
			m.step = stepProjectPath
		} else {
			m.step = stepSummary
		}
	case stepProjectPath:
		p := strings.TrimSpace(m.projectPathInput.Value())
		if err := validateProjectPath(p); err != nil {
			m.projectPathErr = err.Error()
			return m, nil
		}
		m.answers.projectPath = p
		m.projectPathErr = ""
		// Default name = basename of path.
		if strings.TrimSpace(m.projectNameInput.Value()) == "" {
			base := filepath.Base(p)
			m.projectNameInput.SetValue(base)
			m.answers.projectName = base
		}
		m.projectNameInput.Focus()
		m.step = stepProjectName
	case stepProjectName:
		name := strings.TrimSpace(m.projectNameInput.Value())
		if name == "" {
			name = filepath.Base(m.answers.projectPath)
		}
		m.answers.projectName = name
		m.step = stepProjectSince
	case stepProjectSince:
		if sel, ok := m.projectSinceList.SelectedItem().(providerItem); ok {
			m.answers.projectSince = sel.id
		}
		m.step = stepSummary
	case stepSummary:
		m.confirmed = true
		return m, tea.Quit
	}
	return m, nil
}

// afterStartupStep picks the next step after the startup-install prompt
// (or after stepOutputRoot when --no-startup). Branches into the advanced
// flow when --advanced is set; otherwise jumps to the summary.
func (m setupModel) afterStartupStep() int {
	if m.advanced {
		return stepLogLevel
	}
	return stepSummary
}

// validateProjectPath enforces step 10's "must exist + absolute" rule.
func validateProjectPath(p string) error {
	if p == "" {
		return fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("path must be absolute")
	}
	info, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("path does not exist: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}
	return nil
}

func (m setupModel) View() string {
	if m.quit {
		return ""
	}
	bannerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#3cffd0"))

	// Compute box width from terminal size.  The inner content width is what
	// lists and text inputs use; the box adds border (2) + padding (4).
	boxInner := m.width - 12 // margin for border+padding+outer breathing room
	if boxInner < 40 {
		boxInner = 40
	}
	if boxInner > 90 {
		boxInner = 90
	}
	boxOuter := boxInner + 6 // border(2) + padding(4)
	style := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(boxInner)

	var body string
	switch m.step {
	case stepProvider:
		body = m.providerList.View()
	case stepModel:
		body = m.modelList.View()
	case stepFrequency:
		body = fmt.Sprintf("3/5 - Daemon frequency (minutes):\n\n%s\n\n(Press Enter to accept)", m.freqInput.View())
	case stepOutputRoot:
		body = fmt.Sprintf("4/5 - Where should dreamer save results?\n(analysis reports, logs, and project state)\n\n%s\n\nPress Enter to use the default, or type a custom folder path.", m.outputInput.View())
	case stepStartupYN:
		var hookDesc string
		if runtime.GOOS == "windows" {
			hookDesc = "Registers a Windows Task Scheduler entry so the daemon\nstarts automatically when you log in."
		} else {
			hookDesc = "Installs a systemd user service so the daemon\nstarts automatically when you log in."
		}
		body = fmt.Sprintf("5/5 - Start dreamer automatically?\n\n%s\n\nEnter = yes, Esc = skip", hookDesc)
	case stepLogLevel:
		body = m.logLevelList.View()
	case stepRuleTimeout:
		body = fmt.Sprintf("7/10 - Analyzer rule timeout (seconds):\n\n%s\n\n(Press Enter to accept)", m.ruleTimeoutInput.View())
	case stepParallelYN:
		body = "8/10 - Parallel execution mode? Press 'y' to enable, 'n' or Enter to skip."
	case stepMaxConcurrency:
		body = fmt.Sprintf("8.5/10 - Max concurrency (0 = len(chunks)):\n\n%s\n\n(Press Enter to accept)", m.maxConcInput.View())
	case stepMaxChunkBytes:
		body = fmt.Sprintf("9/10 - Max chunk bytes:\n\n%s\n\n(Press Enter to accept)", m.maxChunkInput.View())
	case stepFirstProjectYN:
		body = "10/10 - Configure a first project? Press 'y' to configure, 'n' or Enter to skip."
	case stepProjectPath:
		errLine := ""
		if m.projectPathErr != "" {
			errLine = "\n\nError: " + m.projectPathErr
		}
		body = fmt.Sprintf("10/10 - Project path (absolute, must exist):\n\n%s%s\n\n(Press Enter to accept)", m.projectPathInput.View(), errLine)
	case stepProjectName:
		body = fmt.Sprintf("10/10 - Project name:\n\n%s\n\n(Press Enter to accept; empty = basename of path)", m.projectNameInput.View())
	case stepProjectSince:
		body = m.projectSinceList.View()
	case stepSummary:
		out := m.answers.outputRoot
		if out == "" {
			if root, err := config.UserConfigRoot(); err == nil {
				out = root + " (default)"
			} else {
				out = "(default)"
			}
		}
		body = fmt.Sprintf(
			"Ready to write config:\n\n  provider:          %s\n  model:             %s\n  frequency_seconds: %d\n  output_root:       %s\n  startup_install:   %v\n",
			m.answers.provider, m.answers.model, m.answers.frequency, out, m.answers.startupInstall,
		)
		if m.advanced {
			body += fmt.Sprintf(
				"  log_level:         %s\n  rule_timeout_s:    %d\n  parallel:          %v\n  max_concurrency:   %d\n  max_chunk_bytes:   %d\n",
				m.answers.logLevel, m.answers.ruleTimeout, m.answers.parallel, m.answers.maxConcurrency, m.answers.maxChunkBytes,
			)
			if m.answers.firstProject {
				body += fmt.Sprintf(
					"  project:           %s (%s) since=%s\n",
					m.answers.projectName, m.answers.projectPath, m.answers.projectSince,
				)
			}
		}
		body += "\nPress Enter to confirm or Ctrl+C to cancel.\n\nNote: re-running setup rewrites the file and drops YAML comments.\nKeep ui-overrides.yaml-bound edits in the web UI to preserve config.yaml comments."
	}
	banner := bannerStyle.Render(dreamerBanner)
	box := style.Render(body)

	// Center the banner and content box horizontally.
	centerWidth := m.width
	if centerWidth <= 0 {
		centerWidth = 80 // safe default when no TTY or before first WindowSizeMsg
	}
	// Use the larger of boxOuter and banner width as the centering target.
	// Banner is ~68 chars wide; skip centering if terminal is narrower.
	targetW := boxOuter
	if targetW < 68 {
		targetW = 68
	}
	// Navigation hint (dim, below the box).
	navHint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666666")).
		Render("  ← / esc: back    enter: next    ctrl+c: quit")
	if centerWidth >= targetW {
		banner = lipgloss.PlaceHorizontal(centerWidth, lipgloss.Center, banner)
		box = lipgloss.PlaceHorizontal(centerWidth, lipgloss.Center, box)
		navHint = lipgloss.PlaceHorizontal(centerWidth, lipgloss.Center, navHint)
	}
	layout := banner + "\n" + box + "\n" + navHint

	// Vertically center the whole layout in the terminal.
	centerHeight := m.height
	if centerHeight <= 0 {
		centerHeight = 40
	}
	if centerHeight > 0 {
		layout = lipgloss.PlaceVertical(centerHeight, lipgloss.Center, layout)
	}
	return layout
}

// defaultModelsFor returns the known-good models for a provider. Reuses
// config.DefaultModelByProvider; falls back to config.DefaultModel when the
// provider id is not in the map.
func defaultModelsFor(provider string) []string {
	if m, ok := config.DefaultModelByProvider[config.ProviderID(provider)]; ok && m != "" {
		return []string{m}
	}
	return []string{config.DefaultModel}
}

// dreamerBanner is the ASCII art splashed at the top of `dreamer setup`.
// 5-line block letters (~68 chars wide) for readability at 80+ col terminals.
const dreamerBanner = ` ██████╗ ██████╗ ███████╗ █████╗ ███╗   ███╗███████╗██████╗
 ██╔══██╗██╔══██╗██╔════╝██╔══██╗████╗ ████║██╔════╝██╔══██╗
 ██║  ██║██████╔╝█████╗  ███████║██╔████╔██║█████╗  ██████╔╝
 ██║  ██║██╔══██╗██╔══╝  ██╔══██║██║╚██╔╝██║██╔══╝  ██╔══██╗
 ██████╔╝██║  ██║███████╗██║  ██║██║ ╚═╝ ██║███████╗██║  ██║`

// buildConfigYAML renders the wizard's answers into the full commented
// config template. Comments document every configurable knob the daemon
// understands; the user's chosen values are interpolated into the right
// lines while other provider blocks stay at their canonical defaults.
func buildConfigYAML(a setupAnswers) []byte {
	out, err := renderCommentedConfig(a)
	if err != nil {
		// Template parse/execute failures are programmer errors; fall back
		// to a minimal struct-marshalled config so the wizard still writes
		// something valid rather than silently producing an empty file.
		cfg := config.Config{
			DefaultProvider: a.provider,
			Daemon: config.DaemonConfig{
				FrequencySeconds: a.frequency,
				OutputRoot:       a.outputRoot,
			},
			Providers: map[string]config.ProviderBlock{a.provider: {Model: a.model}},
		}
		raw, _ := yaml.Marshal(&cfg)
		return raw
	}
	return out
}

func newSetupCommand() *cobra.Command {
	var advanced, force, noStartup, nonInteractive bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive TUI wizard that writes <UserConfigDir>/dreamer/config.yaml.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if nonInteractive {
				return fmt.Errorf("--non-interactive is reserved and not yet supported in v1.5")
			}
			cfgPath, err := config.GlobalConfigPath()
			if err != nil {
				return fmt.Errorf("resolve config path: %w", err)
			}
			if _, statErr := os.Stat(cfgPath); statErr == nil && !force {
				fmt.Fprintf(cmd.OutOrStderr(), "config already exists at %s\n", cfgPath)
				return fmt.Errorf("config exists at %s; pass --force to overwrite", cfgPath)
			}

			// Pre-fill from prior config when re-running with --force.
			var prefilled setupAnswers
			if data, err := os.ReadFile(cfgPath); err == nil {
				var prior config.Config
				if yaml.Unmarshal(data, &prior) == nil {
					prefilled = prefillFromConfig(&prior)
				}
			}
			initial := newSetupModel(advanced, noStartup, prefilled)

			prog := tea.NewProgram(initial, tea.WithAltScreen())
			final, err := prog.Run()
			if err != nil {
				return err
			}
			mm := final.(setupModel)
			if mm.quit || !mm.confirmed {
				fmt.Fprintln(cmd.OutOrStdout(), "setup cancelled; no changes written")
				return nil
			}

			out := buildConfigYAML(mm.answers)
			if err := fsutil.WriteFileAtomic(cfgPath, out, fsutil.FilePerms); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "config written to %s\n", cfgPath)
			if mm.answers.startupInstall && !noStartup {
				fmt.Fprintln(cmd.OutOrStdout(), "next step: run 'dreamer startup install'")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "note: re-running setup rewrites this file and drops YAML comments.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&advanced, "advanced", false, "Branch into advanced steps (log level, rule timeout, parallel, chunking, first project).")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing config.yaml without confirmation.")
	cmd.Flags().BoolVar(&noStartup, "no-startup", false, "Skip the startup-install step.")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Reserved (errors in v1.5).")
	return cmd
}
