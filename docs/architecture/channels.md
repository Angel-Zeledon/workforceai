# Channels: two-way Slack for people (implemented) and WhatsApp Business (design only)

Status:

| Channel | State | Where |
|---|---|---|
| Web Push (PWA) | implemented | `docs/architecture/08-api.md` section 18 |
| E-mail / Slack / Teams **alerts** with signed one-time links | implemented | `backend/internal/notify`, `approvals-notify.md` |
| **Slack, two-way** (Approve / Reject buttons, slash command) | **implemented** (A6), not yet tried against a real workspace | `backend/internal/slackapp`, `08-api.md` section 27, migration 801 |
| WhatsApp Business | design only (needs the owner's Meta business account) | this document, last section |

The two-way Slack channel is a different thing from the **Slack connector of the agents**
(`backend/internal/connections/slack`, a tool an agent uses through the Tool Gateway with its own vaulted credential and
human approval on every post). The channel is for people; it has its own app, its own secrets and never shares them
with the connector.

## Principles (all channels)

1. A channel notifies and collects a decision; it never widens what a person may do.
   The decision is always recorded through the same application path as
   `POST /approvals/{id}/decision` (permissions, double approval, no self-approval,
   kill switch, read-only mode and audit all apply unchanged).
2. Messages carry only title, risk, agent and approval id. Details, arguments, recipients
   and content stay inside the authenticated app.
3. The channel identity must be mapped to a platform user first. An unmapped sender can decide nothing.
4. Tenant isolation: every link, token and webhook route is scoped to one
   organization; the organization is derived from the stored configuration (the request URL), never from the
   message body.
5. Every inbound decision is audited (who, which approval, outcome) without payloads.
6. Idempotency: a repeated delivery decides nothing twice.

## Slack, two-way (implemented)

### Setup (bring-your-own app)

An admin (`org:manage`) opens **Security center › Slack** (`SlackChannelCard`) and follows the steps shown there:

1. create a Slack app in their workspace; bot scopes `chat:write`, `commands`, `users:read`, `users:read.email`;
2. Interactivity request URL: `<public API>/api/v1/channels/slack/<org>/interactions`;
3. slash command `/oficina` (any name works; replies use the name Slack sends) with request URL
   `<public API>/api/v1/channels/slack/<org>/commands`;
4. install the app, paste the **Signing Secret** and the **Bot User OAuth Token**;
5. optionally invite the bot to an approvals channel and paste the **channel id** (`C0123…`, not the name).

Saving a new bot token calls `auth.test`: a refused token is not stored (`400 slack_auth_failed`), and the workspace
it belongs to (`team_id`) becomes the only workspace whose requests are accepted. Both secrets are stored **only
sealed** with the vault (`vault.Seal`, purposes `slackapp-signing-secret` / `slackapp-bot-token`, bound to the
organization); there is no plaintext column and the API never returns them (only the last 4 characters of the token).
A deployment without a vault key cannot store them (`409 vault_required`), exactly like the bring-your-own-model keys.
"Test" re-runs `auth.test` and posts a harmless message to the channel. "Disconnect" deletes the secrets, every
account link, pending codes and message references.

### Inbound request security (in this order)

1. Body capped at 64 KB before anything else (`413`).
2. The organization comes from the request URL; without a configured app the route answers `404`.
3. Slack v0 signature: `v0=HMAC-SHA256(signing_secret, "v0:" + X-Slack-Request-Timestamp + ":" + raw_body)`,
   constant-time comparison (`hmac.Equal`), timestamp within **5 minutes** in either direction (replay window).
   Any failure is `401` and nothing is parsed. (`slackapp.Verify`)
4. The payload's `team_id` must equal the stored workspace (`403` otherwise).
5. Interactions are acknowledged at once (Slack expects an answer within 3 s); the decision runs in the background
   and answers through the interaction's `response_url`, which is accepted only for `https://hooks.slack.com/...`
   (no other host, no port, no userinfo: no SSRF through a forged payload).

### Identity: Slack user -> platform user

The `user.id` in a signed payload only says which Slack account clicked. It becomes a platform identity through:

- **explicit link** (always available): the person signs in to the app, presses "Get a link code"
  (`POST /channels/slack/link-code`) and types `/oficina vincular ABCD-EFGH` in Slack. Codes are random
  (8 characters from a 32-symbol alphabet), single use (the consuming `UPDATE ... WHERE used_at IS NULL` is the claim),
  valid 10 minutes, bound to the organization and stored as a hash. A new code replaces the previous one. Five wrong
  codes from the same Slack user within 15 minutes block further attempts for that window (in-process brake).
  The link is stored as `(org, team_id, slack_user_id) -> user_id` (`slack_user_links`).
- **verified e-mail** (on by default, `email_matching`, can be turned off): when there is no link, the server reads
  the Slack user (`users.info`) and maps it only if Slack reports the e-mail as confirmed (`is_email_confirmed`), the
  account is not deleted and not a bot, **and** a member of the organization has that address **verified on the
  platform** (`users.verified_at`). This is re-checked on every interaction and never stored as a link.

A link never carries a role. On every decision the platform user's **current membership** is read (the same check the
web session does on every request): a removed member gets `not_member`, a demoted admin loses the buttons' effect at
once, an expired trial account is not a member.

