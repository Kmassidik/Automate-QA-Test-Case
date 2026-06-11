package generate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"qa-ai/internal/contract"
	"qa-ai/internal/prompt"
	"qa-ai/internal/scoring"
)

// Validate runs step 1 of the two-step flow: analyze/validate the requirement
// only and return a PARTIAL contract.Result (requirement_analysis, ambiguities,
// requirement_health, missing_areas populated; no test cases or coverage). It
// reuses Generate's retry/extract/corrective machinery. Total LLM calls = 1 + maxRetries.
func (g *Generator) Validate(ctx context.Context, req contract.GenerateRequest) (contract.Result, error) {
	system, user := prompt.BuildValidation(req)

	var lastErr error
	for attempt := 0; attempt <= g.maxRetries; attempt++ {
		raw, err := g.llm.Chat(ctx, system, user)
		if err != nil {
			return contract.Result{}, fmt.Errorf("llm call failed: %w", err)
		}

		res, perr := parseAndValidateValidation(raw)
		if perr == nil {
			scoring.ClampHealth(&res)
			return res, nil
		}
		lastErr = perr
		user = correctiveFollowup(user, raw, perr)
	}

	return contract.Result{}, fmt.Errorf("%w: %v", ErrExhausted, lastErr)
}

// parseAndValidateValidation decodes the validation JSON. Its only invariant is a
// non-empty requirement_analysis — the validation pass deliberately omits test
// cases, so the full validate() checks must NOT apply here.
func parseAndValidateValidation(raw string) (contract.Result, error) {
	jsonStr, ok := extractJSONObject(raw)
	if !ok {
		return contract.Result{}, errors.New("no JSON object found in model output")
	}
	var res contract.Result
	if err := json.Unmarshal([]byte(jsonStr), &res); err != nil {
		return contract.Result{}, fmt.Errorf("json decode: %w", err)
	}
	if len(res.RequirementAnalysis) == 0 {
		return contract.Result{}, errors.New("requirement_analysis is empty")
	}
	return res, nil
}
