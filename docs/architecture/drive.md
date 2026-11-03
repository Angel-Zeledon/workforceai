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
  `objects` service: MinIO no longer publishes free community images. The production composes
  run the same image (`objects` in `docker-compose.prod.yml`, `wf-objects` in
  `deploy/vps/docker-compose.yml`): private network only, no published port, persistent volume,
  credentials `S3_ACCESS_KEY` / `S3_SECRET_KEY` required in the `.env`. S3, R2 or any other
  S3-compatible service also work (set `S3_ENDPOINT` & co.).
- With S3 and a database the store is `objstore.Tiered`: new objects go to S3, a key S3 does not
  have is read from Postgres (`drive_objects`). Switching an existing install to S3 therefore
  needs no downtime; `server ctl objects-to-s3` then moves the old bytes (below).
- PDF blobs of artifacts also go to the object store when one is configured; old bytea
  blobs stay readable.

| Env | Default | |
|---|---|---|
| `S3_ENDPOINT`, `S3_BUCKET` | (unset: Postgres) | compose sets `http://objects:9000`, `wfai-drive` |
| `S3_ACCESS_KEY`, `S3_SECRET_KEY` | | backend only: never reach runtime, prompts, frontend or logs |
| `S3_REGION` | `us-east-1` | `auto` for R2 |
| `S3_USE_PATH_STYLE` | `true` | MinIO/RustFS/R2 |
| `S3_PREFIX` | | optional key prefix inside the bucket |
| `DRIVE_MAX_FILE_MB` | 25 | per version written from memory (agents, inboxes, link imports, edits) |
| `DRIVE_MAX_UPLOAD_MB` | 500 with S3; `DRIVE_MAX_FILE_MB` without (at most 64) | per file a person uploads (streamed, below) |
| `DRIVE_SPOOL_DIR` | OS temp dir | where uploads are spooled while they are checked |
| `DRIVE_QUOTA_MB` | 5120 (5 GB) | all versions of the organization |

## Uploads (streamed)

People's uploads never sit whole in the backend's memory (the VPS backend has 256 MB):

1. The handler wraps the body (`api/drive_upload.go`): an announced `Content-Length` over the
   cap is a 413 before anything is read; the read deadline moves forward while bytes keep
   arriving (2 min without progress cuts it), so the 30 s `READ_TIMEOUT` does not end big uploads.
2. `drive.WriteStream` / `NewVersionStream` (`drive/stream.go`) check path, type, read-only mode
   and quota first, then copy the body to a temporary file while hashing it (SHA-256), capped
   at `DRIVE_MAX_UPLOAD_MB`.
3. The temporary file is checked without loading it: magic numbers, OOXML zip directory, UTF-8
   and NUL bytes in 64 KB chunks. Text is extracted only from what `fs.read` would read anyway
   (first 8 MB of text, PDFs up to 20 MB, office files up to 8 MB); a bigger binary is stored
   with `extraction: none`.
4. The file is streamed to the object store with its length and digest (S3 verifies the
   signed digest); the temporary file is deleted in every case. Restoring an old version uses
   the same path. Downloads push the write deadline forward the same way.

Routes: `PUT /drive/upload?path=` and `PUT /drive/files/{id}/content` (raw body, as before) and
`POST /drive/upload?folder=` with `multipart/form-data` for several files: each `file` part is
streamed in turn; its filename may carry a relative path (`sub/a.txt`, folder uploads); the
answer is `{results: [{name, path, status, result | code, error}]}` (one bad file does not undo
the others; at most 100 files). `GET /drive/usage` returns `max_upload_bytes` for the client.
The frontend uses XMLHttpRequest for uploads (`session.authUpload`) to show progress: per file
in the chat composer, one bar for the batch in the Files screen.

Not done yet: resumable uploads (tus-style offsets or S3 multipart) and direct browser-to-S3
uploads with presigned URLs. The internal RustFS is deliberately not reachable from the
internet, so presigned URLs would need a proxied route (Caddy) or an external S3; the
hand-written client signs only headers (no presigned query strings) today.

## Moving existing files to S3

Installs that ran without S3 keep their bytes in Postgres (`drive_objects`, and the `data`
column of `artifact_blobs` for PDFs). After S3 is configured they stay readable (Tiered store);
to move them:

