// Package graph searches the codebase knowledge graph (graphify-out/graph.json)
// so an agent can find the few nodes, and the file:line spans, that answer a
// question instead of reading whole files.
//
// Search is lexical BM25 over each node's name, file path, and the doc comment
// and declaration line read from the source; with an Embedder (embed.go) it can
// also rank by embedding similarity ("semantic"). Every mode is an
// exact scan: at this graph's size (about 1k nodes) that takes a millisecond,
// so there is no ANN index. FAISS was evaluated and only pays off at roughly
// 100k vectors.
package graph

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Node is one graph node with the text it is searched by.
type Node struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Symbol    string `json:"symbol,omitempty"` // bare name: ".Execute()" -> "Execute"
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Community int    `json:"community"`
	Doc       string `json:"doc,omitempty"` // declaration line + doc comment, often enough to answer without reading the file
	terms     []string
	text      string // what an embedding model sees
}

// Edge is a link as stored: source -relation-> target.
type Edge struct {
	Source, Target, Relation string
}

// Graph is a loaded, indexed graph.
type Graph struct {
	Commit string // built_at_commit
	Nodes  []Node
	byID   map[string]int
	out    map[string][]Edge
	in     map[string][]Edge
	df     map[string]int
	avgLen float64

	vecMu sync.Mutex
	vecs  map[string][][]float32 // embedder name -> unit vector per node
}

// Field weights: a name match is worth more than a word in a comment.
const (
	labelWeight = 3
	pathWeight  = 1
	docWeight   = 1
	maxDocLines = 12
	maxDocChars = 280
	testPenalty = 0.5 // "where is X implemented" should find X, not TestX
	bm25K1      = 1.2
	bm25B       = 0.75
)

type rawGraph struct {
	Commit string `json:"built_at_commit"`
	Nodes  []struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		File      string `json:"source_file"`
		Location  string `json:"source_location"`
		Community int    `json:"community"`
	} `json:"nodes"`
	Links []struct {
		Source   string `json:"source"`
		Target   string `json:"target"`
		Relation string `json:"relation"`
	} `json:"links"`
}

var cache struct {
	sync.Mutex
	path string
	mod  time.Time
	g    *Graph
}

// Load reads and indexes graph.json; root resolves node source files. The
// result is cached until the file changes.
func Load(path, root string) (*Graph, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("no code graph at %s (build it with /graphify): %w", path, err)
	}
	cache.Lock()
	defer cache.Unlock()
	if cache.g != nil && cache.path == path && cache.mod.Equal(st.ModTime()) {
		return cache.g, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw rawGraph
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	g := &Graph{Commit: raw.Commit, byID: map[string]int{}, out: map[string][]Edge{}, in: map[string][]Edge{}, df: map[string]int{}}
	src := sourceReader{root: root, files: map[string][]string{}}
	total := 0
	for _, rn := range raw.Nodes {
		n := Node{ID: rn.ID, Label: rn.Label, Symbol: symbolName(rn.Label), File: rn.File, Community: rn.Community}
		n.Line = lineOf(rn.Location)
		if n.Symbol != "" && !src.declares(rn.File, n.Line, n.Symbol) {
			if l := src.find(rn.File, n.Symbol); l > 0 { // no line, or the file moved on since the graph was built
				n.Line = l
			}
		}
		n.terms = repeat(Terms(rn.Label), labelWeight)
		n.terms = append(n.terms, repeat(Terms(rn.File), pathWeight)...)
		doc := src.docAt(rn.File, n.Line)
		n.Doc = clip(doc, maxDocChars)
		n.terms = append(n.terms, repeat(Terms(doc), docWeight)...)
		n.text = strings.TrimSpace(rn.Label + " (" + rn.File + ")\n" + doc)
		seen := map[string]bool{}
		for _, t := range n.terms {
			if !seen[t] {
				seen[t] = true
				g.df[t]++
			}
		}
		total += len(n.terms)
		g.byID[n.ID] = len(g.Nodes)
		g.Nodes = append(g.Nodes, n)
	}
	if len(g.Nodes) > 0 {
		g.avgLen = float64(total) / float64(len(g.Nodes))
	}
	for _, l := range raw.Links {
		e := Edge{l.Source, l.Target, l.Relation}
		g.out[e.Source] = append(g.out[e.Source], e)
		g.in[e.Target] = append(g.in[e.Target], e)
	}
	cache.path, cache.mod, cache.g = path, st.ModTime(), g
	return g, nil
}

