package chat

import (
	"dreamer/internal/chat/readers"
)

// ChatSourceProvider knows how to discover and read one kind of chat source.
// Implementations register themselves at package init time.
type ChatSourceProvider interface {
	Type() SourceType
	Discover(env DiscoveryEnvironment, projectPath string) ([]ChatSource, error)
	ReadMessages(source ChatSource) ([]readers.ChatMessage, error)
	// DeleteSource removes the chat source from local storage. For
	// file-per-chat tools this unlinks the file; for SQLite-backed tools it
	// deletes only the matching session/conversation row.
	DeleteSource(source ChatSource) error
	// SizeBytes returns an approximate on-disk footprint for the source.
	// Errors should be surfaced; the caller decides whether to swallow them.
	SizeBytes(source ChatSource) (int64, error)
}

// BatchSizer is an optional provider extension for providers whose underlying
// storage benefits from batching size lookups (typically SQLite-backed: one
// DB-open serves many sessions). Callers should type-assert and prefer the
// batch path when available.
type BatchSizer interface {
	SizeBytesBatch(sources []ChatSource) map[string]int64
}

// fileBackedProvider is a mixin for providers whose chat sources are
// plain files on disk. It provides Type(), DeleteSource(), and SizeBytes()
// so each concrete provider only needs Discover() and ReadMessages().
type fileBackedProvider struct {
	sourceType SourceType
}

func (p fileBackedProvider) Type() SourceType                    { return p.sourceType }
func (fileBackedProvider) DeleteSource(s ChatSource) error       { return deleteSourceFile(s.Path) }
func (fileBackedProvider) SizeBytes(s ChatSource) (int64, error) { return statSourceSize(s.Path) }

var registeredProviders []ChatSourceProvider

// registerProvider is called from each source file's init() to install a
// provider into the discovery registry.
func registerProvider(provider ChatSourceProvider) {
	registeredProviders = append(registeredProviders, provider)
}

// Providers returns the registered chat source providers in registration
// order. The slice must not be mutated by callers.
func Providers() []ChatSourceProvider {
	return registeredProviders
}

// ProviderFor returns the registered provider for the given source type.
func ProviderFor(sourceType SourceType) (ChatSourceProvider, bool) {
	for _, provider := range registeredProviders {
		if provider.Type() == sourceType {
			return provider, true
		}
	}
	return nil, false
}
