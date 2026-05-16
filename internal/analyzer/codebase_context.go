package analyzer

import (
	"fmt"

	"dreamer/internal/analyzer/grounding"
	"dreamer/internal/analyzer/toolchain"
)

// BuildCodebaseContext assembles the grounding markdown blob that orchestrator
// prompts use for repo-aware analysis. The returned string may be empty when
// the project is too small or the toolchain produces no symbols.
func BuildCodebaseContext(projectRoot string, _ toolchain.Toolchain) (string, error) {
	files, err := grounding.DetectFiles(projectRoot, grounding.DefaultFileCap)
	if err != nil {
		return "", fmt.Errorf("detect files: %w", err)
	}
	symbols := grounding.BuildSymbolIndex(projectRoot, files, 800)
	return grounding.BuildContext(files, symbols, grounding.DefaultFileCap, 800), nil
}
