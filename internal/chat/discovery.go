package chat

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

// DiscoverChats enumerates every supported chat source whose evidence places
// it inside projectPath. The result is sorted by ModifiedTime (newest first),
// tie-breaking on Path. Uses environment variables for provider paths.
func DiscoverChats(projectPath string) ([]Source, error) {
	env, err := DefaultDiscoveryEnvironment()
	if err != nil {
		return nil, err
	}
	return discoverChatsFromEnvironment(env, projectPath)
}

// DiscoverChatsWithEnvironment is like DiscoverChats but accepts a
// pre-built DiscoveryEnvironment. Callers that have config access should
// use this to pass provider-specific paths (e.g. CopilotHome for isolation).
func DiscoverChatsWithEnvironment(env DiscoveryEnvironment, projectPath string) ([]Source, error) {
	return discoverChatsFromEnvironment(env, projectPath)
}

// DefaultDiscoveryEnvironment builds a DiscoveryEnvironment from
// environment variables and OS defaults. Callers can override specific
// fields (e.g. CopilotHome) before calling DiscoverChatsWithEnvironment.
func DefaultDiscoveryEnvironment() (DiscoveryEnvironment, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return DiscoveryEnvironment{}, fmt.Errorf("resolve user home: %w", err)
	}

	appDataDir := strings.TrimSpace(os.Getenv("APPDATA"))
	if appDataDir == "" {
		appDataDir = filepath.Join(homeDir, "AppData", "Roaming")
	}

	dataHomeDir := ""
	// XDG_DATA_HOME is ignored on Windows for the same reason as
	// XDG_CONFIG_HOME in config.ConfigDirBase: Git Bash exports it in
	// .bashrc, making discovery results depend on the launching shell.
	if runtime.GOOS != "windows" {
		dataHomeDir = strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	}
	if dataHomeDir == "" {
		dataHomeDir = filepath.Join(homeDir, ".local", "share")
	}

	return DiscoveryEnvironment{
		HomeDir:           homeDir,
		AppDataDir:        appDataDir,
		DataHomeDir:       dataHomeDir,
		ClaudeConfigDir:   strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")),
		GeminiHomeDir:     strings.TrimSpace(os.Getenv("GEMINI_HOME")),
		OpenCodeDBPath:    strings.TrimSpace(os.Getenv("OPENCODE_DB")),
		KiroCLIDBPath:     strings.TrimSpace(os.Getenv("KIRO_CLI_DB")),
		CodebuffConfigDir: strings.TrimSpace(os.Getenv("CODEBUFF_CONFIG_DIR")),
	}, nil
}

// discoverChatsFromEnvironment runs every registered provider's Discover hook
// in parallel and returns the merged, sorted slice of Sources. Individual
// provider failures are warned and skipped so one corrupt store does not hide
// chats from every other provider. Discovery returns an error only when every
// registered provider fails.
func discoverChatsFromEnvironment(environment DiscoveryEnvironment, projectPath string) ([]Source, error) {
	providers := Providers()
	results := make([][]Source, len(providers))
	providerErrors := make([]error, len(providers))

	var (
		group errgroup.Group
		mu    sync.Mutex
	)
	for index, provider := range providers {
		index, provider := index, provider
		group.Go(func() error {
			sources, err := provider.Discover(environment, projectPath)
			if err != nil {
				mu.Lock()
				providerErrors[index] = fmt.Errorf("%s discovery: %w", provider.Type(), err)
				mu.Unlock()
				return nil
			}
			mu.Lock()
			results[index] = sources
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	successfulProviders := 0
	var failedProviderErrors []error
	for _, err := range providerErrors {
		if err == nil {
			successfulProviders++
			continue
		}
		failedProviderErrors = append(failedProviderErrors, err)
		slog.Warn("chat discovery provider failed", "err", err)
	}
	if len(providers) > 0 && successfulProviders == 0 {
		return nil, errors.Join(failedProviderErrors...)
	}

	// Preallocate combined slice to total sources count to avoid dynamic reallocation
	totalSources := 0
	for _, sources := range results {
		totalSources += len(sources)
	}
	combined := make([]Source, 0, totalSources)
	for _, sources := range results {
		combined = append(combined, sources...)
	}
	sort.Slice(combined, func(i int, j int) bool {
		left := combined[i]
		right := combined[j]
		if left.ModifiedTime.Equal(right.ModifiedTime) {
			return left.Path < right.Path
		}
		return left.ModifiedTime.After(right.ModifiedTime)
	})

	return combined, nil
}
