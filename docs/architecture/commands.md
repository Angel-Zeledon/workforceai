# Slash commands (`/`) in the chat

Status: K1 (backend). The UI is built in parallel (K2) against this contract.

A `/` command system for the office channel, Chats > General and the 1:1 chat with an agent.
It behaves **identically with any LLM** (DeepSeek, OpenAI, Claude, Kimi): the commands are
executed by the backend (or by the frontend when they are pure navigation); the model only
receives text when a command needs it (`uses_llm: true`) and those calls use the same budget
reservation, usage ledger and Tool Gateway as every task.

## Principles

* **Server decides.** RBAC is enforced per command on the server (the same permissions as the
  REST endpoint the command wraps). The UI list only decorates.
* **Both languages always.** Every command answers to its Spanish and its English alias, whatever the
  UI locale (`/resumir` and `/compact` both work). The **response language follows the alias typed**
  (see "Language").
* **Nothing external is sent.** `/email` and `/linkedin` create a request whose agent proposes a
  draft; the send/post tool request goes through human approval like any other external action.
* **Destructive commands confirm.** `status: "needs_confirmation"` first, then the same call with
  `confirm: true`.
* **Audited, rate limited.** Every run (also denied and failed) writes an audit entry
  `command.run` / `command.denied` with the command id, the argument **keys** (never values), the
  surface and the outcome. Never the text of the chat, never secrets.
* **Prompt injection:** the chat text after the command is data. A command never lets the model
  choose another command: only the human typing it can run one.

## Endpoints

All under `/api/v1`, authenticated like the rest of the API.

### `GET /commands`

Catalog flagged by the caller's role:

```json
[{
  "id": "compact",
  "aliases": {"es": ["resumir", "compactar"], "en": ["compact", "summarize"]},
  "title": {"es": "Resumir la conversación", "en": "Compact the conversation"},
  "description": {"es": "...", "en": "..."},
  "example": {"es": "/resumir", "en": "/compact"},
  "group": "control",
  "icon": "minimize",
  "args": [{"name": "agent", "kind": "agent", "required": false, "variadic": false}],
  "permission": "conversations:write",
  "destructive": false,
  "uses_llm": true,
  "surfaces": ["office", "general", "agent"],
  "client_only": false,
  "allowed": true,
  "available": true
}]
```

* `group`: `control` | `office` | `org`.
* `args[].kind`: `agent` | `project` | `file` | `skill` | `template` | `text` | `enum` | `number`;
  `enum?: []` lists the values; `variadic` = takes the rest of the line (several agents, free text).
* `allowed:false` = the caller's role lacks `permission` (the UI shows it disabled); `available:false` =
  the feature behind it is not configured on this server (e.g. no drive). Both flags are additive.
* `client_only: true` = pure navigation the frontend may resolve without calling the server
  (`project`, `shop`, `inventory`). The server also implements them (returns a `navigate` result).

### `GET /commands/complete?kind=&q=&limit=`

`kind` in `agent|project|file|skill|template`. Returns `[{"value","label","hint"}]` (`value` is what
goes into `args`; for `agent` it is the agent id). Default limit 8, max 25. Filtered by RBAC (no files
for a role without `files:read`). `enum` values come from `args[].enum` (client side).

### `GET /commands/suggest?surface=&conversation_id=`

Context suggestions, at most 3:

```json
[{"command":"compact","reason":{"es":"La conversación es larga","en":"This conversation is long"},"severity":"info"}]
```

Rules: `compact` when the conversation has 40+ messages since the last clear/compact; `usage` when the
budget used is at least 80%; `pending` when approvals are waiting.

### `POST /commands/run`

```json
{"command":"compact","args":{},"raw":"/resumir","lang":"es",
 "context":{"surface":"agent","agent_id":"sales","conversation_id":"agent:sales","project_id":null},
 "confirm":false,"idempotency_key":"k-1"}
```

* `command` is the command id **or any alias** (with or without the leading `/`). When empty it is read
  from `raw`. When `args` is empty and `raw` has text after the command, the server parses the
  arguments positionally (see "Parsing").
* `lang` is `"es"|"en"`, derived by the UI from the alias the person typed. When absent: the alias
  typed (if unambiguous: `/pdf`, `/excel` and `/word` are the same in both languages), else detected from
  `raw`, else the organization language, else `es`.
* `context.surface`: `office` | `general` | `agent`. `conversation_id` is `office` for both
  `office` and `general` (one office stream) and `agent:<id>` for 1:1 chats; when missing it is derived.
* `idempotency_key`: the same key (per organization and person, 10 minutes) replays the first answer
  without running the command again.

