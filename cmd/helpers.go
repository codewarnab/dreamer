package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/config"
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
