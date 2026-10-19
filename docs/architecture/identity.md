# Enterprise identity: SSO, verified domains, MFA policy, SCIM

Status: implemented (OIDC, domains, TOTP policy, SCIM). SAML is a documented follow-up (section 9).
Code: `backend/internal/sso`, `backend/internal/scim`, `backend/internal/auth` (`identity.go`, `mfa.go`),
migrations 700-703, UI in Settings, Single sign-on and identity (`frontend/src/components/settings/Identity*.tsx`) and the login page.

Design decision: **SSO is available on every plan.** There is no plan check anywhere in this code.

## 1. Pieces

| Piece | Where | What it does |
| --- | --- | --- |
| OIDC SSO | `internal/sso` | Authorization code flow with PKCE (S256), state, nonce; generic provider (Okta, Entra ID, Google Workspace, any OpenID Connect) |
| Verified domains | `internal/sso/domains.go` | DNS TXT challenge. SSO routing and just-in-time account creation only work for verified domains |
| Sign-in policy | `sso.Service.OrgPolicy` -> `auth.PolicyProvider` | per organization: require MFA, enforce SSO |
| TOTP MFA | `internal/auth/mfa.go`, `totp.go`, `identity.go` | enrolment (QR + secret), recovery codes, replay protection, forced enrolment when the policy requires it |
| SCIM 2.0 | `internal/scim` | Users and Groups, one bearer token per organization |

## 2. OIDC flow

```
browser            app API                         identity provider
  | POST /sso/discover {email} -> {sso: bool}        (yes/no only: never names the organization)
  | POST /sso/start    {email} --> state, nonce, PKCE verifier saved (single use, 10 min)
  |<-- authorization URL ------------------------------>|
  |                    GET /sso/callback?code&state <----|  (browser redirect)
  |                    state consumed (DELETE ... RETURNING), code redeemed at the token endpoint
  |                    id_token verified, domain checked -> exchange code saved (single use, 60 s)
  |<-- 302 /login?sso_code=...  (never a token, never a cookie)
  | POST /auth/sso/exchange {code} -> session (refresh token in the HttpOnly cookie, like every other login)
```

Why an exchange code: the refresh cookie is scoped to `/api/v1/auth` and CSRF-guarded; a session never travels in a URL;
the code is useless without the browser's own POST and lives 60 seconds.

### ID token validation (`sso.VerifyIDToken`, standard library only)

* Algorithm allow-list `RS256` and `ES256`. `none` and every HMAC algorithm are refused (so the client secret, or the public
  key used as an HMAC key, can never validate a token). `crit` headers are refused.
* Signature checked against the issuer's JWKS (cached 1 h; one forced refresh per minute when a `kid` is unknown, so key
  rotation works and a forged `kid` cannot make the server hammer the provider). RSA keys under 2048 bits are ignored.
* `iss` equals the configured issuer, which discovery confirmed (`issuer` in the metadata must equal the URL it was fetched from).
* `aud` contains the client id; with several audiences or an `azp`, `azp` must equal the client id.
* `exp`, `iat`, `nbf` with a 2 minute skew. `nonce` must equal the one saved for this login (constant-time compare).
* `email` required; `email_verified=false` is always refused; an absent claim is refused unless the organization opts in
  (Microsoft Entra ID never sends it).
* The e-mail domain must be **verified by this same organization** (see 3). An IdP cannot sign a person into an organization
  that does not own their address.

### State, nonce, replay

* `state`, the PKCE verifier and the nonce are random 256/384/192-bit values, stored (state hashed) in `sso_pending`, redeemed
  with a single `DELETE ... RETURNING`: a second callback with the same state finds nothing, even concurrently.
  A failed token exchange still burns the state (no retry with the same state).
* The exchange code is also single use and hashed at rest; `Exchange` re-reads the live configuration, so removing SSO within
  the 60 seconds cancels pending sign-ins, and a SCIM-deactivated account is vetoed by the gate (`scim.Service.Deactivated`).

