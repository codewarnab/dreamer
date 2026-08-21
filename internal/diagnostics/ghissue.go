package diagnostics

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"dreamer/internal/procutil"
)

// RepoSlug is the GitHub repository bug reports are filed against.
const RepoSlug = "codewarnab/dreamer"

// IssuesURL is the human-facing new-issue entry point.
const IssuesURL = "https://github.com/" + RepoSlug + "/issues/new/choose"

// ghIssueBodyLimit is GitHub's maximum issue body length in characters.
// Bodies are truncated to stay safely under it.
const ghIssueBodyLimit = 60_000

// GhInfo describes the detected GitHub CLI.
type GhInfo struct {
	Installed bool
	Path      string
	Version   string // first line of `gh --version`, best-effort
}

// DetectGh locates the GitHub CLI on PATH. Never fails: a missing binary is
// reported as Installed=false so callers can fall back to manual reporting.
func DetectGh() GhInfo {
	return DetectGhWithPath(exec.LookPath)
}

// DetectGhWithPath is the injectable-lookup variant of DetectGh used by tests.
func DetectGhWithPath(lookPath func(string) (string, error)) GhInfo {
	path, err := lookPath("gh")
	if err != nil {
		return GhInfo{}
	}
	info := GhInfo{Installed: true, Path: path}
	versionCmd := exec.Command(path, "--version")
	procutil.SetNoWindow(versionCmd)
	if out, err := versionCmd.Output(); err == nil {
		if line, _, ok := strings.Cut(string(out), "\n"); ok {
			info.Version = strings.TrimSpace(line)
		}
	}
	return info
}

// CreateIssue files an issue via the GitHub CLI and returns the issue URL.
//
// Errors are classified so callers can print actionable remediation:
//   - not authenticated → suggests `gh auth login`
//   - other gh failures → wrapped with gh's stderr for diagnosis
func CreateIssue(ctx context.Context, ghPath, title, body string) (string, error) {
	body = TruncateBody(body, ghIssueBodyLimit)

	cmd := exec.CommandContext(ctx, ghPath, "issue", "create",
		"-R", RepoSlug,
		"--title", title,
		"--body", body,
	)
	procutil.SetNoWindow(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		stderrText := strings.TrimSpace(stderr.String())
		if isGhAuthError(stderrText) {
			return "", fmt.Errorf(
				"GitHub CLI is not authenticated; run 'gh auth login' first (details: %s)", stderrText)
		}
		if stderrText != "" {
			return "", fmt.Errorf("gh issue create failed: %s", stderrText)
		}
		return "", fmt.Errorf("gh issue create failed: %w", err)
	}

	url := strings.TrimSpace(string(out))
	if url == "" {
		return "", fmt.Errorf("gh issue create succeeded but returned no issue URL")
	}
	return url, nil
}

// isGhAuthError recognizes the common "not logged in" failure text emitted by
// the GitHub CLI across versions.
func isGhAuthError(stderr string) bool {
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "not logged") {
		return true
	}
	return strings.Contains(lower, "auth") && strings.Contains(lower, "login")
}
