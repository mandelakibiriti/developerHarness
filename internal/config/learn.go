package config

import (
	"path"
	"strings"
)

// Learn declares which repo-relative files the improve loop may edit — the
// Hermit-style "template": a whitelist (allow) with a deny list that wins, on
// top of an un-overridable protected set (the guardrail file, the judge's log,
// the eval set, the practice problems, and the result archive). A file is
// learnable only when it matches an allow glob, matches no deny glob, and is
// not protected.
type Learn struct {
	Allow []string `json:"allow"` // glob patterns the improver may edit
	Deny  []string `json:"deny"`  // glob patterns that win over allow
}

// defaultLearnAllow is applied when harness.json omits "learn.allow". It is
// exactly the set the context compiler reads, so a version hash already
// captures every one of these paths.
var defaultLearnAllow = []string{
	"context/**",        // system/*.md, tools/guidance.md, scripts/, memory/lessons/
	"rules/**",          // rule packs
	"tracks/**",         // level definitions
	".claude/skills/**", // skills and principles.md
}

// protectedGlobs are never learnable regardless of the manifest. They are the
// parts of the harness whose integrity the judge depends on: editing the test,
// the task set, or the guardrail file would let a candidate score itself.
var protectedGlobs = []string{
	"harness.json",         // guardrails, budgets, and this very manifest
	".harness/**",          // the event log the judge reads
	"evals/**",             // the task set and the fixed train/test split
	"practice-problems/**", // the real tests that decide pass/fail
	"runs/**",              // archived scores
	".git/**",              // history the rollback depends on
}

// Learnable reports whether a repo-relative, slash-separated path may be edited
// by the improve loop. deny wins over allow; the protected set wins over both.
func (c *Config) Learnable(p string) bool {
	p = path.Clean(strings.TrimSpace(p))
	if p == "." || p == "" || strings.HasPrefix(p, "../") || path.IsAbs(p) {
		return false
	}
	for _, pat := range protectedGlobs {
		if matchGlob(pat, p) {
			return false
		}
	}
	allow := c.Learn.Allow
	if len(allow) == 0 {
		allow = defaultLearnAllow
	}
	allowed := false
	for _, pat := range allow {
		if matchGlob(pat, p) {
			allowed = true
			break
		}
	}
	if !allowed {
		return false
	}
	for _, pat := range c.Learn.Deny {
		if matchGlob(pat, p) {
			return false
		}
	}
	return true
}
