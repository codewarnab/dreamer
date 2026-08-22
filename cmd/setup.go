package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
	"dreamer/internal/fsutil"
)

const (
	stepProvider  = iota
	stepTransport // sub-step: shown only for provider families with >1 connection option
	stepModel
	stepCustomModel // sub-step: text input for a model id not in the list
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

type selectItem struct {
	id    string
	desc  string
	title string // display override; falls back to id when empty
}

func (p selectItem) Title() string {
	if p.title != "" {
		return p.title
	}
	return p.id
}
func (p selectItem) Description() string { return p.desc }
func (p selectItem) FilterValue() string { return p.id }

// compactDelegate renders one row per item ("id  description") so a list
// of N items occupies exactly N lines plus the title. Avoids the default
// delegate's 2-line + spacing layout that makes short menus look hollow.
type compactDelegate struct{}

func (compactDelegate) Height() int                             { return 1 }
func (compactDelegate) Spacing() int                            { return 0 }
func (compactDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (compactDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(selectItem)
	if !ok {
		return
	}
	display := it.Title()
	line := display
	if it.desc != "" {
		line += "  " + lipgloss.NewStyle().Foreground(colorDesc).Render(it.desc)
	}
	cursor := "  "
	if index == m.Index() {
		cursor = lipgloss.NewStyle().Foreground(colorAccent).Render("> ")
		line = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(display)
		if it.desc != "" {
			line += "  " + lipgloss.NewStyle().Foreground(colorItemDesc).Render(it.desc)
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
	families         []providerFamily
	providerList     list.Model
	transportList    list.Model
	modelList        list.Model
	customModelInput textinput.Model
	customModelErr   string
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

	// Live model discovery (Option C: static fallback + live merge).
	// prior holds the existing config (nil on first run) so a live ListModels
	// probe can reuse the user's provider block (CLI paths, api-key env).
	// cacheDir is where the daemon persists model lists; "" disables both the
	// warm read and the write-back. fetchingModels drives the "checking…" hint.
	prior          *config.App
	cacheDir       string
	modelCache     *analyzer.ModelListCache
	fetchingModels bool
}

// prefillFromConfig pulls defaults out of an existing config so re-running
// setup --force pre-fills every prompt with the prior value. Only fields the
// loader/wizard understand are read; everything else stays at its zero value.
func prefillFromConfig(prior *config.App) setupAnswers {
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

// transportInfo describes one connection variant within a provider family.
// id is the full provider id (e.g. "copilot-sdk"); kind is the transport
// suffix (e.g. "sdk").
type transportInfo struct {
	id      string
	kind    string
	display string // the provider's registered DisplayName
}

// providerFamily groups provider variants that share a vendor but differ
// only in how dreamer connects to it (SDK vs ACP vs CLI vs server). The
// setup wizard lists families first, then asks which transport to use only
// when a family offers more than one.
type providerFamily struct {
	name       string // family key, e.g. "copilot"
	display    string // label shown on the provider page
	desc       string // one-line hint shown next to the label
	transports []transportInfo
}

// familyDisplayNames maps a family key to a friendly vendor label. Families
// not listed fall back to a title-cased key.
var familyDisplayNames = map[string]string{
	"copilot":    "GitHub Copilot",
	"claude":     "Anthropic Claude",
	"gemini":     "Google Gemini",
	"codex":      "OpenAI Codex",
	"opencode":   "OpenCode",
	"openclaude": "OpenClaude",
	"kiro":       "Kiro",
	"codebuff":   "Codebuff",
}

// transportLabels maps a transport suffix to a display label for the
// connection sub-page.
var transportLabels = map[string]string{
	"sdk":    "SDK",
	"acp":    "ACP",
	"cli":    "CLI",
	"server": "Server",
}

// transportDescriptions gives a one-line pros/cons hint per transport kind,
// shown next to each option on the connection sub-page.
var transportDescriptions = map[string]string{
	"sdk":    "Native SDK — fastest and most stable; talks to the provider API directly.",
	"cli":    "Drives the vendor CLI (stream-json) — reuses your existing CLI login.",
	"acp":    "Agent Client Protocol (JSON-RPC) — standardized, editor-agnostic.",
	"server": "HTTP server mode — connects to a long-running provider process.",
}

// familyOf splits a provider id into its family prefix on the last hyphen
// ("copilot-sdk" -> "copilot"). IDs without a hyphen are their own family.
func familyOf(id string) string {
	if i := strings.LastIndex(id, "-"); i >= 0 {
		return id[:i]
	}
	return id
}

// transportOf returns the transport suffix of a provider id ("copilot-sdk"
// -> "sdk"), or "" when the id has no hyphen.
func transportOf(id string) string {
	if i := strings.LastIndex(id, "-"); i >= 0 {
		return id[i+1:]
	}
	return ""
}

func familyDisplay(name string) string {
	if d, ok := familyDisplayNames[name]; ok {
		return d
	}
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func transportLabel(kind string) string {
	if l, ok := transportLabels[kind]; ok {
		return l
	}
	if kind == "" {
		return "Default"
	}
	return kind
}

// transportDescription returns the one-line hint for a transport kind, falling
// back to a generic line for kinds without a curated description (and for the
// empty kind of a provider id with no transport suffix) so list entries are
// never blank.
func transportDescription(kind string) string {
	if d, ok := transportDescriptions[kind]; ok {
		return d
	}
	return "Default connection for this provider."
}

// providerFamilies groups the registered providers by family, preserving
// the registry's order so the wizard list stays in sync as providers are
// added. Single-transport families keep their full DisplayName as the label;
// multi-transport families get a vendor label plus a count hint.
func providerFamilies() []providerFamily {
	meta := analyzer.RegisteredProviderMeta()
	var order []string
	byName := map[string]*providerFamily{}
	for _, m := range meta {
		id := string(m.ID)
		fam := familyOf(id)
		f, ok := byName[fam]
		if !ok {
			f = &providerFamily{name: fam}
			byName[fam] = f
			order = append(order, fam)
		}
		f.transports = append(f.transports, transportInfo{
			id:      id,
			kind:    transportOf(id),
			display: m.DisplayName,
		})
	}
	out := make([]providerFamily, 0, len(order))
	for _, name := range order {
		f := byName[name]
		if len(f.transports) == 1 {
			f.display = f.transports[0].display
		} else {
			f.display = familyDisplay(name)
			f.desc = fmt.Sprintf("%d connection options", len(f.transports))
		}
		out = append(out, *f)
	}
	return out
}

// familyItems builds the provider page's list from grouped families.
func familyItems(families []providerFamily) []list.Item {
	items := make([]list.Item, 0, len(families))
	for _, f := range families {
		items = append(items, selectItem{id: f.name, title: f.display, desc: f.desc})
	}
	return items
}

func newSetupModel(advanced, skipStartup bool, initial setupAnswers, prior *config.App) setupModel {
	families := providerFamilies()
	providers := familyItems(families)
	// Use a sensible initial width; WindowSizeMsg will update it.
	initialW := 80
	providerList := newCompactList("1/5 - Provider", providers, initialW-20)

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
		selectItem{id: "error"},
		selectItem{id: "warn"},
		selectItem{id: "info"},
		selectItem{id: "debug"},
	}
	logLevelList := newCompactList("6/10 - Log level", levels, initialW-20)

	ruleTimeoutInput := textinput.New()
	ruleTimeoutInput.Placeholder = "120"
	if initial.ruleTimeout > 0 {
		ruleTimeoutInput.SetValue(strconv.Itoa(initial.ruleTimeout))
	} else {
		ruleTimeoutInput.SetValue("120")
	}

	maxConcInput := textinput.New()
	maxConcInput.Placeholder = "0 (= len(chunks))"
	if initial.maxConcurrency > 0 {
		maxConcInput.SetValue(strconv.Itoa(initial.maxConcurrency))
	}

	maxChunkInput := textinput.New()
	maxChunkInput.Placeholder = "480000"
	if initial.maxChunkBytes > 0 {
		maxChunkInput.SetValue(strconv.Itoa(initial.maxChunkBytes))
	} else {
		maxChunkInput.SetValue("480000")
	}

	projectPathInput := textinput.New()
	projectPathInput.Placeholder = "/absolute/path/to/project"
	if initial.projectPath != "" {
		projectPathInput.SetValue(initial.projectPath)
	}

	projectNameInput := textinput.New()
	projectNameInput.Placeholder = "(default: basename of path)"
	if initial.projectName != "" {
		projectNameInput.SetValue(initial.projectName)
	}

	customModelInput := textinput.New()
	customModelInput.Placeholder = "e.g. claude-opus-4-7"
	customModelInput.CharLimit = 128

	sinces := []list.Item{
		selectItem{id: "24h"},
		selectItem{id: "7d"},
		selectItem{id: "30d"},
		selectItem{id: "lifetime"},
	}
	sinceList := newCompactList("10/10 - Project lookback (since)", sinces, 60)

	// Warm a model-list cache from the daemon's on-disk cache (when a prior
	// config points at an output root). This lets the model step show live
	// models the daemon/web previously fetched, even before this run's own
	// async probe returns. Failures are non-fatal — we fall back to statics.
	var cacheDir string
	modelCache := &analyzer.ModelListCache{}
	if prior != nil {
		cacheDir = prior.Daemon.OutputRoot
		if cacheDir != "" {
			_ = modelCache.Load(cacheDir)
		}
	}

	m := setupModel{
		step:             stepProvider,
		advanced:         advanced,
		skipStartup:      skipStartup,
		families:         families,
		providerList:     providerList,
		customModelInput: customModelInput,
		freqInput:        freq,
		outputInput:      out,
		logLevelList:     logLevelList,
		ruleTimeoutInput: ruleTimeoutInput,
		maxConcInput:     maxConcInput,
		maxChunkInput:    maxChunkInput,
		projectPathInput: projectPathInput,
		projectNameInput: projectNameInput,
		projectSinceList: sinceList,
		answers:          initial,
		prior:            prior,
		cacheDir:         cacheDir,
		modelCache:       modelCache,
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
	switch typedMsg := msg.(type) {
	case tea.KeyMsg:
		key := typedMsg.String()
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
		m.width = typedMsg.Width
		m.height = typedMsg.Height
		// Box inner width: terminal minus box border (2) + padding (4) minus some margin (6).
		// Clamp to a usable range so tiny and huge terminals both look fine.
		innerWidth := typedMsg.Width - 12
		if innerWidth < 40 {
			innerWidth = 40
		}
		if innerWidth > 90 {
			innerWidth = 90
		}
		m.providerList.SetWidth(innerWidth)
		if m.step == stepTransport {
			m.transportList.SetWidth(innerWidth)
		}
		if m.step == stepModel {
			m.modelList.SetWidth(innerWidth)
		}
		m.logLevelList.SetWidth(innerWidth)
		m.projectSinceList.SetWidth(innerWidth)
	case modelsFetchedMsg:
		m.applyFetchedModels(typedMsg)
		return m, nil
	}

	var cmd tea.Cmd
	switch m.step {
	case stepProvider:
		m.providerList, cmd = m.providerList.Update(msg)
	case stepTransport:
		m.transportList, cmd = m.transportList.Update(msg)
	case stepModel:
		m.modelList, cmd = m.modelList.Update(msg)
	case stepCustomModel:
		m.customModelInput, cmd = m.customModelInput.Update(msg)
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
	case stepTransport:
		m.step = stepProvider
	case stepModel:
		if m.providerHasTransportChoice() {
			m.step = stepTransport
		} else {
			m.step = stepProvider
		}
	case stepFrequency:
		m.step = stepModel
	case stepCustomModel:
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
		return m, m.advanceProvider()
	case stepTransport:
		return m, m.advanceTransport()
	case stepModel:
		m.advanceModel()
	case stepCustomModel:
		m.advanceCustomModel()
	case stepFrequency:
		m.advanceFrequency()
	case stepOutputRoot:
		m.advanceOutputRoot()
	case stepStartupYN:
		m.step = m.afterStartupStep()
	case stepLogLevel:
		m.advanceLogLevel()
	case stepRuleTimeout:
		m.advanceRuleTimeout()
	case stepParallelYN:
		m.advanceParallelYN()
	case stepMaxConcurrency:
		m.advanceMaxConcurrency()
	case stepMaxChunkBytes:
		m.advanceMaxChunkBytes()
	case stepFirstProjectYN:
		m.advanceFirstProjectYN()
	case stepProjectPath:
		if !m.advanceProjectPath() {
			return m, nil
		}
	case stepProjectName:
		m.advanceProjectName()
	case stepProjectSince:
		m.advanceProjectSince()
	case stepSummary:
		m.confirmed = true
		return m, tea.Quit
	}
	return m, nil
}

// familyByName returns the grouped family with the given key, or nil.
func (m setupModel) familyByName(name string) *providerFamily {
	for i := range m.families {
		if m.families[i].name == name {
			return &m.families[i]
		}
	}
	return nil
}

// providerHasTransportChoice reports whether the currently selected
// provider belongs to a family that offers more than one transport, so the
// wizard knows whether stepTransport sits between provider and model.
func (m setupModel) providerHasTransportChoice() bool {
	f := m.familyByName(familyOf(m.answers.provider))
	return f != nil && len(f.transports) > 1
}

// advanceProvider records the chosen family. Families with a single
// transport skip straight to the model step (returning its live-fetch cmd);
// families with several show the connection sub-page first.
func (m *setupModel) advanceProvider() tea.Cmd {
	sel, ok := m.providerList.SelectedItem().(selectItem)
	if !ok {
		return nil
	}
	fam := m.familyByName(sel.id)
	if fam == nil || len(fam.transports) == 0 {
		return nil
	}
	if len(fam.transports) == 1 {
		m.answers.provider = fam.transports[0].id
		return m.gotoModelStep()
	}
	items := make([]list.Item, len(fam.transports))
	for i, t := range fam.transports {
		items[i] = selectItem{id: t.id, title: transportLabel(t.kind), desc: transportDescription(t.kind)}
	}
	// Sub-page of the provider step (step 1), not a top-level step of its own,
	// so it carries no "n/5" counter that would desync the rest of the numbering.
	m.transportList = newCompactList(fam.display+": choose connection", items, m.providerList.Width())
	m.step = stepTransport
	return nil
}

// advanceTransport records the chosen transport variant and moves on,
// returning the model step's live-fetch cmd.
func (m *setupModel) advanceTransport() tea.Cmd {
	if sel, ok := m.transportList.SelectedItem().(selectItem); ok {
		m.answers.provider = sel.id
	}
	return m.gotoModelStep()
}

// gotoModelStep builds the model picker for the chosen provider and advances.
// The initial list merges any warm-cached live models with the static
// defaults; the returned cmd (when non-nil) probes the running provider for a
// fresh list that is merged in via modelsFetchedMsg.
func (m *setupModel) gotoModelStep() tea.Cmd {
	provider := m.answers.provider
	models := mergeModelLists(m.warmModels(provider), defaultModelsFor(provider))
	m.setModelList(models)
	m.step = stepModel

	if !providerSupportsModelListing(provider, m.prior) {
		m.fetchingModels = false
		return nil
	}
	m.fetchingModels = true
	return fetchModelsCmd(provider, m.prior, m.cacheDir)
}

// warmModels returns model names already known for provider from the on-disk
// cache the daemon/web populated, or nil when none.
func (m *setupModel) warmModels(provider string) []string {
	if m.modelCache == nil {
		return nil
	}
	return m.modelCache.Peek(provider)
}

// setModelList rebuilds the model picker from models, keeping the current
// selection when that model survives the rebuild and otherwise defaulting to
// the first entry. A trailing "custom…" entry opens a text input for model ids
// that are not listed. answers.model is synced to the resulting selection.
func (m *setupModel) setModelList(models []string) {
	prevSelected := m.answers.model
	items := make([]list.Item, 0, len(models)+1)
	selectIdx := 0
	for i, model := range models {
		items = append(items, selectItem{id: model})
		if model == prevSelected {
			selectIdx = i
		}
	}
	items = append(items, selectItem{id: customModelOption, desc: "enter a model id"})
	width := m.providerList.Width()
	m.modelList = newCompactList(m.modelStepTitle(), items, width)
	if len(models) > 0 {
		m.modelList.Select(selectIdx)
		m.answers.model = models[selectIdx]
	}
}

// modelStepTitle renders the model step heading, appending a discovery hint
// while a live probe is in flight.
func (m *setupModel) modelStepTitle() string {
	title := "2/5 - Model for " + m.answers.provider
	if m.fetchingModels {
		title += "  (checking for latest models…)"
	}
	return title
}

// customModelOption is the sentinel id of the "enter a model id" entry in the
// model picker. It must never collide with a real model id.
const customModelOption = "__custom__"

func (m *setupModel) advanceModel() {
	sel, ok := m.modelList.SelectedItem().(selectItem)
	if !ok {
		return
	}
	if sel.id == customModelOption {
		m.customModelErr = ""
		m.customModelInput.Focus()
		m.step = stepCustomModel
		return
	}
	m.answers.model = sel.id
	m.freqInput.Focus()
	m.step = stepFrequency
}

// advanceCustomModel commits the manually entered model id. Empty input is
// rejected; ids already present in the picker are accepted as-is (the check
// result stays visible in the sub-step view).
func (m *setupModel) advanceCustomModel() {
	model := strings.TrimSpace(m.customModelInput.Value())
	if model == "" {
		m.customModelErr = "model id is required"
		return
	}
	m.customModelErr = ""
	m.answers.model = model
	m.freqInput.Focus()
	m.step = stepFrequency
}

// customModelExists reports whether the id typed into the custom-model input
// matches an entry already shown in the model picker (static defaults, warm
// cache, and any live-fetched models).
func (m setupModel) customModelExists() bool {
	id := strings.TrimSpace(m.customModelInput.Value())
	if id == "" {
		return false
	}
	for _, item := range m.modelList.Items() {
		if sel, ok := item.(selectItem); ok && sel.id == id {
			return true
		}
	}
	return false
}

func (m *setupModel) advanceFrequency() {
	if v, err := strconv.Atoi(strings.TrimSpace(m.freqInput.Value())); err == nil && v > 0 {
		m.answers.frequency = v * 60 // minutes -> seconds
	}
	m.outputInput.Focus()
	m.step = stepOutputRoot
}

func (m *setupModel) advanceOutputRoot() {
	m.answers.outputRoot = strings.TrimSpace(m.outputInput.Value())
	if m.skipStartup {
		m.answers.startupInstall = false
		m.step = m.afterStartupStep()
	} else {
		m.step = stepStartupYN
	}
}

func (m *setupModel) advanceLogLevel() {
	if sel, ok := m.logLevelList.SelectedItem().(selectItem); ok {
		m.answers.logLevel = sel.id
	}
	m.ruleTimeoutInput.Focus()
	m.step = stepRuleTimeout
}

func (m *setupModel) advanceRuleTimeout() {
	if v, err := strconv.Atoi(strings.TrimSpace(m.ruleTimeoutInput.Value())); err == nil && v > 0 {
		m.answers.ruleTimeout = v
	}
	m.step = stepParallelYN
}

func (m *setupModel) advanceParallelYN() {
	if m.answers.parallel {
		m.maxConcInput.Focus()
		m.step = stepMaxConcurrency
	} else {
		m.maxChunkInput.Focus()
		m.step = stepMaxChunkBytes
	}
}

func (m *setupModel) advanceMaxConcurrency() {
	if v, err := strconv.Atoi(strings.TrimSpace(m.maxConcInput.Value())); err == nil && v >= 0 {
		m.answers.maxConcurrency = v
	}
	m.maxChunkInput.Focus()
	m.step = stepMaxChunkBytes
}

func (m *setupModel) advanceMaxChunkBytes() {
	if v, err := strconv.Atoi(strings.TrimSpace(m.maxChunkInput.Value())); err == nil && v > 0 {
		m.answers.maxChunkBytes = v
	}
	m.step = stepFirstProjectYN
}

func (m *setupModel) advanceFirstProjectYN() {
	if m.answers.firstProject {
		m.projectPathInput.Focus()
		m.projectPathErr = ""
		m.step = stepProjectPath
	} else {
		m.step = stepSummary
	}
}

// advanceProjectPath returns false if validation fails and the step should not advance.
func (m *setupModel) advanceProjectPath() bool {
	p := strings.TrimSpace(m.projectPathInput.Value())
	if err := validateProjectPath(p); err != nil {
		m.projectPathErr = err.Error()
		return false
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
	return true
}

func (m *setupModel) advanceProjectName() {
	name := strings.TrimSpace(m.projectNameInput.Value())
	if name == "" {
		name = filepath.Base(m.answers.projectPath)
	}
	m.answers.projectName = name
	m.step = stepProjectSince
}

func (m *setupModel) advanceProjectSince() {
	if sel, ok := m.projectSinceList.SelectedItem().(selectItem); ok {
		m.answers.projectSince = sel.id
	}
	m.step = stepSummary
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
	bannerStyle := lipgloss.NewStyle().Foreground(colorAccent)

	// Compute box width from terminal size.  The inner content width is what
	// lists and text inputs use; the box adds border (2) + padding (4).
	boxInner := m.width - 12 // margin for border+padding+outer breathing room
	if boxInner < 40 {
		boxInner = 40
	}
	if boxInner > 90 {
		boxInner = 90
	}
	boxOuter := boxInner + 2 // lipgloss Width includes padding; border adds 2
	style := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(boxInner)

	var body string
	switch m.step {
	case stepProvider:
		body = m.providerList.View()
	case stepTransport:
		body = m.transportList.View()
	case stepModel:
		body = m.modelList.View()
	case stepCustomModel:
		status := "New model — saved as-is."
		if m.customModelErr != "" {
			status = "Error: " + m.customModelErr
		} else if m.customModelExists() {
			status = "Already in the list above — Enter selects it."
		}
		body = fmt.Sprintf("2/5 - Model id for %s:\n\n%s\n\n%s\n\n(Press Enter to accept)", m.answers.provider, m.customModelInput.View(), status)
	case stepFrequency:
		body = fmt.Sprintf("3/5 - How often should dreamer check your projects?\n\nDreamer runs in the background and re-analyzes your\nprojects on a schedule. Enter the gap between runs,\nin minutes (e.g. 60 = hourly, 1440 = once a day).\n\n%s minutes\n\n(Press Enter to accept)", m.freqInput.View())
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
		body += "\nPress Enter to confirm or Ctrl+C to cancel.\n\nYou can change any of these settings later from the web dashboard."
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
		Foreground(colorDim).
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

// modelsFetchedMsg carries the result of an async live model-list probe back
// into the bubbletea event loop. An empty models slice means the probe failed
// or the provider does not support listing — the static list stays in place.
type modelsFetchedMsg struct {
	provider string
	models   []string
}

// mergeModelLists merges a live (authoritative) list with the static fallback,
// preserving live order — its first element is the provider's reported default
// — then appending any static-only models. Entries are de-duplicated, keeping
// first occurrence; empty strings are dropped. Either argument may be empty.
func mergeModelLists(live, static []string) []string {
	seen := make(map[string]struct{}, len(live)+len(static))
	out := make([]string, 0, len(live)+len(static))
	for _, group := range [][]string{live, static} {
		for _, model := range group {
			if model == "" {
				continue
			}
			if _, dup := seen[model]; dup {
				continue
			}
			seen[model] = struct{}{}
			out = append(out, model)
		}
	}
	return out
}

// providerConfigForProbe builds the analyzer.ProviderConfig used by the live
// model probe, reusing the user's existing provider block (CLI paths, api-key
// env) when prior config exists. Sandbox is forced off: a model-list probe is
// a lightweight capability query, and running sandbox prepare from this path
// has triggered heap corruption on Windows (see handlers.enrichWithLiveModels).
func providerConfigForProbe(providerID string, prior *config.App) analyzer.ProviderConfig {
	var block config.ProviderBlock
	var sandboxCfg config.SandboxConfig
	if prior != nil {
		if prior.Providers != nil {
			block = prior.Providers[providerID]
		}
		sandboxCfg = prior.Sandbox
	}
	cfg := analyzer.ProviderConfigFromBlock(providerID, block, sandboxCfg)
	cfg.Sandbox = "false"
	return cfg
}

// providerSupportsModelListing reports whether the provider can be constructed
// and implements analyzer.ModelLister. Construction is cheap (no process is
// spawned until Start), so this gates the spinner and the async probe without
// flicker for providers that can only ever serve the static list.
func providerSupportsModelListing(providerID string, prior *config.App) bool {
	factory, ok := analyzer.LookupProvider(analyzer.ProviderID(providerID))
	if !ok {
		return false
	}
	prov, err := factory(providerConfigForProbe(providerID, prior))
	if err != nil {
		return false
	}
	defer prov.Close()
	_, ok = prov.(analyzer.ModelLister)
	return ok
}

// modelProbeTimeout bounds the live probe (Start + ListModels). ACP providers
// cold-start a child process and exchange initialize/session round-trips, so a
// generous budget avoids cutting off a slow-but-working provider. The probe runs
// off the UI goroutine, so this never blocks keystrokes.
const modelProbeTimeout = 30 * time.Second

// fetchModelsCmd returns a tea.Cmd that probes a running provider for its live
// model list and, on success, persists it to the daemon's on-disk cache so the
// web UI and future wizard runs benefit. It always resolves to a
// modelsFetchedMsg (empty on any failure) so the UI can clear its spinner.
func fetchModelsCmd(providerID string, prior *config.App, cacheDir string) tea.Cmd {
	return func() tea.Msg {
		fail := modelsFetchedMsg{provider: providerID}
		factory, ok := analyzer.LookupProvider(analyzer.ProviderID(providerID))
		if !ok {
			return fail
		}
		prov, err := factory(providerConfigForProbe(providerID, prior))
		if err != nil {
			return fail
		}
		lister, ok := prov.(analyzer.ModelLister)
		if !ok {
			return fail
		}
		ctx, cancel := context.WithTimeout(context.Background(), modelProbeTimeout)
		defer cancel()
		defer prov.Close()
		if err := prov.Start(ctx); err != nil {
			return fail
		}
		models, err := lister.ListModels(ctx)
		if err != nil || len(models) == 0 {
			return fail
		}
		persistModelList(providerID, models, cacheDir)
		return modelsFetchedMsg{provider: providerID, models: models}
	}
}

// persistModelList writes a freshly probed model list into the daemon's
// on-disk cache, preserving entries for other providers. Best-effort: a
// missing cache dir or an I/O error is silently ignored — the live list is
// still applied to the running wizard regardless.
func persistModelList(providerID string, models []string, cacheDir string) {
	if cacheDir == "" {
		return
	}
	cache := &analyzer.ModelListCache{}
	_ = cache.Load(cacheDir) // keep other providers' entries
	cache.Set(providerID, models)
	_ = cache.Save(cacheDir)
}

// applyFetchedModels merges a completed probe's result into the model picker.
// It ignores stale results (a different provider, e.g. after the user navigated
// back and chose another) but always clears the spinner for the active provider.
func (m *setupModel) applyFetchedModels(msg modelsFetchedMsg) {
	if msg.provider != m.answers.provider {
		return // stale: user moved to a different provider
	}
	m.fetchingModels = false
	if len(msg.models) == 0 {
		// Probe failed; keep the static/warm list but refresh the title to
		// drop the "checking…" hint.
		m.modelList.Title = m.modelStepTitle()
		return
	}
	m.setModelList(mergeModelLists(msg.models, defaultModelsFor(m.answers.provider)))
}

// defaultModelsFor returns the known-good models for a provider.
// First entry matches config.DefaultModelFor for that provider.
// Used by both the setup wizard and the job wizard's model picker.
func defaultModelsFor(provider string) []string {
	if models := config.AllModelsFor(provider); len(models) > 0 {
		return models
	}
	if m := config.DefaultModelFor(provider); m != "" {
		return []string{m}
	}
	return []string{config.DefaultModel}
}

// newCompactList creates a bubbletea list with compactDelegate styling,
// help and status bar disabled. Used by both wizards for consistent look.
func newCompactList(title string, items []list.Item, width int) list.Model {
	l := list.New(items, compactDelegate{}, width, listHeight(len(items)))
	l.Title = title
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	// Disable pagination. These menus are short and always sized to fit every
	// item, but bubbles' list subtracts the paginator's row from the available
	// height when pagination is on (list.updatePagination). That makes PerPage
	// one short of the item count, forcing a phantom second page whose "••"
	// dots hide the last option even with plenty of screen space.
	l.SetShowPagination(false)
	return l
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
		fmt.Fprintf(os.Stderr,
			"warning: config template render failed (%v); writing minimal fallback config\n", err)
		cfg := config.App{
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

// existingConfigModel is a tiny two-option prompt shown when config.yaml
// already exists and the user ran `setup` without --force on a terminal. It
// lets them edit (re-run the wizard, pre-filled) or cancel without retyping
// the command with --force.
type existingConfigModel struct {
	cfgPath string
	list    list.Model
	edit    bool
	quit    bool
	width   int
	height  int
}

func newExistingConfigModel(cfgPath string) existingConfigModel {
	items := []list.Item{
		selectItem{id: "edit", title: "Edit existing config", desc: "Re-run the wizard, pre-filled from your current settings"},
		selectItem{id: "cancel", title: "Cancel", desc: "Leave config.yaml unchanged"},
	}
	return existingConfigModel{cfgPath: cfgPath, list: newCompactList("Config already exists", items, 60)}
}

func (m existingConfigModel) Init() tea.Cmd { return nil }

func (m existingConfigModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typedMsg := msg.(type) {
	case tea.KeyMsg:
		switch typedMsg.String() {
		case "ctrl+c", "esc", "q":
			m.quit = true
			return m, tea.Quit
		case "enter":
			if sel, ok := m.list.SelectedItem().(selectItem); ok && sel.id == "edit" {
				m.edit = true
			}
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = typedMsg.Width
		m.height = typedMsg.Height
		innerWidth := typedMsg.Width - 12
		if innerWidth < 40 {
			innerWidth = 40
		}
		if innerWidth > 90 {
			innerWidth = 90
		}
		m.list.SetWidth(innerWidth)
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m existingConfigModel) View() string {
	if m.quit {
		return ""
	}
	header := fmt.Sprintf("Config already exists at %s\n\nWhat would you like to do?\n\n", m.cfgPath)
	navHint := lipgloss.NewStyle().Foreground(colorDim).Render("  ↑/↓: move    enter: select    esc: cancel")
	return header + m.list.View() + "\n" + navHint
}

// promptExistingConfig runs the edit/cancel prompt and reports whether the
// user chose to edit. Returns false on cancel or quit. Rendered inline (no
// alt screen) so it stays a compact selector in the existing terminal.
func promptExistingConfig(cfgPath string) (bool, error) {
	prog := tea.NewProgram(newExistingConfigModel(cfgPath))
	final, err := prog.Run()
	if err != nil {
		return false, err
	}
	m, ok := final.(existingConfigModel)
	if !ok {
		return false, fmt.Errorf("setup: unexpected model %T from existing-config prompt", final)
	}
	return m.edit && !m.quit, nil
}

func newSetupCommand() *cobra.Command {
	var (
		advanced, force, noStartup, nonInteractive bool
		niProvider, niModel, niOutputRoot          string
		niFrequency                                int
	)
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive TUI wizard that writes <UserConfigDir>/dreamer/config.yaml.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if nonInteractive {
				return runSetupNonInteractive(cmd, force, niProvider, niModel, niFrequency, niOutputRoot)
			}
			cfgPath, err := config.GlobalConfigPath()
			if err != nil {
				return fmt.Errorf("resolve config path: %w", err)
			}
			if _, statErr := os.Stat(cfgPath); statErr == nil && !force {
				// On a terminal, offer an inline edit/cancel choice so the
				// user need not re-run with --force. Without a TTY on both
				// stdin and stdout (agents, CI, redirected output), keep the
				// actionable error pointing at --force. The --non-interactive
				// path is handled above and never reaches here.
				if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
					return configExistsError(cmd, cfgPath)
				}
				editChosen, err := promptExistingConfig(cfgPath)
				if err != nil {
					return err
				}
				if !editChosen {
					fmt.Fprintln(cmd.OutOrStdout(), "setup cancelled; no changes written")
					return nil
				}
				// User chose to edit: fall through and overwrite, just like --force.
			}

			// Pre-fill from prior config when re-running with --force. The
			// parsed prior also feeds the live model probe (provider block,
			// output root for the model-list cache); nil on a fresh install.
			var prefilled setupAnswers
			var prior *config.App
			if data, err := os.ReadFile(cfgPath); err == nil {
				var parsed config.App
				if yaml.Unmarshal(data, &parsed) == nil {
					prior = &parsed
					prefilled = prefillFromConfig(&parsed)
				}
			}
			initial := newSetupModel(advanced, noStartup, prefilled, prior)

			prog := tea.NewProgram(initial, tea.WithAltScreen())
			final, err := prog.Run()
			if err != nil {
				return err
			}
			finalModel, ok := final.(setupModel)
			if !ok {
				return fmt.Errorf("setup: unexpected model %T from wizard", final)
			}
			if finalModel.quit || !finalModel.confirmed {
				fmt.Fprintln(cmd.OutOrStdout(), "setup cancelled; no changes written")
				return nil
			}

			out := buildConfigYAML(finalModel.answers)
			if err := ensureGlobalConfigDir(cfgPath); err != nil {
				return err
			}
			if err := fsutil.WriteFileAtomic(cfgPath, out, fsutil.SecretPerms); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "config written to %s\n", cfgPath)
			if finalModel.answers.startupInstall && !noStartup {
				fmt.Fprintln(cmd.OutOrStdout(), "next step: run 'dreamer startup install'")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "note: re-running setup rewrites this file and drops YAML comments.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&advanced, "advanced", false, "Branch into advanced steps (log level, rule timeout, parallel, chunking, first project).")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing config.yaml without confirmation.")
	cmd.Flags().BoolVar(&noStartup, "no-startup", false, "Skip the startup-install step.")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Write config from flags without TUI (requires --provider and --output-root).")
	cmd.Flags().StringVar(&niProvider, "provider", "", "Provider id (e.g. copilot, claude, codex). Required with --non-interactive.")
	cmd.Flags().StringVar(&niModel, "model", "", "Model override (default: provider-specific default).")
	cmd.Flags().IntVar(&niFrequency, "frequency", config.DefaultFrequencySeconds, "Analysis interval in seconds.")
	cmd.Flags().StringVar(&niOutputRoot, "output-root", "", "Output directory for todos and state. Required with --non-interactive.")
	return cmd
}

// ensureGlobalConfigDir creates <UserConfigDir>/dreamer if missing.
// fsutil.WriteFileAtomic requires an existing parent directory, and a
// first-run `setup` is the very first thing that touches this path.
func ensureGlobalConfigDir(cfgPath string) error {
	if err := os.MkdirAll(filepath.Dir(cfgPath), fsutil.DirPerms); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	return nil
}

// runSetupNonInteractive writes config.yaml directly from flag values,
// bypassing the bubbletea TUI. This enables agents and CI to configure
// dreamer without an interactive terminal.
func runSetupNonInteractive(cmd *cobra.Command, force bool, provider, model string, frequency int, outputRoot string) error {
	if provider == "" {
		return missingFlagError(cmd, "provider",
			"The LLM provider to use (e.g. claude, copilot, codex).",
			"dreamer setup --non-interactive --provider claude --output-root /path/to/output")
	}
	if outputRoot == "" {
		return missingFlagError(cmd, "output-root",
			"The directory where dreamer saves results, logs, and state.",
			"dreamer setup --non-interactive --provider claude --output-root /path/to/output")
	}
	if !analyzer.IsRegisteredProvider(provider) {
		return fmt.Errorf("unknown provider %q (known providers: %s)",
			provider, strings.Join(analyzer.RegisteredProviderIDStrings(), ", "))
	}

	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}
	if _, statErr := os.Stat(cfgPath); statErr == nil && !force {
		return configExistsError(cmd, cfgPath)
	}

	// Use provider default model when not specified.
	if model == "" {
		model = config.DefaultModelFor(provider)
	}

	answers := setupAnswers{
		provider:   provider,
		model:      model,
		frequency:  frequency,
		outputRoot: outputRoot,
	}
	out := buildConfigYAML(answers)
	if err := ensureGlobalConfigDir(cfgPath); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(cfgPath, out, fsutil.SecretPerms); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "config written to %s\n", cfgPath)
	return nil
}
