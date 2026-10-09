## W3 context handoff and synthesis

Goal: agents stay productive on projects with dozens or hundreds of tasks. Three
mechanisms, all deterministic and additive (old payloads validate, small requests
behave exactly as before).

### 1. Bounded dependency context

`buildRunRequest` no longer sends every direct dependency in full
(`backend/internal/application/context_budget.go`, `fitOutputs`).

- Budget: `DEP_CONTEXT_TOKEN_BUDGET` (default 8000 estimated tokens, 4 bytes per token;
  same ceiling as `projects/calc.go`: `min(8000, 800*deps)`).
- Everything fits: sent untouched (same output as before) plus `ref` (`task:<id>`) and `title`.
- Over budget: every dependency is reduced to its own `summary` (fallback: first finding,
  recommendation, evidence or hypothesis, truncated on a rune boundary; no LLM call), capped
  per item at 80 tokens and shrinking with fan-in (min 12). Then the most relevant
  dependencies are restored in full while the budget allows: same agent as the consumer
  first, then higher confidence, then plan order. Reduced items carry `truncated: true`.
- If even the summaries do not fit (hundreds of deps), the least relevant are dropped and
  counted in `context.dependency_omitted`.
- The runtime (`security.py`) labels truncated items (`ref ...`, "summary only") and adds a
  note for omitted ones; it also has a safety-net cap (`fit_texts`, same env var) that is a
  no-op when the backend already fitted the budget.

### 2. Hierarchical synthesis

`finish()` calls `synthesize()` (`synthesis.go`).

- Threshold: `SYNTH_TOKEN_BUDGET` (default 12000 estimated tokens of outputs). At or below
  it: ONE `Synthesize` call, identical request and result as before (the $50,000 scenario).
- Above it: outputs are grouped (consecutive tasks of the same `workflow_id`, packed within
  the budget; oversized runs are split into consecutive chunks), at most `SYNTH_MAX_GROUPS`
  (default 6; the smallest adjacent pair is merged until it holds). Each group is
  synthesized (`stage: "group"`, `part`, `parts`), then one `stage: "final"` call runs over
  the group digests (summary plus bounded section digests). Total calls <= groups + 1 = 7
  by default. Every call input is itself fitted to the budget.
- Each call goes through `reserveOrPause` and `recordUsage`: request/agent/org caps,
  pause-on-cap, ledger, audit and `request.cost_usd` include all of them. Reservation per
  call uses the single-synthesis estimate, so a hierarchical run can cost more than the
  pre-run estimate of `/estimate` (which still models one synthesis; not updated here).
- Group calls run sequentially (simple, deterministic, within the per-org slot queue).

### 3. Project context (read-only, bounded)

For tasks of a project request (a request recognised by the installed `RequestOwner`, i.e.
projects), `RunContext.project_context` carries:

- `index`: finished tasks of the same request that are NOT direct dependencies (title,
  agent, one-line summary <= 160 chars, `ref`), newest first, within
  `PROJECT_CONTEXT_TOKEN_BUDGET` (default 1500); `index_omitted` counts the rest.
- `artifacts`: the text of artifacts the task description explicitly references as
  `artifact:<id>` (max 3, 3000 tokens in total). Fetched by the BACKEND
  (`artifacts.Service.ContextArtifacts`, wired with `Orchestrator.SetContextSource`),
  only if the agent has access to the artifact or it belongs to the same request.
- Ordinary (non-project) requests get no `project_context` (field omitted).
- The runtime wraps the index and each artifact with `wrap_untrusted` (delimited as
  untrusted data, secrets redacted, forged delimiters neutralized). It never fetches anything.

### Contract changes (all additive)

- Go to runtime: `dependency_outputs[].ref|title|truncated`, `context.dependency_omitted`,
  `context.project_context`, `synthesize.stage|part|parts` (all `omitempty`).
- Runtime models accept old payloads (defaults); unknown `stage` falls back to single pass.
- Env: `DEP_CONTEXT_TOKEN_BUDGET`, `PROJECT_CONTEXT_TOKEN_BUDGET`, `SYNTH_TOKEN_BUDGET`,
  `SYNTH_MAX_GROUPS` (backend); `DEP_CONTEXT_TOKEN_BUDGET`, `SYNTH_PROMPT_TOKEN_BUDGET`
  (runtime safety net, default 24000).

### Not done / limits

- Token counts are estimates (bytes/4), not a tokenizer.
- The summary of a task is whatever the agent wrote; no extra LLM summarization was added.
- Grouping by project phase relies on `workflow_id` when present; the orchestrator does not
  know project phases, so project tasks without it fall back to chunks.
- The pre-run cost estimate does not yet price the extra synthesis calls.
- Not verified against a live provider (simulation and unit tests only).
