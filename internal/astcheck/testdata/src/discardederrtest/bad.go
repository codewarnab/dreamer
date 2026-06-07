package discardederrtest

import (
	"encoding/json"
	"os"
	"os/exec"
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

func badExecCmd(cmd *exec.Cmd) {
	_ = cmd.Run()       // want "error from \\(\\*os/exec.Cmd\\).Run discarded"
	_, _ = cmd.Output() // want "error from \\(\\*os/exec.Cmd\\).Output discarded"
	_ = cmd.Wait()      // want "error from \\(\\*os/exec.Cmd\\).Wait discarded"
}
