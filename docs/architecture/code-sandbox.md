# Code sandbox (F3): agents run code only with a human's consent

Status: implemented. Tool `code`, action `run`. Off by default.

## The rule

An agent can **ask** to run Python. It never runs it by itself. Every run
becomes a high-risk approval that shows the **full code**, the input files
(name, size), the purpose, the expected outputs and the limits. Only after an
owner or admin approves does the backend send the run to the sandbox runner,
which executes it in a disposable container **without network**.

The invariant: **approval is required unless the organization opted into desk
mode, and then only for runs confined to the agent's own files** (see "Desk
mode" below). With desk mode off (the default) there is no path from a tool
request to an execution that skips the approval: no policy verdict, autonomy
level, standing rule or organization rule can (see `application/code_flow.go`).
Agents never approve.

## Flow

1. Runtime: the agent (with the `code` tool, only `data_analyst` and `analyst`
   by default) emits `{tool:"code", action:"run", args:{language:"python", code, input_files:[ids or /paths], purpose, expected_outputs:[names]}}`.
2. Orchestrator (`handleCodeRun`): denies unless a runner is installed
   (`sandbox_unavailable`), the agent holds `code` (`code_tool_not_granted`),
   the kill switch / pause / read-only mode allow it, and the org enabled it
   (`code_execution_disabled`) and is under its daily limit (`code_daily_limit`).
   Then `sandbox.Service.Prepare` stores a run bound to a hash of
   code + language + the **SHA-256 of every input** + expected outputs, and an
   approval (`action: code.run`) is created.
3. The human reviews the card (UI: full code with line numbers, inputs, limits,
   warning) and approves or rejects.
4. `Execute` re-checks everything (setting, kill switch, read-only, hash, the
   inputs' content), claims the run **at most once** (compare-and-set in the
   store, `BeginRun`) and sends it to the runner. While it runs, the service
   polls the guard: kill switch, pause or read-only mode abort it (the runner
   kills the container).
5. The result (stdout/stderr as redacted text, exit status, produced files) is
   stored, audited (`code.run.requested`, `tool.executed`) and handed to the
   agent as delimited `<untrusted_data>`. Produced files are written to the
   agent's desk, **as the agent**: `/Agents/<agent>/outputs/<run id>/<name>`
   (versioned; `file.activity` is emitted, path only).

A modified code, a changed input or a new request is a **new approval**.

## Desk mode (opt-in, per organization): code without a click

Setting `auto_run`: `"off"` (default, everything above applies unchanged) or
`"desk"`. Owner/admin only (`org:manage`), audited (`code.settings.updated` with
`auto_run`, `previous_auto_run`, `risk_acknowledged`), and **turning it on needs
`acknowledge_risk: true` in the PUT** (the server answers 400 without it; the
acknowledgement is never stored). A PUT that does not mention `auto_run` sets it
to `off` (the safe direction). It only has effect while code execution is enabled.

In desk mode a `code.run` starts **without an approval only if every condition
holds**. They are computed by the backend (`application/code_auto.go` builds the
scope from the agent id, the task, the drive and the conversation; the runtime
sends nothing the backend trusts) and checked by `sandbox.autoCheck` at Prepare
**and again at Execute** against the inputs just read:

1. The language is Python (as always) and the code is within the desk caps
   (default 16 KB, configurable up to 32 KB; approval mode allows 64 KB).
2. Every input file, resolved to its canonical drive path/id, is inside the
   requesting agent's **own desk** (`/Agents/<agent>/`), inside the **project
   folder** of the task (`/Projects/...`), or is a **file the user attached in
   this conversation** (matched by id or path against the user's messages of the
   chat). Other agents' desks, other projects and any `/Shared` file that is not
   an attachment of this chat do not qualify. An input that cannot be located or
   a file source that cannot locate files never qualifies (fail closed).
3. Input files total within the desk cap (default 5 MB, up to 10 MB; approval
   mode 25 MB).
4. At most N desk-mode runs of the same task have started (default 5, up to 20;
   `auto_max_runs_per_task`).
