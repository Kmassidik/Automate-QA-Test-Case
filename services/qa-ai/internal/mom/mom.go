// Package mom turns a meeting transcript into a structured Minutes-of-Meeting
// using the LLM, as two bounded stages so it scales to any meeting length:
//
//	Extract     (MAP)    — one transcript CHUNK -> partial notes (attendees,
//	                       discussions, action items found in THIS part).
//	Consolidate (REDUCE) — all partials -> final MOM (merge + dedupe).
//
// qa-core's orchestrator chunks the transcript and drives these. Each call is
// small, so context never overflows and the JSON stays reliable. The prompts pin
// the output language (Indonesian / English / detected) and hunt action items.
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

// ErrExhausted: the model never returned usable JSON within the retry budget.
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

// languageRule returns the instruction that pins the output language. An empty
// language means "match the source text" (whisper auto-detect fallback).
func languageRule(language string) string {
	if l := strings.TrimSpace(language); l != "" {
		return "Write ALL text in " + l + "."
	}
	return "Write ALL text in the SAME language as the transcript text."
}

const extractSystem = `You extract structured notes from ONE part of a meeting transcript. Output a single JSON object, no markdown, no commentary.
- Use ONLY information present in this part. Do NOT invent names, decisions, or numbers.
- "discussions": distinct topics discussed in this part. "title" is a short heading (a few words); "description" is a 1-2 sentence summary.
- "follow_ups": concrete ACTION ITEMS / next steps / things to follow up — anything someone will DO, decide, prepare, or check after the meeting (look for "perlu", "akan", "follow up", "need to", "will", "TODO", owners, deadlines).
- "attendees": names of people mentioned/introduced as present.
- If a list has nothing in this part, return [].`

func extractUser(language, chunk string) string {
	return languageRule(language) + `

Return EXACTLY this JSON shape:
{
  "attendees": [],
  "discussions": [ { "title": "", "description": "" } ],
  "follow_ups": [ { "title": "", "description": "" } ]
}

TRANSCRIPT PART:
"""
` + chunk + `
"""`
}

// Extract runs the MAP step on one chunk.
func (g *Generator) Extract(ctx context.Context, model, language, chunk string) (contract.MOMExtract, error) {
	if strings.TrimSpace(chunk) == "" {
		return contract.MOMExtract{}, nil
	}
	var out contract.MOMExtract
	err := g.callJSON(ctx, model, extractSystem, extractUser(language, chunk), &out)
	return out, err
}

const consolidateSystem = `You are given partial notes extracted from consecutive parts of ONE meeting, in order. Merge them into the final Minutes of Meeting as a single JSON object, no markdown, no commentary.
- MERGE and DEDUPE: combine discussion points about the same topic into one; drop exact duplicates; keep meaningful order.
- "attendees": the union of all names, unique.
- "follow_ups": all distinct action items / next steps, deduped.
- "purpose": one short line inferred from the overall discussion.
- "date_time", "location", "prepared_by": only if clearly stated in the notes, else "".
- Do NOT invent information that isn't in the notes.`

func consolidateUser(language string, partialsJSON string) string {
	return languageRule(language) + `
Also set "language" to the language name you wrote in (e.g. "Indonesian", "English").

Return EXACTLY this JSON shape:
{
  "date_time": "", "location": "", "purpose": "", "prepared_by": "", "language": "",
  "attendees": [],
  "discussions": [ { "title": "", "description": "" } ],
  "follow_ups": [ { "title": "", "description": "" } ]
}

PARTIAL NOTES (JSON, in meeting order):
` + partialsJSON
}

// Consolidate runs the REDUCE step over all chunk partials.
func (g *Generator) Consolidate(ctx context.Context, model, language string, partials []contract.MOMExtract) (contract.MOM, error) {
	b, err := json.Marshal(partials)
	if err != nil {
		return contract.MOM{}, fmt.Errorf("marshal partials: %w", err)
	}
	var out contract.MOM
	if err := g.callJSON(ctx, model, consolidateSystem, consolidateUser(language, string(b)), &out); err != nil {
		return contract.MOM{}, err
	}
	if strings.TrimSpace(out.Language) == "" {
		out.Language = language
	}
	return out, nil
}

// callJSON is the shared retry/extract/decode loop. Total LLM calls = 1 + maxRetries.
func (g *Generator) callJSON(ctx context.Context, model, system, user string, into any) error {
	var lastErr error
	for attempt := 0; attempt <= g.maxRetries; attempt++ {
		raw, err := g.llm.Chat(ctx, model, system, user)
		if err != nil {
			return fmt.Errorf("llm call failed: %w", err)
		}
		jsonStr, ok := extractJSONObject(raw)
		if !ok {
			lastErr = errors.New("no JSON object found in model output")
			user = corrective(user, lastErr)
			continue
		}
		if jerr := json.Unmarshal([]byte(jsonStr), into); jerr != nil {
			lastErr = fmt.Errorf("json decode: %w", jerr)
			user = corrective(user, lastErr)
			continue
		}
		return nil
	}
	return fmt.Errorf("%w: %v", ErrExhausted, lastErr)
}

func corrective(user string, err error) string {
	return user + "\n\nYour previous answer was rejected: " + err.Error() +
		". Reply with ONLY the JSON object in the exact shape requested."
}

// extractJSONObject returns the substring from the first '{' to the last '}',
// tolerating a model that wraps JSON in prose or code fences.
func extractJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < 0 || end <= start {
		return "", false
	}
	return s[start : end+1], true
}
