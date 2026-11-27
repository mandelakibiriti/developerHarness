// Package web embeds the static shell so the harness binary is self-contained.
package web

import "embed"

//go:embed index.html shell.js shell.css filetree.js explorer.js telemetry.js floorplan.js evals.js
var FS embed.FS
