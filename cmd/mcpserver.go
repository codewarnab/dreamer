package cmd

import (
	"fmt"

	"dreamer/internal/mcpserver"
	"github.com/spf13/cobra"
)

func newMCPServerCommand() *cobra.Command {
	var outputPath string

	command := &cobra.Command{
		Use:    "mcp-server",
		Short:  "Run MCP server for Phase 2 finding recording (stdio transport).",
		Hidden: true, // internal use only — spawned by the analyzer
		RunE: func(_ *cobra.Command, _ []string) error {
			return mcpserver.RunMCPServer(outputPath)
		},
	}

	command.Flags().StringVar(&outputPath, "output", "", "Path to write findings as JSONL (required)")
	if err := command.MarkFlagRequired("output"); err != nil {
		panic(fmt.Sprintf("mark --output required: %v", err))
	}
	return command
}
