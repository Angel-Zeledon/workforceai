# Channels: Slack interactive approvals and WhatsApp Business (design only)

Status: design, no code. Web Push (`docs/architecture/08-api.md` section 18) is the
only channel implemented. This document fixes the rules that any further channel
must follow before it is built.

## Principles (all channels)

1. A channel notifies and collects a decision; it never widens what a person may do.
   The decision is always recorded through the same application path as
   `POST /approvals/{id}/decision` (permissions, double approval, no self-approval,
   kill switch, read-only mode and audit all apply unchanged).
2. Messages carry only title, risk and approval id. Details, arguments, recipients
   and content stay inside the authenticated app.
3. The channel identity must be linked to a platform user first (account linking
   with a one-time code issued in the app). An unlinked sender can do nothing.
4. Tenant isolation: every link, token and webhook route is scoped to one
   organization; the organization is derived from the stored link, never from the
   message body.
5. Every inbound event is audited (who, which approval, outcome) without payloads.
6. Idempotency: each action carries the approval id plus a nonce; a repeated
   delivery decides nothing twice (a second decision on a closed approval is a no-op).

## Slack interactive approvals

- Install: Slack app per organization with bot token stored in the vault (like the
  existing Slack connector); scopes `chat:write`, `users:read.email`.
- Outbound: on `approval.requested` post a Block Kit message to the approvals
  channel or DM: title, risk, two buttons (`approve`, `reject`) and an
  "Open in app" link. `action_id` and `value` hold only the approval id.
- Inbound endpoint: `POST /api/v1/channels/slack/interactions` (public, no bearer).
  - Verify the signature before parsing: `v0=HMAC-SHA256(signing_secret, "v0:" + timestamp + ":" + raw_body)`,
    constant-time comparison, reject if `|now - timestamp| > 5 min` (replay) and
    cap the body size. The signing secret lives in the vault, per workspace.
  - Resolve `team_id` + `user.id` to a linked platform user; unknown -> ephemeral
    "link your account" reply, no decision.
  - Role check: the linked user must hold `approvals:decide` and the approval's
    `required_role`. For double approval the second approver must be a different
    person; the message is updated with progress ("1 of 2").
  - Reply within 3 s (ack) and update the message with `response_url` (valid
    host `hooks.slack.com` only, to avoid SSRF).
- Buttons are disabled for high-risk approvals if the organization requires an
  in-app confirmation (policy flag), in which case the button opens the app.

## WhatsApp Business (Cloud API)

- Channel is notification first; decisions by reply are optional and off by default.
- Consent: explicit opt-in per user stored with timestamp and source; an easy
  opt-out ("STOP") immediately disables the link. No opt-in, no message.
- Templates: outside the 24-hour customer service window only pre-approved
  template messages may be sent. Template `approval_requested` with variables
  `{{1}}` title, `{{2}}` risk and a URL button to `/approvals/<id>`; templates per
  locale (es, en) approved in Meta Business Manager. No details in variables.
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

## Open points

- Where channel links live (new table `channel_links` with RLS, like migration 330).
- Rate limits per organization and per user for outbound messages.
- Whether decisions by chat count toward double approval (proposed: yes, with the
  channel recorded in the audit entry).
