package improve

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/devex/harness/internal/config"
)

// ClaudeProposer is the Claude Code CLI-backed Proposer: it shells out to
// `claude -p` with a prompt that shows the current learnable files and the
// scored evidence, and parses the JSON candidate array it prints back. It
// never writes files itself — the loop applies a candidate only after
// ValidateCandidate passes it.
type ClaudeProposer struct {
	root    string
	cfg     *config.Config
	command func(ctx context.Context, prompt string) (string, error)
	timeout time.Duration
}

// NewClaudeProposer binds a proposer to a project root and config. The default
// command runs the `claude` CLI in print (non-interactive) mode; the timeout
// bounds the call (default 5m, overridable via improve.timeout).
func NewClaudeProposer(root string, cfg *config.Config) *ClaudeProposer {
	timeout := defaultProposerTimeout
	if cfg != nil {
		timeout = proposerTimeout(cfg.Improve.Timeout)
	}
	return &ClaudeProposer{root: root, cfg: cfg, command: defaultClaudeCommand, timeout: timeout}
}

// Name reports the improve method's stable identity.
func (p *ClaudeProposer) Name() string { return "claude" }

// Propose turns scored evidence into up to K candidate edits, each confined to
// a learnable file. It resolves K from the request, then the config, then a
// fixed default; builds a prompt; invokes the command under a deadline; parses
// the JSON array it returns; drops any candidate ValidateCandidate rejects;
// and trims to K.
func (p *ClaudeProposer) Propose(ctx context.Context, req ProposeRequest) ([]Candidate, error) {
	K := req.K
	if K <= 0 && p.cfg != nil && p.cfg.Improve.K > 0 {
		K = p.cfg.Improve.K
	}
	if K <= 0 {
		K = 3
	}

	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.timeout)
		defer cancel()
	}

	out, err := p.command(ctx, p.buildPrompt(req))
	if err != nil {
		return nil, fmt.Errorf("proposer: %w", err)
	}

	cands, err := parseCandidates(out)
	if err != nil {
		return nil, err
	}

	kept := make([]Candidate, 0, len(cands))
	for i, c := range cands {
		if c.ID == "" {
			c.ID = fmt.Sprintf("p-%d", i+1)
		}
		if p.cfg != nil && ValidateCandidate(p.cfg, c) != nil {
			continue
		}
		kept = append(kept, c)
	}
	if len(kept) > K {
		kept = kept[:K]
	}
	return kept, nil
}

// buildPrompt renders the instruction, the learnable allow list, the current
// content of every learnable file, and the scored evidence into a plain prompt.
func (p *ClaudeProposer) buildPrompt(req ProposeRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are proposing edits to a coding harness (version %s) to raise its task pass rate.\n", req.Base)
	fmt.Fprintln(&b, "You must not touch the harness's guardrails, judge, eval set, or practice problems.")
	if p.cfg != nil && len(p.cfg.Learn.Allow) > 0 {
		fmt.Fprintln(&b, "Only these learnable allow paths may be edited:")
		for _, pat := range p.cfg.Learn.Allow {
			fmt.Fprintf(&b, "  - %s\n", pat)
		}
		fmt.Fprintln(&b, "Deny patterns and the protected set (harness.json, .harness/, evals/, practice-problems/, runs/, .git/) are off-limits; a proposal touching them is discarded.")
	} else {
		fmt.Fprintln(&b, "Only learnable files (no deny or protected paths) may be edited.")
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Current content of each learnable file under the root:")
	for _, rel := range p.learnableFiles() {
		content, err := os.ReadFile(filepath.Join(p.root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "=== %s ===\n%s\n", rel, string(content))
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Scored evidence (oldest first):")
	for _, e := range req.Evidence {
		r := e.Run
		line := fmt.Sprintf("- task %s: score %.2f, pass %v, cost $%.4f", r.Task, r.Score, r.Pass, r.CostUSD)
		if r.Reason != "" {
			line += fmt.Sprintf(", reason: %s", r.Reason)
		}
		fmt.Fprintln(&b, line)
	}

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, `Reply with ONLY a JSON array and nothing else (no prose, no code fence). Each element must be an object:
{"id":"...","file":"...","content":"...","why":"..."}
where "file" is one of the learnable paths above and "content" is the full replacement text.`)
	return b.String()
}

// learnableFiles returns the repo-relative, slash-separated paths of every
// learnable file under the root, skipping .git and .harness subtrees. The list
// is best-effort: an unreadable file or a walk error merely drops that file
// from the prompt rather than failing the proposal.
func (p *ClaudeProposer) learnableFiles() []string {
	var files []string
	_ = filepath.WalkDir(p.root, func(fpath string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort: stop descending this branch
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == ".harness" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(p.root, fpath)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if p.cfg != nil && !p.cfg.Learnable(rel) {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	sort.Strings(files)
	return files
}

// parseCandidates extracts the first '[' through the last ']' from the command
// output and unmarshals it as a JSON array of candidates.
func parseCandidates(s string) ([]Candidate, error) {
	lo, hi := strings.Index(s, "["), strings.LastIndex(s, "]")
	if lo < 0 || hi <= lo {
		return nil, fmt.Errorf("proposer: bad output: no JSON array")
	}
	var out []Candidate
	if err := json.Unmarshal([]byte(s[lo:hi+1]), &out); err != nil {
		return nil, fmt.Errorf("proposer: bad output: %w", err)
	}
	return out, nil
}

// defaultProposerTimeout bounds a single proposer call. improve.timeout may
// override it; an empty or invalid value falls back here.
const defaultProposerTimeout = 5 * time.Minute

// proposerTimeout parses a duration string, defaulting to defaultProposerTimeout
// on empty, invalid, or non-positive input (the same fallback shape as
// practice.Problem.Timeout).
func proposerTimeout(s string) time.Duration {
	if s == "" {
		return defaultProposerTimeout
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return defaultProposerTimeout
}

// defaultClaudeCommand runs the Claude Code CLI in print mode and returns its
// trimmed combined output.
func defaultClaudeCommand(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", "-p", prompt)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
