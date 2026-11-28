package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// conceptEmbedder maps words to concept axes, so synonyms land together:
// what a real model does, deterministically.
type conceptEmbedder struct{ calls, texts atomic.Int64 }

var concepts = map[string]int{"refuse": 0, "reject": 0, "foreign": 1, "origin": 1, "cross": 1, "helper": 2, "assist": 2}

func (c *conceptEmbedder) Name() string { return "concept" }
func (c *conceptEmbedder) Embed(_ context.Context, texts []string, _ bool) ([][]float32, error) {
	c.calls.Add(1)
	c.texts.Add(int64(len(texts)))
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 4)
		v[3] = 0.01
		for _, w := range strings.Fields(strings.ToLower(t)) {
			if d, ok := concepts[strings.Trim(w, ".,?()")]; ok {
				v[d]++
			}
		}
		out[i] = v
	}
	return out, nil
}

func TestSemanticFindsSynonymsAndCaches(t *testing.T) {
	path, root := writeGraph(t)
	cache.g = nil // fresh load: vectors live on the cached graph
	g, err := Load(path, root)
	if err != nil {
		t.Fatal(err)
	}
	e := &conceptEmbedder{}
	ctx := context.Background()
	dir := t.TempDir()
	// "reject cross" shares no keyword with Gatekeeper's doc ("refuses ... foreign origins").
	if lex := g.Search("reject cross", 3); len(lex) != 0 {
		t.Fatalf("keyword search should miss: %+v", lex)
	}
	hits, err := g.Find(ctx, "reject cross", 3, "semantic", e, dir)
	if err != nil || len(hits) == 0 || hits[0].ID != "gate" {
		t.Fatalf("semantic: %+v %v", hits, err)
	}

	// A fresh graph value re-reads the on-disk cache: only the query is embedded.
	cache.g = nil
	g2, _ := Load(path, root)
	e2 := &conceptEmbedder{}
	if st, err := g2.Embed(ctx, e2, dir); err != nil || st.New != 0 || e2.texts.Load() != 0 {
		t.Fatalf("cache not reused: %+v texts=%d err=%v", st, e2.texts.Load(), err)
	}
	if _, err := g.Find(ctx, "x", 3, "fuzzy", e, dir); err == nil {
		t.Fatal("unknown mode must error")
	}
	if _, err := g.Find(ctx, "x", 3, "semantic", nil, dir); err == nil {
		t.Fatal("semantic without an embedder must error, not silently fall back")
	}
}

func TestOllamaClient(t *testing.T) {
	if _, err := NewOllama("http://example.com:11434", "m", "", ""); err == nil {
		t.Fatal("non-loopback embedding server must be refused")
	}
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&req)
		got = req.Input
		vs := make([][]float32, len(req.Input))
		for i := range vs {
			vs[i] = []float32{1, 0}
		}
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": vs})
	}))
	defer srv.Close()
	o, err := NewOllama(srv.URL, "m", "q: ", "d: ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Embed(context.Background(), []string{"a", "b"}, true); err != nil || got[0] != "q: a" {
		t.Fatalf("query prefix: %v %v", got, err)
	}
	if _, err := o.Embed(context.Background(), []string{"a"}, false); err != nil || got[0] != "d: a" {
		t.Fatalf("doc prefix: %v %v", got, err)
	}
}

// screeningEmbedder refuses any batch holding "helper", the way a provider's
// content screening refuses a batch for one text in it.
type screeningEmbedder struct {
	conceptEmbedder
	outage bool
}

func (s *screeningEmbedder) Embed(ctx context.Context, texts []string, q bool) ([][]float32, error) {
	if s.outage {
		return nil, &statusError{503, "down"}
	}
	for _, t := range texts {
		if strings.Contains(t, "helper") {
			return nil, &statusError{422, "input rejected by content safety screening"}
		}
	}
	return s.conceptEmbedder.Embed(ctx, texts, q)
}

func TestRefusedTextsAreSkippedNotFatal(t *testing.T) {
	path, root := writeGraph(t)
	cache.g = nil
	g, _ := Load(path, root)
	dir := t.TempDir()
	st, err := g.Embed(context.Background(), &screeningEmbedder{}, dir)
	if err != nil || st.Skipped != 1 || st.New != len(g.Nodes)-1 {
		t.Fatalf("stats %+v err %v", st, err)
	}
	if hits, err := g.Find(context.Background(), "reject cross", 3, "semantic", &screeningEmbedder{}, dir); err != nil || hits[0].ID != "gate" {
		t.Fatalf("search after a refusal: %+v %v", hits, err)
	}
	// The refusal is cached: a reload resends nothing.
	cache.g = nil
	g2, _ := Load(path, root)
	e2 := &screeningEmbedder{}
	if st, err := g2.Embed(context.Background(), e2, dir); err != nil || st.New+st.Skipped != 0 || e2.texts.Load() != 0 {
		t.Fatalf("refusal not cached: %+v texts=%d %v", st, e2.texts.Load(), err)
	}
	// An outage is not a refusal: it stops embedding with an error.
	cache.g = nil
	g3, _ := Load(path, root)
	if _, err := g3.Embed(context.Background(), &screeningEmbedder{outage: true}, t.TempDir()); err == nil {
		t.Fatal("a 503 must fail, not mark texts as refused")
	}
}
