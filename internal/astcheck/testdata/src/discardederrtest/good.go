package discardederrtest

import (
	"fmt"
	"os"
	"strconv"
)

// good handles or legitimately ignores errors — should produce zero diagnostics.
func good(f *os.File) {
	// Error checked, not discarded.
	n, err := strconv.Atoi("5")
	if err != nil {
		return
	}
	_ = n

	// Callee not on the curated list — best-effort writes are fine to ignore.
	_, _ = fmt.Fprintf(f, "hi")

	// Deferred Close discard is idiomatic and not curated.
	defer func() { _ = f.Close() }()
}
