package sysinfo

import (
	"runtime"
	"testing"
)

func TestIsWSLVersionReport(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "wsl2 kernel marker",
			content: "Linux version 5.10.16.3-microsoft-standard-WSL2 (root@...) #1 SMP",
			want:    true,
		},
		{
			name:    "wsl1 reports Microsoft without standard marker",
			content: "Linux version 4.4.0-19041-Microsoft (Microsoft@Microsoft.com) GCC",
			want:    true,
		},
		{
			name:    "regular linux",
			content: "Linux version 6.5.0-arch1-1 (linux@archlinux.org) #1 SMP PREEMPT_DYNAMIC",
			want:    false,
		},
		{
			name:    "empty content",
			content: "",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isWSLVersionReport(tt.content); got != tt.want {
				t.Errorf("isWSLVersionReport(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestIsWindowsInteropPath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("interop path classification is Linux-only")
	}

	tests := []struct {
		path string
		want bool
	}{
		{"/mnt/c/projects/foo", true},
		{"/mnt/z", true},
		{"/mnt/C/projects", true},
		{"/home/user/project", false},
		{"/mnt/wsl", false},
		{"/mnt/host/wsl", false},
		{"/mnt", false},
		{"relative/path", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := IsWindowsInteropPath(tt.path); got != tt.want {
			t.Errorf("IsWindowsInteropPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
