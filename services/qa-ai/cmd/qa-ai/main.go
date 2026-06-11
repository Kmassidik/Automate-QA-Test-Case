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

	"qa-ai/internal/bootstrap"
	"qa-ai/internal/generate"
	"qa-ai/internal/httpapi"
	"qa-ai/internal/ollama"
	"qa-ai/internal/platform"
	"qa-ai/internal/readiness"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := loadConfig()
	plat := platform.Detect()
	llm := ollama.New(cfg.ollamaURL, cfg.ollamaModel, cfg.ollamaNumCtx, cfg.genTimeout)
	gen := generate.New(llm, cfg.maxRetries)
	rd := readiness.New()
	srv := httpapi.New(gen, llm, rd, plat, log)

	// Background warmup: wait for Ollama (OS-aware guidance), auto-pull the model
	// if missing, flip readiness to Ready. The server is up immediately; until
	// warmup finishes, /generate refuses with the reason instead of hanging.
	warmCtx, stopWarm := context.WithCancel(context.Background())
	defer stopWarm()
	go bootstrap.Warm(warmCtx, llm, rd, plat, log, bootstrap.Options{
		AutoPull: cfg.autoPull,
		Retry:    cfg.warmupRetry,
	})

	httpSrv := &http.Server{
		Addr:              cfg.addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// Write timeout must exceed a full generation (slow 7B on Metal): give
		// the queue/worker headroom over genTimeout.
		WriteTimeout: cfg.genTimeout + 30*time.Second,
	}

	go func() {
		log.Info("qa-ai listening", "addr", cfg.addr, "platform", plat.Label(), "model", cfg.ollamaModel, "ollama", cfg.ollamaURL, "auto_pull", cfg.autoPull)
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
	autoPull     bool
	warmupRetry  time.Duration
}

func loadConfig() config {
	return config{
		addr:         env("QA_AI_ADDR", ":8081"),
		ollamaURL:    env("OLLAMA_URL", "http://localhost:11434"),
		ollamaModel:  env("OLLAMA_MODEL", "qwen2.5:7b"),
		ollamaNumCtx: envInt("OLLAMA_NUM_CTX", 8192),
		maxRetries:   envInt("MAX_JSON_RETRIES", 3),
		genTimeout:   envDuration("GEN_TIMEOUT", 180*time.Second),
		autoPull:     envBool("OLLAMA_AUTO_PULL", true),
		warmupRetry:  envDuration("WARMUP_RETRY", 5*time.Second),
	}
}

func envBool(k string, def bool) bool {
	switch os.Getenv(k) {
	case "1", "true", "TRUE", "yes":
		return true
	case "0", "false", "FALSE", "no":
		return false
	default:
		return def
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
