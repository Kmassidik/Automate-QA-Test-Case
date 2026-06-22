package contract

// MOM mirrors qa-ai's Minutes-of-Meeting contract. qa-core renders it (web view +
// PDF export) — the fixed strings (title, remarks, table headers) live in the
// renderer, not here. See services/qa-ai/internal/contract/mom.go.
type MOM struct {
	DateTime  string   `json:"date_time"`
	Location  string   `json:"location"`
	Purpose   string   `json:"purpose"`
	Attendees []string `json:"attendees"`

	Discussions []MOMItem `json:"discussions"`
	FollowUps   []MOMItem `json:"follow_ups"`

	PreparedBy string `json:"prepared_by"`
	Language   string `json:"language"`
}

// MOMItem is one numbered row: an optional bold Title lead-in plus a Description.
type MOMItem struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
}

// MOMResult is qa-ai's POST /mom response: the minutes plus the raw transcript.
// A KindMOM queue job stores this on Result.MOM (the queue keeps *Result for
// every job kind); QA jobs leave Result.MOM nil.
type MOMResult struct {
	MOM        MOM    `json:"mom"`
	Transcript string `json:"transcript"`
}
