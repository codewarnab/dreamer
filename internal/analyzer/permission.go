package analyzer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// shellWriteIdiomRE matches common shell write idioms that the upstream
// classifier does not always flag: writers (tee, dd, …), in-place editors
// (sed -i, perl -pi), shell wrappers (bash -c, sh -c) that can hide writes,
// process-substitution write side `>(...)`, and inline interpreter -e/-pi.
//
// All command-name alternates anchor at command position — start of line, or
// after a shell separator (`;`, `&`, `|`, `(`, `$(`) — so legitimate reads
// like `cat patch.txt` or `grep 'tee' README.md` are not denied.
var shellCmdAnchor = `(?:^|[;&|(]|\$\()\s*`

var shellWriteIdiomRE = regexp.MustCompile(
	`(?i)` +
		shellCmdAnchor + `(tee|dd|rsync|mkfifo|mknod|truncate)\b` +
		`|` + shellCmdAnchor + `(bash|sh|zsh|ksh|dash|ash|fish|csh|tcsh)\s+-c\b` +
		`|` + shellCmdAnchor + `sed\s+(-i\b|--in-place\b)` +
		`|` + shellCmdAnchor + `(perl|python\d*|ruby|node|tcl)\s+(-i\b|-pi\b|-e\b)` +
		`|>\(`,
)

// DecidePermission applies the read-only sandbox rules from §4.5 to a
// provider-agnostic permission request. Resolved working directory must be
// absolute (caller's responsibility); empty means "no path scoping".
func DecidePermission(req PermissionRequest, normalizedRoot string) PermissionDecision {
	switch req.Kind {
	case PermissionKindRead:
		return decideFilesystem(req, normalizedRoot)
	case PermissionKindURL:
		return PermissionDecision{Approved: true}
	case PermissionKindShell:
		if !shellRequestReadOnly(req) {
			return PermissionDecision{Reason: "shell request is not read-only"}
		}
		return decideFilesystem(req, normalizedRoot)
	case PermissionKindMCPTool, PermissionKindCustomTool:
		if req.ReadOnly != nil && *req.ReadOnly {
			return PermissionDecision{Approved: true}
		}
		return PermissionDecision{Reason: "tool request is not read-only"}
	default:
		return PermissionDecision{Reason: fmt.Sprintf("permission kind %q is not allowed in read-only analysis mode", req.Kind)}
	}
}

func decideFilesystem(req PermissionRequest, normalizedRoot string) PermissionDecision {
	if normalizedRoot == "" {
		return PermissionDecision{Approved: true}
	}
	candidates := requestPathCandidates(req)
	if len(candidates) == 0 {
		return PermissionDecision{Reason: "filesystem request did not include candidate paths to validate"}
	}
	for _, candidate := range candidates {
		normalized, err := normalizeCandidatePath(candidate, normalizedRoot)
		if err != nil {
			return PermissionDecision{Reason: fmt.Sprintf("filesystem path %q is invalid or ambiguous after symlink resolution: %v", candidate, err)}
		}
		if !pathWithinRoot(normalized, normalizedRoot) {
			return PermissionDecision{Reason: fmt.Sprintf("filesystem path %q resolves outside project root %q after symlink resolution (resolved to %q)", candidate, normalizedRoot, normalized)}
		}
	}
	return PermissionDecision{Approved: true}
}

func requestPathCandidates(req PermissionRequest) []string {
	candidates := make([]string, 0, len(req.PossiblePaths)+1)
	if req.Path != nil {
		candidates = append(candidates, *req.Path)
	}
	candidates = append(candidates, req.PossiblePaths...)
	return candidates
}

func shellRequestReadOnly(req PermissionRequest) bool {
	if req.HasWriteFileRedirection != nil && *req.HasWriteFileRedirection {
		return false
	}
	// Second-pass deny: even when the SDK says read-only, reject known write
	// idioms in the raw command text (B2). The SDK redirection detector
	// handles `>` / `>>` only; it misses tee, dd, sed -i, bash -c wrappers,
	// process substitution, etc.
	if req.FullCommandText != nil && shellWriteIdiomRE.MatchString(*req.FullCommandText) {
		return false
	}
	if req.ReadOnly != nil {
		return *req.ReadOnly
	}
	if len(req.Commands) == 0 {
		return false
	}
	for _, cmd := range req.Commands {
		if !cmd.ReadOnly {
			return false
		}
	}
	return true
}

// NormalizeRootPath returns the absolute, symlink-resolved, cleaned path; "" for blank.
// Root must exist; resolver errors are returned to the caller.
func NormalizeRootPath(root string) (string, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return "", nil
	}
	if strings.ContainsRune(trimmed, '\x00') {
		return "", fmt.Errorf("project root contains null byte")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks for project root %q: %w", abs, err)
	}
	return filepath.Clean(resolved), nil
}

// normalizeCandidatePath returns the absolute, symlink-resolved candidate path.
// Non-existent leaves resolve via the deepest existing ancestor; resolver errors deny.
func normalizeCandidatePath(path string, normalizedRoot string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("path is empty")
	}
	if strings.ContainsRune(trimmed, '\x00') {
		return "", fmt.Errorf("path contains null byte")
	}
	if looksLikeNonFilesystemPath(trimmed) {
		return "", fmt.Errorf("path appears to be non-filesystem")
	}
	candidate := trimmed
	if isVolumeRelativeRootedPath(candidate) {
		candidate = filepath.VolumeName(normalizedRoot) + candidate
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(normalizedRoot, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	return resolveSymlinksAllowingMissing(abs)
}

// resolveSymlinksAllowingMissing resolves `abs`, walking up to the deepest
// existing ancestor when the leaf does not exist; non-ENOENT errors return.
func resolveSymlinksAllowingMissing(abs string) (string, error) {
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve symlinks for %q: %w", abs, err)
	}

	tail := []string{}
	cur := abs
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		base := filepath.Base(cur)
		tail = append([]string{base}, tail...)
		resolvedParent, err := filepath.EvalSymlinks(parent)
		if err == nil {
			out := resolvedParent
			for _, seg := range tail {
				out = filepath.Join(out, seg)
			}
			return filepath.Clean(out), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolve symlinks for ancestor %q of %q: %w", parent, abs, err)
		}
		cur = parent
	}
}

func looksLikeNonFilesystemPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, "://") || strings.HasPrefix(lower, "file:")
}

func isVolumeRelativeRootedPath(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	return filepath.VolumeName(path) == "" && strings.HasPrefix(filepath.Clean(path), string(filepath.Separator))
}

func pathWithinRoot(path string, root string) bool {
	if root == "" {
		return true
	}
	if pathsEqual(path, root) {
		return true
	}
	prefix := root + string(filepath.Separator)
	if runtime.GOOS == "windows" {
		return strings.HasPrefix(strings.ToLower(path), strings.ToLower(prefix))
	}
	return strings.HasPrefix(path, prefix)
}

func pathsEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
