# Security Design

## Security objectives

Orchestra protects administrative workflow capabilities, locally stored identities,
provider and connector credentials, workflow inputs/outputs, and external webhook
controls. The design assumes TLS terminates at Orchestra or a trusted reverse proxy and
that the database is inside the trusted deployment boundary.

The authorization model is deny-by-default. UI visibility is a convenience; backend
middleware and service object checks are authoritative.

## Principals

| Principal | Authentication | Intended surface |
|---|---|---|
| Local user | Opaque server-side session | `/api` and UI |
| API key | Bearer secret | `/ext` workflow operations |
| Anonymous | Only external audit migration mode | `/ext` |
| System | Internal attribution | Maintenance and system actions |

User sessions receive a complete effective permission set. API keys do not receive the
user permission catalog; they are authorized through workflow/action grants.

## Initial administrator bootstrap

On controller startup, the auth service creates an `admin` user only when no users
exist. Bootstrap uses an in-process mutex and, on PostgreSQL, an advisory lock to avoid
creating multiple initial administrators in a shared deployment.

Credentials come from configured deployment secret inputs when present. Otherwise a
random temporary password is written once to `auth.bootstrapOutputPath` with protected
permissions. The account must change its password at first login. The password must not
be logged or returned from a public endpoint.

Offline recovery uses:

```text
orchestra users reset-password USERNAME --password-file FILE
```

Recovery revokes active sessions and requires another password change.

## Passwords and login defense

Passwords are 12-128 Unicode characters, cannot be blank or from the small built-in
common-password denylist, and are hashed with Argon2id using a random 16-byte salt.
Stored hashes encode parameters and are rehashed after successful login when parameters
change.

Login normalizes usernames, uses a generic invalid-credential response, records failed
attempts, applies user/IP rate limits, and stores lockout state. Unknown-user processing
performs defensive hash work to reduce username timing signals. Login events are
audited.

## Browser sessions

The browser receives an opaque random token in the `orchestra_session` cookie. Only a
hash is stored in `sessions`. Cookie attributes are:

- `HttpOnly`;
- `SameSite=Lax`;
- path `/`;
- `Secure` according to `auth.cookieSecure` and deployment URL/TLS configuration.

Sessions enforce idle and absolute expiry. Authentication rejects revoked sessions,
disabled users, expired sessions, password-version mismatch, and authorization-version
mismatch. Valid use advances idle expiry and last-seen state.

Password changes revoke other sessions and issue a fresh current session. Role,
status, or entitlement changes invalidate existing sessions through `authz_version`.

## CSRF and origin checks

Every session has an independent CSRF token returned in the session JSON, not in a
readable cookie. The frontend sends it as `X-CSRF-Token` on POST, PUT, PATCH, and DELETE.
Middleware compares it in constant time and also verifies the exact Origin or Referer
against `app.url`. Development may additionally allow `ui.devProxyURL`.

Login has no existing session token, so it performs origin validation directly.
Requests missing both Origin and Referer fail for unsafe browser operations.

## Roles and permissions

Built-in roles are:

- `admin`: every permission, including users, entitlements, all keys, audit, settings,
  and restart;
- `developer`: workflow/resource authoring and execution, operations, cluster control,
  settings read, owned API keys, and own sessions;
- `observer`: read-only dashboard, workflows, tasks, resources, operations, cluster,
  settings, and own sessions.

Permission families are:

| Family | Permissions |
|---|---|
| Dashboard | `dashboard.read` |
| Definitions | `workflow.definition.read/write/publish` |
| Runs | `workflow.run.read/start/control` |
| Tasks | `workflow.task.read/control` |
| Resources and AI | `resource.read/write`, `ai.use`, `import.analyze/apply` |
| Operations and cluster | `operation.read`, `cluster.read/control` |
| Settings | `settings.read/write`, `server.restart` |
| Identity | `user.read/manage`, `entitlement.manage` |
| API keys | `api_key.read/create/manage_own/manage_all` |
| Audit and sessions | `audit.read`, `session.manage_own/manage_all` |

