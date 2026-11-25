// Package harnessctx holds the harness's editable context components (system
// prompts today; guardrails, tools, subagents, scripts as later phases land).
// The files are embedded as defaults; a project's own context/ files win, so
// edits here take effect without a rebuild when the project root is this repo.
package harnessctx

import "embed"

//go:embed system tools
var FS embed.FS
