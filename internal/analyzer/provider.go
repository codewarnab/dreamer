package analyzer

import (
	"context"
	"errors"
	"time"
)

// ErrNilContext is the shared sentinel returned by every provider when a
// caller passes ctx == nil to Start, NewSession, or Session.Run. Silent
// substitution with context.Background re-anchors the call outside the
// caller's cancellation tree, so the daemon's Ctrl-C and per-rule timeouts
// stop propagating to in-flight provider work (B16).
var ErrNilContext = errors.New("nil context")

type Provider interface {
	ID() string
	Start(ctx context.Context) error
	NewSession(ctx context.Context, sessionConfig SessionConfig) (Session, error)
	Close() error
}

type SessionConfig struct {
	WorkingDirectory string
	Model            string
	ReadOnly         bool
	SystemMessage    string

	// Phase2Mode configures how Phase 2 findings are recorded.
	// Empty = JSON parsing fallback (default).
	// "mcp" = MCP tool-based recording (openclaude/claude).
	// "cli" = CLI tool via Bash (gemini).
	Phase2Mode string

	// MCPConfig is the inline JSON for --mcp-config (openclaude/claude only).
	MCPConfig string

	// MCPTools is the comma-separated list of MCP tool names to add to --tools.
	MCPTools string

	// MCPAllowedTools is the comma-separated list of MCP tool names to add to --allowed-tools.
	MCPAllowedTools string

	// CLIToolPath is the absolute path to the dreamer binary for Bash-based
	// Phase 2 (gemini only). The model calls: echo '<json>' | <CLIToolPath> record-finding --output <path>
	CLIToolPath string

	// FindingsOutputPath is the temp file where tool-based Phase 2 writes findings.
	// Passed through so the prompt can reference the actual output path.
	FindingsOutputPath string
}

type Session interface {
	Run(ctx context.Context, prompt string, timeout time.Duration) (string, error)
	Close() error
}

type PermissionKind string

const (
	PermissionKindRead       PermissionKind = "read"
	PermissionKindURL        PermissionKind = "url"
	PermissionKindShell      PermissionKind = "shell"
	PermissionKindMCPTool    PermissionKind = "mcp"
	PermissionKindCustomTool PermissionKind = "custom"
)

type ShellCommand struct {
	Identifier string
	ReadOnly   bool
}

type PermissionRequest struct {
	Kind                    PermissionKind
	Path                    *string
	PossiblePaths           []string
	ReadOnly                *bool
	Commands                []ShellCommand
	HasWriteFileRedirection *bool
	// FullCommandText carries the raw shell command line as classified by the
	// upstream provider, when available. Used by shellRequestReadOnly to run a
	// second-pass deny check against known write idioms the SDK may miss.
	FullCommandText *string
}

type PermissionDecision struct {
	Approved bool
	Reason   string
}
