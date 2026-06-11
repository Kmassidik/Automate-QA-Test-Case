// Package aiclient calls the qa-ai service. The queue's single worker is the
// only caller, so qa-ai never sees concurrent requests.
package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"qa-core/internal/contract"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

// Generate posts the form to qa-ai's /generate and returns the full result.
func (c *Client) Generate(ctx context.Context, req contract.GenerateRequest) (contract.Result, error) {
	return c.post(ctx, "/generate", req)
}

// Validate posts to qa-ai's /validate (step 1 of the two-step flow) and returns a
// partial result (analysis + ambiguities + requirement health only).
func (c *Client) Validate(ctx context.Context, req contract.GenerateRequest) (contract.Result, error) {
	return c.post(ctx, "/validate", req)
}

// post is the shared request path. It honors ctx (the per-job timeout) in
// addition to the client timeout, and surfaces qa-ai's error body verbatim.
func (c *Client) post(ctx context.Context, path string, req contract.GenerateRequest) (contract.Result, error) {
	buf, err := json.Marshal(req)
	if err != nil {
		return contract.Result{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return contract.Result{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return contract.Result{}, fmt.Errorf("qa-ai unreachable: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// Surface qa-ai's error message (e.g. retry-exhaustion) verbatim.
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("qa-ai status %d", resp.StatusCode)
		}
		return contract.Result{}, fmt.Errorf("generation failed: %s", e.Error)
	}

	var res contract.Result
	if err := json.Unmarshal(body, &res); err != nil {
		return contract.Result{}, fmt.Errorf("decode qa-ai result: %w", err)
	}
	return res, nil
}

// ModelInfo is one installed model (from qa-ai's /models, which proxies Ollama).
type ModelInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"` // bytes on disk
}

// ModelList is qa-ai's /models response: installed models plus qa-ai's default.
type ModelList struct {
	Default string      `json:"default"`
	Models  []ModelInfo `json:"models"`
}

// Models fetches the installed model list for the picker. Best-effort: a down
// backend yields an error the caller can degrade gracefully on.
func (c *Client) Models(ctx context.Context) (ModelList, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return ModelList{}, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return ModelList{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ModelList{}, fmt.Errorf("qa-ai /models status %d", resp.StatusCode)
	}
	var ml ModelList
	if err := json.NewDecoder(resp.Body).Decode(&ml); err != nil {
		return ModelList{}, fmt.Errorf("decode models: %w", err)
	}
	return ml, nil
}

// LoadedModel mirrors qa-ai's /stats loaded-model entry (Ollama /api/ps).
type LoadedModel struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	SizeVRAM int64  `json:"size_vram"`
}

// Stats is qa-ai's /stats body: host CPU/RAM and the models Ollama has loaded.
type Stats struct {
	CPUPercent float64       `json:"cpu_percent"`
	MemUsed    int64         `json:"mem_used"`
	MemTotal   int64         `json:"mem_total"`
	Loaded     []LoadedModel `json:"loaded"`
	Platform   string        `json:"platform"`
	Reachable  bool          `json:"-"` // set by the caller: did the request succeed
}

// Stats fetches qa-ai's /stats. Unreachable qa-ai yields Reachable=false.
func (c *Client) Stats(ctx context.Context) Stats {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/stats", nil)
	if err != nil {
		return Stats{}
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return Stats{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Stats{}
	}
	var s Stats
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return Stats{}
	}
	s.Reachable = true
	return s
}

// Health probes qa-ai's /healthz (which in turn probes Ollama).
func (c *Client) Health(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("qa-ai unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

// BackendStatus is qa-ai's /healthz body — the real generation backend's state,
// surfaced in the UI header so it reflects reality (model name / pulling / down)
// instead of a hard-coded string.
type BackendStatus struct {
	Status    string `json:"status"`   // ok | pulling | degraded
	State     string `json:"state"`    // ready | pulling | ollama_down | starting
	Detail    string `json:"detail"`   // human guidance/progress
	Model     string `json:"model"`    // e.g. "qwen2.5:7b" or "stub"
	Progress  string `json:"progress"` // e.g. "62%" while pulling
	Platform  string `json:"platform"` // e.g. "Linux (native)"
	Reachable bool   `json:"-"`        // set by the caller: did the request succeed
}

// Status fetches qa-ai's /healthz body. A non-nil error or unreachable qa-ai
// yields Reachable=false.
func (c *Client) Status(ctx context.Context) BackendStatus {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return BackendStatus{}
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return BackendStatus{} // Reachable stays false
	}
	defer resp.Body.Close()
	var s BackendStatus
	_ = json.NewDecoder(resp.Body).Decode(&s) // 503 still carries a useful body
	s.Reachable = true
	return s
}