```sh
# VPS (deploy/vps): inside the running backend, which has DATABASE_URL and S3_*
docker compose exec wf-backend server ctl objects-to-s3 -dry-run   # read + verify, copy nothing
docker compose exec wf-backend server ctl objects-to-s3            # copy + verify (Postgres untouched)
docker compose exec wf-backend server ctl objects-to-s3 -delete    # copy if needed, verify, free Postgres
docker compose exec wf-postgres psql -U workforce -d workforce -c 'VACUUM (VERBOSE, ANALYZE) drive_objects'
# docker-compose.prod.yml: docker compose -f docker-compose.prod.yml --env-file .env.prod exec backend server ctl objects-to-s3 ...
```

- **Idempotent and resumable:** objects are walked in key order per organization; one already
  in S3 with the same SHA-256 is not copied again, so an interrupted run is just started again.
  `-org ID` limits it to one organization, `-batch N` sets the page size.
- **Verified before deleting:** the bytea is hashed and compared with its recorded SHA-256 and
  size; the copy is uploaded with a signed digest and then read back from S3 and hashed. Only
  then, and only with `-delete`, the `drive_objects` row is deleted (`... AND sha256 = <verified>`)
  or the PDF row switches to `object_key` with `data = NULL`. An object that does not verify is
  reported and left untouched; the command exits non-zero.
- It connects as the owner (`MIGRATE_URL`, else `DATABASE_URL`, no `SET ROLE`) because PDF rows
  are insert-only for `app_user`; every statement still runs in an organization-scoped
  transaction. Organizations come from memberships plus the default org (and every org with
  objects when the connection is a superuser, as in the composes).
- Plain `VACUUM` makes the freed space reusable; `VACUUM FULL drive_objects` returns it to the
  disk but locks the table while it runs.
- Run the database backup first (below).

## Backup

The database dump (`deploy/vps/backup.sh`, nightly) holds folders, files and versions but **not**
the bytes that live in S3: back up both.

- **VPS:** `backup.sh` now also mirrors the RustFS volume (`workforceai_wf-objdata`, override
  with `WF_OBJECTS_VOLUME`) into `/var/backups/workforceai/objects` with an incremental copy:
  objects are immutable (a new version is a new object), so each night only new files are
  copied. Copy `/var/backups/workforceai` off the server as well (the VPS disk is one failure).
- **Other installs:** any S3 mirror works (`rclone sync`, `mc mirror`) against the bucket, or
  `docker run --rm -v <project>_objdata:/data:ro -v $PWD:/b alpine tar czf /b/objects.tgz -C /data .`.
- **Restore:** stop the stack, restore the database dump and copy the mirror back into the
  volume (`docker run --rm -v <volume>:/data -v <mirror>:/b alpine cp -a /b/. /data/`), start.
  A version whose object is missing answers `content_missing` instead of failing the drive.

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

## Files in the conversation

- **Agents -> chat.** When a task of a request born in a chat finishes and the agent wrote or
  moved files (`fs.write/move`, notified through `file.activity` with the task id), the agent
  posts one message in that conversation: "I finished X. I left these files:" with
  `files: [{file_id, path, name, mime, size, version, action: "write"}]`. References only,
  never content; tasks that wrote nothing say nothing. Persisted in `messages.files`
  (migration 490) and sent in `chat.message.files`.
- **People -> chat.** The chat input uploads attachments to `/Shared/Chat/<batch>/<name>`
  (member+, versioned like any file) and sends `POST /messages {attachments: [paths]}`. The
  stored message keeps the user's text and gets `files` (`action: "attach"`); the agents see the
  paths in a delimited `[attachments]` block (data, not instructions) and read them with `fs.read`
  (`/Shared` is readable by every agent).
- **Viewer.** Clicking a card opens a side panel by type: Markdown (own safe renderer: no HTML,
  no remote images, links `noopener noreferrer nofollow`, only http(s)/mailto), CSV/XLSX as a
  sortable table (xlsx through `/text`), JSON tree, code/text with line numbers, PDF with the
  pdf.js viewer of PDF artifacts, images from our own API as blobs (`nosniff`). Other types:
  download. Version picker, line diff against the previous version, download, open in Files.
- **Iterating.** "Ask for changes" sends a chat message to the agent that wrote the last version
  (or the office) with the file attached: its next task reads it with `fs.read` and writes a new
  version. People can also edit text files in the panel (`PUT /drive/files/{id}/content` with
  `expected_version`; a 409 offers "save mine on top" or "discard mine"). "Comment" sends a
  whole-file comment as a chat message (no new backend).

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
