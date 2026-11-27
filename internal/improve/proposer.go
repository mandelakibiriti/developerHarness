package improve

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/devex/harness/internal/config"
)

// Proposer is the improver half of the loop (Hermit's "Evolver", harness2's
// "propose edits"). It turns scored evidence into candidate edits to the
// harness's learnable files. It proposes; the judge and the developer decide.
// Implementations must not write files: they return Candidates, and the loop
// applies one only after ValidateCandidate passes it.
type Proposer interface {
	// Name is the method's identity for logs and reports, so the improve loop
	// stays one of many methods (prompt-only, skillopt, harness2, ...). Keep it
	// short and stable.
	Name() string

	// Propose returns up to req.K candidates for the learnable files under
	// req.Root, each justified by req.Evidence. It may read harness files (all
	// read-only) but must not edit them.
	Propose(ctx context.Context, req ProposeRequest) ([]Candidate, error)
}

// ProposeRequest is what a Proposer gets: the version being improved, the root
// its learnable files live under, the scored runs to learn from, and the max
// number of candidates to return.
type ProposeRequest struct {
	Root     string     // project root (context/, rules/, tracks/ live under it)
	Base     string     // harness version being improved (compiler hash or tag)
	Evidence []Evidence // scored runs, oldest first
	K        int        // max candidates to return (>= 1)
}

// Evidence is one scored run: the judge's verdict plus the trajectory it came
// from, so a Proposer can point a candidate at the exact failure.
type Evidence struct {
	Run  RunScore // the judge's verdict for one scored run
	Path string   // optional repo-relative path to the run's trajectory/log
}

// Candidate is one proposed edit. It is inert until the loop validates and
// applies it; nothing is written at proposal time.
type Candidate struct {
	ID      string `json:"id"`      // stable within a proposal round, e.g. "p-3"
	File    string `json:"file"`    // repo-relative, slash-separated learnable path
	Content string `json:"content"` // proposed full replacement content
	Why     string `json:"why"`     // one-line rationale tied to req.Evidence
}

// ValidateCandidate is the machine gate: it rejects any candidate that escapes
// the project root or touches a file the learnable manifest does not allow.
// Every Proposer's output must pass through this before it is applied or
// probed, so no method can edit the guardrails or the tests it is scored on.
func ValidateCandidate(cfg *config.Config, c Candidate) error {
	if c.ID == "" || strings.TrimSpace(c.File) == "" {
		return fmt.Errorf("candidate needs a non-empty id and file")
	}
	f := path.Clean(c.File)
	if f == "." || strings.HasPrefix(f, "../") || path.IsAbs(f) {
		return fmt.Errorf("candidate %q: file %q escapes the project root", c.ID, c.File)
	}
	if !cfg.Learnable(f) {
		return fmt.Errorf("candidate %q: file %q is not in the learnable set", c.ID, c.File)
	}
	if strings.TrimSpace(c.Content) == "" {
		return fmt.Errorf("candidate %q: empty content for %q", c.ID, c.File)
	}
	return nil
}
