package prompt

import (
	"fmt"
	"strings"

	"qa-ai/internal/contract"
)

// This file builds the prompts for the batched pipeline (one small call per
// stage) used by qa-core's step-by-step orchestrator. Keeping each stage narrow
// means each LLM response easily fits the context window, so large suites don't
// truncate the way a single-shot "generate everything" call would.

// ----- Stage 1: analysis + acceptance criteria -----

const analysisACsSchema = `{
  "requirement_analysis": [
    { "feature": "string", "sub_requirements": ["string"], "risk_level": "Critical|High|Medium|Low" }
  ],
  "acceptance_criteria": [
    { "id": "AC-1", "description": "string", "module": "string", "severity": "Critical|High|Medium|Low", "risk_level": "Critical|High|Medium|Low" }
  ],
  "ambiguities": [ { "location": "string", "issue": "string", "suggestion": "string", "severity": "Critical|High|Medium|Low" } ],
  "requirement_health": { "score": 0, "rating": "string", "deductions": [ { "reason": "string", "points": -10 } ] },
  "missing_areas": ["string"]
}`

// BuildAnalysisAndACs asks for the requirement breakdown, ambiguities, health,
// and the full list of acceptance criteria — but NO test cases. ACs are not
// capped: the model is told to emit as many as the requirement genuinely needs.
func BuildAnalysisAndACs(r contract.GenerateRequest) (system, user string) {
	var b strings.Builder
	b.WriteString("Analyze the requirement and derive its acceptance criteria. Do NOT write test cases yet.\n\n")
	writeRequirementAndOptions(&b, r)
	b.WriteString("\nINSTRUCTIONS:\n")
	b.WriteString("- Break the requirement into features and sub-requirements with risk levels.\n")
	b.WriteString("- Flag every ambiguity with a severity.\n")
	b.WriteString("- Score Requirement Health per the policy with itemized deductions.\n")
	b.WriteString("- Derive ALL acceptance criteria the requirement implies — do not limit the count; a rich requirement may have many.\n")
	b.WriteString("- Each AC gets a stable id (AC-1, AC-2, …), a module/feature, severity, and risk level.\n")
	b.WriteString("\nReturn ONLY this JSON shape (exact keys):\n")
	b.WriteString(analysisACsSchema)
	return System, b.String()
}

// ----- Stage 2: test cases for ONE acceptance criterion -----

const testCasesSchema = `{
  "test_cases": [
    { "id": "TC-1", "title": "string", "type": "Positive|Negative|Edge case|Trivial", "technique": "string",
      "preconditions": ["string"], "steps": ["string"], "expected_result": "string",
      "postconditions": ["string"], "priority": "P0|P1|P2|P3", "severity": "Critical|High|Medium|Low",
      "tags": ["Functional|Security|Performance|Accessibility|Usability"],
      "covers": ["AC-1"], "format": "step-by-step|gherkin|checklist", "risk": "Critical|High|Medium|Low" }
  ]
}`

// BuildTestCasesForAC asks for a thorough set of test cases that cover a single
// acceptance criterion. Numbering starts at startIndex so the orchestrator can
// concatenate batches without colliding TC IDs.
func BuildTestCasesForAC(r contract.GenerateRequest, ac contract.AcceptanceCriterion, startIndex int) (system, user string) {
	var b strings.Builder
	fmt.Fprintf(&b, "Write thorough test cases that cover this ONE acceptance criterion of a larger requirement.\n\n")
	fmt.Fprintf(&b, "ACCEPTANCE CRITERION:\n- id: %s\n- description: %s\n- module: %s\n- severity: %s\n\n",
		ac.ID, ac.Description, ac.Module, ac.Severity)
	writeRequirementAndOptions(&b, r)
	n := r.CasesPerTypeOrDefault()
	b.WriteString("\nINSTRUCTIONS:\n")
	fmt.Fprintf(&b, "- Every test case's \"covers\" MUST be [\"%s\"].\n", ac.ID)
	fmt.Fprintf(&b, "- Write UP TO %d cases for EACH requested case nature that applies to this AC (Positive, Negative, Edge case). Quality over quantity: pick the most valuable, DISTINCT cases — do not pad or repeat.\n", n)
	b.WriteString("- Within that budget, prioritise: boundaries, invalid input, error paths, and state transitions.\n")
	b.WriteString("- For each case set: postconditions (state/cleanup after), priority (P0-P3), severity (Critical-Low), and tags = the test dimension(s) it covers (Functional/Security/Performance/Accessibility/Usability).\n")
	if len(r.TestDimensions) > 0 {
		b.WriteString("- Include cases for the requested non-functional dimensions where they apply to this AC, tagged accordingly.\n")
	}
	fmt.Fprintf(&b, "- Number the test cases starting at TC-%d, incrementing by 1 (TC-%d, TC-%d, …).\n", startIndex, startIndex, startIndex+1)
	b.WriteString("- Produce them in the chosen output format.\n")
	b.WriteString("\nReturn ONLY this JSON shape (exact keys):\n")
	b.WriteString(testCasesSchema)
	return System, b.String()
}

// ----- Stage 3: edge cases + structured test data -----

const auxSchema = `{
  "edge_cases": [ { "id": "EC-1", "scenario": "string", "expected_result": "string" } ],
  "test_data": { "valid_emails": ["string"], "invalid_emails": ["string"], "boundary_values": ["string"] }
}`

// BuildAux asks for cross-cutting edge cases and structured test data (one cheap
// call, independent of any single AC).
func BuildAux(r contract.GenerateRequest) (system, user string) {
	var b strings.Builder
	b.WriteString("Produce cross-cutting edge cases and structured test data for the requirement.\n\n")
	writeRequirementAndOptions(&b, r)
	b.WriteString("\nINSTRUCTIONS:\n")
	b.WriteString("- edge_cases: empty fields, max length, Unicode, concurrency, expired tokens, etc.\n")
	b.WriteString("- test_data: realistic valid/invalid emails and boundary values.\n")
	b.WriteString("\nReturn ONLY this JSON shape (exact keys):\n")
	b.WriteString(auxSchema)
	return System, b.String()
}
