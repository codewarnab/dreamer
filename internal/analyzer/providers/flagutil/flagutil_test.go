package flagutil

import (
	"fmt"
	"reflect"
	"testing"
)

func TestReplaceFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name             string
		args             []string
		flag, oldV, newV string
		want             []string
	}{
		{
			name: "space form match",
			args: []string{"claude", "--permission-mode", "plan", "--add-dir", "/x"},
			flag: "--permission-mode", oldV: "plan", newV: "default",
			want: []string{"claude", "--permission-mode", "default", "--add-dir", "/x"},
		},
		{
			name: "equals form match",
			args: []string{"claude", "--permission-mode=plan", "--add-dir", "/x"},
			flag: "--permission-mode", oldV: "plan", newV: "default",
			want: []string{"claude", "--permission-mode=default", "--add-dir", "/x"},
		},
		{
			name: "no match leaves args alone",
			args: []string{"claude", "--add-dir", "/x"},
			flag: "--permission-mode", oldV: "plan", newV: "default",
			want: []string{"claude", "--add-dir", "/x"},
		},
		{
			name: "wrong old value skipped",
			args: []string{"claude", "--permission-mode", "default"},
			flag: "--permission-mode", oldV: "plan", newV: "yolo",
			want: []string{"claude", "--permission-mode", "default"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ReplaceFlag(append([]string(nil), c.args...), c.flag, c.oldV, c.newV)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestInjectMCPFlags(t *testing.T) {
	t.Parallel()
	base := []string{"claude", "-p", "--verbose", "--output-format=stream-json", "--dangerously-skip-permissions", "--bare", "--no-session-persistence"}
	configPath := `C:\Users\test\AppData\Local\Temp\dreamer-mcp-config-abc123.json`

	cases := []struct {
		name       string
		toolNames  []string
		configPath string
		validateFn func() error
		wantErr    bool
		check      func(t *testing.T, got []string)
	}{
		{
			name:       "basic MCP injection",
			toolNames:  []string{"mcp__dreamer__record_finding"},
			configPath: configPath,
			check: func(t *testing.T, got []string) {
				// --bare should be removed (MCP auto-discovery must be active)
				for _, a := range got {
					if a == "--bare" {
						t.Error("--bare should be removed by InjectMCPFlags")
					}
				}
				// --tools includes MCP tool name
				for i, a := range got {
					if a == "--tools" && i+1 < len(got) {
						if got[i+1] != "mcp__dreamer__record_finding" {
							t.Errorf("--tools: got %q", got[i+1])
						}
					}
				}
				// --allowed-tools present
				found := false
				for i, a := range got {
					if a == "--allowed-tools" && i+1 < len(got) && got[i+1] == "mcp__dreamer__record_finding" {
						found = true
					}
				}
				if !found {
					t.Error("--allowed-tools with MCP tool name not found")
				}
				// --mcp-config with file path
				found = false
				for i, a := range got {
					if a == "--mcp-config" && i+1 < len(got) && got[i+1] == configPath {
						found = true
					}
				}
				if !found {
					t.Errorf("--mcp-config %s not found", configPath)
				}
				// Verify flags that must survive InjectMCPFlags (B6 regression).
				for _, must := range []string{"-p", "--verbose", "--no-session-persistence"} {
					survived := false
					for _, a := range got {
						if a == must {
							survived = true
							break
						}
					}
					if !survived {
						t.Errorf("expected %q to survive InjectMCPFlags, but it was removed", must)
					}
				}
			},
		},
		{
			name:       "validation error propagated",
			toolNames:  []string{"mcp__dreamer__record_finding"},
			configPath: configPath,
			validateFn: func() error { return fmt.Errorf("blocked") },
			wantErr:    true,
		},
		{
			name:       "empty tool names still adds --mcp-config",
			toolNames:  nil,
			configPath: configPath,
			check: func(t *testing.T, got []string) {
				found := false
				for i, a := range got {
					if a == "--mcp-config" && i+1 < len(got) && got[i+1] == configPath {
						found = true
					}
				}
				if !found {
					t.Errorf("--mcp-config %s not found", configPath)
				}
				// --tools should NOT be appended to (empty tool list)
				for _, a := range got {
					if a == "--allowed-tools" {
						t.Error("--allowed-tools should not be present with empty tool names")
					}
				}
			},
		},
		{
			name:       "Windows path preserved as-is in args",
			toolNames:  []string{"mcp__dreamer__record_finding"},
			configPath: `C:\Users\user name\AppData\Local\Temp\dreamer-mcp-config-xyz.json`,
			check: func(t *testing.T, got []string) {
				for i, a := range got {
					if a == "--mcp-config" && i+1 < len(got) {
						if got[i+1] != `C:\Users\user name\AppData\Local\Temp\dreamer-mcp-config-xyz.json` {
							t.Errorf("--mcp-config value mangled: got %q", got[i+1])
						}
					}
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := InjectMCPFlags(append([]string(nil), base...), c.toolNames, c.configPath, true, c.validateFn)
			if c.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.check != nil {
				c.check(t, got)
			}
		})
	}
}

func TestInjectMCPFlagsKeepsPolicyWhenUnrestrictedDisabled(t *testing.T) {
	t.Parallel()
	base := []string{"claude", "-p", "--permission-mode", "plan", "--bare"}
	got, err := InjectMCPFlags(
		append([]string(nil), base...),
		[]string{"mcp__dreamer__record_finding"},
		"config.json",
		false,
		nil,
	)
	if err != nil {
		t.Fatalf("InjectMCPFlags: %v", err)
	}
	for _, arg := range got {
		if arg == "--dangerously-skip-permissions" {
			t.Fatalf("unrestricted flag should not be added when native sandbox is disabled: %v", got)
		}
	}
	if !reflect.DeepEqual(got[:4], []string{"claude", "-p", "--permission-mode", "plan"}) {
		t.Fatalf("policy flag should survive, got %v", got)
	}
}

func TestRemoveFlag(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		args  []string
		flag  string
		arity FlagArity
		want  []string
	}{
		{
			name:  "space form removed",
			args:  []string{"gemini", "--approval-mode", "plan", "--add-dir", "/x"},
			flag:  "--approval-mode",
			arity: Valued,
			want:  []string{"gemini", "--add-dir", "/x"},
		},
		{
			name:  "equals form removed",
			args:  []string{"gemini", "--approval-mode=plan", "--add-dir", "/x"},
			flag:  "--approval-mode",
			arity: Valued,
			want:  []string{"gemini", "--add-dir", "/x"},
		},
		{
			name:  "double occurrence removed",
			args:  []string{"gemini", "--approval-mode", "plan", "--approval-mode", "yolo"},
			flag:  "--approval-mode",
			arity: Valued,
			want:  []string{"gemini"},
		},
		{
			name:  "value starting with dash still consumed",
			args:  []string{"cmd", "--threshold", "-1", "--keep"},
			flag:  "--threshold",
			arity: Valued,
			want:  []string{"cmd", "--keep"},
		},
		{
			name:  "prefix-collision flag survives",
			args:  []string{"cmd", "--approval-mode-extra", "keep", "--approval-mode", "plan"},
			flag:  "--approval-mode",
			arity: Valued,
			want:  []string{"cmd", "--approval-mode-extra", "keep"},
		},
		{
			name:  "flag absent leaves args alone",
			args:  []string{"cmd", "--keep"},
			flag:  "--gone",
			arity: Valued,
			want:  []string{"cmd", "--keep"},
		},
		{
			name:  "boolean flag does not consume next token",
			args:  []string{"claude", "--bare", "--no-session-persistence", "--verbose"},
			flag:  "--bare",
			arity: Boolean,
			want:  []string{"claude", "--no-session-persistence", "--verbose"},
		},
		{
			name:  "boolean flag --no-session-persistence does not consume next",
			args:  []string{"claude", "--no-session-persistence", "--verbose", "-p"},
			flag:  "--no-session-persistence",
			arity: Boolean,
			want:  []string{"claude", "--verbose", "-p"},
		},
		{
			name:  "boolean terminal token removed",
			args:  []string{"claude", "--strict-mcp-config"},
			flag:  "--strict-mcp-config",
			arity: Boolean,
			want:  []string{"claude"},
		},
		{
			name:  "boolean followed by dash-prefixed token does not consume next",
			args:  []string{"claude", "--strict-mcp-config", "--mcp-config", "config.json"},
			flag:  "--strict-mcp-config",
			arity: Boolean,
			want:  []string{"claude", "--mcp-config", "config.json"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RemoveFlag(append([]string(nil), c.args...), c.flag, c.arity)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestHasFlag(t *testing.T) {
	t.Parallel()
	if HasFlag([]string{"claude", "--bare", "--no-session"}, "--bare") != true {
		t.Error("expected true for present flag")
	}
	if HasFlag([]string{"claude", "--bare"}, "--verbose") != false {
		t.Error("expected false for absent flag")
	}
	if HasFlag([]string{"claude", "--output-format=stream-json"}, "--output-format") != true {
		t.Error("expected true for equals form")
	}
}
