// Package astcheck — runner.go loads Go packages with type information and
// executes registered analyzers, collecting findings. Supports baseline
// subtraction, diff filtering, and inline suppression.
package astcheck

import (
	"fmt"
	"go/ast"
	"os/exec"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/packages"
)

// Config controls which packages are loaded and which analyzers run.
type Config struct {
	// Patterns are go/packages patterns (e.g. "./...", "./internal/...").
	Patterns []string

	// MinSeverity filters out findings below this level.
	MinSeverity Severity

	// Enabled explicitly turns on analyzers that are default-off.
	Enabled []string

	// Disabled explicitly turns off analyzers that are default-on.
	Disabled []string

	// DiffFrom is a git revision (e.g. "origin/main"). When set, only
	// findings in files changed vs this revision are reported. All files
	// are still loaded for type correctness.
	DiffFrom string

	// BaselinePath is the path to the baseline file for subtraction.
	// Findings whose position-stable keys appear in the baseline are
	// excluded from the result.
	BaselinePath string

	// WriteBaseline, when true, writes current findings as a baseline
	// file to BaselinePath and returns them in Result.BaselineKeys.
	WriteBaseline bool
}

// Result holds the output of a Run.
type Result struct {
	Findings    []Finding
	Errors      []string
	BaselineKey map[string]bool // keys of all findings (for --write-baseline)
}

// Run loads packages and executes all enabled analyzers, returning findings
// filtered by MinSeverity, diff, baseline, and suppression.
func Run(cfg Config) (*Result, error) {
	if len(cfg.Patterns) == 0 {
		cfg.Patterns = []string{"./..."}
	}

	entries := Registry()
	enabled := selectAnalyzers(entries, cfg.Enabled, cfg.Disabled)
	if len(enabled) == 0 {
		return &Result{}, nil
	}

	pkgs, err := loadPackages(cfg.Patterns)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	// Load diff filter if requested.
	var changedFiles map[string]bool
	if cfg.DiffFrom != "" {
		changedFiles, err = ChangedFiles(cfg.DiffFrom)
		if err != nil {
			return nil, fmt.Errorf("computing diff from %s: %w", cfg.DiffFrom, err)
		}
		if len(changedFiles) == 0 {
			// No files changed — return early.
			return &Result{}, nil
		}
	}

	// Load baseline if requested.
	var baseline map[string]bool
	if cfg.BaselinePath != "" && !cfg.WriteBaseline {
		baseline, err = ReadBaseline(cfg.BaselinePath)
		if err != nil {
			return nil, err
		}
	}

	var findings []Finding
	var errs []string

	for _, pkg := range pkgs {
		if pkg.IllTyped {
			errs = append(errs, fmt.Sprintf("package %s has type errors", pkg.PkgPath))
			continue
		}

		// Build suppression map from all files in this package.
		suppressed := make(map[int][]string)
		for _, file := range pkg.Syntax {
			for line, checks := range computeSuppressedLines(pkg.Fset, file) {
				suppressed[line] = append(suppressed[line], checks...)
			}
		}

		for _, entry := range enabled {
			finds, err := runAnalyzer(entry, pkg)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s on %s: %v", entry.Analyzer.Name, pkg.PkgPath, err))
				continue
			}
			for _, f := range finds {
				// Apply severity filter.
				if f.Severity < cfg.MinSeverity {
					continue
				}
				// Apply suppression.
				if isSuppressed(f.Pos.Line, f.Check, suppressed) {
					continue
				}
				// Apply diff filter.
				if changedFiles != nil && !changedFiles[f.Pos.Filename] {
					continue
				}
				findings = append(findings, f)
			}
		}
	}

	// Apply baseline subtraction (unless writing).
	if baseline != nil {
		findings = SubtractBaseline(findings, baseline)
	}

	// Compute baseline keys for all findings (useful for --write-baseline).
	keys := make(map[string]bool, len(findings))
	for _, f := range findings {
		keys[f.Key()] = true
	}

	return &Result{
		Findings:    findings,
		Errors:      errs,
		BaselineKey: keys,
	}, nil
}

// selectAnalyzers returns the analyzers that should run based on config.
func selectAnalyzers(entries []RegistryEntry, enabled, disabled []string) []RegistryEntry {
	var out []RegistryEntry
	for _, e := range entries {
		if IsEnabled(e, enabled, disabled) {
			out = append(out, e)
		}
	}
	return out
}

// loadPackages loads Go packages with type information and test files.
func loadPackages(patterns []string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedImports |
			packages.NeedTypes |
			packages.NeedTypesSizes |
			packages.NeedSyntax |
			packages.NeedTypesInfo |
			packages.NeedDeps |
			packages.NeedModule,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	return dedupTestPackages(pkgs), nil
}

// dedupTestPackages removes the external test package variant (pkg_test) when
// the internal variant (pkg) is also present, to avoid running analyzers twice.
func dedupTestPackages(pkgs []*packages.Package) []*packages.Package {
	seen := make(map[string]bool)
	var out []*packages.Package
	for _, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			continue
		}
		key := pkg.PkgPath
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, pkg)
	}
	return out
}

// runAnalyzer executes a single analyzer on a loaded package, converting
// diagnostics to Findings with computed enclosing symbols.
func runAnalyzer(entry RegistryEntry, pkg *packages.Package) ([]Finding, error) {
	deps := make(map[*analysis.Analyzer]interface{})
	if err := populateDeps(entry.Analyzer, deps); err != nil {
		return nil, err
	}

	var findings []Finding

	for _, file := range pkg.Syntax {
		pass := &analysis.Pass{
			Analyzer:   entry.Analyzer,
			Fset:       pkg.Fset,
			Files:      []*ast.File{file},
			TypesInfo:  pkg.TypesInfo,
			Pkg:        pkg.Types,
			TypesSizes: pkg.TypesSizes,
			ResultOf:   deps,
			Report: func(d analysis.Diagnostic) {
				pos := pkg.Fset.Position(d.Pos)
				symbol := EnclosingSymbol(pkg.Fset, file, d.Pos)
				findings = append(findings, Finding{
					Pos:      pos,
					Check:    entry.Analyzer.Name,
					Severity: entry.Severity,
					Symbol:   symbol,
					Message:  d.Message,
				})
			},
		}
		_, err := entry.Analyzer.Run(pass)
		if err != nil {
			return nil, err
		}
	}

	return findings, nil
}

// populateDeps runs an analyzer's Required dependencies and populates the
// ResultOf map.
func populateDeps(a *analysis.Analyzer, deps map[*analysis.Analyzer]interface{}) error {
	for _, req := range a.Requires {
		if req == inspect.Analyzer {
			deps[req] = nil
		}
		if err := populateDeps(req, deps); err != nil {
			return err
		}
	}
	return nil
}

// DiffFromChangedFiles returns the set of file paths changed between the
// working tree and the given git revision. Used by the diff filter.
// Exported as ChangedFiles for use by the runner and tests.
func ChangedFiles(rev string) (map[string]bool, error) {
	return changedFilesGit(rev)
}

// changedFilesGit shells out to git diff to get changed files.
func changedFilesGit(rev string) (map[string]bool, error) {
	// --name-only: just file paths
	// --diff-filter=ACMR: only added/copied/modified/renamed (not deleted)
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=ACMR", rev, "--")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff %s: %s", rev, stderr.String())
	}

	files := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			// Normalize to forward slashes for consistent comparison.
			files[line] = true
		}
	}
	return files, nil
}
