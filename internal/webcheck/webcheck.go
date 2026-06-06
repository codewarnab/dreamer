package webcheck

import (
	"io/fs"
	"os"
	"path/filepath"
)

// Config controls which checks run and where to look for files.
type Config struct {
	// TemplatesDir is the directory containing HTML template files.
	TemplatesDir string
	// CSSDir is the directory containing CSS files.
	CSSDir string
	// JSDir is the directory containing JavaScript files.
	JSDir string
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

	if cfg.JSDir != "" {
		jsFiles, err := globDir(cfg.JSDir, ".js")
		if err != nil {
			return nil, err
		}
		all = append(all, CheckAlpineInit(jsFiles)...)
	}

	return all, nil
}

// globDir returns all files with the given extension in dir (recursive).
// Returns nil (not an error) if dir does not exist.
func globDir(dir, ext string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == dir {
				return filepath.SkipAll
			}
			return err
		}
		if !d.IsDir() && filepath.Ext(d.Name()) == ext {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return files, nil
}
