package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"qa-core/internal/aiclient"
	"qa-core/internal/backend"
	"qa-core/internal/contract"
	"qa-core/internal/export"
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
	log    *slog.Logger
}

func NewServer(access *Access, q *queue.Manager, bc *sse.Broadcaster, ai *aiclient.Client, be *backend.Monitor, log *slog.Logger) (*Server, error) {
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
	gated.HandleFunc("POST /generate", s.handleGenerate)
	gated.HandleFunc("GET /events", s.handleEvents)
	gated.HandleFunc("GET /result/{id}", s.handleResult)
	gated.HandleFunc("GET /export/{kind}/{id}", s.handleExport)
	mux.Handle("/", s.access.Middleware(gated))

	return logRequests(s.log, mux)
}

// ----- pages -----

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	snap := s.q.Snapshot()
	s.tmpl.render(w, "index.html", map[string]any{
		"Status":   statusView(snap),
		"Backend":  backendView(s.be.Current()),
		"Examples": examples,
		"Options":  formOptions,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.access.Revoke(w)
	http.Redirect(w, r, "/access", http.StatusSeeOther)
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

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderPartialError(w, "Could not read the form.")
		return
	}
	req := parseForm(r)
	if req.Requirement == "" {
		s.renderPartialError(w, "Requirement is required.")
		return
	}

	job, err := s.q.Submit(req)
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
		opt := export.Options{PriorityScheme: job.Req.PriorityScheme, Requirement: job.Req.Requirement}
		header, rows := export.QARepositoryRows(*job.Result, opt)
		s.tmpl.render(w, "result.html", map[string]any{
			"ID":     job.ID,
			"Result": job.Result,
			"Header": header,
			"Rows":   rows,
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
		"global":  statusView(s.q.Snapshot()),
		"backend": backendView(s.be.Current()),
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

// parseForm maps the input form (PRD §5.1) to a GenerateRequest.
func parseForm(r *http.Request) contract.GenerateRequest {
	return contract.GenerateRequest{
		Requirement:          trimLimit(r.FormValue("requirement"), 3000),
		ApplicationType:      r.FormValue("application_type"),
		DetailLevel:          r.FormValue("detail_level"),
		TestTypes:            r.Form["test_types"],
		TestDesignTechniques: r.Form["test_design_techniques"],
		OutputFormat:         r.FormValue("output_format"),
		PriorityScheme:       r.FormValue("priority_scheme"),
		IncludePreconditions: r.FormValue("include_preconditions") != "",
		IncludeTestData:      r.FormValue("include_test_data") != "",
		GenerateEdgeCases:    r.FormValue("generate_edge_cases") != "",
		PlatformMatrix:       r.FormValue("platform_matrix"),
	}
}
