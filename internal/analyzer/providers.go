package analyzer

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ErrRateLimited is the sentinel for provider rate-limit / quota errors.
// Providers MUST wrap their underlying error with errors.Join(ErrRateLimited, err)
// so the orchestrator can abort the whole run rather than continuing rule-by-rule.
var ErrRateLimited = errors.New("provider rate limited")

// ProviderID identifies a known provider implementation. Values match the
// strings used in the configuration system (§3, §4.1 of doc/spec.md).
type ProviderID string

const (
	ProviderCopilotSDK ProviderID = "copilot-sdk"
	ProviderCopilotACP ProviderID = "copilot-acp"
	ProviderClaudeCLI  ProviderID = "claude-cli"
	ProviderClaudeACP  ProviderID = "claude-acp"
	ProviderGeminiSDK  ProviderID = "gemini-sdk"
	ProviderGeminiCLI  ProviderID = "gemini-cli"
	ProviderGeminiACP  ProviderID = "gemini-acp"
	ProviderKiroACP    ProviderID = "kiro-acp"
	ProviderCodexCLI   ProviderID = "codex-cli"
	ProviderCodexACP   ProviderID = "codex-acp"
)

// ProviderConfig is the per-provider configuration block resolved by the
// caller (CLI/config). Unused fields per provider are ignored.
type ProviderConfig struct {
	// Common
	Model string

	// copilot-sdk
	CopilotHome     string
	UseLoggedInUser bool
	AutoStart       bool
	CLIURL          string

	// CLI/ACP providers
	Command []string
	Env     map[string]string

	// API providers
	APIKeyEnv string

	// Token budget override (optional, 0 = use provider default)
	MaxInputTokens int
}

// ProviderFactory builds a Provider instance.
type ProviderFactory func(cfg ProviderConfig) (Provider, error)

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
func NewProvider(id ProviderID, cfg ProviderConfig) (Provider, error) {
	factory, ok := LookupProvider(id)
	if !ok {
		return nil, fmt.Errorf("provider %q is not registered (known: %s)", id, joinProviderIDs(RegisteredProviders(), ", "))
	}
	return factory(cfg)
}

func joinProviderIDs(ids []ProviderID, sep string) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, string(id))
	}
	return strings.Join(parts, sep)
}
