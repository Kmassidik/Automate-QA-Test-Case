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

// bom is the UTF-8 byte order mark Excel needs to detect encoding.
const bom = "\ufeff"

// parseCSV strips the BOM (as a tolerant importer would) before parsing.
func parseCSV(t *testing.T, data []byte) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), bom))).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v", err)
	}
	return rows
}

// TestCSVExcelCompat guards the Excel-compatibility fix: both CSVs must start
// with a UTF-8 BOM and use CRLF record endings.
func TestCSVExcelCompat(t *testing.T) {
	for name, gen := range map[string]func() ([]byte, error){
		"qa":   func() ([]byte, error) { return QARepositoryCSV(fixture(), Options{PriorityScheme: "P0-P3"}) },
		"jira": func() ([]byte, error) { return JiraCSV(fixture(), Options{PriorityScheme: "P0-P3"}) },
	} {
		data, err := gen()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(string(data), bom) {
			t.Errorf("%s CSV missing UTF-8 BOM", name)
		}
		if !strings.Contains(string(data), "\r\n") {
			t.Errorf("%s CSV missing CRLF line endings", name)
		}
	}
}

func TestQARepositoryCSV(t *testing.T) {
	data, err := QARepositoryCSV(fixture(), Options{PriorityScheme: "P0-P3"})
	if err != nil {
		t.Fatal(err)
	}
	rows := parseCSV(t, data)
	if len(rows) != 2 {
		t.Fatalf("want header + 1 row, got %d rows", len(rows))
	}
	if len(rows[0]) != len(qaCSVHeader) || len(qaCSVHeader) != 13 {
		t.Fatalf("header width = %d, want 13", len(rows[0]))
	}
	row := rows[1]
	// 13-col template: TC ID, AC ID, Module/Feature, Title, Precondition, Steps,
	// Test Data, Expected, Priority, Severity, Type, Actual Result, Notes.
	if row[0] != "TC-1" {
		t.Errorf("TC ID = %q, want TC-1", row[0])
	}
	if row[1] != "AC-1" {
		t.Errorf("AC ID = %q, want AC-1", row[1])
	}
	if row[2] != "Auth" { // Module/Feature from covered AC
		t.Errorf("module = %q, want Auth", row[2])
	}
	if row[8] != "P0" { // Critical risk -> P0 under P0-P3
		t.Errorf("priority = %q, want P0", row[8])
	}
	if row[10] != "Negative" {
		t.Errorf("type = %q, want Negative", row[10])
	}
	if row[11] != "" { // Actual Result empty at generation
		t.Errorf("actual result = %q, want empty", row[11])
	}
}

func TestJiraCSVValid(t *testing.T) {
	data, err := JiraCSV(fixture(), Options{PriorityScheme: "P0-P3"})
	if err != nil {
		t.Fatal(err)
	}
	rows := parseCSV(t, data)
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
