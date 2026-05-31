package astcheck

import (
	"encoding/json"
	"fmt"
	"go/token"
	"io"
)

// Finding is a single diagnostic produced by an analyzer.
type Finding struct {
	Pos      token.Position
	Check    string
	Severity Severity
	Symbol   string // enclosing function/method/type name (for baseline keys)
	Message  string
}

// String returns a human-readable one-line representation.
func (f Finding) String() string {
	return fmt.Sprintf("%s: [%s] %s: %s",
		f.Pos.String(), f.Severity, f.Check, f.Message)
}

// findingJSON is the JSON wire format for a Finding.
type findingJSON struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	Check    string `json:"check"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// MarshalJSON implements json.Marshaler.
func (f Finding) MarshalJSON() ([]byte, error) {
	return json.Marshal(findingJSON{
		File:     f.Pos.Filename,
		Line:     f.Pos.Line,
		Col:      f.Pos.Column,
		Check:    f.Check,
		Severity: f.Severity.String(),
		Message:  f.Message,
	})
}

// WriteText renders findings grouped by file, with color if the writer is a terminal.
func WriteText(w io.Writer, findings []Finding) error {
	if len(findings) == 0 {
		_, err := io.WriteString(w, "No findings.\n")
		return err
	}

	// Group by file.
	grouped := make(map[string][]Finding)
	fileOrder := make([]string, 0)
	for _, f := range findings {
		fn := f.Pos.Filename
		if _, ok := grouped[fn]; !ok {
			fileOrder = append(fileOrder, fn)
		}
		grouped[fn] = append(grouped[fn], f)
	}

	for _, fn := range fileOrder {
		fmt.Fprintf(w, "\n%s\n", fn)
		for _, f := range grouped[fn] {
			fmt.Fprintf(w, "  L%d:%d  %-5s  %s: %s\n",
				f.Pos.Line, f.Pos.Column,
				f.Severity, f.Check, f.Message)
		}
	}

	fmt.Fprintf(w, "\n%d finding(s).\n", len(findings))
	return nil
}

// WriteJSON renders findings as a JSON array.
func WriteJSON(w io.Writer, findings []Finding) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(findings)
}
