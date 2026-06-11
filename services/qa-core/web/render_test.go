package web

import (
	"bytes"
	"strings"
	"testing"

	"qa-core/internal/aiclient"
	"qa-core/internal/contract"
	"qa-core/internal/export"
	"qa-core/internal/queue"
)

func TestBackendView(t *testing.T) {
	cases := []struct {
		in        aiclient.BackendStatus
		wantLabel string
		wantState string
	}{
		{aiclient.BackendStatus{Reachable: false}, "LLM offline", "down"},
		{aiclient.BackendStatus{Reachable: true, State: "ready", Model: "qwen2.5:7b"}, "qwen2.5:7b", "ready"},
		{aiclient.BackendStatus{Reachable: true, State: "ready", Model: "stub"}, "stub backend", "stub"},
		{aiclient.BackendStatus{Reachable: true, State: "pulling", Progress: "62%"}, "downloading model 62%", "pulling"},
		{aiclient.BackendStatus{Reachable: true, State: "ollama_down"}, "LLM offline", "down"},
	}
	for _, c := range cases {
		got := backendView(c.in)
		if got.Label != c.wantLabel || got.State != c.wantState {
			t.Errorf("backendView(%+v) = %+v, want {%q %q}", c.in, got, c.wantLabel, c.wantState)
		}
	}
}

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
		"Backend":  backendView(aiclient.BackendStatus{Reachable: true, State: "ready", Model: "qwen2.5:7b"}),
		"Examples": examples,
		"Options":  formOptions,
		"Models":   ModelsView{OK: true, Active: "qwen2.5:7b", Available: []ModelChoice{{Name: "qwen2.5:7b", Size: "4.7 GB"}, {Name: "llama3.1:8b", Size: "4.9 GB"}}},
	})

	exec(t, p, "waiting.html", map[string]any{
		"Job": jobView(queue.JobView{ID: "abc", State: queue.StateQueued, Position: 3, ETAKnown: true}),
	})

	header, rows := export.QARepositoryRows(*sampleResult(), export.Options{PriorityScheme: "P0-P3"})
	out := exec(t, p, "result.html", map[string]any{
		"ID": "job1", "Result": sampleResult(), "Header": header, "Rows": rows,
	})
	for _, want := range []string{"AC-1", "TC-1", "Requirement Health", "/export/qa-csv/job1", "Expired token"} {
		if !strings.Contains(out, want) {
			t.Errorf("result.html missing %q", want)
		}
	}

	vout := exec(t, p, "validation.html", map[string]any{
		"ID":     "job2",
		"Result": sampleResult(),
		"Req":    contract.GenerateRequest{Requirement: "reset password", ApplicationType: "Web", TestTypes: []string{"Positive", "Negative"}, IncludePreconditions: true},
	})
	// Must show analysis + carry the form forward to /generate as hidden inputs.
	for _, want := range []string{"Requirement breakdown", "hx-post=\"/generate\"", "name=\"test_types\" value=\"Positive\"", "Generate test cases"} {
		if !strings.Contains(vout, want) {
			t.Errorf("validation.html missing %q", want)
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
		EdgeCases:         []contract.EdgeCase{{ID: "EC-1", Scenario: "Expired token", ExpectedResult: "Rejected"}},
		TestData:          contract.TestData{ValidEmails: []string{"a@b.com"}, InvalidEmails: []string{"x@"}, BoundaryValues: []string{"255 chars"}},
		CoverageMatrix:    []contract.CoverageRow{{RequirementPoint: "Token expiry", CoveredBy: []string{"TC-1"}, CoverageType: "Negative"}},
		CoverageSummary:   contract.CoverageSummary{Positive: 3, Negative: 2, EdgeCase: 1, Trivial: 1},
		Ambiguities:       []contract.Ambiguity{{Location: "policy", Issue: "undefined", Suggestion: "define", Severity: "Medium"}},
		RequirementHealth: contract.Score{Score: 82, Rating: "Good", Deductions: []contract.Deduction{{Reason: "validation unspecified", Points: -15}}},
		TraceabilityScore: contract.Score{Score: 88, Rating: "Minor Gaps", Deductions: []contract.Deduction{{Reason: "boundary gap", Points: -10}}},
		MissingAreas:      []string{"Error handling"},
	}
}
