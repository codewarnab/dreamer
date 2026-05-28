package apply

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/categories"
	"dreamer/internal/fsutil"
	"dreamer/internal/state"
)

// MaxApplyTargetBytes is the 4 MiB cap on the pre-image stored in the
// reversal record. Targets larger than this are rejected at apply time
// per spec.v1.5 §6.5.
const MaxApplyTargetBytes = 4 << 20

// EligibleCategories lists the rule categories whose findings the UI is
// allowed to apply automatically (spec.v1.5 §6.4). Uses the canonical
// category constants from the categories package.
var EligibleCategories = map[string]bool{
	string(categories.Doc):      true,
	string(categories.LintRule): true,
	string(categories.CICheck):  true,
	string(categories.Config):   true,
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

// Request carries the inputs to one apply operation.
type Request struct {
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
func Apply(req Request) (*state.FindingReversal, error) {
	absRoot, err := filepath.EvalSymlinks(req.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root %q: %w", req.ProjectRoot, err)
	}
	abs, err := resolveTargetUnderRoot(absRoot, req.TargetFile)
	if err != nil {
		return nil, err
	}

	preImage, err := readPreImage(abs)
	if err != nil {
		return nil, err
	}

	postImage, actualStrategy, err := transform(string(preImage), req.Strategy, req.Anchor, req.Snippet)
	if err != nil {
		return nil, err
	}
	if len(postImage) > MaxApplyTargetBytes {
		return nil, fmt.Errorf("%w (post-image %d bytes)", ErrTargetTooLarge, len(postImage))
	}

	if err := os.MkdirAll(filepath.Dir(abs), fsutil.DirPerms); err != nil {
		return nil, fmt.Errorf("mkdir target parent: %w", err)
	}
	if err := fsutil.WriteFileAtomic(abs, []byte(postImage), fsutil.FilePerms); err != nil {
		return nil, fmt.Errorf("write target: %w", err)
	}

	preHash := sha256.Sum256(preImage)
	postHash := sha256.Sum256([]byte(postImage))
	return &state.FindingReversal{
		Path:            abs,
		Strategy:        actualStrategy,
		PreImageSHA256:  hex.EncodeToString(preHash[:]),
		PostImageSHA256: hex.EncodeToString(postHash[:]),
		PreImage:        string(preImage),
	}, nil
}

// Preview returns the pre- and post-image bytes that Apply would write,
// without performing any write or recording a reversal. The same
// containment, symlink, and size checks as Apply are enforced.
func Preview(req Request) (preImage []byte, postImage []byte, finalStrategy string, err error) {
	absRoot, err := filepath.EvalSymlinks(req.ProjectRoot)
	if err != nil {
		return nil, nil, "", fmt.Errorf("resolve project root %q: %w", req.ProjectRoot, err)
	}
	abs, err := resolveTargetUnderRoot(absRoot, req.TargetFile)
	if err != nil {
		return nil, nil, "", err
	}
	preImage, err = readPreImage(abs)
	if err != nil {
		return nil, nil, "", err
	}
	postStr, finalStrategy, err := transform(string(preImage), req.Strategy, req.Anchor, req.Snippet)
	if err != nil {
		return nil, nil, "", err
	}
	return preImage, []byte(postStr), finalStrategy, nil
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
	// Re-evaluate symlinks on rev.Path so a parent dir swapped into a
	// symlink between apply and undo still gets caught by containment.
	cleanPath, resolveErr := filepath.EvalSymlinks(rev.Path)
	if resolveErr != nil {
		return fmt.Errorf("resolve reversal path %q: %w", rev.Path, resolveErr)
	}
	if !fsutil.PathWithinRoot(cleanPath, absRoot) {
		return fmt.Errorf("%w: reversal path %s", ErrContainment, rev.Path)
	}
	current, err := os.ReadFile(cleanPath)
	if err != nil {
		return fmt.Errorf("read current target: %w", err)
	}
	curHash := sha256.Sum256(current)
	if hex.EncodeToString(curHash[:]) != rev.PostImageSHA256 {
		return ErrTargetChanged
	}
	return fsutil.WriteFileAtomic(cleanPath, []byte(rev.PreImage), fsutil.FilePerms)
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
		// Only promote to replace-section when the header exists as a
		// complete section heading (not a prefix of a longer heading).
		if findHeader(pre, header) >= 0 {
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

// findHeader returns the byte offset of a markdown header in pre, or -1 if
// not found. It matches only at line boundaries and requires the header to
// be followed by a newline, space, tab, or EOF — so "## Cache" does not
// match "## CacheBackend".
func findHeader(pre, header string) int {
	needle := "\n" + header
	for start := 0; ; {
		idx := strings.Index(pre[start:], needle)
		if idx < 0 {
			break
		}
		abs := start + idx + 1 // skip leading newline
		end := abs + len(header)
		if end >= len(pre) || pre[end] == '\n' || pre[end] == ' ' || pre[end] == '\t' {
			return abs
		}
		start = end
	}
	if strings.HasPrefix(pre, header) {
		end := len(header)
		if end >= len(pre) || pre[end] == '\n' || pre[end] == ' ' || pre[end] == '\t' {
			return 0
		}
	}
	return -1
}

func replaceSection(pre, anchor, snippet string) (string, error) {
	header := "## " + anchor
	idx := findHeader(pre, header)
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

// resolveTargetUnderRoot resolves req.TargetFile against absRoot and
// returns the absolute target path, refusing absolute inputs and any
// path that escapes absRoot via parent-dir symlinks. Symlinks on every
// existing ancestor are walked so a parent symlink that points outside
// the root cannot be used to write through it.
func resolveTargetUnderRoot(absRoot, targetFile string) (string, error) {
	rel := filepath.Clean(targetFile)
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: target_file must be repo-relative, got %q", ErrContainment, targetFile)
	}
	abs := filepath.Join(absRoot, rel)
	resolved, err := fsutil.ResolveSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve target %q: %w", abs, err)
	}
	if !fsutil.PathWithinRoot(resolved, absRoot) {
		return "", fmt.Errorf("%w: %s", ErrContainment, resolved)
	}
	return resolved, nil
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
