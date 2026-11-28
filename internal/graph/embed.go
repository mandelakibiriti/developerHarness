package graph

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/devex/harness/internal/scope"
)

// Embedder turns texts into vectors. query marks search queries, which some
// models embed with a different task prefix than documents.
type Embedder interface {
	Name() string
	Embed(ctx context.Context, texts []string, query bool) ([][]float32, error)
}

const (
	embedBatch   = 32
	embedTimeout = 2 * time.Minute // per batch; the first batch also loads the model
	fusePool     = 50              // each ranking contributes its top fusePool to hybrid
	rrfK         = 60              // reciprocal rank fusion constant (Cormack et al.)
)

// Ollama embeds with a local Ollama server's /api/embed.
type Ollama struct {
	base                   *url.URL
	model                  string
	queryPrefix, docPrefix string
	client                 *http.Client
}

// NewOllama returns an embedder for base (which must be loopback: code never
// leaves the machine) and model.
func NewOllama(base, model, queryPrefix, docPrefix string) (*Ollama, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("embeddings url %q is not a URL", base)
	}
	if !scope.Loopback([]string{u.Hostname()}) {
		return nil, fmt.Errorf("embeddings url %q is not loopback: code must not leave this machine", base)
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("embeddings model is required")
	}
	return &Ollama{base: u, model: model, queryPrefix: queryPrefix, docPrefix: docPrefix, client: &http.Client{}}, nil
}

func (o *Ollama) Name() string { return "ollama-" + o.model }

func (o *Ollama) Embed(ctx context.Context, texts []string, query bool) ([][]float32, error) {
	prefix := o.docPrefix
	if query {
		prefix = o.queryPrefix
	}
	in := make([]string, len(texts))
	for i, t := range texts {
		in[i] = prefix + t
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := postJSON(ctx, o.client, o.base.JoinPath("api", "embed").String(), "", map[string]any{"model": o.model, "input": in}, &out); err != nil {
		return nil, fmt.Errorf("%w (is `ollama serve` running?)", err)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embedding server returned %d vectors for %d texts", len(out.Embeddings), len(texts))
	}
	return out.Embeddings, nil
}

// API embeds with a hosted provider's OpenAI-compatible POST {base}/embeddings,
// the same provider and key the harness agent already uses for chat.
type API struct {
	base, key, model string
	inputType        bool // NVIDIA NIM: input_type "query" | "passage" is required
	client           *http.Client
}

// NewAPI returns an embedder for a provider endpoint.
func NewAPI(base, key, model string, inputType bool) (*API, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("embeddings base url %q is not a URL", base)
	}
	if key == "" || model == "" {
		return nil, errors.New("embeddings need an API key and a model")
	}
	return &API{base: strings.TrimRight(base, "/"), key: key, model: model, inputType: inputType, client: &http.Client{}}, nil
}

func (a *API) Name() string { return "api-" + a.model }

func (a *API) Embed(ctx context.Context, texts []string, query bool) ([][]float32, error) {
	body := map[string]any{"model": a.model, "input": texts, "encoding_format": "float"}
	if a.inputType {
		body["input_type"], body["truncate"] = "passage", "END"
		if query {
			body["input_type"] = "query"
		}
	}
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := postJSON(ctx, a.client, a.base+"/embeddings", a.key, body, &out); err != nil {
		return nil, err
	}
	vs := make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vs) {
			return nil, fmt.Errorf("embeddings API returned index %d for %d texts", d.Index, len(texts))
		}
		vs[d.Index] = d.Embedding
	}
	for i, v := range vs {
		if len(v) == 0 {
			return nil, fmt.Errorf("embeddings API returned no vector for text %d", i)
		}
	}
	return vs, nil
}

// postJSON posts body and decodes a 200 JSON reply into out.
func postJSON(ctx context.Context, c *http.Client, endpoint, key string, body, out any) error {
	b, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(ctx, embedTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("embedding server not reachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &statusError{resp.StatusCode, strings.TrimSpace(string(raw))}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("embedding server returned non-JSON: %w", err)
	}
	return nil
}

type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("embedding server HTTP %d: %.300s", e.code, e.body)
}

