package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Models lists the provider's model ids via GET /v1/models (OpenAI-compatible).
// The catalog may include ids that are not callable on a given account, so the
// caller should treat this as candidate list, not a guarantee.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	if c.cfg.APIKey == "" {
		return nil, fmt.Errorf("no API key set")
	}
	base := strings.TrimRight(c.cfg.BaseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("could not parse /models response")
	}
	ids := make([]string, 0, len(out.Data))
	for _, d := range out.Data {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

// Test verifies the key works and returns the candidate model list (best
// effort). Providers without a /models endpoint fall back to a tiny completion
// to at least confirm the key is valid.
func (c *Client) Test(ctx context.Context) ([]string, error) {
	if ids, err := c.Models(ctx); err == nil && len(ids) > 0 {
		return ids, nil
	}
	if _, _, err := c.Chat(ctx, []Message{{Role: "user", Content: "Reply with the single word: ok"}}); err != nil {
		return nil, err
	}
	return nil, nil // key works, but no model list
}
