package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/devex/harness/internal/codemode"
	"github.com/devex/harness/internal/graph"
)

// cmdGraph: code-graph search from the shell (the same search agents get
// through Code Mode's graph.search).
func cmdGraph(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	fs := flag.NewFlagSet("graph "+args[0], flag.ExitOnError)
	configPath := fs.String("config", "harness.json", "path to harness.json")
	mode := fs.String("mode", "", "lexical|semantic (search; default semantic when embeddings are available)")
	k := fs.Int("k", 5, "results")
	queries := fs.String("queries", "evals/queries.json", "recall: question file")
	fs.Parse(args[1:])

	cfg, root := loadRoot(*configPath)
	env := codemode.Env{Root: root, Cfg: cfg}
	g, err := env.Graph()
	fatal(err)
	emb, err := env.Embedder()
	fatal(err)
	ctx := context.Background()

	switch args[0] {
	case "embed":
		if emb == nil {
			fatal(fmt.Errorf("no embeddings: set \"embeddings\" in %s, or pick an LLM provider with an embeddings API (and a saved key)", *configPath))
		}
		start := time.Now()
		st, err := g.Embed(ctx, emb, env.EmbedCache())
		fatal(err)
		fmt.Printf("%s: %d of %d nodes newly embedded, %d refused by the provider (keyword-only), in %v (cache %s)\n",
			emb.Name(), st.New, len(g.Nodes), st.Skipped, time.Since(start).Round(time.Millisecond), env.EmbedCache())
	case "search":
		q := strings.Join(fs.Args(), " ")
		m := *mode
		if m == "" && emb != nil {
			m = "semantic"
		}
		hits, err := g.Find(ctx, q, *k, m, emb, env.EmbedCache())
		fatal(err)
		for _, h := range hits {
			fmt.Printf("%7.3f  %-36s %s:%d\n", h.Score, h.Label, h.File, h.Line)
		}
	case "recall":
		graphRecall(ctx, g, emb, env.EmbedCache(), *queries, *k)
	default:
		usage()
		os.Exit(2)
	}
}

type evalQuery struct {
	ID      string     `json:"id"`
	Q       string     `json:"q"`
	File    string     `json:"file"`
	Symbol  string     `json:"symbol"`
	Require [][]string `json:"require"` // every group must be named; any name in a group counts
}

func loadQueries(path string) []evalQuery {
	b, err := os.ReadFile(path)
	fatal(err)
	var f struct{ Queries []evalQuery }
	fatal(json.Unmarshal(b, &f))
	for i, q := range f.Queries {
		if len(q.Require) == 0 {
			f.Queries[i].Require = [][]string{{q.Symbol}}
		}
	}
	return f.Queries
}

// graphRecall reports, per mode, the share of required symbols found in the
// top k results and how many questions had all of them.
func graphRecall(ctx context.Context, g *graph.Graph, emb graph.Embedder, cacheDir, path string, k int) {
	qs := loadQueries(path)
	modes := []string{"lexical"}
	if emb != nil {
		modes = append(modes, "semantic")
	}
	for _, mode := range modes {
		var found, total, complete int
		var misses []string
		for _, q := range qs {
			hits, err := g.Find(ctx, q.Q, k, mode, emb, cacheDir)
			fatal(err)
			got := 0
			for _, group := range q.Require {
				if hitAny(hits, group, q.File) {
					got++
				}
			}
			found, total = found+got, total+len(q.Require)
			if got == len(q.Require) {
				complete++
			} else {
				misses = append(misses, fmt.Sprintf("%s %d/%d", q.ID, got, len(q.Require)))
			}
		}
		fmt.Printf("%-8s recall@%d %5.1f%% (%d/%d symbols), complete %d/%d  misses: %s\n",
			mode, k, 100*float64(found)/float64(total), found, total, complete, len(qs), strings.Join(misses, ", "))
	}
}

func hitAny(hits []graph.Hit, names []string, file string) bool {
	for _, h := range hits {
		for _, n := range names {
			if h.Symbol == n && (file == "" || h.File == file) {
				return true
			}
		}
	}
	return false
}
