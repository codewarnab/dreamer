// Package flagutil provides helpers for manipulating CLI flag slices
// (e.g. constructing provider command lines with conditional flags).
//
// Every helper handles both the space-separated form (`--flag value`) and
// the equals form (`--flag=value`) — providers may emit either, and the
// flag rewrites have to survive the difference. Helpers return a new slice
// (or the same slice when nothing changed); they do not mutate the input.
package flagutil

import "strings"

// Phase2MCPTools is the tool list for CLI-based Phase 2 MCP transport.
// Includes Bash so the model can invoke `dreamer record-finding`.
// Used by Claude-family CLI providers in MCP mode.
var Phase2MCPTools = []string{"Read", "Grep", "Glob", "Bash"}

// InjectMCPFlags applies the standard MCP-mode mutation to a Claude-family
// provider command slice: removes --bare (MCP auto-discovery must be active),
// injects --dangerously-skip-permissions, --tools, --allowed-tools, and
// --mcp-config. Returns the modified slice.
//
// configFilePath is the path to a temp file containing the MCP server
// configuration JSON (written by mcpserver.BuildClientLaunchSpec).
// Both Claude-family CLIs support file-path mode — it's actually the
// primary mode; inline JSON is the secondary "parse-first" path.
// Using a file avoids Windows CreateProcess backslash-mangling.
func InjectMCPFlags(command []string, toolNames []string, configFilePath string, validateFn func() error) ([]string, error) {
	if validateFn != nil {
		if err := validateFn(); err != nil {
			return nil, err
		}
	}
	toolList := strings.Join(toolNames, ",")

	// Remove --bare so MCP auto-discovery loads the configured servers.
	command = RemoveFlag(command, "--bare")
	// Remove --strict-mcp-config if present (incompatible with file-based MCP).
	command = RemoveFlag(command, "--strict-mcp-config")
	// Remove --permission-mode if present (sandbox replaces policy flags).
	command = RemoveFlag(command, "--permission-mode")

	// Add unrestricted mode + MCP tools.
	if !HasFlag(command, "--dangerously-skip-permissions") {
		command = append(command, "--dangerously-skip-permissions")
	}
	if len(toolNames) > 0 {
		command = append(command, "--tools", toolList)
		command = append(command, "--allowed-tools", toolList)
	}
	command = append(command, "--mcp-config", configFilePath)
	return command, nil
}

// ReplaceFlag replaces oldVal with newVal for --flag in command.
// Handles both "--flag oldVal" and "--flag=oldVal" forms.
// Returns the (possibly unchanged) slice.
func ReplaceFlag(command []string, flag, oldVal, newVal string) []string {
	out := append([]string(nil), command...)
	for i, arg := range out {
		if arg == flag && i+1 < len(out) && out[i+1] == oldVal {
			out[i+1] = newVal
			return out
		}
		if strings.HasPrefix(arg, flag+"=") {
			suffix := strings.TrimPrefix(arg, flag+"=")
			if suffix == oldVal {
				out[i] = flag + "=" + newVal
				return out
			}
		}
	}
	return out
}

// booleanFlags lists flags that never take a separate value argument.
// RemoveFlag uses this to avoid consuming the next token as a value when
// the flag is boolean. Trade-off: new boolean flags added to any CLI tool
// must be added here or RemoveFlag will incorrectly consume the next token.
var booleanFlags = map[string]bool{
	"--bare":                        true,
	"--no-session-persistence":      true,
	"--verbose":                     true,
	"--dangerously-skip-permissions": true,
	"--yolo":                        true,
	"--strict-mcp-config":           true,
	"-p":                            true,
}

// RemoveFlag removes every occurrence of a flag and its value from command.
// Handles both "--flag value" (space form) and "--flag=value" (equals form).
// For non-value flags, the next token is consumed as the value. For known
// boolean flags (see booleanFlags), only the flag itself is removed.
// Leading-dash values like "-1" survive. Returns the (possibly unchanged) slice.
func RemoveFlag(command []string, flag string) []string {
	var out []string
	for i := 0; i < len(command); i++ {
		if command[i] == flag {
			// Space form: skip the flag. Only consume the next token as
			// a value if the flag is NOT a known boolean flag.
			if i+1 < len(command) && !booleanFlags[flag] {
				i++
			}
			continue
		}
		if strings.HasPrefix(command[i], flag+"=") {
			// Equals form: skip the entire "flag=value" token.
			continue
		}
		out = append(out, command[i])
	}
	return out
}

// HasFlag reports whether flag is present in command.
func HasFlag(command []string, flag string) bool {
	for _, arg := range command {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}
