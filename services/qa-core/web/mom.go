package web

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"qa-core/internal/contract"
	"qa-core/internal/queue"
)

// handlePM renders the PM tab: upload one audio file -> Minutes of Meeting.
func (s *Server) handlePM(w http.ResponseWriter, r *http.Request) {
	snap := s.q.Snapshot()
	s.tmpl.render(w, "pm.html", map[string]any{
		"Tab":       "pm",
		"Status":    statusView(snap),
		"Backend":   backendView(s.be.Current()),
		"Models":    s.modelView(r.Context()),
		"Resources": statsView(s.be.CurrentStats()),
	})
}

// handlePMProcess receives the uploaded audio and enqueues a KindMOM job on the
// shared single-worker queue — so a meeting transcription serialises with QA
// generations and other uploads (one-at-a-time on the local GPU). It returns the
// waiting card; the SSE flow swaps in the editable minutes via /result/{id} when
// the job finishes, exactly like the QA tab.
func (s *Server) handlePMProcess(w http.ResponseWriter, r *http.Request) {
	file, hdr, err := r.FormFile("audio")
	if err != nil {
		s.renderPartialError(w, "Please choose an audio file to upload.")
		return
	}
	defer file.Close()

	// Persist the upload: the job may wait behind others, so the bytes must
	// outlive this request. The MOM runner removes the file when it's done.
	tmp, err := saveAudioUpload(file, hdr.Filename)
	if err != nil {
		s.renderPartialError(w, "Could not save the upload: "+err.Error())
		return
	}

	req := contract.GenerateRequest{
		Model:     s.getActiveModel(),
		AudioPath: tmp,
		AudioName: hdr.Filename,
	}
	job, err := s.q.Submit(queue.KindMOM, req)
	if err != nil {
		os.Remove(tmp)
		s.renderPartialError(w, err.Error())
		return
	}

	view, _ := s.q.JobView(job.ID)
	s.tmpl.render(w, "waiting.html", map[string]any{"Job": jobView(view)})
}

// saveAudioUpload streams the upload to a temp file, keeping the extension so
// ffmpeg can detect the container format.
func saveAudioUpload(src io.Reader, name string) (string, error) {
	ext := filepath.Ext(name)
	if ext == "" {
		ext = ".bin"
	}
	dst, err := os.CreateTemp("", "mom-audio-*"+ext)
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(dst.Name())
		return "", err
	}
	return dst.Name(), nil
}

// handlePMPrint renders the EDITED minutes as a print-ready HTML page styled to
// match the frozen docx template exactly. The user saves it as PDF via the
// browser's print dialog (WYSIWYG — the CSS is the layout, no server-side PDF
// engine). Edits to names/dates/wording flow straight in.
func (s *Server) handlePMPrint(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not read the form", http.StatusBadRequest)
		return
	}
	m := momFromForm(r)
	s.tmpl.render(w, "mom_print.html", map[string]any{
		"DateTime":    m.DateTime,
		"DateOnly":    meetingDateOnly(m.DateTime),
		"Location":    m.Location,
		"Purpose":     m.Purpose,
		"Attendees":   m.Attendees,
		"Discussions": numberRows(m.Discussions),
		"FollowUps":   numberRows(m.FollowUps),
		"PreparedBy":  m.PreparedBy,
	})
}

// printRow is a numbered minutes row for the print template (templates can't do
// arithmetic, so the index is precomputed here).
type printRow struct {
	N           int
	Title       string
	Description string
}

func numberRows(items []contract.MOMItem) []printRow {
	out := make([]printRow, 0, len(items))
	for i, it := range items {
		out = append(out, printRow{N: i + 1, Title: it.Title, Description: it.Description})
	}
	return out
}

// momFromForm reconstructs a MOM from the editable form. Discussion / follow-up
// rows arrive as parallel arrays; blank rows are dropped.
func momFromForm(r *http.Request) contract.MOM {
	return contract.MOM{
		DateTime:    formatMeetingDateTime(r.PostFormValue("date_time")),
		Location:    strings.TrimSpace(r.PostFormValue("location")),
		Purpose:     strings.TrimSpace(r.PostFormValue("purpose")),
		Attendees:   splitNonEmptyLines(r.PostFormValue("attendees")),
		PreparedBy:  strings.TrimSpace(r.PostFormValue("prepared_by")),
		Language:    strings.TrimSpace(r.PostFormValue("language")),
		Discussions: zipItems(r.PostForm["discussion_title"], r.PostForm["discussion_desc"]),
		FollowUps:   zipItems(r.PostForm["followup_title"], r.PostForm["followup_desc"]),
	}
}

// zipItems pairs parallel title/description arrays into MOMItems, skipping rows
// where both are blank.
func zipItems(titles, descs []string) []contract.MOMItem {
	n := len(titles)
	if len(descs) > n {
		n = len(descs)
	}
	var items []contract.MOMItem
	for i := 0; i < n; i++ {
		var t, d string
		if i < len(titles) {
			t = strings.TrimSpace(titles[i])
		}
		if i < len(descs) {
			d = strings.TrimSpace(descs[i])
		}
		if t == "" && d == "" {
			continue
		}
		items = append(items, contract.MOMItem{Title: t, Description: d})
	}
	return items
}

// formatMeetingDateTime converts the HTML datetime-local value
// ("2006-01-02T15:04") to the template's "DD-MM-YYYY / HH.MM WIB" form. Anything
// it can't parse (already-formatted or free text) passes through unchanged.
func formatMeetingDateTime(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("02-01-2006") + " / " + t.Format("15.04") + " WIB"
		}
	}
	return s
}

// toDateTimeLocal best-effort parses a stored DateTime back into the
// "2006-01-02T15:04" the datetime-local input needs to pre-fill. Returns "" if it
// can't (so the user just picks it).
func toDateTimeLocal(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{
		"02-01-2006 / 15.04 WIB", "02-01-2006 / 15.04", "2006-01-02T15:04",
		"02-01-2006 15.04", "02-01-2006", "2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("2006-01-02T15:04")
		}
	}
	return ""
}

// meetingDateOnly returns just the date part of a "DD-MM-YYYY / HH.MM WIB" string
// (the footer shows date only, matching the template).
func meetingDateOnly(s string) string {
	if i := strings.Index(s, " / "); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func splitNonEmptyLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// momResultView builds the template data for the editable minutes form.
func momResultView(res contract.MOMResult, source string) map[string]any {
	m := res.MOM
	// Ensure at least one empty row so the user can start adding if the model
	// returned none.
	disc := m.Discussions
	if len(disc) == 0 {
		disc = []contract.MOMItem{{}}
	}
	follow := m.FollowUps
	if len(follow) == 0 {
		follow = []contract.MOMItem{{}}
	}
	return map[string]any{
		"Source":        source,
		"DateTime":      m.DateTime,
		"DateTimeLocal": toDateTimeLocal(m.DateTime),
		"Location":      m.Location,
		"Purpose":       m.Purpose,
		"Attendees":     strings.Join(m.Attendees, "\n"),
		"PreparedBy":    m.PreparedBy,
		"Language":      m.Language,
		"Discussions":   disc,
		"FollowUps":     follow,
		"Transcript":    res.Transcript,
	}
}
