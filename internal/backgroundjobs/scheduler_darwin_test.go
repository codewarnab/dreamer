//go:build darwin

package backgroundjobs

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"dreamer/internal/logging"
)

func TestBuildCalendarIntervals_Hourly(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleInterval}
	intervals, err := buildCalendarIntervals(spec)
	if err != nil {
		t.Fatalf("buildCalendarIntervals(hourly): %v", err)
	}
	if len(intervals) != 1 {
		t.Fatalf("got %d intervals, want 1", len(intervals))
	}
	if intervals[0].Hour != -1 {
		t.Errorf("Hour = %d, want -1", intervals[0].Hour)
	}
	if intervals[0].Minute != 0 {
		t.Errorf("Minute = %d, want 0", intervals[0].Minute)
	}
	if intervals[0].Weekday != -1 {
		t.Errorf("Weekday = %d, want -1 (not specified)", intervals[0].Weekday)
	}
}

func TestBuildCalendarIntervals_Daily(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "14:30"}
	intervals, err := buildCalendarIntervals(spec)
	if err != nil {
		t.Fatalf("buildCalendarIntervals(daily): %v", err)
	}
	if len(intervals) != 1 {
		t.Fatalf("got %d intervals, want 1", len(intervals))
	}
	if intervals[0].Hour != 14 {
		t.Errorf("Hour = %d, want 14", intervals[0].Hour)
	}
	if intervals[0].Minute != 30 {
		t.Errorf("Minute = %d, want 30", intervals[0].Minute)
	}
	if intervals[0].Weekday != -1 {
		t.Errorf("Weekday = %d, want -1 (not specified)", intervals[0].Weekday)
	}
}

func TestBuildCalendarIntervals_Weekly(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleWeekly, DayOfWeek: "Monday", TimeOfDay: "09:00"}
	intervals, err := buildCalendarIntervals(spec)
	if err != nil {
		t.Fatalf("buildCalendarIntervals(weekly): %v", err)
	}
	if len(intervals) != 1 {
		t.Fatalf("got %d intervals, want 1", len(intervals))
	}
	if intervals[0].Hour != 9 {
		t.Errorf("Hour = %d, want 9", intervals[0].Hour)
	}
	if intervals[0].Minute != 0 {
		t.Errorf("Minute = %d, want 0", intervals[0].Minute)
	}
	// Monday = 1 in launchd.
	if intervals[0].Weekday != 1 {
		t.Errorf("Weekday = %d, want 1 (Monday)", intervals[0].Weekday)
	}
}

func TestBuildCalendarIntervals_Sunday(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleWeekly, DayOfWeek: "Sunday", TimeOfDay: "10:00"}
	intervals, err := buildCalendarIntervals(spec)
	if err != nil {
		t.Fatalf("buildCalendarIntervals(sunday): %v", err)
	}
	if len(intervals) != 1 {
		t.Fatalf("got %d intervals, want 1", len(intervals))
	}
	// Sunday = 0 in launchd.
	if intervals[0].Weekday != 0 {
		t.Errorf("Weekday = %d, want 0 (Sunday)", intervals[0].Weekday)
	}
}

func TestComputeNextCalendarRun_Hourly(t *testing.T) {
	now := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC)
	next := computeNextCalendarRun(now, -1, 0, -1)
	// Next hourly run should be at 15:00.
	if next.Hour() != 15 || next.Minute() != 0 {
		t.Errorf("next = %v, want 15:00", next)
	}
}

func TestComputeNextCalendarRun_Daily(t *testing.T) {
	// Before target time.
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	next := computeNextCalendarRun(now, 9, 0, -1)
	if next.Day() != 27 || next.Hour() != 9 {
		t.Errorf("next = %v, want May 27 09:00", next)
	}
	// After target time — should be tomorrow.
	now = time.Date(2026, 5, 27, 10, 0, 0, 0, time.UTC)
	next = computeNextCalendarRun(now, 9, 0, -1)
	if next.Day() != 28 || next.Hour() != 9 {
		t.Errorf("next = %v, want May 28 09:00", next)
	}
}

func TestComputeNextCalendarRun_Weekly(t *testing.T) {
	// Wednesday = 3. It's Monday (weekday 1).
	now := time.Date(2026, 5, 25, 8, 0, 0, 0, time.UTC)
	next := computeNextCalendarRun(now, 9, 0, 3)
	if next.Weekday() != time.Wednesday {
		t.Errorf("next weekday = %v, want Wednesday", next.Weekday())
	}
}

