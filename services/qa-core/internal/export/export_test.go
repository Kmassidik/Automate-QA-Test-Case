package export

import (
	"encoding/csv"
	"strings"
	"testing"

	"qa-core/internal/contract"
)

func fixture() contract.Result {
	return contract.Result{
		AcceptanceCriteria: []contract.AcceptanceCriterion{
			{ID: "AC-1", Description: "Link expires in 30m", Module: "Auth", Severity: "High", RiskLevel: "High"},
		},
		TestCases: []contract.TestCase{
			{ID: "TC-1", Title: "Expired link rejected", Type: "Negative", Technique: "Boundary Value Analysis",
				Preconditions: []string{"reset requested"}, Steps: []string{"wait 31m", "open link"},
				ExpectedResult: "rejected", Covers: []string{"AC-1"}, Format: "step-by-step", Risk: "Critical"},
		},
		TestData:       contract.TestData{InvalidEmails: []string{"x@", "y@"}, BoundaryValues: []string{"255"}},
		CoverageMatrix: []contract.CoverageRow{{RequirementPoint: "Token expiry", CoveredBy: []string{"TC-1"}, CoverageType: "Negative"}},
	}
}

func TestQARepositoryCSV(t *testing.T) {
	data, err := QARepositoryCSV(fixture(), Options{PriorityScheme: "P0-P3"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want header + 1 row, got %d rows", len(rows))
	}
	if len(rows[0]) != len(qaCSVHeader) {
		t.Fatalf("header width = %d, want %d", len(rows[0]), len(qaCSVHeader))
	}
	row := rows[1]
	// TC ID, Title, ReqID, ReqSummary, AC, Type, Technique, Priority...
	if row[0] != "TC-1" || row[4] != "AC-1" {
		t.Errorf("TC/AC mapping wrong: %v", row[:5])
	}
	if row[7] != "P0" { // Critical risk -> P0 under P0-P3
		t.Errorf("priority = %q, want P0", row[7])
	}
	if row[2] != "RP-1" { // synthesized requirement id
		t.Errorf("requirement id = %q, want RP-1", row[2])
	}
}

func TestJiraCSVValid(t *testing.T) {
	data, err := JiraCSV(fixture(), Options{PriorityScheme: "P0-P3"})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil {
		t.Fatalf("not valid CSV: %v", err)
	}
	if rows[0][0] != "Issue Type" {
		t.Errorf("first column = %q, want Issue Type", rows[0][0])
	}
	// 1 Story (AC) + 1 Test (TC) + header = 3.
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
}

func TestMarkdownContainsSections(t *testing.T) {
	md := string(Markdown(fixture(), Options{Requirement: "reset password"}))
	for _, want := range []string{"# QA Test Artifacts", "## Test Cases", "TC-1", "## Coverage", "reset password"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}
