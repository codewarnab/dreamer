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
	RunID            string // injected into marker for transcript correlation

	// Phase2 configures tool-based finding recording for the Phase 2 session.
	// Nil means JSON parsing fallback (the default and also what Phase 1
	// sessions always pass). Non-nil means the provider must wire the
	// corresponding transport (MCP stdio child, or Bash CLI tool).
	Phase2 *Phase2Config

	// Sandbox holds the resolved sandbox mode ("auto", "true", "false")
	// for this session. Providers that spawn child processes read this
	// to decide whether to apply OS-level sandboxing.
	Sandbox string
}

// Phase2Mode names the tool-based Phase 2 transport.
// Provider metadata declares one of these; the pipeline materializes the
// matching Phase2Config variant at runtime.
type Phase2Mode string

const (
	// Phase2ModeNone disables tool-based Phase 2; the model returns findings
	// inline as JSON (legacy path).
	Phase2ModeNone Phase2Mode = ""
	// Phase2ModeMCP uses an MCP stdio child (`dreamer mcp-server`) registered
	// via --mcp-config; the model calls the record_finding MCP tool.
	Phase2ModeMCP Phase2Mode = "mcp"
	// Phase2ModeCLI uses the model's Bash tool to invoke `dreamer
	// record-finding` with a heredoc-piped finding payload.
	Phase2ModeCLI Phase2Mode = "cli"
)

// Phase2Config is a sum type carrying transport-specific knobs for tool-based
// Phase 2. Exactly one of MCP or CLI must be non-nil; FindingsOutputPath is
// always required because both transports write to the same JSONL file.
// Validate before use via Validate().
type Phase2Config struct {
	// FindingsOutputPath is the temp file where the recording transport
	// writes JSONL findings. The orchestrator reads it back after the
	// Phase 2 session returns.
	FindingsOutputPath string
	MCP                *Phase2MCPConfig
	CLI                *Phase2CLIConfig
}

// Phase2MCPConfig is the launch spec for the MCP transport — produced by
// internal/mcpserver and passed through unchanged by the pipeline.
type Phase2MCPConfig struct {
	// ConfigFilePath is the absolute path to a temp file containing the
	// MCP server configuration JSON. Providers pass this path to
	// --mcp-config instead of inline JSON, avoiding Windows
	// backslash-escaping issues in exec.Command → CreateProcess.
	ConfigFilePath string
	// ToolNames is the list of MCP tool names the model is allowed to call.
	// Providers map this onto their flag naming convention (--tools vs --allowed-tools).
	ToolNames []string
}

// Phase2CLIConfig is the launch spec for the CLI tool transport.
type Phase2CLIConfig struct {
	// DreamerBinaryPath is the absolute path to the running dreamer binary.
	// The model invokes `<DreamerBinaryPath> record-finding --output <findings>`.
	DreamerBinaryPath string
}

// Mode reports which variant of Phase2Config is set. Returns Phase2ModeNone
// only when both MCP and CLI are nil.
func (p *Phase2Config) Mode() Phase2Mode {
	if p == nil {
		return Phase2ModeNone
	}
	if p.MCP != nil {
		return Phase2ModeMCP
	}
	if p.CLI != nil {
		return Phase2ModeCLI
	}
	return Phase2ModeNone
}

// Validate enforces the one-of invariant — exactly one of MCP / CLI must be
// set and FindingsOutputPath must be non-empty. Providers call this in
// NewSession before wiring the transport so misconfiguration crashes early
// at the boundary rather than producing silently-broken Phase 2 runs.
func (p *Phase2Config) Validate() error {
	if p == nil {
		return nil
	}
	if p.FindingsOutputPath == "" {
		return errors.New("Phase2Config.FindingsOutputPath is required")
	}
	hasMCP := p.MCP != nil
	hasCLI := p.CLI != nil
	switch {
	case hasMCP && hasCLI:
		return errors.New("Phase2Config: MCP and CLI are mutually exclusive")
	case !hasMCP && !hasCLI:
		return errors.New("Phase2Config: one of MCP or CLI must be set")
	case hasMCP && p.MCP.ConfigFilePath == "":
		return errors.New("Phase2MCPConfig.ConfigFilePath is required")
	case hasCLI && p.CLI.DreamerBinaryPath == "":
		return errors.New("Phase2CLIConfig.DreamerBinaryPath is required")
	}
	return nil
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
	// ApprovedIP is the pre-resolved IP address for URL approvals.
	// Callers should dial this IP directly instead of the hostname to
	// prevent DNS rebinding attacks. Empty for non-URL decisions.
	ApprovedIP string
}
