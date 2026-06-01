package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	// updateRepo is the GitHub owner/repo for release downloads.
	updateRepo = "codewarnab/dreamer"

	// updateTimeout is the HTTP client timeout for API + download calls.
	updateTimeout = 60 * time.Second

	// githubAPIBase is the base URL for GitHub REST API calls.
	githubAPIBase = "https://api.github.com"
)

func newUpdateCommand() *cobra.Command {
	var (
		checkOnly bool
		force     bool
	)

	command := &cobra.Command{
		Use:   "update",
		Short: "Update dreamer to the latest release",
		Long: "Check GitHub Releases for a newer version and replace the running binary.\n" +
			"Use --check to only compare versions without downloading.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			current := Version()
			latest, err := fetchLatestRelease()
			if err != nil {
				return err
			}

			latestTag := strings.TrimPrefix(latest.TagName, "v")
			currentClean := strings.TrimPrefix(current, "v")

			if !force && currentClean == latestTag {
				cmd.Printf("Already up to date: %s\n", current)
				return nil
			}

			if checkOnly {
				cmd.Printf("Current:  %s\n", current)
				cmd.Printf("Latest:   %s\n", latestTag)
				return nil
			}

			cmd.Printf("Updating %s → %s ...\n", current, latestTag)

			wantAsset := platformAssetName()
			asset := findAsset(latest.Assets, wantAsset)
			if asset == nil {
				return fmt.Errorf("no release asset found for %s", wantAsset)
			}

			checksumURL := strings.Replace(asset.BrowserDownloadURL, wantAsset, "checksums.txt", 1)
			if err := downloadAndReplace(asset.BrowserDownloadURL, checksumURL, wantAsset); err != nil {
				return err
			}

			cmd.Printf("Updated dreamer %s → %s\n", current, latestTag)
			return nil
		},
	}

	command.Flags().BoolVar(&checkOnly, "check", false, "Check for updates without downloading")
	command.Flags().BoolVar(&force, "force", false, "Re-download even if already up to date")
	return command
}

// --- GitHub API types ---

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// --- Pure helpers (no I/O) ---

// platformAssetName returns the expected release asset filename for the
// current OS and architecture. Matches goreleaser's naming convention:
// "dreamer_{os}_{arch}[.exe]".
func platformAssetName() string {
	name := fmt.Sprintf("dreamer_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// findAsset returns the asset matching wantName, or nil.
func findAsset(assets []githubAsset, wantName string) *githubAsset {
	for i := range assets {
		if assets[i].Name == wantName {
			return &assets[i]
		}
	}
	return nil
}

// --- Imperative shell (I/O) ---

// fetchLatestRelease calls the GitHub API for the latest release.
func fetchLatestRelease() (*githubRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPIBase, updateRepo)

	client := &http.Client{Timeout: updateTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("check latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no releases found at github.com/%s — publish a release first", updateRepo)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release JSON: %w", err)
	}
	return &release, nil
}

// downloadAndReplace downloads the binary from binaryURL, verifies its SHA-256
// checksum against checksums.txt fetched from checksumURL, and replaces the
// running executable. Uses a same-directory temp file so the rename stays on
// one filesystem (required for atomic rename on Unix).
func downloadAndReplace(binaryURL, checksumURL, assetName string) error {
	selfPath, err := resolveSelfExecutable()
	if err != nil {
		return err
	}
	selfDir := filepath.Dir(selfPath)

	tmpFile, err := os.CreateTemp(selfDir, ".dreamer-update-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	success := false
	defer func() {
		if !success {
			tmpFile.Close()
			os.Remove(tmpPath)
		}
	}()

	client := &http.Client{Timeout: updateTimeout}
	resp, err := client.Get(binaryURL)
	if err != nil {
		return fmt.Errorf("download release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("write download: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	// Verify SHA-256 checksum before replacing the running binary.
	expectedHash, err := fetchChecksum(checksumURL, assetName)
	if err != nil {
		return fmt.Errorf("fetch checksum: %w", err)
	}
	if err := verifySHA256(tmpPath, expectedHash); err != nil {
		return err
	}

	// Make executable (Unix). On Windows Chmod is a no-op for this purpose.
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("set permissions: %w", err)
	}

	if err := replaceBinary(selfPath, tmpPath); err != nil {
		return err
	}

	success = true
	return nil
}

// fetchChecksum downloads checksums.txt from checksumURL and returns the
// expected SHA-256 hex digest for assetName. The file uses goreleaser's format:
//
//	<sha256hex>  <filename>
func fetchChecksum(checksumURL, assetName string) (string, error) {
	client := &http.Client{Timeout: updateTimeout}
	resp, err := client.Get(checksumURL)
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksums download returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}

	return parseChecksum(body, assetName)
}

// parseChecksum parses goreleaser-format checksums.txt and returns the hex
// SHA-256 for the given asset name.
func parseChecksum(data []byte, assetName string) (string, error) {
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "<sha256>  <filename>" (two-space separator)
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[1]) == assetName {
			hash := strings.TrimSpace(parts[0])
			if _, err := hex.DecodeString(hash); err != nil {
				return "", fmt.Errorf("invalid checksum format for %s", assetName)
			}
			return hash, nil
		}
	}
	return "", fmt.Errorf("checksum for %s not found in checksums.txt", assetName)
}

// verifySHA256 computes the SHA-256 of the file at path and compares it to the
// expected hex digest. Returns an error on mismatch with both hashes in the
// message.
func verifySHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open file for verification: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("compute checksum: %w", err)
	}
	actual := hex.EncodeToString(h.Sum(nil))

	if actual != expected {
		return fmt.Errorf("checksum mismatch: expected %s, got %s — binary may be corrupted or tampered with", expected, actual)
	}
	return nil
}

// replaceBinary moves tmpPath to targetPath. On Windows the running binary
// cannot be deleted directly, so we rename the old one first and clean up
// after. On Unix, os.Rename works because the OS keeps the old inode alive
// while the process is running.
func replaceBinary(targetPath, tmpPath string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(tmpPath, targetPath)
	}

	oldPath := targetPath + ".old"
	os.Remove(oldPath) // clean up leftover from previous update

	if err := os.Rename(targetPath, oldPath); err != nil {
		return fmt.Errorf("rename current binary: %w", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		os.Rename(oldPath, targetPath) // restore on failure
		return fmt.Errorf("place new binary: %w", err)
	}
	os.Remove(oldPath) // best-effort cleanup
	return nil
}
