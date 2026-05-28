package web

import "embed"

//go:embed all:templates all:static
var assets embed.FS
