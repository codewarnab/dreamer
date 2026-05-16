package analyzer

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
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
			return PermissionDecision{Reason: fmt.Sprintf("filesystem path %q is invalid or ambiguous: %v", candidate, err)}
		}
		if !pathWithinRoot(normalized, normalizedRoot) {
			return PermissionDecision{Reason: fmt.Sprintf("filesystem path %q resolves outside project root %q", candidate, normalizedRoot)}
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

// NormalizeRootPath returns the absolute, cleaned path of root, or empty if root is empty.
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
	return filepath.Clean(abs), nil
}

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
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(normalizedRoot, candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func looksLikeNonFilesystemPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, "://") || strings.HasPrefix(lower, "file:")
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