5. The agent's autonomy is not `suggest` / `approve_each` and the policy verdict
   asks for nothing beyond the baseline (no required role, no double approval, no
   no-self-approval, no deny). A `deny` rule still denies. Note: an org rule that
   only says "always approve" `code.run` is not visible to this check and does not
   stop desk mode; turning desk mode on is the explicit opposite decision.
6. Unchanged for every run: code execution enabled, kill switch, agent pause,
   per-tool kill switch and read-only mode allow it (otherwise the request is
   **denied**, not turned into an approval, because the approval would be
   denied at execution anyway), the organization's daily run limit is not
   exhausted (also a denial), no network and no secrets in the container, and
   outputs are written **only** to `/Agents/<agent>/outputs/<run id>/`.

Anything else (an input from elsewhere, a bigger run, the per-task limit, a
policy that wants a person) falls back to the **normal approval card**, which
now says why ("Approval needed because ..." from the stable `approval_reason`
code in the card and `auto_fallback` in the run). Reading a file outside the
desk, or writing outside it, still needs the same approvals it needs today.

How it runs: `Prepare` records the run exactly as before, bound to the same hash
of code + language + SHA-256 of every input + expected outputs, under an
approval id that no approval has (nobody can approve or replay it). The
orchestrator audits `tool.auto_approved`, claims the execution in the same
at-most-once ledger and calls `Execute` with approver `auto:desk`. A modified
code or a changed input is a new run (new hash) subject to the same checks;
a file that changed or left the desk between Prepare and Execute is denied
(`inputs_changed` / `auto_run_not_allowed:...`).

