package cmd

import (
	"fmt"
	"strings"

	"dreamer/internal/fsutil"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"
)

const (
	filePickerMaxVisible = 5 // max dropdown items shown at once
	filePickerMaxResults = 20
)

// fileListLoadedMsg carries the async result of file discovery.
type fileListLoadedMsg struct {
	files []string
	err   error
}

// filePickerModel is a bubbletea component that provides a text input with
// fuzzy file search. The user types to search files, navigates matches with
// Up/Down, and presses Enter/Tab to select. Multiple files can be selected.
type filePickerModel struct {
	textInput    textinput.Model
	allFiles     []string        // full file list from discovery
	filtered     []fuzzy.Match   // current fuzzy matches
	selected     map[string]bool // set of selected file paths
	cursor       int             // highlighted index in filtered list
	dropdownOpen bool
	loading      bool
	loadErr      error
	width        int
	projectRoot  string
}

func newFilePickerModel(projectRoot string, width int) filePickerModel {
	ti := textinput.New()
	ti.Placeholder = "search files..."
	ti.CharLimit = 2048
	ti.Width = width - 4
	ti.Focus()
	return filePickerModel{
		textInput:   ti,
		selected:    make(map[string]bool),
		width:       width,
		projectRoot: projectRoot,
		loading:     true,
	}
}

// Init starts async file discovery.
func (m filePickerModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.loadFiles())
}

func (m filePickerModel) loadFiles() tea.Cmd {
	return func() tea.Msg {
		files, err := fsutil.ListProjectFiles(m.projectRoot, 0)
		return fileListLoadedMsg{files: files, err: err}
	}
}

// Preselected returns a new model with the given paths pre-selected (used
// when navigating back to this step).
func (m filePickerModel) Preselected(paths []string) filePickerModel {
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p != "" {
			m.selected[p] = true
		}
	}
	return m
}

// SelectedPaths returns the sorted list of selected file paths.
func (m filePickerModel) SelectedPaths() []string {
	paths := make([]string, 0, len(m.selected))
	for p := range m.selected {
		paths = append(paths, p)
	}
	// Deterministic order for testing and display.
	sortStrings(paths)
	return paths
}

// sortStrings is a simple insertion sort for small slices.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (m filePickerModel) Update(msg tea.Msg) (filePickerModel, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case fileListLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.loadErr = msg.err
			return m, nil
		}
		m.allFiles = msg.files
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.textInput.Width = msg.Width - 4
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m filePickerModel) handleKey(msg tea.KeyMsg) (filePickerModel, tea.Cmd) {
	switch msg.String() {
	case "up":
		if m.dropdownOpen && len(m.filtered) > 0 {
			m.cursor--
			if m.cursor < 0 {
				m.cursor = len(m.filtered) - 1
			}
			return m, nil
		}

	case "down":
		if m.dropdownOpen && len(m.filtered) > 0 {
			m.cursor++
			if m.cursor >= len(m.filtered) {
				m.cursor = 0
			}
			return m, nil
		}

	case "tab", "enter":
		if m.dropdownOpen && len(m.filtered) > 0 {
			return m.selectMatch()
		}
		// If dropdown is closed, let the caller handle enter (advance step).
		return m, nil

	case "esc":
		if m.dropdownOpen {
			m.dropdownOpen = false
			return m, nil
		}
		return m, nil

	case "backspace":
		// Let the textinput handle the deletion, then re-evaluate.
		var cmd tea.Cmd
		m.textInput, cmd = m.textInput.Update(msg)
		m.updateFilter()
		return m, cmd
	}

	// Default: pass to textinput, then update fuzzy filter.
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	m.updateFilter()
	return m, cmd
}

