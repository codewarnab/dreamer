package backgroundjobs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGenerateRandomHex_Length(t *testing.T) {
	for _, n := range []int{8, 16, 32} {
		s, err := generateRandomHex(n)
		if err != nil {
			t.Fatalf("generateRandomHex(%d) error: %v", n, err)
		}
		if len(s) != n {
			t.Errorf("generateRandomHex(%d) len = %d, want %d", n, len(s), n)
		}
	}
}

func TestGenerateRandomHex_HexChars(t *testing.T) {
	s, err := generateRandomHex(16)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("generateRandomHex produced non-hex char %q in %q", c, s)
		}
	}
}

func TestGenerateRandomHex_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		s, err := generateRandomHex(16)
		if err != nil {
			t.Fatal(err)
		}
		if seen[s] {
			t.Fatalf("duplicate hex string %q on iteration %d", s, i)
		}
		seen[s] = true
	}
}

func TestResolveInstallID_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	id, err := ResolveInstallID(dir)
	if err != nil {
		t.Fatalf("ResolveInstallID error: %v", err)
	}
	if !isValidHexID(id) {
		t.Errorf("ResolveInstallID returned invalid ID %q", id)
	}

	// Verify file was written.
	data, err := os.ReadFile(filepath.Join(dir, installIDFile))
	if err != nil {
		t.Fatalf("ReadFile install_id: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if got != id {
		t.Errorf("file content %q != returned ID %q", got, id)
	}
}

func TestResolveInstallID_StableAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	id1, err := ResolveInstallID(dir)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := ResolveInstallID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Errorf("ResolveInstallID not stable: %q != %q", id1, id2)
	}
}

func TestResolveInstallID_RegeneratesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, installIDFile), []byte("corrupt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	id, err := ResolveInstallID(dir)
	if err != nil {
		t.Fatalf("ResolveInstallID error: %v", err)
	}
	if !isValidHexID(id) {
		t.Errorf("did not regenerate corrupt ID: %q", id)
	}
}

func TestIsValidHexID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"0123456789abcdef", true},
		{"0123456789ABCDEF", false},  // uppercase
		{"0123456789abcde", false},   // too short
		{"0123456789abcdeff", false}, // too long
		{"", false},
		{"ghijklmnopqrstuv", false}, // non-hex
	}
	for _, tt := range tests {
		got := isValidHexID(tt.id)
		if got != tt.want {
			t.Errorf("isValidHexID(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestHashConfigPath_Deterministic(t *testing.T) {
	h1 := HashConfigPath("/tmp/test/config.yaml")
	h2 := HashConfigPath("/tmp/test/config.yaml")
	if h1 != h2 {
		t.Errorf("HashConfigPath not deterministic: %q != %q", h1, h2)
	}
	if len(h1) != 16 {
		t.Errorf("HashConfigPath length = %d, want 16", len(h1))
	}
}

func TestHashConfigPath_CaseInsensitiveWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}
	h1 := HashConfigPath("C:\\Users\\test\\config.yaml")
	h2 := HashConfigPath("c:\\users\\test\\config.yaml")
	if h1 != h2 {
		t.Errorf("HashConfigPath not case-insensitive on Windows: %q != %q", h1, h2)
	}
}

func TestHashExecutablePath_Deterministic(t *testing.T) {
	h1 := HashExecutablePath("/usr/local/bin/dreamer")
	h2 := HashExecutablePath("/usr/local/bin/dreamer")
	if h1 != h2 {
		t.Errorf("HashExecutablePath not deterministic: %q != %q", h1, h2)
	}
	if len(h1) != 16 {
		t.Errorf("HashExecutablePath length = %d, want 16", len(h1))
	}
}

func TestHashScheduleSpec_Deterministic(t *testing.T) {
	spec := ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "09:00", Timezone: "UTC"}
	h1, err := HashScheduleSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := HashScheduleSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("HashScheduleSpec not deterministic: %q != %q", h1, h2)
	}
}

func TestHashScheduleSpec_DifferentForDifferentSpecs(t *testing.T) {
	spec1 := ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "09:00", Timezone: "UTC"}
	spec2 := ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "10:00", Timezone: "UTC"}
	h1, _ := HashScheduleSpec(spec1)
	h2, _ := HashScheduleSpec(spec2)
	if h1 == h2 {
		t.Errorf("HashScheduleSpec should differ for different specs: %q == %q", h1, h2)
	}
}