### Secrets and SSRF

* The client secret is sealed with `vault.Seal` (AES-256-GCM under the organization's data key, wrapped by the KEK
  `CONNECTIONS_KEK`), stored as ciphertext in `org_sso_configs.sealed_secret`, never returned by the API (only `has_secret`),
  and unsealed only to call the token endpoint. Without a KEK the secret is **refused**, not stored in clear. Leaving the
  field empty on save keeps the stored secret. PKCE is used regardless, so a provider that issues public clients also works
  (empty secret).
* Provider URLs come from administrators, so the HTTP client (`NewSafeClient`) refuses loopback, private, link-local, CGNAT and
  multicast addresses at **dial time** (immune to DNS rebinding between validation and connection), follows no redirects,
  and caps time and size. Issuers must be `https`.

## 3. Verified domains

`POST /identity/domains {domain}` returns a record to publish:

```
_aiworkforce-challenge.example.com   TXT   "aiworkforce-verification=<token>"
```

`POST /identity/domains/{domain}/verify` resolves the TXT record and marks the domain verified. Rules:

* A domain is verified by **one organization at a time** (primary key on `verified_domains.domain`); the second claim gets
  `409 domain_taken` even with a correct record.
* Public mailbox domains (gmail.com, outlook.com, icloud.com, ...), IP addresses, single labels and reserved TLDs are refused.
* The last verified domain cannot be removed while SSO is enforced.
* `verified_domains` (domain -> org id) is the only pre-tenant lookup; it holds no secret.

Auto-join: with `jit` on, SSO sign-in creates the account and membership (role = configured default, never owner) but only for
an address on a verified domain.

Auto-join by domain (`GET|PUT /identity/auto-join {enabled, role}`, `org_auto_join`, migration 780): off by default; the role is
`admin`, `member` or `viewer` (default viewer, never owner) and turning it on needs a verified domain. Password sign-ups join
only when they **open the verification link** (`auth.Service.Verify` -> `AutoJoiner`), never at registration, so a typed address
proves nothing; SCIM-deprovisioned accounts stay out. For SSO the setting also acts as JIT when the SSO config has `jit` off.

Re-check (migration 781, `sso.Janitor`, hourly sweep started from `cmd/server`): the DNS challenge record of every verified domain
is re-read at most once per 24 h. A missing record sets `lapsed_since` (grace period 7 days, the domain keeps working); a
record that stays gone revokes the verification (it leaves `verified_domains`, so SSO routing and auto-join stop). A DNS failure
(timeout, SERVFAIL) is not a lapse. The same sweep calls `PurgeExpired` on abandoned login records.

## 4. Sign-in policy

`org_identity_policy`: `require_mfa`, `enforce_sso`. `auth.Service` asks `Config.Policy` (nil = no policy) at password login and org switch; an
unreadable policy fails closed.

* **Require MFA.** A password login without a second factor gets no session: `200 {mfa_enrollment_required, mfa_token}`.
  The token is purpose-bound (MAC covers the purpose; it cannot be used as an MFA challenge and vice versa), 5 minutes, and only
  opens `POST /auth/mfa/enroll/setup` and `/enroll/enable`; the session opens when the first code is confirmed and the recovery
  codes are shown once. Switching to an organization that requires MFA without having it is refused; MFA cannot be disabled
  while any of the user's organizations requires it (`mfa_required_by_org`). SSO sign-ins are exempt: the IdP owns MFA.
* **Enforce SSO.** Password login is refused (`sso_required`) for members. **Owners keep the password** as break-glass, so a broken
  IdP configuration cannot lock the organization out. Enforcement is only accepted when SSO is enabled and a domain is verified;
  removing SSO lifts it.
* Both need `org:manage` (owner and admin) and are recorded in the audit log.

## 5. TOTP

