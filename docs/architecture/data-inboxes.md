# Data inboxes

A **data inbox** is the generic way to bring data INTO the platform without integrating the software that has it.
It is a URL plus a secret token. Anything that can make an HTTP request (a shell script, an ERP export job, an
automation in n8n / Zapier / Make, a cron with `curl`) POSTs JSON, CSV or files to it, and every push becomes a
versioned file in the organization's drive.

v1 ships no vendor-specific integrations by default (`VENDOR_INTEGRATIONS`, `internal/integrations`); inboxes
(inbound) and API sources (`custom_api`, outbound read-only HTTP) are the two generic paths for data.

## Pieces

| Layer | Where |
|---|---|
| Use cases, parser, memory store | `backend/internal/inboxes/` (`service.go`, `parse.go`, `memstore.go`, `types.go`) |
| Postgres store | `backend/internal/infrastructure/postgres/inboxes.go`, migration `803_data_inboxes.sql` (+ `backend/migrations/down/803_data_inboxes_down.sql`) |
| HTTP | `backend/internal/api/inboxes.go` (admin under `/api/v1/data-inboxes`, public `POST /api/v1/inbox/{id}`) |
| Wiring | `backend/cmd/server/inboxes.go` (store, controls gate, customer folders, audit, realtime) |
| Drive principal | `drive.PrincipalInbox` (`kind: "inbox"`, `id` = inbox id) |
| UI | Connections > "Data inboxes" tab: `frontend/src/components/connections/DataInboxesTab.tsx`, `lib/dataInboxesApi.ts`, `lib/dataInboxLogic.ts`, mock `lib/mock/data-inboxes-mock.ts` |

## Flow

1. An owner/admin (`org:manage`) creates an inbox: name, destination folder, optional customer (B12), allowed
   content (`json`, `csv`, `files`), max size per push and pushes per minute.
2. The server answers ONCE with the token (`aiwi_` + 43 base64url characters = 256 random bits) and the push URL.
   Only the SHA-256 of the token and its last 4 characters (`token_hint`) are stored.
3. The sending system calls `POST /api/v1/inbox/{id}` with `Authorization: Bearer <token>`.
4. The body is parsed (raw JSON / CSV / allowed file type, or `multipart/form-data` with up to 20 file parts),
   checked against the allowed kinds and the size cap, and written to the destination folder as the inbox
   principal. A file name the same inbox wrote before gets a new version; a name a person or agent wrote is never
   overwritten (the push gets a unique suffix).
5. The push is recorded (metadata only), audited (`inbox.push_received`) and announced in realtime
   (`inbox.data_received` `{inbox_id, inbox_name, push_id, customer_id, size, files[{file_id, path, name, size, mime, version}], received_at}`).

Destinations: without a customer, an absolute folder under `/Shared` or an existing `/Projects/<project>`
(default `/Shared/Imports/<slug>`). With a customer, a folder relative to `/Clients/<customer>` (default
`Imports/<slug>`), resolved at every push, so renaming the customer never moves data outside its folder; an
archived customer stops the inbox (`409 customer_unavailable`). The destination and customer are fixed after
creation: a new destination is a new inbox, so the history of where data went stays readable.

## Security model

- **The token is the credential, and only in the header.** The public route is mounted outside the user-session
  middleware; a user's access token is not an inbox token (and vice versa). A token in the query string
  (`?token=` / `?access_token=`) is refused with `400 token_in_query` so it never sits in access logs, proxies
  or shell history; the answer tells the caller to rotate it.
- **Unknown, revoked, rotated and mismatched tokens are indistinguishable** (`401 unauthorized` with
  `WWW-Authenticate: Bearer`). The lookup is by hash only; the inbox id in the URL must also match.
- **Postgres isolation.** Every admin query runs under organization RLS. The pre-tenant lookup uses the
  `inbox_lookup` policy: a transaction that sets `app.inbox_hash` sees only the inbox with that hash.
- **Two rate limits.** Per client address before the token is checked (600/min, flood and guessing guard; fails
  open like the other public endpoints) and per inbox after (its `rate_per_min`, 1..600, default 30; fails
  closed: `503 unavailable` if the limiter cannot be read). Both use the shared limiter (Redis when `REDIS_URL`
  is set, in-process otherwise).
- **Body cap before reading.** A declared `Content-Length` above the inbox cap is refused without reading the
  body; chunked bodies go through `http.MaxBytesReader` (cap = `max_bytes` + 64 KiB multipart overhead), and the
  parser enforces `max_bytes` on the payload itself. `max_bytes` can never exceed `DRIVE_MAX_FILE_MB`.
- **Kill switch and read-only.** Each push asks the controls first: kill switch → `503 kill_switch_active`,
  read-only → `423 read_only_mode`, unreadable controls → read-only (fail closed). The drive's own write gate
  applies as well. Refusals of an authenticated caller are audited (`inbox.push_refused` with the code).
- **External content is data, never instructions.** Nothing is interpreted beyond what storing it safely needs
  (JSON is checked to be JSON). Files are authored by the inbox principal, so when an agent reads one, `fs.read`
  marks it as authored by others (tainted, `internal/drive/gwtool.go`): any external action that follows needs a
  human approval. Agents have no tool to create, rotate or revoke inboxes.
- **Audit without content.** `inbox.created`, `inbox.updated`, `inbox.token_rotated`, `inbox.revoked`,
  `inbox.push_received` (push id, size, file ids, kinds, versions, whether an idempotency key was used) and
  `inbox.push_refused`. Never the token, never the content. The idempotency key itself is not stored, only its
  SHA-256 (it may carry the caller's identifiers).
- **Media types** follow the drive's allowlist (no HTML, SVG, archives or executables): `415
  unsupported_media_type`; a type the inbox does not allow: `415 kind_not_allowed`.

## Limits

| Limit | Value |
|---|---|
| Active inboxes per organization | 50 |
| Max size per push | 1 KB .. `DRIVE_MAX_FILE_MB` (25 MB by default); 5 MB when not set |
| Pushes per minute per inbox | 1 .. 600 (default 30) |
| Requests per minute per client address (public route) | 600 |
| Files per multipart push | 20 |
| `X-Idempotency-Key` | ≤ 200 printable ASCII characters |
| Recent pushes listed | 20 by default, 100 max |

Optional request headers: `X-Filename` (raw bodies; the extension must match the `Content-Type`) and
`X-Idempotency-Key` (a retry with the same key answers `200` with the first result and `replayed: true`; while
the first one is still being written: `409 push_in_progress`).

## Push URL

The admin API returns `push_path` (`/api/v1/inbox/<id>`) and `push_url`, built from `PUBLIC_API_URL` (e.g.
`https://app.example.com/api/v1`) or `UI_BASE_URL + /api/v1`. When neither is set `push_url` is empty and the UI
composes the URL from its own API base and origin.

## What was not verified

- The Postgres store against a real database in this change: `inboxes_pg_test.go` needs `TEST_DATABASE_URL` and
  skips otherwise; the HTTP tests use the memory store.
- The Redis-backed limiter with inboxes (the HTTP tests use the in-process limiter).
- Real senders (n8n, Zapier, Make, an ERP export): only `curl`-shaped requests built by the Go HTTP client in
  the API tests.
- The UI was type-checked (`tsc`) and its pure helpers unit-tested; it was not exercised in a browser nor
  screenshotted, and has no Playwright e2e yet.
- Browser-originated pushes: the public route goes through the normal CORS policy, so a web page on another
  origin cannot call it unless that origin is allowed; inboxes are meant for server-to-server calls.
