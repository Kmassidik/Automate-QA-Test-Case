package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"qa-ai/internal/ollama"
	"qa-ai/internal/platform"
	"qa-ai/internal/readiness"
)

// fakeOllama simulates a daemon where the model is initially absent, becomes
// present only after /api/pull is called.
func fakeOllama() (*httptest.Server, *int32) {
	var pulled int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&pulled) == 1 {
			io.WriteString(w, `{"models":[{"name":"qwen2.5:7b"}]}`)
		} else {
			io.WriteString(w, `{"models":[]}`)
		}
	})
	mux.HandleFunc("/api/pull", func(w http.ResponseWriter, r *http.Request) {
		// Stream a little progress, then success; flip the model to "present".
		io.WriteString(w, `{"status":"downloading","total":100,"completed":50}`+"\n")
		io.WriteString(w, `{"status":"downloading","total":100,"completed":100}`+"\n")
		atomic.StoreInt32(&pulled, 1)
		io.WriteString(w, `{"status":"success"}`+"\n")
	})
	return httptest.NewServer(mux), &pulled
}

func TestWarmAutoPullsThenReady(t *testing.T) {
	srv, pulled := fakeOllama()
	defer srv.Close()

	llm := ollama.New(srv.URL, "qwen2.5:7b", 8192, 5*time.Second)
	rd := readiness.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	Warm(ctx, llm, rd, platform.Info{OS: "linux"}, log, Options{AutoPull: true, Retry: 10 * time.Millisecond})

	if !rd.Ready() {
		t.Fatalf("expected ready after auto-pull, got %+v", rd.Snapshot())
	}
	if atomic.LoadInt32(pulled) != 1 {
		t.Error("expected the model to have been pulled")
	}
}

func TestWarmNoAutoPullStaysDown(t *testing.T) {
	srv, _ := fakeOllama()
	defer srv.Close()

	llm := ollama.New(srv.URL, "qwen2.5:7b", 8192, 5*time.Second)
	rd := readiness.New()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// AutoPull off + model absent → one cycle should leave it not-ready with a
	// "ollama pull" instruction; cancel quickly so the retry loop exits.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	Warm(ctx, llm, rd, platform.Info{OS: "linux"}, log, Options{AutoPull: false, Retry: 10 * time.Millisecond})

	snap := rd.Snapshot()
	if rd.Ready() {
		t.Fatal("should not be ready when model missing and auto-pull disabled")
	}
	if !containsSub(snap.Detail, "ollama pull") {
		t.Errorf("detail should instruct manual pull, got %q", snap.Detail)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
