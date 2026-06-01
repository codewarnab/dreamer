package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
)

// NodeJSExtraDirs returns additional writable directories that Node.js
// processes need to function correctly under a restricted sandbox. Node.js
// (V8 engine, npm, libuv) writes to cache directories, temp paths, and
// package install locations that aren't covered by the standard
// os.TempDir() + configDir writable list.
//
// On Windows under a WRITE_RESTRICTED token, missing these dirs causes
// STATUS_HEAP_CORRUPTION (0xc0000374) when V8 tries to memory-map files
// in denied directories.
//
// Not all npm-distributed CLIs need this — Codex CLI is a Rust binary,
// Kiro CLI is a native binary. Only call this for Node.js-based CLIs
// (openclaude, claude, gemini, copilot, codebuff).
func NodeJSExtraDirs() []string {
	var dirs []string

	switch runtime.GOOS {
	case "windows":
		// npm cache: %APPDATA%/npm-cache
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			dirs = append(dirs, filepath.Join(appdata, "npm-cache"))
		}
		// LocalAppData temp: %LOCALAPPDATA%/Temp
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			dirs = append(dirs, filepath.Join(localAppData, "Temp"))
		}
	default:
		// npm cache: ~/.npm
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(home, ".npm"))
		}
	}

	return dirs
}
