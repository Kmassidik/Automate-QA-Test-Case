package orchestrator

import (
	"sort"
	"strings"

	"qa-core/internal/contract"
)

// computeCoverage derives the coverage matrix (one row per AC) and the
// coverage_summary counts from the generated test cases — deterministically, so
// these never disagree with the actual suite.
func computeCoverage(acs []contract.AcceptanceCriterion, cases []contract.TestCase) ([]contract.CoverageRow, contract.CoverageSummary) {
	var summary contract.CoverageSummary
	for _, tc := range cases {
		switch normType(tc.Type) {
		case "Negative":
			summary.Negative++
		case "Edge case":
			summary.EdgeCase++
		default:
			summary.Positive++
		}
	}

	rows := make([]contract.CoverageRow, 0, len(acs))
	for _, ac := range acs {
		var covered []string
		typeSet := map[string]bool{}
		for _, tc := range cases {
			if covers(tc, ac.ID) {
				covered = append(covered, tc.ID)
				typeSet[normType(tc.Type)] = true
			}
		}
		rows = append(rows, contract.CoverageRow{
			RequirementPoint: ac.Description,
			CoveredBy:        covered,
			CoverageType:     joinTypes(typeSet),
		})
	}
	return rows, summary
}

// computeTraceability applies the frozen scoring policy (PRD §6c) as code, with
// itemized deductions so the UI can explain the number.
func computeTraceability(acs []contract.AcceptanceCriterion, cases []contract.TestCase, sum contract.CoverageSummary, ambs []contract.Ambiguity) contract.Score {
	score := 100
	var ded []contract.Deduction
	sub := func(pts int, reason string) {
		score += pts
		ded = append(ded, contract.Deduction{Reason: reason, Points: pts})
	}

	// Each AC not covered by >=1 test case: -15.
	uncovered := 0
	for _, ac := range acs {
		hit := false
		for _, tc := range cases {
			if covers(tc, ac.ID) {
				hit = true
				break
			}
		}
		if !hit {
			uncovered++
		}
	}
	if uncovered > 0 {
		sub(-15*uncovered, plural(uncovered, "acceptance criterion", "acceptance criteria")+" with no test coverage")
	}

	// Coverage-type gaps.
	if sum.Negative == 0 {
		sub(-15, "No negative testing")
	}
	if sum.EdgeCase == 0 {
		sub(-10, "No edge-case testing")
	}
	if sum.Negative == 0 && sum.EdgeCase == 0 {
		sub(-10, "Only-positive suite")
	}

	// Unresolved ambiguity impact.
	for _, a := range ambs {
		switch normSeverity(a.Severity) {
		case "Critical":
			sub(-15, "Critical ambiguity unresolved")
		case "High":
			sub(-8, "High ambiguity unresolved")
		case "Medium":
			sub(-4, "Medium ambiguity unresolved")
		case "Low":
			sub(-1, "Low ambiguity unresolved")
		}
	}

	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return contract.Score{Score: score, Rating: traceRating(score), Deductions: ded}
}

// traceRating: 95-100 Fully Covered · 85-94 Minor Gaps · 70-84 Coverage Risk ·
// 50-69 Significant Gaps · <50 Unsafe To Release (PRD §6c).
func traceRating(score int) string {
	switch {
	case score >= 95:
		return "Fully Covered"
	case score >= 85:
		return "Minor Gaps"
	case score >= 70:
		return "Coverage Risk"
	case score >= 50:
		return "Significant Gaps"
	default:
		return "Unsafe To Release"
	}
}

func covers(tc contract.TestCase, acID string) bool {
	for _, c := range tc.Covers {
		if strings.EqualFold(strings.TrimSpace(c), acID) {
			return true
		}
	}
	return false
}

func normType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "negative":
		return "Negative"
	case "edge case", "edge", "edgecase", "edge-case":
		return "Edge case"
	default:
		return "Positive"
	}
}

func normSeverity(s string) string {
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
		return ""
	}
}

func joinTypes(set map[string]bool) string {
	if len(set) == 0 {
		return ""
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
