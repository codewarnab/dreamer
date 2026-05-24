package analyzer

import (
	"strings"
	"testing"
)

func TestPhase2Config_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *Phase2Config
		wantErr string // substring; empty = expect nil
	}{
		{
			name:    "nil config is valid",
			cfg:     nil,
			wantErr: "",
		},
		{
			name: "valid MCP config",
			cfg: &Phase2Config{
				FindingsOutputPath: "/tmp/findings.jsonl",
				MCP: &Phase2MCPConfig{
					ConfigFilePath: "/tmp/mcp-config.json",
					ToolNames:      []string{"mcp__dreamer__record_finding"},
				},
			},
			wantErr: "",
		},
		{
			name: "valid CLI config",
			cfg: &Phase2Config{
				FindingsOutputPath: "/tmp/findings.jsonl",
				CLI: &Phase2CLIConfig{
					DreamerBinaryPath: "/usr/local/bin/dreamer",
				},
			},
			wantErr: "",
		},
		{
			name: "empty FindingsOutputPath",
			cfg: &Phase2Config{
				FindingsOutputPath: "",
				MCP: &Phase2MCPConfig{
					ConfigFilePath: "/tmp/mcp-config.json",
				},
			},
			wantErr: "FindingsOutputPath is required",
		},
		{
			name: "both MCP and CLI set",
			cfg: &Phase2Config{
				FindingsOutputPath: "/tmp/findings.jsonl",
				MCP: &Phase2MCPConfig{
					ConfigFilePath: "/tmp/mcp-config.json",
				},
				CLI: &Phase2CLIConfig{
					DreamerBinaryPath: "/usr/local/bin/dreamer",
				},
			},
			wantErr: "mutually exclusive",
		},
		{
			name: "neither MCP nor CLI set",
			cfg: &Phase2Config{
				FindingsOutputPath: "/tmp/findings.jsonl",
			},
			wantErr: "one of MCP or CLI must be set",
		},
		{
			name: "MCP with empty ConfigFilePath",
			cfg: &Phase2Config{
				FindingsOutputPath: "/tmp/findings.jsonl",
				MCP: &Phase2MCPConfig{
					ConfigFilePath: "",
				},
			},
			wantErr: "ConfigFilePath is required",
		},
		{
			name: "CLI with empty DreamerBinaryPath",
			cfg: &Phase2Config{
				FindingsOutputPath: "/tmp/findings.jsonl",
				CLI: &Phase2CLIConfig{
					DreamerBinaryPath: "",
				},
			},
			wantErr: "DreamerBinaryPath is required",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("expected nil error, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("expected error containing %q, got: %v", c.wantErr, err)
			}
		})
	}
}

func TestPhase2Config_Mode(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Phase2Config
		want Phase2Mode
	}{
		{"nil", nil, Phase2ModeNone},
		{"no transport", &Phase2Config{FindingsOutputPath: "/x"}, Phase2ModeNone},
		{"MCP set", &Phase2Config{FindingsOutputPath: "/x", MCP: &Phase2MCPConfig{ConfigFilePath: "/c"}}, Phase2ModeMCP},
		{"CLI set", &Phase2Config{FindingsOutputPath: "/x", CLI: &Phase2CLIConfig{DreamerBinaryPath: "/d"}}, Phase2ModeCLI},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.cfg.Mode()
			if got != c.want {
				t.Errorf("Mode(): got %q want %q", got, c.want)
			}
		})
	}
}
