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

// MCPTempFilePattern is the glob pattern used for MCP config temp files.
// The daemon's stale-file sweep uses this to clean up orphaned config
// files from crashed runs. Defined here (next to CreateTemp) so the
// pattern and the sweep cannot drift.
const MCPTempFilePattern = "dreamer-mcp-config-*.json"

// ClientLaunchSpec is the launch contract handed to a CLI provider when it
// needs to spawn the dreamer MCP server as a child process. It hides the
// `--mcp-config` wire format and the tool naming convention from callers —
// providers consume ConfigFilePath and ToolNames without knowing how either
// is constructed.
//
// This struct is intentionally separate from analyzer.Phase2MCPConfig
// despite carrying the same fields. The mcpserver package cannot import
// analyzer (that would create an import cycle), so the pipeline maps
// fields 1:1 at the boundary. If the packages are ever restructured to
// allow a direct dependency, these types should be collapsed.
type ClientLaunchSpec struct {
	// ConfigFilePath is the absolute path to a temp file containing the
	// MCP server configuration JSON. The provider passes this path to
	// --mcp-config instead of inline JSON, which avoids Windows
	// backslash-escaping issues in exec.Command → CreateProcess.
	ConfigFilePath string
	// ToolNames is the list of MCP tool names the model is allowed to call.
	// Currently a single-element list, but kept plural so future tools
	// (e.g. record_summary) compose without changing the API.
	ToolNames []string
}

// BuildMCPConfigJSON produces the JSON bytes for an MCP server config that
// launches dreamerBinaryPath with ["mcp-server", "--output", outputPath].
// This is a pure function — no I/O — so callers can test the JSON shape
// independently of temp file management.
func BuildMCPConfigJSON(outputPath, dreamerBinaryPath string) ([]byte, error) {
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
		return nil, fmt.Errorf("marshal mcp config: %w", err)
	}
	return encoded, nil
}

// BuildClientLaunchSpec builds the MCP config file + tool name list for a
// CLI provider that wants to register the dreamer MCP server. outputPath is
// the JSONL file the spawned server will append findings to;
// dreamerBinaryPath is the absolute path to the running dreamer binary
// (resolve via FindDreamerBinary so the child runs the same version as the
// parent).
//
// The config JSON is written to a temp file (returned as ConfigFilePath)
// rather than passed inline. Both Claude-family CLIs support file-path
// mode — it's actually the primary mode; inline JSON is the secondary
// "parse-first" path. Using a file avoids Windows CreateProcess
// backslash-mangling that breaks inline JSON with paths like
// C:\Users\....
//
// On success, the caller is responsible for scheduling os.Remove on
// ConfigFilePath after the Phase 2 session completes. On error, the
// temp file is removed internally before returning.
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

	encoded, err := BuildMCPConfigJSON(outputPath, dreamerBinaryPath)
	if err != nil {
		return ClientLaunchSpec{}, err
	}

	tmpFile, tmpErr := os.CreateTemp("", MCPTempFilePattern)
	if tmpErr != nil {
		return ClientLaunchSpec{}, fmt.Errorf("create mcp config temp file: %w", tmpErr)
	}
	configFilePath := tmpFile.Name()
	if _, writeErr := tmpFile.Write(encoded); writeErr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(configFilePath)
		return ClientLaunchSpec{}, fmt.Errorf("write mcp config temp file: %w", writeErr)
	}
	if closeErr := tmpFile.Close(); closeErr != nil {
		_ = os.Remove(configFilePath)
		return ClientLaunchSpec{}, fmt.Errorf("close mcp config temp file: %w", closeErr)
	}

	return ClientLaunchSpec{
		ConfigFilePath: configFilePath,
		ToolNames:      []string{PrefixedToolName},
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
