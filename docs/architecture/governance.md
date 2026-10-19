# Governance: admin console, reports, retention, API tokens and webhooks

Code: `backend/internal/governance`, `backend/internal/webhooks`, `backend/internal/audit/bundle.go` (export only).
HTTP wiring: `backend/internal/api/governance_wire.go` (one marked block in `router.go`), `backend/cmd/server/governance.go`.
Migrations: `710_governance.sql`, `711_api_access.sql` (up files live in `internal/infrastructure/postgres/migrations`, which is
what the server embeds; the matching rollbacks are in `backend/migrations/down`).
UI: `/admin` (`frontend/src/components/admin/*`) and `frontend/src/components/settings/ApiAccess*.tsx`.

Everything needs Postgres (Row-Level Security by `org_id`, same pattern as every tenant table). Without `DATABASE_URL`
the governance routes are not mounted.

## Roles

| Action | Who |
|---|---|
| Console, spend overview, departments, limits, tokens, webhooks, retention read/preview | owner, admin |
| Change or run the retention policy | owner only |
| Usage report | `metrics:read` (every role) |
| Audit export | `audit:read` (admin, owner) |
| Org-wide ZIP export (`GET /org/export`, existing) | owner, admin, 1 per hour |

## Endpoints (`/api/v1`)

* `GET /governance/overview`: agents with department, month spend and monthly cap; departments with limit and state.
* `PUT /governance/agents/{id}/department` `{department}`: free-text tag (max 60, "" clears). Plain nullable string
  column `agents.department`; another stream may build real departments on top of the same tag.
* `PUT /governance/departments/limit` `{department, monthly_usd}` (0 removes).
* Per-agent monthly cap: the existing `PUT /agents/{id}/budget` (budget_caps), which the budget service enforces.
* `GET /governance/reports/usage?by=agent|project|department&from&to[&format=csv]`: calls, requests, tokens, cost,
  share. Default range: current month; max 400 days. Projects join through `projects.request_id`. CSV cells that start
  with `= + - @` are neutralized (CSV injection).
* `GET /governance/audit/export?format=csv|json|zip&actor&type&from&to`.
  * `csv`: streamed; the chain check travels in `X-Audit-Chain-OK`, `X-Audit-Chain-Checked`, `X-Audit-Head-Seq`,
    `X-Audit-Head-Hash`, `X-Audit-Chain-Reason`.
  * `json`: `{filters, entries, verification:{chain, export}, records[]}`. `chain` is `audit.Verify` over the whole stored
    chain; `export` is `audit.VerifyRecords` recomputing every exported hash and linking adjacent entries.
  * `zip`: `audit.csv` + `verification.json` + `README.txt`. json and zip are assembled in memory (48 MiB cap, then 413:
    narrow the dates or use csv).
* `GET/PUT /governance/retention`, `POST /governance/retention/preview`, `POST /governance/retention/run {confirm:true}`.
* `GET/POST/DELETE /governance/tokens`.
* `GET/POST/DELETE /governance/webhooks`, `PUT /governance/webhooks/{id}/active`, `POST .../test`, `GET .../deliveries`.
* `GET /governance/policies`: read-only snapshot (approval rules, model policy, retention, scopes, events).

## Data retention

Policy per organization (`org_retention`): `enabled`, `chats_days`, `files_days`, `audit_days`; 0 means keep for ever.

Safety rules, all enforced in `Purger.run` (the one place that deletes), not only in the API:

1. Floors: chats and files >= 30 days, audit >= 365, ceiling 3650. A policy edited straight in the database below the
   floor is refused at run time.
2. Disabled by default; a disabled policy never deletes. The shared demo organization is protected.
3. Batches: at most 5000 rows per table per organization per run; the rest follows in the next run (`capped`).
4. Chats: messages older than the cutoff are deleted; a conversation is deleted only when it is itself older than the
   cutoff and has no messages left.
5. Files are never hard-deleted: they go to the Drive trash (`deleted_at`, `deleted_by = system/retention`) and can be
   restored. Old file versions stay append-only.
