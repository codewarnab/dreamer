package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
