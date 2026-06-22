package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"qa-core/internal/aiclient"
	"qa-core/internal/backend"
	"qa-core/internal/contract"
	"qa-core/internal/export"
	"qa-core/internal/options"
	"qa-core/internal/queue"
	"qa-core/internal/sse"
)

type Server struct {
	tmpl   *parsedTemplates
	access *Access
	q      *queue.Manager
	bc     *sse.Broadcaster
	ai     *aiclient.Client
	be     *backend.Monitor
	opts   *options.Store
	log    *slog.Logger

	// activeModel is the global, user-chosen Ollama model applied to every job.
	// Empty means "use qa-ai's configured default". Guarded by modelMu.
	modelMu     sync.RWMutex
	activeModel string
}

func (s *Server) getActiveModel() string {
	s.modelMu.RLock()
	defer s.modelMu.RUnlock()
	return s.activeModel
}

func (s *Server) setActiveModel(m string) {
	s.modelMu.Lock()
	s.activeModel = m
	s.modelMu.Unlock()
}

func NewServer(access *Access, q *queue.Manager, bc *sse.Broadcaster, ai *aiclient.Client, be *backend.Monitor, opts *options.Store, log *slog.Logger) (*Server, error) {
	t, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{
		tmpl:   &parsedTemplates{t},
		access: access,
		q:      q,
		bc:     bc,
		ai:     ai,
		be:     be,
		opts:   opts,
		log:    log,
	}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Public.
	mux.HandleFunc("GET /access", s.handleAccessPage)
	mux.HandleFunc("POST /access", s.handleAccessSubmit)
	mux.HandleFunc("GET /healthz", s.handleHealth)

	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServer(http.FS(staticSub)))))

	// Gated.
	gated := http.NewServeMux()
	gated.HandleFunc("GET /{$}", s.handleIndex)
	gated.HandleFunc("GET /logout", s.handleLogout)
	gated.HandleFunc("GET /settings", s.handleSettings)
	gated.HandleFunc("POST /settings", s.handleSaveSettings)
	gated.HandleFunc("POST /model", s.handleSetModel)
	gated.HandleFunc("POST /validate", s.handleValidate)
	gated.HandleFunc("POST /generate", s.handleGenerate)
	gated.HandleFunc("GET /events", s.handleEvents)
	gated.HandleFunc("GET /result/{id}", s.handleResult)
	gated.HandleFunc("GET /export/{kind}/{id}", s.handleExport)
	// PM tab — Minutes of Meeting (audio -> transcribe -> minutes -> PDF).
	gated.HandleFunc("GET /pm", s.handlePM)
	gated.HandleFunc("POST /pm/process", s.handlePMProcess)
	gated.HandleFunc("POST /pm/export.pdf", s.handlePMExport)
	mux.Handle("/", s.access.Middleware(gated))

	return logRequests(s.log, mux)
}

// ----- pages -----

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	snap := s.q.Snapshot()
	s.tmpl.render(w, "index.html", map[string]any{
		"Tab":       "qa",
		"Status":    statusView(snap),
		"Backend":   backendView(s.be.Current()),
		"Examples":  examples,
		"Options":   formViewFrom(s.opts.Get()),
		"Models":    s.modelView(r.Context()),
		"Resources": statsView(s.be.CurrentStats()),
	})
}

// handleSettings renders the editable form-vocabulary page (review.md #1).
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	o := s.opts.Get()
	s.tmpl.render(w, "settings.html", map[string]any{
		"ApplicationTypes": strings.Join(o.ApplicationTypes, "\n"),
		"CaseNatures":      strings.Join(o.CaseNatures, "\n"),
		"TestDimensions":   strings.Join(o.TestDimensions, "\n"),
		"Saved":            r.URL.Query().Get("saved") == "1",
	})
}

// handleSaveSettings persists edited vocabularies (one option per line). Empty
// lists fall back to defaults (handled in the store), so the form can't brick.
func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderPartialError(w, "Could not read the form.")
		return
	}
	o := options.Options{
		ApplicationTypes: splitLines(r.FormValue("application_types")),
		CaseNatures:      splitLines(r.FormValue("case_natures")),
		TestDimensions:   splitLines(r.FormValue("test_dimensions")),
	}
	if err := s.opts.Set(o); err != nil {
		s.log.Warn("settings persist failed", "err", err)
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.access.Revoke(w)
	http.Redirect(w, r, "/access", http.StatusSeeOther)
}

