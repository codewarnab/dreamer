package fsutil

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExpandUserHome(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty", "", false},
		{"relative", "foo/bar", false},
		{"absolute", "/tmp/test", false},
		{"tilde", "~", false},
		{"tilde_slash", "~/foo", false},
		{"tilde_backslash", "~\\foo", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExpandUserHome(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExpandUserHome(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if tt.input == "" && got != "" {
				t.Fatalf("ExpandUserHome(%q) = %q, want empty", tt.input, got)
			}
			if tt.input == "~" && err == nil && got == "" {
				t.Fatalf("ExpandUserHome(%q) returned empty with no error", tt.input)
			}
			if strings.HasPrefix(tt.input, "~/") && err == nil && !filepath.IsAbs(got) {
				t.Fatalf("ExpandUserHome(%q) = %q, want absolute", tt.input, got)
			}
		})
	}
}

func TestCanonicalPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"relative", "foo/bar"},
		{"absolute", "/tmp/test"},
		{"tilde", "~"},
		{"tilde_slash", "~/foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanonicalPath(tt.input)
			if tt.input == "" {
				if got != "" {
					t.Fatalf("CanonicalPath(%q) = %q, want empty", tt.input, got)
				}
				return
			}
			if !filepath.IsAbs(got) {
				t.Fatalf("CanonicalPath(%q) = %q, want absolute", tt.input, got)
			}
			if runtime.GOOS == "windows" && got != strings.ToLower(got) {
				t.Fatalf("CanonicalPath(%q) = %q, want lowercase on Windows", tt.input, got)
			}
		})
	}
}

func TestNormalizeRootPath(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := NormalizeRootPath("")
		if err != nil {
			t.Fatalf("NormalizeRootPath(\"\") error = %v", err)
		}
		if got != "" {
			t.Fatalf("NormalizeRootPath(\"\") = %q, want empty", got)
		}
	})

	t.Run("null_byte", func(t *testing.T) {
		_, err := NormalizeRootPath("/tmp\x00/test")
		if err == nil {
			t.Fatal("NormalizeRootPath with null byte should error")
		}
	})

	t.Run("valid_dir", func(t *testing.T) {
		dir := t.TempDir()
		got, err := NormalizeRootPath(dir)
		if err != nil {
			t.Fatalf("NormalizeRootPath(%q) error = %v", dir, err)
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("NormalizeRootPath(%q) = %q, want absolute", dir, got)
		}
	})

	t.Run("missing_dir", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist")
		_, err := NormalizeRootPath(missing)
		if err == nil {
			t.Fatalf("NormalizeRootPath(%q) expected error for missing dir", missing)
		}
	})
}

func TestResolveSymlinks(t *testing.T) {
	t.Run("existing_dir", func(t *testing.T) {
		dir := t.TempDir()
		got, err := ResolveSymlinks(dir)
		if err != nil {
			t.Fatalf("ResolveSymlinks(%q) error = %v", dir, err)
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("ResolveSymlinks(%q) = %q, want absolute", dir, got)
		}
	})

	t.Run("missing_leaf", func(t *testing.T) {
		dir := t.TempDir()
		missing := filepath.Join(dir, "sub", "does-not-exist")
		got, err := ResolveSymlinks(missing)
		if err != nil {
			t.Fatalf("ResolveSymlinks(%q) error = %v", missing, err)
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("ResolveSymlinks(%q) = %q, want absolute", missing, got)
		}
	})
}

func TestPathWithinRoot(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp")
	child := filepath.Join(root, "foo")
	other := filepath.Join(string(filepath.Separator), "other")
	prefixMismatch := root + "-other"

	tests := []struct {
		name string
		path string
		root string
		want bool
	}{
		{"empty_path", "", root, false},
		{"empty_root", child, "", true},
		{"both_empty", "", "", false},
		{"equal", root, root, true},
		{"child", child, root, true},
		{"not_child", other, root, false},
		{"prefix_mismatch", prefixMismatch, root, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PathWithinRoot(tt.path, tt.root)
			if got != tt.want {
				t.Fatalf("PathWithinRoot(%q, %q) = %v, want %v", tt.path, tt.root, got, tt.want)
			}
		})
	}

	if runtime.GOOS == "windows" {
		t.Run("windows_case_insensitive", func(t *testing.T) {
			if !PathWithinRoot("C:\\tmp\\foo", "c:\\tmp") {
				t.Fatal("PathWithinRoot should be case-insensitive on Windows")
			}
		})
	}
}
