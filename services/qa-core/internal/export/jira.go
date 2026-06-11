package export

import (
	"fmt"
	"strings"

	"qa-core/internal/contract"
)

// jiraCSVHeader is the FROZEN Jira CSV schema (PRD §5.3): issue-centric, kept
// SEPARATE from the QA CSV because Jira tracks issues, not test cases.
var jiraCSVHeader = []string{
	"Issue Type", "Summary", "Description", "Priority", "Labels",
	"Components", "Acceptance Criteria", "Risk", "Test Type",
}

// JiraCSV renders one issue per acceptance criterion (a Story), plus one Test
// issue per test case. This is the issue-centric shape Jira imports cleanly.
func JiraCSV(r contract.Result, opt Options) ([]byte, error) {
	d := derive(r)

	buf, w := newExcelCSV()
	if err := w.Write(jiraCSVHeader); err != nil {
		return nil, err
	}

	// Stories from acceptance criteria.
	for _, ac := range r.AcceptanceCriteria {
		row := []string{
			"Story",
			ac.Description,
			"Acceptance criterion " + ac.ID + " (module: " + fallback(ac.Module, "n/a") + ")",
			priorityFromSeverity(ac.Severity, opt.PriorityScheme),
			"qa-generated",
			ac.Module,
			ac.Description,
			normalizeSeverity(ac.RiskLevel),
			"",
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}

	// Test issues from test cases.
	for _, tc := range r.TestCases {
		desc := buildTestDescription(tc)
		row := []string{
			"Test",
			tc.Title,
			desc,
			priorityForTC(tc, opt.PriorityScheme),
			"qa-generated",
			strings.Join(d.reqPointsByTC[tc.ID], "; "),
			strings.Join(tc.Covers, "; "),
			normalizeSeverity(tc.Risk),
			tc.Type,
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}

	w.Flush()
	return buf.Bytes(), w.Error()
}

func buildTestDescription(tc contract.TestCase) string {
	var b strings.Builder
	if len(tc.Preconditions) > 0 {
		fmt.Fprintf(&b, "Preconditions:\n%s\n\n", strings.Join(tc.Preconditions, "\n"))
	}
	b.WriteString("Steps:\n")
	b.WriteString(joinSteps(tc))
	fmt.Fprintf(&b, "\n\nExpected: %s", tc.ExpectedResult)
	return b.String()
}

// priorityFromSeverity maps an AC severity onto the chosen scheme (same rules as
// priorityForTC but sourced from severity).
func priorityFromSeverity(severity, scheme string) string {
	return priorityForTC(contract.TestCase{Risk: severity}, scheme)
}
