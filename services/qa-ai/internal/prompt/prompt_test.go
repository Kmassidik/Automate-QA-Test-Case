package prompt

import (
	"strings"
	"testing"

	"qa-ai/internal/contract"
)

func TestBuildOmitsAPIBlockForNonAPI(t *testing.T) {
	_, user := Build(contract.GenerateRequest{Requirement: "x", ApplicationType: "Web"})
	if strings.Contains(user, `"api_tests":`) {
		t.Error("non-API request should not include api_tests in the schema")
	}
	if !strings.Contains(user, "This is NOT an API") {
		t.Error("expected non-API instruction")
	}
}

func TestBuildIncludesAPIBlockForAPI(t *testing.T) {
	_, user := Build(contract.GenerateRequest{Requirement: "x", ApplicationType: "API"})
	if !strings.Contains(user, `"api_tests":`) {
		t.Error("API request must include api_tests in the schema")
	}
}

func TestBuildInjectsDefaults(t *testing.T) {
	_, user := Build(contract.GenerateRequest{Requirement: "reset password", ApplicationType: "Web"})
	for _, want := range []string{"Detail level: Standard", "Output format: step-by-step", "reset password"} {
		if !strings.Contains(user, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}