Per-user entitlements add `allow` or `deny` overrides. Denies are applied after allows,
so deny always wins. Disabled users receive no effective permissions.

The user service prevents changes that would leave the installation without an active
user manager. API key access is additionally restricted by ownership unless the actor
has `api_key.manage_all`.

## Forced password change

A user with `mustChangePassword` may access current session, logout, and password change
routes. Permission middleware blocks other protected operations with a stable error.
The frontend routes that user to `/change-password`.

## API key security

Keys have the form `orch_<12-hex-prefix>_<64-hex-secret>`. The prefix locates a record;
only a hash of the secret portion is stored. Comparison is constant-time. Secrets are
shown only at creation or rotation.

Authentication checks syntax, hash, active status, expiry, and a persisted rate-limit
bucket. Usage time/IP writes are coalesced by `usageWriteWindow`.

Every key requires at least one grant. A grant specifies:

- workflow definition ID;
- action: `start`, `signal`, `status.read`, or `result.read`;
- instance scope: `own` or `definition`;
- whether pinned versions are allowed;
- whether callback URLs are allowed;
- optional allowed signal names.

Rotation creates a new key with copied grants and revokes the old key atomically.
Revocation is irreversible through the public service.

## Audit

Unsafe authenticated API calls generate request audit records with actor, method, path,
status, source, and outcome. Authentication, authorization denial, user administration,
API key lifecycle, and external workflow operations add domain-specific records.

Audit retention cleanup runs on controller startup and hourly. Audit records are
security data: access requires `audit.read`, and metadata must not include raw passwords,
session tokens, API key secrets, or provider credentials.

## HTTP hardening

The server emits:

- `X-Content-Type-Options: nosniff`;
- `X-Frame-Options: DENY`;
- same-origin referrer policy;
- a restrictive permissions policy;
- HSTS when the direct request uses TLS;
- no-store caching for `/api` and `/ext`;
- a production CSP limited to self, data fonts/images, WebSocket connections, and blob
  workers, with inline styles allowed for the UI toolchain.

Request bodies are capped at 4 MiB and headers at 1 MiB. Panics are recovered without
returning stack traces. Reverse-proxy headers are trusted only from configured CIDRs;
the rightmost untrusted address is selected as the client.

## Outbound request boundaries

- External workflow callback URLs must match configured regular expressions and the API
  key grant must allow callbacks.
- `http-request` validates target URLs and restricts response body size.
- AI and MCP credentials are server-side configuration/database data and are not exposed
  through public metadata.
- The raw config endpoint is permission-protected and mounted only in all-in-one mode,
  but it can contain secrets and should be disabled operationally by separating roles.

## Threat-driven requirements

| Threat | Control |
|---|---|
| Credential database theft | Argon2id passwords; hashed session/API-key secrets |
| CSRF | SameSite cookie, session token header, exact origin check |
| Privilege escalation | Route permissions, deny overrides, service ownership checks |
| API key overreach | Workflow/action grants, instance scope, signal restrictions |
| Brute force/abuse | Login lock/rate state and API key rate buckets |
| Clickjacking/XSS impact | Frame denial, CSP, no-sniff, no-store |
| Proxy IP spoofing | Explicit trusted proxy CIDRs |
| Workflow enumeration | Not-found concealment for unauthorized scoped key access |
| Lost administrator access | Offline password recovery with session revocation |

## Security limitations

- Identity is local only; no MFA, OIDC, SAML, or external group synchronization exists.
- Database contents include workflow data and potentially sensitive MCP headers.
- Audit events share the primary database and are not tamper-evident or exported to a
  separate security system by the application.
- Rate limiting is database-backed but currently uses a fixed-window count plus burst,
  not a distributed token bucket.
- TLS certificate management is outside the application.
