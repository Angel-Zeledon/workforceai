# tenantdata: export and deletion of an organization (GDPR)

Code: `export.go`, `lifecycle.go`, `purge.go`, `janitor.go`, `schema.go`. HTTP: `internal/api/tenantdata.go`.
Wiring: `cmd/server/tenantdata.go`. Migration: `650_org_lifecycle.sql`. UI: `frontend/src/components/settings/DataPrivacy.tsx`.

| Endpoint | Who | What |
|---|---|---|
| `GET /api/v1/org/export` | owner, admin | ZIP of all the organization's data. 1 per hour per organization (429 + `Retry-After`). Audited as `org.exported`. |
| `GET /api/v1/org/deletion` | any member | `{pending, org_name, requested_at, deletion_at, grace_days, protected, now}` (for the banner). |
| `POST /api/v1/org/deletion` | owner (`org:delete`) | `{confirm_name, password}`: exact organization name + the owner's current password (argon2id, existing hasher; 5 attempts / 15 min / user). Schedules the purge 7 days later. Idempotent. Audited (`org.deletion_scheduled`, `org.deletion_denied`). |
| `DELETE /api/v1/org/deletion` | owner | Cancels a pending deletion (`org.deletion_cancelled`). |

While a deletion is pending nothing changes: the organization keeps working and the UI shows a banner.

## Which tables (discovered, never hard-coded)

`Discover` reads `pg_catalog` for the current schema and classifies every table:

* **org**: has an `org_id` column (this includes `organizations`, whose `org_id` is a generated copy of `id`,
  `memberships`, `refresh_tokens`, `invitations`, ...).
* **child**: no `org_id`, but references an org (or child) table through a foreign key, transitively. Its rows are
  scoped with `EXISTS (...)` joins along the foreign keys.
* **account**: `users` and the tables that only hang from users (e.g. `email_verifications`).
* **global**: declared in `GlobalTables` (`schema_migrations`, `org_tombstones`).

Anything else is **unclassified** and makes the guard tests fail:

* `TestMigrationsGuard` (no database): parses every migration; each `CREATE TABLE` must have `org_id`, reference
  organization data, hang from `users`, or be listed in `GlobalTables`.
* `TestPGGuardEveryOrgTableIsCovered` (Postgres): on the fully migrated schema, every table with `org_id` must be in
  the export plan (or in `ExcludedFromExport`) and in the purge order, and there must be no unclassified table and no
  foreign-key cycle.

So a new migration that adds a tenant table with `org_id` is exported and purged automatically; a new table that is
not tenant data must be declared in `GlobalTables` on purpose.

## What is exported

`<table>.jsonl` for every org and child table (one JSON object per row, `to_jsonb` of the row, empty file when the
organization has no rows there) plus `users.jsonl` with the accounts that are members of the organization. File
bytes stored in Postgres (`drive_objects.data`, `artifact_blobs.data`, the bytea object-store fallback) are written
as separate entries under `files/<table>/<key or id>` and the row references them as `{"file": ..., "size": ...}`.
When an external object store (S3) is configured, the objects referenced by `object_key` columns are added under
`files/objects/<key>` (missing ones are listed in the manifest). `README.txt` (scope, date, tables and row counts)
and `MANIFEST.json` (tables, kinds, row counts, redacted columns, the redaction policy) complete it.

The export runs on the **application pool** (role `app_user`, RLS bound to the organization) inside one
`REPEATABLE READ READ ONLY` transaction: a consistent snapshot, and the database itself refuses any row of another
tenant even if a predicate were wrong. Files are streamed (two passes ordered by `ctid` in the same snapshot) so
memory stays bounded.

## What is never exported (secrets)

Secret columns are removed **inside the database** (`to_jsonb(t) - ARRAY[...]`), so they never reach the backend
process, and appear as `"[REDACTED]"`. A column is secret when any of these holds:

1. It is in the explicit denylist `RedactedColumns`: `users.password_hash`, `refresh_tokens.token_hash`,
   `invitations.token_hash`, `email_verifications.token_hash`, `oauth_states.state_hash`,
   `oauth_states.pkce_verifier_enc`, `credentials.ciphertext`, `credentials.nonce`, `org_data_keys.wrapped_dek`,
   `connections.oauth_client_secret_enc`, `org_llm_credentials.sealed_key`, `push_subscriptions.endpoint|p256dh|auth`.
