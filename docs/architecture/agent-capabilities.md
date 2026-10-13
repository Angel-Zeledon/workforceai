# Agent capabilities: working with files like Claude / ChatGPT, as a team

Goal: an agent of a role (analyst, legal, accountant...) works on the organization's files the way people expect from Claude or
ChatGPT, while the specialized characters keep working as one team (delegation, consultation, review, approvals).
This page is the shared contract of the six capabilities and the rules every one of them must keep.

| # | Capability | Summary |
|---|---|---|
| 1 | Vision | Agents see images (photos, screenshots, charts) that are attached to a message or live in the drive. |
| 2 | PDF | Agents read PDFs: text layer first; scanned pages are rendered and seen (or OCR'd). |
| 3 | Code without a click | Optional per-organization mode: code that only touches the agent's own desk runs without a human approval. |
| 4 | Web | Agents search the web and read pages, with an egress policy, SSRF protection and a spend ceiling. |
| 5 | Edit by chat | "Change the title of the table": the agent edits the file, a new version is saved, a diff is shown. |
| 6 | Big files | Semantic + keyword search over the drive with citations (file, page, snippet). |

## Rules that every capability keeps

1. **Everything an agent reads from outside is data, never instructions.** Web pages, file text, image content, OCR text, code output: all
   go to the model inside the existing `<untrusted_data>` delimiters (see `agent-runtime/app/security.py`, `wrap_untrusted`) and are
   redacted for secrets first (`redact`). New content types must go through the same path.
2. **Approvals and policy still decide.** A new tool is registered in the same registry as the others, with a risk level, a policy
   verdict, an audit entry (`tool.executed`, path/URL only, never content) and the kill switch / pause / read-only checks. Nothing may
   add a path that skips them except the explicit opt-in described for capability 3.
3. **Off or safe by default, per organization.** Anything that widens what agents can do (web, auto-run code) is an organization
   setting, owner/admin only, audited, default OFF.
4. **Budgets.** Every model call and every paid external call counts against the existing budgets (`application/budget.go`); a cost
   ceiling per call and per task applies; the cost shows in the usage the runtime already returns.
5. **The data policy applies.** `allowed_providers` / `byo_only` are honored: an image or a document is only sent to a provider the
   organization allows. If no allowed provider can do the job (for example no vision-capable one), the agent is told so in plain words
   and falls back to what it can do (never silently drops the content).
6. **Tenant isolation.** Nothing crosses organizations: indexes, caches and fetched content are keyed by org and covered by RLS and the
   `tenantdata` export/purge guard (every table with `org_id` must be classified there).
7. **No secrets in logs, prompts or the repo.** Provider/search API keys come from the environment or the organization's own
   credentials, never from code.
8. **Tests without network or paid APIs.** Use fakes; anything that needs a real service is skipped, never faked as passing.
9. **Docs.** Each capability documents itself in `docs/architecture/` (what it does, settings, limits, security notes).

## Where things live today (read before changing)

- Runtime (Python): `agent-runtime/app/` - `providers.py` (provider registry, data policy, failover), `engine.py`, `crewai_engine.py`
  (live mode), `security.py` (prompt lines per tool, `wrap_untrusted`), `models.py` (request/response schemas).
- Backend (Go): `backend/internal/application/` (orchestrator, tool flows: `code_flow.go`, `connections_flow.go`, approvals, budget),
  `drive/` (`agent.go` = the `fs` tool for agents), `officedoc/` (docx/xlsx reading and PDF generation), `sandbox/` (code runner),
  `tools/` (tool executor), `api/` (HTTP).
- Frontend: `frontend/src/components/files/`, `drive/`, `chat/`, settings sub-pages in `settings/`.