### Permission model: the same as the web

- Deciding (approve **or** reject) requires `approvals:decide` (admin / owner), like the web route. Members and
  viewers are refused (`forbidden`) and the `/oficina pendientes` list does not show them buttons.
- The decision goes through `application.Approvals.Decide` with `WithActor(user_id)`, `WithActorRole(current role)`
  and `WithChannel("slack")`, so the application enforces everything it enforces for the web: `required_role`,
  double approval with two **distinct** people (a web approval and a Slack approval of the same person count once),
  `no_self_approval` (the requester never approves), agents and system principals never decide. An empty or unknown
  role is refused before the call (it never falls back to the demo default).
- **Kill switch / read-only mode**: approving from Slack is refused (`kill_switch` / `read_only`); if the control
  state cannot be read it fails closed. Rejecting stays allowed because it only stops work.
- **High risk** (`high_risk_in_app`, on by default) and **code execution** (always): the message has no Approve
  button and an approve press is refused (`needs_app`); the person approves in the app with their session.
  Rejecting from Slack is allowed.
- Turning the channel off (`enabled: false`) refuses every press and command (`disabled`).

Unlike a signed e-mail link (which counts as an admin's decision within `LinkAllowed`), a Slack decision is always
made by an identified person with their own role, so double approval and required roles work from Slack.

### Idempotency

Each press is keyed by `sha256(team, user, action_id, approval_id, action_ts)` and claimed in `slack_interactions`
(primary key `(org_id, dedupe_key)`, `INSERT ... ON CONFLICT DO NOTHING`); a redelivery of the same press is
acknowledged and ignored, the stored `outcome` records what the first one did (rows pruned after a day). A second,
different press (a new click) reaches the application, which refuses a second decision on a closed approval
(`not_pending`, and the message is refreshed with the real state) or a second approval by the same person
(`already_approved_by_you`).

### Messages

- `approval.requested` (risk >= `min_risk`, default medium) posts a Block Kit message to the channel: title (escaped:
  `&`, `<`, `>` so a title cannot become a mention, link or `@channel`), risk, agent, "approvals n of m", buttons
  **Approve** (with a confirmation dialog) / **Reject** whose `value` is only the approval id, and **Open in the app**.
  Details, arguments and recipients are never sent.
- After a decision from Slack the original message is replaced through `response_url`: "Approved by @person",
  "Rejected by @person", or "Approval 1 of 2. Another person is needed." (buttons stay). Refusals are ephemeral
  (only the person sees them).
- `approval.progress` / `approval.resolved` (decided in the app, by a link, by a timeout or from Slack) update the
  posted channel message with `chat.update` (`slack_approval_messages` remembers channel and ts).
- Texts in es / en / pt (`locale` of the Slack settings).

### Slash command

Ephemeral answers; all subcommands except `vincular` / `desvincular` / help need a mapped member with
`approvals:read`:

| Command | Does |
|---|---|
| `/oficina estado` (`status`) | pending approvals, tasks in progress, tasks waiting for approval, kill switch / read-only state |
| `/oficina pendientes` (`pending`, `pendentes`) | up to 5 pending approvals with buttons (only for people who can decide) |
| `/oficina vincular <code>` (`link`) | links this Slack account with a code from the app |
| `/oficina desvincular` (`unlink`) | removes the links of the linked person |
| anything else | help |

### Audit

`channel.slack.decision` for every press (including refusals before the application is reached) with
`channel: "slack"`, `decision`, `outcome`, `slack_team`, `slack_user`, `identity` (`code` | `email`); the actor is the
platform user id, or `slack:<team>:<user>` when nobody was mapped. The application's own entries for the same decision
(`approval.approved|rejected|partial|decision_refused`) carry `channel: "slack"` too. Also audited:
`channel.slack.settings_updated` (no secrets), `.tested`, `.disconnected`, `.link_code_issued`, `.linked`,
`.link_refused`, `.unlinked`. Bad signatures are logged (organization and reason), not audited, so an attacker cannot
flood the audit trail.

### Data (migration 801, RLS per organization, down script in `backend/migrations/down`)

`slack_app_settings`, `slack_user_links`, `slack_link_codes` (hash only), `slack_interactions`,
`slack_approval_messages`. The GDPR export redacts `*_enc` and `code_hash` columns automatically (tenantdata rules).

### What is real and what is not

- Real and tested with a faked Slack API: signature verification (valid, stale, future, tampered body and timestamp,
  wrong secret), workspace check, identity refusal (unlinked, e-mail confirmed on one side only, bots, matching off),
  link codes (single use, expiry, brake), role enforcement through the real `application.Approvals` (viewer, member,
  required role, self-approval, double approval with distinct people, demoted and removed members), idempotency of
  redelivered presses, kill switch / read-only / high risk / channel off, sealed secrets, message content and updates,
  slash commands, routes and permissions over HTTP. Postgres store and the down migration have gated tests
  (`TEST_DATABASE_URL`).
- **Not verified**: a real Slack workspace (no workspace was available while building it). Field names and behaviour
  follow Slack's public documentation (`block_actions` payload, `response_url`, `auth.test`, `users.info`
  `is_email_confirmed`, `chat.postMessage`, `chat.update`); if Slack omits `is_email_confirmed` for a workspace the
  e-mail mapping simply never matches and people use the link code.
