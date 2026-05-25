package sandbox

import (
	"testing"
)

func TestParseMode(t *testing.T) {
	cases := []struct {
		input string
		want  Mode
		err   bool
	}{
		{"", ModeAuto, false},
		{"auto", ModeAuto, false},
		{"AUTO", ModeAuto, false},
		{"true", ModeOn, false},
		{"on", ModeOn, false},
		{"require", ModeOn, false},
		{"false", ModeOff, false},
		{"off", ModeOff, false},
		{"disable", ModeOff, false},
		{"maybe", "", true},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got, err := ParseMode(c.input)
			if c.err {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", c.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", c.input, err)
			}
			if got != c.want {
				t.Errorf("ParseMode(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

func TestPrepare_ModeOff_ReturnsNilOnNilCmd(t *testing.T) {
	cfg := Config{
		ProjectDir: "/tmp/test",
		Mode:       ModeOff,
	}
	// Prepare with nil cmd is fine when mode is off — it returns early.
	// (We can't test with a real cmd without starting a process.)
	cleanup, err := Prepare(nil, cfg)
	if err != nil {
		t.Fatalf("Prepare with ModeOff should return nil error, got: %v", err)
	}
	if cleanup == nil {
		t.Fatal("Prepare with ModeOff should return non-nil cleanup")
	}
	cleanup() // should be safe to call
}

func TestPostStart_ModeOff_ReturnsNilOnNilCmd(t *testing.T) {
	cfg := Config{
		ProjectDir: "/tmp/test",
		Mode:       ModeOff,
	}
	cleanup, err := PostStart(nil, cfg)
	if err != nil {
		t.Fatalf("PostStart with ModeOff should return nil error, got: %v", err)
	}
	if cleanup == nil {
		t.Fatal("PostStart with ModeOff should return non-nil cleanup")
	}
	cleanup() // should be safe to call
}

func TestBuildConfig(t *testing.T) {
	cfg, err := BuildConfig("/tmp/project", ".claude", "auto")
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if cfg.ProjectDir != "/tmp/project" {
		t.Errorf("ProjectDir = %q, want %q", cfg.ProjectDir, "/tmp/project")
	}
	if cfg.Mode != ModeAuto {
		t.Errorf("Mode = %q, want %q", cfg.Mode, ModeAuto)
	}
	if len(cfg.WritableDirs) != 2 {
		t.Fatalf("WritableDirs len = %d, want 2", len(cfg.WritableDirs))
	}
}

func TestBuildConfig_InvalidMode(t *testing.T) {
	_, err := BuildConfig("/tmp/project", ".claude", "maybe")
	if err == nil {
		t.Fatal("BuildConfig with invalid mode should return error")
	}
}
