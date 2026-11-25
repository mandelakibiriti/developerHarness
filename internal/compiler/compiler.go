// Package compiler builds the model's context from the harness's editable
// components, so every prompt the harness sends comes from versioned files
// rather than Go strings (the precondition for improving them, harness2-style).
//
// A Bundle has two parts:
//
//	Prefix  stable across turns, so providers can cache it: system prompt
//	        (base + mode + mode.level), principles, rule-pack index, track,
//	        guardrails, and the skill index (names only: progressive disclosure).
//	Suffix  per turn: the latest lessons for the request's domain.
//
// Version is a short hash of the whole bundle; it is stamped on every event the
// call produces, so a score can be traced to the context that earned it.
package compiler

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/devex/harness/internal/config"
	"github.com/devex/harness/internal/track"
)

// lessonsPerTurn caps how much memory goes into one prompt.
const lessonsPerTurn = 5

// Request selects what to compile.
type Request struct {
	Mode   string `json:"mode"`   // architect | build | explain | "" (general)
	Level  string `json:"level"`  // beginner | intermediate | advanced
	Domain string `json:"domain"` // lessons file; "" means "general"
}

// Bundle is a compiled context.
type Bundle struct {
	Prefix  string   `json:"prefix"`
	Suffix  string   `json:"suffix"`
	Version string   `json:"version"`
	Sources []string `json:"sources"` // inputs read, in order
}

// System returns the full system prompt.
func (b Bundle) System() string {
	if b.Suffix == "" {
		return b.Prefix
	}
	return b.Prefix + "\n\n" + b.Suffix
}

// Compiler reads components from a project root, falling back to embedded
// defaults for context/ files the project does not override.
type Compiler struct {
	Root     string
	Cfg      *config.Config
	Defaults fs.FS // rooted at context/ (harnessctx.FS)
}

// Compile assembles the bundle for req.
func (c *Compiler) Compile(req Request) (Bundle, error) {
	var b Bundle
	var pre []string

	base, err := c.contextFile(&b, "system/base.md", true)
	if err != nil {
		return Bundle{}, err
	}
	pre = append(pre, base)
	if req.Mode != "" {
		for _, name := range []string{req.Mode + ".md", req.Mode + "." + req.Level + ".md"} {
			txt, err := c.contextFile(&b, "system/"+name, false)
			if err != nil {
				return Bundle{}, err
			}
			if txt != "" {
				pre = append(pre, txt)
			}
		}
	}

	if txt, err := c.projectFile(&b, ".claude/skills/go-tutor/principles.md"); err != nil {
		return Bundle{}, err
	} else if txt != "" {
		pre = append(pre, "## Principles\n"+txt)
	}
	if txt, err := c.rules(&b); err != nil {
		return Bundle{}, err
	} else if txt != "" {
		pre = append(pre, txt)
	}
	if txt, err := c.track(&b, req.Level); err != nil {
		return Bundle{}, err
	} else if txt != "" {
		pre = append(pre, txt)
	}
	if txt := c.guardrails(); txt != "" {
		pre = append(pre, txt)
	}
	if txt, err := c.skills(&b); err != nil {
		return Bundle{}, err
	} else if txt != "" {
		pre = append(pre, txt)
	}

	b.Prefix = strings.Join(pre, "\n\n")
	if b.Suffix, err = c.lessons(&b, req.Domain); err != nil {
		return Bundle{}, err
	}
	sum := sha256.Sum256([]byte(b.Prefix + "\x00" + b.Suffix))
	b.Version = hex.EncodeToString(sum[:])[:12]
	return b, nil
}

// ContextFile reads one required context component, e.g. "tools/guidance.md",
// with the same project-over-embedded precedence as Compile.
func (c *Compiler) ContextFile(rel string) (string, error) {
	var b Bundle
	return c.contextFile(&b, rel, true)
}

// contextFile reads context/<rel>: the project's copy wins, else the embedded
// default. A missing optional file returns "".
func (c *Compiler) contextFile(b *Bundle, rel string, required bool) (string, error) {
	disk := filepath.Join(c.Root, "context", filepath.FromSlash(rel))
	if data, err := os.ReadFile(disk); err == nil {
		b.Sources = append(b.Sources, "context/"+rel)
		return strings.TrimSpace(string(data)), nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read %s: %w", disk, err)
	}
	if c.Defaults != nil {
		if data, err := fs.ReadFile(c.Defaults, rel); err == nil {
			b.Sources = append(b.Sources, "embedded:context/"+rel)
			return strings.TrimSpace(string(data)), nil
		}
	}
	if required {
		return "", fmt.Errorf("context/%s not found in project or embedded defaults", rel)
	}
	return "", nil
}

