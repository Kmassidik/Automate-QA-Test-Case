package contract

// MOM is the Minutes-of-Meeting artifact produced by the PM tab. The model fills
// the variable fields from a meeting transcript; fixed strings (the "MINUTES OF
// MEETING" title, the 24-hour remarks line, table headers) are renderer constants
// in qa-core, NOT part of this contract.
//
// The shape mirrors the user's frozen docx template: a header block, a numbered
// "Meeting Discussions and Info" table, a numbered "Follow Up" table, and a
// prepared/approved footer. The MOM is written in the SAME language as the audio
// (auto-detected), so Indonesian transcripts yield Indonesian minutes.
type MOM struct {
	DateTime  string   `json:"date_time"` // e.g. "07-05-2026 / 14.00 WIB" ("" if not stated)
	Location  string   `json:"location"`  // e.g. "Jakarta / Offline Meeting"
	Purpose   string   `json:"purpose"`   // one-line purpose of the meeting
	Attendees []string `json:"attendees"` // names mentioned / introduced

	Discussions []MOMItem `json:"discussions"` // the main "Discussions and Info" rows
	FollowUps   []MOMItem `json:"follow_ups"`  // action / follow-up rows

	PreparedBy string `json:"prepared_by"` // name of the minute-taker, if stated
	Language   string `json:"language"`    // detected output language, e.g. "Indonesian"
}

// MOMItem is one numbered row. Title is an optional bold lead-in (a short topic
// label); Description is the body. The renderer prints Title in bold then the
// Description, matching the template's "bold heading + paragraph" rows.
type MOMItem struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
}

// MOMResult is what qa-ai returns from POST /mom: the structured minutes plus the
// raw transcript (handy for the UI to show "from this transcript" / debugging).
type MOMResult struct {
	MOM        MOM    `json:"mom"`
	Transcript string `json:"transcript"`
}
