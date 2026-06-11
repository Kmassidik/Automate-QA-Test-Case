package scoring

import (
	"testing"

	"qa-ai/internal/contract"
)

func TestClampRecomputesAndBounds(t *testing.T) {
	r := &contract.Result{
		RequirementHealth: contract.Score{
			Score:  999, // model lied; must be recomputed
			Rating: "bogus",
			Deductions: []contract.Deduction{
				{Reason: "validation rules unspecified", Points: -15},
				{Reason: "high ambiguity", Points: -10},
			},
		},
		TraceabilityScore: contract.Score{
			Deductions: []contract.Deduction{
				{Reason: "no negative testing", Points: -10},
				{Reason: "low ambiguity", Points: -2},
			},
		},
	}
	Clamp(r)

	if got := r.RequirementHealth.Score; got != 75 {
		t.Errorf("health score = %d, want 75", got)
	}
	if got := r.RequirementHealth.Rating; got != "Good" {
		t.Errorf("health rating = %q, want Good", got)
	}
	if got := r.TraceabilityScore.Score; got != 88 {
		t.Errorf("trace score = %d, want 88", got)
	}
	if got := r.TraceabilityScore.Rating; got != "Minor Gaps" {
		t.Errorf("trace rating = %q, want Minor Gaps", got)
	}
}

func TestClampFloorsAtZero(t *testing.T) {
	r := &contract.Result{
		RequirementHealth: contract.Score{
			Deductions: []contract.Deduction{
				{Reason: "a", Points: -80},
				{Reason: "b", Points: -50},
			},
		},
	}
	Clamp(r)
	if r.RequirementHealth.Score != 0 {
		t.Errorf("score = %d, want 0 (floored)", r.RequirementHealth.Score)
	}
	if r.RequirementHealth.Rating != "Not Test Ready" {
		t.Errorf("rating = %q, want Not Test Ready", r.RequirementHealth.Rating)
	}
}

func TestClampNormalizesPositivePoints(t *testing.T) {
	// A model that emits +15 instead of -15 should still be penalized.
	r := &contract.Result{
		RequirementHealth: contract.Score{
			Deductions: []contract.Deduction{{Reason: "sign slip", Points: 15}},
		},
	}
	Clamp(r)
	if r.RequirementHealth.Score != 85 {
		t.Errorf("score = %d, want 85", r.RequirementHealth.Score)
	}
}
