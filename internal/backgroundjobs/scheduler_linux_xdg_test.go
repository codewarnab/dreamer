//go:build linux

package backgroundjobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
)

// unsetEnv removes key for the duration of the test, restoring any prior
// value afterwards. t.Setenv alone cannot express "unset", so when a value
// exists it is re-set to itself first, registering restoration, and the key
// is then unset for the test body.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	if orig, had := os.LookupEnv(key); had {
		t.Setenv(key, orig)
	}
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unsetenv %s: %v", key, err)
	}
}

// The systemd user unit directory must honor XDG_CONFIG_HOME: systemd reads
// user units from $XDG_CONFIG_HOME/systemd/user and only falls back to
// ~/.config/systemd/user when XDG_CONFIG_HOME is unset or invalid (empty or
// relative). Writing units anywhere else installs jobs systemd never runs.
func TestUnitDir_XDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	wantXDG := filepath.Join(xdg, "systemd", "user")
	wantHome := filepath.Join(home, ".config", "systemd", "user")

	t.Run("absolute XDG_CONFIG_HOME is honored", func(t *testing.T) {
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", xdg)
		s := &linuxScheduler{logger: logging.Silent()}
		got, err := s.unitDir()
		if err != nil {
			t.Fatalf("unitDir: %v", err)
		}
		if got != wantXDG {
			t.Errorf("unitDir = %q, want %q", got, wantXDG)
		}
	})

	t.Run("trailing separator is cleaned", func(t *testing.T) {
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", xdg+string(os.PathSeparator))
		s := &linuxScheduler{logger: logging.Silent()}
		got, err := s.unitDir()
		if err != nil {
			t.Fatalf("unitDir: %v", err)
		}
		if got != wantXDG {
			t.Errorf("unitDir = %q, want %q", got, wantXDG)
		}
	})

	t.Run("unset falls back to ~/.config", func(t *testing.T) {
		t.Setenv("HOME", home)
		unsetEnv(t, "XDG_CONFIG_HOME")
		s := &linuxScheduler{logger: logging.Silent()}
		got, err := s.unitDir()
		if err != nil {
			t.Fatalf("unitDir: %v", err)
		}
		if got != wantHome {
			t.Errorf("unitDir = %q, want %q", got, wantHome)
		}
	})

	t.Run("empty falls back to ~/.config", func(t *testing.T) {
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		s := &linuxScheduler{logger: logging.Silent()}
		got, err := s.unitDir()
		if err != nil {
			t.Fatalf("unitDir: %v", err)
		}
		if got != wantHome {
			t.Errorf("unitDir = %q, want %q", got, wantHome)
		}
	})

	t.Run("whitespace-only falls back to ~/.config", func(t *testing.T) {
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "   ")
		s := &linuxScheduler{logger: logging.Silent()}
		got, err := s.unitDir()
		if err != nil {
			t.Fatalf("unitDir: %v", err)
		}
		if got != wantHome {
			t.Errorf("unitDir = %q, want %q", got, wantHome)
		}
	})

	t.Run("relative falls back to ~/.config", func(t *testing.T) {
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "relative/config")
		s := &linuxScheduler{logger: logging.Silent()}
		got, err := s.unitDir()
		if err != nil {
			t.Fatalf("unitDir: %v", err)
		}
		if got != wantHome {
			t.Errorf("unitDir = %q, want %q (systemd ignores relative XDG_CONFIG_HOME)", got, wantHome)
		}
	})
}

func TestEnsureUnitDir_CreatesUnderXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	s := &linuxScheduler{logger: logging.Silent()}
	dir, err := s.ensureUnitDir()
	if err != nil {
		t.Fatalf("ensureUnitDir: %v", err)
	}
	want := filepath.Join(xdg, "systemd", "user")
	if dir != want {
		t.Errorf("ensureUnitDir = %q, want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("unit dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("%q is not a directory", dir)
	}
	if info.Mode().Perm() != fsutil.DirPerms {
		t.Errorf("unit dir perms = %o, want %o", info.Mode().Perm(), fsutil.DirPerms)
	}
	// Nothing may be created under the ~/.config fallback location.
	if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user")); !os.IsNotExist(err) {
		t.Errorf("fallback unit dir under HOME should not exist, stat err = %v", err)
	}
}

func TestInstall_WritesUnitsUnderXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	var calls [][]string
	s := &linuxScheduler{
		cfg: SchedulerConfig{
			StoreDir:       t.TempDir(),
			ExecutablePath: "/usr/local/bin/dreamer",
			ConfigPath:     filepath.Join(xdg, "dreamer", "config.yaml"),
		},
		logger: logging.Silent(),
		runCmd: func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, append([]string{name}, args...))
			return nil, nil
		},
	}

	params := ScheduleParams{
		JobID:    "aabbccdd11223344",
		Schedule: ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "09:00", Timezone: "UTC"},
		Name:     "Test Job",
		Enabled:  true,
	}
	state, err := s.Install(context.Background(), params)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if state.ScheduleID != "dreamer-job-aabbccdd11223344" {
		t.Errorf("ScheduleID = %q", state.ScheduleID)
	}

	unitDir := filepath.Join(xdg, "systemd", "user")
	timer, err := os.ReadFile(filepath.Join(unitDir, "dreamer-job-aabbccdd11223344.timer"))
	if err != nil {
		t.Fatalf("timer unit not written under XDG_CONFIG_HOME: %v", err)
	}
	if !strings.Contains(string(timer), "OnCalendar=*-*-* 09:00:00") {
		t.Errorf("timer unit missing OnCalendar, got:\n%s", timer)
	}
	service, err := os.ReadFile(filepath.Join(unitDir, "dreamer-job-aabbccdd11223344.service"))
	if err != nil {
		t.Fatalf("service unit not written under XDG_CONFIG_HOME: %v", err)
	}
	if !strings.Contains(string(service), "ExecStart=") {
		t.Errorf("service unit missing ExecStart, got:\n%s", service)
	}

	// Nothing may be written under the ~/.config fallback location.
	if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user")); !os.IsNotExist(err) {
		t.Errorf("fallback unit dir under HOME should not exist, stat err = %v", err)
	}

	// systemctl must have been reloaded and the timer enabled.
	joined := make([]string, len(calls))
	for i, c := range calls {
		joined[i] = strings.Join(c, " ")
	}
	all := strings.Join(joined, "\n")
	if !strings.Contains(all, "daemon-reload") {
		t.Errorf("expected daemon-reload call, got:\n%s", all)
	}
	if !strings.Contains(all, "enable --now dreamer-job-aabbccdd11223344.timer") {
		t.Errorf("expected enable --now call, got:\n%s", all)
	}
}

