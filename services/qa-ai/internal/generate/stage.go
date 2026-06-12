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

// GenerateStage runs ONE stage of the batched pipeline and returns a PARTIAL
// Result (only that stage's fields populated). qa-core's orchestrator assembles
// the stages into a full Result. Each stage is a small prompt, so large suites
// don't overflow context the way single-shot generation can.
func (g *Generator) GenerateStage(ctx context.Context, sr contract.StageRequest) (contract.Result, error) {
	var system, user string
	var check func(contract.Result) error

	switch sr.Stage {
	case contract.StageAnalysis:
		system, user = prompt.BuildAnalysisAndACs(sr.Req)
		check = func(r contract.Result) error {
			if len(r.AcceptanceCriteria) == 0 {
				return errors.New("acceptance_criteria is empty")
			}
			return nil
		}
	case contract.StageTestCases:
		if sr.AC == nil {
			return contract.Result{}, errors.New("stage test_cases requires an acceptance criterion")
		}
		system, user = prompt.BuildTestCasesForAC(sr.Req, *sr.AC, sr.StartIndex)
		check = func(r contract.Result) error {
			if len(r.TestCases) == 0 {
				return errors.New("test_cases is empty")
			}
			return nil
		}
	case contract.StageAux:
		system, user = prompt.BuildAux(sr.Req)
		check = func(contract.Result) error { return nil } // lenient: aux may be sparse
	default:
		return contract.Result{}, fmt.Errorf("unknown stage %q", sr.Stage)
	}

	res, err := g.runJSON(ctx, sr.Req.Model, system, user, check)
	if err != nil {
		return contract.Result{}, err
	}
	if sr.Stage == contract.StageAnalysis {
		scoring.ClampHealth(&res) // bound the health score; traceability is computed later by qa-core
	}
	return res, nil
}

// runJSON is the shared retry/extract/validate loop (1 + maxRetries LLM calls)
// parameterized by a per-stage check. Generate/Validate predate it and keep
// their own copies; new stage code uses this.
func (g *Generator) runJSON(ctx context.Context, model, system, user string, check func(contract.Result) error) (contract.Result, error) {
	var lastErr error
	for attempt := 0; attempt <= g.maxRetries; attempt++ {
		raw, err := g.llm.Chat(ctx, model, system, user)
		if err != nil {
			return contract.Result{}, fmt.Errorf("llm call failed: %w", err)
		}

		jsonStr, ok := extractJSONObject(raw)
		if !ok {
			lastErr = errors.New("no JSON object found in model output")
			user = correctiveFollowup(user, raw, lastErr)
			continue
		}
		var res contract.Result
		if jerr := json.Unmarshal([]byte(jsonStr), &res); jerr != nil {
			lastErr = fmt.Errorf("json decode: %w", jerr)
			user = correctiveFollowup(user, raw, lastErr)
			continue
		}
		if cerr := check(res); cerr != nil {
			lastErr = cerr
			user = correctiveFollowup(user, raw, cerr)
			continue
		}
		return res, nil
	}
	return contract.Result{}, fmt.Errorf("%w: %v", ErrExhausted, lastErr)
}