func TestWeekdayToLaunchd(t *testing.T) {
	tests := []struct {
		day  string
		want int
	}{
		{"sunday", 0},
		{"monday", 1},
		{"tuesday", 2},
		{"wednesday", 3},
		{"thursday", 4},
		{"friday", 5},
		{"saturday", 6},
		{"invalid", -1},
	}
	for _, tt := range tests {
		t.Run(tt.day, func(t *testing.T) {
			got := weekdayToLaunchd(tt.day)
			if got != tt.want {
				t.Errorf("weekdayToLaunchd(%q) = %d, want %d", tt.day, got, tt.want)
			}
		})
	}
}

// B1: Verify Install uses os.Getuid(), not os.Getenv("UID").
func TestDarwinScheduler_Install_UsesGetuid(t *testing.T) {
	var capturedArgs []string
	s := &darwinScheduler{
		cfg: SchedulerConfig{
			StoreDir:       t.TempDir(),
			ExecutablePath: "/usr/local/bin/dreamer",
			ConfigPath:     "/Users/test/.config/dreamer/config.yaml",
			InstallID:      "test-install",
			ConfigHash:     "abc123",
			ExecHash:       "def456",
		},
		logger: logging.Silent(),
		runCmd: func(_ context.Context, name string, args ...string) ([]byte, error) {
			capturedArgs = append(capturedArgs, append([]string{name}, args...)...)
			return nil, nil
		},
	}

	params := ScheduleParams{
		JobID:    "aabbccdd11223344",
		Schedule: ScheduleSpec{Kind: ScheduleInterval, Timezone: "UTC"},
		Name:     "Test Job",
		Enabled:  true,
	}

	_, err := s.Install(context.Background(), params)
	if err != nil {
		t.Fatalf("Install error: %v", err)
	}

	// Verify bootstrap uses os.Getuid() (via strconv.Itoa), not os.Getenv("UID").
	expectedUID := strconv.Itoa(os.Getuid())
	found := false
	for _, arg := range capturedArgs {
		if arg == "gui/"+expectedUID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected gui/%s in args %v, not found", expectedUID, capturedArgs)
	}
}

// B1: Verify Remove uses os.Getuid().
func TestDarwinScheduler_Remove_UsesGetuid(t *testing.T) {
	var capturedArgs []string
	s := &darwinScheduler{
		cfg: SchedulerConfig{
			StoreDir:       t.TempDir(),
			ExecutablePath: "/usr/local/bin/dreamer",
			ConfigPath:     "/Users/test/.config/dreamer/config.yaml",
			InstallID:      "test-install",
			ConfigHash:     "abc123",
			ExecHash:       "def456",
		},
		logger: logging.Silent(),
		runCmd: func(_ context.Context, name string, args ...string) ([]byte, error) {
			capturedArgs = append([]string{name}, args...)
			return nil, nil
		},
	}

	err := s.Remove(context.Background(), "aabbccdd11223344")
	if err != nil {
		t.Fatalf("Remove error: %v", err)
	}

	// Verify bootout uses os.Getuid().
	expectedUID := strconv.Itoa(os.Getuid())
	found := false
	for _, arg := range capturedArgs {
		if arg == "gui/"+expectedUID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected gui/%s in args %v, not found", expectedUID, capturedArgs)
	}
}

