package events

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// safeRecorder is a concurrency-safe http.ResponseWriter + Flusher, so the SSE
// test can read the body while the handler is still streaming to it.
type safeRecorder struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	header http.Header
	status int
}

func newSafeRecorder() *safeRecorder {
	return &safeRecorder{header: make(http.Header), status: 200}
}

func (s *safeRecorder) Header() http.Header  { return s.header }
func (s *safeRecorder) WriteHeader(code int) { s.status = code }
func (s *safeRecorder) Flush()               {}
func (s *safeRecorder) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(b)
}
func (s *safeRecorder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestAppendTailBroadcast(t *testing.T) {
	log, err := Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	if err := log.Append(Event{Ev: "test", Tool: "x"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(log.Recent(100)) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("event not picked up by tailer")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ev := log.Recent(1)[0]
	if ev.Ev != "test" || ev.Tool != "x" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if ev.T == "" {
		t.Fatal("timestamp should be set")
	}
}

func TestSubscribeBroadcast(t *testing.T) {
	log, err := Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	id, ch := log.Subscribe()
	defer log.Unsubscribe(id)

	if err := log.Append(Event{Ev: "ping"}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if ev.Ev != "ping" {
			t.Fatalf("got %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no broadcast received")
	}
}

func TestReadAll(t *testing.T) {
	p := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Event{Ev: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(Event{Ev: "two"}); err != nil {
		t.Fatal(err)
	}
	log.Close()

	evs, err := ReadAll(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Ev != "one" || evs[1].Ev != "two" {
		t.Fatalf("events: %+v", evs)
	}
}

func TestSSE(t *testing.T) {
	log, err := Open(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	rec := newSafeRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		log.ServeHTTP(rec, req)
	}()

	if err := log.Append(Event{Ev: "sse-test"}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(rec.String(), "sse-test") {
		if time.Now().After(deadline) {
			t.Fatalf("SSE body missing event: %q", rec.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not exit after context cancel")
	}
}
