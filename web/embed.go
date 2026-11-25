// Package web embeds the static shell so the harness binary is self-contained.
package web

import "embed"

//go:embed index.html shell.js shell.css filetree.js visuals.js explorer.js
var FS embed.FS
