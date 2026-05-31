package discardederrtest

import (
	"encoding/json"
	"os"
	"strconv"
	"time"
)

func badPackageFuncs() {
	_, _ = time.ParseDuration("5s") // want "error from time.ParseDuration discarded"
	_, _ = strconv.Atoi("5")        // want "error from strconv.Atoi discarded"
	_, _ = json.Marshal(struct{}{}) // want "error from encoding/json.Marshal discarded"
}

func badIgnoredErrPosition() {
	// Value kept, error dropped — still a bug for a curated callee.
	n, _ := strconv.Atoi("5") // want "error from strconv.Atoi discarded"
	_ = n
}

func badMethod(f *os.File) {
	_ = f.Sync() // want "error from \\(\\*os.File\\).Sync discarded"
}
