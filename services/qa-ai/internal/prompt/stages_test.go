package prompt

import (
	"strings"
	"testing"

	"qa-ai/internal/contract"
)

func TestBuildAnalysisAndACs(t *testing.T) {
	_, user := BuildAnalysisAndACs(contract.GenerateRequest{Requirement: "reset password", ApplicationType: "Web"})
	if !strings.Contains(user, `"acceptance_criteria"`) {
		t.Error("analysis stage must request acceptance_criteria")
	}
	if strings.Contains(user, `"test_cases"`) {
		t.Error("analysis stage must NOT request test_cases")
	}
	if !strings.Contains(user, "do not limit the count") {
		t.Error("analysis stage should tell the model not to cap ACs")
	}
}

func TestBuildTestCasesForAC(t *testing.T) {
	ac := contract.AcceptanceCriterion{ID: "AC-2", Description: "Link expires in 30m", Module: "Auth", Severity: "High"}
	_, user := BuildTestCasesForAC(contract.GenerateRequest{Requirement: "x", ApplicationType: "Web"}, ac, 5)
	if !strings.Contains(user, "AC-2") {
		t.Error("must reference the target AC id")
	}
	if !strings.Contains(user, `["AC-2"]`) {
		t.Error("must pin covers to the AC id")
	}
	if !strings.Contains(user, "TC-5") {
		t.Error("must number from the start index")
	}
	if strings.Contains(user, `"acceptance_criteria"`) {
		t.Error("test-case stage should only request test_cases")
	}
}

func TestBuildTestCasesForACCap(t *testing.T) {
	ac := contract.AcceptanceCriterion{ID: "AC-1", Description: "x"}
	// Explicit cap honored + a few-shot example with an Edge case is present.
	sys, user := BuildTestCasesForAC(contract.GenerateRequest{Requirement: "r", CasesPerType: 2}, ac, 1)
	if !strings.Contains(user, "up to 2 cases per nature") {
		t.Errorf("cap of 2 not in prompt: %q", user)
	}
	if !strings.Contains(user, `"type": "Edge case"`) {
		t.Error("few-shot example should include an Edge-case-typed case")
	}
	if strings.Contains(sys, "SCORING POLICY") {
		t.Error("test-case stage should use the lean system prompt (no scoring policy)")
	}
	// Default when unset.
	_, def := BuildTestCasesForAC(contract.GenerateRequest{Requirement: "r"}, ac, 1)
	if !strings.Contains(def, "up to 3 cases per nature") {
		t.Errorf("default cap of 3 not in prompt: %q", def)
	}
}

func TestBuildAux(t *testing.T) {
	_, user := BuildAux(contract.GenerateRequest{Requirement: "x", ApplicationType: "Web"})
	if !strings.Contains(user, `"edge_cases"`) || !strings.Contains(user, `"test_data"`) {
		t.Error("aux stage must request edge_cases + test_data")
	}
}
