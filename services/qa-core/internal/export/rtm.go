package export

import (
	"sort"
	"strings"

	"qa-core/internal/contract"
)

// rtmHeader is the Requirement Traceability Matrix — a standard QA deliverable
// (review.md #2). One row per requirement (acceptance criterion), showing which
// test cases cover it and whether it's covered or a gap. Bidirectional: the QA
// table maps test→requirement (the "AC ID" column); this maps requirement→tests.
var rtmHeader = []string{
	"Requirement ID", "Requirement", "Module", "Severity",
	"Covered By (Test Cases)", "Coverage Types", "# Tests", "Status",
}

// RTMRows is the single source for the RTM table + CSV (one row per AC).
func RTMRows(r contract.Result) (header []string, rows [][]string) {
	rows = make([][]string, 0, len(r.AcceptanceCriteria))
	for _, ac := range r.AcceptanceCriteria {
		var covering []string
		typeSet := map[string]bool{}
		for _, tc := range r.TestCases {
			if tcCovers(tc, ac.ID) {
				covering = append(covering, tc.ID)
				if t := strings.TrimSpace(tc.Type); t != "" {
					typeSet[t] = true
				}
			}
		}
		status := "Covered"
		if len(covering) == 0 {
			status = "GAP — no test"
		}
		rows = append(rows, []string{
			ac.ID,
			ac.Description,
			ac.Module,
			ac.Severity,
			strings.Join(covering, "; "),
			joinSet(typeSet),
			itoa(len(covering)),
			status,
		})
	}
	return rtmHeader, rows
}

// GapCount returns how many requirements have no covering test case (for the UI
// headline).
func GapCount(r contract.Result) int {
	n := 0
	for _, ac := range r.AcceptanceCriteria {
		covered := false
		for _, tc := range r.TestCases {
			if tcCovers(tc, ac.ID) {
				covered = true
				break
			}
		}
		if !covered {
			n++
		}
	}
	return n
}

// RTM renders the matrix as an Excel-friendly CSV.
func RTM(r contract.Result, _ Options) ([]byte, error) {
	header, rows := RTMRows(r)
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

func tcCovers(tc contract.TestCase, acID string) bool {
	for _, c := range tc.Covers {
		if strings.EqualFold(strings.TrimSpace(c), acID) {
			return true
		}
	}
	return false
}

func joinSet(set map[string]bool) string {
	if len(set) == 0 {
		return ""
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
