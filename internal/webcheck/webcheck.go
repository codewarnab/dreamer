package webcheck

import (
	"os"
	"path/filepath"
)

// Config controls which checks run and where to look for files.
type Config struct {
	// TemplatesDir is the directory containing HTML template files.
	TemplatesDir string
	// CSSDir is the directory containing CSS files.
	CSSDir string
}

// Check runs all webcheck scanners and returns the combined findings.
func Check(cfg Config) ([]Finding, error) {
	var all []Finding

	if cfg.TemplatesDir != "" {
		htmlFiles, err := globDir(cfg.TemplatesDir, ".html")
		if err != nil {
			return nil, err
		}
		all = append(all, CheckXHTML(htmlFiles)...)
	}

	if cfg.CSSDir != "" {
		cssFiles, err := globDir(cfg.CSSDir, ".css")
		if err != nil {
			return nil, err
		}
		all = append(all, CheckCSSTokens(cssFiles)...)
	}

	return all, nil
}

// globDir returns all files with the given extension in dir (non-recursive).
// Returns nil (not an error) if dir does not exist.
func globDir(dir, ext string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ext {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}
