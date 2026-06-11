// Package ollama is a thin client over the native Ollama HTTP API
// (/api/chat, non-streaming). qa-ai reaches Ollama on the host via
// host.docker.internal:11434 (PRD §6).
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	model   string
	numCtx  int
	http    *http.Client
}

func New(baseURL, model string, numCtx int, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		model:   model,
		numCtx:  numCtx,
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) Model() string   { return c.model }
func (c *Client) BaseURL() string { return c.baseURL }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Stream   bool           `json:"stream"`
	Format   string         `json:"format,omitempty"` // "json" forces valid-JSON decoding
	Options  map[string]any `json:"options,omitempty"`
}

type chatResponse struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
	Error   string      `json:"error,omitempty"`
}

// Chat sends a system+user prompt and returns the assistant's raw content.
// model selects the Ollama model; an empty model falls back to the configured
// default. format="json" asks Ollama to constrain output to valid JSON; we still
// validate against the contract upstream because "valid JSON" != "contract-shaped JSON".
func (c *Client) Chat(ctx context.Context, model, system, user string) (string, error) {
	if strings.TrimSpace(model) == "" {
		model = c.model
	}
	reqBody := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream: false,
		Format: "json",
		Options: map[string]any{
			"temperature": 0.2, // low temp: structured/technical output wants determinism
			"num_ctx":     c.numCtx,
		},
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal chat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(buf))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call ollama: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama status %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}
	if cr.Error != "" {
		return "", fmt.Errorf("ollama error: %s", cr.Error)
	}
	return cr.Message.Content, nil
}

// Health reports whether Ollama is reachable (used by qa-ai's /healthz).
func (c *Client) Health(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

// HasModel reports whether the configured model is already downloaded
// (present in /api/tags). A model configured without a tag (e.g. "qwen2.5")
// also matches "<name>:latest".
func (c *Client) HasModel(ctx context.Context) (bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return false, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("ollama /api/tags status %d", resp.StatusCode)
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return false, fmt.Errorf("decode tags: %w", err)
	}
	want := c.model
	wantLatest := ""
	if !strings.Contains(want, ":") {
		wantLatest = want + ":latest"
	}
	for _, m := range tags.Models {
		if strings.EqualFold(m.Name, want) || (wantLatest != "" && strings.EqualFold(m.Name, wantLatest)) {
			return true, nil
		}
	}
	return false, nil
}

// ModelInfo is one installed model from /api/tags.
type ModelInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"` // bytes on disk
}

// ListModels returns the models installed in Ollama (/api/tags), newest first as
// Ollama returns them. Used to populate the model picker.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama /api/tags status %d", resp.StatusCode)
	}
	var tags struct {
		Models []ModelInfo `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("decode tags: %w", err)
	}
	return tags.Models, nil
}

// PullProgress is one streamed status update during a model pull.
type PullProgress struct {
	Status    string `json:"status"`
	Total     int64  `json:"total"`
	Completed int64  `json:"completed"`
	Error     string `json:"error"`
}

// Pull downloads the configured model via /api/pull, invoking onProgress for
// each streamed status line. It blocks until the pull completes or fails. The
// request honors ctx but uses its own client without the per-generation timeout
// (a cold pull of a 7B model can take many minutes).
func (c *Client) Pull(ctx context.Context, onProgress func(PullProgress)) error {
	body, _ := json.Marshal(map[string]any{"name": c.model, "stream": true})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// No timeout on the pull itself — bound only by ctx (process lifetime).
	puller := &http.Client{}
	resp, err := puller.Do(httpReq)
	if err != nil {
		return fmt.Errorf("call ollama pull: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama pull status %d: %s", resp.StatusCode, truncate(string(b), 300))
	}

	dec := json.NewDecoder(resp.Body)
	for {
		var p PullProgress
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode pull stream: %w", err)
		}
		if p.Error != "" {
			return fmt.Errorf("ollama pull error: %s", p.Error)
		}
		if onProgress != nil {
			onProgress(p)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
