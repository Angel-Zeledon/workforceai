# Meetings and calendar

Status: implemented (backend `internal/meetings`, `internal/calendar`, `internal/connections/meetingcal`;
frontend `components/meetings`, route `/meetings`). Migrations 740 and 741.

Meeting rooms in the office, scheduling with people and agents, video through a **provider link** (no media
stack of our own), Google Calendar and Microsoft 365 through the connections vault, an ICS export and a
per-person subscription feed, and minutes written by an agent after the meeting, saved to the drive and turned
into tasks by an explicit human step.

## Principles

* **We run no video.** A meeting carries a link: Jitsi (default, configurable base URL, can be embedded in the
  app), Google Meet, Zoom, Microsoft Teams or any https link. Meet and Teams links can be created by the
  calendar itself when the meeting is synced; Zoom and custom links are pasted. Links are validated (https, no
  credentials, host belongs to the provider it claims: `meet.google.com.evil.io` is refused).
* **Server decides.** RBAC reuses existing permissions (no new ones): `requests:read` to see meetings, rooms,
  minutes and your feeds; `requests:create` to create, run, brief, write minutes, create tasks, read other
  people's availability; `files:write` to save minutes; `org:manage` to change the settings;
  `connections:read` to list calendar connections.
* **Calendars are connections.** Same vault, same bring-your-own OAuth app, same kill switch, revoke and
  needs-reauth flow as Gmail/Drive/Slack. They are **person-driven**: the providers expose **no agent tools**.
  The agent-facing `google_calendar` provider (read events, create event behind approval) is unchanged.
* **Notes are data, never instructions.** Minutes generation never creates a task. Action items become tasks only
  when a person presses "Create tasks" (`POST /meetings/{id}/minutes/tasks`), through the same orchestrator
  path as `/assign` and `/reunion`, so budget, approvals and the Tool Gateway apply.
* **LLM calls stay behind the runtime interface.** Minutes use the existing `POST /v1/summarize` (`mode:
  "meeting"`, notes as untrusted messages, same budget reservation and usage ledger). In simulation, or when the
  runtime fails, a deterministic extractor still produces decisions and action items.
* **Nothing is emailed to other people from here.** Google events are written with `sendUpdates=none`. Microsoft
  Graph e-mails every attendee added to an event and has no switch against it, so for Microsoft the guests are
  written in the body of the event (`Guests: ...`) instead of in its attendee list. The person invites people from
  their own calendar if they want invitations to go out.

## Model

`Meeting`: id, title, description, room, status, start/end (UTC), organizer, provider, join_url, attendees
(`person` | `agent` | `external`, with id/name/email), agenda items (title, minutes, owner, notes), `external`
(calendar provider, connection, event id, last sync error, briefing request id), timestamps. The API adds
`has_minutes` and `embeddable`.

### State machine (`internal/meetings/state.go`)

```
scheduled --start--> live --end--> ended
scheduled --cancel--> cancelled
```

`ended` and `cancelled` are final. Only a `scheduled` meeting can be edited or rescheduled; a live meeting is
ended, not cancelled. Minutes can be written while live or after the end. Transitions are compare-and-set on
the status in the store (`UPDATE ... WHERE status = $want`), so two people pressing "start" at once cannot both
win (tested).

### Rooms

A room is an id (+ optional name and capacity). Defaults: `boardroom`, `huddle`, `focus` (names translated by the
UI). The organization can define its own in the settings. Two meetings that overlap in the same room are refused
with `room_busy` (back to back is fine; cancelled and ended meetings do not hold a room). `GET /meetings/rooms`
returns each room with the meeting holding it now and the next one.

### Settings (`meeting_settings`, per organization)

`default_provider`, `jitsi_base_url` (https only; platform default from `MEETINGS_JITSI_BASE_URL`, else
`https://meet.jit.si`), `calendar_connection_id` (where meetings are written), `free_busy_connection_id`
(optional, defaults to the former), `minutes_folder` (default `/Shared/Meetings`), `rooms`.

