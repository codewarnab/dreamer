package transport

import (
	"bufio"
	"io"
)

// ScannerInitialBuf is the initial buffer size for line-oriented
// stdout/stderr scanners in provider transports and file parsers.
// 64 KiB handles typical JSON-RPC lines; the max grows on demand.
const ScannerInitialBuf = 1 << 16 // 64 KiB

// ScannerMaxBuf is the hard ceiling for a single scanner token in
// provider transports. 16 MiB accommodates the largest known ACP/CLI
// response envelope.
const ScannerMaxBuf = 1 << 24 // 16 MiB

// FileScannerMaxBuf is the ceiling for scanner tokens when parsing
// structured files (todos.md, Go source). 1 MiB is sufficient for
// individual lines in these formats.
const FileScannerMaxBuf = 1 << 20 // 1 MiB

// NewScanner returns a *bufio.Scanner configured with the standard
// provider transport buffer sizes.
func NewScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, ScannerInitialBuf), ScannerMaxBuf)
	return sc
}

// NewFileScanner returns a *bufio.Scanner configured with file-parsing
// buffer sizes (smaller max than provider transports).
func NewFileScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, ScannerInitialBuf), FileScannerMaxBuf)
	return sc
}
