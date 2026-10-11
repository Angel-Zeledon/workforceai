# Drive: the office file system

Agents need a place to work beyond chat. The drive is the organization's file
system: folders, files of any allowed type, immutable versions, trash. People
use it through `/api/v1/drive/*`; agents use it only through the `fs` tool of the
Tool Gateway. Every change is versioned and reversible.

## Layout

| Root | Who | Notes |
|---|---|---|
| `/Agents/<agent id>` | the agent's desk | created on demand; the folder name is the agent id |
| `/Projects/<project>` | a launched project | created on demand; deliverables land here |
| `/Clients/<client>` | client material | agents read only with a grant |
| `/Shared` | the whole team | agents read; writes need approval |

## Storage

- Bytes live in an object store (`backend/internal/objstore`): S3-compatible when
  `S3_ENDPOINT` and `S3_BUCKET` are set, otherwise Postgres (`drive_objects`, bytea,
  migration 480), otherwise memory (no database).
- Postgres keeps folders, files and versions (migrations 480/481, RLS by organization).
- The S3 client is hand written (SigV4, Put/Get/Head/Delete/CreateBucket) to keep the
  component that holds the credentials free of large dependency trees. Its contract test
  runs against a real server: `S3_TEST_ENDPOINT=... go test -run TestS3AgainstMinIO ./internal/objstore/`.
- docker-compose runs **RustFS 1.0.1** (Apache-2.0, MinIO-compatible) as the internal
  `objects` service: MinIO no longer publishes free community images. Production: S3, R2 or
  any S3-compatible service.
- PDF blobs of artifacts also go to the object store when one is configured; old bytea
  blobs stay readable.

| Env | Default | |
|---|---|---|
| `S3_ENDPOINT`, `S3_BUCKET` | (unset: Postgres) | compose sets `http://objects:9000`, `wfai-drive` |
| `S3_ACCESS_KEY`, `S3_SECRET_KEY` | | backend only: never reach runtime, prompts, frontend or logs |
| `S3_REGION` | `us-east-1` | `auto` for R2 |
| `S3_USE_PATH_STYLE` | `true` | MinIO/RustFS/R2 |
| `S3_PREFIX` | | optional key prefix inside the bucket |
| `DRIVE_MAX_FILE_MB` | 25 | per version |
| `DRIVE_QUOTA_MB` | 5120 (5 GB) | all versions of the organization |

## Agents: the `fs` tool

Role templates carry `fs` (every action, under the zone policy) or `fs_read` (list,
read, search). Run requests of those agents include `workspace {desk, project, shared,
can_write}` and the prompt explains the tool. The backend executes; the runtime only asks.

| Action | Own desk / assigned project | /Shared, granted folders | Other desks, other projects, /Clients |
|---|---|---|---|
| list, read, search | yes | yes | refused (`not_granted`) unless granted |
| write | **no approval** | approval | approval |
| move | no approval if both ends are writable areas | approval | approval |
| delete | approval (high), soft delete | approval | approval |

- Reads (`fs.list/read/search`) go through the existing read round of the orchestrator:
  results come back sanitized and delimited as untrusted data, and the task is re-run with
  them (up to 2 rounds, 5 reads each). F2 replaces this with a bounded multi-step work loop.
- Reading a file another principal wrote marks the task as tainted: any later action with
  external effects needs human approval.
- Writes are attributed to the agent, versioned and reversible; approvals bind to the exact
  arguments and run at most once (execution ledger); kill switch, pause and read-only mode
  apply as to any tool.
- Project deliverables: when a project task finishes with an output, a Markdown file
  `/Projects/<project>/<node>-<title>.md` is written as the agent (replays add no version).

## People: the API

Permissions: `files:read` (viewer+), `files:write` (member+), `files:delete` and
`files:grant` (admin+). See `docs/architecture/08-api.md` for the routes. Downloads are
served with `nosniff`, `private, no-store` and a sandboxing CSP.

## Limits and guards

- Paths: no traversal, length and character limits; names cleaned; folder depth bounded.
- Types: extension/MIME allowlist; size per version; quota per organization (413).
- Text extraction for reading and search: text/markdown/csv/json/code as is; xlsx and docx
  through the same zip-bomb and size guards as the statement importer; PDFs are stored but
  their text is only extracted when simple (otherwise reported as "binary, not extracted").
- Optimistic locking: `expected_version` (409 `version_conflict` with `current_version`).
- Search: Postgres full text (Spanish stemming by default) over names and extracted text.
  The same `Retriever` interface will serve the agents' long-term memory.

## Threat model (summary)

- **Prompt injection in files**: content is always delimited data; a file cannot grant
  tools or approvals; reading others' files taints the task.
- **Exfiltration**: agents cannot send files anywhere by themselves; external actions need
  approval.
- **Cross-tenant access**: RLS on every table and object keys prefixed by organization.
- **Credential leaks**: S3 credentials only in the backend; known secrets are redacted from
  anything an agent reads.
- **Not protected**: a person with `files:write` can upload anything allowed by the type
  list; malware scanning is not done (files are never executed by the backend).