Response:

```json
{"status":"ok","lang":"es","command":"compact",
 "result":{"kind":"message","title":"Conversación resumida","text":"...","data":{"tokens_before":1800,"tokens_after":240}}}
```

| `status` | meaning |
|---|---|
| `ok` | done, `result` has the outcome |
| `needs_confirmation` | destructive; `confirm.message` is `{es,en}`; repeat with `confirm:true` |
| `started` | work was queued (a request/meeting/draft): `result.kind="request"` with `request_id` |
| `error` | `error: {code, message}`; `message` is in the response language |

`result.kind`:

* `message`: `title?`, `text`, `data?`
* `table`: `title?`, `columns: string[]`, `rows: string[][]`, `text?`
* `navigate`: `href` (an app route, see below), `title?`/`text?`
* `download`: `url` (an API path, authenticated GET), `filename`
* `card`: `title`, `text`, `data` (key/values to render)
* `request`: `request_id`, `conversation_id?`, `text`

HTTP status: `200` for `ok|needs_confirmation|started`; errors: `400 invalid_args|missing_arg|unknown_agent|unknown_project|surface_not_supported`,
`403 forbidden`, `404 unknown_command|not_found`, `409 conflict|read_only|budget_exceeded`, `422 nothing_to_do`, `429 rate_limited`,
`501 not_available` (the service behind it is not wired). The body is always the same envelope.

Navigation hrefs used: `/office?agent=<id>`, `/chats`, `/chats/<agentId>`, `/projects/new?template=<id>`,
`/projects?project=<id>`, `/files?path=<path>`, `/files?open=<fileId>`, `/office?agent=<id>&trace=<traceId>`,
`/shop`, `/shop?tab=inventory`, `/skills`, `/approvals`. The UI maps them to its router.

## Parsing (`raw`)

`/command arg1 arg2 ...`: arguments are assigned in the order of `args`; the last `variadic` argument
takes the rest of the line. `@name` is accepted wherever an `agent` is expected. Agent references match
the id, the name or the role, ignoring case and accents, and also the words people use for the area (`ventas`, `contabilidad`, `rrhh`, `legal`, `finance`...). Special syntax:

* `/meeting sales legal accounting: <topic>` (`/reunion ventas legal contabilidad: <tema>`): agents before the colon, topic after.
* `/memory forget <text>` / `/memoria olvidar <texto>`.
* `/assign @sales prepare the quote` / `/encargar @ventas prepara la cotización`.

## Catalog

Aliases are unique across the two languages (a unit test enforces it). The registry in
`backend/internal/commands/registry.go` is the source of truth; the table is a summary.

| id | es | en | group | perm | LLM | notes |
|---|---|---|---|---|---|---|
| clear | limpiar, nuevo | clear, new | control | conversations:write | no | new epoch: the model stops seeing older messages; they stay in the history; a notice is posted |
| compact | resumir, compactar | compact, summarize | control | conversations:write | yes | summary stored as the conversation context; later turns send only it + newer messages; tokens before/after |
| context | contexto | context | control | agents:read | no | what the agent receives: segments with approximate tokens and fill % (runtime builder, no LLM) |
| usage | uso, costo, gasto | usage, cost | control | metrics:read | no | today/month, per agent, per project, budget left, coins |
| model | modelo | model | control | metrics:read (set: org:manage) | no | show/change provider order; BYO seam |
| memory | memoria | memory | control | memories:read (write: memories:write) | no | `ver`, `borrar`, `olvidar X`; long-memory seam |
| pause | pausar | pause | control | controls:pause | no | agent or project |
| resume | reanudar | resume | control | controls:pause | no | agent or project |
| stop | detener, parar | stop | control | controls:pause | no | destructive; agent: immediate drain; project: cancel |
| status | estado | status | control | agents:read | no | agents, running tasks, pending approvals |
| trace | traza | trace | control | audit:read | no | navigate to the last trace of the agent |
| export | exportar | export | control | conversations:read | no | `md` or `pdf` of the whole conversation, saved in the drive under `/Shared/Exports/` (needs `files:write`); result kind `download` |
| excel, word, pdf, presentation | excel, word, pdf, presentacion | excel, word, pdf, presentation | office | requests:create | yes | request with `[[format:..]]` marker |
| meeting | reunion | meeting | office | requests:create | yes | request with `[[meeting:..]]`: one task per agent + conclusion by the assistant |
| assign | encargar | assign | office | requests:create | yes | direct task to one agent |
| project | proyecto | project | office | requests:create | no | navigate to the wizard (optional template) |
| daily-summary | resumen-del-dia | daily-summary | office | reports:read | yes | summary of today (LLM, with local fallback) |
| pending | pendientes | pending | office | approvals:read | no | approvals + open tasks |
| search | buscar | search | office | conversations:read | no | files, chats, reports, memory |
| file, open | archivo, abrir | file, open | office | files:read | no | navigate to the file |
| email, linkedin | correo, linkedin | email, linkedin | office | requests:create | yes | draft + approval, never sends |
| remember | recordar | remember | office | memories:write | no | stores a fact in the agent memory |
| tone, language | tono, idioma | tone, language | office | agents:write (org: org:manage) | no | profile quick settings |
| save-template | plantilla | template | office | requests:create | no | last request or project into a template |
| skill, learn | skill, aprender | skill, learn | office | skills:read / skills:write | no | assign a skill / save the last task as a skill draft |
| undo | deshacer | undo | office | files:write | no | destructive; restores the previous drive version of the last change |
| team | equipo | team | org | agents:read | no | |
| hire | contratar | hire | org | agents:write | no | from a role template |
| permissions | permisos | permissions | org | approvals:read | no | who approves what |
| shop, inventory | tienda, inventario | shop, inventory | org | shop:read | no | navigate |