RFC 6238, HMAC-SHA1, 6 digits, 30 s. Window of +-1 step (clock drift); a used step, and every earlier one, is rejected
(`UseMFAStep`); wrong codes count toward the same lockout as wrong passwords; 8 single-use recovery codes stored as SHA-256;
the secret is AES-256-GCM encrypted at rest and bound to the user id; QR/secret come from `otpauth://` (UI: `AccountSecurity`,
`MfaEnroll`).

## 6. SCIM 2.0 (`/api/v1/scim/v2`)

* Auth: `Authorization: Bearer scim_...`, one token per organization, stored as SHA-256, shown once on creation/rotation,
  replaced on rotation (the old one stops at once), revocable. Administered from Settings (`/identity/scim`).
* Endpoints: `ServiceProviderConfig`, `ResourceTypes`, `Schemas`, Users (`GET` with `filter=userName eq "..."`, `POST`, `GET/PUT/PATCH/DELETE {id}`),
  Groups (`GET`, `POST`, `GET/PUT/PATCH/DELETE {id}`). Okta and Entra PATCH shapes (including `"False"` strings) are handled.
* Users: `userName` = e-mail, which must be on a domain **verified by the organization**. Create adds the membership with the
  role from `roles` (`admin`, `member`, `viewer`; default member; `owner` falls back to member). `active=false` or `DELETE`
  removes the membership and revokes the user's sessions in that organization at once; the record is kept so the account can be
  reactivated and SSO just-in-time creation cannot bring it back.
* Blast radius: SCIM only sees and changes users in `scim_users` (those it created). It never touches an owner, never changes a
  user's profile, never deletes the global account, and every query is scoped to the token's organization.
* Groups are the three assignable roles (`admin`, `member`, `viewer`); membership of a group = holding the role. Removing someone
  from a group demotes to `viewer` (least privilege). Groups cannot be created with other names or deleted.

## 7. Data and migrations (700-703, 780-781)

| Table | RLS | Notes |
| --- | --- | --- |
| `org_sso_configs`, `org_identity_policy`, `org_domains`, `scim_users` | yes (`tenant_isolation`, as 202) | all access via `auth.WithOrgTx` |
| `verified_domains`, `sso_pending`, `scim_tokens` | no, by design | read before the tenant is known (domain routing, callback, bearer token). They hold no secret: domain -> org id, hashed single-use values, token hash. Written only by `internal/sso` and `internal/scim` |

Down migrations: `backend/migrations/down/700..703_*_down.sql`, `780`, `781`. `org_auto_join` has RLS (`tenant_isolation`).

## 8. Operations

* `PUBLIC_API_URL` (e.g. `https://app.example.com/api/v1`) or `UI_BASE_URL` build the redirect URL; `SSO_REDIRECT_URL` overrides it.
  Register `<PUBLIC_API_URL>/sso/callback` at the IdP; the UI shows it with a copy button. SCIM base URL: `<PUBLIC_API_URL>/scim/v2`.
* `CONNECTIONS_KEK` must be set to store a client secret.
* Entra ID: use the tenant-specific issuer `https://login.microsoftonline.com/<tenant-id>/v2.0` and turn on "accept tokens without
  the verified-email claim". The multi-tenant `common` issuer is not supported (its issuer differs per tenant).
* Rate limits: discover 30 / start 20 / callback 60 per 15 min per IP, exchange as login, SCIM 1200 / min per IP.

## 9. Not done / follow-ups

* **SAML 2.0.** Not implemented: a correct SAML service provider needs XML canonicalization and XML-DSig verification, which
  the standard library does not provide, and a hand-rolled one is the classic source of signature-wrapping vulnerabilities.
  Plan: add a vetted library (e.g. `crewjam/saml`) behind the same `Identity` seam (`auth.FederatedIdentity`) so everything after
  the assertion (domain check, JIT, gate, exchange code, policy) is reused.
* Back-channel logout / session revocation from the IdP, SCIM bulk and sorting, `/Me`.
* Integration tests against a real Postgres for migrations 700-703 (unit tests use the in-memory stores).
