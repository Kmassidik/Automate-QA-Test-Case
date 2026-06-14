// Package orchestrator runs a generation as a sequence of small qa-ai calls
// instead of one big one: analyze + derive ACs, then a batch of test cases PER
// acceptance criterion, then aux (edge cases + test data), then deterministic
// coverage + traceability. Each step reports live progress (1..N) and each LLM
// call is small enough to fit context, so large suites don't truncate.
package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

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

// Snapshotter persists a work-in-progress result after each step (e.g. a
// markdown temp file). Optional — nil means in-memory only.
type Snapshotter func(jobID string, res contract.Result)

// GenRunner returns a queue.Runner that generates via the batched pipeline.
// snap may be nil (memory-only temp store); tlog may be nil (timing disabled).
func GenRunner(ai StageClient, snap Snapshotter, tlog *slog.Logger) queue.Runner {
	return func(ctx context.Context, req contract.GenerateRequest, p *queue.Progress) (contract.Result, error) {
		return Run(ctx, req, p, ai, snap, tlog)
	}
}

func Run(ctx context.Context, req contract.GenerateRequest, p *queue.Progress, ai StageClient, snap Snapshotter, tlog *slog.Logger) (contract.Result, error) {
	jid := p.JobID()
	jobStart := time.Now()
	// tstage logs one timed step to the timing log (and is a no-op if disabled).
	tstage := func(kind string, started time.Time, extra ...any) {
		if tlog == nil {
			return
		}
		args := append([]any{"job", jid, "stage", kind, "dur", time.Since(started).Round(time.Millisecond)}, extra...)
		tlog.Info("stage", args...)
	}
	if tlog != nil {
		tlog.Info("job start", "job", jid, "model", modelOrDefault(req.Model),
			"cases_per_type", req.CasesPerTypeOrDefault(), "natures", len(req.TestTypes))
	}
	save := func(res contract.Result) {
		p.Partial(res) // in-memory temp store (source of truth)
		if snap != nil {
			snap(p.JobID(), res) // optional human-readable markdown trace
		}
	}

	// Step-2 review inputs: fold the QA's clarifications into the requirement so
	// every stage sees them.
	if strings.TrimSpace(req.Clarifications) != "" {
		req.Requirement = strings.TrimSpace(req.Requirement) +
			"\n\nADDITIONAL CLARIFICATIONS (from QA review):\n" + strings.TrimSpace(req.Clarifications)
	}
	p.Plan("Analyzing requirement & deriving acceptance criteria")

	// Stage 1 — analysis + acceptance criteria.
	p.Start(0)
	t := time.Now()
	analysis, err := ai.Stage(ctx, contract.StageRequest{Stage: contract.StageAnalysis, Req: req})
	if err != nil {
		tstage("analysis", t, "error", err.Error())
		p.Fail(0, err.Error())
		return contract.Result{}, fmt.Errorf("analysis stage: %w", err)
	}
	tstage("analysis", t, "acs", len(analysis.AcceptanceCriteria))
	acs := analysis.AcceptanceCriteria
	// If the QA curated the ACs on the review page, generate from EXACTLY those
	// (the analysis above still gives us accurate health/ambiguities/breakdown).
	if len(req.AcceptanceCriteria) > 0 {
		acs = req.AcceptanceCriteria
		p.Done(0, fmt.Sprintf("%d acceptance criteria (QA-curated)", len(acs)))
	} else {
		p.Done(0, fmt.Sprintf("%d acceptance criteria", len(acs)))
	}

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

		ts := time.Now()
		sr := contract.StageRequest{Stage: contract.StageTestCases, Req: req, AC: &batched[i], StartIndex: len(cases) + 1}
		out, serr := ai.Stage(ctx, sr)
		if serr != nil {
			// One AC failing shouldn't sink the whole job — note it and continue.
			tstage("test_cases", ts, "ac", ac.ID, "i", i+1, "of", len(batched), "error", serr.Error())
			p.Fail(step, serr.Error())
			continue
		}
		tstage("test_cases", ts, "ac", ac.ID, "i", i+1, "of", len(batched), "cases", len(out.TestCases))
		for _, tc := range out.TestCases {
			if len(tc.Covers) == 0 {
				tc.Covers = []string{ac.ID} // pin traceability if the model forgot
			}
			cases = append(cases, tc)
		}
		p.Done(step, fmt.Sprintf("%d cases", len(out.TestCases)))
		result.TestCases = renumber(cases)
		save(result) // publish WIP so a streaming UI fills rows + snapshot
	}
	result.TestCases = renumber(cases)

	if len(result.TestCases) == 0 {
		return contract.Result{}, fmt.Errorf("no test cases were generated")
	}

	// Stage 3 — edge cases + test data (best-effort; never fails the job).
	auxStep := p.Append("Edge cases & test data")
	p.Start(auxStep)
	tAux := time.Now()
	if aux, aerr := ai.Stage(ctx, contract.StageRequest{Stage: contract.StageAux, Req: req}); aerr != nil {
		tstage("aux", tAux, "error", aerr.Error())
		p.Fail(auxStep, aerr.Error())
	} else {
		result.EdgeCases = aux.EdgeCases
		result.TestData = aux.TestData
		tstage("aux", tAux, "edge_cases", len(aux.EdgeCases))
		p.Done(auxStep, fmt.Sprintf("%d edge cases", len(aux.EdgeCases)))
	}

	// Stage 4 — coverage + traceability, computed deterministically (no LLM).
	covStep := p.Append("Coverage & traceability")
	p.Start(covStep)
	result.CoverageMatrix, result.CoverageSummary = computeCoverage(result.AcceptanceCriteria, result.TestCases)
	result.TraceabilityScore = computeTraceability(result.AcceptanceCriteria, result.TestCases, result.CoverageSummary, result.Ambiguities)
	p.Done(covStep, result.TraceabilityScore.Rating)

	if tlog != nil {
		tlog.Info("job done", "job", jid, "total", time.Since(jobStart).Round(time.Millisecond),
			"test_cases", len(result.TestCases), "acs", len(batched),
			"cases_per_type", req.CasesPerTypeOrDefault(), "model", modelOrDefault(req.Model))
	}

	save(result) // final snapshot
	return result, nil
}

func modelOrDefault(m string) string {
	if strings.TrimSpace(m) == "" {
		return "(qa-ai default)"
	}
	return m
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
