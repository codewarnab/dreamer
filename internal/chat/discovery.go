package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

// DiscoverChats enumerates every supported chat source whose evidence places
// it inside projectPath. The result is sorted by ModifiedTime (newest first),
// tie-breaking on Path.
func DiscoverChats(projectPath string) ([]ChatSource, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home: %w", err)
	}

	appDataDir := strings.TrimSpace(os.Getenv("APPDATA"))
	if appDataDir == "" {
		appDataDir = filepath.Join(homeDir, "AppData", "Roaming")
	}

	dataHomeDir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dataHomeDir == "" {
		dataHomeDir = filepath.Join(homeDir, ".local", "share")
	}

	environment := DiscoveryEnvironment{
		HomeDir:         homeDir,
		AppDataDir:      appDataDir,
		DataHomeDir:     dataHomeDir,
		ClaudeConfigDir: strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")),
		GeminiHomeDir:   strings.TrimSpace(os.Getenv("GEMINI_HOME")),
		OpenCodeDBPath:    strings.TrimSpace(os.Getenv("OPENCODE_DB")),
		KiroCLIDBPath:     strings.TrimSpace(os.Getenv("KIRO_CLI_DB")),
		CodebuffConfigDir: strings.TrimSpace(os.Getenv("CODEBUFF_CONFIG_DIR")),
	}

	return discoverChatsFromEnvironment(environment, projectPath)
}

// discoverChatsFromEnvironment runs every registered provider's Discover hook
// in parallel and returns the merged, sorted slice of ChatSources. First
// non-nil error from any provider wins.
func discoverChatsFromEnvironment(environment DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	providers := Providers()
	results := make([][]ChatSource, len(providers))

	var (
		group errgroup.Group
		mu    sync.Mutex
	)
	for index, provider := range providers {
		index, provider := index, provider
		group.Go(func() error {
			sources, err := provider.Discover(environment, projectPath)
			if err != nil {
				return err
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

	combined := make([]ChatSource, 0)
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
