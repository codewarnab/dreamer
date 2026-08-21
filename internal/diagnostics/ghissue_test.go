package diagnostics

import (
	"os/exec"
	"testing"
)

func TestIsGhAuthErrorDetectsLoginPrompt(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"gh: To get started with GitHub CLI, please run: gh auth login", true},
		{"error: not logged into any host", true},
		{"authentication token expired", false},
		{"GraphQL: Not found (repository)", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isGhAuthError(tc.stderr); got != tc.want {
			t.Errorf("isGhAuthError(%q) = %v, want %v", tc.stderr, got, tc.want)
		}
	}
}

func TestDetectGhMissingBinaryIsNotInstalled(t *testing.T) {
	// DetectGh must never fail; a missing binary just reports Installed=false.
	info := DetectGhWithPath(func(string) (string, error) {
		return "", exec.ErrNotFound
	})
	if info.Installed {
		t.Error("gh must not be detected when lookup fails")
	}
}

func TestTruncateBodyRespectsIssueLimit(t *testing.T) {
	body := make([]byte, ghIssueBodyLimit+5000)
	for i := range body {
		body[i] = 'x'
	}
	got := TruncateBody(string(body), ghIssueBodyLimit)
	if len([]rune(got)) > ghIssueBodyLimit {
		t.Errorf("truncated body exceeds limit: %d > %d", len([]rune(got)), ghIssueBodyLimit)
	}
}
