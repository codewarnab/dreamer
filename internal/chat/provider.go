package chat

import (
	"dreamer/internal/chat/readers"
)

// SourceProvider knows how to discover and read one kind of chat source.
// Implementations register themselves at package init time.
type SourceProvider interface {
	Type() SourceType
	Discover(env DiscoveryEnvironment, projectPath string) ([]Source, error)
	ReadMessages(source Source) ([]readers.ChatMessage, error)
	// DeleteSource removes the chat source from local storage. For
	// file-per-chat tools this unlinks the file; for SQLite-backed tools it
	// deletes only the matching session/conversation row.
	DeleteSource(source Source) error
	// SizeBytes returns an approximate on-disk footprint for the source.
	// Errors should be surfaced; the caller decides whether to swallow them.
	SizeBytes(source Source) (int64, error)
}

type ReadOptions struct {
	Budget readers.ReadBudget
}

type ReadResult struct {
	Messages  []readers.ChatMessage
	Truncated bool
	Bytes     int
}

// BudgetedReader is an optional extension for source readers that can enforce
// a per-source byte cap while streaming. The pipeline uses it when available
// because chunking happens after decoded messages are already resident in RAM.
type BudgetedReader interface {
	ReadMessagesWithOptions(source Source, options ReadOptions) (ReadResult, error)
}

// BatchSizer is an optional provider extension for providers whose underlying
// storage benefits from batching size lookups (typically SQLite-backed: one
// DB-open serves many sessions). Callers should type-assert and prefer the
// batch path when available.
type BatchSizer interface {
	SizeBytesBatch(sources []Source) map[string]int64
}

// SourceHasher is an optional provider extension for computing the per-source
// content digest that feeds the incremental cache key. SQLite-backed providers
// implement it because their Source.Path encodes `<dbFile>#<sessionID>`:
// hashing that literal path as a file always fails and would key every session
// on the whole shared database. Providers without it fall back to hashing the
// source path as a plain file.
type SourceHasher interface {
	SourceHash(source Source) (string, error)
}

// fileBackedProvider is a mixin for providers whose chat sources are
// plain files on disk. It provides Type(), DeleteSource(), and SizeBytes()
// so each concrete provider only needs Discover() and ReadMessages().
type fileBackedProvider struct {
	sourceType SourceType
}

func (p fileBackedProvider) Type() SourceType                { return p.sourceType }
func (fileBackedProvider) DeleteSource(s Source) error       { return deleteSourceFile(s.Path) }
func (fileBackedProvider) SizeBytes(s Source) (int64, error) { return statSourceSize(s.Path) }

var registeredProviders []SourceProvider

// registerProvider is called from each source file's init() to install a
// provider into the discovery registry.
func registerProvider(provider SourceProvider) {
	registeredProviders = append(registeredProviders, provider)
}

// Providers returns the registered chat source providers in registration
// order. The slice must not be mutated by callers.
func Providers() []SourceProvider {
	return registeredProviders
}

// ProviderFor returns the registered provider for the given source type.
func ProviderFor(sourceType SourceType) (SourceProvider, bool) {
	for _, provider := range registeredProviders {
		if provider.Type() == sourceType {
			return provider, true
		}
	}
	return nil, false
}
