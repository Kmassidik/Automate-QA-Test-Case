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

func TestQARepositoryUsesModelSetFields(t *testing.T) {
	r := contract.Result{
		AcceptanceCriteria: []contract.AcceptanceCriterion{{ID: "AC-1", Module: "Auth", Severity: "Low"}},
		TestCases: []contract.TestCase{{
			ID: "TC-1", Title: "rate-limit reset requests", Type: "Negative",
			Covers: []string{"AC-1"}, Risk: "Low",
			Priority: "P1", Severity: "High", // explicit -> must win over derived
			Postconditions: []string{"account unlocked after window"},
			Tags:           []string{"Security", "Performance"},
		}},
	}
	header, rows := QARepositoryRows(r, Options{PriorityScheme: "P0-P3"})
	idx := func(name string) int {
		for i, h := range header {
			if h == name {
				return i
			}
		}
		t.Fatalf("column %q not found", name)
		return -1
	}
	row := rows[0]
	if got := row[idx("Priority (P0-P3)")]; got != "P1" {
		t.Errorf("priority = %q, want P1 (model-set, not derived P3)", got)
	}
	if got := row[idx("Severity (Critical-Low)")]; got != "High" {
		t.Errorf("severity = %q, want High (model-set, not AC's Low)", got)
	}
	if got := row[idx("Tags")]; got != "Security, Performance" {
		t.Errorf("tags = %q", got)
	}
	if got := row[idx("Postcondition")]; got != "account unlocked after window" {
		t.Errorf("postcondition = %q", got)
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
	if len(rows[0]) != len(qaCSVHeader) || len(qaCSVHeader) != 15 {
		t.Fatalf("header width = %d, want 15", len(rows[0]))
	}
	row := rows[1]
	// 15-col template: TC ID, AC ID, Module/Feature, Title, Precondition, Steps,
	// Test Data, Expected, Postcondition, Priority, Severity, Type, Tags,
	// Actual Result, Notes.
	if row[0] != "TC-1" {
		t.Errorf("TC ID = %q, want TC-1", row[0])
	}
	if row[1] != "AC-1" {
		t.Errorf("AC ID = %q, want AC-1", row[1])
	}
	if row[2] != "Auth" { // Module/Feature from covered AC
		t.Errorf("module = %q, want Auth", row[2])
	}
	if row[9] != "P0" { // Critical risk -> P0 under P0-P3 (derived; no explicit priority)
		t.Errorf("priority = %q, want P0", row[9])
	}
	if row[11] != "Negative" {
		t.Errorf("type = %q, want Negative", row[11])
	}
	if row[13] != "" { // Actual Result empty at generation
		t.Errorf("actual result = %q, want empty", row[13])
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
