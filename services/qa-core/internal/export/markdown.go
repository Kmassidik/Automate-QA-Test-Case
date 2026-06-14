package export

import (
	"fmt"
	"strings"

	"qa-core/internal/contract"
)

// Markdown renders the full report (PRD §5.3) — docs-friendly, every section.
func Markdown(r contract.Result, opt Options) []byte {
	var b strings.Builder

	b.WriteString("# QA Test Artifacts\n\n")
	if strings.TrimSpace(opt.Requirement) != "" {
		b.WriteString("## Requirement\n\n")
		b.WriteString("> " + strings.ReplaceAll(strings.TrimSpace(opt.Requirement), "\n", "\n> ") + "\n\n")
	}

	// Scores up top — the QA-lead headline.
	b.WriteString("## Scores\n\n")
	writeScore(&b, "Requirement Health", r.RequirementHealth)
	writeScore(&b, "Traceability", r.TraceabilityScore)
	if len(r.MissingAreas) > 0 {
		fmt.Fprintf(&b, "**Missing areas:** %s\n\n", strings.Join(r.MissingAreas, ", "))
	}

	// Requirement breakdown.
	if len(r.RequirementAnalysis) > 0 {
		b.WriteString("## Requirement Breakdown\n\n")
		for _, f := range r.RequirementAnalysis {
			fmt.Fprintf(&b, "### %s  _(risk: %s)_\n\n", f.Feature, fallback(f.RiskLevel, "n/a"))
			for _, sr := range f.SubRequirements {
				fmt.Fprintf(&b, "- %s\n", sr)
			}
			b.WriteString("\n")
		}
	}

	// Acceptance criteria.
	if len(r.AcceptanceCriteria) > 0 {
		b.WriteString("## Acceptance Criteria\n\n")
		b.WriteString("| ID | Description | Module | Severity | Risk |\n|---|---|---|---|---|\n")
		for _, ac := range r.AcceptanceCriteria {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
				ac.ID, mdCell(ac.Description), mdCell(ac.Module), ac.Severity, ac.RiskLevel)
		}
		b.WriteString("\n")
	}

	// Test cases.
	if len(r.TestCases) > 0 {
		b.WriteString("## Test Cases\n\n")
		for _, tc := range r.TestCases {
			fmt.Fprintf(&b, "### %s — %s\n\n", tc.ID, tc.Title)
			fmt.Fprintf(&b, "- **Type:** %s · **Priority:** %s · **Severity:** %s · **Risk:** %s\n",
				tc.Type, fallback(tc.Priority, "—"), fallback(tc.Severity, "—"), tc.Risk)
			if len(tc.Tags) > 0 {
				fmt.Fprintf(&b, "- **Tags:** %s\n", strings.Join(tc.Tags, ", "))
			}
			if len(tc.Covers) > 0 {
				fmt.Fprintf(&b, "- **Covers:** %s\n", strings.Join(tc.Covers, ", "))
			}
			if len(tc.Preconditions) > 0 {
				b.WriteString("- **Preconditions:**\n")
				for _, p := range tc.Preconditions {
					fmt.Fprintf(&b, "  - %s\n", p)
				}
			}
			b.WriteString("- **Steps:**\n")
			for i, s := range tc.Steps {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, s)
			}
			fmt.Fprintf(&b, "- **Expected:** %s\n", tc.ExpectedResult)
			if len(tc.Postconditions) > 0 {
				fmt.Fprintf(&b, "- **Postconditions:** %s\n", strings.Join(tc.Postconditions, "; "))
			}
			b.WriteString("\n")
		}
	}

	// Coverage.
	b.WriteString("## Coverage\n\n")
	cs := r.CoverageSummary
	fmt.Fprintf(&b, "**Summary:** positive %d · negative %d · edge case %d\n\n",
		cs.Positive, cs.Negative, cs.EdgeCase)
	if len(r.CoverageMatrix) > 0 {
		b.WriteString("| Requirement Point | Covered By | Type |\n|---|---|---|\n")
		for _, row := range r.CoverageMatrix {
			fmt.Fprintf(&b, "| %s | %s | %s |\n",
				mdCell(row.RequirementPoint), strings.Join(row.CoveredBy, ", "), row.CoverageType)
		}
		b.WriteString("\n")
	}

	// Edge cases.
	if len(r.EdgeCases) > 0 {
		b.WriteString("## Edge Cases\n\n")
		for _, ec := range r.EdgeCases {
			fmt.Fprintf(&b, "- **%s** — %s → %s\n", ec.ID, ec.Scenario, ec.ExpectedResult)
		}
		b.WriteString("\n")
	}

	// Structured test data.
	td := r.TestData
	if len(td.ValidEmails)+len(td.InvalidEmails)+len(td.BoundaryValues) > 0 {
		b.WriteString("## Test Data\n\n")
		writeList(&b, "Valid emails", td.ValidEmails)
		writeList(&b, "Invalid emails", td.InvalidEmails)
		writeList(&b, "Boundary values", td.BoundaryValues)
	}

	// API tests.
	if len(r.APITests) > 0 {
		b.WriteString("## API Tests\n\n")
		for _, at := range r.APITests {
			fmt.Fprintf(&b, "- `%s` → expect **%d**\n  - %s\n", at.RequestExample, at.ExpectedStatus, at.ResponseValidation)
		}
		b.WriteString("\n")
	}

	// Ambiguities.
	if len(r.Ambiguities) > 0 {
		b.WriteString("## Ambiguities\n\n")
		b.WriteString("| Severity | Location | Issue | Suggestion |\n|---|---|---|---|\n")
		for _, a := range r.Ambiguities {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
				a.Severity, mdCell(a.Location), mdCell(a.Issue), mdCell(a.Suggestion))
		}
		b.WriteString("\n")
	}

	return []byte(b.String())
}

func writeScore(b *strings.Builder, label string, s contract.Score) {
	fmt.Fprintf(b, "### %s: **%d / 100** — %s\n\n", label, s.Score, s.Rating)
	if len(s.Deductions) > 0 {
		for _, d := range s.Deductions {
			fmt.Fprintf(b, "- %s (%d)\n", d.Reason, d.Points)
		}
		b.WriteString("\n")
	}
}

func writeList(b *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "**%s:** %s\n\n", label, strings.Join(items, ", "))
}

// mdCell escapes pipes/newlines so table cells don't break Markdown rendering.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