// updateFilter reads the text input value and updates fuzzy match results.
// The entire input value is used as the search term — no @ prefix required.
func (m *filePickerModel) updateFilter() {
	val := m.textInput.Value()

	if val == "" {
		m.dropdownOpen = false
		m.filtered = nil
		return
	}

	if len(m.allFiles) == 0 {
		m.dropdownOpen = true
		m.filtered = nil
		m.cursor = 0
		return
	}

	// Fuzzy match against the full input value.
	m.filtered = fuzzy.Find(val, m.allFiles)
	if len(m.filtered) > filePickerMaxResults {
		m.filtered = m.filtered[:filePickerMaxResults]
	}

	m.dropdownOpen = true
	if m.cursor >= len(m.filtered) {
		m.cursor = 0
	}
}

// selectMatch adds the highlighted file to the selected set, clears the
// search input, and closes the dropdown so the user can search again.
func (m filePickerModel) selectMatch() (filePickerModel, tea.Cmd) {
	if m.cursor >= len(m.filtered) {
		return m, nil
	}

	chosen := m.filtered[m.cursor].Str

	// Add to selected set.
	m.selected[chosen] = true

	// Clear the input for the next search.
	m.textInput.SetValue("")

	// Close dropdown.
	m.dropdownOpen = false
	m.filtered = nil
	m.cursor = 0

	return m, nil
}

// View renders the file picker: text input, dropdown overlay, and selected files.
func (m filePickerModel) View() string {
	var b strings.Builder

	// Text input.
	b.WriteString(m.textInput.View())
	b.WriteString("\n")

	// Dropdown overlay.
	if m.loading {
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("  Loading files..."))
		b.WriteString("\n")
	} else if m.loadErr != nil {
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(
			fmt.Sprintf("  Error: %v", m.loadErr)))
		b.WriteString("\n")
	} else if m.dropdownOpen {
		b.WriteString(m.renderDropdown())
	}

	// Selected files.
	if len(m.selected) > 0 {
		b.WriteString("\n")
		header := lipgloss.NewStyle().Bold(true).Render(
			fmt.Sprintf("  %d writable file(s) selected:", len(m.selected)))
		b.WriteString(header)
		b.WriteString("\n")
		for _, p := range m.SelectedPaths() {
			marker := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render("+")
			b.WriteString(fmt.Sprintf("  %s %s\n", marker, p))
		}
	}

	return b.String()
}

func (m filePickerModel) renderDropdown() string {
	if len(m.filtered) == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Render("  No matching files") + "\n"
	}

	var b strings.Builder
	visible := filePickerMaxVisible
	if len(m.filtered) < visible {
		visible = len(m.filtered)
	}

	// Calculate scroll offset to keep cursor visible.
	offset := 0
	if m.cursor >= visible {
		offset = m.cursor - visible + 1
	}

	for i := 0; i < visible; i++ {
		idx := i + offset
		if idx >= len(m.filtered) {
			break
		}

		match := m.filtered[idx]
		prefix := "  "
		style := lipgloss.NewStyle()

		if idx == m.cursor {
			prefix = lipgloss.NewStyle().Foreground(colorAccent).Render("> ")
			style = style.Foreground(colorAccent).Bold(true)
		}

		// Render with match highlighting.
		rendered := highlightMatch(match)
		b.WriteString(prefix)
		b.WriteString(style.Render(rendered))
		b.WriteString("\n")
	}

	if len(m.filtered) > visible {
		remaining := len(m.filtered) - visible
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(
			fmt.Sprintf("  ... and %d more", remaining)))
		b.WriteString("\n")
	}

	return b.String()
}

// highlightMatch renders the file path with matched characters highlighted.
func highlightMatch(m fuzzy.Match) string {
	if len(m.MatchedIndexes) == 0 {
		return m.Str
	}

	runes := []rune(m.Str)
	matchSet := make(map[int]bool, len(m.MatchedIndexes))
	for _, idx := range m.MatchedIndexes {
		matchSet[idx] = true
	}

	highlightStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	normalStyle := lipgloss.NewStyle()

	var b strings.Builder
	for i, r := range runes {
		if matchSet[i] {
			b.WriteString(highlightStyle.Render(string(r)))
		} else {
			b.WriteString(normalStyle.Render(string(r)))
		}
	}
	return b.String()
}
