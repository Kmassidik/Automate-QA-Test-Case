// Package httpapi exposes qa-ai over HTTP. qa-core calls POST /generate one
// request at a time (it owns the queue); qa-ai itself is stateless.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"qa-ai/internal/contract"
	"qa-ai/internal/generate"
	"qa-ai/internal/ollama"
)

type Server struct {
	gen *generate.Generator
	llm *ollama.Client
	log *slog.Logger
}

func New(gen *generate.Generator, llm *ollama.Client, log *slog.Logger) *Server {
	return &Server{gen: gen, llm: llm, log: log}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /generate", s.handleGenerate)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.llm.Health(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "degraded", "ollama": err.Error(), "model": s.llm.Model(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "model": s.llm.Model()})
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
