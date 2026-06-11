# Review & Feedback Log

Feedback gathered from testing the generated output and the current UI. Captured
here before reworking the PRD. Several items intentionally **override frozen
PRD v1.2 decisions** — flagged inline.

---

## 1. Feedback on generated output (`feedback hasil generate`)

### 1.1 Deeper requirement validation *before* generating
> "Untuk mencari kevalidan requirement/flow dari sebuah sistem, baiknya AI
> memvalidasi lebih detail sebelum melakukan generate test case."

The AI should **validate the requirement / flow more thoroughly first**, then
generate test cases. Implies a distinct **pre-generation validation step** (is the
requirement clear, complete, internally consistent?) before any test cases are
produced.

- *PRD status:* partially exists — we have `requirement_analysis` + ambiguity +
  `requirement_health`. Feedback wants this to be a **gating/first-class step**,
  not just fields in the final output.

### 1.2 Multimodal input — upload photo / document / diagram / video  ❌ DROPPED (text-only)
> Decision (2026-06-11): dropped for now. Qwen2.5 7B is text-only and a 16 GB Mac
> Mini can't host a vision model. The app serves text input only. Out of scope.

> "Jika bisa, tambahan fitur seperti upload foto/dokumen/diagram bergambar/video
> bisa dibuatkan sebagai akses untuk AI mendalami isi sistem."

Allow uploading **images / documents / diagrams / video** so the AI can
understand the system more deeply.

- ⚠️ **Model implication (important):** our locked model **Qwen2.5 7B is
  text-only**. Image/diagram/video understanding needs a **vision model**
  (e.g. Qwen2.5-VL) or a doc/OCR pre-processing pipeline — and video is a much
  larger problem. This is a significant scope + hardware change for the 16 GB
  Mac Mini. Needs a decision: vision model vs. text-extraction-only (PDF/docx →
  text), and which media types are realistic for v1.

### 1.3 Best-practice test case template (columns)
Requested canonical column set:

| # | Column |
|---|---|
| 1 | TC ID |
| 2 | AC ID |
| 3 | Module / Feature |
| 4 | Title / Scenario |
| 5 | Precondition |
| 6 | Test Steps |
| 7 | Test Data |
| 8 | Expected Result |
| 9 | Priority (P0, P1, P2, P3) |
| 10 | Severity (Critical, High, Medium, Low) |
| 11 | Type (Positive, Negative, Edge case, Trivial) |
| 12 | Actual Result |
| 13 | Notes |

- *PRD status vs frozen §5.3 QA CSV:* this is **leaner** than the frozen 18-col
  schema. Differences: feedback **drops** Requirement ID, Requirement Summary,
  Test Design Technique, Risk, Coverage Type, Traceability Links, Status; **keeps/
  renames** to Module/Feature, Title/Scenario; **changes** the `Type` vocabulary
  to Positive/Negative/Edge case/Trivial. Decide whether this replaces the frozen
  schema or is a second "manual QA" view.

### 1.4 Result layout — TABLE, not cards  ⭐ (main concern)
> "Buat tampilan table memanjang ke vertikal berdasarkan best practice template
> di atas." + "right now we are creating like card... but user need read like a
> table — it makes us easier to convert into spreadsheet or Excel."

**Current:** results render as **cards** (one block per test case).
**Wanted:** a real **table** — one **row per test case**, columns = the §1.3
template fields. The "memanjang ke vertikal" means rows extend downward (a long
table), not a wide sideways-scrolling one.

**Why it matters (the key reason):** a table is a **1:1 mapping to a spreadsheet /
Excel grid** — each row → a spreadsheet row, each column → a column. Users can
read it as a grid *and* copy/export it straight into Excel with no mental
re-flattening. Cards look nice but don't translate to a grid. For a tool whose
output *is* exportable test cases, the table should be the **primary view**.

- *Action:* rework `web/templates/result.html` (and `app.css`) from card layout
  to a vertical table keyed on the §1.3 columns. Keep it readable on screen
  (wrap long cells, multi-line steps) while staying grid-shaped.

### 1.5 Remove caps on acceptance criteria & ambiguities
- **Acceptance criteria can exceed 20** — don't cap the count. A real requirement
  may legitimately yield 20+ ACs; truncating hides coverage.
- **Ambiguities should surface more freely too** — list all that are found, not a
  small fixed number.
- *Action:* check the prompt and any output limits in `qa-ai` (`internal/prompt`,
  `internal/generate`) and the contract/rendering for hidden caps; relax them.
  Watch the 7B model's context/output length budget so longer outputs still fit.

---

## 2. Feedback on the front page / input form (`tampilan halaman depan`)

Requested form changes (➕ keep, ✏️ change, ❌ remove):

| Field | Decision | Detail |
|---|---|---|
| **Application type** | ✏️ reduce | **Web · Mobile · Desktop** only (was 8 options) |
| **Detail level** | ❌ remove | Always generate in full detail — "concern QA adalah detail oriented" |
| **Test type** | ✏️ change | **Positive · Negative · Edge case · Trivial** (new vocabulary) |
| **Test design technique** | ❌ remove | Drop EP/BVA/Decision Table/etc. selector |
| **Output format** | ✏️ reduce | **Step-by-step & Gherkin (BDD)** — only these 2 |
| **Priority** | ❌ remove | No selector — priority is always auto-mapped during generation |
| **Platform / device** | ➕ keep | Keep as-is |
| **Options / toggles** | ❌ remove | Drop the misc option toggles |

---

## 3. Net impact on frozen PRD v1.2 (to discuss before reworking)

These contradict the freeze and need explicit re-decision:

1. **Form simplification** — removing detail level, test design technique,
   priority selector, and option toggles; reducing app types to 3; changing test
   type vocabulary. (PRD §5.1)
2. **`Type` vocabulary** changes to Positive/Negative/Edge case/Trivial — affects
   prompt, contract, scoring coverage-type logic, and exports.
3. **Test case template / export** — leaner column set + new `Actual Result`
   column. Reconcile with frozen §5.3 QA CSV (replace vs. add a view).
4. **Table result layout** (not cards) as the primary view, for 1:1
   spreadsheet/Excel conversion — UI/template rework (`result.html` + `app.css`).
5. **No caps on AC / ambiguity counts** — relax prompt/output limits, mind the
   7B context budget. (PRD §5.2 / §7)
6. **Pre-generation requirement validation** as a gating step. (PRD §5.2)
7. **Multimodal input** — biggest one: needs a vision model or doc-extraction
   pipeline; Qwen2.5 7B (text-only) can't do it. Hardware + model decision. (PRD §4)

> Recommendation: treat 1–5 as a **PRD v1.3 form/output revision** (mostly
> additive/simplifying, achievable on current stack), and treat **6 (multimodal)**
> as a separate track with its own model/hardware decision.
