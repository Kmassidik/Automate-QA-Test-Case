package web

import (
	"net/http"
	"strings"

	"qa-core/internal/contract"
	"qa-core/internal/export"
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

// handlePMProcess receives the uploaded audio, sends it to qa-ai (transcribe ->
// minutes), and swaps in the EDITABLE minutes form. htmx target, so the page
// frame (nav/header) stays put. Processing is synchronous (it can take minutes).
func (s *Server) handlePMProcess(w http.ResponseWriter, r *http.Request) {
	file, hdr, err := r.FormFile("audio")
	if err != nil {
		s.renderPartialError(w, "Please choose an audio file to upload.")
		return
	}
	defer file.Close()

	res, err := s.ai.MOM(r.Context(), hdr.Filename, s.getActiveModel(), file)
	if err != nil {
		s.renderPartialError(w, "Could not create the minutes: "+err.Error())
		return
	}
	s.tmpl.render(w, "pm_result.html", momResultView(res, hdr.Filename))
}

// handlePMExport builds a PDF from the EDITED form values (not the raw AI output)
// so the user's corrections to names/dates/wording flow into the document.
func (s *Server) handlePMExport(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not read the form", http.StatusBadRequest)
		return
	}
	m := momFromForm(r)
	data, err := export.MOMPDF(m)
	if err != nil {
		http.Error(w, "PDF export failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="minutes-of-meeting.pdf"`)
	_, _ = w.Write(data)
}

// momFromForm reconstructs a MOM from the editable form. Discussion / follow-up
// rows arrive as parallel arrays; blank rows are dropped.
func momFromForm(r *http.Request) contract.MOM {
	return contract.MOM{
		DateTime:    strings.TrimSpace(r.PostFormValue("date_time")),
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
		"Source":      source,
		"DateTime":    m.DateTime,
		"Location":    m.Location,
		"Purpose":     m.Purpose,
		"Attendees":   strings.Join(m.Attendees, "\n"),
		"PreparedBy":  m.PreparedBy,
		"Language":    m.Language,
		"Discussions": disc,
		"FollowUps":   follow,
		"Transcript":  res.Transcript,
	}
}
