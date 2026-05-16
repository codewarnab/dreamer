package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/config"
	"dreamer/internal/logging"
)

const defaultConfigFileName = "config.yaml"

func resolveConfigPath(configPath string) (string, error) {
	if strings.TrimSpace(configPath) == "" {
		path, err := config.GlobalConfigPath()
		if err != nil {
			return "", fmt.Errorf("resolve global config path: %w", err)
		}
		return path, nil
	}

	expandedPath, err := expandHomePath(configPath)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expandedPath) {
		absolutePath, err := filepath.Abs(expandedPath)
		if err != nil {
			return "", fmt.Errorf("resolve absolute config path %q: %w", configPath, err)
		}
		return absolutePath, nil
	}

	return filepath.Clean(expandedPath), nil
}

// logDefaultedSinceNotices emits one info line per project whose `since` was
// filled with the v1.2 default. No-op when the list is empty.
func logDefaultedSinceNotices(logger *logging.Logger, cfg *config.Config) {
	if logger == nil || cfg == nil {
		return
	}
	for _, name := range cfg.Notices.DefaultedSince {
		logger.Info("since defaulted",
			logging.Any("project", name),
			logging.Any("since", config.DefaultSince),
			logging.Any("hint", "set `since: lifetime` to restore prior behavior"),
		)
	}
}

func expandHomePath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		if path == "~" {
			return homeDir, nil
		}
		return filepath.Join(homeDir, path[2:]), nil
	}
	return path, nil
}
