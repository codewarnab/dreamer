package cmd

import (
	"path/filepath"
	"testing"

	"dreamer/internal/config"
)

func TestResolveProjectSince(t *testing.T) {
	tests := []struct {
		name         string
		appConfig    *config.App
		projectPath  string
		defaultSince string
		want         string
	}{
		{
			name:         "nil appConfig returns default",
			appConfig:    nil,
			projectPath:  "/path/to/project",
			defaultSince: "24h",
			want:         "24h",
		},
		{
			name: "matching project with since returns project since",
			appConfig: &config.App{
				Projects: []config.ProjectConfig{
					{
						Name:  "my-project",
						Path:  "/path/to/project",
						Since: "7d",
					},
				},
			},
			projectPath:  "/path/to/project",
			defaultSince: "24h",
			want:         "7d",
		},
		{
			name: "matching project with empty since returns default",
			appConfig: &config.App{
				Projects: []config.ProjectConfig{
					{
						Name:  "my-project",
						Path:  "/path/to/project",
						Since: "",
					},
				},
			},
			projectPath:  "/path/to/project",
			defaultSince: "24h",
			want:         "24h",
		},
		{
			name: "different project returns default",
			appConfig: &config.App{
				Projects: []config.ProjectConfig{
					{
						Name:  "other-project",
						Path:  "/path/to/other",
						Since: "14d",
					},
				},
			},
			projectPath:  "/path/to/project",
			defaultSince: "24h",
			want:         "24h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveProjectSince(tt.appConfig, tt.projectPath, tt.defaultSince)
			if got != tt.want {
				t.Errorf("resolveProjectSince() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveOutputInProjectRoot(t *testing.T) {
	projPath := filepath.Clean("/path/to/project")
	tests := []struct {
		name        string
		appConfig   *config.App
		projectPath string
		want        bool
	}{
		{
			name:        "nil appConfig returns false",
			appConfig:   nil,
			projectPath: projPath,
			want:        false,
		},
		{
			name: "project config has output_in_project_root true",
			appConfig: &config.App{
				Projects: []config.ProjectConfig{
					{
						Name:                "proj",
						Path:                projPath,
						OutputInProjectRoot: true,
					},
				},
			},
			projectPath: projPath,
			want:        true,
		},
		{
			name: "global output_in_project_root true inherited by project",
			appConfig: &config.App{
				OutputInProjectRoot: true,
				Projects: []config.ProjectConfig{
					{
						Name:                "proj",
						Path:                projPath,
						OutputInProjectRoot: false,
					},
				},
			},
			projectPath: projPath,
			want:        true,
		},
		{
			name: "global output_in_project_root true for unlisted project",
			appConfig: &config.App{
				OutputInProjectRoot: true,
				Projects:            []config.ProjectConfig{},
			},
			projectPath: projPath,
			want:        true,
		},
		{
			name: "both false returns false",
			appConfig: &config.App{
				OutputInProjectRoot: false,
				Projects: []config.ProjectConfig{
					{
						Name:                "proj",
						Path:                projPath,
						OutputInProjectRoot: false,
					},
				},
			},
			projectPath: projPath,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveOutputInProjectRoot(tt.appConfig, tt.projectPath)
			if got != tt.want {
				t.Errorf("resolveOutputInProjectRoot() = %v, want %v", got, tt.want)
			}
		})
	}
}
