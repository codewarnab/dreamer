package analyzer

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"dreamer/internal/config"
)

// ErrRateLimited is the legacy sentinel for provider rate-limit / quota errors.
//
// Deprecated: use errs.RateLimit(...) to produce and errs.Is(err, errs.KindRateLimit)
// to consume rate-limit errors. This sentinel is kept temporarily for external
// importers; it will be removed in a future release.
var ErrRateLimited = errors.New("provider rate limited")

// ErrUnavailable: provider can no longer serve the current pipeline.Run.
// Providers join via errors.Join(analyzer.ErrUnavailable, cause).
var ErrUnavailable = errors.New("provider unavailable")

// ProviderID is a type alias for config.ProviderID — the canonical definition
// lives in the config package so it can be used in config maps without import
// cycles. This alias preserves backward compatibility for provider packages
// that reference analyzer.ProviderID and analyzer.ProviderXxx constants.
type ProviderID = config.ProviderID

// Re-exported from config so existing provider packages compile unchanged.
const (
	ProviderCopilotSDK     = config.ProviderCopilotSDK
	ProviderCopilotACP     = config.ProviderCopilotACP
	ProviderClaudeCLI      = config.ProviderClaudeCLI
	ProviderClaudeACP      = config.ProviderClaudeACP
	ProviderGeminiCLI      = config.ProviderGeminiCLI
	ProviderGeminiACP      = config.ProviderGeminiACP
	ProviderKiroACP        = config.ProviderKiroACP
	ProviderCodexCLI       = config.ProviderCodexCLI
	ProviderCodexACP       = config.ProviderCodexACP
	ProviderOpenClaudeCLI  = config.ProviderOpenClaudeCLI
	ProviderOpenCodeACP    = config.ProviderOpenCodeACP
	ProviderOpenCodeServer = config.ProviderOpenCodeServer
	ProviderCodebuffSDK    = config.ProviderCodebuffSDK
)

// ProviderConfig is the per-provider configuration block resolved by the
// caller (CLI/config). Unused fields per provider are ignored.
type ProviderConfig struct {
	// Common
	Model        string
	DefaultModel string // per-provider default; applied when Model is empty

	// copilot-sdk
	CopilotHome        string
	UseLoggedInUser    bool
	UseLoggedInUserSet bool
	AutoStart          bool
	CLIURL             string

	// CLI/ACP providers
	Command []string
	Env     map[string]string

	// API providers
	APIKeyEnv string

	// HTTP server providers
	BaseURL  string
	Password string

	// Token budget override (optional, 0 = use provider default)
	MaxInputTokens int

	// Sandbox is the raw "sandbox" config value ("auto", "true", "false",
	// or empty for default). Parsed by the sandbox package.
	Sandbox string
}

// ProviderFactory builds a Provider instance.
type ProviderFactory func(providerConfig ProviderConfig) (Provider, error)

var (
	providerRegistryMutex sync.RWMutex
	providerRegistry      = map[ProviderID]ProviderFactory{}
)

// RegisterProvider installs a factory for the given provider id. Provider
// implementation packages call this from init().
func RegisterProvider(id ProviderID, factory ProviderFactory) {
	providerRegistryMutex.Lock()
	defer providerRegistryMutex.Unlock()
	providerRegistry[id] = factory
}

// LookupProvider returns the factory for id, or false if none is registered.
func LookupProvider(id ProviderID) (ProviderFactory, bool) {
	providerRegistryMutex.RLock()
	defer providerRegistryMutex.RUnlock()
	factory, ok := providerRegistry[id]
	return factory, ok
}

// RegisteredProviders returns the sorted list of registered provider ids.
func RegisteredProviders() []ProviderID {
	providerRegistryMutex.RLock()
	defer providerRegistryMutex.RUnlock()
	ids := make([]ProviderID, 0, len(providerRegistry))
	for id := range providerRegistry {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
	return ids
}

// NewProvider builds a Provider for the given id using the registered factory.
func NewProvider(id ProviderID, providerConfig ProviderConfig) (Provider, error) {
	factory, ok := LookupProvider(id)
	if !ok {
		return nil, fmt.Errorf("provider %q is not registered (known: %s)", id, joinProviderIDs(RegisteredProviders(), ", "))
	}
	return factory(providerConfig)
}

// ProviderMeta carries display metadata for a registered provider.
// Providers self-register via RegisterProviderMeta in init().
type ProviderMeta struct {
	ID          ProviderID
	DisplayName string // e.g. "OpenClaude CLI (recommended)"
	Order       int    // sort order in UI (lower = higher)
	// Phase2Mode declares the tool-based Phase 2 strategy for this provider.
	Phase2Mode Phase2Mode
}

var (
	providerMetaRegistryMutex sync.RWMutex
	providerMetaRegistry      = map[ProviderID]ProviderMeta{}
	providerMetaOrder         []ProviderID // insertion order, used for stable iteration
)

// RegisterProviderMeta installs display metadata for a provider. Provider
// implementation packages call this from init() alongside RegisterProvider.
// Idempotent — re-registration overwrites the prior entry.
func RegisterProviderMeta(meta ProviderMeta) {
	providerMetaRegistryMutex.Lock()
	defer providerMetaRegistryMutex.Unlock()
	if _, exists := providerMetaRegistry[meta.ID]; !exists {
		providerMetaOrder = append(providerMetaOrder, meta.ID)
	}
	providerMetaRegistry[meta.ID] = meta
}

// RegisteredProviderMeta returns all registered provider metadata sorted by
// Order (ascending). The slice must not be mutated by callers.
func RegisteredProviderMeta() []ProviderMeta {
	providerMetaRegistryMutex.RLock()
	defer providerMetaRegistryMutex.RUnlock()
	out := make([]ProviderMeta, 0, len(providerMetaRegistry))
	for _, id := range providerMetaOrder {
		out = append(out, providerMetaRegistry[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// LookupPhase2Mode returns the Phase2Mode for the given provider id.
// Returns Phase2ModeNone if the provider is not registered or has no
// tool-based Phase 2 mode.
func LookupPhase2Mode(id ProviderID) Phase2Mode {
	providerMetaRegistryMutex.RLock()
	defer providerMetaRegistryMutex.RUnlock()
	if meta, ok := providerMetaRegistry[id]; ok {
		return meta.Phase2Mode
	}
	return Phase2ModeNone
}

func joinProviderIDs(ids []ProviderID, sep string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, string(id))
	}
	return strings.Join(parts, sep)
}
