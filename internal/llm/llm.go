// Package llm is the harness's model layer: a registry of hosted providers
// (Anthropic, NVIDIA NIM, DeepSeek, Gemini, OpenCode) behind one OpenAI-
// compatible chat client. System prompts are compiled from context/ by
// internal/compiler, not written here.
//
// Keys live in .harness/llm.json (git-ignored), never in code or logs. The
// client fails loudly: an empty or missing content field is an error, so a
// reasoning model that burned its budget on thinking never looks like a
// successful empty answer.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider is one hosted model endpoint. All are OpenAI-compatible
// (POST {base}/chat/completions with Authorization: Bearer).
type Provider struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	BaseURL string   `json:"base_url"`
	Models  []string `json:"models"`
}

// Providers is the catalog. Base URLs and model names are defaults; both are
// overridable in the saved config. NVIDIA NIM base is integrate.api.nvidia.com/v1.
var Providers = []Provider{
	{ID: "claude", Label: "Claude (Anthropic)", BaseURL: "https://api.anthropic.com/v1",
		Models: []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-2"}},
	{ID: "nvidia", Label: "NVIDIA NIM", BaseURL: "https://integrate.api.nvidia.com/v1",
		Models: []string{"deepseek-ai/deepseek-r1", "meta/llama-3.3-70b-instruct", "nvidia/llama-3.1-nemotron-70b-instruct"}},
	{ID: "deepseek", Label: "DeepSeek", BaseURL: "https://api.deepseek.com/v1",
		Models: []string{"deepseek-chat", "deepseek-reasoner"}},
	{ID: "gemini", Label: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
		Models: []string{"gemini-2.5-flash", "gemini-2.5-pro"}},
	{ID: "opencode", Label: "OpenCode", BaseURL: "https://opencode.ai/v1",
		Models: []string{"gpt-4o", "claude-sonnet-4-5"}},
}

// Lookup returns a provider by id.
func Lookup(id string) (Provider, bool) {
	for _, p := range Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Config is the saved model selection and key.
type Config struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"` // override the provider default
	APIKey   string `json:"api_key,omitempty"`  // never echoed back in responses
	// Optional prices (USD per million tokens). When set, each call also emits
	// a cost event so the budget sees this agent's spend; unset means tokens only.
	USDPerMTokIn  float64 `json:"usd_per_mtok_in,omitempty"`
	USDPerMTokOut float64 `json:"usd_per_mtok_out,omitempty"`
	// MaxTokens caps the reply (reasoning tokens included on reasoning models).
	// Per call, not saved; 0 means DefaultMaxTokens.
	MaxTokens int `json:"-"`
}

const DefaultMaxTokens = 4096

// Usage is the provider-reported token count for one call.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// CostUSD prices u with cfg's rates; ok is false when no rate is configured.
func (cfg Config) CostUSD(u Usage) (usd float64, ok bool) {
	if cfg.USDPerMTokIn == 0 && cfg.USDPerMTokOut == 0 {
		return 0, false
	}
	return (float64(u.PromptTokens)*cfg.USDPerMTokIn + float64(u.CompletionTokens)*cfg.USDPerMTokOut) / 1e6, true
}

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client calls one provider's OpenAI-compatible endpoint.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a client for cfg.
func New(cfg Config) *Client {
	// Long generations on reasoning models can run for minutes; callers bound
	// each call with their context (the UI's Stop button cancels it).
	return &Client{cfg: cfg, http: &http.Client{Timeout: 15 * time.Minute}}
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Chat sends messages and returns the assistant reply and token usage. An
// empty content field is an error (fail loud), including when a reasoning
// model returned only reasoning_content.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, Usage, error) {
	if c.cfg.APIKey == "" {
		return "", Usage{}, fmt.Errorf("no API key set for provider %q", c.cfg.Provider)
	}
	base := strings.TrimRight(c.cfg.BaseURL, "/")
	maxTokens := c.cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	body := chatRequest{Model: c.cfg.Model, Messages: messages, MaxTokens: maxTokens, Temperature: 0.7}
	b, err := json.Marshal(body)
	if err != nil {
		return "", Usage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", Usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", Usage{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", Usage{}, err
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", Usage{}, fmt.Errorf("provider returned non-JSON (HTTP %d): %s", resp.StatusCode, truncate(raw, 200))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if out.Error != nil && out.Error.Message != "" {
			msg = out.Error.Message
		}
		return "", Usage{}, fmt.Errorf("provider error (%d): %s", resp.StatusCode, msg)
	}
	if len(out.Choices) == 0 {
		return "", Usage{}, fmt.Errorf("provider returned no choices")
	}
	content := stripThink(out.Choices[0].Message.Content)
	if strings.TrimSpace(content) == "" {
		if out.Choices[0].Message.ReasoningContent != "" {
			return "", Usage{}, fmt.Errorf("model used its whole %d-token budget reasoning and returned no answer; try again or pick a non-reasoning model", maxTokens)
		}
		return "", Usage{}, fmt.Errorf("provider returned an empty message")
	}
	return content, out.Usage, nil
}

// stripThink drops an inline <think>…</think> block that some reasoning models
// (e.g. DeepSeek-R1 on NIM) put in content instead of reasoning_content.
func stripThink(s string) string {
	if i := strings.LastIndex(s, "</think>"); i >= 0 {
		return strings.TrimSpace(s[i+len("</think>"):])
	}
	return s
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
