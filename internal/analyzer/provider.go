package analyzer

import (
	"context"
	"time"
)

type Provider interface {
	ID() string
	Start(ctx context.Context) error
	NewSession(ctx context.Context, cfg SessionConfig) (Session, error)
	Close() error
}

type SessionConfig struct {
	WorkingDirectory string
	Model            string
	ReadOnly         bool
	SystemMessage    string
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
}

type PermissionDecision struct {
	Approved bool
	Reason   string
}