// refused reports a provider declining the input itself (e.g. NVIDIA's 422
// "content safety screening"), as opposed to auth, rate limits or outages,
// which say nothing about the text and must stop embedding.
func refused(err error) bool {
	var se *statusError
	if !errors.As(err, &se) {
		return false
	}
	switch se.code {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

// EmbedStats counts one Embed call's work.
type EmbedStats struct {
	New     int // nodes embedded by this call
	Skipped int // nodes the provider refused; they stay findable by keyword
}

// Embed makes sure every node has a vector from e, reusing the on-disk cache
// in cacheDir (keyed by a hash of each node's text, so only changed nodes are
// re-embedded). A batch the provider refuses is retried one text at a time;
// a text still refused is cached as skipped, so it is not resent each call.
func (g *Graph) Embed(ctx context.Context, e Embedder, cacheDir string) (EmbedStats, error) {
	var st EmbedStats
	g.vecMu.Lock()
	defer g.vecMu.Unlock()
	if g.vecs[e.Name()] != nil {
		return st, nil
	}
	path := filepath.Join(cacheDir, safeName.ReplaceAllString(e.Name(), "_")+".gob")
	cached := map[string][]float32{}
	if f, err := os.Open(path); err == nil {
		err = gob.NewDecoder(f).Decode(&cached)
		f.Close()
		if err != nil {
			cached = map[string][]float32{} // a corrupt cache is rebuilt, not trusted
		}
	}
	keys := make([]string, len(g.Nodes))
	var missing []int
	for i, n := range g.Nodes {
		h := sha256.Sum256([]byte(n.text))
		keys[i] = hex.EncodeToString(h[:16])
		if _, ok := cached[keys[i]]; !ok {
			missing = append(missing, i)
		}
	}
	for start := 0; start < len(missing); start += embedBatch {
		end := min(start+embedBatch, len(missing))
		texts := make([]string, 0, end-start)
		for _, i := range missing[start:end] {
			texts = append(texts, g.Nodes[i].text)
		}
		vs, err := e.Embed(ctx, texts, false)
		if err != nil && refused(err) {
			vs, err = embedEach(ctx, e, texts)
		}
		if err != nil {
			if start > 0 { // keep finished batches: the next call resumes, not restarts
				_ = writeGob(path, cached)
			}
			return st, err
		}
		for j, i := range missing[start:end] {
			cached[keys[i]] = vs[j]
			if len(vs[j]) == 0 {
				st.Skipped++
			} else {
				st.New++
			}
		}
	}
	if len(missing) > 0 {
		if err := writeGob(path, cached); err != nil {
			return st, err
		}
	}
	vecs := make([][]float32, len(g.Nodes))
	for i, k := range keys {
		vecs[i] = unit(cached[k]) // empty for a skipped node: it scores 0 semantically
	}
	if g.vecs == nil {
		g.vecs = map[string][][]float32{}
	}
	g.vecs[e.Name()] = vecs
	return st, nil
}

// embedEach embeds texts one by one; a refused text gets an empty vector.
func embedEach(ctx context.Context, e Embedder, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := e.Embed(ctx, []string{t}, false)
		switch {
		case err == nil:
			out[i] = v[0]
		case refused(err):
			out[i] = []float32{}
		default:
			return nil, err
		}
	}
	return out, nil
}

var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func writeGob(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".embed-*")
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(tmp).Encode(v); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// semantic scores every node by cosine similarity to q.
func (g *Graph) semantic(ctx context.Context, q string, e Embedder, cacheDir string) ([]Hit, error) {
	if _, err := g.Embed(ctx, e, cacheDir); err != nil {
		return nil, err
	}
	qv, err := e.Embed(ctx, []string{q}, true)
	if err != nil {
		return nil, err
	}
	u := unit(qv[0])
	g.vecMu.Lock()
	vecs := g.vecs[e.Name()]
	g.vecMu.Unlock()
	hits := make([]Hit, 0, len(g.Nodes))
	for i, n := range g.Nodes {
		if len(vecs[i]) == 0 {
			continue // refused by the provider when embedding
		}
		if len(vecs[i]) != len(u) {
			return nil, fmt.Errorf("embedding size changed (%d vs %d): delete %s", len(vecs[i]), len(u), cacheDir)
		}
		var s float64
		for j, x := range vecs[i] {
			s += float64(x * u[j])
		}
		if isTest(n.File) {
			s *= testPenalty
		}
		hits = append(hits, Hit{Node: n, Score: math.Round(s*1000) / 1000})
	}
	return hits, nil
}

// Find searches in one of three modes: "lexical" (BM25, no embedder needed),
// "semantic" (embedding cosine), or "hybrid" (reciprocal rank fusion of both).
func (g *Graph) Find(ctx context.Context, q string, k int, mode string, e Embedder, cacheDir string) ([]Hit, error) {
	if mode == "" || mode == "lexical" {
		return g.Search(q, k), nil
	}
	if mode != "semantic" && mode != "hybrid" {
		return nil, fmt.Errorf("mode must be lexical, semantic or hybrid, not %q", mode)
	}
	if e == nil {
		return nil, errors.New(`no embeddings configured in harness.json; use mode "lexical"`)
	}
	sem, err := g.semantic(ctx, q, e, cacheDir)
	if err != nil {
		return nil, err
	}
	if mode == "semantic" {
		return topK(sem, k), nil
	}
	fused := map[string]*Hit{}
	var order []string
	for _, list := range [][]Hit{topK(g.lexical(q), fusePool), topK(sem, fusePool)} {
		for rank, h := range list {
			f := fused[h.ID]
			if f == nil {
				f = &Hit{Node: h.Node}
				fused[h.ID] = f
				order = append(order, h.ID)
			}
			f.Score += 1.0 / float64(rrfK+rank+1)
		}
	}
	out := make([]Hit, 0, len(order))
	for _, id := range order {
		h := *fused[id]
		h.Score = math.Round(h.Score*1e5) / 1e3 // ×100, so scores read like 3.2 not 0.032
		out = append(out, h)
	}
	return topK(out, k), nil
}

func unit(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	if n == 0 {
		return v
	}
	inv := float32(1 / math.Sqrt(n))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}
