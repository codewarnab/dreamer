package pathjointest

import (
	"os"
	"path"
)

func badEnvDerived() string {
	dir := os.Getenv("APP_DIR")
	return path.Join(dir, "config.json") // want "path.Join used on a filesystem path"
}

func badOSCall() string {
	home, _ := os.UserHomeDir()
	return path.Join(home, ".dreamer") // want "path.Join used on a filesystem path"
}

func badFSName(configPath string) string {
	return path.Dir(configPath) // want "path.Join used on a filesystem path"
}
