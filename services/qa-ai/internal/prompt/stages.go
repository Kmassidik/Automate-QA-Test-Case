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
    { "id": "TC-1", "title": "string", "type": "Positive|Negative|Edge case", "technique": "string",
      "preconditions": ["string"], "steps": ["string"], "expected_result": "string",
      "postconditions": ["string"], "priority": "P0|P1|P2|P3", "severity": "Critical|High|Medium|Low",
      "tags": ["Functional|Security|Performance|Accessibility|Usability"],
      "covers": ["AC-1"], "format": "step-by-step|gherkin|checklist", "risk": "Critical|High|Medium|Low" }
  ]
}`

// systemTestCases is a lean system prompt for the test-case/aux stages. It drops
// the scoring policy (irrelevant here — scores are computed elsewhere), so the
// small model has fewer tokens to juggle and fewer ways to break JSON.
const systemTestCases = `You are a senior QA engineer who writes precise, executable test cases.

OUTPUT RULES (critical):
- Respond with a SINGLE JSON object and NOTHING else — no prose, no markdown, no code fences, no trailing commas; escape any quotes inside strings.
- Match the exact schema and key names. Do not add or rename keys.
- English only.`

// testCaseExample is a one-shot gold example (for a DIFFERENT AC) showing the
// exact shape and — crucially — one case of EACH nature including an Edge case,
// so the model reliably emits Edge-case-typed cases instead of only Positive/
// Negative. Biggest single lever for output quality on a 7B.
const testCaseExample = `EXAMPLE (for a different AC "AC-9: a valid login redirects to the dashboard"). Match this shape and quality — note one case PER nature, with "type" set exactly:
{
  "test_cases": [
    { "id": "TC-1", "title": "Valid credentials log the user in", "type": "Positive", "technique": "Equivalence Partitioning",
      "preconditions": ["User has a registered, active account"], "steps": ["Open the login page", "Enter a valid email and correct password", "Submit"],
      "expected_result": "User is authenticated and redirected to the dashboard", "postconditions": ["A session is started"],
      "priority": "P1", "severity": "High", "tags": ["Functional"], "covers": ["AC-9"], "format": "step-by-step", "risk": "High" },
    { "id": "TC-2", "title": "Incorrect password is rejected", "type": "Negative", "technique": "Error Guessing",
      "preconditions": ["User has a registered account"], "steps": ["Open the login page", "Enter a valid email and a wrong password", "Submit"],
      "expected_result": "Login fails with an 'invalid credentials' message and no session is created", "postconditions": [],
      "priority": "P1", "severity": "High", "tags": ["Functional", "Security"], "covers": ["AC-9"], "format": "step-by-step", "risk": "High" },
    { "id": "TC-3", "title": "Email at the maximum allowed length (boundary)", "type": "Edge case", "technique": "Boundary Value Analysis",
      "preconditions": ["None"], "steps": ["Enter an email of exactly the maximum allowed length", "Enter any password", "Submit"],
      "expected_result": "The boundary input is handled gracefully — accepted up to the limit, rejected beyond it, with no crash", "postconditions": [],
      "priority": "P2", "severity": "Medium", "tags": ["Functional"], "covers": ["AC-9"], "format": "step-by-step", "risk": "Medium" }
  ]
}`

// BuildTestCasesForAC asks for test cases covering a single acceptance criterion,
// one batch per AC. Numbering starts at startIndex so the orchestrator can
// concatenate batches without colliding TC IDs.
func BuildTestCasesForAC(r contract.GenerateRequest, ac contract.AcceptanceCriterion, startIndex int) (system, user string) {
	n := r.CasesPerTypeOrDefault()
	natures := joinOr(r.TestTypes, "Positive, Negative, Edge case")
	natCount := len(r.TestTypes)
	if natCount == 0 {
		natCount = 3
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Write executable test cases that cover this ONE acceptance criterion of a larger requirement.\n\n")
	fmt.Fprintf(&b, "ACCEPTANCE CRITERION:\n- id: %s\n- description: %s\n- module: %s\n- severity: %s\n\n",
		ac.ID, ac.Description, ac.Module, ac.Severity)
	writeRequirementAndOptions(&b, r)
	b.WriteString("\nINSTRUCTIONS:\n")
	fmt.Fprintf(&b, "- Every test case's \"covers\" MUST be [\"%s\"].\n", ac.ID)
	fmt.Fprintf(&b, "- Produce test cases of EACH of these natures, and set each case's \"type\" to its nature EXACTLY: %s.\n", natures)
	fmt.Fprintf(&b, "- Aim for up to %d cases per nature (up to ~%d for this AC). Positive and Negative almost always apply; an \"Edge case\" applies wherever there is a boundary, limit, empty/maximum value, exact threshold, or unusual-but-valid input — include at least one Edge case unless genuinely impossible for this AC.\n", n, n*natCount)
	b.WriteString("- Each case must test a DISTINCT condition — no padding or repeats.\n")
	b.WriteString("- Set postconditions (state/cleanup after), priority (P0-P3), severity (Critical-Low), and tags = the dimension(s) covered (Functional/Security/Performance/Accessibility/Usability).\n")
	if len(r.TestDimensions) > 0 {
		b.WriteString("- Add cases for the requested non-functional dimensions where they apply, tagged accordingly.\n")
	}
	fmt.Fprintf(&b, "- Number the test cases starting at TC-%d.\n\n", startIndex)
	b.WriteString(testCaseExample)
	b.WriteString("\n\nNow return ONLY a JSON object of this exact shape (same keys as the example):\n")
	b.WriteString(testCasesSchema)
	return systemTestCases, b.String()
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
	return systemTestCases, b.String()
}