func TestRemove_DeletesUnitsUnderXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	unitDir := filepath.Join(xdg, "systemd", "user")
	if err := os.MkdirAll(unitDir, fsutil.DirPerms); err != nil {
		t.Fatal(err)
	}
	timerPath := filepath.Join(unitDir, "dreamer-job-aabbccdd11223344.timer")
	servicePath := filepath.Join(unitDir, "dreamer-job-aabbccdd11223344.service")
	for _, p := range []string{timerPath, servicePath} {
		if err := os.WriteFile(p, []byte("unit"), fsutil.FilePerms); err != nil {
			t.Fatal(err)
		}
	}
	// Decoy units under the fallback dir must be left untouched.
	fallbackDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(fallbackDir, fsutil.DirPerms); err != nil {
		t.Fatal(err)
	}
	decoyPath := filepath.Join(fallbackDir, "dreamer-job-aabbccdd11223344.timer")
	if err := os.WriteFile(decoyPath, []byte("decoy"), fsutil.FilePerms); err != nil {
		t.Fatal(err)
	}

	var calls [][]string
	s := &linuxScheduler{
		logger: logging.Silent(),
		runCmd: func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, append([]string{name}, args...))
			return nil, nil
		},
	}
	if err := s.Remove(context.Background(), "aabbccdd11223344"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, p := range []string{timerPath, servicePath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("unit file %q should be removed, stat err = %v", p, err)
		}
	}
	if _, err := os.Stat(decoyPath); err != nil {
		t.Errorf("decoy unit under fallback dir must be untouched: %v", err)
	}
}

func TestListOwn_ReadsFromXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	unitDir := filepath.Join(xdg, "systemd", "user")
	if err := os.MkdirAll(unitDir, fsutil.DirPerms); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"dreamer-job-aabbccdd11223344.timer",
		"dreamer-job-aabbccdd11223344.service",
		"dreamer-job-0011223344556677.timer",
		"dreamer-job-nothexnothex1.timer", // invalid ID: skipped
		"other-app.timer",
	} {
		if err := os.WriteFile(filepath.Join(unitDir, name), []byte("unit"), fsutil.FilePerms); err != nil {
			t.Fatal(err)
		}
	}
	// A valid unit under the fallback dir must not be listed.
	fallbackDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(fallbackDir, fsutil.DirPerms); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fallbackDir, "dreamer-job-ffeeddccbbaa9988.timer"), []byte("decoy"), fsutil.FilePerms); err != nil {
		t.Fatal(err)
	}

	s := &linuxScheduler{logger: logging.Silent()}
	ids, err := s.ListOwn(context.Background())
	if err != nil {
		t.Fatalf("ListOwn: %v", err)
	}
	sort.Strings(ids)
	want := []string{"0011223344556677", "aabbccdd11223344"}
	if len(ids) != len(want) {
		t.Fatalf("ListOwn = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ListOwn = %v, want %v", ids, want)
		}
	}
}

func TestInspect_DetectsUnitUnderXDGConfigHome(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	unitDir := filepath.Join(xdg, "systemd", "user")
	if err := os.MkdirAll(unitDir, fsutil.DirPerms); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unitDir, "dreamer-job-aabbccdd11223344.timer"), []byte("unit"), fsutil.FilePerms); err != nil {
		t.Fatal(err)
	}

	// systemctl reports the timer as not active; Inspect must fall back to
	// checking the unit file under XDG_CONFIG_HOME.
	s := &linuxScheduler{
		logger: logging.Silent(),
		runCmd: func(_ context.Context, name string, args ...string) ([]byte, error) {
			return []byte("inactive"), errors.New("exit status 3")
		},
	}
	health, err := s.Inspect(context.Background(), "aabbccdd11223344")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !health.Installed {
		t.Errorf("Inspect should find the unit file under XDG_CONFIG_HOME, got %+v", health)
	}
	if health.Enabled {
		t.Errorf("inactive timer should not be Enabled, got %+v", health)
	}

	// With no unit file present anywhere, the job is not installed.
	other := &linuxScheduler{
		logger: logging.Silent(),
		runCmd: func(_ context.Context, name string, args ...string) ([]byte, error) {
			return []byte("inactive"), errors.New("exit status 3")
		},
	}
	health, err = other.Inspect(context.Background(), "0011223344556677")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if health.Installed {
		t.Errorf("missing unit should report Installed=false, got %+v", health)
	}
}
