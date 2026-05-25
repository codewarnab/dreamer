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

func TestPrepareModeOffIsNoOp(t *testing.T) {
	cfg := Config{
		ProjectDir: "/tmp/test",
		Mode:       ModeOff,
	}
	// Prepare with nil cmd is fine when mode is off — it returns early.
	// (We can't test with a real cmd without starting a process.)
	if err := Prepare(nil, cfg); err != nil {
		t.Errorf("Prepare with ModeOff should return nil, got: %v", err)
	}
}

func TestPostStartModeOffIsNoOp(t *testing.T) {
	cfg := Config{
		ProjectDir: "/tmp/test",
		Mode:       ModeOff,
	}
	if err := PostStart(nil, cfg); err != nil {
		t.Errorf("PostStart with ModeOff should return nil, got: %v", err)
	}
}
