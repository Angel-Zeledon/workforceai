# Agent web access (capability 4: `web`)

Agents holding the `web` tool can search the web (`web.search`) and read public pages (`web.fetch`). Code:
`backend/internal/webtool/` (guard, fetcher, extractor, providers, service), `application/web_flow.go` (the flow in
the orchestrator), `api/web.go` (`GET/PUT /api/v1/settings/web`), `agent-runtime/app/security.py` (`web_lines`),
`frontend/src/components/security/WebToolCard.tsx`. Migration `685_web_tool.sql` (`web_settings`, `web_calls`).

## How it works

* A web request is a **read** (like a connection read): the runtime emits `tool_requests` `{tool:"web", action:"search"|"fetch", args}`;
  the orchestrator serves it between runtime turns (`webReads`, called at the start of `connectionReads`), hands the
  result back in a follow-up runtime call as `external_content` (`<untrusted_data>` block) and marks the task **tainted**.
  A web request that reaches the action loop is never executed (audited as `tool.read_skipped`).
* Gates, in order: tool wired, agent holds the `web` grant, kill switch / pause (`Admit`), per-tool kill switch,
  read-only mode (also denies web: nothing leaves the company), policy deny rules (an org rule that asks for approval
  fails closed, reads have no approval), org setting `enabled`, input hygiene, domain policy, quotas, then the call.
* Risk is **low**: no approval when enabled. Every call is audited (`web.search` / `web.fetch`: query or URL only,
  never content) and recorded in `web_calls` (quota ledger). A search costs `WEB_SEARCH_COST_USD` (default 0.005) in the budget
  (usage kind `web`).
* Default OFF per organization. Default grant only to `analyst` and `data_analyst` role templates; editable like other tool grants.

## Settings and env

Org setting `web` (owner/admin, audited as `web.settings.updated`):
`{enabled, mode: "open"|"allowlist", allowed_domains[], blocked_domains[], max_calls_per_task, max_calls_per_day}`.
Blocked wins over allowed; subdomains are included; the mode defaults to `allowlist`. The organization can only go
lower than the platform ceiling.

| Env | Meaning |
|---|---|
| `WEB_SEARCH_PROVIDER` | `brave` (recommended) / `tavily` / `searxng`. Unset = search unavailable (the agent is told plainly; fetch still works). |
| `WEB_SEARCH_API_KEY`, `WEB_SEARCH_URL` | Provider key; base URL of the SearXNG instance (its `json` format must be enabled). |
| `WEB_SEARCH_COST_USD` | Budget cost per search (0.005). |
| `WEB_MAX_CALLS_PER_DAY` | Platform ceiling of calls per day per organization (200). |
| `WEB_ALLOWED_HOSTS` | Optional operator allow-list: when set, only these domains (and subdomains) are reachable by anyone. |
| `WEB_ALLOWED_PORTS` | Allowed destination ports (default 80,443). |

Brave: `GET https://api.search.brave.com/res/v1/web/search?q=&count=` with header `X-Subscription-Token`; results in `web.results[]`
(`title`, `url`, `description`, `age`/`page_age`). Tavily: `POST https://api.tavily.com/search`, Bearer key. Results are capped at 8, only http(s) URLs.

## Threat model

### 1. SSRF (an agent, or a page steering it, makes the server call internal systems)

Mitigated in `netguard.go` / `fetch.go` (tests: `netguard_test.go`, a long list of hostile inputs):

* Only `http`/`https`; ports 80/443 (configurable); no credentials in the URL; no controls/spaces; URL length <= 2048; fragments dropped.
* Hosts are normalized (lowercase, trailing dot stripped, IDNA to punycode, full-width digits folded). Rejected: IP spellings other than
  canonical dotted/IPv6 (decimal `2130706433`, octal `0177.0.0.1`, hex `0x7f.1`, `127.1`: any host whose last label is numeric),
  single-label names, `localhost`, `*.localhost/.local/.internal/.lan/.home.arpa`, `metadata.google.internal`, zone ids.
