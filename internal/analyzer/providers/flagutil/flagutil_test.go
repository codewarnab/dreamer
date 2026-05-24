package flagutil

import (
	"fmt"
	"reflect"
	"testing"
)

func TestReplaceFlag(t *testing.T) {
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

func TestAppendToFlag(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		flag, value string
		want        []string
	}{
		{
			name: "space form append to non-empty",
			args: []string{"gemini", "--tools", "Read,Grep"},
			flag: "--tools", value: "Bash",
			want: []string{"gemini", "--tools", "Read,Grep,Bash"},
		},
		{
			name: "equals form append to non-empty",
			args: []string{"gemini", "--tools=Read,Grep"},
			flag: "--tools", value: "Bash",
			want: []string{"gemini", "--tools=Read,Grep,Bash"},
		},
		{
			name: "empty current value: no leading comma",
			args: []string{"gemini", "--tools", ""},
			flag: "--tools", value: "Bash",
			want: []string{"gemini", "--tools", "Bash"},
		},
		{
			name: "equals form empty value: no leading comma",
			args: []string{"gemini", "--tools="},
			flag: "--tools", value: "Bash",
			want: []string{"gemini", "--tools=Bash"},
		},
		{
			name: "empty suffix is a no-op",
			args: []string{"gemini", "--tools", "Read"},
			flag: "--tools", value: "",
			want: []string{"gemini", "--tools", "Read"},
		},
		{
			name: "missing flag is a no-op",
			args: []string{"gemini", "--add-dir", "/x"},
			flag: "--tools", value: "Bash",
			want: []string{"gemini", "--add-dir", "/x"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AppendToFlag(append([]string(nil), c.args...), c.flag, c.value)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestInjectMCPFlags(t *testing.T) {
	base := []string{"claude", "-p", "--verbose", "--output-format=stream-json", "--permission-mode", "plan", "--tools", "Read,Grep,Glob", "--bare", "--no-session-persistence"}
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
				// permission-mode changed from plan to default
				for i, a := range got {
					if a == "--permission-mode" && i+1 < len(got) {
						if got[i+1] != "default" {
							t.Errorf("permission-mode: got %q want %q", got[i+1], "default")
						}
					}
				}
				// --tools includes MCP tool name
				for i, a := range got {
					if a == "--tools" && i+1 < len(got) {
						if got[i+1] != "Read,Grep,Glob,mcp__dreamer__record_finding" {
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
			got, err := InjectMCPFlags(append([]string(nil), base...), c.toolNames, c.configPath, c.validateFn)
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

func TestRemoveFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		flag string
		want []string
	}{
		{
			name: "space form removed",
			args: []string{"gemini", "--approval-mode", "plan", "--add-dir", "/x"},
			flag: "--approval-mode",
			want: []string{"gemini", "--add-dir", "/x"},
		},
		{
			name: "equals form removed",
			args: []string{"gemini", "--approval-mode=plan", "--add-dir", "/x"},
			flag: "--approval-mode",
			want: []string{"gemini", "--add-dir", "/x"},
		},
		{
			name: "double occurrence removed",
			args: []string{"gemini", "--approval-mode", "plan", "--approval-mode", "yolo"},
			flag: "--approval-mode",
			want: []string{"gemini"},
		},
		{
			name: "value starting with dash still consumed",
			args: []string{"cmd", "--threshold", "-1", "--keep"},
			flag: "--threshold",
			want: []string{"cmd", "--keep"},
		},
		{
			name: "prefix-collision flag survives",
			args: []string{"cmd", "--approval-mode-extra", "keep", "--approval-mode", "plan"},
			flag: "--approval-mode",
			want: []string{"cmd", "--approval-mode-extra", "keep"},
		},
		{
			name: "flag absent leaves args alone",
			args: []string{"cmd", "--keep"},
			flag: "--gone",
			want: []string{"cmd", "--keep"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RemoveFlag(append([]string(nil), c.args...), c.flag)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}
