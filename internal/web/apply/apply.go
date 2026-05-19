package apply

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/fsutil"
	"dreamer/internal/state"
)

// MaxApplyTargetBytes is the 4 MiB cap on the pre-image stored in the
// reversal record. Targets larger than this are rejected at apply time
// per spec.v1.5 §6.5.
const MaxApplyTargetBytes = 4 << 20

// EligibleCategories lists the analyzer rule categories whose findings
// the UI is allowed to apply automatically (spec.v1.5 §6.4).
var EligibleCategories = map[string]bool{
	"doc":       true,
	"lint-rule": true,
	"ci-check":  true,
	"config":    true,
}

var (
	ErrContainment     = errors.New("apply target outside project root")
	ErrTargetTooLarge  = errors.New("apply target too large for safe apply (4 MiB cap)")
	ErrTargetChanged   = errors.New("apply target file changed since apply; refusing undo")
	ErrAnchorMissing   = errors.New("apply anchor not found in target file")
	ErrUnknownStrategy = errors.New("unknown apply strategy")
)

func IsTargetTooLarge(err error) bool { return errors.Is(err, ErrTargetTooLarge) }
func IsTargetChanged(err error) bool  { return errors.Is(err, ErrTargetChanged) }
func IsContainment(err error) bool    { return errors.Is(err, ErrContainment) }

// ApplyRequest carries the inputs to one apply operation.
type ApplyRequest struct {
	ProjectRoot string
	TargetFile  string // repo-relative path from the rule pack
	Strategy    string
	Anchor      string
	Snippet     string
}

// Apply resolves TargetFile against ProjectRoot (symlink-aware,
// containment-checked), reads the existing contents, applies the
// strategy, atomically writes the result, and returns a FindingReversal
// capable of undoing the write.
func Apply(req ApplyRequest) (*state.FindingReversal, error) {
	absRoot, err := filepath.EvalSymlinks(req.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root %q: %w", req.ProjectRoot, err)
	}
	rel := filepath.Clean(req.TargetFile)
	if filepath.IsAbs(rel) {
		return nil, fmt.Errorf("%w: target_file must be repo-relative, got %q", ErrContainment, req.TargetFile)
	}
	abs := filepath.Join(absRoot, rel)
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("resolve target %q: %w", abs, err)
	}
	if !strings.HasPrefix(abs, absRoot+string(filepath.Separator)) && abs != absRoot {
		return nil, fmt.Errorf("%w: %s", ErrContainment, abs)
	}

	pre, err := readPreImage(abs)
	if err != nil {
		return nil, err
	}

	post, actualStrategy, err := transform(string(pre), req.Strategy, req.Anchor, req.Snippet)
	if err != nil {
		return nil, err
	}
	if len(post) > MaxApplyTargetBytes {
		return nil, fmt.Errorf("%w (post-image %d bytes)", ErrTargetTooLarge, len(post))
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir target parent: %w", err)
	}
	if err := fsutil.WriteFileAtomic(abs, []byte(post), 0o644); err != nil {
		return nil, fmt.Errorf("write target: %w", err)
	}

	preHash := sha256.Sum256(pre)
	postHash := sha256.Sum256([]byte(post))
	return &state.FindingReversal{
		Path:            abs,
		Strategy:        actualStrategy,
		PreImageSHA256:  hex.EncodeToString(preHash[:]),
		PostImageSHA256: hex.EncodeToString(postHash[:]),
		PreImage:        string(pre),
	}, nil
}