// projectFile reads an optional file under the project root.
func (c *Compiler) projectFile(b *Bundle, rel string) (string, error) {
	data, err := os.ReadFile(filepath.Join(c.Root, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", rel, err)
	}
	b.Sources = append(b.Sources, rel)
	return strings.TrimSpace(string(data)), nil
}

// rules indexes the configured rule packs: id, title and the Rule: line only.
// The full pack (Prevents/Detect/Teach) stays on disk for rule-gate to read.
// A configured pack that is missing is an error: a silently dropped rule is a
// rule nobody enforces.
func (c *Compiler) rules(b *Bundle) (string, error) {
	if c.Cfg == nil || len(c.Cfg.Rules) == 0 {
		return "", nil
	}
	var out []string
	for _, rel := range c.Cfg.Rules {
		data, err := os.ReadFile(filepath.Join(c.Root, rel))
		if err != nil {
			return "", fmt.Errorf("rule pack %s: %w", rel, err)
		}
		b.Sources = append(b.Sources, rel)
		var id string
		for _, line := range strings.Split(string(data), "\n") {
			switch {
			case strings.HasPrefix(line, "### "):
				id = strings.Join(strings.Fields(strings.TrimPrefix(line, "### ")), " ")
			case id != "" && strings.HasPrefix(line, "Rule:"):
				out = append(out, "- "+id+": "+strings.TrimSpace(strings.TrimPrefix(line, "Rule:")))
				id = ""
			}
		}
	}
	if len(out) == 0 {
		return "", nil
	}
	return "## Rules (cite the id when a plan or diff breaks one)\n" + strings.Join(out, "\n"), nil
}

func (c *Compiler) track(b *Bundle, level string) (string, error) {
	if level == "" {
		return "", nil
	}
	rel := filepath.Join("tracks", level+".json")
	path := filepath.Join(c.Root, rel)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	t, err := track.Load(path)
	if err != nil {
		return "", err
	}
	b.Sources = append(b.Sources, rel)
	return fmt.Sprintf("## Track: %s\nAgent mode: %s. Gates required before a card is done: %s.",
		t.Level, t.AgentMode, strings.Join(t.Gates, ", ")), nil
}

func (c *Compiler) guardrails() string {
	lines := []string{"Every tool binds to 127.0.0.1; never send traffic off this machine."}
	if c.Cfg != nil {
		if usd := c.Cfg.Budgets.PerFeatureUSD; usd > 0 {
			lines = append(lines, fmt.Sprintf("Budget: $%.2f per feature; warn the developer before exceeding it.", usd))
		}
		for _, t := range c.Cfg.Tools {
			if t.Scope == nil {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s may only target hosts %s on ports %v; every intrusive step needs the developer's approval.",
				t.ID, strings.Join(t.Scope.Allow, ", "), t.Scope.Ports))
		}
	}
	return "## Guardrails\n- " + strings.Join(lines, "\n- ")
}

// skills lists each skill's name and description; the body is loaded only
// when the agent decides to use it.
func (c *Compiler) skills(b *Bundle) (string, error) {
	paths, err := filepath.Glob(filepath.Join(c.Root, ".claude", "skills", "*", "SKILL.md"))
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	var out []string
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", p, err)
		}
		name, desc := frontmatter(data)
		if name == "" {
			continue
		}
		rel, _ := filepath.Rel(c.Root, p)
		b.Sources = append(b.Sources, rel)
		out = append(out, "- "+name+": "+desc)
	}
	if len(out) == 0 {
		return "", nil
	}
	return "## Skills available (load one only when the task calls for it)\n" + strings.Join(out, "\n"), nil
}

func frontmatter(data []byte) (name, desc string) {
	if !bytes.HasPrefix(data, []byte("---")) {
		return "", ""
	}
	for i, line := range strings.Split(string(data), "\n") {
		if i > 0 && strings.TrimSpace(line) == "---" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "name":
			name = v
		case "description":
			desc = v
		}
	}
	return name, desc
}

// lessons returns the last lessonsPerTurn lessons for domain.
func (c *Compiler) lessons(b *Bundle, domain string) (string, error) {
	if domain == "" {
		domain = "general"
	}
	if strings.ContainsAny(domain, `/\.`) {
		return "", fmt.Errorf("invalid domain %q", domain)
	}
	rel := filepath.Join("context", "memory", "lessons", domain+".jsonl")
	f, err := os.Open(filepath.Join(c.Root, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open %s: %w", rel, err)
	}
	defer f.Close()
	var all []string
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var l struct {
			Lesson string `json:"lesson"`
		}
		if err := json.Unmarshal([]byte(line), &l); err != nil || l.Lesson == "" {
			return "", fmt.Errorf("%s:%d: not a lesson object", rel, n)
		}
		all = append(all, "- "+l.Lesson)
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read %s: %w", rel, err)
	}
	if len(all) == 0 {
		return "", nil
	}
	b.Sources = append(b.Sources, rel)
	if len(all) > lessonsPerTurn {
		all = all[len(all)-lessonsPerTurn:]
	}
	return "## Lessons from earlier work (" + domain + ")\n" + strings.Join(all, "\n"), nil
}
