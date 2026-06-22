package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"qa-ai/internal/contract"
	"qa-ai/internal/mom"
)

// maxAudioBytes caps the upload (meeting recordings can be large, but bound it so
// a stray huge file can't exhaust disk). ~200 MB covers a multi-hour compressed
// recording.
const maxAudioBytes = 200 << 20

// handleMOM is the PM tab's worker endpoint: a multipart upload with an "audio"
// file (and optional "model" field). It transcribes the audio (whisper.cpp) then
// generates Minutes of Meeting (Ollama), returning both the structured MOM and the
// raw transcript. One request at a time — qa-core owns the queue/serialisation.
func (s *Server) handleMOM(w http.ResponseWriter, r *http.Request) {
	if s.tr == nil || s.momGen == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error": "transcription is not configured on this qa-ai instance (set WHISPER_MODEL)",
		})
		return
	}
	if !s.gateReady(w) { // model must be loaded before we bother transcribing
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAudioBytes+(1<<20))
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid upload: " + err.Error()})
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, hdr, err := r.FormFile("audio")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing 'audio' file"})
		return
	}
	defer file.Close()

	tmp, err := saveUpload(file, hdr.Filename)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	defer os.Remove(tmp)

	model := r.FormValue("model")

	start := time.Now()
	transcript, err := s.tr.Transcribe(r.Context(), tmp)
	if err != nil {
		s.log.Error("mom transcribe failed", "err", err, "dur", time.Since(start))
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "transcription failed: " + err.Error()})
		return
	}
	sttDur := time.Since(start)

	genStart := time.Now()
	minutes, err := s.momGen.Generate(r.Context(), model, transcript)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, mom.ErrExhausted) {
			status = http.StatusUnprocessableEntity
		}
		s.log.Error("mom generate failed", "err", err, "dur", time.Since(genStart))
		writeJSON(w, status, map[string]any{"error": "minutes generation failed: " + err.Error()})
		return
	}

	s.log.Info("mom ok", "stt", s.tr.Name(), "stt_dur", sttDur, "gen_dur", time.Since(genStart),
		"discussions", len(minutes.Discussions), "follow_ups", len(minutes.FollowUps))
	writeJSON(w, http.StatusOK, contract.MOMResult{MOM: minutes, Transcript: transcript})
}

// saveUpload streams the multipart file to a temp file, preserving its extension
// so ffmpeg can detect the container format.
func saveUpload(src io.Reader, name string) (string, error) {
	ext := filepath.Ext(name)
	if ext == "" {
		ext = ".bin"
	}
	dst, err := os.CreateTemp("", "mom-upload-*"+ext)
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(dst.Name())
		return "", err
	}
	return dst.Name(), nil
}
