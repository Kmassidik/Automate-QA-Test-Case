package export

import (
	"strings"

	"qa-core/internal/contract"
)

// qaCSVHeader is the FROZEN QA Repository CSV schema (PRD §5.3 / review.md §1.3):
// the 13-column best-practice manual-QA template. The on-screen result table and
// this CSV are derived from the SAME rows (QARepositoryRows) so they match 1:1.
var qaCSVHeader = []string{
	"TC ID", "AC ID", "Module/Feature", "Title/Scenario", "Precondition",
	"Test Steps", "Test Data", "Expected Result", "Priority (P0-P3)",
	"Severity (Critical-Low)", "Type", "Actual Result", "Notes",
}

// QARepositoryRows is the single source of truth for the QA table: it returns the
// header and one row per test case, in the frozen 13-column order. Both the CSV
// exporter and the result-page table render from this — guaranteeing the screen
// matches the spreadsheet exactly.
func QARepositoryRows(r contract.Result, opt Options) (header []string, rows [][]string) {
	d := derive(r)
	rows = make([][]string, 0, len(r.TestCases))
	for i, tc := range r.TestCases {
		rows = append(rows, []string{
			fallback(tc.ID, "TC-"+itoa(i+1)),      // TC ID
			strings.Join(tc.Covers, "; "),         // AC ID
			moduleForTC(d, tc),                    // Module/Feature
			tc.Title,                              // Title/Scenario
			strings.Join(tc.Preconditions, "\n"),  // Precondition
			joinSteps(tc),                         // Test Steps
			testDataForTC(tc, r.TestData),         // Test Data
			tc.ExpectedResult,                     // Expected Result
			priorityForTC(tc, opt.PriorityScheme), // Priority (P0-P3)
			d.severityForTC(tc),                   // Severity (Critical-Low)
			tc.Type,                               // Type
			tc.ActualResult,                       // Actual Result (empty at generation)
			"",                                    // Notes
		})
	}
	return qaCSVHeader, rows
}

// QARepositoryCSV renders the QA table as an Excel-friendly CSV (BOM + CRLF).
func QARepositoryCSV(r contract.Result, opt Options) ([]byte, error) {
	header, rows := QARepositoryRows(r, opt)

	buf, w := newExcelCSV()
	if err := w.Write(header); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// moduleForTC returns the module/feature for a test case: the first non-empty
// Module among the acceptance criteria it covers.
func moduleForTC(d derived, tc contract.TestCase) string {
	for _, acID := range tc.Covers {
		if ac, ok := d.acByID[acID]; ok && strings.TrimSpace(ac.Module) != "" {
			return ac.Module
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

func fallback(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
