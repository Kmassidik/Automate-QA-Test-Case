// Package contract defines the wire types shared between qa-core and qa-ai:
// the generation request (built from the input form) and the canonical result
// JSON (the LLM Output Contract, PRD §7). Exports in qa-core are pure transforms
// over Result, so this is the single source of truth for the artifact shape.
package contract

// GenerateRequest is the input form (PRD §5.1) sent by qa-core to qa-ai.
type GenerateRequest struct {
	Requirement          string   `json:"requirement"`
	Model                string   `json:"model,omitempty"`        // active Ollama model; empty => qa-ai's configured default
	ApplicationType      string   `json:"application_type"`       // Web · Mobile · API · Desktop · E-commerce · CMS · SaaS · Embedded
	DetailLevel          string   `json:"detail_level"`           // Condensed · Standard · Detailed
	TestTypes            []string `json:"test_types"`             // case natures: Positive · Negative · Edge case · Trivial
	TestDimensions       []string `json:"test_dimensions"`        // Functional · Security · Performance · Accessibility · Usability
	TestDesignTechniques []string `json:"test_design_techniques"` // EP · BVA · Decision Table · ...
	OutputFormat         string   `json:"output_format"`          // step-by-step · gherkin · checklist
	PriorityScheme       string   `json:"priority_scheme"`        // "P0-P3" · "Critical-Low"
	IncludePreconditions bool     `json:"include_preconditions"`
	IncludeTestData      bool     `json:"include_test_data"`
	GenerateEdgeCases    bool     `json:"generate_edge_cases"`
	PlatformMatrix       string   `json:"platform_matrix"` // optional free text

	// Two-step review (Step 1 → edit → Step 2). User-curated acceptance criteria
	// + extra clarifications carried from the review page.
	AcceptanceCriteria []AcceptanceCriterion `json:"acceptance_criteria,omitempty"`
	Clarifications     string                `json:"clarifications,omitempty"`
}

// IsAPI reports whether API testing artifacts (§5.2.G) should be produced.
func (r GenerateRequest) IsAPI() bool { return r.ApplicationType == "API" }

// Stage names a single unit of the batched generation pipeline (mirrors qa-ai).
type Stage string

const (
	StageAnalysis  Stage = "analysis"
	StageTestCases Stage = "test_cases"
	StageAux       Stage = "aux"
)

// StageRequest is the body qa-core sends to qa-ai's POST /stage.
type StageRequest struct {
	Stage      Stage                `json:"stage"`
	Req        GenerateRequest      `json:"req"`
	AC         *AcceptanceCriterion `json:"ac,omitempty"`
	StartIndex int                  `json:"start_index,omitempty"`
}

// ----- Result (LLM Output Contract, PRD §7) -----

type Result struct {
	RequirementAnalysis []RequirementFeature  `json:"requirement_analysis"`
	AcceptanceCriteria  []AcceptanceCriterion `json:"acceptance_criteria"`
	TestCases           []TestCase            `json:"test_cases"`
	EdgeCases           []EdgeCase            `json:"edge_cases"`
	TestData            TestData              `json:"test_data"`
	APITests            []APITest             `json:"api_tests,omitempty"`
	CoverageMatrix      []CoverageRow         `json:"coverage_matrix"`
	CoverageSummary     CoverageSummary       `json:"coverage_summary"`
	Ambiguities         []Ambiguity           `json:"ambiguities"`
	RequirementHealth   Score                 `json:"requirement_health"`
	TraceabilityScore   Score                 `json:"traceability_score"`
	MissingAreas        []string              `json:"missing_areas"`
}

type RequirementFeature struct {
	Feature         string   `json:"feature"`
	SubRequirements []string `json:"sub_requirements"`
	RiskLevel       string   `json:"risk_level"`
}

// AcceptanceCriterion is one acceptance criterion (AC).
type AcceptanceCriterion struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Module      string `json:"module"`
	Severity    string `json:"severity"`
	RiskLevel   string `json:"risk_level"`
}

type TestCase struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Type           string   `json:"type"` // Positive | Negative | Edge case | Trivial
	Technique      string   `json:"technique"`
	Preconditions  []string `json:"preconditions"`
	Steps          []string `json:"steps"`
	ExpectedResult string   `json:"expected_result"`
	Postconditions []string `json:"postconditions"` // state after the case (cleanup/teardown/effects)
	Priority       string   `json:"priority"`       // P0..P3 (model-set; derived if empty)
	Severity       string   `json:"severity"`       // Critical..Low (model-set; derived if empty)
	Tags           []string `json:"tags"`           // dimensions/labels, e.g. Security, Performance
	ActualResult   string   `json:"actual_result"`  // empty at generation; filled during manual execution
	Covers         []string `json:"covers"`         // AC traceability
	Format         string   `json:"format"`         // step-by-step | gherkin | checklist
	Risk           string   `json:"risk"`
}

type EdgeCase struct {
	ID             string `json:"id"`
	Scenario       string `json:"scenario"`
	ExpectedResult string `json:"expected_result"`
}

type TestData struct {
	ValidEmails    []string `json:"valid_emails"`
	InvalidEmails  []string `json:"invalid_emails"`
	BoundaryValues []string `json:"boundary_values"`
}

type APITest struct {
	RequestExample     string `json:"request_example"`
	ExpectedStatus     int    `json:"expected_status"`
	ResponseValidation string `json:"response_validation"`
}

type CoverageRow struct {
	RequirementPoint string   `json:"requirement_point"`
	CoveredBy        []string `json:"covered_by"`
	CoverageType     string   `json:"coverage_type"` // Positive · Negative · Edge case · Trivial
}

type CoverageSummary struct {
	Positive int `json:"positive"`
	Negative int `json:"negative"`
	EdgeCase int `json:"edge_case"`
	Trivial  int `json:"trivial"`
}

type Ambiguity struct {
	Location   string `json:"location"`
	Issue      string `json:"issue"`
	Suggestion string `json:"suggestion"`
	Severity   string `json:"severity"` // Critical · High · Medium · Low
}

// Score is an explainable 0–100 score: starts at 100, subtracts itemized
// deductions, clamped server-side (PRD §6c).
type Score struct {
	Score      int         `json:"score"`
	Rating     string      `json:"rating"`
	Deductions []Deduction `json:"deductions"`
}

type Deduction struct {
	Reason string `json:"reason"`
	Points int    `json:"points"` // negative
}
