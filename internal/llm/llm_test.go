package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatSuccessAndAuth(t *testing.T) {
	var gotAuth string
	var gotBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"hello there"}}],"usage":{"prompt_tokens":1000,"completion_tokens":200}}`))
	}))
	defer srv.Close()

	c := New(Config{Provider: "test", BaseURL: srv.URL, APIKey: "sk-test", Model: "m1"})
	got, usage, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if usage.PromptTokens != 1000 || usage.CompletionTokens != 200 {
		t.Fatalf("usage = %+v", usage)
	}
	if got != "hello there" {
		t.Fatalf("got %q", got)
	}
	if gotAuth != "Bearer sk-test" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotBody.Model != "m1" || len(gotBody.Messages) != 1 {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestChatEmptyContentFailsLoud(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning_content":"thinking..."}}]}`))
	}))
	defer srv.Close()
	c := New(Config{Provider: "test", BaseURL: srv.URL, APIKey: "k"})
	if _, _, err := c.Chat(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "budget reasoning") {
		t.Fatalf("expected reasoning_content error, got %v", err)
	}
}

func TestChatProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":{"message":"model not found"}}`))
	}))
	defer srv.Close()
	c := New(Config{Provider: "test", BaseURL: srv.URL, APIKey: "k"})
	if _, _, err := c.Chat(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("expected provider error, got %v", err)
	}
}

func TestChatNoKey(t *testing.T) {
	c := New(Config{Provider: "test", BaseURL: "http://x", APIKey: ""})
	if _, _, err := c.Chat(context.Background(), nil); err == nil {
		t.Fatal("expected no-key error")
	}
}

func TestLookup(t *testing.T) {
	p, ok := Lookup("nvidia")
	if !ok || p.BaseURL != "https://integrate.api.nvidia.com/v1" {
		t.Fatalf("nvidia provider: %+v ok=%v", p, ok)
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("expected miss")
	}
}

func TestCostUSD(t *testing.T) {
	u := Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000}
	if _, ok := (Config{}).CostUSD(u); ok {
		t.Fatal("no rates configured must report ok=false, not $0")
	}
	usd, ok := Config{USDPerMTokIn: 3, USDPerMTokOut: 15}.CostUSD(u)
	if !ok || usd != 10.5 {
		t.Fatalf("got %v %v, want 10.5 true", usd, ok)
	}
}

func TestChatMaxTokensAndThinkStripping(t *testing.T) {
	var gotMax int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			MaxTokens int `json:"max_tokens"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		gotMax = in.MaxTokens
		w.Write([]byte(`{"choices":[{"message":{"content":"<think>{not json}</think>\n{\"ok\":1}"}}]}`))
	}))
	defer srv.Close()
	for _, tc := range []struct{ set, want int }{{0, DefaultMaxTokens}, {16384, 16384}} {
		reply, _, err := New(Config{Provider: "x", Model: "m", BaseURL: srv.URL, APIKey: "k", MaxTokens: tc.set}).
			Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
		if err != nil || reply != `{"ok":1}` || gotMax != tc.want {
			t.Fatalf("set=%d: reply=%q max=%d err=%v", tc.set, reply, gotMax, err)
		}
	}
}
