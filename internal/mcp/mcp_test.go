package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestServe(t *testing.T) {
	tools := []Tool{
		{Name: "echo", Description: "echo", InputSchema: map[string]any{"type": "object"},
			Call: func(_ context.Context, a json.RawMessage) (string, error) { return string(a), nil }},
		{Name: "fail", Call: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("nope") }},
	}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"x":1}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fail"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"bogus"}`,
		`not json`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(in), &out, "t", "0", tools); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 6 { // the notification gets no reply
		t.Fatalf("want 6 responses, got %d:\n%s", len(lines), out.String())
	}
	for i, want := range []string{
		`"protocolVersion":"2025-03-26"`,
		`"name":"echo"`,
		`"text":"{\"x\":1}"`,
		`"isError":true`,
		`"code":-32601`,
		`"code":-32700`,
	} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("response %d = %s; want it to contain %s", i, lines[i], want)
		}
	}
}
