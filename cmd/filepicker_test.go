package cmd

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"
)

func TestFilePickerSearchAsYouType(t *testing.T) {
	m := newFilePickerModel(t.TempDir(), 80)
	m.allFiles = []string{"src/main.go", "src/lib.go", "README.md"}
	m.loading = false

	// Type "sr" — no @ needed.
	m.textInput.SetValue("sr")
	m.textInput.SetCursor(2)
	m.updateFilter()

	if !m.dropdownOpen {
		t.Fatal("expected dropdown open after typing 'sr'")
	}
	if len(m.filtered) == 0 {
		t.Fatal("expected fuzzy matches for 'sr'")
	}
	// "src/main.go" and "src/lib.go" should match.
	found := false
	for _, f := range m.filtered {
		if f.Str == "src/main.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected src/main.go in matches, got: %v", m.filtered)
	}
}

func TestFilePickerEmptyInputClosesDropdown(t *testing.T) {
	m := newFilePickerModel(t.TempDir(), 80)
	m.allFiles = []string{"main.go"}

	m.textInput.SetValue("")
	m.textInput.SetCursor(0)
	m.updateFilter()

	if m.dropdownOpen {
		t.Fatal("dropdown should not open for empty input")
	}
}

func TestFilePickerSearchFromStart(t *testing.T) {
	m := newFilePickerModel(t.TempDir(), 80)
	m.allFiles = []string{"main.go", "README.md"}
	m.loading = false

	m.textInput.SetValue("main")
	m.textInput.SetCursor(4)
	m.updateFilter()

	if !m.dropdownOpen {
		t.Fatal("expected dropdown open for 'main'")
	}
	if len(m.filtered) == 0 {
		t.Fatal("expected matches for 'main'")
	}
}

func TestFilePickerSelectMatch(t *testing.T) {
	m := newFilePickerModel(t.TempDir(), 80)
	m.allFiles = []string{"src/main.go", "src/lib.go", "README.md"}
	m.loading = false

	m.textInput.SetValue("src")
	m.textInput.SetCursor(3)
	m.updateFilter()

	if !m.dropdownOpen {
		t.Fatal("dropdown should be open")
	}

	// Select the first match.
	m, _ = m.selectMatch()

	if m.dropdownOpen {
		t.Fatal("dropdown should close after selection")
	}
	if len(m.selected) != 1 {
		t.Errorf("expected 1 selected file, got %d", len(m.selected))
	}

	// Input should be cleared after selection.
	if m.textInput.Value() != "" {
		t.Errorf("expected input cleared after selection, got %q", m.textInput.Value())
	}

	paths := m.SelectedPaths()
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	if paths[0] != "src/lib.go" && paths[0] != "src/main.go" {
		t.Errorf("unexpected selected path: %q", paths[0])
	}
}

func TestFilePickerMultiSelect(t *testing.T) {
	m := newFilePickerModel(t.TempDir(), 80)
	m.allFiles = []string{"a.go", "b.go", "c.go"}
	m.loading = false

	// Select a.go
	m.textInput.SetValue("a")
	m.textInput.SetCursor(1)
	m.updateFilter()
	m, _ = m.selectMatch()

	// Select b.go
	m.textInput.SetValue("b")
	m.textInput.SetCursor(1)
	m.updateFilter()
	m, _ = m.selectMatch()

	paths := m.SelectedPaths()
	if len(paths) != 2 {
		t.Fatalf("expected 2 selected paths, got %d: %v", len(paths), paths)
	}
	if !m.selected["a.go"] || !m.selected["b.go"] {
		t.Errorf("expected a.go and b.go selected, got: %v", paths)
	}
}

func TestFilePickerEscape(t *testing.T) {
	m := newFilePickerModel(t.TempDir(), 80)
	m.allFiles = []string{"main.go"}
	m.loading = false

	m.textInput.SetValue("main")
	m.textInput.SetCursor(4)
	m.updateFilter()

	if !m.dropdownOpen {
		t.Fatal("dropdown should be open")
	}

	m, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	if m.dropdownOpen {
		t.Fatal("dropdown should close on escape")
	}
}

func TestFilePickerNavigate(t *testing.T) {
	m := newFilePickerModel(t.TempDir(), 80)
	m.allFiles = []string{"a.go", "b.go", "c.go"}
	m.loading = false

	m.textInput.SetValue("go")
	m.textInput.SetCursor(2)
	m.updateFilter()

	if !m.dropdownOpen {
		t.Fatal("dropdown should be open")
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.cursor)
	}

	// Down
	m, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", m.cursor)
	}

	// Down wraps to 0
	m, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 0 {
		t.Errorf("cursor after wrap = %d, want 0", m.cursor)
	}

	// Up wraps to end
	m, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != len(m.filtered)-1 {
		t.Errorf("cursor after up-wrap = %d, want %d", m.cursor, len(m.filtered)-1)
	}
}

func TestFilePickerPreselected(t *testing.T) {
	m := newFilePickerModel(t.TempDir(), 80)
	m = m.Preselected([]string{"a.go", "b.go"})

	if !m.selected["a.go"] || !m.selected["b.go"] {
		t.Fatal("expected preselected files to be in selected set")
	}

	paths := m.SelectedPaths()
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
}

func TestFilePickerNoMatches(t *testing.T) {
	m := newFilePickerModel(t.TempDir(), 80)
	m.allFiles = []string{"main.go"}
	m.loading = false

	m.textInput.SetValue("zzzzz")
	m.textInput.SetCursor(5)
	m.updateFilter()

	if !m.dropdownOpen {
		t.Fatal("dropdown should be open even with no matches")
	}
	if len(m.filtered) != 0 {
		t.Errorf("expected 0 matches, got %d", len(m.filtered))
	}
}

func TestFilePickerAcceptsInputAfterConstruction(t *testing.T) {
	m := newFilePickerModel(t.TempDir(), 80)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should return a command")
	}
	// The model returned by Init should have the input focused.
	// We verify by checking that the textinput responds to key events.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if m2.textInput.Value() != "a" {
		t.Errorf("expected input to accept keystrokes after Init, got %q", m2.textInput.Value())
	}
}

func TestHighlightMatch(t *testing.T) {
	// Just verify it doesn't panic with various inputs.
	cases := []fuzzy.Match{
		{Str: "src/main.go", MatchedIndexes: []int{0, 4, 5}},
		{Str: "a.go", MatchedIndexes: []int{0}},
		{Str: "file.go", MatchedIndexes: nil},
	}
	for _, c := range cases {
		result := highlightMatch(c)
		if result == "" {
			t.Errorf("highlightMatch returned empty for %q", c.Str)
		}
	}
}
