package diagnostics

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dreamer/internal/analyzer"
)

func TestCollectReportsConfigFailureAsFailedCheck(t *testing.T) {
	in := Input{
		ConfigPath: "/does/not/exist/config.yaml",
		ConfigErr:  os.ErrNotExist,
	}

	report := Collect(in)

	var configCheck *Check
	for i := range report.Checks {
		if report.Checks[i].Name == "config" {
			configCheck = &report.Checks[i]
			break
		}
	}
	if configCheck == nil {
		t.Fatal("Collect must always emit a 'config' check")
	}
	if configCheck.Status != StatusFail {
		t.Errorf("broken config must produce a fail check, got %q", configCheck.Status)
	}
	if !strings.Contains(configCheck.Detail, "failed to load") {
		t.Errorf("fail detail should explain the load failure, got %q", configCheck.Detail)
	}
	if !report.Failed() {
		t.Error("Report.Failed() must be true when the config check fails")
	}
}

func TestCollectHealthyInputPassesAllChecks(t *testing.T) {
	outputRoot := t.TempDir()
	logDir := filepath.Join(outputRoot, "logging")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "dreamer.log"), []byte("line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	in := Input{OutputRoot: outputRoot}
	report := Collect(in)

	for _, c := range report.Checks {
		switch c.Name {
		case "output-root", "log-file":
			if c.Status != StatusPass {
				t.Errorf("%s check should pass with a writable root and log present, got %q (%s)", c.Name, c.Status, c.Detail)
			}
		case "sandbox":
			if c.Status != StatusPass && c.Status != StatusWarn {
				t.Errorf("sandbox check should be pass or warn, got %q", c.Status)
			}
		}
	}
	if report.Failed() {
		t.Error("healthy input must not produce failed checks")
	}
}

func TestCollectOutputRootUnwritableFails(t *testing.T) {
	// A file where the output root should be makes MkdirAll fail.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := Collect(Input{OutputRoot: blocker})

	var found bool
	for _, c := range report.Checks {
		if c.Name == "output-root" {
			found = true
			if c.Status != StatusFail {
				t.Errorf("unwritable output root must fail, got %q", c.Status)
			}
		}
	}
	if !found {
		t.Fatal("missing output-root check")
	}
}

func TestWriteBundleRedactsSecretsAndIncludesReport(t *testing.T) {
	outputRoot := t.TempDir()
	logDir := filepath.Join(outputRoot, "logging")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secretLog := "provider said: ghp_" + strings.Repeat("aBcDeFgHiJkLmNoPqRsT1234", 2) + " ok\n"
	if err := os.WriteFile(filepath.Join(logDir, "dreamer.log"), []byte(secretLog), 0o644); err != nil {
		t.Fatal(err)
	}

	redactor, err := analyzer.NewRedactor(nil)
	if err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "bundle.zip")
	written, err := WriteBundle(Report{}, Input{OutputRoot: outputRoot}, redactor, BundleOptions{}, dest)
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	if written != dest {
		t.Errorf("bundle path = %q, want %q", written, dest)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}

	var reportContent string
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatal(err)
		}
		rc.Close()
		content := buf.String()

		if strings.Contains(content, "ghp_") && strings.Contains(content, "aBcDeFgHiJkLmNoPqRsT") {
			t.Errorf("zip entry %q contains unredacted secret", f.Name)
		}
		if f.Name == "report.txt" {
			reportContent = content
		}
	}
	if reportContent == "" {
		t.Error("bundle must contain report.txt")
	}
}

func TestWriteBundleSkipsMissingLogs(t *testing.T) {
	// Output root exists but has no logging dir at all — bundling must still
	// succeed with just report.txt.
	dest := filepath.Join(t.TempDir(), "bundle.zip")
	_, err := WriteBundle(Report{}, Input{OutputRoot: t.TempDir()}, nil, BundleOptions{}, dest)
	if err != nil {
		t.Fatalf("WriteBundle with no logs should succeed: %v", err)
	}
}

func TestTailFileDropsPartialFirstLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.txt")
	content := strings.Repeat("x", 200) + "\nsecond line\nthird line\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := tailFile(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, strings.Repeat("x", 200)) {
		t.Error("tail must not include content beyond maxBytes")
	}
	if !strings.HasPrefix(got, "second line") {
		prefix := got
		if len(prefix) > 20 {
			prefix = prefix[:20]
		}
		t.Errorf("tail must start on a complete line, got %q", prefix)
	}
	if !strings.Contains(got, "third line") {
		t.Errorf("tail must include the final line, got %q", got)
	}
}

func TestTruncateBodyAppendsMarker(t *testing.T) {
	long := strings.Repeat("a", 100_000)
	got := TruncateBody(long, 1000)
	if len([]rune(got)) >= 100_000 {
		t.Error("body was not truncated")
	}
	if !strings.Contains(got, "[...truncated by dreamer bug") {
		t.Error("truncated body must carry a visible marker")
	}
	if TruncateBody("short", 1000) != "short" {
		t.Error("short bodies must pass through unchanged")
	}
}
