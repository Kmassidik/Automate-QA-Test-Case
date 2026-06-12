package orchestrator

import (
	"context"
	"testing"

	"qa-core/internal/contract"
	"qa-core/internal/queue"
)

// fakeAI returns canned partial results per stage.
type fakeAI struct{ calls []contract.Stage }

func (f *fakeAI) Stage(_ context.Context, sr contract.StageRequest) (contract.Result, error) {
	f.calls = append(f.calls, sr.Stage)
	switch sr.Stage {
	case contract.StageAnalysis:
		return contract.Result{
			RequirementAnalysis: []contract.RequirementFeature{{Feature: "Reset", RiskLevel: "High"}},
			AcceptanceCriteria: []contract.AcceptanceCriterion{
				{ID: "AC-1", Description: "Link emailed", Module: "Auth", Severity: "High"},
				{ID: "AC-2", Description: "Link expires", Module: "Auth", Severity: "High"},
			},
			Ambiguities:       []contract.Ambiguity{{Severity: "Medium", Issue: "policy"}},
			RequirementHealth: contract.Score{Score: 80, Rating: "Good"},
		}, nil
	case contract.StageTestCases:
		// One positive + one negative per AC; model "forgot" covers on the 2nd.
		return contract.Result{TestCases: []contract.TestCase{
			{ID: "x", Title: "ok", Type: "Positive", Covers: []string{sr.AC.ID}},
			{ID: "y", Title: "bad", Type: "Negative"},
		}}, nil
	case contract.StageAux:
		return contract.Result{
			EdgeCases: []contract.EdgeCase{{ID: "EC-1", Scenario: "empty"}},
			TestData:  contract.TestData{ValidEmails: []string{"a@b.com"}},
		}, nil
	}
	return contract.Result{}, nil
}

func TestRunAssemblesBatches(t *testing.T) {
	ai := &fakeAI{}
	m := queue.New(queue.Config{Runner: GenRunner(ai, nil)})
	// drive Run directly via a job's Progress by using the manager's runner path:
	res, err := Run(context.Background(), contract.GenerateRequest{Requirement: "reset"}, mkProgress(m), ai, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(res.AcceptanceCriteria) != 2 {
		t.Fatalf("want 2 ACs, got %d", len(res.AcceptanceCriteria))
	}
	// 2 ACs × 2 cases = 4, renumbered TC-1..TC-4.
	if len(res.TestCases) != 4 {
		t.Fatalf("want 4 test cases, got %d", len(res.TestCases))
	}
	for i, tc := range res.TestCases {
		want := "TC-" + itoa(i+1)
		if tc.ID != want {
			t.Errorf("test case %d id = %q, want %q", i, tc.ID, want)
		}
		if len(tc.Covers) == 0 {
			t.Errorf("test case %s has no covers (should be pinned to its AC)", tc.ID)
		}
	}
	// Coverage summary: 2 positive + 2 negative.
	if res.CoverageSummary.Positive != 2 || res.CoverageSummary.Negative != 2 {
		t.Errorf("coverage summary = %+v, want 2 pos / 2 neg", res.CoverageSummary)
	}
	// Both ACs covered → no uncovered penalty; edge_case==0 → -10; medium amb → -4.
	if res.TraceabilityScore.Score != 86 {
		t.Errorf("traceability = %d, want 86 (100 -10 edge -4 amb)", res.TraceabilityScore.Score)
	}
	if len(res.EdgeCases) != 1 {
		t.Errorf("aux edge cases not merged")
	}
}

// mkProgress builds a Progress bound to a throwaway job (Run only needs the
// reporting side-effects, which are harmless here).
func mkProgress(m *queue.Manager) *queue.Progress {
	job, _ := m.Submit(queue.KindGenerate, contract.GenerateRequest{Requirement: "x"})
	return m.NewProgress(job)
}
