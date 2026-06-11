// Command qa-ai is the stateless generation service: it builds prompts from
// form fields, calls native Ollama, validates JSON against the contract, and
// clamps scores. qa-core calls it one request at a time.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"qa-ai/internal/generate"
	"qa-ai/internal/httpapi"
	"qa-ai/internal/ollama"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := loadConfig()
	llm := ollama.New(cfg.ollamaURL, cfg.ollamaModel, cfg.ollamaNumCtx, cfg.genTimeout)
	gen := generate.New(llm, cfg.maxRetries)
	srv := httpapi.New(gen, llm, log)

	httpSrv := &http.Server{
		Addr:              cfg.addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// Write timeout must exceed a full generation (slow 7B on Metal): give
		// the queue/worker headroom over genTimeout.
		WriteTimeout: cfg.genTimeout + 30*time.Second,
	}

	go func() {
		log.Info("qa-ai listening", "addr", cfg.addr, "model", cfg.ollamaModel, "ollama", cfg.ollamaURL, "max_retries", cfg.maxRetries)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

type config struct {
	addr         string
	ollamaURL    string
	ollamaModel  string
	ollamaNumCtx int
	maxRetries   int
	genTimeout   time.Duration
}

func loadConfig() config {
	return config{
		addr:         env("QA_AI_ADDR", ":8081"),
		ollamaURL:    env("OLLAMA_URL", "http://localhost:11434"),
		ollamaModel:  env("OLLAMA_MODEL", "qwen2.5:7b"),
		ollamaNumCtx: envInt("OLLAMA_NUM_CTX", 8192),
		maxRetries:   envInt("MAX_JSON_RETRIES", 3),
		genTimeout:   envDuration("GEN_TIMEOUT", 180*time.Second),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
