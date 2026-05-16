package analyzer

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDecidePermissionApprovesAllowedRequests(t *testing.T) {
	repo := t.TempDir()
	root, err := NormalizeRootPath(repo)
	if err != nil {
		t.Fatalf("NormalizeRootPath: %v", err)
	}

	readPath := filepath.Join(repo, "README.md")

	cases := []struct {
		name string
		req  PermissionRequest
	}{
		{"read in-root", PermissionRequest{Kind: PermissionKindRead, Path: stringPtr(readPath)}},
		{"url", PermissionRequest{Kind: PermissionKindURL}},
		{"readonly mcp", PermissionRequest{Kind: PermissionKindMCPTool, ReadOnly: boolPtr(true)}},
		{"readonly custom", PermissionRequest{Kind: PermissionKindCustomTool, ReadOnly: boolPtr(true)}},
		{"readonly shell in-root", PermissionRequest{
			Kind:          PermissionKindShell,
			Commands:      []ShellCommand{{Identifier: "git", ReadOnly: true}},
			PossiblePaths: []string{repo},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := DecidePermission(tc.req, root)
			if !decision.Approved {
				t.Fatalf("expected approval, got reason=%q", decision.Reason)
			}
		})
	}
}

func TestDecidePermissionRejectsOutOfRootAndWrites(t *testing.T) {
	repo := t.TempDir()
	root, err := NormalizeRootPath(repo)
	if err != nil {
		t.Fatalf("NormalizeRootPath: %v", err)
	}

	cases := []struct {
		name       string
		req        PermissionRequest
		reasonLike string
	}{
		{
			name:       "unknown kind",
			req:        PermissionRequest{Kind: PermissionKind("write")},
			reasonLike: "not allowed",
		},
		{
			name: "shell with non-readonly command",
			req: PermissionRequest{
				Kind: PermissionKindShell,
				Commands: []ShellCommand{
					{Identifier: "git", ReadOnly: true},
					{Identifier: "rm", ReadOnly: false},
				},
			},
			reasonLike: "not read-only",
		},
		{
			name: "shell with write redirection",
			req: PermissionRequest{
				Kind:                    PermissionKindShell,
				HasWriteFileRedirection: boolPtr(true),
			},
			reasonLike: "not read-only",
		},
		{
			name:       "read out-of-root",
			req:        PermissionRequest{Kind: PermissionKindRead, Path: stringPtr("/etc/passwd")},
			reasonLike: "outside project root",
		},
		{
			name:       "ambiguous URI",
			req:        PermissionRequest{Kind: PermissionKindRead, Path: stringPtr("file:///etc/passwd")},
			reasonLike: "invalid or ambiguous",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := DecidePermission(tc.req, root)
			if decision.Approved {
				t.Fatalf("expected rejection")
			}
			if !strings.Contains(strings.ToLower(decision.Reason), strings.ToLower(tc.reasonLike)) {
				t.Fatalf("reason %q does not contain %q", decision.Reason, tc.reasonLike)
			}
		})
	}
}

func stringPtr(s string) *string { return &s }
func boolPtr(b bool) *bool       { return &b }