2. Its name matches the pattern `secretName` (password, secret, ciphertext, sealed, wrapped, private_key, api_key,
   pkce, nonce, dek, `*_enc`, `{password,token,state,key,secret,code}_hash`, `{access,refresh,id,bearer}_token`).
3. It is `bytea` and not listed in `BlobColumns` (default deny: a new binary column is assumed to be ciphertext
   until someone declares it as file content).

Content digests are not secrets and are exported (`audit_logs.hash/prev_hash` let the customer verify the audit
chain, `sha256`, `content_hash`, `args_hash`). Whole tables excluded: `oauth_states` (in-flight OAuth handshakes),
account security tables (`email_verifications`), global tables. Full-text `tsvector` columns are left out (derived).

## Purge algorithm

`Purger.PurgeOrg` runs on the **owner connection** (`MIGRATE_DATABASE_URL`, else `DATABASE_URL` without
`SET ROLE app_user`), in **one transaction**:

1. Refuse the demo organization (`00000000-0000-0000-0000-000000000001`).
2. `set_config('app.org_id', org)` (so it also works if the owner is subject to `FORCE ROW LEVEL SECURITY`) and
   `set_config('app.purge_org', org)` (see append-only below); `statement_timeout` 10 min, `lock_timeout` 30 s.
3. Lock the organization row; for scheduled deletions re-check under the lock that `deletion_at <= now()` (a cancel
   that races the janitor wins).
4. Re-discover the schema inside the transaction and compute the delete order: a topological sort of the foreign-key
   graph among the org and child tables (children before parents, self references allowed, any other cycle aborts),
   with `memberships` last.
5. Remember the members and (S3 only) the object keys; `DELETE` every table's rows of the organization in that order.
6. Accounts left without any membership are deleted with everything that references them (`UserDependents`: the
   tables that reach `users` through foreign keys, in dependency order: refresh tokens of any organization, e-mail
   verifications, ...). Accounts that still belong to another organization stay.
7. Delete the `organizations` row.
8. Verify that every org and child table has zero rows of the organization; otherwise roll back.
9. Insert the tombstone and commit. Then delete the external objects (best effort, logged).

**Append-only tables** (`audit_logs`, `artifact_versions`, `drive_versions`, `skill_versions`) keep refusing UPDATE and
DELETE; migration 650 lets their triggers accept a DELETE only when `app_purging_org(OLD.org_id)` is true, i.e. inside
a transaction that set `app.purge_org` to that very organization. The application role has no DELETE privilege on
those tables, so it cannot use this; a new append-only trigger must call `app_purging_org()` too (the Postgres purge
test fails otherwise).

**Tombstone**: `org_tombstones(org_hash, reason, deleted_at)`, with `org_hash = sha256("org:" + id)`: no name, no
e-mail, nothing personal; it lets operators answer "was this tenant deleted and when" and spot abuse. Not readable
by the application role.

## Janitor

`Janitor.RunOnce`, every 15 minutes (started from `main.go`; `TENANT_JANITOR=off` disables it):

* takes a session advisory lock (`pg_try_advisory_lock`) on a dedicated owner connection: with several replicas only
  one works, the others skip the tick;
* purges every organization whose `deletion_at` has passed (`PurgeOptions{RequireDue: true}`);
* **trial accounts**: users with `trial_expires_at` more than 7 days in the past. Their organizations where *every*
  member is such an expired trial account are purged through the same `PurgeOrg` (reason `trial_expired`); then the
  account itself is deleted if no membership is left (`PurgeAccounts`, same code as step 6).

## Safety rules

* The demo organization can never be deleted: refused by `ScheduleDeletion`, by `PurgeOrg`, skipped by the janitor's
  queries, and a `BEFORE DELETE` trigger on `organizations` (migration 650) aborts any transaction that tries.
* Purging is all-or-nothing (one transaction + verification before commit).
* Deletion needs the owner role, the exact organization name and the owner's password, then 7 days of grace during
  which any owner can cancel.
* Export is admin/owner only, rate limited, audited, and runs under RLS.

## Known limits

* Rate limits use the shared limiter (Redis when configured, otherwise per process).
* Objects in an external object store are deleted after the commit, best effort; a failure is logged (the database
  rows, including the keys, are already gone).
* If the owner role is neither superuser nor `BYPASSRLS`, the janitor finds due organizations only through
  `memberships` (one query per organization).

## Running the Postgres tests

```
TEST_DATABASE_URL=postgres://postgres:pw@127.0.0.1:55433/postgres?sslmode=disable go test ./internal/tenantdata/ -run PG -v -timeout 30m
```
