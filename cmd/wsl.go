package cmd

import (
	"github.com/spf13/cobra"

	"dreamer/internal/logging"
	"dreamer/internal/sysinfo"
)

// warnIfWSLInteropWorkspace warns when dreamer runs inside WSL but targets a
// workspace on the Windows filesystem (/mnt/<drive>/...): git operations and
// file scans are dramatically slower across the 9P boundary, and provider
// CLIs installed on the Windows side cannot operate on these paths.
func warnIfWSLInteropWorkspace(cmd *cobra.Command, logger *logging.Logger, projectPath string) {
	if !sysinfo.IsWSL() || !sysinfo.IsWindowsInteropPath(projectPath) {
		return
	}
	cmd.Printf(
		"WARNING: workspace %q is on the Windows filesystem via WSL interop (/mnt/...). "+
			"Analysis and git operations will be slow, and provider CLIs installed on the "+
			"Windows side cannot see this path — prefer keeping the repository inside WSL.\n",
		projectPath)
	logger.Warn("wsl interop workspace",
		logging.Any("path", projectPath))
}
