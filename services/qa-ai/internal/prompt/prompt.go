// Package prompt builds the LLM prompt from the input form. Every form field
// (PRD §5.1) becomes an injected variable; the system prompt pins the JSON
// output contract (PRD §7) and the scoring policy (PRD §6c) so a 7B model stays
// on-rails and returns parseable, contract-shaped JSON.
package prompt

import (
	"fmt"
	"strings"

	"qa-ai/internal/contract"
)

// System is the fixed instruction block. It is deliberately strict about
// "JSON only" because Qwen2.5 7B will otherwise wrap output in prose or code
// fences, which breaks parsing and burns retries.
const System = `You are a senior QA/QC engineer that generates professional, structured test artifacts.

OUTPUT RULES (critical):
- Respond with a SINGLE JSON object and NOTHING else. No prose, no markdown, no code fences.
- The JSON MUST match the exact schema and key names given by the user. Do not add or rename keys.
- Use English only.
- Every test case's "covers" must reference acceptance-criteria IDs you actually emit.
- Every coverage_matrix "covered_by" must reference test-case IDs you actually emit.

SCORING POLICY (apply exactly):
- Both scores start at 100 and subtract itemized deductions. Each deduction has a short "reason" and negative "points".
- Requirement Health (requirement quality, BEFORE generation):
    Ambiguity penalties per flagged ambiguity: Critical -20, High -10, Medium -5, Low -2.
    Missing components: Primary actor -10, Business rule -10, Success path -10,
    Error handling -15, Validation rules -15, Acceptance criteria implied but unstated -10.
    Rating bands: 90-100 Excellent, 75-89 Good, 60-74 Needs Clarification, 40-59 High Risk, <40 Not Test Ready.
- Traceability (coverage completeness, AFTER generation, stricter):
    Each requirement point not covered by >=1 test case: -15.
    Coverage-type gaps (from coverage_summary counts): No negative coverage (negative==0) -15,
    No edge-case coverage (edge_case==0) -10, Only-positive suite (negative==0 AND edge_case==0) additional -10,
    Over-reliance on trivial cases (trivial > positive+negative+edge_case) -5. Positive coverage is the baseline (no penalty).
    Unresolved ambiguity impact: Critical -15, High -8, Medium -4, Low -1.
    Rating bands: 95-100 Fully Covered, 85-94 Minor Gaps, 70-84 Coverage Risk, 50-69 Significant Gaps, <50 Unsafe To Release.
- The server re-clamps scores to 0-100, so report honest deductions; do not pre-clamp.`

// schema is the exact shape the model must return. Keep this in lockstep with
// contract.Result. api_tests is only requested when app type = API.
func schema(apiBlock string) string {
	return `{
  "requirement_analysis": [
    { "feature": "string", "sub_requirements": ["string"], "risk_level": "Critical|High|Medium|Low" }
  ],
  "acceptance_criteria": [
    { "id": "AC-1", "description": "string", "module": "string", "severity": "Critical|High|Medium|Low", "risk_level": "Critical|High|Medium|Low" }
  ],
  "test_cases": [
    { "id": "TC-1", "title": "string", "type": "Positive|Negative|Edge case|Trivial", "technique": "string",
      "preconditions": ["string"], "steps": ["string"], "expected_result": "string",
      "covers": ["AC-1"], "format": "step-by-step|gherkin|checklist", "risk": "Critical|High|Medium|Low" }
  ],
  "edge_cases": [ { "id": "EC-1", "scenario": "string", "expected_result": "string" } ],
  "test_data": { "valid_emails": ["string"], "invalid_emails": ["string"], "boundary_values": ["string"] },` + apiBlock + `
  "coverage_matrix": [ { "requirement_point": "string", "covered_by": ["TC-1"], "coverage_type": "Positive|Negative|Edge case|Trivial" } ],
  "coverage_summary": { "positive": 0, "negative": 0, "edge_case": 0, "trivial": 0 },
  "ambiguities": [ { "location": "string", "issue": "string", "suggestion": "string", "severity": "Critical|High|Medium|Low" } ],
  "requirement_health": { "score": 0, "rating": "string", "deductions": [ { "reason": "string", "points": -10 } ] },
  "traceability_score": { "score": 0, "rating": "string", "deductions": [ { "reason": "string", "points": -10 } ] },
  "missing_areas": ["string"]
}`
}

const apiSchemaBlock = `
  "api_tests": [ { "request_example": "POST /path { ... }", "expected_status": 200, "response_validation": "string" } ],`

