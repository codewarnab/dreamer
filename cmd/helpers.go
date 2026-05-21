package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"dreamer/internal/config"
	"dreamer/internal/logging"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	defaultConfigFileName = "config.yaml"

	// Flag names for analyzer execution overrides shared by analyze and daemon.
	flagParallel  = "parallel"
	flagJobs      = "jobs"
	flagChunkSize = "chunk-size"
)

// analyzerFlagVars holds the variables bound to the analyzer execution flags.
// Both analyze and daemon declare these locally and pass the address to
// registerAnalyzerFlags.
type analyzerFlagVars struct {
	parallel  bool
	jobs      int
	chunkSize int
}

// registerAnalyzerFlags adds the shared analyzer execution flags to fs.
func registerAnalyzerFlags(fs *pflag.FlagSet, v *analyzerFlagVars) {
	fs.BoolVarP(&v.parallel, flagParallel, "p", false, "Force analyzer.execution.mode=parallel (provider must implement ParallelCapable; fallback logged)")
	fs.IntVarP(&v.jobs, flagJobs, "j", 0, "Cap parallel session count. 0 = len(chunks). Ignored when sequential.")
	fs.IntVar(&v.chunkSize, flagChunkSize, 0, "Override analyzer.chunking.max_chunk_bytes. 0 disables chunking.")
}

func resolveConfigPath(configPath string) (string, error) {
	if strings.TrimSpace(configPath) == "" {
		path, err := config.GlobalConfigPath()
		if err != nil {
			return "", fmt.Errorf("resolve global config path: %w", err)
		}
		return path, nil
	}

	expandedPath, err := config.ExpandUserHome(configPath)
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

// printBox renders a Unicode box around the given lines.
func printBox(cmd *cobra.Command, lines []string) {
	maxLen := 0
	for _, l := range lines {
		if len(l) > maxLen {
			maxLen = len(l)
		}
	}
	w := maxLen + 4 // padding inside the box

	cmd.Printf("╔%s╗\n", strings.Repeat("═", w))
	for _, l := range lines {
		cmd.Printf("║  %-*s  ║\n", maxLen, l)
	}
	cmd.Printf("╚%s╝\n", strings.Repeat("═", w))
}
