package export

import (
	"bytes"
	"encoding/csv"
	"strings"

	"qa-core/internal/contract"
)

// qaCSVHeader is the FROZEN QA Repository CSV schema (PRD §5.3): test-case-centric,
// import-ready for TestRail/Zephyr without schema changes.
var qaCSVHeader = []string{
	"TC ID", "Title", "Requirement ID", "Requirement Summary", "Acceptance Criteria ID",
	"Test Type", "Test Design Technique", "Priority", "Severity", "Risk",
	"Preconditions", "Test Data", "Steps", "Expected Result", "Coverage Type",
	"Traceability Links", "Status", "Notes",
}

// QARepositoryCSV renders one row per test case.
func QARepositoryCSV(r contract.Result, opt Options) ([]byte, error) {
	d := derive(r)

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(qaCSVHeader); err != nil {
		return nil, err
	}

	for i, tc := range r.TestCases {
		reqPoints := d.reqPointsByTC[tc.ID]
		coverageTypes := d.coverageTypeByTC[tc.ID]

		// Requirement ID is synthesized (the contract has no explicit per-TC
		// requirement ID): RP-n for the first covered requirement point, else "".
		reqID := ""
		if len(reqPoints) > 0 {
			reqID = synthReqID(r, reqPoints[0])
		}

		row := []string{
			fallback(tc.ID, "TC-"+itoa(i+1)),
			tc.Title,
			reqID,
			strings.Join(reqPoints, "; "),
			strings.Join(tc.Covers, "; "),
			tc.Type,
			tc.Technique,
			priorityForTC(tc, opt.PriorityScheme),
			d.severityForTC(tc),
			normalizeSeverity(tc.Risk),
			strings.Join(tc.Preconditions, "\n"),
			testDataForTC(tc, r.TestData),
			joinSteps(tc),
			tc.ExpectedResult,
			strings.Join(coverageTypes, "; "),
			traceabilityLinks(tc, reqPoints),
			"Not Run", // default execution status for manual runs
			"",
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}

	w.Flush()
	return buf.Bytes(), w.Error()
}

// synthReqID gives a stable RP-n id based on the requirement point's position in
// the coverage matrix.
func synthReqID(r contract.Result, point string) string {
	for i, row := range r.CoverageMatrix {
		if row.RequirementPoint == point {
			return "RP-" + itoa(i+1)
		}
	}
	return ""
}

// joinSteps numbers step-by-step cases and leaves Gherkin/checklist lines as-is.
func joinSteps(tc contract.TestCase) string {
	if strings.EqualFold(tc.Format, "gherkin") || strings.EqualFold(tc.Format, "checklist") {
		return strings.Join(tc.Steps, "\n")
	}
	var b strings.Builder
	for i, s := range tc.Steps {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(itoa(i + 1))
		b.WriteString(". ")
		b.WriteString(s)
	}
	return b.String()
}

func traceabilityLinks(tc contract.TestCase, reqPoints []string) string {
	parts := append([]string(nil), tc.Covers...)
	parts = append(parts, reqPoints...)
	return strings.Join(parts, "; ")
}

func fallback(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
