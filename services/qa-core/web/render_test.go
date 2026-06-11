package web

import (
	"bytes"
	"strings"
	"testing"

	"qa-core/internal/contract"
	"qa-core/internal/queue"
)

func newTmpl(t *testing.T) *parsedTemplates {
	t.Helper()
	tp, err := parseTemplates()
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	return &parsedTemplates{tp}
}

func exec(t *testing.T, p *parsedTemplates, name string, data any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := p.t.ExecuteTemplate(&buf, name, data); err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return buf.String()
}

func TestRenderAllTemplates(t *testing.T) {
	p := newTmpl(t)

	exec(t, p, "access.html", map[string]any{"Error": "bad code"})
	exec(t, p, "error.html", map[string]any{"Message": "boom"})

	exec(t, p, "index.html", map[string]any{
		"Status":   statusView(queue.Snapshot{Busy: true, QueueLen: 2}),
		"Examples": examples,
		"Options":  formOptions,
	})

	exec(t, p, "waiting.html", map[string]any{
		"Job": jobView(queue.JobView{ID: "abc", State: queue.StateQueued, Position: 3, ETAKnown: true}),
	})

	out := exec(t, p, "result.html", map[string]any{"ID": "job1", "Result": sampleResult()})
	for _, want := range []string{"AC-1", "TC-1", "Requirement Health", "/export/qa-csv/job1", "Expired token"} {
		if !strings.Contains(out, want) {
			t.Errorf("result.html missing %q", want)
		}
	}
}

func sampleResult() *contract.Result {
	return &contract.Result{
		RequirementAnalysis: []contract.RequirementFeature{
			{Feature: "Password Reset", SubRequirements: []string{"Email validation", "Token expiry"}, RiskLevel: "High"},
		},
		AcceptanceCriteria: []contract.AcceptanceCriterion{
			{ID: "AC-1", Description: "Reset link expires in 30m", Module: "Auth", Severity: "High", RiskLevel: "High"},
		},
		TestCases: []contract.TestCase{
			{ID: "TC-1", Title: "Expired reset link rejected", Type: "Negative", Technique: "Boundary Value Analysis",
				Preconditions: []string{"User requested reset"}, Steps: []string{"Wait 31m", "Open link"},
				ExpectedResult: "Link rejected", Covers: []string{"AC-1"}, Format: "step-by-step", Risk: "High"},
		},
		EdgeCases:       []contract.EdgeCase{{ID: "EC-1", Scenario: "Expired token", ExpectedResult: "Rejected"}},
		TestData:        contract.TestData{ValidEmails: []string{"a@b.com"}, InvalidEmails: []string{"x@"}, BoundaryValues: []string{"255 chars"}},
		CoverageMatrix:  []contract.CoverageRow{{RequirementPoint: "Token expiry", CoveredBy: []string{"TC-1"}, CoverageType: "Negative"}},
		CoverageSummary: contract.CoverageSummary{Positive: 3, Negative: 2, Boundary: 1, Security: 1},
		Ambiguities:     []contract.Ambiguity{{Location: "policy", Issue: "undefined", Suggestion: "define", Severity: "Medium"}},
		RequirementHealth: contract.Score{Score: 82, Rating: "Good", Deductions: []contract.Deduction{{Reason: "validation unspecified", Points: -15}}},
		TraceabilityScore: contract.Score{Score: 88, Rating: "Minor Gaps", Deductions: []contract.Deduction{{Reason: "boundary gap", Points: -10}}},
		MissingAreas:      []string{"Error handling"},
	}
}
