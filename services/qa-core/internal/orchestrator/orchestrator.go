// Package orchestrator runs a generation as a sequence of small qa-ai calls
// instead of one big one: analyze + derive ACs, then a batch of test cases PER
// acceptance criterion, then aux (edge cases + test data), then deterministic
// coverage + traceability. Each step reports live progress (1..N) and each LLM
// call is small enough to fit context, so large suites don't truncate.
package orchestrator

import (
	"context"
	"fmt"

	"qa-core/internal/contract"
	"qa-core/internal/queue"
)

// maxACBatches bounds how many ACs get their own test-case batch. On CPU each
// batch is minutes, so this caps a single job's wall-time; ACs beyond it are
// still listed, just flagged as not-yet-covered (no silent truncation).
const maxACBatches = 15

// StageClient is the slice of aiclient the orchestrator needs (one method →
// trivially fakeable in tests).
type StageClient interface {
	Stage(ctx context.Context, sr contract.StageRequest) (contract.Result, error)
}

// GenRunner returns a queue.Runner that generates via the batched pipeline.
func GenRunner(ai StageClient) queue.Runner {
	return func(ctx context.Context, req contract.GenerateRequest, p *queue.Progress) (contract.Result, error) {
		return Run(ctx, req, p, ai)
	}
}

func Run(ctx context.Context, req contract.GenerateRequest, p *queue.Progress, ai StageClient) (contract.Result, error) {
	p.Plan("Analyzing requirement & deriving acceptance criteria")

	// Stage 1 — analysis + acceptance criteria.
	p.Start(0)
	analysis, err := ai.Stage(ctx, contract.StageRequest{Stage: contract.StageAnalysis, Req: req})
	if err != nil {
		p.Fail(0, err.Error())
		return contract.Result{}, fmt.Errorf("analysis stage: %w", err)
	}
	acs := analysis.AcceptanceCriteria
	p.Done(0, fmt.Sprintf("%d acceptance criteria", len(acs)))

	result := contract.Result{
		RequirementAnalysis: analysis.RequirementAnalysis,
		AcceptanceCriteria:  acs,
		Ambiguities:         analysis.Ambiguities,
		RequirementHealth:   analysis.RequirementHealth,
		MissingAreas:        analysis.MissingAreas,
	}

	// Stage 2 — test cases, one batch per AC (bounded).
	batched := acs
	if len(batched) > maxACBatches {
		batched = batched[:maxACBatches]
		result.MissingAreas = append(result.MissingAreas,
			fmt.Sprintf("Test cases not generated for %d additional acceptance criteria (batch cap)", len(acs)-maxACBatches))
	}

	var cases []contract.TestCase
	for i, ac := range batched {
		label := fmt.Sprintf("Test cases · AC %d of %d", i+1, len(batched))
		if ac.Module != "" {
			label += " — " + ac.Module
		}
		step := p.Append(label)
		p.Start(step)

		sr := contract.StageRequest{Stage: contract.StageTestCases, Req: req, AC: &batched[i], StartIndex: len(cases) + 1}
		out, serr := ai.Stage(ctx, sr)
		if serr != nil {
			// One AC failing shouldn't sink the whole job — note it and continue.
			p.Fail(step, serr.Error())
			continue
		}
		for _, tc := range out.TestCases {
			if len(tc.Covers) == 0 {
				tc.Covers = []string{ac.ID} // pin traceability if the model forgot
			}
			cases = append(cases, tc)
		}
		p.Done(step, fmt.Sprintf("%d cases", len(out.TestCases)))
		result.TestCases = renumber(cases)
		p.Partial(result) // publish work-in-progress so a streaming UI can fill rows
	}
	result.TestCases = renumber(cases)

	if len(result.TestCases) == 0 {
		return contract.Result{}, fmt.Errorf("no test cases were generated")
	}

	// Stage 3 — edge cases + test data (best-effort; never fails the job).
	auxStep := p.Append("Edge cases & test data")
	p.Start(auxStep)
	if aux, aerr := ai.Stage(ctx, contract.StageRequest{Stage: contract.StageAux, Req: req}); aerr != nil {
		p.Fail(auxStep, aerr.Error())
	} else {
		result.EdgeCases = aux.EdgeCases
		result.TestData = aux.TestData
		p.Done(auxStep, fmt.Sprintf("%d edge cases", len(aux.EdgeCases)))
	}

	// Stage 4 — coverage + traceability, computed deterministically (no LLM).
	covStep := p.Append("Coverage & traceability")
	p.Start(covStep)
	result.CoverageMatrix, result.CoverageSummary = computeCoverage(result.AcceptanceCriteria, result.TestCases)
	result.TraceabilityScore = computeTraceability(result.AcceptanceCriteria, result.TestCases, result.CoverageSummary, result.Ambiguities)
	p.Done(covStep, result.TraceabilityScore.Rating)

	return result, nil
}

// renumber assigns sequential TC IDs (TC-1..TC-N). Safe because test cases trace
// to ACs (not to each other), so coverage is computed after renumbering.
func renumber(cases []contract.TestCase) []contract.TestCase {
	out := make([]contract.TestCase, len(cases))
	for i, tc := range cases {
		tc.ID = fmt.Sprintf("TC-%d", i+1)
		out[i] = tc
	}
	return out
}
