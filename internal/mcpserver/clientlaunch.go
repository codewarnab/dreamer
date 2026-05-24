package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
)

// RecordFindingToolName is the canonical name of the MCP tool the model
// calls to record a Phase 2 finding. Both the server registration and the
// client launch spec reference this constant so they cannot drift.
const RecordFindingToolName = "record_finding"

// ServerName is the MCP server name advertised to the client and embedded
// in the prefixed tool name (`mcp__<server>__<tool>`).
const ServerName = "dreamer"

// PrefixedToolName is the wire name claude-cli expects on --tools /
// --allowed-tools: `mcp__<server>__<tool>`. Pre-rendered so callers never
// have to hand-format it.
const PrefixedToolName = "mcp__" + ServerName + "__" + RecordFindingToolName

// ClientLaunchSpec is the launch contract handed to a CLI provider when it
// needs to spawn the dreamer MCP server as a child process. It hides the
// `--mcp-config` wire format and the tool naming convention from callers —
// providers consume ConfigJSON and ToolNames without knowing how either is
// constructed.
type ClientLaunchSpec struct {
	// ConfigJSON is the inline JSON for the provider's --mcp-config flag.
	ConfigJSON string
	// ToolNames is the list of MCP tool names the model is allowed to call.
	// Currently a single-element list, but kept plural so future tools
	// (e.g. record_summary) compose without changing the API.
	ToolNames []string
}

// BuildClientLaunchSpec builds the inline JSON + tool name list for a CLI
// provider that wants to register the dreamer MCP server. outputPath is the
// JSONL file the spawned server will append findings to; dreamerBinaryPath
// is the absolute path to the running dreamer binary (resolve via
// FindDreamerBinary so the child runs the same version as the parent).
//
// Returns an error if either input is empty — callers must not pass a bare
// "dreamer" on $PATH, which risks version skew or silent
// "command not found" failures in Phase 2 tool calls.
func BuildClientLaunchSpec(outputPath, dreamerBinaryPath string) (ClientLaunchSpec, error) {
	if outputPath == "" {
		return ClientLaunchSpec{}, fmt.Errorf("BuildClientLaunchSpec: outputPath is required")
	}
	if dreamerBinaryPath == "" {
		return ClientLaunchSpec{}, fmt.Errorf("BuildClientLaunchSpec: dreamerBinaryPath is required")
	}

	// Anonymous types keep the JSON shape adjacent to its single producer —
	// MCP wire format leaking into named types would invite reuse in places
	// it doesn't belong.
	type mcpCommand struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	type mcpServers struct {
		Dreamer mcpCommand `json:"dreamer"`
	}
	type mcpConfig struct {
		Servers mcpServers `json:"mcpServers"`
	}
	cfg := mcpConfig{Servers: mcpServers{Dreamer: mcpCommand{
		Command: dreamerBinaryPath,
		Args:    []string{"mcp-server", "--output", outputPath},
	}}}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		// Marshal failures on a fixed struct shape are impossible in
		// practice — return the error rather than swallowing it so a
		// future struct change surfaces loudly.
		return ClientLaunchSpec{}, fmt.Errorf("marshal mcp config: %w", err)
	}
	return ClientLaunchSpec{
		ConfigJSON: string(encoded),
		ToolNames:  []string{PrefixedToolName},
	}, nil
}

// FindDreamerBinary returns the absolute path of the running dreamer binary.
// Callers must not fall back to a bare "dreamer" on PATH because that risks
// version skew (older binary on PATH) or silent "command not found" failures
// inside Phase 2 tool calls.
func FindDreamerBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve dreamer binary: %w", err)
	}
	return exe, nil
}
