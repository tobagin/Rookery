// Package web embeds the single-page UI so the whole product ships as one
// static binary.
package web

import "embed"

//go:embed index.html app.js style.css
var Files embed.FS
