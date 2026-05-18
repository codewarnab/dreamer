package analyzer

import (
	"os"
	"path/filepath"
	"runtime"
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
		// B2: SDK said read-only but full command text contains a tee write.
		{
			name: "shell sdk-readonly but tee write",
			req: PermissionRequest{
				Kind:            PermissionKindShell,
				ReadOnly:        boolPtr(true),
				FullCommandText: stringPtr("cat /tmp/a.txt | tee /tmp/b.txt"),
				Commands:        []ShellCommand{{Identifier: "cat", ReadOnly: true}},
			},
			reasonLike: "not read-only",
		},
		{
			name: "shell sdk-readonly but bash -c wrapper",
			req: PermissionRequest{
				Kind:            PermissionKindShell,
				ReadOnly:        boolPtr(true),
				FullCommandText: stringPtr(`bash -c 'echo hi > /tmp/x'`),
				Commands:        []ShellCommand{{Identifier: "bash", ReadOnly: true}},
			},
			reasonLike: "not read-only",
		},
		{
			name: "shell sdk-readonly but sed in-place",
			req: PermissionRequest{
				Kind:            PermissionKindShell,
				ReadOnly:        boolPtr(true),
				FullCommandText: stringPtr("sed -i s/foo/bar/ /tmp/file"),
				Commands:        []ShellCommand{{Identifier: "sed", ReadOnly: true}},
			},
			reasonLike: "not read-only",
		},
		{
			name: "shell sdk-readonly but process-substitution write",
			req: PermissionRequest{
				Kind:            PermissionKindShell,
				ReadOnly:        boolPtr(true),
				FullCommandText: stringPtr("diff a.txt >(cat > out.txt)"),
				Commands:        []ShellCommand{{Identifier: "diff", ReadOnly: true}},
			},
			reasonLike: "not read-only",
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

// B2: regex must not deny legitimate reads whose arguments happen to mention
// a writer command as a substring or quoted literal.
func TestShellRequestReadOnlyAllowsBenignReads(t *testing.T) {
	cases := []string{
		"cat patch.txt",
		"cat install.log",
		"grep 'tee' README.md",
		"grep -F 'rsync' src.go",
		"cat docs/install-guide.md",
		"git log -p",
		"ls -la",
	}
	for _, line := range cases {
		text := line
		t.Run(line, func(t *testing.T) {
			req := PermissionRequest{
				Kind:            PermissionKindShell,
				ReadOnly:        boolPtr(true),
				FullCommandText: &text,
				Commands:        []ShellCommand{{Identifier: "cat", ReadOnly: true}},
			}
			if !shellRequestReadOnly(req) {
				t.Fatalf("benign read %q wrongly classified as not read-only", text)
			}
		})
	}
}

func TestDecidePermissionSymlinkEscapeIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("classified"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	repo := t.TempDir()
	linkInsideRepo := filepath.Join(repo, "notes")
	if err := os.Symlink(outsideFile, linkInsideRepo); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	root, err := NormalizeRootPath(repo)
	if err != nil {
		t.Fatalf("NormalizeRootPath: %v", err)
	}

	decision := DecidePermission(PermissionRequest{
		Kind: PermissionKindRead,
		Path: stringPtr(linkInsideRepo),
	}, root)
	if decision.Approved {
		t.Fatalf("symlink pointing outside root must be denied; reason=%q", decision.Reason)
	}
	if !strings.Contains(strings.ToLower(decision.Reason), "symlink") {
		t.Fatalf("reason %q should mention symlink resolution", decision.Reason)
	}
}

func TestDecidePermissionSymlinkInPrefixIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "secrets"), 0o700); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}

	repo := t.TempDir()
	// `repo/escape` is a symlink pointing at outside; any path under it
	// must be denied even when the leaf does not exist yet.
	if err := os.Symlink(outside, filepath.Join(repo, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	root, err := NormalizeRootPath(repo)
	if err != nil {
		t.Fatalf("NormalizeRootPath: %v", err)
	}

	decision := DecidePermission(PermissionRequest{
		Kind: PermissionKindRead,
		Path: stringPtr(filepath.Join(repo, "escape", "secrets", "vault.txt")),
	}, root)
	if decision.Approved {
		t.Fatalf("non-existent path under escaping symlink must be denied; reason=%q", decision.Reason)
	}
	if !strings.Contains(strings.ToLower(decision.Reason), "symlink") {
		t.Fatalf("reason %q should mention symlink resolution", decision.Reason)
	}
}

func TestNormalizeRootPathErrorsWhenRootMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := NormalizeRootPath(missing); err == nil {
		t.Fatalf("NormalizeRootPath(%q) expected error for missing dir", missing)
	}
}

func stringPtr(s string) *string { return &s }
func boolPtr(b bool) *bool       { return &b }
