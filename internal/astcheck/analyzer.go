// Package astcheck implements dreamer-specific, type-aware code quality analyzers.
//
// Each analyzer is an [*go/analysis.Analyzer] registered via [Register] with a
// severity level and default-on/off flag. The registry is consumed by the runner
// (in runner.go) which loads packages with type information and executes the
// analyzers, and by the report layer (in report.go) which renders findings.
//
// All analyzers in this package are intra-package only — no cross-package
// [analysis.Fact] propagation. Whole-program checks (e.g. truly-unused exports)
// are delegated to external tools like `deadcode`.
//
// Registered analyzers (suppress any with //astcheck:ignore[<name>]):
//
//	nodirectlog      error  direct stdlib log use; use logging.Logger          (default on)
//	settesthome      error  setTestHome env-var hygiene                         (default on)
//	theatricaltest   warn   tests that don't assert their advertised behavior   (default on)
//	atomicwrite      warn   os.WriteFile; use fsutil.WriteFileAtomic            (default on)
//	discardederr     warn   _-discard of a curated callee's error return        (default on)
//	unboundedread    warn   io.ReadAll on stdin/request body without a cap      (default on)
//	overlaybool      warn   overlay-merged bool field; use *bool                (default on)
//	lockorder        error  read before fsutil.AcquireLock; TOCTOU race        (default on)
//	lockskip         error  exported method on mutex struct skips lock          (default on)
//	permissionbypass error  sandbox write-posture without Available() check     (default on)
//	stringconst      warn   repeated string literals across files; use constants(default on)
//	stringerr        warn   strings.Contains(err.Error(), literal); use errors.Is/As(default on)
//	errverbatim      warn   err.Error() as HTTP response body; leaks paths       (default on)
//	symlinkresolve   warn   filepath.Clean in containment check without EvalSymlinks(default on)
//	implicitstatus   info   w.Write without preceding w.WriteHeader              (default on)
//	goroutinerecover info   pipeline goroutine without a recover() guard        (default OFF)
package astcheck

import (
	"sync"

	"golang.org/x/tools/go/analysis"
)

// Severity classifies a finding's importance.
type Severity int

const (
	SevError Severity = iota
	SevWarn
	SevInfo
)

func (s Severity) String() string {
	switch s {
	case SevError:
		return "error"
	case SevWarn:
		return "warn"
	case SevInfo:
		return "info"
	default:
		return "unknown"
	}
}

// ParseSeverity converts a string to a Severity.
func ParseSeverity(s string) (Severity, bool) {
	switch s {
	case "error":
		return SevError, true
	case "warn":
		return SevWarn, true
	case "info":
		return SevInfo, true
	default:
		return 0, false
	}
}

// RegistryEntry pairs an analyzer with its severity and default toggle.
type RegistryEntry struct {
	Analyzer  *analysis.Analyzer
	Severity  Severity
	DefaultOn bool
}

var (
	registryMu sync.Mutex
	registry   []RegistryEntry
)

// Register adds an analyzer to the global registry. Called from init() in each
// analyzer source file. Panics if called concurrently with Run.
func Register(entry RegistryEntry) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, entry)
}

// Registry returns a copy of the current registry entries.
func Registry() []RegistryEntry {
	registryMu.Lock()
	defer registryMu.Unlock()
	out := make([]RegistryEntry, len(registry))
	copy(out, registry)
	return out
}

// IsEnabled reports whether an analyzer should run based on the enabled/disabled
// lists. Explicit enable overrides default-off; explicit disable overrides default-on.
func IsEnabled(entry RegistryEntry, enabled, disabled []string) bool {
	name := entry.Analyzer.Name
	for _, d := range disabled {
		if d == name {
			return false
		}
	}
	for _, e := range enabled {
		if e == name {
			return true
		}
	}
	return entry.DefaultOn
}