// B2: Verify plist Marshal → Unmarshal round-trip preserves all fields.
func TestLaunchAgentPlist_MarshalUnmarshalRoundTrip(t *testing.T) {
	original := launchAgentPlist{
		Version: "1.0",
		Label:   "com.dreamer.background.aabbccdd11223344",
		ProgramArguments: []string{
			"/usr/local/bin/dreamer", "jobs", "run", "aabbccdd11223344",
			"--config", "/Users/test/.config/dreamer/config.yaml",
		},
		WorkingDirectory: "/Users/test/.dreamer/background-jobs",
		StartCalendarInterval: []calendarInterval{
			{Hour: 9, Minute: 0, Weekday: -1},
		},
		StandardOutPath:   "/tmp/stdout.log",
		StandardErrorPath: "/tmp/stderr.log",
		Disabled:          false,
	}

	// Marshal.
	data, err := xml.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Unmarshal.
	var roundTripped launchAgentPlist
	if err := xml.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	// Verify fields round-trip correctly.
	if roundTripped.Label != original.Label {
		t.Errorf("Label = %q, want %q", roundTripped.Label, original.Label)
	}
	if roundTripped.WorkingDirectory != original.WorkingDirectory {
		t.Errorf("WorkingDirectory = %q, want %q", roundTripped.WorkingDirectory, original.WorkingDirectory)
	}
	if roundTripped.StandardOutPath != original.StandardOutPath {
		t.Errorf("StandardOutPath = %q, want %q", roundTripped.StandardOutPath, original.StandardOutPath)
	}
	if roundTripped.StandardErrorPath != original.StandardErrorPath {
		t.Errorf("StandardErrorPath = %q, want %q", roundTripped.StandardErrorPath, original.StandardErrorPath)
	}
	if roundTripped.Disabled != original.Disabled {
		t.Errorf("Disabled = %v, want %v", roundTripped.Disabled, original.Disabled)
	}
	if len(roundTripped.ProgramArguments) != len(original.ProgramArguments) {
		t.Errorf("ProgramArguments len = %d, want %d", len(roundTripped.ProgramArguments), len(original.ProgramArguments))
	}
	if len(roundTripped.StartCalendarInterval) != len(original.StartCalendarInterval) {
		t.Errorf("StartCalendarInterval len = %d, want %d", len(roundTripped.StartCalendarInterval), len(original.StartCalendarInterval))
	}
}

// B2: Verify Unmarshal sets Disabled from <true/>.
func TestLaunchAgentPlist_UnmarshalSetsDisabled(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>test.label</string>
	<key>Disabled</key>
	<true/>
</dict>
</plist>`

	var p launchAgentPlist
	if err := xml.Unmarshal([]byte(xmlData), &p); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if !p.Disabled {
		t.Error("Disabled = false, want true")
	}
}

// B2: Verify Unmarshal parses ProgramArguments array.
func TestLaunchAgentPlist_UnmarshalProgramArguments(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>test.label</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/bin/env</string>
		<string>jobs</string>
		<string>run</string>
		<string>aabbccdd11223344</string>
	</array>
</dict>
</plist>`

	var p launchAgentPlist
	if err := xml.Unmarshal([]byte(xmlData), &p); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(p.ProgramArguments) != 4 {
		t.Errorf("ProgramArguments len = %d, want 4", len(p.ProgramArguments))
	}
	if p.ProgramArguments[0] != "/usr/bin/env" {
		t.Errorf("ProgramArguments[0] = %q, want %q", p.ProgramArguments[0], "/usr/bin/env")
	}
}