// Hit is one search result.
type Hit struct {
	Node
	Score float64 `json:"score"`
}

// Search returns the k best keyword (BM25) matches for q, best first; ties
// break on id so the same graph and query always give the same answer.
func (g *Graph) Search(q string, k int) []Hit {
	return topK(g.lexical(q), k)
}

// lexical scores every node that shares a term with q.
func (g *Graph) lexical(q string) []Hit {
	qt := uniq(Terms(q))
	if len(qt) == 0 {
		return nil
	}
	n := float64(len(g.Nodes))
	var hits []Hit
	for _, node := range g.Nodes {
		tf := map[string]int{}
		for _, t := range node.terms {
			tf[t]++
		}
		var s float64
		for _, t := range qt {
			f := float64(tf[t])
			if f == 0 {
				continue
			}
			df := float64(g.df[t])
			idf := math.Log(1 + (n-df+0.5)/(df+0.5))
			s += idf * f * (bm25K1 + 1) / (f + bm25K1*(1-bm25B+bm25B*float64(len(node.terms))/g.avgLen))
		}
		if s == 0 {
			continue
		}
		if isTest(node.File) {
			s *= testPenalty
		}
		hits = append(hits, Hit{Node: node, Score: math.Round(s*100) / 100})
	}
	return hits
}

// topK sorts hits best first and keeps k, merging hits that point at the same
// file:line.
func topK(hits []Hit, k int) []Hit {
	if k <= 0 {
		return nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	// The graph often holds one symbol twice (AST and semantic passes): keep the best.
	seen := map[string]bool{}
	out := hits[:0]
	for _, h := range hits {
		key := h.ID
		if h.File != "" && h.Line > 0 {
			key = h.File + ":" + strconv.Itoa(h.Line)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, h)
		if len(out) == k {
			break
		}
	}
	return out
}

// Neighbor is a node reached from the start node.
type Neighbor struct {
	Node
	Relation string `json:"relation"`
	Dir      string `json:"dir"` // "out": start -> this, "in": this -> start
	Depth    int    `json:"depth"`
}

// Neighbors walks edges both ways up to depth hops, returning at most limit
// nodes in breadth-first order.
func (g *Graph) Neighbors(id string, depth, limit int) ([]Neighbor, error) {
	if _, ok := g.byID[id]; !ok {
		return nil, fmt.Errorf("no node %q (use graph.search to find ids)", id)
	}
	seen := map[string]bool{id: true}
	frontier := []string{id}
	var out []Neighbor
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []string
		for _, cur := range frontier {
			visit := func(other, rel, dir string) {
				i, ok := g.byID[other]
				if !ok || seen[other] || len(out) >= limit {
					return
				}
				seen[other] = true
				out = append(out, Neighbor{Node: g.Nodes[i], Relation: rel, Dir: dir, Depth: d})
				next = append(next, other)
			}
			for _, e := range g.out[cur] {
				visit(e.Target, e.Relation, "out")
			}
			for _, e := range g.in[cur] {
				visit(e.Source, e.Relation, "in")
			}
		}
		frontier = next
	}
	return out, nil
}

var locLine = regexp.MustCompile(`^L(\d+)$|:(\d+)$`)

// lineOf parses the graph's source_location: "L13" or "file.go:190".
func lineOf(loc string) int {
	m := locLine.FindStringSubmatch(strings.TrimSpace(loc))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1] + m[2])
	return n
}

