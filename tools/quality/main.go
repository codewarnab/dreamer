// Command quality runs dreamer-specific, type-aware code quality analyzers.
//
// This is a standalone dev/CI tool — NOT a dreamer subcommand — so that
// golang.org/x/tools is never linked into the shipped dreamer binary.
//
// Usage:
//
//	go run ./tools/quality [packages]
//	go run ./tools/quality --diff-from origin/main
//	go run ./tools/quality --json
//	go run ./tools/quality --min-severity error
//	go run ./tools/quality --enable theatricaltest
//	go run ./tools/quality --write-baseline
//	go run ./tools/quality --baseline .quality-baseline.json
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"dreamer/internal/astcheck"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "quality: %v\n", err)
		os.Exit(2)
	}
}

func run() error {
	cfg, jsonOut, err := parseFlags()
	if err != nil {
		return err
	}
	warnUnknownAnalyzers(cfg.Enabled, cfg.Disabled)

	result, err := astcheck.Run(*cfg)
	if err != nil {
		return err
	}
	for _, e := range result.Errors {
		fmt.Fprintf(os.Stderr, "quality: %s\n", e)
	}
	if cfg.WriteBaseline {
		if err := astcheck.WriteBaseline(cfg.BaselinePath, result.Findings); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "quality: wrote baseline to %s (%d findings)\n", cfg.BaselinePath, len(result.Findings))
		return nil
	}
	if jsonOut {
		return astcheck.WriteJSON(os.Stdout, result.Findings)
	}
	astcheck.WriteText(os.Stderr, result.Findings)
	return nil
}

// parseFlags processes CLI flags and returns a Config. Extracted from main
// to stay under the funlen linter limit.
func parseFlags() (*astcheck.Config, bool, error) {
	var (
		diffFrom    string
		baseline    string
		writeBase   bool
		minSev      string
		enableList  string
		disableList string
		jsonOut     bool
	)

	flag.StringVar(&diffFrom, "diff-from", "", "report only findings in files changed vs this rev")
	flag.StringVar(&baseline, "baseline", ".quality-baseline.json", "baseline file to subtract")
	flag.BoolVar(&writeBase, "write-baseline", false, "write current findings as baseline and exit")
	flag.StringVar(&minSev, "min-severity", "warn", "minimum severity to report (error, warn, info)")
	flag.StringVar(&enableList, "enable", "", "comma-separated list of analyzers to enable")
	flag.StringVar(&disableList, "disable", "", "comma-separated list of analyzers to disable")
	flag.BoolVar(&jsonOut, "json", false, "output findings as JSON")
	flag.Parse()

	severity, ok := astcheck.ParseSeverity(minSev)
	if !ok {
		return nil, false, fmt.Errorf("invalid --min-severity: %q (use error, warn, or info)", minSev)
	}

	var enabled, disabled []string
	if enableList != "" {
		enabled = strings.Split(enableList, ",")
	}
	if disableList != "" {
		disabled = strings.Split(disableList, ",")
	}

	patterns := flag.Args()
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}

	return &astcheck.Config{
		Patterns:      patterns,
		MinSeverity:   severity,
		Enabled:       enabled,
		Disabled:      disabled,
		DiffFrom:      diffFrom,
		BaselinePath:  baseline,
		WriteBaseline: writeBase,
	}, jsonOut, nil
}

// warnUnknownAnalyzers prints a warning for any --enable/--disable names
// that don't match a registered analyzer.
func warnUnknownAnalyzers(enabled, disabled []string) {
	known := make(map[string]bool)
	for _, e := range astcheck.Registry() {
		known[e.Analyzer.Name] = true
	}
	for _, name := range append(enabled, disabled...) {
		if !known[name] {
			fmt.Fprintf(os.Stderr, "quality: warning: unknown analyzer %q\n", name)
		}
	}
}