// Build returns (system, user) prompts for the given request.
func Build(r contract.GenerateRequest) (system, user string) {
	apiBlock := ""
	if r.IsAPI() {
		apiBlock = apiSchemaBlock
	}

	var b strings.Builder
	b.WriteString("Generate QA artifacts for the following requirement.\n\n")
	writeRequirementAndOptions(&b, r)

	b.WriteString("\nINSTRUCTIONS:\n")
	b.WriteString("- Run a requirement breakdown FIRST, then derive acceptance criteria, then test cases.\n")
	b.WriteString("- Produce test cases in the chosen output format and design technique(s).\n")
	if !r.IncludePreconditions {
		b.WriteString("- Preconditions are optional; you may leave them empty.\n")
	}
	if !r.IncludeTestData {
		b.WriteString("- Test data may be minimal/empty arrays.\n")
	}
	if !r.GenerateEdgeCases {
		b.WriteString("- Edge cases may be a short list or empty.\n")
	}
	if r.IsAPI() {
		b.WriteString("- This is an API: populate api_tests with request examples, expected status codes, and response validation rules.\n")
	} else {
		b.WriteString("- This is NOT an API: omit api_tests entirely.\n")
	}

	b.WriteString("\nReturn ONLY this JSON shape (exact keys):\n")
	b.WriteString(schema(apiBlock))

	return System, b.String()
}

// validationSchema is the smaller shape the validation pass returns: analysis +
// ambiguities + requirement health only (no test cases/coverage/traceability).
const validationSchema = `{
  "requirement_analysis": [
    { "feature": "string", "sub_requirements": ["string"], "risk_level": "Critical|High|Medium|Low" }
  ],
  "ambiguities": [ { "location": "string", "issue": "string", "suggestion": "string", "severity": "Critical|High|Medium|Low" } ],
  "requirement_health": { "score": 0, "rating": "string", "deductions": [ { "reason": "string", "points": -10 } ] },
  "missing_areas": ["string"]
}`

// BuildValidation returns (system, user) prompts for step 1 of the two-step flow:
// analyze/validate the requirement only, before any test cases are written.
func BuildValidation(r contract.GenerateRequest) (system, user string) {
	var b strings.Builder
	b.WriteString("Validate and analyze the following requirement BEFORE any test cases are written.\n\n")
	writeRequirementAndOptions(&b, r)
	b.WriteString("\nINSTRUCTIONS:\n")
	b.WriteString("- Break the requirement into features and sub-requirements with risk levels.\n")
	b.WriteString("- Flag every ambiguity with a severity (Critical/High/Medium/Low).\n")
	b.WriteString("- Score Requirement Health per the policy and list itemized deductions.\n")
	b.WriteString("- Do NOT generate test cases, coverage, or traceability — analysis only.\n")
	b.WriteString("\nReturn ONLY this JSON shape (exact keys):\n")
	b.WriteString(validationSchema)
	return System, b.String()
}

// writeRequirementAndOptions writes the shared REQUIREMENT + GENERATION OPTIONS
// block used by both Build and BuildValidation.
func writeRequirementAndOptions(b *strings.Builder, r contract.GenerateRequest) {
	b.WriteString("REQUIREMENT:\n")
	b.WriteString(strings.TrimSpace(r.Requirement))
	b.WriteString("\n\nGENERATION OPTIONS:\n")
	writeKV(b, "Application type", r.ApplicationType)
	writeKV(b, "Detail level", orDefault(r.DetailLevel, "Detailed"))
	writeKV(b, "Case natures", joinOr(r.TestTypes, "Positive, Negative, Edge case"))
	if len(r.TestDimensions) > 0 {
		writeKV(b, "Test dimensions to cover", strings.Join(r.TestDimensions, ", "))
		b.WriteString("  (For each non-functional dimension requested, include concrete checks — e.g. Security: authz/input validation/rate limiting; Performance: load/latency/limits; Accessibility: WCAG/keyboard/contrast; Usability: error messaging/affordances.)\n")
	}
	if len(r.TestDesignTechniques) > 0 {
		writeKV(b, "Test design techniques", strings.Join(r.TestDesignTechniques, ", "))
	}
	writeKV(b, "Output format", orDefault(r.OutputFormat, "step-by-step"))
	writeKV(b, "Priority/Severity scheme", orDefault(r.PriorityScheme, "P0-P3"))
	writeKV(b, "Include preconditions", yesNo(r.IncludePreconditions))
	writeKV(b, "Include test data suggestions", yesNo(r.IncludeTestData))
	writeKV(b, "Generate edge cases", yesNo(r.GenerateEdgeCases))
	if strings.TrimSpace(r.PlatformMatrix) != "" {
		writeKV(b, "Platform/device/browser matrix", r.PlatformMatrix)
	}
}

func writeKV(b *strings.Builder, k, v string) {
	fmt.Fprintf(b, "- %s: %s\n", k, v)
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func joinOr(v []string, def string) string {
	if len(v) == 0 {
		return def
	}
	return strings.Join(v, ", ")
}
