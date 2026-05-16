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

// RunConfig tells the orchestrator how to dispatch per-chunk phase-1 calls.
// SessionFactory must be safe to call concurrently when Mode == ModeParallel.
type RunConfig struct {
	SessionFactory func() (Session, error)
	Mode           ExecutionMode
	MaxConcurrency int
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
