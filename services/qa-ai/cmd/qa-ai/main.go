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
	"qa-ai/internal/mom"
	"qa-ai/internal/ollama"
	"qa-ai/internal/platform"
	"qa-ai/internal/readiness"
	"qa-ai/internal/transcribe"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg := loadConfig()
	plat := platform.Detect()
	llm := ollama.New(cfg.ollamaURL, cfg.ollamaModel, cfg.ollamaNumCtx, cfg.genTimeout)
	gen := generate.New(llm, cfg.maxRetries)
	rd := readiness.New()

	// PM tab (Minutes of Meeting): a transcriber + a MOM generator. Real STT needs
	// whisper.cpp (WHISPER_MODEL); without it, MOM_STUB=true wires a canned
	// transcript so the pipeline still works for dev. Otherwise /mom returns 501.
	momGen := mom.New(llm, cfg.maxRetries)
	var tr transcribe.Transcriber
	switch {
	case cfg.whisperModel != "":
		tr = transcribe.WhisperCLI{WhisperBin: cfg.whisperBin, ModelPath: cfg.whisperModel, FFmpegBin: cfg.ffmpegBin}
		log.Info("MOM transcription enabled", "backend", tr.Name())
	case cfg.momStub:
		tr = transcribe.Stub{}
		log.Info("MOM transcription using STUB (set WHISPER_MODEL for real speech-to-text)")
	default:
		log.Info("MOM transcription disabled (set WHISPER_MODEL or MOM_STUB=true to enable the PM tab)")
	}
	srv := httpapi.New(gen, llm, rd, plat, log, tr, momGen)

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
		// Write timeout must exceed the slowest response: a full generation OR a
		// MOM run (transcription of a long recording + minutes). Take the larger.
		WriteTimeout: maxDur(cfg.genTimeout, cfg.momTimeout) + 30*time.Second,
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

	// PM tab / Minutes of Meeting transcription.
	whisperBin   string        // whisper.cpp CLI (brew: whisper-cpp -> whisper-cli)
	whisperModel string        // path to a ggml model; "" disables real STT
	ffmpegBin    string        // ffmpeg for audio normalisation
	momStub      bool          // wire a canned transcript when whisper isn't available
	momTimeout   time.Duration // upper bound for transcribe + minutes (long recordings)
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

		whisperBin:   env("WHISPER_BIN", "whisper-cli"),
		whisperModel: env("WHISPER_MODEL", ""),
		ffmpegBin:    env("FFMPEG_BIN", "ffmpeg"),
		momStub:      envBool("MOM_STUB", false),
		momTimeout:   envDuration("MOM_TIMEOUT", 1200*time.Second),
	}
}

func maxDur(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
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
