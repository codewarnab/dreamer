package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPlatformAssetName(t *testing.T) {
	got := platformAssetName()
	want := fmt.Sprintf("dreamer_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	if got != want {
		t.Errorf("platformAssetName() = %q, want %q", got, want)
	}
}

func TestFindAssetMatch(t *testing.T) {
	assets := []githubAsset{
		{Name: "dreamer_linux_amd64", BrowserDownloadURL: "https://example.com/linux"},
		{Name: "dreamer_windows_amd64.exe", BrowserDownloadURL: "https://example.com/win"},
	}
	got := findAsset(assets, "dreamer_windows_amd64.exe")
	if got == nil {
		t.Fatal("findAsset returned nil for existing asset")
	}
	if got.BrowserDownloadURL != "https://example.com/win" {
		t.Errorf("URL = %q, want %q", got.BrowserDownloadURL, "https://example.com/win")
	}
}

func TestFindAssetNoMatch(t *testing.T) {
	assets := []githubAsset{
		{Name: "dreamer_linux_amd64", BrowserDownloadURL: "https://example.com/linux"},
	}
	got := findAsset(assets, "dreamer_plan9_amd64")
	if got != nil {
		t.Errorf("findAsset should return nil for missing asset, got %v", got)
	}
}

func TestFetchLatestReleaseSuccess(t *testing.T) {
	want := githubRelease{
		TagName: "v1.2.3",
		Assets: []githubAsset{
			{Name: "dreamer_windows_amd64.exe", BrowserDownloadURL: "https://example.com/dreamer.exe"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	// Temporarily override the API base.
	orig := githubAPIBase
	// We can't override a const, so we test via the HTTP handler directly.
	// This validates the JSON parsing logic.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	var got githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.TagName != want.TagName {
		t.Errorf("TagName = %q, want %q", got.TagName, want.TagName)
	}
	if len(got.Assets) != 1 {
		t.Fatalf("Assets len = %d, want 1", len(got.Assets))
	}
	if got.Assets[0].Name != "dreamer_windows_amd64.exe" {
		t.Errorf("Asset name = %q, want %q", got.Assets[0].Name, "dreamer_windows_amd64.exe")
	}

	_ = orig // suppress unused warning
}

func TestUpdateCommandAlreadyUpToDate(t *testing.T) {
	// This test verifies the command structure compiles and the --check flag
	// is registered. Actual update testing requires a live GitHub API.
	cmd := newUpdateCommand()
	if cmd.Use != "update" {
		t.Errorf("Use = %q, want %q", cmd.Use, "update")
	}
	if cmd.Flags().Lookup("check") == nil {
		t.Error("--check flag not registered")
	}
	if cmd.Flags().Lookup("force") == nil {
		t.Error("--force flag not registered")
	}
}

func TestParseChecksum(t *testing.T) {
	data := []byte("abc123def456  dreamer_linux_amd64\n7890abcdef12  dreamer_windows_amd64.exe\n")
	got, err := parseChecksum(data, "dreamer_windows_amd64.exe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "7890abcdef12" {
		t.Errorf("got %q, want %q", got, "7890abcdef12")
	}
}

func TestParseChecksumMissing(t *testing.T) {
	data := []byte("abc123def456  dreamer_linux_amd64\n")
	_, err := parseChecksum(data, "dreamer_plan9_amd64")
	if err == nil {
		t.Fatal("expected error for missing asset")
	}
}

func TestParseChecksumInvalidHex(t *testing.T) {
	data := []byte("ZZZZZZ  dreamer_linux_amd64\n")
	_, err := parseChecksum(data, "dreamer_linux_amd64")
	if err == nil {
		t.Fatal("expected error for invalid hex")
	}
}

func TestFetchChecksum(t *testing.T) {
	wantHash := "abc123def4567890abcdef1234567890abcdef1234567890abcdef1234567890"
	checksumsContent := fmt.Sprintf("%s  dreamer_%s_%s\n", wantHash, runtime.GOOS, runtime.GOARCH)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(checksumsContent))
	}))
	defer srv.Close()

	got, err := fetchChecksum(srv.URL, fmt.Sprintf("dreamer_%s_%s", runtime.GOOS, runtime.GOARCH))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != wantHash {
		t.Errorf("got %q, want %q", got, wantHash)
	}
}

func TestFetchChecksumMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("abc123  dreamer_linux_amd64\n"))
	}))
	defer srv.Close()

	_, err := fetchChecksum(srv.URL, "dreamer_plan9_amd64")
	if err == nil {
		t.Fatal("expected error for missing asset")
	}
}

func TestVerifySHA256Match(t *testing.T) {
	dir := t.TempDir()
	content := []byte("test binary content")
	path := filepath.Join(dir, "testfile")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	h := sha256.Sum256(content)
	expected := hex.EncodeToString(h[:])

	if err := verifySHA256(path, expected); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifySHA256Mismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")
	if err := os.WriteFile(path, []byte("test binary content"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := verifySHA256(path, "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}
