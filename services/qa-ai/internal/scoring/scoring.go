// Package scoring enforces the server-side guarantees of the scoring policy
// (PRD §6c / §7): scores are model-proposed but sanity-bounded. We recompute
// each score as 100 + sum(deductions) and clamp to 0–100, then assign the
// rating band from the score so the rating can never disagree with the number.
package scoring

import "qa-ai/internal/contract"

// Clamp normalizes both scores in-place: recompute from deductions, bound to
// 0–100, and derive the rating band. This makes the displayed score trustworthy
// regardless of what the model put in the "score"/"rating" fields.
func Clamp(r *contract.Result) {
	r.RequirementHealth = normalize(r.RequirementHealth, healthRating)
	r.TraceabilityScore = normalize(r.TraceabilityScore, traceRating)
}

func normalize(s contract.Score, rate func(int) string) contract.Score {
	total := 100
	for _, d := range s.Deductions {
		p := d.Points
		if p > 0 { // deductions are penalties; treat any positive as a sign slip
			p = -p
		}
		total += p
	}
	if total < 0 {
		total = 0
	}
	if total > 100 {
		total = 100
	}
	s.Score = total
	s.Rating = rate(total)
	return s
}

// healthRating: 90-100 Excellent · 75-89 Good · 60-74 Needs Clarification ·
// 40-59 High Risk · <40 Not Test Ready.
func healthRating(score int) string {
	switch {
	case score >= 90:
		return "Excellent"
	case score >= 75:
		return "Good"
	case score >= 60:
		return "Needs Clarification"
	case score >= 40:
		return "High Risk"
	default:
		return "Not Test Ready"
	}
}

// traceRating: 95-100 Fully Covered · 85-94 Minor Gaps · 70-84 Coverage Risk ·
// 50-69 Significant Gaps · <50 Unsafe To Release.
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
