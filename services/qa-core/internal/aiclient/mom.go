package aiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"qa-core/internal/contract"
)

// MOM streams an uploaded audio file to qa-ai's POST /mom (multipart) and returns
// the structured minutes + transcript. The audio is piped straight through, so a
// large recording isn't buffered fully in memory. model selects the Ollama model
// for the minutes generation ("" => qa-ai default).
func (c *Client) MOM(ctx context.Context, filename, model string, audio io.Reader) (contract.MOMResult, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	// Write the multipart body in a goroutine so the HTTP request can stream it.
	go func() {
		var err error
		defer func() { _ = pw.CloseWithError(err) }()
		if model != "" {
			if err = mw.WriteField("model", model); err != nil {
				return
			}
		}
		var part io.Writer
		if part, err = mw.CreateFormFile("audio", filename); err != nil {
			return
		}
		if _, err = io.Copy(part, audio); err != nil {
			return
		}
		err = mw.Close()
	}()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/mom", pr)
	if err != nil {
		return contract.MOMResult{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return contract.MOMResult{}, fmt.Errorf("qa-ai unreachable: %w", err)
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
		return contract.MOMResult{}, fmt.Errorf("%s", e.Error)
	}

	var res contract.MOMResult
	if err := json.Unmarshal(body, &res); err != nil {
		return contract.MOMResult{}, fmt.Errorf("decode qa-ai MOM result: %w", err)
	}
	return res, nil
}
