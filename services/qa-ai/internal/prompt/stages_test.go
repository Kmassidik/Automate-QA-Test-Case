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

func TestBuildAux(t *testing.T) {
	_, user := BuildAux(contract.GenerateRequest{Requirement: "x", ApplicationType: "Web"})
	if !strings.Contains(user, `"edge_cases"`) || !strings.Contains(user, `"test_data"`) {
		t.Error("aux stage must request edge_cases + test_data")
	}
}