// handleSetModel sets the global active model (applies to every subsequent job).
func (s *Server) handleSetModel(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	s.setActiveModel(r.FormValue("model"))
	w.WriteHeader(http.StatusNoContent)
}

// modelView builds the model-picker view: installed models + the active choice
// (the user's global selection, or qa-ai's default when unset). Best-effort —
// a down backend yields OK=false and an empty list.
func (s *Server) modelView(ctx context.Context) ModelsView {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ml, err := s.ai.Models(cctx)
	active := s.getActiveModel()
	if active == "" {
		active = ml.Default
	}
	v := ModelsView{Active: active, OK: err == nil}
	for _, m := range ml.Models {
		v.Available = append(v.Available, ModelChoice{Name: m.Name, Size: humanSize(m.Size)})
	}
	return v
}

func (s *Server) handleAccessPage(w http.ResponseWriter, r *http.Request) {
	if s.access.Check(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.tmpl.render(w, "access.html", map[string]any{})
}

func (s *Server) handleAccessSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if s.access.Grant(w, r.FormValue("access_code")) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusUnauthorized)
	s.tmpl.render(w, "access.html", map[string]any{"Error": "Incorrect access code."})
}

// ----- generation -----

// handleValidate is step 1 of the two-step flow (analysis only); handleGenerate
// is step 2 (full generation). Both enqueue onto the same FIFO queue, so every
// logged-in user's dashboard sees the live busy/queue state regardless of kind.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	s.submitForm(w, r, queue.KindValidate)
}

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	s.submitForm(w, r, queue.KindGenerate)
}

func (s *Server) submitForm(w http.ResponseWriter, r *http.Request, kind queue.Kind) {
	if err := r.ParseForm(); err != nil {
		s.renderPartialError(w, "Could not read the form.")
		return
	}
	req := parseForm(r)
	if req.Requirement == "" {
		s.renderPartialError(w, "Requirement is required.")
		return
	}
	req.Model = s.getActiveModel() // global active model (empty => qa-ai default)

	job, err := s.q.Submit(kind, req)
	if err != nil {
		s.renderPartialError(w, err.Error())
		return
	}

	view, _ := s.q.JobView(job.ID)
	s.tmpl.render(w, "waiting.html", map[string]any{"Job": jobView(view)})
}

// handleResult is polled/triggered after SSE says the job is done; it returns
// the rendered result, an error partial, or the waiting partial if still in flight.
func (s *Server) handleResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, ok := s.q.Get(id)
	if !ok {
		s.renderPartialError(w, "Job not found (it may have expired).")
		return
	}
	switch job.State {
	case queue.StateDone:
		if job.Kind == queue.KindValidate {
			s.tmpl.render(w, "validation.html", map[string]any{
				"ID":     job.ID,
				"Result": job.Result,
				"Req":    job.Req,
			})
			return
		}
		opt := export.Options{PriorityScheme: job.Req.PriorityScheme, Requirement: job.Req.Requirement}
		header, rows := export.QARepositoryRows(*job.Result, opt)
		rtmHeader, rtmRows := export.RTMRows(*job.Result)
		s.tmpl.render(w, "result.html", map[string]any{
			"ID":        job.ID,
			"Result":    job.Result,
			"Header":    header,
			"Rows":      rows,
			"RTMHeader": rtmHeader,
			"RTMRows":   rtmRows,
			"Gaps":      export.GapCount(*job.Result),
		})
	case queue.StateFailed:
		s.renderPartialError(w, "Generation failed: "+job.Err)
	default:
		view, _ := s.q.JobView(id)
		s.tmpl.render(w, "waiting.html", map[string]any{"Job": jobView(view)})
	}
}

// ----- SSE live status (PRD §5.4) -----

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	jobID := r.URL.Query().Get("job")
	ticks, cancel := s.bc.Subscribe()
	defer cancel()

	// Send one frame immediately so the client doesn't wait for the first change.
	s.writeEvent(w, flusher, jobID)

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case _, open := <-ticks:
			if !open {
				return
			}
			s.writeEvent(w, flusher, jobID)
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n") // comment frame keeps the connection warm
			flusher.Flush()
		}
	}
}

func (s *Server) writeEvent(w http.ResponseWriter, flusher http.Flusher, jobID string) {
	payload := map[string]any{
		"global":    statusView(s.q.Snapshot()),
		"backend":   backendView(s.be.Current()),
		"resources": statsView(s.be.CurrentStats()),
	}
	if jobID != "" {
		if view, ok := s.q.JobView(jobID); ok {
			payload["you"] = jobView(view)
		}
	}
	b, _ := json.Marshal(payload)
	fmt.Fprintf(w, "event: tick\ndata: %s\n\n", b)
	flusher.Flush()
}

