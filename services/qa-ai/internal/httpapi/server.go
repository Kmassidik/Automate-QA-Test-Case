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
	var req contract.GenerateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if len(req.Requirement) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "requirement is required"})
		return
	}

	// Fail fast with the warmup's actionable reason instead of attempting a call
	// that will just time out against a missing daemon/model.
	if !s.rd.Ready() {
		snap := s.rd.Snapshot()
		msg := snap.Detail
		if snap.State == readiness.StatePulling {
			msg = "Model is still downloading (" + snap.Progress + "). Please retry shortly."
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": msg, "state": snap.State})
		return
	}

	start := time.Now()
	res, err := s.gen.Generate(r.Context(), req)
	if err != nil {
		// Retry-exhaustion is a 422 (we understood the request, the model failed
		// to comply); transport failures are 502 (upstream Ollama problem).
		status := http.StatusBadGateway
		if errors.Is(err, generate.ErrExhausted) {
			status = http.StatusUnprocessableEntity
		}
		s.log.Error("generate failed", "err", err, "dur", time.Since(start))
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	s.log.Info("generate ok", "dur", time.Since(start), "test_cases", len(res.TestCases))
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
