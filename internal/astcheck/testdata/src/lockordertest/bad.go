package lockordertest

import (
	"io"
	"os"

	"fsutil"
)

func badReadAllBeforeLock() {
	data := readAll() // want "readAll before fsutil.AcquireLock"
	release, _ := fsutil.AcquireLock("/tmp/test.lock")
	_ = data
	_ = release
}

func badOsReadFileBeforeLock() {
	data, _ := os.ReadFile("/tmp/test.json") // want "os.ReadFile before fsutil.AcquireLock"
	release, _ := fsutil.AcquireLock("/tmp/test.lock")
	_ = data
	_ = release
}

func badOsOpenBeforeLock() {
	f, _ := os.Open("/tmp/test.json") // want "os.Open before fsutil.AcquireLock"
	release, _ := fsutil.AcquireLock("/tmp/test.lock")
	_ = f
	_ = release
}

func badIoReadAllBeforeLock() {
	data, _ := io.ReadAll(os.Stdin) // want "io.ReadAll before fsutil.AcquireLock"
	release, _ := fsutil.AcquireLock("/tmp/test.lock")
	_ = data
	_ = release
}

func readAll() []byte { return nil }
