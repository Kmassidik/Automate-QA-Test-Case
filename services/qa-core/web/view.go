package web

import (
	"bytes"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"qa-core/internal/aiclient"
	"qa-core/internal/queue"
)

type parsedTemplates struct{ t *template.Template }

func (p *parsedTemplates) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer // render to a buffer so a template error doesn't emit a half page
	if err := p.t.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// StatusView is the global queue status, used both in templates and as the
// `global` object in SSE frames (hence JSON tags).
type StatusView struct {
	Busy       bool   `json:"busy"`
	QueueLen   int    `json:"queue_len"`
	ETASeconds int    `json:"eta_seconds"`
	ETAKnown   bool   `json:"eta_known"`
	Label      string `json:"label"`
}

func statusView(s queue.Snapshot) StatusView {
	v := StatusView{
		Busy:       s.Busy,
		QueueLen:   s.QueueLen,
		ETAKnown:   s.ETAKnown,
		ETASeconds: int(s.ETAPerJob.Seconds()),
	}
	switch {
	case !s.Busy && s.QueueLen == 0:
		v.Label = "Available — submit now."
	case s.QueueLen == 0:
		v.Label = "Generating now."
	default:
		v.Label = "Generating now · " + itoa(s.QueueLen) + " in queue."
	}
	return v
}

// JobStatusView is a submitter's personal status (template + SSE `you` object).
type JobStatusView struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Position   int    `json:"position"`
	ETASeconds int    `json:"eta_seconds"`
	ETAKnown   bool   `json:"eta_known"`
	Error      string `json:"error"`
}

func jobView(v queue.JobView) JobStatusView {
	return JobStatusView{
		ID:         v.ID,
		State:      string(v.State),
		Position:   v.Position,
		ETASeconds: int(v.ETA.Seconds()),
		ETAKnown:   v.ETAKnown,
		Error:      v.Err,
	}
}

// BackendView is the real generation-backend state for the header (template +
// SSE `backend` object). Label is what the user sees; State drives styling.
type BackendView struct {
	Label string `json:"label"`
	State string `json:"state"` // ready | pulling | down | stub
}

func backendView(s aiclient.BackendStatus) BackendView {
	if !s.Reachable {
		return BackendView{Label: "LLM offline", State: "down"}
	}
	switch s.State {
	case "ready":
		if strings.EqualFold(s.Model, "stub") {
			return BackendView{Label: "stub backend", State: "stub"}
		}
		return BackendView{Label: s.Model, State: "ready"} // e.g. "qwen2.5:7b"
	case "pulling":
		label := "downloading model"
		if s.Progress != "" {
			label += " " + s.Progress
		}
		return BackendView{Label: label, State: "pulling"}
	case "ollama_down":
		return BackendView{Label: "LLM offline", State: "down"}
	default:
		return BackendView{Label: "starting…", State: "down"}
	}
}

// ----- static form option lists (PRD §5.1) -----

type Option struct{ Value, Label string }

// FormOptions are the simplified v1.3 form choices (review.md §2). Detail level,
// test design technique, and priority scheme were removed — generation is always
// detailed and priority is auto-mapped.
type FormOptions struct {
	ApplicationTypes []string
	TestTypes        []string
	OutputFormats    []Option
}

var formOptions = FormOptions{
	ApplicationTypes: []string{"Web", "Mobile", "Desktop"},
	TestTypes:        []string{"Positive", "Negative", "Edge case", "Trivial"},
	OutputFormats: []Option{
		{"step-by-step", "Step-by-step"},
		{"gherkin", "Gherkin / BDD (Given-When-Then)"},
	},
}

// Example seeds the "example requirements" picker (PRD §5.3).
type Example struct {
	Name            string `json:"name"`
	Requirement     string `json:"requirement"`
	ApplicationType string `json:"application_type"`
}

var examples = []Example{
	{
		Name:            "Password reset",
		ApplicationType: "Web",
		Requirement:     "As a registered user, I can reset my password by requesting a reset link sent to my email. The link expires after 30 minutes and can be used once. The new password must meet the password policy. After a successful reset, all existing sessions are invalidated.",
	},
	{
		Name:            "Checkout payment",
		ApplicationType: "Web",
		Requirement:     "The checkout flow accepts a cart and a payment token, charges the customer, and returns an order confirmation. It must reject expired tokens, insufficient funds, and duplicate submissions (idempotency key). On success it shows an order ID.",
	},
	{
		Name:            "Mobile login with OTP",
		ApplicationType: "Mobile",
		Requirement:     "A user logs in with a phone number and a 6-digit OTP. The OTP is valid for 5 minutes and locks the account after 5 failed attempts. Successful login issues a session token.",
	},
}

// ----- middleware / helpers -----

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		next.ServeHTTP(w, r)
	})
}

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// SSE connections are long-lived; logging their full duration is noise.
		if r.URL.Path != "/events" {
			log.Info("http", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start))
		}
	})
}

func trimLimit(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

func itoa(n int) string {
	// small, allocation-light int->string for status labels
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
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
