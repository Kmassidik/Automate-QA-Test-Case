// Package bootstrap runs qa-ai's warmup loop: it waits for Ollama to become
// reachable (logging OS-aware guidance while it isn't), then ensures the model
// is present — auto-pulling it if missing — and flips readiness to Ready. It
// runs in the background so the HTTP server is up immediately; generations are
// refused with a clear reason until warmup completes.
package bootstrap

import (
	"context"
	"log/slog"
	"time"

	"qa-ai/internal/ollama"
	"qa-ai/internal/platform"
	"qa-ai/internal/readiness"
)

type Options struct {
	AutoPull bool          // pull the model automatically if missing
	Retry    time.Duration // backoff between attempts while Ollama is down
}

// Warm blocks until the model is ready or ctx is cancelled. Intended to run in
// its own goroutine.
func Warm(ctx context.Context, llm *ollama.Client, rd *readiness.Readiness, plat platform.Info, log *slog.Logger, opts Options) {
	if opts.Retry <= 0 {
		opts.Retry = 5 * time.Second
	}
	guidance := plat.OllamaGuidance(llm.BaseURL())
	loggedDown := false

	for {
		if ctx.Err() != nil {
			return
		}

		// 1. Is the daemon reachable?
		if err := llm.Health(ctx); err != nil {
			rd.Set(readiness.StateOllamaDown, guidance)
			if !loggedDown { // log the guidance once, then quietly retry
				log.Warn("Ollama not reachable — waiting", "platform", plat.Label(), "url", llm.BaseURL(), "fix", guidance)
				loggedDown = true
			}
			if sleep(ctx, opts.Retry) {
				return
			}
			continue
		}
		loggedDown = false

		// 2. Is the model present?
		has, err := llm.HasModel(ctx)
		if err != nil {
			rd.Set(readiness.StateOllamaDown, "Ollama reachable but model check failed: "+err.Error())
			if sleep(ctx, opts.Retry) {
				return
			}
			continue
		}

		if !has {
			if !opts.AutoPull {
				rd.Set(readiness.StateOllamaDown, "Model "+llm.Model()+" not installed. Run: ollama pull "+llm.Model())
				log.Warn("model missing and auto-pull disabled", "model", llm.Model())
				if sleep(ctx, opts.Retry) {
					return
				}
				continue
			}
			if err := pull(ctx, llm, rd, log); err != nil {
				if ctx.Err() != nil {
					return
				}
				rd.Set(readiness.StateOllamaDown, "Model pull failed: "+err.Error())
				log.Error("model pull failed; will retry", "model", llm.Model(), "err", err)
				if sleep(ctx, opts.Retry) {
					return
				}
				continue
			}
		}

		rd.Set(readiness.StateReady, "ready")
		log.Info("qa-ai ready", "platform", plat.Label(), "model", llm.Model())
		return
	}
}

// pull streams the model download, logging throttled progress and reflecting it
// in readiness so the UI can show "downloading… 62%".
func pull(ctx context.Context, llm *ollama.Client, rd *readiness.Readiness, log *slog.Logger) error {
	rd.Set(readiness.StatePulling, "downloading model "+llm.Model())
	log.Info("model not installed — auto-pulling", "model", llm.Model())

	var lastPct = -1
	var lastLog = time.Now().Add(-time.Hour)
	return llm.Pull(ctx, func(p ollama.PullProgress) {
		if p.Total > 0 {
			pct := int(p.Completed * 100 / p.Total)
			rd.SetProgress(itoa(pct) + "%")
			// Log on a 5% step or every 5s, whichever comes first — avoids spam.
			if pct/5 != lastPct/5 || time.Since(lastLog) > 5*time.Second {
				log.Info("pulling model", "model", llm.Model(), "status", p.Status, "percent", pct)
				lastPct, lastLog = pct, time.Now()
			}
		} else if p.Status != "" {
			rd.Set(readiness.StatePulling, p.Status)
		}
	})
}

// sleep waits d or until ctx is done; returns true if ctx was cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
