package webcheck

import "fmt"

// Severity mirrors astcheck severity levels.
type Severity string

const (
	SevError Severity = "error"
	SevWarn  Severity = "warn"
	SevInfo  Severity = "info"
)

// Finding is a single lint violation found by a webcheck scanner.
type Finding struct {
	File     string
	Line     int
	Col      int
	Check    string
	Severity Severity
	Message  string
}

// String returns a human-readable representation of the finding.
func (f Finding) String() string {
	return fmt.Sprintf("%s:%d:%d: [%s] %s: %s", f.File, f.Line, f.Col, f.Severity, f.Check, f.Message)
}