// symbolName reduces a code label to its bare name; "" for files and prose.
func symbolName(label string) string {
	l := strings.TrimSuffix(strings.TrimSpace(label), "()")
	if l == "" || strings.ContainsAny(l, " /\\") || filepath.Ext(l) != "" && codeExt[filepath.Ext(l)] {
		return ""
	}
	if i := strings.LastIndexByte(l, '.'); i >= 0 {
		l = l[i+1:]
	}
	return l
}

var codeExt = map[string]bool{".go": true, ".md": true, ".ts": true, ".tsx": true, ".js": true, ".py": true, ".json": true, ".rs": true, ".css": true, ".html": true}

// sourceReader reads the doc comment and declaration line of a node.
type sourceReader struct {
	root  string
	files map[string][]string
}

func (s sourceReader) lines(rel string) []string {
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return nil
	}
	lines, ok := s.files[rel]
	if !ok {
		if f, err := os.Open(filepath.Join(s.root, rel)); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 64<<10), 1<<20)
			for sc.Scan() {
				lines = append(lines, sc.Text())
			}
			f.Close()
		}
		s.files[rel] = lines // a missing file caches as empty
	}
	return lines
}

func declRe(sym string) *regexp.Regexp {
	return regexp.MustCompile(`^\s*(export\s+)?(async\s+)?(func|type|class|def|interface|struct|const|var|let|fn|function)\b.*\b` + regexp.QuoteMeta(sym) + `\b`)
}

// declares reports whether line (1-based) of rel declares sym.
func (s sourceReader) declares(rel string, line int, sym string) bool {
	lines := s.lines(rel)
	return line >= 1 && line <= len(lines) && declRe(sym).MatchString(lines[line-1])
}

// find returns the 1-based line declaring sym in rel, or 0.
func (s sourceReader) find(rel, sym string) int {
	decl := declRe(sym)
	for i, l := range s.lines(rel) {
		if decl.MatchString(l) {
			return i + 1
		}
	}
	return 0
}

func (s sourceReader) docAt(rel string, line int) string {
	lines := s.lines(rel)
	if line < 1 || line > len(lines) {
		return ""
	}
	start := line - 1
	for start > 0 && line-start <= maxDocLines {
		t := strings.TrimSpace(lines[start-1])
		if !strings.HasPrefix(t, "//") && !strings.HasPrefix(t, "#") {
			break
		}
		start--
	}
	parts := make([]string, 0, line-start)
	for _, l := range lines[start:line] {
		parts = append(parts, strings.TrimSpace(l))
	}
	return strings.Join(parts, " ")
}

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be by can do does for from func go how i if in into is it
		its of on or that the this to was what when where which who why with code function method file implement
		implemented implements handled handles handle defined happen happens there they them their our we you`) {
		stop[w] = true
	}
}

// Terms splits text into lowercase, crudely stemmed search terms, breaking
// identifiers at camelCase, snake_case and path boundaries.
func Terms(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			w := strings.ToLower(string(cur))
			if !stop[w] && len(w) > 1 {
				out = append(out, stem(w))
			}
			cur = cur[:0]
		}
	}
	rs := []rune(s)
	for i, r := range rs {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush() // fooBar | HTTPServer -> HTTP Server
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

// stem strips one common suffix so promote, promoted and promotion meet.
func stem(w string) string {
	for _, suf := range []string{"ations", "ation", "ated", "ates", "ate", "ions", "ion", "ing", "ers", "er", "ed", "es", "s", "e"} {
		if strings.HasSuffix(w, suf) && len(w)-len(suf) >= 4 {
			return w[:len(w)-len(suf)]
		}
	}
	return w
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func repeat(ts []string, n int) []string {
	out := make([]string, 0, len(ts)*n)
	for i := 0; i < n; i++ {
		out = append(out, ts...)
	}
	return out
}

func uniq(ts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range ts {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func isTest(file string) bool {
	return strings.HasSuffix(file, "_test.go") || strings.Contains(file, ".test.") || strings.Contains(file, "/test/")
}
