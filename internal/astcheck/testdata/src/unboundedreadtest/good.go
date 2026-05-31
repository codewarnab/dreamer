package unboundedreadtest

import (
	"bytes"
	"io"
	"net/http"
	"os"
)

func goodLimitedStdin() {
	_, _ = io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
}

func goodLimitedBody(r *http.Request) {
	_, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
}

// Reading from an in-memory buffer is bounded by definition — not flagged.
func goodBuffer(b *bytes.Buffer) {
	_, _ = io.ReadAll(b)
}
