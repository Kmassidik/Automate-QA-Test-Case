# Scalable MOM Orchestrator — design

How the **PM tab** turns one audio file into Minutes of Meeting, and the plan to
make the `transcript → minutes` step **scale to meetings of any length**.

> Status: the basic version is **built and running** (single-shot). The scalable
> map-reduce version below is the **next tuning item** — not yet implemented.

---

## 1. What exists today (and why it doesn't scale)

```
upload audio → qa-core saves temp file → KindMOM job on the shared queue
            → qa-ai: ffmpeg → whisper.cpp → Ollama (ONE call, whole transcript)
            → MOM JSON → editable form
```

The weak link is the **single LLM call over the entire transcript**:

- **Context overflow** — a 1-hour meeting ≈ 8–15k words, but the model context is
  ~4k tokens. Long transcripts get silently truncated; the tail is lost.
- **Under-extraction** — one pass over a long input makes a small model miss
  discussion points and leave Follow-Up empty.
- **Fragile JSON** — one huge JSON response is more likely to truncate/malform →
  retries → slow or failed jobs.

---

## 2. The fix: chunk + map-reduce

The same shape the **QA orchestrator** already uses (`analysis → per-AC stages →
aux`): don't ask for everything at once — break it into bounded steps.

```
transcribe (whisper)
   │
chunk transcript        split into windows that fit the context.
   │                     prefer whisper's segment timestamps → natural ~5-min
   │                     boundaries, with a small overlap so a topic isn't cut
   │                     mid-sentence.
   │
MAP  (per chunk)        for each chunk: "extract the discussion points,
   │                     decisions and action items mentioned in THIS part" →
   │                     a small partial-MOM JSON. N steps, one-at-a-time on the
   │                     single worker. Live progress: "chunk 3 of 8".
   │
REDUCE (consolidate)    merge + dedupe all partials → final MOM JSON.
   │                     hierarchical (reduce in groups, then groups-of-groups)
   │                     if there are very many chunks.
   │
final minutes → editable form
```

**Why it scales**

| Problem today        | Map-reduce fix                                    |
|----------------------|---------------------------------------------------|
| Context overflow     | Each chunk fits the window — a 3-hr meeting is just more chunks |
| Under-extraction     | Every chunk is fully attended → Follow-Up actually fills in |
| Fragile big JSON     | Each call's output is small → reliable JSON, fewer retries |
| No progress          | Reuse `Progress` + SSE → "chunk 4 of 8" live      |

---

## 3. File-backed intermediate state (the key to flat memory)

Holding every chunk + partial in RAM defeats the purpose. Instead, **persist the
pipeline to a per-job working directory**, then **delete it when the job finishes**.

```
data/mom-jobs/<jobID>/
   transcript.txt     whisper output (written once)
   chunks.jsonl       1 line per chunk: {idx, start, end, text}
   partials.jsonl     1 line per chunk result, APPENDED as each MAP step completes
   (final MOM is returned in-memory to the form — no need to keep on disk)
```

- **`jsonl` (append-only)** is ideal for the MAP step: each chunk is one independent
  line, so memory stays flat and a half-written line never corrupts the rest.
- The REDUCE step **streams** `partials.jsonl` line by line — never loads them all.
- `data/` is already gitignored, so nothing leaks.

### Cleanup — belt and suspenders (no junk, ever)

1. **`defer os.RemoveAll(jobDir)`** in the runner → removed on success, error,
   panic *and* timeout.
2. **Boot-time sweep** → on startup, delete any leftover `data/mom-jobs/*` (covers
   the rare case where the process was killed mid-job before the defer ran).

Together these guarantee orphan files never accumulate, even after a crash.

---

## 4. Where the pieces live

- **qa-ai** (the AI worker, where transcription + the LLM run) owns the working
  files and the map-reduce loop. Split `/mom` into stages:
  - `POST /transcribe` — audio → transcript text
  - `POST /mom/extract` — one chunk → partial MOM JSON
  - `POST /mom/consolidate` — partials → final MOM JSON
- **qa-core** drives the stages as queue steps (mirrors `orchestrator.GenRunner`),
  so the user sees per-chunk progress over SSE. It owns the queue + temp upload
  lifecycle; qa-ai stays stateless per request.

(Alternative: keep the whole loop *inside* qa-ai's `/mom` for simplicity, at the
cost of opaque progress. Decide when we build it.)

---

## 5. Later refinements

- **Hierarchical reduce** for very long meetings (combine partials in groups).
- **Speaker diarization** → "who owns which action item" (whisper.cpp doesn't do
  this natively; add a diarization pass later).
- **Streaming** — start MAP on early chunks while later audio is still transcribing.

---

## Open items (separate from scaling)

1. **Language** — Indonesian audio currently yields English minutes. Fix: capture
   whisper's detected language and instruct the model to write in it explicitly
   (don't rely on the small model inferring "same language").
2. **Stronger Follow-Up / action-item extraction.**
3. **Deploy to the Mac** when it's back online (install whisper + the new binaries).
