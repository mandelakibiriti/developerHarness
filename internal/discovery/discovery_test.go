package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "server.log")
	if err := os.WriteFile(p, []byte("some noise\nFloorplan: http://127.0.0.1:4321\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := FromFile(p, `Floorplan: (http://\S+)`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:4321" {
		t.Fatalf("got %q", got)
	}
}

func TestFromFileNoMatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "server.log")
	if err := os.WriteFile(p, []byte("nothing here"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := FromFile(p, `Floorplan: (http://\S+)`); err == nil {
		t.Fatal("expected no-match error")
	}
}

func TestAtPath(t *testing.T) {
	v := map[string]any{"a": map[string]any{"b": "c"}}
	got, err := AtPath(v, "a.b")
	if err != nil || got != "c" {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := AtPath(v, "a.z"); err == nil {
		t.Fatal("expected error for missing key")
	}
}
