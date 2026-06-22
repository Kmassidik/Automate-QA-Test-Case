package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"qa-core/internal/contract"
)

// Transcribe streams an uploaded audio file to qa-ai POST /transcribe and returns
// the transcript + detected language. The audio is piped through, so a large
// recording isn't buffered fully in memory.
func (c *Client) Transcribe(ctx context.Context, filename string, audio io.Reader) (contract.TranscribeResult, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		var err error
		defer func() { _ = pw.CloseWithError(err) }()
		var part io.Writer
		if part, err = mw.CreateFormFile("audio", filename); err != nil {
			return
		}
		if _, err = io.Copy(part, audio); err != nil {
			return
		}
		err = mw.Close()
	}()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/transcribe", pr)
	if err != nil {
		return contract.TranscribeResult{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	var out contract.TranscribeResult
	if err := c.doJSON(httpReq, &out); err != nil {
		return contract.TranscribeResult{}, err
	}
	return out, nil
}

// Extract runs one MAP step: a transcript chunk -> partial notes.
func (c *Client) Extract(ctx context.Context, req contract.ExtractRequest) (contract.MOMExtract, error) {
	var out contract.MOMExtract
	if err := c.postJSON(ctx, "/mom/extract", req, &out); err != nil {
		return contract.MOMExtract{}, err
	}
	return out, nil
}

// Consolidate runs the REDUCE step: all partials -> final MOM.
func (c *Client) Consolidate(ctx context.Context, req contract.ConsolidateRequest) (contract.MOM, error) {
	var out contract.MOM
	if err := c.postJSON(ctx, "/mom/consolidate", req, &out); err != nil {
		return contract.MOM{}, err
	}
	return out, nil
}

// postJSON marshals reqBody, POSTs it, and decodes the response into out.
func (c *Client) postJSON(ctx context.Context, path string, reqBody, out any) error {
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	return c.doJSON(httpReq, out)
}

// doJSON executes the request and decodes a 200 body into out, surfacing qa-ai's
// error message verbatim on non-200.
func (c *Client) doJSON(httpReq *http.Request, out any) error {
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("qa-ai unreachable: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("qa-ai status %d", resp.StatusCode)
		}
		return fmt.Errorf("%s", e.Error)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode qa-ai response: %w", err)
	}
	return nil
}