## Video links (`links.go`)

| provider | link |
|---|---|
| `jitsi` | generated: `<base>/<title-slug>-<64 random bits>`; embeddable in the app (iframe with camera/microphone permissions) only when the host is the configured Jitsi base |
| `meet` | pasted, or created by Google Calendar when synced |
| `teams` | pasted, or created by Microsoft 365 when synced |
| `zoom` | pasted (host must be `zoom.us`, `zoom.com` or `zoomgov.com`) |
| `custom` | any https link |
| `none` | no video |

Zoom, Meet and Teams refuse to be framed: the UI opens them in a new tab (`rel="noopener noreferrer"`). If you
run a restrictive CSP, allow `frame-src` for your Jitsi host and `Permissions-Policy: camera=(self "<jitsi>"),
microphone=(self "<jitsi>")`; the repository ships no CSP.

## Calendar integration

### Connections (`internal/connections/meetingcal`)

| provider id | auth | capabilities (separate connections, like every other provider) |
|---|---|---|
| `google_calendar_sync` | Google OAuth + PKCE, shares the deployment's Google app or the connection's own | `calendar.freebusy` (read: `calendar.freebusy` scope), `calendar.sync` (write: `calendar.events` + `calendar.freebusy`) |
| `microsoft_calendar` | Microsoft identity platform v2 + PKCE, **bring your own app** (or `MICROSOFT_OAUTH_CLIENT_ID/SECRET`) | `calendar.freebusy` (`Calendars.Read`), `calendar.sync` (`Calendars.ReadWrite`) |

Setting up Microsoft 365: in Microsoft Entra ID register an app (web platform), set the redirect URI to the
server's `OAUTH_REDIRECT_URL` (the same one Google uses), add delegated permissions `Calendars.ReadWrite`,
`Calendars.Read`, `User.Read`, `offline_access`, create a client secret, then in Settings > Connections create
"Microsoft 365 (meetings)" and paste the application (client) id and the secret. `MICROSOFT_OAUTH_TENANT`
(default `common`) picks the tenant for single-tenant apps. Microsoft has no token revocation endpoint: revoking
the connection destroys the credential in the vault; the person can also remove the app in their account.
Microsoft rotates refresh tokens; the stored one keeps working until it is unused for its lifetime (90 days).

Calls go through `connections.Service.Handle`, whose HTTP transport injects the bearer token and scrubs errors,
and through `guardedCalendar` (`cmd/server/meetings.go`): the **kill switch** stops every call, **read-only mode**
and read-class connections stop writes (`blocked` / `read_only`), a 401 flags the connection `needs_reauth`.
Simulated connections use an in-memory calendar (`calendar.Fake`).

### Operations (`internal/calendar`)

`Client`: `Events`, `Create`, `Update`, `Delete`, `FreeBusy`. Google: `events` (with `conferenceDataVersion=1` and
a `hangoutsMeet` create request for Meet links) and `freeBusy`. Microsoft: `calendarView` (with `Prefer:
outlook.timezone="UTC"`), `events` (`isOnlineMeeting` + `teamsForBusiness` for Teams links), `getSchedule`.
Provider error bodies are never surfaced; errors are the stable `reauth_required`, `invalid`, `provider_error`,
`read_only`, `blocked`.

A calendar failure never fails the meeting: it is stored as `external.sync_error` and the UI offers "retry".
Rescheduling updates the event, cancelling deletes it.

### Free/busy

`POST /meetings/free-busy {from,to,emails[],duration_min,...}` returns the busy blocks of the connected account
(`me`) and of the listed addresses, plus up to 12 suggested slots where everybody is free (30-minute grid,
working hours 9-18 in the caller's time zone, no weekends). Needs `requests:create` (it reveals colleagues'
availability). The schedule dialog calls it from "Find a free time".

### ICS

