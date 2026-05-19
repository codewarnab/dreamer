package cmd

import (
	"os"
	"runtime"
	"testing"
)

func TestDaemonSignalsIncludesSIGTERMOnUnix(t *testing.T) {
	signals := daemonSignals()

	if len(signals) == 0 {
		t.Fatal("daemonSignals returned empty slice")
	}

	// os.Interrupt should always be present.
	foundInterrupt := false
	for _, s := range signals {
		if s == os.Interrupt {
			foundInterrupt = true
		}
	}
	if !foundInterrupt {
		t.Fatal("daemonSignals should include os.Interrupt")
	}

	if runtime.GOOS == "windows" {
		if len(signals) != 1 {
			t.Fatalf("Windows: expected 1 signal, got %d", len(signals))
		}
	} else {
		if len(signals) != 2 {
			t.Fatalf("Unix: expected 2 signals (SIGINT+SIGTERM), got %d", len(signals))
		}
	}
}
