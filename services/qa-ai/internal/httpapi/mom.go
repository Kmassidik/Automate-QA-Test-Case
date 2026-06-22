package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"qa-ai/internal/contract"
	"qa-ai/internal/mom"
)

// maxAudioBytes caps the upload (~200 MB covers a multi-hour compressed recording).
const maxAudioBytes = 200 << 20

// handleTranscribe is the first MOM stage: a multipart "audio" upload -> transcript
// text + detected language (whisper.cpp). No LLM needed, so it doesn't gate on
// model readiness.
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if s.tr == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{
			"error": "transcription is not configured (set WHISPER_MODEL)",
		})
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

	start := time.Now()
	transcript, lang, err := s.tr.Transcribe(r.Context(), tmp)
	if err != nil {
		s.log.Error("transcribe failed", "err", err, "dur", time.Since(start))
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "transcription failed: " + err.Error()})
		return
	}
	s.log.Info("transcribe ok", "backend", s.tr.Name(), "lang", lang, "chars", len(transcript), "dur", time.Since(start))
	writeJSON(w, http.StatusOK, contract.TranscribeResult{Transcript: transcript, Language: lang})
}

// handleMOMExtract is the MAP stage: one transcript chunk -> partial notes.
func (s *Server) handleMOMExtract(w http.ResponseWriter, r *http.Request) {
	var req contract.ExtractRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if !s.gateReady(w) {
		return
	}
	start := time.Now()
	out, err := s.momGen.Extract(r.Context(), req.Model, req.Language, req.Chunk)
	if err != nil {
		s.writeMOMErr(w, "mom:extract", err, start)
		return
	}
	s.log.Info("mom extract ok", "dur", time.Since(start), "discussions", len(out.Discussions), "follow_ups", len(out.FollowUps))
	writeJSON(w, http.StatusOK, out)
}

// handleMOMConsolidate is the REDUCE stage: all partials -> final MOM.
func (s *Server) handleMOMConsolidate(w http.ResponseWriter, r *http.Request) {
	var req contract.ConsolidateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid request body: " + err.Error()})
		return
	}
	if !s.gateReady(w) {
		return
	}
	start := time.Now()
	out, err := s.momGen.Consolidate(r.Context(), req.Model, req.Language, req.Partials)
	if err != nil {
		s.writeMOMErr(w, "mom:consolidate", err, start)
		return
	}
	s.log.Info("mom consolidate ok", "dur", time.Since(start), "discussions", len(out.Discussions), "follow_ups", len(out.FollowUps))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) writeMOMErr(w http.ResponseWriter, op string, err error, start time.Time) {
	status := http.StatusBadGateway
	if errors.Is(err, mom.ErrExhausted) {
		status = http.StatusUnprocessableEntity
	}
	s.log.Error(op+" failed", "err", err, "dur", time.Since(start))
	writeJSON(w, status, map[string]any{"error": err.Error()})
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
