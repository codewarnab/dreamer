package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
)

const (
	stepProvider = iota
	stepModel
	stepFrequency
	stepOutputRoot
	stepStartupYN
	stepSummary
)

type setupAnswers struct {
	provider       string
	model          string
	frequency      int // seconds
	outputRoot     string
	startupInstall bool
}

type providerItem struct {
	id   string
	desc string
}

func (p providerItem) Title() string       { return p.id }
func (p providerItem) Description() string { return p.desc }
func (p providerItem) FilterValue() string { return p.id }

type setupModel struct {
	step         int
	advanced     bool
	skipStartup  bool
	answers      setupAnswers
	providerList list.Model
	modelList    list.Model
	freqInput    textinput.Model
	outputInput  textinput.Model
	quit         bool
	confirmed    bool
}

func newSetupModel(advanced, skipStartup bool) setupModel {
	providers := []list.Item{
		providerItem{"openclaude-cli", "OpenClaude CLI (recommended)"},
		providerItem{"copilot-sdk", "GitHub Copilot SDK"},
		providerItem{"copilot-acp", "Copilot via ACP"},
		providerItem{"claude-cli", "Anthropic Claude (stream-json)"},
		providerItem{"gemini-cli", "Google Gemini CLI"},
		providerItem{"codex-cli", "OpenAI Codex CLI"},
	}
	pl := list.New(providers, list.NewDefaultDelegate(), 60, 14)
	pl.Title = "1/5 - Provider"
	pl.SetShowHelp(false)
	pl.SetShowStatusBar(false)

	freq := textinput.New()
	freq.Placeholder = "60"
	freq.SetValue("60")

	out := textinput.New()
	out.Placeholder = "(empty = default: <UserConfigDir>/dreamer)"

	return setupModel{
		step:         stepProvider,
		advanced:     advanced,
		skipStartup:  skipStartup,
		providerList: pl,
		freqInput:    freq,
		outputInput:  out,
		answers:      setupAnswers{provider: "openclaude-cli", frequency: 3600},
	}
}

func (m setupModel) Init() tea.Cmd { return textinput.Blink }

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch t := msg.(type) {
	case tea.KeyMsg:
		key := t.String()
		switch key {
		case "ctrl+c", "esc":
			m.quit = true
			return m, tea.Quit
		case "enter":
			return m.advance()
		}
		// Step-specific key handling for the yes/no prompt.
		if m.step == stepStartupYN {
			switch strings.ToLower(key) {
			case "y":
				m.answers.startupInstall = true
				return m.advance()
			case "n":
				m.answers.startupInstall = false
				return m.advance()
			}
		}
	case tea.WindowSizeMsg:
		m.providerList.SetSize(t.Width-4, t.Height-8)
		if m.step == stepModel {
			m.modelList.SetSize(t.Width-4, t.Height-8)
		}
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
	}
	return m, cmd
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
		ml := list.New(items, list.NewDefaultDelegate(), 60, 14)
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
			m.step = stepSummary
		} else {
			m.step = stepStartupYN
		}
	case stepStartupYN:
		// Enter alone advances with current default (false unless 'y' was pressed).
		m.step = stepSummary
	case stepSummary:
		// G.2 will add the advanced branch here.
		// TODO(G.2): if m.advanced, transition into advanced steps instead of quitting.
		m.confirmed = true
		return m, tea.Quit
	}
	return m, nil
}

func (m setupModel) View() string {
	if m.quit {
		return ""
	}
	style := lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).Padding(1, 2)
	var body string
	switch m.step {
	case stepProvider:
		body = m.providerList.View()
	case stepModel:
		body = m.modelList.View()
	case stepFrequency:
		body = fmt.Sprintf("3/5 - Daemon frequency (minutes):\n\n%s\n\n(Press Enter to accept)", m.freqInput.View())
	case stepOutputRoot:
		body = fmt.Sprintf("4/5 - Output root (absolute path; empty = default):\n\n%s\n\n(Press Enter to accept)", m.outputInput.View())
	case stepStartupYN:
		body = "5/5 - Install startup hook? Press 'y' to install, 'n' or Enter to skip."
	case stepSummary:
		out := m.answers.outputRoot
		if out == "" {
			out = "(default)"
		}
		body = fmt.Sprintf(
			"Ready to write config:\n\n  provider:          %s\n  model:             %s\n  frequency_seconds: %d\n  output_root:       %s\n  startup_install:   %v\n\nPress Enter to confirm or Ctrl+C to cancel.\n\nNote: re-running setup rewrites the file and drops YAML comments.",
			m.answers.provider, m.answers.model, m.answers.frequency, out, m.answers.startupInstall,
		)
	}
	return style.Render(body)
}

// defaultModelsFor returns the known-good models for a provider. Reuses
// config.DefaultModelByProvider; falls back to config.DefaultModel when the
// provider id is not in the map.
func defaultModelsFor(provider string) []string {
	if m, ok := config.DefaultModelByProvider[provider]; ok && m != "" {
		return []string{m}
	}
	return []string{config.DefaultModel}
}

func buildConfigYAML(a setupAnswers) []byte {
	cfg := config.Config{
		DefaultProvider: a.provider,
		Daemon: config.DaemonConfig{
			FrequencySeconds: a.frequency,
			OutputRoot:       a.outputRoot,
		},
		Providers: map[string]config.ProviderBlock{
			a.provider: {Model: a.model},
		},
	}
	out, _ := yaml.Marshal(&cfg)
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

			initial := newSetupModel(advanced, noStartup)
			// If config exists and --force, pre-fill defaults from it.
			if data, err := os.ReadFile(cfgPath); err == nil {
				var prior config.Config
				if yaml.Unmarshal(data, &prior) == nil {
					if prior.DefaultProvider != "" {
						initial.answers.provider = prior.DefaultProvider
					}
					if prior.Daemon.FrequencySeconds > 0 {
						initial.answers.frequency = prior.Daemon.FrequencySeconds
						initial.freqInput.SetValue(strconv.Itoa(prior.Daemon.FrequencySeconds / 60))
					}
					if prior.Daemon.OutputRoot != "" {
						initial.answers.outputRoot = prior.Daemon.OutputRoot
						initial.outputInput.SetValue(prior.Daemon.OutputRoot)
					}
				}
			}

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
			if err := fsutil.WriteFileAtomic(cfgPath, out, 0o644); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "config written to %s\n", cfgPath)
			if mm.answers.startupInstall && !noStartup {
				// Avoid coupling to startup subcommand internals; just nudge.
				fmt.Fprintln(cmd.OutOrStdout(), "next step: run 'dreamer startup install'")
			}
			fmt.Fprintln(cmd.OutOrStdout(), "note: re-running setup rewrites this file and drops YAML comments.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&advanced, "advanced", false, "Branch into advanced steps after step 5 (G.2).")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing config.yaml without confirmation.")
	cmd.Flags().BoolVar(&noStartup, "no-startup", false, "Skip the startup-install step.")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Reserved (errors in v1.5).")
	return cmd
}
