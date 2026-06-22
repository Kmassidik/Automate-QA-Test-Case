// Package mom turns a meeting transcript into a structured Minutes-of-Meeting
// (contract.MOM) using the LLM. It mirrors the generate package's approach: ask
// Ollama for JSON, extract + decode, retry on malformed output. The prompt is
// pinned to the user's frozen MOM template fields and instructs the model to
// write in the transcript's own language (so Indonesian audio → Indonesian MOM).
package mom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"qa-ai/internal/contract"
	"qa-ai/internal/ollama"
)

// ErrExhausted mirrors generate.ErrExhausted: the model never returned usable JSON.
var ErrExhausted = errors.New("model did not return valid MOM JSON within retry budget")

type Generator struct {
	llm        *ollama.Client
	maxRetries int
}

func New(llm *ollama.Client, maxRetries int) *Generator {
	if maxRetries < 0 {
		maxRetries = 0
	}
	return &Generator{llm: llm, maxRetries: maxRetries}
}

const system = `You are a precise meeting-minutes assistant. You read a raw meeting transcript and produce formal Minutes of Meeting (MOM) as a single JSON object.

Rules:
- Write ALL text fields in the SAME language as the transcript (e.g. Indonesian transcript -> Indonesian minutes). Set "language" to that language name in English (e.g. "Indonesian", "English").
- Use ONLY information present in the transcript. Do NOT invent attendees, dates, decisions, or numbers. If something is not stated, use an empty string "" (or an empty list).
- Be concise, factual and professional. Each discussion item is a distinct topic; "title" is a short bold heading (a few words) and "description" is a 1-3 sentence summary.
- "follow_ups" are concrete action items / things to follow up after the meeting.
- Output JSON only — no markdown, no commentary.`

func userPrompt(transcript string) string {
	return `Produce the Minutes of Meeting for the transcript below. Return EXACTLY this JSON shape:

{
  "date_time": "",          // e.g. "07-05-2026 / 14.00 WIB" if stated, else ""
  "location": "",           // e.g. "Jakarta / Offline Meeting" if stated, else ""
  "purpose": "",            // one short line: the purpose of the meeting
  "attendees": [],          // names of people present / introduced
  "discussions": [          // the main topics discussed, in order
    { "title": "", "description": "" }
  ],
  "follow_ups": [           // action items / things to follow up
    { "title": "", "description": "" }
  ],
  "prepared_by": "",        // name of the minute-taker if stated, else ""
  "language": ""            // language you wrote in, e.g. "Indonesian"
}

TRANSCRIPT:
"""
` + transcript + `
"""`
}

// Generate produces the MOM. Total LLM calls = 1 + maxRetries.
func (g *Generator) Generate(ctx context.Context, model, transcript string) (contract.MOM, error) {
	if strings.TrimSpace(transcript) == "" {
		return contract.MOM{}, errors.New("empty transcript")
	}
	user := userPrompt(transcript)
	var lastErr error
	for attempt := 0; attempt <= g.maxRetries; attempt++ {
		raw, err := g.llm.Chat(ctx, model, system, user)
		if err != nil {
			return contract.MOM{}, fmt.Errorf("llm call failed: %w", err)
		}
		jsonStr, ok := extractJSONObject(raw)
		if !ok {
			lastErr = errors.New("no JSON object found in model output")
			user = corrective(user, lastErr)
			continue
		}
		var m contract.MOM
		if jerr := json.Unmarshal([]byte(jsonStr), &m); jerr != nil {
			lastErr = fmt.Errorf("json decode: %w", jerr)
			user = corrective(user, lastErr)
			continue
		}
		if len(m.Discussions) == 0 && strings.TrimSpace(m.Purpose) == "" {
			lastErr = errors.New("empty minutes (no purpose and no discussions)")
			user = corrective(user, lastErr)
			continue
		}
		return m, nil
	}
	return contract.MOM{}, fmt.Errorf("%w: %v", ErrExhausted, lastErr)
}

func corrective(user string, err error) string {
	return user + "\n\nYour previous answer was rejected: " + err.Error() +
		". Reply with ONLY the JSON object in the exact shape above."
}

// extractJSONObject returns the substring from the first '{' to the last '}'.
// Tolerates a model that wraps JSON in prose or code fences.
func extractJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < 0 || end <= start {
		return "", false
	}
	return s[start : end+1], true
}
