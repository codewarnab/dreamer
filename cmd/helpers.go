package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"

	"dreamer/internal/config"
	"dreamer/internal/logging"
)

// resolveSelfExecutable returns the absolute path of the running dreamer
// binary with symlinks resolved. Falls back to the un-resolved path when
// EvalSymlinks fails (e.g. the binary was deleted since launch).
func resolveSelfExecutable() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(execPath); resolveErr == nil {
		return resolved, nil
	}
	return execPath, nil
}

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

// loadConfigAndLogger loads config with overlay and creates the file logger.
// Shared by `daemon` and `web --serve` — both bootstrap the same config+logger
// pair before standing up their respective runtimes. Emits the v1.2 `since`
// default notices as a side effect so every entry point surfaces them.
//
// NOTE: Both daemon and serveWeb write to the same output root's dreamer.log file.
// While concurrent writes from separate processes could occasionally race during log
// rotation, this is accepted in v1.5 as standalone mode is transient and typically
// run ad-hoc for manual inspection.
func loadConfigAndLogger(configPath, overlayPath string) (*config.App, *logging.Logger, error) {
	cfg, err := config.LoadConfigWithOverlay(configPath, overlayPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load config %q: %w", configPath, err)
	}
	logger, err := logging.New(cfg.Daemon.OutputRoot, cfg.Logging.Level, cfg.Logging.MaxSizeMB)
	if err != nil {
		return nil, nil, err
	}
	logDefaultedSinceNotices(logger, cfg)
	return cfg, logger, nil
}

// logDefaultedSinceNotices emits one info line per project whose `since` was
// filled with the v1.2 default. No-op when the list is empty.
func logDefaultedSinceNotices(logger *logging.Logger, cfg *config.App) {
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

// printBox renders a Unicode box around the given lines to w.
func printBox(w io.Writer, lines []string) {
	maxLen := 0
	for _, line := range lines {
		if len(line) > maxLen {
			maxLen = len(line)
		}
	}
	boxWidth := maxLen + 4 // padding inside the box

	fmt.Fprintf(w, "╔%s╗\n", strings.Repeat("═", boxWidth))
	for _, line := range lines {
		fmt.Fprintf(w, "║  %-*s  ║\n", maxLen, line)
	}
	fmt.Fprintf(w, "╚%s╝\n", strings.Repeat("═", boxWidth))
}