* **We resolve DNS ourselves** and every answer must be public: loopback, RFC1918, link-local (incl. `169.254.169.254`), CGNAT
  `100.64/10`, `0/8`, multicast, reserved/documentation ranges, `240/4`, Alibaba/Oracle metadata IPs, IPv6 `::`, `::1`, ULA `fc00::/7`
  (AWS IPv6 metadata), `fe80::/10`, NAT64, 6to4, Teredo, IPv4-compatible, and IPv4-mapped (`::ffff:127.0.0.1` is judged as `127.0.0.1`).
  One private record in an answer rejects the host.
* **Pinned connection**: resolution happens once, inside the transport's `DialContext`, and the socket is opened to the validated IP
  (no second lookup, so no DNS rebinding window). A `Dialer.Control` hook re-checks the final socket address and port as a last line of defense.
  No environment proxy is used.
* Redirects are followed manually, max 3; **each hop** re-runs URL, scheme, port, credentials, domain policy and resolution.
* Only `User-Agent`, `Accept`, `Accept-Language` are sent: no cookies, no `Authorization`, no `Referer`, no cookie jar.
* Hard timeouts (dial 5s, TLS 8s, headers 10s, total 20s), response header cap 64 KB, body cap 2 MB **after decompression**
  (a gzip bomb stops at the cap), content-type allow-list (html, xhtml, text, markdown, csv, json, xml, rss/atom, `+json`/`+xml`;
  sniffed when absent). PDF is a typed refusal (`pdf_not_supported_yet`). Errors are mapped to stable codes; raw network errors never reach the agent.

### 2. Prompt injection from pages

* Text extraction never executes JS and drops scripts, styles, comments, `noscript`, templates, iframes, `hidden`/`aria-hidden`/`display:none`/
  `visibility:hidden`/zero-size elements, hidden inputs, zero-width and bidi characters. Links render as `text (url)` (http(s) only, no credentials).
* The output passes secret redaction (existing patterns plus hyphenated provider keys), delimiter neutralization and the injection scorer, and is
  wrapped as `<untrusted_data source=URL>` with the fetch time and a sha256 of the text. A flagged page gets a warning line.
* The task becomes tainted (the gateway's existing taint rules apply to later side-effect actions). The runtime prompt tells the model to treat pages as
  data, never follow their instructions, and cite the URL for every claim.
* Residual: a model can still be persuaded by text it reads; the real barriers are the downstream controls (grants, approvals for side effects, taint), not the filter.

### 3. Data exfiltration through queries and URLs

* Queries are refused if they contain a secret pattern or personal data (email, phone, id/account numbers), max 300 chars; URLs are refused if they carry a
  token/secret pattern. Refused calls never leave the server and are audited as denied.
* Every query and URL is audited and counted; the organization can restrict to an allow-list of domains (then arbitrary attacker-controlled hosts cannot be
  reached, which also closes "encode data in the URL" for unlisted domains).
* Residual: in `open` mode an agent can still encode small amounts of non-pattern data in a query or path on a public site. Use `allowlist` for sensitive organizations.
  Search providers see the queries (third-party processing).

### 4. Cost abuse

* Off by default; per-task and per-day call limits (org value under the platform ceiling `WEB_MAX_CALLS_PER_DAY`); max 4 calls per round, 3 rounds per task turn;
  searches are charged to the budget (and so to request/agent caps); kill switch, pause and read-only mode stop all calls.
* Residual: the quota check and the ledger insert are two statements, so concurrent tasks of one organization can overshoot a limit by a few calls.
  Fetch has no per-byte charge (bounded by 2 MB and the call limits).

## Not covered / future

PDF extraction, JavaScript-rendered pages, authenticated pages, robots.txt handling (the fetcher identifies itself as `AIWorkforceBot`, GET only),
per-agent (not per-role) grants UI. No new dependencies beyond promoting `golang.org/x/net` (BSD-3-Clause) from indirect to direct.