// ----- exports -----

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	id := r.PathValue("id")
	job, ok := s.q.Get(id)
	if !ok || job.Result == nil {
		http.Error(w, "result not available", http.StatusNotFound)
		return
	}
	opt := export.Options{
		PriorityScheme: job.Req.PriorityScheme,
		Requirement:    job.Req.Requirement,
	}

	var (
		data        []byte
		err         error
		contentType string
		filename    string
	)
	switch kind {
	case "qa-csv":
		data, err = export.QARepositoryCSV(*job.Result, opt)
		contentType, filename = "text/csv", "qa-repository-"+id+".csv"
	case "jira-csv":
		data, err = export.JiraCSV(*job.Result, opt)
		contentType, filename = "text/csv", "jira-import-"+id+".csv"
	case "markdown":
		data = export.Markdown(*job.Result, opt)
		contentType, filename = "text/markdown", "qa-report-"+id+".md"
	case "rtm":
		data, err = export.RTM(*job.Result, opt)
		contentType, filename = "text/csv", "rtm-"+id+".csv"
	default:
		http.Error(w, "unknown export kind", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "export failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType+"; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	_, _ = w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	status := map[string]any{"status": "ok"}
	code := http.StatusOK
	if err := s.ai.Health(ctx); err != nil {
		status["status"] = "degraded"
		status["qa_ai"] = err.Error()
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) renderPartialError(w http.ResponseWriter, msg string) {
	s.tmpl.render(w, "error.html", map[string]any{"Message": msg})
}

// splitLines parses a textarea (one option per line) into a trimmed list.
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if v := strings.TrimSpace(line); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// parseForm maps the input form (PRD §5.1) to a GenerateRequest.
// parseForm maps the simplified v1.3 form (review.md §2). Removed fields are
// hardcoded to sensible defaults: QA is detail-oriented (always Detailed +
// always include preconditions/test-data/edge-cases) and priority is always
// auto-mapped (P0-P3). Test design technique is no longer requested.
func parseForm(r *http.Request) contract.GenerateRequest {
	return contract.GenerateRequest{
		Requirement:          trimLimit(r.FormValue("requirement"), 3000),
		ApplicationType:      r.FormValue("application_type"),
		DetailLevel:          "Detailed",
		TestTypes:            r.Form["test_types"],      // case natures
		TestDimensions:       r.Form["test_dimensions"], // functional + non-functional
		TestDesignTechniques: nil,
		OutputFormat:         r.FormValue("output_format"),
		PriorityScheme:       "P0-P3",
		IncludePreconditions: true,
		IncludeTestData:      true,
		GenerateEdgeCases:    true,
		PlatformMatrix:       r.FormValue("platform_matrix"),
		Clarifications:       strings.TrimSpace(r.FormValue("clarifications")),
		AcceptanceCriteria:   parseCuratedACs(r),
		CasesPerType:         atoiDefault(r.FormValue("cases_per_type"), 3),
	}
}

func atoiDefault(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}

// parseCuratedACs reads the editable acceptance-criteria rows from the Step-1
// review page (parallel form arrays). Blank descriptions are dropped and IDs are
// renumbered AC-1..N so user additions/removals stay consistent. Returns nil when
// the form has no AC rows (i.e. a direct generate, not via the review page).
func parseCuratedACs(r *http.Request) []contract.AcceptanceCriterion {
	descs := r.Form["ac_desc"]
	if len(descs) == 0 {
		return nil
	}
	modules := r.Form["ac_module"]
	severities := r.Form["ac_severity"]
	risks := r.Form["ac_risk"]

	at := func(s []string, i int) string {
		if i < len(s) {
			return s[i]
		}
		return ""
	}

	var out []contract.AcceptanceCriterion
	n := 0
	for i, d := range descs {
		d = strings.TrimSpace(d)
		if d == "" {
			continue // dropped (a removed/empty row)
		}
		n++
		out = append(out, contract.AcceptanceCriterion{
			ID:          fmt.Sprintf("AC-%d", n), // renumber so add/remove stays consistent
			Description: d,
			Module:      at(modules, i),
			Severity:    at(severities, i),
			RiskLevel:   at(risks, i),
		})
	}
	return out
}
