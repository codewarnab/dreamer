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
//	go run ./tools/quality --enable theatricaltest --enable deferclose
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
		fmt.Fprintf(os.Stderr, "invalid --min-severity: %q (use error, warn, or info)\n", minSev)
		os.Exit(2)
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

	cfg := astcheck.Config{
		Patterns:      patterns,
		MinSeverity:   severity,
		Enabled:       enabled,
		Disabled:      disabled,
		DiffFrom:      diffFrom,
		BaselinePath:  baseline,
		WriteBaseline: writeBase,
	}

	result, err := astcheck.Run(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	for _, e := range result.Errors {
		fmt.Fprintf(os.Stderr, "warning: %s\n", e)
	}

	// --write-baseline: write findings as baseline and exit 0.
	if writeBase {
		if err := astcheck.WriteBaseline(baseline, result.Findings); err != nil {
			fmt.Fprintf(os.Stderr, "error writing baseline: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "wrote %d finding(s) to %s\n", len(result.Findings), baseline)
		os.Exit(0)
	}

	if jsonOut {
		if err := astcheck.WriteJSON(os.Stdout, result.Findings); err != nil {
			fmt.Fprintf(os.Stderr, "error writing JSON: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := astcheck.WriteText(os.Stdout, result.Findings); err != nil {
			fmt.Fprintf(os.Stderr, "error writing output: %v\n", err)
			os.Exit(1)
		}
	}

	// Exit 1 if any finding meets the minimum severity.
	if len(result.Findings) > 0 {
		os.Exit(1)
	}
}
