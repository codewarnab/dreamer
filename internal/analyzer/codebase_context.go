package analyzer

import (
	"fmt"

	"dreamer/internal/analyzer/grounding"
	"dreamer/internal/analyzer/toolchain"
)

// defaultSymbolCap is the max number of exported symbols to include in
// the grounding context passed to analyzer prompts.
const defaultSymbolCap = 800

// BuildCodebaseContext assembles the grounding markdown blob that orchestrator
// prompts use for repo-aware analysis. The returned string may be empty when
// the project is too small or the toolchain produces no symbols.
func BuildCodebaseContext(projectRoot string, _ toolchain.Toolchain) (string, error) {
	files, err := grounding.DetectFiles(projectRoot, grounding.DefaultFileCap)
	if err != nil {
		return "", fmt.Errorf("detect files: %w", err)
	}
	symbols := grounding.BuildSymbolIndex(projectRoot, files, defaultSymbolCap)
	return grounding.BuildContext(files, symbols, grounding.DefaultFileCap, defaultSymbolCap), nil
}

// CodebaseFiles returns the path-only file list used for phase-2 grounding.
// Files are sorted by the grounding detector (recent modifications first).
func CodebaseFiles(projectRoot string) ([]string, error) {
	files, err := grounding.DetectFiles(projectRoot, grounding.DefaultFileCap)
	if err != nil {
		return nil, fmt.Errorf("detect files: %w", err)
	}
	return files, nil
}
