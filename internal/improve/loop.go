package improve

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/devex/harness/internal/config"
)

// proposerRegistry maps a method name to its constructor. Methods register
// themselves at init time (or via RegisterProposer) so the improve loop stays
// one of many (prompt-only, skillopt, harness2, ...).
var proposerRegistry = map[string]func(root string, cfg *config.Config) Proposer{
	"claude": func(root string, cfg *config.Config) Proposer { return NewClaudeProposer(root, cfg) },
}

// RegisterProposer adds (or replaces) a named proposer constructor. Call it
// from a package's init() so `harness improve propose` can find it.
func RegisterProposer(name string, fn func(root string, cfg *config.Config) Proposer) {
	proposerRegistry[name] = fn
}

// ResolveProposer picks the configured proposer (default "claude") and builds
// it. cfg may be nil, in which case the default is used.
func ResolveProposer(cfg *config.Config, root string) (Proposer, error) {
	name := ""
	if cfg != nil {
		name = cfg.Improve.Proposer
	}
	if name == "" {
		name = "claude"
	}
	fn, ok := proposerRegistry[name]
	if !ok {
		return nil, fmt.Errorf("unknown proposer %q", name)
	}
	return fn(root, cfg), nil
}

// ApplyCandidate validates a candidate against the learnable manifest and, if
// it passes, writes the full replacement content to root. ValidateCandidate is
// the machine gate: no Proposer's output is ever written past it.
func ApplyCandidate(root string, cfg *config.Config, c Candidate) error {
	if err := ValidateCandidate(cfg, c); err != nil {
		return err
	}
	dst := filepath.Join(root, filepath.FromSlash(path.Clean(c.File)))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, []byte(c.Content), 0o644)
}

// ApplyAll applies every candidate it can and returns the ones it could not:
// each validation error or write error appends the candidate to the rejected
// slice, while valid writes proceed.
func ApplyAll(root string, cfg *config.Config, cands []Candidate) []Candidate {
	var rejected []Candidate
	for _, c := range cands {
		if err := ApplyCandidate(root, cfg, c); err != nil {
			rejected = append(rejected, c)
		}
	}
	return rejected
}
