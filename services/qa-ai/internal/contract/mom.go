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

// MOMResult is the final minutes plus the raw transcript (shown in the UI).
type MOMResult struct {
	MOM        MOM    `json:"mom"`
	Transcript string `json:"transcript"`
}

// ----- Scalable map-reduce stages (qa-core drives these) -----

// TranscribeResult is POST /transcribe output: the text plus whisper's detected
// language code (e.g. "id", "en").
type TranscribeResult struct {
	Transcript string `json:"transcript"`
	Language   string `json:"language"`
}

// MOMExtract is the partial pulled from ONE transcript chunk (the MAP step).
type MOMExtract struct {
	Attendees   []string  `json:"attendees"`
	Discussions []MOMItem `json:"discussions"`
	FollowUps   []MOMItem `json:"follow_ups"`
}

// ExtractRequest asks qa-ai to extract notes from one chunk. Language is the
// resolved output-language NAME ("Indonesian"/"English"/"" = match source).
type ExtractRequest struct {
	Chunk    string `json:"chunk"`
	Language string `json:"language"`
	Model    string `json:"model,omitempty"`
}

// ConsolidateRequest asks qa-ai to merge all chunk partials into the final MOM.
type ConsolidateRequest struct {
	Partials []MOMExtract `json:"partials"`
	Language string       `json:"language"`
	Model    string       `json:"model,omitempty"`
}