## Behavior notes

* `/clear` and `/compact` act on the conversation of the context (`office` for the office and General, `agent:<id>` for a 1:1 chat). Neither deletes messages: the chat history keeps everything and shows a system notice where the epoch changed; only what the model receives changes.
* `/compact` summarizes only what is newer than the last clear/summary and keeps the previous summary as input, so it can be repeated. The summary is sent as the first history item (`from: "system"`).
* `/assign` creates the request with a one-task plan for that agent (no planner call), `/meeting` and the deliverables go through the normal planner with markers.
* `/skill` assigns an active library skill to the agent (it then appears in the agent's skill list for every next task; `skill.load` still brings the text). Loading a skill for a single task only is not part of K1.
* `/learn` saves the last finished task of the agent as a skill **draft** (source `from_work`); an administrator reviews it before agents can use it.
* `/undo` restores the previous version of the file in the agent's (or your) latest drive change as a new version; history is kept.
* `/trace` needs `audit:read` (thought audit is admin only).
* `/presentation` is a PDF with one section per slide (no `.pptx`).
* Seams: `Deps.Commands.Models` and `Deps.Commands.LongMemory` (see below); the long-term memory of branch m1 and the BYO models of g3 plug in there without touching the commands.

## Language

The alias fixes the language of: every text in `result`/`error`, the instructions given to the
LLM by `compact`, `meeting`, `daily-summary`, `email`, `linkedin`, `assign` and the deliverable
commands (`lang` is forwarded as the locale of the runtime call and as a `[[lang:xx]]` marker in
requests), the system notice posted by `clear`/`compact`, and the exported document. Argument values
(agent names, topic) are never translated. Titles/descriptions in `GET /commands` always carry both languages.

## Runtime contract (agent-runtime)

* `POST /v1/summarize` `{mode: "compact"|"meeting"|"daily", messages:[{from,text}], previous_summary?, topic?, participants?, locale, tone}` ->
  `{text, usage}`. Works with providers **without structured output** (DeepSeek/Kimi): the JSON schema goes in the
  instructions (same mechanism as `json_instructions`), one retry on invalid JSON.
* `POST /v1/context-sizes` (body = a run-task request) -> `{segments:[{key, label, tokens}], total_tokens}` built with the
  same prompt builders as `run-task`, without calling the LLM.
* Markers in request text (honored by the live prompts and by the simulation, deterministically):
  `[[format:xlsx|docx|pdf]]`, `[[meeting:<agent ids>]]`, `[[lang:es|en]]`, `[[draft:email|linkedin]]`.
  `[[debate]]` / `[[debate:<agent ids>]]` (`/debatir`, B5) is read by the backend, which has the team debate the plan's
  decision before it runs (docs/architecture/team-debates.md); the runtime only strips it.

## Storage

Migration `600_conversation_context`: `conversation_contexts(org_id, conversation_id, epoch, cleared_at, summary, summary_upto, summary_tokens, updated_at)`
with RLS. The orchestrator reads it when it builds the history of a chat turn.

## Seams (other branches)

* `Deps.Commands.Models` (BYO models, branch g3): when nil `/model` can only choose among the built-in providers and a custom name answers `not_available`.
* `Deps.Commands.LongMemory` (branch m1): when nil `/memory` and `/remember` use the existing agent memory store.
