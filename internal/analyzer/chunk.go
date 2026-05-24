package analyzer

// Chunk is one phase-1 prompt's transcript payload. Sources in SourceLabels
// appear in Transcript in the same order. Split is true when this chunk
// holds part of a single provider that was hard-split for size.
type Chunk struct {
	Index        int
	SourceLabels []string
	Transcript   string
	Bytes        int
	Split        bool
}

// ExecutionMode picks the per-chunk dispatch strategy in RunConfig.
type ExecutionMode int

const (
	ModeSequential ExecutionMode = iota
	ModeParallel
)

// String returns the canonical string form ("sequential" / "parallel").
func (m ExecutionMode) String() string {
	switch m {
	case ModeParallel:
		return "parallel"
	default:
		return "sequential"
	}
}

// SessionFactory builds a fresh provider Session. Must be safe for
// concurrent calls when Mode == ModeParallel.
type SessionFactory func() (Session, error)

// RunConfig tells the orchestrator how to dispatch per-chunk phase-1 calls
// and the single phase-2 call.
//
// Phase 1 and Phase 2 use separate factories so a Phase 1 session can be
// built without MCP / CLI tool wiring. This prevents a misbehaving Phase 1
// model from calling the Phase 2 record_finding tool and polluting the
// findings file (defense in depth — the Phase 1 prompt doesn't reference
// the tool, but it might be reachable through prompt injection).
//
// Phase2SessionFactory falls back to Phase1SessionFactory when nil, so
// existing callers that don't yet split factories keep working.
type RunConfig struct {
	Phase1SessionFactory SessionFactory
	Phase2SessionFactory SessionFactory
	Mode                 ExecutionMode
	MaxConcurrency       int
}

// Phase1Factory returns the Phase 1 factory, panicking if unset — callers
// must always provide it.
func (rc RunConfig) Phase1Factory() SessionFactory { return rc.Phase1SessionFactory }

// Phase2Factory returns the Phase 2 factory, falling back to Phase 1 if
// the caller didn't split them.
func (rc RunConfig) Phase2Factory() SessionFactory {
	if rc.Phase2SessionFactory != nil {
		return rc.Phase2SessionFactory
	}
	return rc.Phase1SessionFactory
}

// ParallelCapable is implemented by providers verified to produce correct
// output when multiple sessions are opened concurrently. Providers that
// do not implement it (or return false) are treated as sequential-only.
type ParallelCapable interface {
	SupportsParallelSessions() bool
}

// ProviderSupportsParallel reports whether p signals parallel-session safety.
func ProviderSupportsParallel(p Provider) bool {
	pc, ok := p.(ParallelCapable)
	return ok && pc.SupportsParallelSessions()
}
