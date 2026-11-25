// Package mcp is a minimal Model Context Protocol server over stdio:
// newline-delimited JSON-RPC 2.0 with initialize, ping, tools/list and
// tools/call. That is all the harness needs to hand Claude Code its Code Mode
// tools; resources, prompts and sampling are not implemented.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// Tool is one MCP tool. Call returns the text shown to the model; an error is
// reported as a tool error (isError), not a protocol error.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Call        func(ctx context.Context, args json.RawMessage) (string, error)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const maxLine = 8 << 20 // 8 MiB per message

// Serve reads requests from in and writes responses to out until in closes or
// ctx is done. Requests are handled one at a time, in order.
func Serve(ctx context.Context, in io.Reader, out io.Writer, name, version string, tools []Tool) error {
	byName := map[string]Tool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	enc := json.NewEncoder(out)
	send := func(r response) error {
		r.JSONRPC = "2.0"
		return enc.Encode(r)
	}

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), maxLine)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := send(response{ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 { // a notification: never answered
			continue
		}
		result, rerr := handle(ctx, req, name, version, tools, byName)
		if err := send(response{ID: req.ID, Result: result, Error: rerr}); err != nil {
			return err
		}
	}
	return sc.Err()
}

func handle(ctx context.Context, req request, name, version string, tools []Tool, byName map[string]Tool) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = "2025-06-18"
		}
		return map[string]any{
			"protocolVersion": p.ProtocolVersion, // we use nothing version-specific, so agree to the client's
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": name, "version": version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			list = append(list, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": t.InputSchema})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		t, ok := byName[p.Name]
		if !ok {
			return nil, &rpcError{-32602, fmt.Sprintf("unknown tool %q", p.Name)}
		}
		if len(p.Arguments) == 0 {
			p.Arguments = json.RawMessage("{}")
		}
		text, err := t.Call(ctx, p.Arguments)
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		return toolResult(text, false), nil
	default:
		return nil, &rpcError{-32601, "method not found: " + req.Method}
	}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError}
}
