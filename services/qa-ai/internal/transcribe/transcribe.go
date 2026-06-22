// Package transcribe turns an uploaded audio file into plain text. The real
// implementation shells out to whisper.cpp (offline, Metal-accelerated on the
// Mac), preceded by an ffmpeg step that normalises any input format to the 16 kHz
// mono WAV whisper.cpp expects. A stub implementation lets the rest of the
// pipeline (MOM generation, rendering) be developed/tested without whisper present.
package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Transcriber converts an audio file into a transcript plus the detected language
// code (e.g. "id", "en"; "" if unknown).
type Transcriber interface {
	Transcribe(ctx context.Context, audioPath string) (text, language string, err error)
	// Name is shown in logs/health so the UI can say which backend is active.
	Name() string
}

// WhisperCLI runs ffmpeg + whisper.cpp as subprocesses. All three paths are
// configurable so deployment can point at brew-installed binaries and a chosen
// ggml model (e.g. ggml-small.bin for a good speed/accuracy balance).
type WhisperCLI struct {
	WhisperBin string // e.g. "whisper-cli" (brew: whisper-cpp)
	ModelPath  string // path to a ggml model, e.g. ".../ggml-small.bin"
	FFmpegBin  string // e.g. "ffmpeg"
	WorkDir    string // scratch dir for the wav + txt
}

func (w WhisperCLI) Name() string { return "whisper.cpp (" + filepath.Base(w.ModelPath) + ")" }

// Transcribe normalises the audio to 16 kHz mono WAV, runs whisper.cpp with
// automatic language detection, and returns the transcript text + detected
// language code.
func (w WhisperCLI) Transcribe(ctx context.Context, audioPath string) (string, string, error) {
	if strings.TrimSpace(w.ModelPath) == "" {
		return "", "", fmt.Errorf("whisper model path not configured (set WHISPER_MODEL)")
	}
	dir, err := os.MkdirTemp(w.WorkDir, "mom-stt-")
	if err != nil {
		return "", "", fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	wav := filepath.Join(dir, "audio.wav")
	// -y overwrite, -ar 16000 sample rate, -ac 1 mono, pcm_s16le 16-bit — exactly
	// what whisper.cpp wants; ffmpeg handles mp3/m4a/wav/ogg/etc. transparently.
	if out, err := run(ctx, w.ffmpeg(), "-y", "-i", audioPath,
		"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wav); err != nil {
		return "", "", fmt.Errorf("ffmpeg convert: %w (%s)", err, out)
	}

	outPrefix := filepath.Join(dir, "out")
	// -l auto detects language; -otxt writes <prefix>.txt; -oj writes <prefix>.json
	// (which carries the detected language); -nt strips inline timestamps.
	if out, err := run(ctx, w.whisper(), "-m", w.ModelPath, "-f", wav,
		"-l", "auto", "-otxt", "-oj", "-of", outPrefix, "-nt"); err != nil {
		return "", "", fmt.Errorf("whisper transcribe: %w (%s)", err, out)
	}

	txt, err := os.ReadFile(outPrefix + ".txt")
	if err != nil {
		return "", "", fmt.Errorf("read transcript: %w", err)
	}
	t := strings.TrimSpace(string(txt))
	if t == "" {
		return "", "", fmt.Errorf("empty transcript (no speech detected?)")
	}
	return t, readWhisperLang(outPrefix + ".json"), nil
}

// readWhisperLang pulls the detected language code from whisper.cpp's JSON output
// (result.language). Best-effort: "" if anything goes wrong.
func readWhisperLang(jsonPath string) string {
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		return ""
	}
	var doc struct {
		Result struct {
			Language string `json:"language"`
		} `json:"result"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return ""
	}
	return doc.Result.Language
}

func (w WhisperCLI) ffmpeg() string {
	if w.FFmpegBin != "" {
		return w.FFmpegBin
	}
	return "ffmpeg"
}

func (w WhisperCLI) whisper() string {
	if w.WhisperBin != "" {
		return w.WhisperBin
	}
	return "whisper-cli"
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return strings.TrimSpace(tail(buf.String(), 600)), err
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// Stub returns a fixed transcript. Used when no whisper backend is configured so
// the MOM pipeline can be exercised end-to-end without audio tooling installed.
type Stub struct{ Text string }

func (s Stub) Name() string { return "stub" }

func (s Stub) Transcribe(_ context.Context, _ string) (string, string, error) {
	if s.Text != "" {
		return s.Text, "id", nil
	}
	return "Rapat membahas progres tim AI. Mas Miftah, Mas Sultan, dan Kurnia hadir. " +
		"Dibahas penyesuaian Dashboard Agent termasuk action item Hide Agent, tooltip pada mode sistem, " +
		"dan peningkatan readability chat. Tindak lanjut: editing video tutorial dan kebutuhan AI engineer.", "id", nil
}
