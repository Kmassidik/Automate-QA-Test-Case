# Architecture & Orchestration

How the orchestrator works as of **v1.3** (two-step validate → generate flow).
GitHub renders the Mermaid diagrams below inline; PNG/SVG copies live in
[`docs/img/`](./img).

- **`qa-core`** — the gatekeeper (`:8080`): access gate, the FIFO queue + single
  worker, ETA tracker, SSE broadcaster, exports, and the UI.
- **`qa-ai`** — stateless generator (`:8081`): builds the prompt, calls Ollama,
  validates the JSON (≤3 retries), and clamps the scores.
- **Ollama** — native on the host (`:11434`), serving `qwen2.5:7b`.

---

## 1. Architecture & data flow

![Architecture](./img/architecture.png)

```mermaid
flowchart TD
    subgraph Browser["🌐 Browser (htmx + SSE)"]
        UI["Form / validation page / 13-col table"]
        ES["EventSource /events"]
    end

    subgraph Core["qa-core (gatekeeper · :8080)"]
        MW["Access-code middleware"]
        H["HTTP handlers<br/>/validate · /generate · /result · /export · /model"]
        Q["FIFO queue<br/>buffered channel"]
        W["Single worker goroutine<br/>(one job at a time)"]
        ETA["ETA tracker<br/>rolling avg last 20"]
        MON["backend.Monitor<br/>polls qa-ai every 3s"]
        BC["SSE broadcaster<br/>(fan-out ticks)"]
        EXP["Exporters<br/>QA-CSV · Jira-CSV · Markdown"]
    end

    subgraph AI["qa-ai (stateless · :8081)"]
        PB["Prompt builder<br/>Build / BuildValidation"]
        VAL["JSON validate + retry ≤3"]
        CLAMP["Score clamp 0–100"]
        STATS["System stats (gopsutil)<br/>+ loaded-model info"]
    end

    OLL["Ollama (native · :11434)<br/>qwen2.5:7b"]

    UI -->|"POST /validate or /generate + access cookie"| MW --> H
    H -->|"Submit(kind, req)"| Q --> W
    W -->|"KindValidate → /validate<br/>KindGenerate → /generate"| PB
    PB --> OLL --> VAL --> CLAMP -->|"Result JSON"| W
    W --> ETA
    W -->|"state change"| BC
    MON -->|"GET /healthz"| STATS
    MON -->|"model + CPU/RAM"| BC
    BC -->|"tick: queue + backend + resources + your position"| ES
    H -->|"GET /result/{id}"| EXP
    EXP -.->|"download"| UI
```

---

## 2. The two-step flow (sequence)

![Two-step flow](./img/two-step-flow.png)

```mermaid
sequenceDiagram
    actor U as User
    participant C as qa-core
    participant Q as Queue + Worker
    participant A as qa-ai
    participant O as Ollama

    Note over U,O: STEP 1 — Validate (analysis only)
    U->>C: POST /validate (requirement)
    C->>Q: Submit(KindValidate)
    C-->>U: waiting.html (#position in line)
    Q->>A: POST /validate
    A->>O: BuildValidation prompt
    O-->>A: analysis JSON
    A->>A: validate + ClampHealth
    A-->>Q: health · breakdown · ambiguities
    Note over C: SSE tick → "done"
    U->>C: GET /result/{id}
    C-->>U: validation.html + "Generate →"

    Note over U,O: STEP 2 — Generate (full suite)
    U->>C: POST /generate (carried fields)
    C->>Q: Submit(KindGenerate)
    C-->>U: waiting.html
    Q->>A: POST /generate
    A->>O: Build prompt (full schema)
    O-->>A: full Result JSON
    A->>A: validate + Clamp (both scores)
    A-->>Q: ACs · test cases · coverage · scores
    Note over C: SSE tick → "done"
    U->>C: GET /result/{id}
    C-->>U: result.html (13-col table) + exports
```

---

## Key orchestration facts

- **One queue, two job kinds** (`KindValidate` / `KindGenerate`) — both serialize
  through the *same single worker*, so the one-at-a-time guarantee holds across
  the two-step flow. A validate and a generate never run concurrently.
- **qa-ai is stateless** — it doesn't track step 1 vs step 2. qa-core decides the
  job kind; qa-ai just picks `Build` (full schema) vs `BuildValidation` (analysis
  only).
- **Two separate LLM calls** per full run (validate, then generate). This makes
  each step independent — and splits the model's output budget, so the suite is
  leaner than the old single-shot generation.
- **SSE is independent of the work.** The broadcaster fans out queue position +
  backend model state + CPU/RAM to *every* connected browser on each state change,
  so live status needs no polling and no refresh.
- **Exports are pure transforms** over the canonical `Result` JSON in qa-core —
  the 13-col table view and the QA CSV come from the same `QARepositoryRows`.