* `GET /meetings/{id}/ics`: one meeting as `.ics` (authenticated download).
* Subscription feed per person: `POST /meetings/feeds {label}` returns `{token, path}` **once**;
  `GET /api/v1/calendar/feed/<token>.ics` is public (calendar apps cannot send a bearer token). The token is
  `<org id>.<256-bit random>`; only the SHA-256 of the random part is stored (`calendar_feeds.token_hash`),
  revoking a feed (`DELETE /meetings/feeds/{id}`) kills the URL at once, at most 10 active feeds per person, the
  endpoint is rate limited per address (600/min) and answers a plain 404 for any unknown, revoked or malformed
  token. The feed lists the meetings the person organizes or attends (agents are not people: they never match),
  from 30 days ago to 180 days ahead, cancelled ones as `STATUS:CANCELLED` for 7 days. Output is RFC 5545: UTC
  times, escaped text, lines folded at 75 octets, CR/LF injection neutralized.

## Agents in meetings

Agents are attendees (`kind: "agent"`, validated against the organization's agents; the name always comes from
the directory). Agenda items can be owned by an attendee. `POST /meetings/{id}/brief` (or "brief the invited
agents" when starting) creates **one request with one preparation task per invited agent**: the agenda, the
participants and the points that agent owns, asking for a summary of at most 200 words and forbidding external
effects. It can be done once per meeting. The tasks run under the normal budget, approvals and Tool Gateway.

## Minutes

1. Input: pasted notes, a transcript (`.txt .md .vtt .srt`; WebVTT/SRT are cleaned to `Speaker: text`), or a drive
   file path (Word, PDF, text: the drive extracts the text; needs `files:read`). Limit 60 000 characters.
2. `ExtractMinutes` (deterministic, ES/EN/PT): headings ("Decisions", "Acuerdos", "Ações"...), labels
   (`Decision:`, `TODO:`, `[ ]`), phrases ("we decided", "decidimos"), `@owner` lines with a commitment verb, and
   first-person commitments in transcripts ("Ana: I will send the quote"). It extracts owner and `YYYY-MM-DD` due
   dates, caps items (50 × 300 characters) and ignores prose ("todo el equipo" is not a TODO).
3. The summary comes from `Orchestrator.Summarize` with `mode: "meeting"`, `topic`, `participants` and the notes
   as messages (the runtime wraps them as untrusted data). The model's answer is parsed with the same extractor
   and merged, so free prose can add items. If the runtime has no summarizer or fails, the runtime's own local
   fallback is ignored and `LocalSummary` (first sentences) is used. A budget refusal surfaces as
   `budget_exceeded` instead of being hidden.
4. Owners are matched with the attendees (exact name/id, first name, unique word match); an agent owner keeps
   `owner_kind: "agent"`.
5. The minutes (markdown, in the language of the request; third-party text is escaped) are saved to
   `<minutes_folder>/<date> <slug> (<id>).md` as the person who asked (a new version when regenerated). A drive
   failure is audited and leaves the minutes without a file.
6. A person can **edit** the minutes (`PUT /meetings/{id}/minutes`) before tasks exist. Once tasks were created
   the minutes are locked (409 `invalid_state`) so tasks are never duplicated or orphaned.
7. **Create tasks** (`POST .../minutes/tasks {indexes?}`): items owned by an invited agent go to that agent, the
   rest to the default agent (`assistant`) which tracks them for the human owner. The task text states it comes
   from meeting notes and must be verified. An item never becomes two tasks (`task_key`).

## Slash commands

| id | es | en | what |
|---|---|---|---|
| `room` | `sala`, `junta` | `room`, `huddle` | opens a meeting now in a free room that fits, with the named agents (`/sala ventas legal: revisar precios`); answers `navigate` to `/meetings?open=<id>` |
| `schedule` | `agendar`, `programar` | `schedule`, `book` | `navigate` to `/meetings?new=1&title=...&agents=a,b`: the dialog opens prefilled, the date is picked there in the browser's time zone (a trailing "tomorrow 10:00" / "mañana 10:00" in the topic is understood) |
| `minutes` | `acta`, `minuta` | `minutes` | minutes of the latest live/ended meeting from the text typed (or a drive path); `uses_llm: true`; does not create tasks |

`/meeting` (`/reunion`, agents discussing a topic) is unchanged. All three require `requests:create`.

## API

All under `/api/v1`. Errors are `{"error", "code"}` with the codes `not_found` (404), `invalid` (400),
`room_busy` / `invalid_state` / `budget_exceeded` / `reauth_required` / `read_only` / `blocked` (409),
`nothing_to_do` (422), `not_available` (501), `provider_error` (502).

```
GET    /meetings?from&to&status&room&mine=1        POST /meetings {title,start,end,room,provider,join_url,attendees,agenda,sync_calendar,start_now}
GET    /meetings/{id}   PATCH /meetings/{id}       POST /meetings/{id}/start|end|cancel|brief|sync
GET    /meetings/{id}/ics
GET    /meetings/{id}/minutes  POST (notes|file_path)  PUT (summary,decisions,action_items)  POST .../minutes/tasks {indexes}
GET    /meetings/rooms   GET|PUT /meetings/settings
GET    /meetings/calendars   GET /meetings/calendar-events?from&to   POST /meetings/free-busy
GET|POST /meetings/feeds   DELETE /meetings/feeds/{id}
GET    /calendar/feed/{token}.ics                   (public)
```

## Storage (migrations 740, 741; down files in `backend/migrations/down`)

`meetings` (attendees, agenda and external as jsonb, `CHECK (ends_at > starts_at)`, status check), `meeting_minutes`
(foreign key to `meetings`), `meeting_settings`, `calendar_feeds` (unique `(org_id, token_hash)`). All have RLS
(`tenant_isolation` on `app_current_org()`), the application role gets no `DELETE` on settings and feeds.
`tenantdata` discovers the tables on its own (export, purge); `token_hash` is redacted from the export by the
existing column-name rule. The up files live with the other embedded migrations in
`backend/internal/infrastructure/postgres/migrations`.

## UI (`/meetings`)

Tabs **Meetings** (grouped by day, filters upcoming / live / past / all, "only mine", the connected calendar's
events dimmed in between), **Rooms** (who is in each room now, next meeting, open a room), **Calendar**
(connections, defaults, rooms, ICS subscription with copy / Google / Outlook-Apple links). The meeting view has the
video panel (embedded Jitsi, or an "open in a new tab" button), start/end/cancel/edit, agenda, attendees, the
calendar sync state and the **Minutes** tab (paste, upload, drive path, edit, create tasks). Deep links:
`?open=<id>&tab=minutes`, `?new=1&title=&agents=`. Strings are in `meetings.*` (es/en/pt).

## Tests

* `internal/meetings`: state machine table, concurrent start, rooms, validation, links, Jitsi base, calendar sync
  (Meet/Teams, failures, reschedule, cancel), free/busy slots, briefing, minutes (extraction in three languages,
  transcripts, captions, no-model and failing-runtime fallbacks, budget refusal, injection, tasks idempotency,
  edit/lock), feeds (token, revoke, isolation) and ICS.
* `internal/calendar`: Google and Microsoft clients against `httptest` fakes, ICS generation, free slots.
* `internal/connections/meetingcal`: Microsoft OAuth flow with a bring-your-own app, manifests.
* `internal/api`: RBAC, lifecycle, minutes + tasks, calendar sync, free/busy, ICS and public feed, slash commands.
* `internal/infrastructure/postgres` (needs `TEST_DATABASE_URL`): RLS, compare-and-set, feeds, rollbacks.
* `frontend/src/components/meetings/logic.test.mjs` (`node --test`).

## Not done / next

* Incoming calendar changes are not imported (we only read events to show them dimmed); no webhooks/subscriptions.
* Recurring meetings, per-attendee RSVP and room resources in Google/Microsoft calendars.
* Zoom link generation (Zoom has no connector yet: links are pasted).
* A person directory for attendees (people are added by e-mail or name; there is no members list API yet).
* Live captions or recording: out of scope (no media stack).
