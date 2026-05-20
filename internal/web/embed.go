package web

import "embed"

//go:embed all:templates all:static
var assets embed.FS

// TemplateFS returns the embedded template tree.
func TemplateFS() embed.FS { return assets }

// StaticFS returns the embedded static asset tree.
func StaticFS() embed.FS { return assets }
