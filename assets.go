// Package harnessassets holds the files `harness init` writes into a new
// project. They are this repository's own copies, so there is one source of truth.
package harnessassets

import "embed"

//go:embed harness.json tracks/*.json rules/*.md .claude/skills
var FS embed.FS