6. Audit (`audit_days`, migration 782): the trail stays append-only for the application role. The job removes old
   chained entries only through `audit_purge_through(org, seq)`, a `SECURITY DEFINER` function that works only for the
   organization of the transaction, never removes the head of the chain (the newest entry stays), reads the hash of the
   last removed entry from the table itself and stores it as the organization's **checkpoint**
   (`audit_chain_heads.checkpoint_seq/checkpoint_hash`, plus `purged_total`) in the same transaction as the delete. The
   head trigger lets the checkpoint move forward only inside that function and never during an append. The job picks the
   oldest entries older than the cutoff (timestamps strictly increase along the chain, so they are a prefix), at most
   one batch per run, and counts them in `audit_entries` (also in the `retention.purged` audit entry and in the
   preview). `audit.Verify` starts at the checkpoint: the first remaining entry must have `seq = checkpoint_seq + 1` and
   `prev_hash = checkpoint_hash`, so editing, inserting, reordering or truncating what is left still breaks the chain
   (`Report.checkpoint_seq` says where it started). An external anchor older than the checkpoint cannot be compared and
   is not a failure; one at the checkpoint is compared with the checkpoint hash. `audit_skipped` is now true only when
   the store cannot purge the chained trail (it does not implement `governance.AuditRetention`). Entries written before
   the chain existed (`seq` NULL) are never purged. What the checkpoint cannot prove is the content of the purged
   prefix: keep an export or an external anchor if you need that evidence.
7. Scheduling: a run is claimed atomically (`UPDATE ... SET next_run_at ... WHERE enabled AND next_run_at <= now()`),
   so replicas never purge the same organization twice. Saving a policy delays the first run by an hour so a
   mistake can be fixed. The job runs hourly, each organization at most once per 24 h. `RETENTION_JOB=off` disables it.
8. Preview counts exactly what a run would remove and deletes nothing; a run records `retention.purged` in the audit
   trail (counts only, never content) and stores `last_result`.

Full organization export (JSON of every table + files, secrets redacted, under RLS in a read-only snapshot) is the
existing `internal/tenantdata` export; the console links to it. New tables are picked up by schema discovery.

## API tokens

`aiw_` + 256 random bits. Only `sha256(token)` is stored (the token has full entropy, so a fast hash is the right
tool) plus a 10 character display prefix. Shown once. Scopes: `requests:read`, `requests:write`, `reports:read`,
`agents:read`, `usage:read` (write implies read). No scope reaches administration. Optional expiry (max 730 days),
revocation is immediate, `last_used_at` is updated at most once a minute, 25 active tokens per organization.

A bearer has no organization, so lookup uses a second policy on `api_tokens`
(`token_hash = current_setting('app.token_hash')`, SELECT only): a transaction that announces a hash sees that single
row. Everything else, including the `last_used_at` update, runs under the normal tenant policy.

The public API is mounted at `/api/public/v1` (outside the user-session group): `GET /agents`, `GET|POST /requests`,
`GET /requests/{id}`, `GET /reports`, `GET /reports/{id}`, `GET /usage`. The organization comes only from the token;
120 requests/minute/token; submitting work is audited (`api.request_submitted`, with the token id).

## Webhooks

Events: `request.done` (internal `request.completed`), `approval.requested`, `report.created`, plus `webhook.ping` for
"send test". `webhooks.Publisher` decorates the event publisher and enqueues asynchronously (bounded queue; a full
queue drops with a warning) into the outbox `webhook_deliveries`.

* Body: `{id, type, created_at, org_id, data}`; identical bytes on every retry.
* Signature: `X-AIWOS-Signature: t=<unix>,v1=hex(HMAC-SHA256(secret, "<t>." + body))`, plus `X-AIWOS-Event`,
  `X-AIWOS-Delivery`. Receivers should check the MAC in constant time and reject old `t` (`webhooks.Verify`).
* Secret: `whsec_...`, shown once, stored sealed under the organization's data key (vault). If no key is configured the
  endpoint cannot be created (fail closed).
* URL: https only, public hosts only, ports 443/8443. The same `webtool.NetGuard` as the agents' web tool validates the
  URL, resolves the name itself, requires every answer to be public and pins the connection to the validated address
  (anti SSRF and DNS rebinding); no redirects, no environment proxy. Revalidated on every attempt.
* Retries: 2xx = delivered; 408, 425, 429, 5xx and network errors retry after 30 s, 2 m, 10 m, 1 h, 6 h, 24 h (+-10%
  jitter), 7 attempts then `dead`; other 4xx are dead at once. Rows are leased (`FOR UPDATE SKIP LOCKED` + next attempt
  moved forward) so replicas never deliver twice. 50 consecutive failures switch the endpoint off. Delivered and dead
  rows are pruned after 30 days. Delivery is at least once: receivers must de-duplicate by the delivery id.

## What remains

* The console still words `audit_days` as "stored only"; it should show `audit_entries` of the preview and last run.
* Department limits are reported and flagged but not yet enforced by the budget service (agent caps are).
* The department tag is a string; replace the free text with a picker once real departments exist.
* The sidebar highlight and page title of `/admin` use the office defaults (`sectionOf` lives in `navBridge.ts`).
* Postgres integration tests of `governance.PgStore` and `webhooks.PgStore` (need `TEST_DATABASE_URL`).
