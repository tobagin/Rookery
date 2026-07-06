// Package web embeds the single-page UI so the whole product ships as one
// static binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed dist
var dist embed.FS

// Files exposes the built SPA at the filesystem root for http.FileServerFS.
var Files, _ = fs.Sub(dist, "dist")
