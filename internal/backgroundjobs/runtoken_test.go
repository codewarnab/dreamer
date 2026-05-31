package backgroundjobs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateRunToken_CreatesOnFirstCall(t *testing.T) {
	dir := t.TempDir()

	token, err := LoadOrCreateRunToken(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateRunToken: %v", err)
	}
	if len(token) != 64 {
		t.Errorf("token length = %d, want 64 (32 bytes hex)", len(token))
	}

	// Verify file exists. Unix perm check skipped on Windows (os.Chmod only controls read-only bit).
	info, err := os.Stat(filepath.Join(dir, runTokenFile))
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("token file perm = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadOrCreateRunToken_ReturnsExistingToken(t *testing.T) {
	dir := t.TempDir()

	token1, err := LoadOrCreateRunToken(dir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	token2, err := LoadOrCreateRunToken(dir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if token1 != token2 {
		t.Errorf("tokens differ: %q vs %q", token1, token2)
	}
}

func TestLoadOrCreateRunToken_RegeneratesEmptyFile(t *testing.T) {
	dir := t.TempDir()

	// Write an empty token file.
	if err := os.WriteFile(filepath.Join(dir, runTokenFile), []byte(""), 0o600); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	token, err := LoadOrCreateRunToken(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateRunToken: %v", err)
	}
	if token == "" {
		t.Error("expected non-empty token after empty file")
	}
}

func TestValidateRunToken_Match(t *testing.T) {
	dir := t.TempDir()

	token, err := LoadOrCreateRunToken(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := ValidateRunToken(dir, token); err != nil {
		t.Errorf("ValidateRunToken should pass on match: %v", err)
	}
}

func TestValidateRunToken_Mismatch(t *testing.T) {
	dir := t.TempDir()

	if _, err := LoadOrCreateRunToken(dir); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := ValidateRunToken(dir, "wrong-token"); err == nil {
		t.Error("ValidateRunToken should fail on mismatch")
	}
}

func TestValidateRunToken_NoTokenFile(t *testing.T) {
	dir := t.TempDir()

	if err := ValidateRunToken(dir, "anything"); err == nil {
		t.Error("ValidateRunToken should fail when no token file exists")
	}
}

func TestRunTokenPath(t *testing.T) {
	got := RunTokenPath("/some/dir")
	want := filepath.Join("/some/dir", runTokenFile)
	if got != want {
		t.Errorf("RunTokenPath = %q, want %q", got, want)
	}
}

func TestReadRunToken_Existing(t *testing.T) {
	dir := t.TempDir()

	created, err := LoadOrCreateRunToken(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	read, err := ReadRunToken(dir)
	if err != nil {
		t.Fatalf("ReadRunToken: %v", err)
	}
	if read != created {
		t.Errorf("ReadRunToken = %q, want %q", read, created)
	}
}

func TestReadRunToken_Missing(t *testing.T) {
	dir := t.TempDir()

	if _, err := ReadRunToken(dir); err == nil {
		t.Error("ReadRunToken should fail when no token file exists")
	}
}
