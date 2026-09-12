// Package astcheck — buildtagoverlap.go detects redeclarations that only
// collide on some GOOS/GOARCH combinations.
//
// Background: the standard analyzers in this package run via go/packages,
// which only loads the files selected for the *host* platform. A duplicate
// declaration split across files with overlapping //go:build constraints
// (issue #102: seccomp_types.go tagged `linux` vs seccomp_stub.go tagged
// `linux && !amd64`, both selected on linux/arm64) is invisible on the
// developer's machine and only breaks cross-compilation.
//
// This check walks the source tree directly, collects every package-level
// declaration, and uses go/build.MatchFile to test whether any two files
// declaring the same name can be selected together on some real
// GOOS/GOARCH/cgo combination. Filename-implied constraints
// (foo_linux_arm64.go) are honored because MatchFile evaluates those too.
//
// Wired into tools/quality alongside webcheck (not via Register, since the
// per-file analysis.Pass harness cannot see build-excluded files).
package astcheck

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// BuildTagOverlapCheck is the check name reported in Findings.
const BuildTagOverlapCheck = "buildtagoverlap"

// overlapGOOS and overlapGOARCH span the platforms probed for file
// co-inclusion. A full cross product is used deliberately: evaluating each
// file against concrete (GOOS, GOARCH) pairs enforces the real mutual
// exclusivity of GOOS tags (unlike a naive boolean SAT over tag names,
// which would claim `linux` and `windows` overlap). Pairs that do not
// exist in `go tool dist list` can only cause a conservative extra report,
// never a missed collision.
var overlapGOOS = []string{
	"aix", "android", "darwin", "dragonfly", "freebsd", "illumos",
	"ios", "js", "linux", "netbsd", "openbsd", "plan9", "solaris",
	"windows", "zos",
}

var overlapGOARCH = []string{
	"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64",
	"mips64le", "mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm",
}

// overlapSkipDirs are never walked: vendored code, VCS metadata, and
// analysistest fixtures (testdata/src/*/good.go + bad.go intentionally
// share a package and must not be treated as one build).
var overlapSkipDirs = map[string]bool{
	"vendor":       true,
	"testdata":     true,
	"node_modules": true,
	".git":         true,
	".hg":          true,
}

// declOccurrence is one package-level declaration of a name in a file.
type declOccurrence struct {
	kind       string // type, func, method, var, const
	name       string // bare name, or recv.Method for methods
	pos        token.Position
	constraint string // raw //go:build line, "" when always included
}

// CheckBuildTagOverlaps scans root for package-level names declared in two
// or more files whose build constraints can select both files in one build.
// Returns one Finding (severity error) per colliding declaration, positioned
// at the later file in sorted path order.
func CheckBuildTagOverlaps(root string) ([]Finding, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root %q: %w", root, err)
	}
	files, err := collectOverlapFiles(abs)
	if err != nil {
		return nil, err
	}

	type fileInfo struct {
		path  string
		dir   string
		base  string
		pkg   string
		decls []declOccurrence
	}
	infos := make([]fileInfo, 0, len(files))
	for _, path := range files {
		pkg, decls, err := parseOverlapDecls(path)
		if err != nil {
			// Unparsable files are left to the compiler/vet; skipping
			// keeps quality green on work-in-progress edits.
			continue
		}
		if len(decls) == 0 {
			continue
		}
		infos = append(infos, fileInfo{
			path:  path,
			dir:   filepath.Dir(path),
			base:  filepath.Base(path),
			pkg:   pkg,
			decls: decls,
		})
	}

	// Group occurrences by directory + package + declared name. The package
	// qualifier matters: foo.go (package foo) and foo_test.go
	// (package foo_test, external test package) share no scope.
	groups := make(map[string][]int) // key -> indexes into infos-parallel occurrence list
	type occurrence struct {
		infoIdx int
		decl    declOccurrence
	}
	var occurrences []occurrence
	for i := range infos {
		for _, decl := range infos[i].decls {
			key := infos[i].dir + "\x00" + infos[i].pkg + "\x00" + decl.name
			groups[key] = append(groups[key], len(occurrences))
			occurrences = append(occurrences, occurrence{infoIdx: i, decl: decl})
		}
	}

	included := make(map[string]map[string]bool, len(infos)) // path -> platform set
	includedFor := func(info fileInfo) map[string]bool {
		if set, ok := included[info.path]; ok {
			return set
		}
		set := matchPlatforms(info.dir, info.base)
		included[info.path] = set
		return set
	}

	var findings []Finding
	for _, idxs := range groups {
		if len(idxs) < 2 {
			continue
		}
		// Deterministic order: sort occurrences by file path.
		sort.Slice(idxs, func(a, b int) bool {
			return occurrences[idxs[a]].decl.pos.Filename < occurrences[idxs[b]].decl.pos.Filename
		})
		for j := 1; j < len(idxs); j++ {
			cur := occurrences[idxs[j]]
			curInfo := infos[cur.infoIdx]
			curSet := includedFor(curInfo)
			for _, prevIdx := range idxs[:j] {
				prev := occurrences[prevIdx]
				prevInfo := infos[prev.infoIdx]
				if prevInfo.path == curInfo.path {
					// Same-file duplicates are the compiler's job.
					continue
				}
				example, ok := firstCommonPlatform(curSet, includedFor(prevInfo))
				if !ok {
					continue
				}
				findings = append(findings, Finding{
					Pos:      cur.decl.pos,
					Check:    BuildTagOverlapCheck,
					Severity: SevError,
					Symbol:   shortDeclName(cur.decl.name),
					Message: fmt.Sprintf("%s %s redeclared in this package (also declared in %s [%s]); both files can be selected together (e.g. %s)",
						cur.decl.kind, shortDeclName(cur.decl.name),
						filepath.Base(prevInfo.path), describeConstraint(prev.decl.constraint),
						example),
				})
				break // one finding per colliding occurrence
			}
		}
	}

	sort.Slice(findings, func(a, b int) bool {
		if findings[a].Pos.Filename != findings[b].Pos.Filename {
			return findings[a].Pos.Filename < findings[b].Pos.Filename
		}
		return findings[a].Pos.Line < findings[b].Pos.Line
	})
	return findings, nil
}

