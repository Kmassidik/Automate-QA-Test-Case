// Package export renders the canonical Result JSON into the three frozen output
// formats (PRD §5.3): QA Repository CSV (test-case-centric), Jira CSV
// (issue-centric), and a Markdown report. All three are pure transforms over
// contract.Result — no model calls.
package export

import (
	"sort"
	"strconv"
	"strings"

	"qa-core/internal/contract"
)

func itoa(n int) string { return strconv.Itoa(n) }

// Options carries fields that live on the request, not the Result, but are
// needed to render exports faithfully (e.g. the chosen priority scheme).
type Options struct {
	PriorityScheme string // "P0-P3" or "Critical-Low"
	Requirement    string // original requirement text (for the Jira parent / MD header)
}

// derived precomputes test-case → coverage relationships from the coverage
// matrix so each exporter doesn't re-walk it.
type derived struct {
	// reqPointsByTC maps a test-case ID to the requirement points it covers.
	reqPointsByTC map[string][]string
	// coverageTypeByTC maps a test-case ID to its coverage type(s).
	coverageTypeByTC map[string][]string
	// acByID indexes acceptance criteria for severity lookup.
	acByID map[string]contract.AcceptanceCriterion
}

func derive(r contract.Result) derived {
	d := derived{
		reqPointsByTC:    map[string][]string{},
		coverageTypeByTC: map[string][]string{},
		acByID:           map[string]contract.AcceptanceCriterion{},
	}
	for _, ac := range r.AcceptanceCriteria {
		d.acByID[ac.ID] = ac
	}
	for _, row := range r.CoverageMatrix {
		for _, tcID := range row.CoveredBy {
			if row.RequirementPoint != "" {
				d.reqPointsByTC[tcID] = appendUnique(d.reqPointsByTC[tcID], row.RequirementPoint)
			}
			if row.CoverageType != "" {
				d.coverageTypeByTC[tcID] = appendUnique(d.coverageTypeByTC[tcID], row.CoverageType)
			}
		}
	}
	return d
}

// severityForTC: a test case has no explicit severity in the contract, so we
// take the max severity of the ACs it covers, falling back to its own risk.
func (d derived) severityForTC(tc contract.TestCase) string {
	best := ""
	bestRank := -1
	for _, acID := range tc.Covers {
		if ac, ok := d.acByID[acID]; ok {
			if rk := sevRank(ac.Severity); rk > bestRank {
				bestRank, best = rk, ac.Severity
			}
		}
	}
	if best == "" {
		return normalizeSeverity(tc.Risk)
	}
	return best
}

// priorityForTC maps the test case's risk onto the chosen priority scheme.
// Derivation (documented): Critical→P0, High→P1, Medium→P2, Low→P3; the
// Critical-Low scheme keeps the risk vocabulary as-is.
func priorityForTC(tc contract.TestCase, scheme string) string {
	risk := normalizeSeverity(tc.Risk)
	if strings.EqualFold(scheme, "Critical-Low") {
		return risk
	}
	switch risk {
	case "Critical":
		return "P0"
	case "High":
		return "P1"
	case "Medium":
		return "P2"
	case "Low":
		return "P3"
	default:
		return "P2"
	}
}

// testDataForTC picks the slice of global structured test data most relevant to
// a test case, so the flat CSV row is self-contained without repeating the whole
// dataset on every line.
func testDataForTC(tc contract.TestCase, td contract.TestData) string {
	t := strings.ToLower(tc.Type + " " + tc.Technique + " " + tc.Title)
	switch {
	case strings.Contains(t, "boundary"):
		return strings.Join(td.BoundaryValues, "; ")
	case strings.Contains(t, "negative"), strings.Contains(t, "invalid"):
		return strings.Join(td.InvalidEmails, "; ")
	default:
		return strings.Join(td.ValidEmails, "; ")
	}
}

func sevRank(s string) int {
	switch normalizeSeverity(s) {
	case "Critical":
		return 3
	case "High":
		return 2
	case "Medium":
		return 1
	case "Low":
		return 0
	default:
		return -1
	}
}

func normalizeSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return "Critical"
	case "high":
		return "High"
	case "medium", "med":
		return "Medium"
	case "low":
		return "Low"
	default:
		return strings.TrimSpace(s)
	}
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func sortedJoin(s []string, sep string) string {
	cp := append([]string(nil), s...)
	sort.Strings(cp)
	return strings.Join(cp, sep)
}
