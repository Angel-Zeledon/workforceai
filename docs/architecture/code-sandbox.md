# Code sandbox (F3): agents run code only with a human's consent

Status: implemented. Tool `code`, action `run`. Off by default.

## The rule

An agent can **ask** to run Python. It never runs it. Every run becomes a
high-risk approval that shows the **full code**, the input files (name, size),
the purpose, the expected outputs and the limits. Only after an owner or admin
approves does the backend send the run to the sandbox runner, which executes it
in a disposable container **without network**. There is no path from a tool
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
`{enabled, max_runs_per_day, max_runtime_seconds}`; disabled by default.

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
  approval, no network, no secrets and the limits reduce the blast radius; they
  do not remove the need for the stronger runtime in production.

## Tests

- `sandbox/service_test.go`, `application/code_flow_test.go`: approval always
  required, policy cannot skip it, tool grant and setting, kill switch and
  read-only before and after approval, rejection, hash binding.
- `infrastructure/postgres/code_runs_pg_test.go` (`TEST_DATABASE_URL`):
  lifecycle, `BeginRun` exactly once under 16 concurrent callers, RLS.
- `sandbox/drivefiles_test.go`, `cmd/server/sandbox_test.go`, `api/code_api_test.go`.
- With Docker: `docker build -t wfai-sandbox-python:1 sandbox/` then the
  runnerd integration tests and `TestCodeSandboxEndToEndWithRealDocker`
  (skipped, never faked, when Docker or the image is missing).