// B2: Verify Unmarshal parses calendar interval dict.
func TestLaunchAgentPlist_UnmarshalCalendarInterval(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>test.label</string>
	<key>StartCalendarInterval</key>
	<dict>
		<key>Hour</key>
		<integer>9</integer>
		<key>Minute</key>
		<integer>30</integer>
	</dict>
</dict>
</plist>`

	var p launchAgentPlist
	if err := xml.Unmarshal([]byte(xmlData), &p); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(p.StartCalendarInterval) != 1 {
		t.Fatalf("StartCalendarInterval len = %d, want 1", len(p.StartCalendarInterval))
	}
	if p.StartCalendarInterval[0].Hour != 9 {
		t.Errorf("Hour = %d, want 9", p.StartCalendarInterval[0].Hour)
	}
	if p.StartCalendarInterval[0].Minute != 30 {
		t.Errorf("Minute = %d, want 30", p.StartCalendarInterval[0].Minute)
	}
}

func TestBuildCalendarIntervals_HourlyEvery(t *testing.T) {
	tests := []struct {
		name    string
		every   string
		wantNil bool
	}{
		{"default", "", false},
		{"5m", "5m", true},
		{"15m", "15m", true},
		{"2h", "2h", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ScheduleSpec{Kind: ScheduleInterval, Every: tt.every}
			intervals, err := buildCalendarIntervals(spec)
			if err != nil {
				t.Fatalf("buildCalendarIntervals(every=%q): %v", tt.every, err)
			}
			if tt.wantNil {
				if intervals != nil {
					t.Errorf("buildCalendarIntervals(every=%q) = %v, want nil", tt.every, intervals)
				}
			} else {
				if len(intervals) != 1 {
					t.Errorf("buildCalendarIntervals(every=%q) len = %d, want 1", tt.every, len(intervals))
				}
			}
		})
	}
}

func TestLaunchAgentPlist_MarshalStartInterval(t *testing.T) {
	plist := launchAgentPlist{
		Version: "1.0",
		Label:   "com.dreamer.background.test",
		ProgramArguments: []string{
			"/usr/local/bin/dreamer", "jobs", "run", "test",
		},
		WorkingDirectory:  "/tmp/test",
		StartInterval:     900, // 15 minutes
		StandardOutPath:   "/tmp/stdout.log",
		StandardErrorPath: "/tmp/stderr.log",
		Disabled:          false,
	}

	data, err := xml.MarshalIndent(plist, "", "  ")
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	xmlStr := string(data)
	if !contains(xmlStr, "<integer>900</integer>") {
		t.Errorf("Marshal should contain <integer>900</integer>, got: %s", xmlStr)
	}
	// Should NOT contain StartCalendarInterval.
	if contains(xmlStr, "StartCalendarInterval") {
		t.Errorf("Marshal should not contain StartCalendarInterval, got: %s", xmlStr)
	}
}

func TestLaunchAgentPlist_UnmarshalStartInterval(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>test.label</string>
	<key>StartInterval</key>
	<integer>900</integer>
</dict>
</plist>`

	var p launchAgentPlist
	if err := xml.Unmarshal([]byte(xmlData), &p); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if p.StartInterval != 900 {
		t.Errorf("StartInterval = %d, want 900", p.StartInterval)
	}
}

func TestLaunchAgentPlist_MarshalUnmarshalStartIntervalRoundTrip(t *testing.T) {
	original := launchAgentPlist{
		Version: "1.0",
		Label:   "com.dreamer.background.test",
		ProgramArguments: []string{
			"/usr/local/bin/dreamer", "jobs", "run", "test",
		},
		WorkingDirectory:  "/tmp/test",
		StartInterval:     900,
		StandardOutPath:   "/tmp/stdout.log",
		StandardErrorPath: "/tmp/stderr.log",
	}

	data, err := xml.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var roundTripped launchAgentPlist
	if err := xml.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if roundTripped.StartInterval != original.StartInterval {
		t.Errorf("StartInterval = %d, want %d", roundTripped.StartInterval, original.StartInterval)
	}
}

// contains is a simple helper for test assertions.
// B3: Verify bootstrap suppresses "already bootstrapped" errors by searching
// the combined output bytes, not err.Error().
func TestDarwinScheduler_Bootstrap_AlreadyBootstrapped(t *testing.T) {
	s := &darwinScheduler{
		cfg: SchedulerConfig{
			StoreDir:       t.TempDir(),
			ExecutablePath: "/usr/local/bin/dreamer",
			ConfigPath:     "/Users/test/.config/dreamer/config.yaml",
		},
		logger: logging.Silent(),
		runCmd: func(_ context.Context, name string, args ...string) ([]byte, error) {
			// launchctl writes the message to stderr; CombinedOutput merges both.
			return []byte("Boot-out failed: 5: Input/output error\nAlready bootstrapped"), fmt.Errorf("exit status 1")
		},
	}

	err := s.bootstrap(context.Background(), "/tmp/test.plist")
	if err != nil {
		t.Errorf("bootstrap should suppress 'already bootstrapped', got: %v", err)
	}
}

// B3: Verify bootstrap returns real errors.
func TestDarwinScheduler_Bootstrap_RealError(t *testing.T) {
	s := &darwinScheduler{
		cfg: SchedulerConfig{
			StoreDir:       t.TempDir(),
			ExecutablePath: "/usr/local/bin/dreamer",
			ConfigPath:     "/Users/test/.config/dreamer/config.yaml",
		},
		logger: logging.Silent(),
		runCmd: func(_ context.Context, name string, args ...string) ([]byte, error) {
			return []byte("Could not read path"), fmt.Errorf("exit status 1")
		},
	}

	err := s.bootstrap(context.Background(), "/tmp/test.plist")
	if err == nil {
		t.Error("bootstrap should return error for non-'already bootstrapped' failures")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
