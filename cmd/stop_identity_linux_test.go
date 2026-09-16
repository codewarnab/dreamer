//go:build linux

package cmd

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"dreamer/internal/fsutil"
)

func TestVerifyDreamerProcessAcceptsCurrentExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDreamerProcess(os.Getpid(), executable); err != nil {
		t.Fatalf("verify current Dreamer process: %v", err)
	}
}

func TestPortFallbackRejectsUnrelatedHealthServerAndLeavesItAlive(t *testing.T) {
	original, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "unrelated-health-server")
	copyExecutable(t, original, copyPath)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	process := exec.Command(copyPath, "-test.run=TestUnrelatedHealthServerHelper")
	process.Env = append(os.Environ(), "DREAMER_TEST_HEALTH_PORT="+fmt.Sprint(port))
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = process.Process.Kill()
		_, _ = process.Process.Wait()
	})

	url := fmt.Sprintf("http://127.0.0.1:%d/api/health", port)
	deadline := time.Now().Add(5 * time.Second)
	for probeHealth(url, 100*time.Millisecond) != nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := probeHealth(url, 100*time.Millisecond); err != nil {
		t.Fatalf("helper health server did not start: %v", err)
	}

	if err := verifyDreamerProcess(process.Process.Pid, original); err == nil {
		t.Fatal("unrelated health server passed Dreamer executable verification")
	}
	if !fsutil.IsProcessAlive(process.Process.Pid) {
		t.Fatal("identity rejection terminated the unrelated server")
	}
}

func TestWaitForProcessExitReportsSurvivor(t *testing.T) {
	if err := waitForProcessExit(os.Getpid(), 20*time.Millisecond); err == nil {
		t.Fatal("waitForProcessExit reported success for a live process")
	}
}

func TestUnrelatedHealthServerHelper(t *testing.T) {
	port := os.Getenv("DREAMER_TEST_HEALTH_PORT")
	if port == "" {
		return
	}
	http.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	if err := http.ListenAndServe("127.0.0.1:"+port, nil); err != nil {
		os.Exit(2)
	}
}

func copyExecutable(t *testing.T, source, destination string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