// Preview returns the pre- and post-image bytes that Apply would write,
// without performing any write or recording a reversal. The same
// containment, symlink, and size checks as Apply are enforced.
func Preview(req ApplyRequest) (pre []byte, post []byte, finalStrategy string, err error) {
	absRoot, err := filepath.EvalSymlinks(req.ProjectRoot)
	if err != nil {
		return nil, nil, "", fmt.Errorf("resolve project root %q: %w", req.ProjectRoot, err)
	}
	rel := filepath.Clean(req.TargetFile)
	if filepath.IsAbs(rel) {
		return nil, nil, "", fmt.Errorf("%w: target_file must be repo-relative, got %q", ErrContainment, req.TargetFile)
	}
	abs := filepath.Join(absRoot, rel)
	resolved, sErr := filepath.EvalSymlinks(abs)
	if sErr == nil {
		abs = resolved
	} else if !errors.Is(sErr, os.ErrNotExist) {
		return nil, nil, "", fmt.Errorf("resolve target %q: %w", abs, sErr)
	}
	if !strings.HasPrefix(abs, absRoot+string(filepath.Separator)) && abs != absRoot {
		return nil, nil, "", fmt.Errorf("%w: %s", ErrContainment, abs)
	}
	pre, err = readPreImage(abs)
	if err != nil {
		return nil, nil, "", err
	}
	postStr, finalStrategy, err := transform(string(pre), req.Strategy, req.Anchor, req.Snippet)
	if err != nil {
		return nil, nil, "", err
	}
	return pre, []byte(postStr), finalStrategy, nil
}

// readPreImage stat-checks and reads the target file. A missing file is
// treated as an empty pre-image (returns nil, nil).
func readPreImage(abs string) ([]byte, error) {
	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat target %q: %w", abs, err)
	}
	if info.Size() > MaxApplyTargetBytes {
		return nil, fmt.Errorf("%w (%d bytes)", ErrTargetTooLarge, info.Size())
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read target %q: %w", abs, err)
	}
	return data, nil
}

// Undo restores the file to its pre-image bytes. It refuses (with
// ErrTargetChanged) when the current file's SHA-256 does not match the
// reversal's PostImageSHA256 — i.e. an operator edited the file between
// apply and undo.
func Undo(projectRoot string, rev state.FindingReversal) error {
	absRoot, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root: %w", err)
	}
	if !strings.HasPrefix(rev.Path, absRoot+string(filepath.Separator)) && rev.Path != absRoot {
		return fmt.Errorf("%w: reversal path %s", ErrContainment, rev.Path)
	}
	current, err := os.ReadFile(rev.Path)
	if err != nil {
		return fmt.Errorf("read current target: %w", err)
	}
	curHash := sha256.Sum256(current)
	if hex.EncodeToString(curHash[:]) != rev.PostImageSHA256 {
		return ErrTargetChanged
	}
	return fsutil.WriteFileAtomic(rev.Path, []byte(rev.PreImage), 0o644)
}

func transform(pre, strategy, anchor, snippet string) (string, string, error) {
	switch strategy {
	case "append-file":
		if pre == "" {
			return snippet + "\n", strategy, nil
		}
		return pre + "\n" + snippet + "\n", strategy, nil
	case "replace-file":
		return snippet, strategy, nil
	case "append-section":
		header := "## " + anchor
		if strings.Contains(pre, header) {
			out, err := replaceSection(pre, anchor, snippet)
			return out, "replace-section", err
		}
		return pre + "\n\n" + header + "\n\n" + snippet + "\n", strategy, nil
	case "replace-section":
		out, err := replaceSection(pre, anchor, snippet)
		return out, strategy, err
	case "insert-after":
		out, err := insertAfter(pre, anchor, snippet)
		return out, strategy, err
	default:
		return "", "", fmt.Errorf("%w: %q", ErrUnknownStrategy, strategy)
	}
}

func replaceSection(pre, anchor, snippet string) (string, error) {
	header := "## " + anchor
	idx := strings.Index(pre, header)
	if idx < 0 {
		return "", fmt.Errorf("%w: anchor %q", ErrAnchorMissing, anchor)
	}
	// Find end-of-section: the next "## " heading or EOF.
	rest := pre[idx+len(header):]
	end := strings.Index(rest, "\n## ")
	var tail string
	if end >= 0 {
		tail = rest[end:] // includes leading "\n"
	} else {
		tail = ""
	}
	return pre[:idx] + header + "\n\n" + snippet + "\n" + tail, nil
}

func insertAfter(pre, anchor, snippet string) (string, error) {
	lines := strings.SplitAfter(pre, "\n")
	for i, line := range lines {
		if strings.TrimRight(line, "\n") == anchor {
			lines[i] = line + snippet + "\n"
			return strings.Join(lines, ""), nil
		}
	}
	return "", fmt.Errorf("%w: anchor %q", ErrAnchorMissing, anchor)
}
