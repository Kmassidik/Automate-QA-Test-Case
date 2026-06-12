package web

import (
	"bytes"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"qa-core/internal/aiclient"
	"qa-core/internal/options"
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
	if !s.Busy && s.QueueLen == 0 {
		v.Label = "Available — submit now."
		return v
	}
	// Busy: the banner is for a bystander deciding whether to submit. Count the
	// running job too — "jobs ahead of you" = 1 running + everyone queued — and
	// show the honest wait (rolling-avg ETA × jobs ahead).
	eta := ""
	if s.ETAKnown && s.ETAPerJob > 0 {
		eta = " · " + humanDur(time.Duration(1+s.QueueLen)*s.ETAPerJob)
	}
	if s.QueueLen == 0 {
		v.Label = "Someone's generating now" + eta + " — submit to take the next slot."
	} else {
		v.Label = "Someone's generating · " + itoa(s.QueueLen) + " waiting" + eta +
			" — you'd be #" + itoa(s.QueueLen+1) + "."
	}
	return v
}

// humanDur renders a short "~45s" / "~2m" estimate for the busy banner.
func humanDur(d time.Duration) string {
	sec := int(d.Seconds() + 0.5)
	if sec < 60 {
		return "~" + itoa(sec) + "s"
	}
	return "~" + itoa((sec+30)/60) + "m"
}

// StepView is one plan step for the template + SSE.
type StepView struct {
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// JobStatusView is a submitter's personal status (template + SSE `you` object).
type JobStatusView struct {
	ID         string     `json:"id"`
	State      string     `json:"state"`
	Position   int        `json:"position"`
	ETASeconds int        `json:"eta_seconds"`
	ETAKnown   bool       `json:"eta_known"`
	Error      string     `json:"error"`
	Steps      []StepView `json:"steps,omitempty"`
	Percent    int        `json:"percent"`    // 0..100, share of done steps
	StepLabel  string     `json:"step_label"` // current running (or last done) step
}

func jobView(v queue.JobView) JobStatusView {
	jv := JobStatusView{
		ID:         v.ID,
		State:      string(v.State),
		Position:   v.Position,
		ETASeconds: int(v.ETA.Seconds()),
		ETAKnown:   v.ETAKnown,
		Error:      v.Err,
	}
	if len(v.Plan) > 0 {
		done := 0
		for _, s := range v.Plan {
			jv.Steps = append(jv.Steps, StepView{Label: s.Label, State: string(s.State), Detail: s.Detail})
			switch s.State {
			case queue.StepDone:
				done++
			case queue.StepRunning:
				jv.StepLabel = s.Label
			}
		}
		jv.Percent = done * 100 / len(v.Plan)
		if jv.StepLabel == "" && done > 0 { // between steps: show the last finished one
			jv.StepLabel = jv.Steps[done-1].Label
		}
	}
	return jv
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

// ----- model picker -----

// ModelsView drives the header model selector: the installed models, the active
// choice (user's global selection or qa-ai's default), and whether the list was
// reachable (OK=false => backend down, selector disabled).
type ModelsView struct {
	Available []ModelChoice
	Active    string
	OK        bool
}

type ModelChoice struct {
	Name string
	Size string // human-readable, e.g. "4.7 GB"
}

// humanSize renders a byte count as a compact GB/MB string for the picker.
func humanSize(b int64) string {
	const (
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/gb)
	case b >= mb:
		return fmt.Sprintf("%.0f MB", float64(b)/mb)
	case b <= 0:
		return ""
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// ----- resource widget -----

// StatsView is the live resource widget (template seed + SSE `resources` object).
// On Apple Silicon there's no clean GPU-% API, so LoadedModel (name + VRAM split
// from Ollama /api/ps) is the honest GPU signal.
type StatsView struct {
	OK          bool   `json:"ok"`
	CPUPercent  int    `json:"cpu_percent"`
	MemPercent  int    `json:"mem_percent"`
	MemUsed     string `json:"mem_used"`
	MemTotal    string `json:"mem_total"`
	LoadedModel string `json:"loaded_model"` // "name · 4.7 GB (100% GPU)" or "" when none loaded
}

func statsView(s aiclient.Stats) StatsView {
	v := StatsView{OK: s.Reachable, CPUPercent: int(s.CPUPercent + 0.5)}
	if s.MemTotal > 0 {
		v.MemPercent = int(float64(s.MemUsed)/float64(s.MemTotal)*100 + 0.5)
		v.MemUsed = humanSize(s.MemUsed)
		v.MemTotal = humanSize(s.MemTotal)
	}
	if len(s.Loaded) > 0 {
		m := s.Loaded[0]
		label := m.Name
		if m.Size > 0 {
			gpuPct := int(float64(m.SizeVRAM)/float64(m.Size)*100 + 0.5)
			label += fmt.Sprintf(" · %s (%d%% on GPU)", humanSize(m.Size), gpuPct)
		}
		v.LoadedModel = label
	}
	return v
}

// ----- static form option lists (PRD §5.1) -----

type Option struct{ Value, Label string }

// FormView are the form choices rendered into index.html. The three vocab lists
// come from the editable options store (review.md #1); OutputFormats stay fixed
// in code because each value is coupled to the renderer/exports.
type FormView struct {
	ApplicationTypes []string
	CaseNatures      []string
	TestDimensions   []string
	OutputFormats    []Option
}

// outputFormats is intentionally NOT user-editable (each maps to renderer logic).
var outputFormats = []Option{
	{"step-by-step", "Step-by-step"},
	{"gherkin", "Gherkin / BDD (Given-When-Then)"},
}

func formViewFrom(o options.Options) FormView {
	return FormView{
		ApplicationTypes: o.ApplicationTypes,
		CaseNatures:      o.CaseNatures,
		TestDimensions:   o.TestDimensions,
		OutputFormats:    outputFormats,
	}
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
