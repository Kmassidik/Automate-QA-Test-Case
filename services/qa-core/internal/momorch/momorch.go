// Package momorch is the scalable Minutes-of-Meeting orchestrator. It drives
// qa-ai's stages (transcribe -> per-chunk extract -> consolidate) so a meeting of
// any length never overflows the model context. Intermediate state is persisted
// to a per-job working dir (transcript.txt, chunks.jsonl, partials.jsonl) so
// memory stays flat; the runner deletes the dir when it's done.
//
// It mirrors the QA orchestrator (orchestrator.GenRunner): a queue.Runner that
// reports a live 1..N step plan via Progress/SSE.
package momorch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"qa-core/internal/aiclient"
	"qa-core/internal/contract"
	"qa-core/internal/queue"
)

// Chunking: ~800-word windows with 100-word overlap keep each MAP call well
// within a 4k context while not cutting topics at the seam.
const (
	chunkWords   = 800
	overlapWords = 100
)

// MOMRunner returns the queue.Runner for KindMOM jobs. workRoot is the parent dir
// for per-job working directories (e.g. ./data/mom-jobs).
func MOMRunner(ai *aiclient.Client, workRoot string) queue.Runner {
	return func(ctx context.Context, req contract.GenerateRequest, p *queue.Progress) (contract.Result, error) {
		mom, err := run(ctx, ai, p, workRoot, req)
		if err != nil {
			return contract.Result{}, err
		}
		return contract.Result{MOM: &mom}, nil
	}
}

func run(ctx context.Context, ai *aiclient.Client, p *queue.Progress, workRoot string, req contract.GenerateRequest) (contract.MOMResult, error) {
	defer os.Remove(req.AudioPath) // the uploaded temp file (saved by the handler)

	jobDir := filepath.Join(workRoot, p.JobID())
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return contract.MOMResult{}, fmt.Errorf("work dir: %w", err)
	}
	defer os.RemoveAll(jobDir) // belt: cleaned on success/error/panic/timeout

	// ---- Stage 1: transcribe -------------------------------------------------
	p.Plan("Transcribe audio")
	p.Start(0)
	f, err := os.Open(req.AudioPath)
	if err != nil {
		p.Fail(0, "could not open the upload")
		return contract.MOMResult{}, err
	}
	tr, err := ai.Transcribe(ctx, req.AudioName, f)
	f.Close()
	if err != nil {
		p.Fail(0, err.Error())
		return contract.MOMResult{}, err
	}
	if err := os.WriteFile(filepath.Join(jobDir, "transcript.txt"), []byte(tr.Transcript), 0o644); err != nil {
		return contract.MOMResult{}, fmt.Errorf("write transcript: %w", err)
	}
	language := resolveLanguage(req.MOMLanguage, tr.Language)
	p.Done(0, "transcribed ("+orAuto(language)+")")

	// ---- Stage 2: chunk ------------------------------------------------------
	chunks := chunkText(tr.Transcript, chunkWords, overlapWords)
	if err := writeChunks(filepath.Join(jobDir, "chunks.jsonl"), chunks); err != nil {
		return contract.MOMResult{}, err
	}

	// ---- Stage 3: MAP — extract per chunk, append to partials.jsonl ----------
	partialsPath := filepath.Join(jobDir, "partials.jsonl")
	pf, err := os.Create(partialsPath)
	if err != nil {
		return contract.MOMResult{}, fmt.Errorf("partials file: %w", err)
	}
	enc := json.NewEncoder(pf)
	for i, chunk := range chunks {
		step := p.Append(fmt.Sprintf("Extract notes — chunk %d/%d", i+1, len(chunks)))
		p.Start(step)
		part, err := ai.Extract(ctx, contract.ExtractRequest{Chunk: chunk, Language: language, Model: req.Model})
		if err != nil {
			pf.Close()
			p.Fail(step, err.Error())
			return contract.MOMResult{}, err
		}
		if err := enc.Encode(part); err != nil { // one JSON object per line
			pf.Close()
			return contract.MOMResult{}, fmt.Errorf("write partial: %w", err)
		}
		p.Done(step, fmt.Sprintf("%d points, %d follow-ups", len(part.Discussions), len(part.FollowUps)))
	}
	pf.Close()

	// ---- Stage 4: REDUCE — stream partials back, consolidate -----------------
	step := p.Append("Consolidate minutes")
	p.Start(step)
	partials, err := readPartials(partialsPath)
	if err != nil {
		p.Fail(step, "read partials")
		return contract.MOMResult{}, err
	}
	mom, err := ai.Consolidate(ctx, contract.ConsolidateRequest{Partials: partials, Language: language, Model: req.Model})
	if err != nil {
		p.Fail(step, err.Error())
		return contract.MOMResult{}, err
	}
	p.Done(step, fmt.Sprintf("%d discussion points, %d follow-ups", len(mom.Discussions), len(mom.FollowUps)))

	return contract.MOMResult{MOM: mom, Transcript: tr.Transcript}, nil
}

// chunkText splits text into ~size-word windows that overlap by `overlap` words,
// so a topic spanning the seam is seen by both chunks. Returns one chunk for short
// transcripts.
func chunkText(text string, size, overlap int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	if len(words) <= size {
		return []string{strings.Join(words, " ")}
	}
	step := size - overlap
	if step < 1 {
		step = size
	}
	var chunks []string
	for start := 0; start < len(words); start += step {
		end := start + size
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[start:end], " "))
		if end == len(words) {
			break
		}
	}
	return chunks
}

func writeChunks(path string, chunks []string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("chunks file: %w", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i, c := range chunks {
		if err := enc.Encode(map[string]any{"idx": i, "text": c}); err != nil {
			return err
		}
	}
	return nil
}

// readPartials streams partials.jsonl line-by-line back into a slice. The MAP step
// never holds them all; only the (small) reduce input does.
func readPartials(path string) ([]contract.MOMExtract, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open partials: %w", err)
	}
	defer f.Close()
	var out []contract.MOMExtract
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e contract.MOMExtract
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("decode partial: %w", err)
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// resolveLanguage picks the output language NAME: an explicit non-Auto override
// wins; otherwise map whisper's detected code (id/en) to a name. "" => the model
// matches the source text.
func resolveLanguage(override, code string) string {
	if o := strings.TrimSpace(override); o != "" && !strings.EqualFold(o, "auto") {
		return o
	}
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "id":
		return "Indonesian"
	case "en":
		return "English"
	default:
		return ""
	}
}

func orAuto(lang string) string {
	if lang == "" {
		return "auto"
	}
	return lang
}
