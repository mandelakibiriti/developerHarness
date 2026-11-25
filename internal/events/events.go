// Package events implements the shared append-only event log and its SSE fan-out.
//
// The log is a JSONL file (.harness/events.jsonl). The server tails the file so
// events appended by anything — the supervisor, Claude Code hooks via jq, or
// gates — are broadcast to every timeline subscriber over Server-Sent Events.
package events

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Event is one JSON line in the log, keyed by session + sha + step when known.
type Event struct {
	T       string         `json:"t"`
	Ev      string         `json:"ev"`
	Session string         `json:"session,omitempty"`
	Tool    string         `json:"tool,omitempty"`
	Sha     string         `json:"sha,omitempty"`
	Step    int            `json:"step,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

const replayCap = 200

// Log tails a JSONL file and fans new events out to subscribers.
type Log struct {
	path      string
	file      *os.File // our append handle
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	offset    int64
	recent    []Event
	subs      map[int]chan Event
	nextID    int
}

// Open creates (if needed) the log file, seeds the replay buffer from its
// tail, and starts the tailer.
func Open(path string) (*Log, error) {
	dir := "."
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		dir = path[:i]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	l := &Log{
		path:   path,
		file:   f,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		offset: st.Size(),
		subs:   map[int]chan Event{},
	}
	l.loadRecent()
	go l.tailer()
	return l, nil
}

// Append writes one event to the log. The tailer broadcasts it.
func (l *Log) Append(ev Event) error {
	if ev.T == "" {
		ev.T = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.file.Write(b)
	return err
}

// Close stops the tailer and closes the file.
func (l *Log) Close() error {
	l.closeOnce.Do(func() {
		close(l.stop)
		<-l.done
	})
	return l.file.Close()
}

// Recent returns up to n most recent events (n <= 0 means all buffered).
func (l *Log) Recent(n int) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.recent
	if n > 0 && len(r) > n {
		r = r[len(r)-n:]
	}
	out := make([]Event, len(r))
	copy(out, r)
	return out
}

// Subscribe registers a subscriber channel (buffer 64). Events are dropped,
// not blocked, for slow consumers.
func (l *Log) Subscribe() (int, <-chan Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.nextID
	l.nextID++
	ch := make(chan Event, 64)
	l.subs[id] = ch
	return id, ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (l *Log) Unsubscribe(id int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ch, ok := l.subs[id]; ok {
		delete(l.subs, id)
		close(ch)
	}
}

func (l *Log) tailer() {
	defer close(l.done)
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			l.tail()
		}
	}
}

func (l *Log) tail() {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.path)
	if err != nil {
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return
	}
	if st.Size() < l.offset {
		l.offset = 0 // file truncated/rotated
	}
	if st.Size() == l.offset {
		return
	}
	if _, err := f.Seek(l.offset, 0); err != nil {
		return
	}
	buf := make([]byte, st.Size()-l.offset)
	if _, err := io.ReadFull(f, buf); err != nil {
		return
	}
	l.offset = st.Size()

	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		l.recent = append(l.recent, ev)
		if len(l.recent) > replayCap {
			l.recent = l.recent[len(l.recent)-replayCap:]
		}
		for _, ch := range l.subs {
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

// loadRecent seeds the replay buffer from the last replayCap lines on disk.
func (l *Log) loadRecent() {
	f, err := os.Open(l.path)
	if err != nil {
		return
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > replayCap {
		lines = lines[len(lines)-replayCap:]
	}
	for _, line := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		l.recent = append(l.recent, ev)
	}
}

// ReadAll parses every event line in a log file, oldest first. Unlike Recent,
// it is not capped to the replay buffer, so it covers the full history (used
// for evidence-based promotion).
func ReadAll(path string) ([]Event, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var evs []Event
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		evs = append(evs, ev)
	}
	return evs, nil
}

// ServeHTTP streams events as Server-Sent Events. It replays the recent buffer
// first, then streams live until the client disconnects.
func (l *Log) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	for _, ev := range l.Recent(replayCap) {
		writeEvent(w, ev)
	}
	fl.Flush()

	id, ch := l.Subscribe()
	defer l.Unsubscribe(id)

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			writeEvent(w, ev)
			fl.Flush()
		}
	}
}

func writeEvent(w io.Writer, ev Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
}
