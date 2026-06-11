// Package httpapi exposes qa-ai over HTTP. qa-core calls POST /generate one
// request at a time (it owns the queue); qa-ai itself is stateless.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"qa-ai/internal/contract"
	"qa-ai/internal/generate"
	"qa-ai/internal/ollama"
	"qa-ai/internal/platform"
	"qa-ai/internal/readiness"
)

type Server struct {
	gen  *generate.Generator
	llm  *ollama.Client
	rd   *readiness.Readiness
	plat platform.Info
	log  *slog.Logger
}

func New(gen *generate.Generator, llm *ollama.Client, rd *readiness.Readiness, plat platform.Info, log *slog.Logger) *Server {
	return &Server{gen: gen, llm: llm, rd: rd, plat: plat, log: log}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /generate", s.handleGenerate)
	mux.HandleFunc("POST /validate", s.handleValidate)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Readiness is driven by the warmup loop (daemon reachable + model present).
	// It already carries OS-aware guidance, so /healthz just reflects it.
	snap := s.rd.Snapshot()
	body := map[string]any{
		"status":   readinessStatus(snap.State),
		"state":    snap.State,
		"detail":   snap.Detail,
		"model":    s.llm.Model(),
		"platform": s.plat.Label(),
	}
	if snap.Progress != "" {
		body["progress"] = snap.Progress
	}
	code := http.StatusOK
	if !s.rd.Ready() {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, body)
}

func readinessStatus(s readiness.State) string {
	switch s {
	case readiness.StateReady:
		return "ok"
	case readiness.StatePulling:
		return "pulling"
	default:
		return "degraded"
	}
}

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeAndGate(w, r)
	if !ok {
		return
	}
	start := time.Now()
	res, err := s.gen.Generate(r.Context(), req)
	if err != nil {
		s.writeGenErr(w, "generate", err, start)
		return
	}
	s.log.Info("generate ok", "dur", time.Since(start), "test_cases", len(res.TestCases))
	writeJSON(w, http.StatusOK, res)
}

// handleValidate is step 1 of the two-step flow: analysis only, no test cases.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeAndGate(w, r)
	if !ok {
		return
	}
	start := time.Now()
	res, err := s.gen.Validate(r.Context(), req)
	if err != nil {
		s.writeGenErr(w, "validate", err, start)
		return
	}
	s.log.Info("validate ok", "dur", time.Since(start), "features", len(res.RequirementAnalysis))
	writeJSON(w, http.StatusOK, res)
}

// decodeAndGate decodes the request, enforces a non-empty requirement, and fails
// fast with the warmup's actionable reason if the model isn't ready. Shared by
// both /generate and /validate. Returns ok=false (after writing the response)
// when the caller should stop.
func (s *Server) decodeAndGate(w http.ResponseWriter, r *http.Request) (contract.GenerateRequest, bool) {
	var req contract.GenerateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return req, false
	}
	if len(req.Requirement) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "requirement is required"})
		return req, false
	}
	if !s.rd.Ready() {
		snap := s.rd.Snapshot()
		msg := snap.Detail
		if snap.State == readiness.StatePulling {
			msg = "Model is still downloading (" + snap.Progress + "). Please retry shortly."
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": msg, "state": snap.State})
		return req, false
	}
	return req, true
}

// writeGenErr maps a generate/validate failure to a status: retry-exhaustion is a
// 422 (request understood, model failed to comply); transport failures are 502.
func (s *Server) writeGenErr(w http.ResponseWriter, op string, err error, start time.Time) {
	status := http.StatusBadGateway
	if errors.Is(err, generate.ErrExhausted) {
		status = http.StatusUnprocessableEntity
	}
	s.log.Error(op+" failed", "err", err, "dur", time.Since(start))
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
