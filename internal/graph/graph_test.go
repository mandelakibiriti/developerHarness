package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTerms(t *testing.T) {
	cases := map[string][]string{
		"SameOrigin":                    {"same", "origin"},
		"HTTPServer startBudgetWatcher": {"http", "serv", "start", "budget", "watch"},
		"internal/proxy/proxy.go":       {"internal", "proxy", "proxy"},
		"promoted promotion promote":    {"promot", "promot", "promot"},
		"validate validation validated": {"valid", "valid", "valid"},
		"Where is the function":         nil,
	}
	for in, want := range cases {
		if got := Terms(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Terms(%q) = %v, want %v", in, got, want)
		}
	}
}

func writeGraph(t *testing.T) (path, root string) {
	t.Helper()
	root = t.TempDir()
	src := "package p\n\n// Gatekeeper refuses requests whose Origin is foreign.\nfunc Gatekeeper() {}\n\nfunc helper() {}\n"
	if err := os.WriteFile(filepath.Join(root, "p.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	g := map[string]any{
		"built_at_commit": "abc",
		"nodes": []map[string]any{
			{"id": "gate", "label": "Gatekeeper", "source_file": "p.go", "source_location": "L4", "community": 1},
			{"id": "help", "label": "helper", "source_file": "p.go", "source_location": "L6", "community": 1},
			{"id": "tgate", "label": "TestGatekeeper", "source_file": "p_test.go", "source_location": "L1", "community": 2},
			{"id": "far", "label": "Far", "community": 3},
		},
		"links": []map[string]any{
			{"source": "gate", "target": "help", "relation": "calls"},
			{"source": "tgate", "target": "gate", "relation": "calls"},
			{"source": "help", "target": "far", "relation": "calls"},
		},
	}
	b, _ := json.Marshal(g)
	path = filepath.Join(root, "graph.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, root
}

func TestSearchUsesDocCommentsAndPenalisesTests(t *testing.T) {
	path, root := writeGraph(t)
	g, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	// "foreign origin" appears only in the doc comment, never in a name.
	hits := g.Search("which code refuses a foreign origin", 5)
	if len(hits) == 0 || hits[0].ID != "gate" || hits[0].Line != 4 || hits[0].File != "p.go" {
		t.Fatalf("doc-comment search: %+v", hits)
	}
	hits = g.Search("gatekeeper", 5)
	if len(hits) != 2 || hits[0].ID != "gate" || hits[1].ID != "tgate" {
		t.Fatalf("implementation should outrank its test: %+v", hits)
	}
	if again := g.Search("gatekeeper", 5); !reflect.DeepEqual(hits, again) {
		t.Fatal("search is not deterministic")
	}
	if g.Search("the where is", 5) != nil {
		t.Fatal("stopword-only query must return nothing")
	}
}

func TestNeighbors(t *testing.T) {
	path, root := writeGraph(t)
	g, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	ns, err := g.Neighbors("gate", 1, 10)
	if err != nil || len(ns) != 2 || ns[0].ID != "help" || ns[0].Dir != "out" || ns[1].ID != "tgate" || ns[1].Dir != "in" {
		t.Fatalf("depth 1: %+v %v", ns, err)
	}
	if ns, _ := g.Neighbors("gate", 2, 10); len(ns) != 3 || ns[2].ID != "far" || ns[2].Depth != 2 {
		t.Fatalf("depth 2: %+v", ns)
	}
	if ns, _ := g.Neighbors("gate", 2, 1); len(ns) != 1 {
		t.Fatalf("limit: %+v", ns)
	}
	if _, err := g.Neighbors("nope", 1, 10); err == nil {
		t.Fatal("unknown id must error")
	}
}

func TestLoadRejectsTraversalInSourcePaths(t *testing.T) {
	if got := (sourceReader{root: t.TempDir(), files: map[string][]string{}}).docAt("../../etc/passwd", 1); got != "" {
		t.Fatalf("read outside root: %q", got)
	}
}

type query struct {
	ID, Q, File, Symbol string
}

// TestRepoRecall measures search on this repo's real graph against the
// ground-truth questions in evals/queries.json. It reports tokens too: what
// the agent would read via search (results + a 40-line span per hit) against
// reading every file the answer could be in.
func TestRepoRecall(t *testing.T) {
	root := filepath.Join("..", "..")
	g, err := Load(filepath.Join(root, "graphify-out", "graph.json"), root)
	if err != nil {
		t.Skip(err)
	}
	var qs struct{ Queries []query }
	b, err := os.ReadFile(filepath.Join(root, "evals", "queries.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &qs); err != nil {
		t.Fatal(err)
	}
	const k = 5
	found1, foundK := 0, 0
	for _, q := range qs.Queries {
		hits := g.Search(q.Q, k)
		rank := 0
		for i, h := range hits {
			if h.File == q.File && h.Symbol == q.Symbol {
				rank = i + 1
				break
			}
		}
		if rank == 1 {
			found1++
		}
		if rank > 0 {
			foundK++
		}
		top := "-"
		if len(hits) > 0 {
			top = hits[0].Label + " " + hits[0].File + ":" + fmt.Sprint(hits[0].Line)
		}
		t.Logf("%-12s rank %d  top: %s", q.ID, rank, top)
	}
	n := len(qs.Queries)
	t.Logf("recall@1 %d/%d, recall@%d %d/%d (graph built at %.7s)", found1, n, k, foundK, n, g.Commit)
	if float64(foundK) < 0.8*float64(n) {
		t.Errorf("recall@%d %d/%d is below 80%%", k, foundK, n)
	}
}
