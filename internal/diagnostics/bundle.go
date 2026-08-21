package diagnostics

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/fsutil"
)

const (
	// DefaultLogTailBytes caps each log file included in a bundle. Matches
	// the web dashboard's default log tail window (256 KiB).
	DefaultLogTailBytes = 256 * 1024
	// DefaultRunLogTailBytes caps each per-run provider log.
	DefaultRunLogTailBytes = 128 * 1024
	// maxBundleRunLogs limits how many run logs are bundled so one noisy
	// job cannot produce an enormous archive.
	maxBundleRunLogs = 10
)

// BundleOptions controls bundle generation. Zero-value fields fall back to
// the package defaults above.
type BundleOptions struct {
	LogTailBytes    int64
	RunLogTailBytes int64
}

// WriteBundle builds a zip archive containing the rendered report plus size-
// capped, redacted tails of dreamer.log, its rotation backup, the background
// jobs audit log, and the most recent provider run logs. The archive is
// written atomically to destPath and the path is returned.
//
// Redaction is mandatory and non-bypassable: every byte of file content passes
// through the redactor before it enters the archive.
func WriteBundle(rep Report, in Input, redactor *analyzer.Redactor, opts BundleOptions, destPath string) (string, error) {
	if opts.LogTailBytes <= 0 {
		opts.LogTailBytes = DefaultLogTailBytes
	}
	if opts.RunLogTailBytes <= 0 {
		opts.RunLogTailBytes = DefaultRunLogTailBytes
	}
	if redactor == nil {
		var err error
		redactor, err = analyzer.NewRedactor(nil)
		if err != nil {
			return "", fmt.Errorf("build redactor: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(destPath), fsutil.DirPerms); err != nil {
		return "", fmt.Errorf("create bundle directory: %w", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// report.txt — always present, even when every log read fails.
	if err := writeZipEntry(zw, "report.txt", redact(redactor, RenderText(rep))); err != nil {
		return "", err
	}

	if in.OutputRoot != "" {
		logDir := filepath.Join(in.OutputRoot, "logging")
		for _, name := range []string{"dreamer.log", "dreamer.log.1"} {
			path := filepath.Join(logDir, name)
			content, err := tailFile(path, opts.LogTailBytes)
			if err != nil {
				continue // missing/unreadable logs are non-fatal for bundling
			}
			if err := writeZipEntry(zw, "logs/"+name, redact(redactor, content)); err != nil {
				return "", err
			}
		}

		for _, f := range recentRunLogs(in.OutputRoot, maxBundleRunLogs) {
			content, err := tailFile(f.path, opts.RunLogTailBytes)
			if err != nil {
				continue
			}
			if err := writeZipEntry(zw, f.zipName, redact(redactor, content)); err != nil {
				return "", err
			}
		}
	}

	if err := zw.Close(); err != nil {
		return "", fmt.Errorf("finalize zip: %w", err)
	}

	if err := fsutil.WriteFileAtomic(destPath, buf.Bytes(), fsutil.FilePerms); err != nil {
		return "", fmt.Errorf("write bundle %q: %w", destPath, err)
	}
	return destPath, nil
}

// writeZipEntry adds one text file to the archive.
func writeZipEntry(zw *zip.Writer, name, content string) error {
	entry, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("create zip entry %q: %w", name, err)
	}
	if _, err := entry.Write([]byte(content)); err != nil {
		return fmt.Errorf("write zip entry %q: %w", name, err)
	}
	return nil
}

// redact applies the redaction pipeline to content. On a nil redactor it
// returns content unchanged; callers construct a fallback redactor first,
// so this only guards direct test usage.
func redact(r *analyzer.Redactor, content string) string {
	if r == nil {
		return content
	}
	out, _ := r.Redact(content)
	return out
}

// tailFile returns at most maxBytes from the end of path, starting on a
// complete line. Returns an error when the file cannot be read.
func tailFile(path string, maxBytes int64) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes {
		data = data[int64(len(data))-maxBytes:]
		// Drop the partial first line so the tail starts clean.
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			data = data[idx+1:]
		}
	}
	return string(data), nil
}

// runLogFile is one candidate run log for inclusion in a bundle.
type runLogFile struct {
	path    string
	zipName string
	modTime time.Time
}

// recentRunLogs finds the most recent .log files under
// <outputRoot>/background-jobs/runs/<jobID>/*.log, most recently modified
// first, capped at limit entries.
func recentRunLogs(outputRoot string, limit int) []runLogFile {
	runsDir := filepath.Join(outputRoot, "background-jobs", "runs")
	jobEntries, err := os.ReadDir(runsDir)
	if err != nil {
		return nil
	}

	var candidates []runLogFile
	for _, jobEntry := range jobEntries {
		if !jobEntry.IsDir() {
			continue
		}
		runEntries, err := os.ReadDir(filepath.Join(runsDir, jobEntry.Name()))
		if err != nil {
			continue
		}
		for _, runEntry := range runEntries {
			if runEntry.IsDir() || filepath.Ext(runEntry.Name()) != ".log" {
				continue
			}
			path := filepath.Join(runsDir, jobEntry.Name(), runEntry.Name())
			info, err := runEntry.Info()
			if err != nil {
				continue
			}
			candidates = append(candidates, runLogFile{
				path:    path,
				zipName: fmt.Sprintf("runs/%s/%s", jobEntry.Name(), runEntry.Name()),
				modTime: info.ModTime(),
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}
