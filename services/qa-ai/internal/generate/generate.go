// Package generate orchestrates one generation: build prompt -> call Ollama ->
// extract + validate JSON against the contract -> clamp scores. Malformed JSON
// is retried with a corrective follow-up, capped at MaxRetries (PRD §7, §8),
// after which a clear error is surfaced to the caller.
package generate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"qa-ai/internal/contract"
	"qa-ai/internal/ollama"
	"qa-ai/internal/prompt"
	"qa-ai/internal/scoring"
)

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

// ErrExhausted is returned when the model never produced contract-valid JSON
// within the retry budget.
var ErrExhausted = errors.New("model did not return valid JSON within retry budget")

// Generate runs the full pipeline. Total LLM calls = 1 + maxRetries.
func (g *Generator) Generate(ctx context.Context, req contract.GenerateRequest) (contract.Result, error) {
	system, user := prompt.Build(req)

	var lastErr error
	for attempt := 0; attempt <= g.maxRetries; attempt++ {
		raw, err := g.llm.Chat(ctx, system, user)
		if err != nil {
			// Transport/timeout errors are not "bad JSON" — fail fast, don't burn retries.
			return contract.Result{}, fmt.Errorf("llm call failed: %w", err)
		}

		res, perr := parseAndValidate(raw, req)
		if perr == nil {
			scoring.Clamp(&res)
			return res, nil
		}
		lastErr = perr

		// Corrective retry: tell the model exactly what was wrong, keep the schema in view.
		user = correctiveFollowup(user, raw, perr)
	}

	return contract.Result{}, fmt.Errorf("%w: %v", ErrExhausted, lastErr)
}

// parseAndValidate extracts a JSON object from the model output and checks the
// minimum structural invariants the export layer relies on.
func parseAndValidate(raw string, req contract.GenerateRequest) (contract.Result, error) {
	jsonStr, ok := extractJSONObject(raw)
	if !ok {
		return contract.Result{}, errors.New("no JSON object found in model output")
	}

	// Lenient decode: a 7B model may sprinkle extra keys. Unknown fields are
	// harmless to the export layer, so tolerate them rather than burn a retry.
	var res contract.Result
	if err := json.Unmarshal([]byte(jsonStr), &res); err != nil {
		return contract.Result{}, fmt.Errorf("json decode: %w", err)
	}

	if err := validate(res, req); err != nil {
		return contract.Result{}, err
	}
	return res, nil
}

// validate enforces the invariants exports and the UI depend on. Kept minimal so
// a decent-but-imperfect 7B response still passes rather than looping to error.
func validate(r contract.Result, req contract.GenerateRequest) error {
	var problems []string

	if len(r.TestCases) == 0 {
		problems = append(problems, "test_cases is empty")
	}
	if len(r.AcceptanceCriteria) == 0 {
		problems = append(problems, "acceptance_criteria is empty")
	}
	for i, tc := range r.TestCases {
		if strings.TrimSpace(tc.ID) == "" {
			problems = append(problems, fmt.Sprintf("test_cases[%d].id is empty", i))
		}
		if strings.TrimSpace(tc.Title) == "" {
			problems = append(problems, fmt.Sprintf("test_cases[%d].title is empty", i))
		}
	}
	for i, ac := range r.AcceptanceCriteria {
		if strings.TrimSpace(ac.ID) == "" {
			problems = append(problems, fmt.Sprintf("acceptance_criteria[%d].id is empty", i))
		}
	}
	if req.IsAPI() && len(r.APITests) == 0 {
		problems = append(problems, "application type is API but api_tests is empty")
	}

	if len(problems) > 0 {
		return fmt.Errorf("contract validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

// extractJSONObject pulls the outermost {...} from model output, tolerating
// stray prose or ```json fences despite the system prompt forbidding them.
func extractJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start == -1 || end == -1 || end < start {
		return "", false
	}
	return s[start : end+1], true
}

func correctiveFollowup(prevUser, badOutput string, perr error) string {
	return prevUser + fmt.Sprintf(
		"\n\nYOUR PREVIOUS RESPONSE WAS REJECTED.\nError: %s\nReturn ONLY a single valid JSON object matching the schema above — no prose, no code fences, correct key names.",
		perr.Error(),
	)
}
