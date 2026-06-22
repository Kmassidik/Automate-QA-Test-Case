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
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"qa-core/internal/aiclient"
	"qa-core/internal/backend"
	"qa-core/internal/contract"
	"qa-core/internal/eta"
	"qa-core/internal/export"
	"qa-core/internal/options"
	"qa-core/internal/orchestrator"
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
	ai := aiclient.New(cfg.aiURL, cfg.stageTimeout) // per qa-ai call (one stage)
	tlog := timingLogger(cfg.timingLog, log)        // per-job, per-stage timing breakdown
	etaTracker := eta.New(cfg.etaWindow)
	q := queue.New(queue.Config{
		Buffer: cfg.queueBuffer,
		// Whole-job budget: the batched orchestrator makes many sequential stage
		// calls (analysis + one per AC + aux), so this must be far larger than a
		// single stage timeout or long jobs get canceled mid-flight.
		GenTimeout: cfg.jobTimeout,
		ETA:        etaTracker,
		Runner:     orchestrator.GenRunner(ai, snapshotter(cfg.snapshotDir, log), tlog), // batched, per-AC, step-by-step + timing
		Validator:  validateRunner(ai),
		MOMRunner:  momRunner(ai), // PM tab: audio -> transcribe -> minutes
		OnChange:   bc.Publish,
	})
	q.Start(ctx)

	// Backend monitor: poll qa-ai/healthz and push changes (model name, pull %,
	// offline) to browsers via the same SSE broadcaster.
	be := backend.NewMonitor(ai, bc.Publish)
	be.Start(ctx, 3*time.Second)

	optStore := options.Load(cfg.optionsFile) // editable form vocabularies (review.md #1)

	access := web.NewAccess(cfg.accessCode)
	srv, err := web.NewServer(access, q, bc, ai, be, optStore, log)
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

// snapshotter returns an orchestrator.Snapshotter that writes a markdown trace
// of the work-in-progress result to dir/<jobID>.md after each step. Returns nil
// when dir is empty (in-memory only — the PRD's no-persistence default).
func snapshotter(dir string, log *slog.Logger) orchestrator.Snapshotter {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Warn("snapshot dir unavailable; disabling snapshots", "dir", dir, "err", err)
		return nil
	}
	return func(jobID string, res contract.Result) {
		path := filepath.Join(dir, jobID+".md")
		if err := os.WriteFile(path, export.Markdown(res, export.Options{}), 0o644); err != nil {
			log.Warn("snapshot write failed", "path", path, "err", err)
		}
	}
}

// validateRunner is Step 1 of the two-step flow. It runs the analysis stage so
// the review page can show the proposed ACCEPTANCE CRITERIA (which the QA then
// edits) — not just the breakdown/health.
// timingLogger returns a logger that appends a human-readable per-job, per-stage
// timing breakdown to dir/timing.log (tail -f to watch live; grep "job done" for
// totals). Returns nil (timing off) when path is empty or unopenable.
func timingLogger(path string, log *slog.Logger) *slog.Logger {
	if path == "" {
		return nil
	}
	if d := filepath.Dir(path); d != "" {
		_ = os.MkdirAll(d, 0o755)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Warn("timing log unavailable; disabling", "path", path, "err", err)
		return nil
	}
	log.Info("timing log enabled", "path", path)
	return slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func validateRunner(ai *aiclient.Client) queue.Runner {
	return func(ctx context.Context, req contract.GenerateRequest, p *queue.Progress) (contract.Result, error) {
		p.Plan("Analyzing requirement & proposing acceptance criteria")
		p.Start(0)
		res, err := ai.Stage(ctx, contract.StageRequest{Stage: contract.StageAnalysis, Req: req})
		if err != nil {
			p.Fail(0, err.Error())
			return res, err
		}
		p.Done(0, fmt.Sprintf("%d acceptance criteria", len(res.AcceptanceCriteria)))
		return res, nil
	}
}

// momRunner is the PM tab's queue runner: it opens the uploaded audio (saved to a
// temp file by the handler), sends it to qa-ai (transcribe -> minutes), and
// returns the minutes inside Result.MOM. The temp file is removed when done.
// Running through the queue means a MOM job serialises with QA jobs — one heavy
// task on the local GPU at a time.
func momRunner(ai *aiclient.Client) queue.Runner {
	return func(ctx context.Context, req contract.GenerateRequest, p *queue.Progress) (contract.Result, error) {
		defer os.Remove(req.AudioPath)
		p.Plan("Transcribe audio & write minutes")
		p.Start(0)
		f, err := os.Open(req.AudioPath)
		if err != nil {
			p.Fail(0, "could not open the upload")
			return contract.Result{}, err
		}
		defer f.Close()
		res, err := ai.MOM(ctx, req.AudioName, req.Model, f)
		if err != nil {
			p.Fail(0, err.Error())
			return contract.Result{}, err
		}
		cp := res
		p.Done(0, fmt.Sprintf("%d discussion points", len(res.MOM.Discussions)))
		return contract.Result{MOM: &cp}, nil
	}
}

type config struct {
	addr         string
	aiURL        string
	accessCode   string
	queueBuffer  int
	etaWindow    int
	stageTimeout time.Duration // one qa-ai call (a single pipeline stage)
	jobTimeout   time.Duration // the whole batched generation (all stages)
	snapshotDir  string
	optionsFile  string
	timingLog    string
}

func loadConfig() (config, error) {
	c := config{
		addr:        env("QA_CORE_ADDR", ":8080"),
		aiURL:       env("QA_AI_URL", "http://localhost:8081"),
		accessCode:  os.Getenv("ACCESS_CODE"),
		queueBuffer: envInt("QUEUE_BUFFER", 256),
		etaWindow:   envInt("ETA_WINDOW", 20),
		// STAGE_TIMEOUT bounds a single qa-ai call; JOB_TIMEOUT bounds the whole
		// batched run (many stages). QA_AI_TIMEOUT kept as a back-compat alias for
		// the stage timeout. Job default is generous so multi-AC runs don't get cut.
		// Stage default covers a single qa-ai call PLUS its JSON-retry budget
		// (1 + MAX_JSON_RETRIES attempts), so a retrying stage isn't cut off.
		stageTimeout: envDuration("STAGE_TIMEOUT", envDuration("QA_AI_TIMEOUT", 600*time.Second)),
		jobTimeout:   envDuration("JOB_TIMEOUT", 1800*time.Second),
		snapshotDir:  os.Getenv("SNAPSHOT_DIR"),                  // empty => in-memory only
		optionsFile:  env("OPTIONS_FILE", "./data/options.json"), // editable form vocabularies
		timingLog:    env("TIMING_LOG", "./data/timing.log"),     // per-stage timing for tuning ("" disables)
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