// collectOverlapFiles returns absolute paths of buildable Go files under
// root, skipping skipped directories and files the go tool ignores
// (leading `.`/`_`) as well as _test.go handling left to MatchFile.
func collectOverlapFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && overlapSkipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %q: %w", root, err)
	}
	sort.Strings(files)
	return files, nil
}

// parseOverlapDecls parses one file and returns its package name plus every
// package-level declared name. Methods are keyed by receiver + name since
// (T).Foo and (U).Foo do not collide; blank identifiers never collide.
func parseOverlapDecls(path string) (string, []declOccurrence, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return "", nil, err
	}
	constraint := buildConstraintLine(file)
	var decls []declOccurrence
	for _, decl := range file.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			if node.Name.Name == "_" {
				continue
			}
			if node.Recv == nil {
				// Multiple bare init functions are legal Go; every
				// analyzer package in this repo uses one per file.
				if node.Name.Name == "init" {
					continue
				}
				decls = append(decls, declOccurrence{
					kind: "func", name: node.Name.Name,
					pos: fset.Position(node.Name.Pos()), constraint: constraint,
				})
				continue
			}
			recv := receiverBaseName(node.Recv)
			if recv == "" {
				continue
			}
			decls = append(decls, declOccurrence{
				kind: "method", name: recv + "." + node.Name.Name,
				pos: fset.Position(node.Name.Pos()), constraint: constraint,
			})
		case *ast.GenDecl:
			kind := genDeclKind(node.Tok)
			for _, spec := range node.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if spec.Name.Name == "_" {
						continue
					}
					decls = append(decls, declOccurrence{
						kind: "type", name: spec.Name.Name,
						pos: fset.Position(spec.Name.Pos()), constraint: constraint,
					})
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						if name.Name == "_" {
							continue
						}
						decls = append(decls, declOccurrence{
							kind: kind, name: name.Name,
							pos: fset.Position(name.Pos()), constraint: constraint,
						})
					}
				}
			}
		}
	}
	return file.Name.Name, decls, nil
}

// genDeclKind names a GenDecl token for messages (type, var, const).
func genDeclKind(tok token.Token) string {
	switch tok {
	case token.TYPE:
		return "type"
	case token.CONST:
		return "const"
	default:
		return "var"
	}
}

// receiverBaseName extracts T from `func (t T) M()` or `func (t *T) M()`.
func receiverBaseName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	// Generic receiver (T[P]) or anything exotic: fall back to source text
	// shape via the type expression's top identifier when possible.
	if idx, ok := expr.(*ast.IndexExpr); ok {
		if id, ok := idx.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	if idx, ok := expr.(*ast.IndexListExpr); ok {
		if id, ok := idx.X.(*ast.Ident); ok {
			return id.Name
		}
	}
	return ""
}

// buildConstraintLine returns the raw //go:build expression of a file,
// or "" when the file has none (always included, modulo filename suffix).
func buildConstraintLine(file *ast.File) string {
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if rest, ok := strings.CutPrefix(text, "go:build "); ok {
				return strings.TrimSpace(rest)
			}
		}
	}
	return ""
}

// describeConstraint renders a constraint for messages.
func describeConstraint(expression string) string {
	if expression == "" {
		return "no build constraint"
	}
	return "//go:build " + expression
}

// shortDeclName strips the receiver from method keys for messages.
func shortDeclName(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// matchPlatforms returns the set of "GOOS/GOARCH/cgo=N" triples selecting
// the file, evaluating both filename suffixes and //go:build lines through
// go/build so the verdict matches the real toolchain.
func matchPlatforms(dir, base string) map[string]bool {
	set := make(map[string]bool)
	for _, goos := range overlapGOOS {
		for _, goarch := range overlapGOARCH {
			for _, cgo := range []bool{false, true} {
				ctx := build.Context{GOOS: goos, GOARCH: goarch, CgoEnabled: cgo}
				ok, err := ctx.MatchFile(dir, base)
				if err != nil || !ok {
					continue
				}
				cgoFlag := "0"
				if cgo {
					cgoFlag = "1"
				}
				set[goos+"/"+goarch+"/cgo="+cgoFlag] = true
			}
		}
	}
	return set
}

// firstCommonPlatform returns an example "GOOS/GOARCH" triple present in
// both inclusion sets, preferring cgo-independent phrasing when the triple
// collides under both cgo settings.
func firstCommonPlatform(first, second map[string]bool) (string, bool) {
	var candidates []string
	for platform := range first {
		if second[platform] {
			candidates = append(candidates, platform)
		}
	}
	if len(candidates) == 0 {
		return "", false
	}
	// Prefer linux examples: most build-tag collisions that matter in
	// practice (seccomp, bwrap, cgroups) are Linux-scoped, and naming a
	// familiar pair keeps the message actionable.
	sort.Slice(candidates, func(a, b int) bool {
		aLinux := strings.HasPrefix(candidates[a], "linux/")
		bLinux := strings.HasPrefix(candidates[b], "linux/")
		if aLinux != bLinux {
			return aLinux
		}
		return candidates[a] < candidates[b]
	})
	example := candidates[0]
	if idx := strings.Index(example, "/cgo="); idx >= 0 {
		example = example[:idx]
	}
	return example, true
}