Audit: the same entries as an approved run (`code.run.requested`,
`code.run.started`, `code.run.finished`, `tool.executed`) plus `auto_run: true`
and the full `code_hash`/`args_hash`. The code, stdout and stderr stay stored; the
UI shows the code and result in the run history (Security Center, "Code
execution by agents") with the badge "Ran without approval (desk mode)".

The runtime is told the truth: the run request carries `code_auto_run: "desk"`
(additive; omitted when off) and `agent-runtime/app/security.py` `code_lines`
states the mode (desk: runs directly on its own files and the result arrives in
the same turn; otherwise: a person must approve). The prompt is information
only; the backend decides every run on its own. After the result is handed back,
further `code.run` requests of that turn are not executed (as before).

**What desk mode costs.** Nobody reads the code before it runs. What bounds the
damage is the sandbox (no network, no secrets, read-only root, limits), the
confinement of inputs and outputs to the agent's files, the caps and the audit.
The residual risk is the one below, now without a human in front of it: a bug in
the isolation (a container/kernel escape) can be triggered by code that an
agent wrote, possibly steered by prompt injection from a file the user attached
or the agent's desk. Use gVisor/rootless/dedicated Docker (below) before enabling
it for anything sensitive. Prompt injection can also make an agent compute
something misleading from its own files; the output still only lands on its desk.

## What the container gets

`--network none`, read-only root, `/work` tmpfs (noexec), non-root user,
`--cap-drop ALL`, `no-new-privileges`, CPU / memory / pids limits, a hard
timeout, output caps (stdout/stderr, per file, total, file count) and an
extension allow-list for produced files (csv, tsv, xlsx, png, jpg, pdf, txt,
json, md; never html/svg/js). The runner's environment is **never** forwarded:
no secrets reach the code. The image (`sandbox/Dockerfile`) is pinned: base by
digest, pandas / numpy / openpyxl / matplotlib by exact version. Inputs are
copies of the approved bytes; the code cannot reach the drive or the backend.

Verified against real Docker (`runnerd/integration_test.go`, 12 cases, and the
end-to-end `application/code_docker_e2e_test.go`: drive CSV in, approval with
the exact code, pandas in a container, CSV + PNG back on the agent's desk, new
approval for modified code, kill switch aborting a running run).

## Configuration

| Variable | Where | Meaning |
|---|---|---|
| `SANDBOX_URL`, `SANDBOX_TOKEN` | backend | both set: the sandbox is available. Unset: `code.run` is denied with `sandbox_unavailable` |
| `SANDBOX_TOKEN` | runner | shared bearer token (>= 16 chars; the runner refuses to start otherwise) |
| `SANDBOX_IMAGE`, `SANDBOX_RUNTIME` | runner | image (`wfai-sandbox-python:1`) and OCI runtime (`runsc` = gVisor) |
| `SANDBOX_MAX_RUNTIME_SECONDS`, `SANDBOX_MEMORY_MB`, `SANDBOX_PIDS`, `SANDBOX_MAX_OUTPUT_BYTES`, `SANDBOX_MAX_OUTPUT_FILE_BYTES` | backend and runner | platform ceilings; organizations can only lower them |
| `SANDBOX_MAX_RUNS_PER_DAY` | backend | platform ceiling of runs per org per UTC day (default 20) |
| `SANDBOX_MAX_CONCURRENT`, `SANDBOX_WORK_MB` | runner | concurrency and size of `/work` |

Per organization (owner / admin, audited): `GET/PUT /api/v1/settings/code-execution`
`{enabled, max_runs_per_day, max_runtime_seconds, auto_run, auto_max_code_bytes,
auto_max_input_bytes, auto_max_runs_per_task, acknowledge_risk}`; disabled and
`auto_run: "off"` by default (`acknowledge_risk` is request-only and required to
turn `auto_run` to `"desk"`).

Run it: `make up-sandbox` (builds `wfai-sandbox-python:1`, generates
`SANDBOX_TOKEN` into `.env.sandbox`, starts the stack with compose profile
`sandbox`). `make sandbox-image` builds only the image.

## WARNING: the Docker socket

The `sandbox-runner` service mounts `/var/run/docker.sock`. **Access to the
Docker socket is root-equivalent on the host**: anyone who can talk to the
runner can start any container. Therefore:

- Only `sandbox-runner` mounts the socket; the backend, the runtime and the
  frontend never do. The runner publishes **no port**, requires the bearer
  token, and only builds `docker run` arguments from a fixed template (the
  code goes through stdin, never through a shell or the command line).
- Do not expose it beyond the internal network. Treat `SANDBOX_TOKEN` like a
  root credential.
- The profile is off by default. For anything beyond a trusted single host use
  a **dedicated, rootless or remote Docker daemon** (point the runner at it with
  `DOCKER_HOST`) and set `SANDBOX_RUNTIME=runsc` (gVisor) for a user-space
  kernel between the code and the host.
- A kernel or runtime escape from a container is the residual risk: the human
  approval (when not in desk mode), no network, no secrets and the limits reduce
  the blast radius; they do not remove the need for the stronger runtime in
  production. In desk mode the approval is gone, so that runtime matters more.

## Tests

- `sandbox/service_test.go`, `application/code_flow_test.go`: approval always
  required, policy cannot skip it, tool grant and setting, kill switch and
  read-only before and after approval, rejection, hash binding.
- Desk mode: `sandbox/autorun_test.go` (off unchanged, auto-run on desk /
  project / attachment inputs, every fallback reason, per-task limit, kill
  switch / read-only / daily limit, re-check at Execute, binding hash, audit
  flags, acknowledgement, tenant isolation), `application/code_auto_test.go`
  (end to end through the orchestrator), `api/code_auto_api_test.go`,
  `agent-runtime/tests/test_f3_code.py` (prompt per mode),
  `infrastructure/postgres/code_runs_auto_pg_test.go` (`TEST_DATABASE_URL`).
- `infrastructure/postgres/code_runs_pg_test.go` (`TEST_DATABASE_URL`):
  lifecycle, `BeginRun` exactly once under 16 concurrent callers, RLS.
- `sandbox/drivefiles_test.go`, `cmd/server/sandbox_test.go`, `api/code_api_test.go`.
- With Docker: `docker build -t wfai-sandbox-python:1 sandbox/` then the
  runnerd integration tests and `TestCodeSandboxEndToEndWithRealDocker`
  (skipped, never faked, when Docker or the image is missing).
