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
	"path/filepath"
	"strings"
	"go/token"

	"dreamer/internal/astcheck"
	"dreamer/internal/webcheck"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "quality: %v\n", err)
		os.Exit(2)
	}
}

func run() error {
	cfg, jsonOut, templatesDir, cssDir, err := parseFlags()
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

	// Run webcheck on HTML templates and CSS files.
	webFindings, err := webcheck.Check(webcheck.Config{
		TemplatesDir: templatesDir,
		CSSDir:       cssDir,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "quality: webcheck: %v\n", err)
	} else {
		result.Findings = append(result.Findings, convertWebFindings(webFindings, cfg.MinSeverity)...)
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
func parseFlags() (*astcheck.Config, bool, string, string, error) {
	var (
		diffFrom    string
		baseline    string
		writeBase   bool
		minSev      string
		enableList  string
		disableList string
		jsonOut     bool
		templatesDir string
		cssDir       string
	)

	flag.StringVar(&diffFrom, "diff-from", "", "report only findings in files changed vs this rev")
	flag.StringVar(&baseline, "baseline", ".quality-baseline.json", "baseline file to subtract")
	flag.BoolVar(&writeBase, "write-baseline", false, "write current findings as baseline and exit")
	flag.StringVar(&minSev, "min-severity", "warn", "minimum severity to report (error, warn, info)")
	flag.StringVar(&enableList, "enable", "", "comma-separated list of analyzers to enable")
	flag.StringVar(&disableList, "disable", "", "comma-separated list of analyzers to disable")
	flag.BoolVar(&jsonOut, "json", false, "output findings as JSON")
	flag.StringVar(&templatesDir, "templates-dir", "internal/web/templates", "directory containing HTML templates for webcheck")
	flag.StringVar(&cssDir, "css-dir", "internal/web/static/css", "directory containing CSS files for webcheck")
	flag.Parse()

	severity, ok := astcheck.ParseSeverity(minSev)
	if !ok {
		return nil, false, "", "", fmt.Errorf("invalid --min-severity: %q (use error, warn, or info)", minSev)
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

	// Resolve webcheck directories relative to current directory.
	if templatesDir != "" {
		if abs, err := filepath.Abs(templatesDir); err == nil {
			templatesDir = abs
		}
	}
	if cssDir != "" {
		if abs, err := filepath.Abs(cssDir); err == nil {
			cssDir = abs
		}
	}

	return &astcheck.Config{
		Patterns:      patterns,
		MinSeverity:   severity,
		Enabled:       enabled,
		Disabled:      disabled,
		DiffFrom:      diffFrom,
		BaselinePath:  baseline,
		WriteBaseline: writeBase,
	}, jsonOut, templatesDir, cssDir, nil
}

// convertWebFindings converts webcheck findings to astcheck findings for
// unified reporting. Filters by minimum severity.
func convertWebFindings(webFindings []webcheck.Finding, minSev astcheck.Severity) []astcheck.Finding {
	var out []astcheck.Finding
	for _, wf := range webFindings {
		sev, ok := astcheck.ParseSeverity(string(wf.Severity))
		if !ok {
			sev = astcheck.SevWarn
		}
		if sev > minSev {
			continue
		}
		out = append(out, astcheck.Finding{
			Pos: token.Position{
				Filename: wf.File,
				Line:     wf.Line,
				Column:   wf.Col,
			},
			Check:    wf.Check,
			Severity: sev,
			Message:  wf.Message,
		})
	}
	return out
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
