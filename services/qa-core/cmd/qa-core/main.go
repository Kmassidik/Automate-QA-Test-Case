// Command qa-core is the gatekeeper service: it serves the UI, gates access
// behind a shared code, owns the FIFO queue + single worker, tracks ETA,
// broadcasts live status over SSE, and renders exports. It calls qa-ai one
// request at a time.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"qa-core/internal/aiclient"
	"qa-core/internal/backend"
	"qa-core/internal/contract"
	"qa-core/internal/eta"
	"qa-core/internal/queue"
	"qa-core/internal/sse"
	"qa-core/web"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := loadConfig()
	if err != nil {
		log.Error("config error", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Wiring: broadcaster <- queue OnChange; queue runner -> qa-ai client.
	bc := sse.NewBroadcaster()
	ai := aiclient.New(cfg.aiURL, cfg.aiTimeout)
	etaTracker := eta.New(cfg.etaWindow)
	q := queue.New(queue.Config{
		Buffer:     cfg.queueBuffer,
		GenTimeout: cfg.aiTimeout,
		ETA:        etaTracker,
		Runner:     genRunner(ai),
		Validator:  validateRunner(ai),
		OnChange:   bc.Publish,
	})
	q.Start(ctx)

	// Backend monitor: poll qa-ai/healthz and push changes (model name, pull %,
	// offline) to browsers via the same SSE broadcaster.
	be := backend.NewMonitor(ai, bc.Publish)
	be.Start(ctx, 3*time.Second)

	access := web.NewAccess(cfg.accessCode)
	srv, err := web.NewServer(access, q, bc, ai, be, log)
	if err != nil {
		log.Error("server init", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              cfg.addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: SSE connections are long-lived; per-job timeouts are
		// enforced inside the queue worker instead.
	}

	go func() {
		log.Info("qa-core listening", "addr", cfg.addr, "qa_ai", cfg.aiURL, "queue_buffer", cfg.queueBuffer)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

// genRunner wraps the qa-ai full generation as a single-step plan. Commit 3
// replaces this with the batched, per-AC orchestrator that reports 1..N steps.
func genRunner(ai *aiclient.Client) queue.Runner {
	return func(ctx context.Context, req contract.GenerateRequest, p *queue.Progress) (contract.Result, error) {
		p.Plan("Generating test cases")
		p.Start(0)
		res, err := ai.Generate(ctx, req)
		if err != nil {
			p.Fail(0, err.Error())
			return res, err
		}
		p.Done(0, fmt.Sprintf("%d test cases", len(res.TestCases)))
		return res, nil
	}
}

func validateRunner(ai *aiclient.Client) queue.Runner {
	return func(ctx context.Context, req contract.GenerateRequest, p *queue.Progress) (contract.Result, error) {
		p.Plan("Analyzing requirement")
		p.Start(0)
		res, err := ai.Validate(ctx, req)
		if err != nil {
			p.Fail(0, err.Error())
			return res, err
		}
		p.Done(0, "")
		return res, nil
	}
}

type config struct {
	addr        string
	aiURL       string
	accessCode  string
	queueBuffer int
	etaWindow   int
	aiTimeout   time.Duration
}

func loadConfig() (config, error) {
	c := config{
		addr:        env("QA_CORE_ADDR", ":8080"),
		aiURL:       env("QA_AI_URL", "http://localhost:8081"),
		accessCode:  os.Getenv("ACCESS_CODE"),
		queueBuffer: envInt("QUEUE_BUFFER", 256),
		etaWindow:   envInt("ETA_WINDOW", 20),
		aiTimeout:   envDuration("QA_AI_TIMEOUT", 200*time.Second),
	}
	if c.accessCode == "" {
		return c, errors.New("ACCESS_CODE is required (set it in .env or the deploy environment)")
	}
	return c, nil
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