- Not built: Slack app distribution / OAuth install (each organization creates its own app), Enterprise Grid
  multi-workspace installs, App Home, modals for notes on a rejection, rate limits per organization for outbound
  messages, a persistent brake for link-code guessing across replicas (it is per process).

## WhatsApp Business (Cloud API) — design only, out of scope until the owner provides a Meta business account

- Channel is notification first; decisions by reply are optional and off by default.
- Consent: explicit opt-in per user stored with timestamp and source; an easy
  opt-out ("STOP") immediately disables the link. No opt-in, no message.
- Templates: outside the 24-hour customer service window only pre-approved
  template messages may be sent. Template `approval_requested` with variables
  `{{1}}` title, `{{2}}` risk and a URL button to `/approvals/<id>`; templates per
  locale (es, en, pt) approved in Meta Business Manager. No details in variables.
- Inbound webhook: `GET` verification (`hub.verify_token`) and
  `POST /api/v1/channels/whatsapp/webhook`.
  - Verify `X-Hub-Signature-256` = `sha256=HMAC-SHA256(app_secret, raw_body)` with
    constant-time comparison before any parsing; reject otherwise.
  - Deduplicate by message id (webhooks are retried); acknowledge fast, process async.
  - Interactive reply buttons ("Approve"/"Reject") are accepted only from a linked,
    consenting phone number inside the service window, and only for approvals at
    low or medium risk; high risk always requires the app.
- Phone numbers are personal data: stored encrypted, never logged, deleted with
  the link.
- Reuse from Slack: the link-code flow, the dedupe table pattern, `WithChannel("whatsapp")` in the approval audit and
  the same rule "the decision goes through `application.Approvals` with the person's current role".

## Open points

- Outbound rate limits per organization and per user (Slack and future channels).
- Whether an organization policy should be able to forbid decisions from chat channels altogether (today: the
  `enabled` switch of the channel, and `high_risk_in_app`).
