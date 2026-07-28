# Authentication and Authorization Plan

Status: Proposed implementation plan. No authentication code is implemented by this document.

## 1. Executive Summary

Orchestra currently exposes its control-plane API, WebSocket event stream, administrative operations, and external webhook endpoints without an identity boundary. This plan adds a locally stored user system, secure browser sessions, role-based access control with per-user entitlement overrides, an administration UI, automatic initial-admin creation, and workflow-scoped API keys for external webhook callers.

The recommended design is:

- Store users, sessions, entitlement overrides, API keys, API-key workflow grants, and audit events in the same SQLite or PostgreSQL database as workflow state.
- Authenticate UI users with username and password, using Argon2id password hashes and opaque server-side sessions in an HttpOnly cookie.
- Keep three built-in roles: `admin`, `developer`, and `observer`.
- Treat roles as understandable default permission bundles and entitlement overrides as exceptional per-user allow/deny rules.
- Enforce permissions in backend middleware and object-level service checks. UI permission gates are usability features, not security boundaries.
- Auto-create one initial `admin` user when the user table is empty. Use an injected bootstrap password when available; otherwise generate a strong temporary password and place it in a mode `0600` bootstrap file.
- Replace anonymous `/ext/*` access with high-entropy API keys. Each key receives explicit grants for selected workflow definitions and selected actions.
- Store only hashes of session tokens and API-key secrets. Display an API-key secret only once, when it is created or rotated.
- Add a dedicated security audit trail. Existing workflow events remain workflow history and are not used as the security audit log.

This should be delivered as several reviewable pull requests rather than one large security change.

## 2. Goals

1. Require a known, active user for access to the Orchestra UI and `/api/*` control-plane endpoints.
2. Store user accounts internally without requiring an external identity provider.
3. Support user creation, disabling, role assignment, password reset, and entitlement management through the UI.
4. Automatically create the first administrator without leaving a public first-user registration race.
5. Provide clear built-in roles:
   - `admin`: full application administration, including user management.
   - `developer`: workflow and integration development plus operational control, without user or server administration.
   - `observer`: read-only access to operational and design data.
6. Issue, rotate, revoke, and expire API keys used only by external webhook endpoints.
7. Bind every API key to explicit workflow definitions and webhook actions.
8. Make authorization deny by default and test every protected route for every built-in role.
9. Preserve SQLite single-node and PostgreSQL multi-controller deployments.
10. Produce attributable audit records for authentication, authorization, administration, and external workflow control.

## 3. Non-Goals for the First Delivery

- Public self-registration.
- Email verification or email-based password recovery.
- OAuth, OIDC, SAML, LDAP, or social login.
- Multi-factor authentication. The schema and UI should leave room for it, but MFA is a follow-up.
- User-defined roles. Fixed roles plus per-user entitlement overrides cover the initial requirement with less policy complexity.
- Multi-tenant isolation or per-team ownership.
- API keys for the browser control-plane `/api/*`. API keys are accepted only by `/ext/*`.
- Long-lived JWT access or refresh tokens.
- Storing recoverable passwords or API-key secrets.
- Worker-to-controller authentication. That should be coordinated with the separate worker gRPC design.

## 4. Current-State Findings

The implementation must account for these existing behaviors:

- `internal/server/server.go` mounts `/api`, `/ext`, and the SPA without authentication middleware.
- `internal/api/router.go` mixes public metadata, administrative actions, reads, mutations, and the WebSocket route in one router.
- `internal/api/v1handler.go` explicitly exposes workflow start, signal, status, and result operations anonymously.
- `internal/api/handler_stream.go` accepts WebSockets with origin verification disabled.
- `internal/api/handler_admin.go` allows config-file updates and process restarts without an authenticated administrator.
- `ui/src/services/api.ts` performs direct `fetch` calls without a shared credentials, CSRF, or unauthorized-response policy.
- `ui/src/App.tsx` and `ui/src/components/Layout.tsx` have no login, protected route, current-user state, or permission-aware navigation.
- The workflow service owns the database connection and SQL dialect internally. Authentication should not create a second independent SQLite pool against the same file.
- SQLite schema initialization is automatic. PostgreSQL schema application is intentionally manual through `orchestra schema --create`.
- Workflow events describe runtime state changes but do not consistently record the requesting user, API key, IP address, outcome, or authorization failures.

The most sensitive existing endpoints are config read/write, server restart, connector headers, AI-assisted operations, workflow and task control, imports, and external result retrieval. They need explicit permissions rather than a broad "logged in" check.

## 5. Security Model

### 5.1 Principals

Every protected request is associated with one of these principal types:

| Principal type | Authentication mechanism | Accepted surface |
| --- | --- | --- |
| `user` | Opaque server-side session cookie | `/api/*`, `/api/ws`, UI |
| `api_key` | `Authorization: Bearer <key>` | `/ext/*` only |
| `system` | Internal process context | Worker/runtime operations only |
| `anonymous` | No credentials | Liveness, public app metadata, login assets |

Do not accept API keys on `/api/*`, and do not accept browser session cookies as a substitute for an API key on `/ext/*`. Keeping the mechanisms on separate surfaces limits accidental privilege expansion.

### 5.2 Authentication Versus Authorization

- Authentication answers who is calling.
- Function-level authorization answers whether that principal may invoke an operation.
- Object-level authorization answers whether that principal may invoke the operation on the selected workflow, run, user, or API key.
- State validation answers whether the operation is valid for the resource's current workflow state.

All four checks remain server-side. A hidden button or a guessed UUID is never considered a control.

### 5.3 Deny-by-Default Rule

Every route must be registered in exactly one category:

1. Public and explicitly documented.
2. Authenticated user plus a named permission.
3. Authenticated API key plus a named workflow grant.

A route with no category must fail closed during development tests and must not be mounted in production.

## 6. Role and Entitlement Design

### 6.1 Permission Catalog

Use stable permission strings in backend code, API responses, tests, and the UI. Avoid deriving permissions from HTTP methods at runtime.

| Permission | Allows |
| --- | --- |
| `dashboard.read` | Dashboard summaries and health details |
| `workflow.definition.read` | List/read/export definitions and versions |
| `workflow.definition.write` | Create definitions and draft versions |
| `workflow.definition.publish` | Publish and activate versions |
| `workflow.run.read` | List/read runs, histories, signals, and results |
| `workflow.run.start` | Start a workflow from the control-plane UI/API |
| `workflow.run.control` | Cancel runs and send signals |
| `workflow.task.read` | List queues and task state |
| `workflow.task.control` | Retry, requeue, pause, resume, and cancel tasks |
| `resource.read` | Read/export scripts, schemas, agents, and connectors |
| `resource.write` | Create/update/delete scripts, schemas, agents, and connectors |
| `ai.use` | Prompt enhancement, script assistance, and other provider-backed operations |
| `import.analyze` | Analyze an import bundle without applying it |
| `import.apply` | Apply an import bundle and overwrite approved resources |
| `operation.read` | Read workflow operations and live events |
| `cluster.read` | View node inventory and node health |
| `cluster.control` | Trigger active node health checks or future node controls |
| `settings.read` | Read non-secret application and build settings |
| `settings.write` | Edit the active config file |
| `server.restart` | Request an application restart |
| `user.read` | List users and view account metadata |
| `user.manage` | Create, enable, disable, rename, reset, and assign roles |
| `entitlement.manage` | Add or remove per-user entitlement overrides |
| `api_key.read` | List API-key metadata and grants, never secret values |
| `api_key.create` | Create workflow-scoped API keys |
| `api_key.manage_own` | Update, rotate, or revoke keys created by the current user |
| `api_key.manage_all` | Update, rotate, or revoke any API key |
| `audit.read` | Read security audit events |
| `session.manage_own` | View and revoke the current user's sessions |
| `session.manage_all` | Revoke another user's active sessions |

Permission names should be constants in Go and a generated or API-provided catalog in TypeScript. Do not maintain two hand-written catalogs that can drift.

### 6.2 Built-In Role Matrix

`admin` includes every permission. The detailed matrix below makes the intended developer and observer boundaries explicit.

| Capability | Admin | Developer | Observer |
| --- | :---: | :---: | :---: |
| Read dashboards, workflows, runs, tasks, resources, operations, cluster | Yes | Yes | Yes |
| Receive authenticated live events | Yes | Yes | Yes |
| Create/edit workflow definitions and resources | Yes | Yes | No |
| Publish/activate workflow versions | Yes | Yes | No |
| Start/cancel/signal workflows | Yes | Yes | No |
| Retry/requeue/pause/resume/cancel tasks | Yes | Yes | No |
| Use AI assistance and connector exploration | Yes | Yes | No |
| Analyze/apply imports | Yes | Yes | No |
| Run cluster health checks | Yes | Yes | No |
| Read non-secret settings/build metadata | Yes | Yes | Yes |
| Edit raw config or restart server | Yes | No | No |
| List and manage users | Yes | No | No |
| Manage entitlement overrides | Yes | No | No |
| Create workflow-scoped API keys | Yes | Yes | No |
| Manage own API keys | Yes | Yes | No |
| Manage all API keys | Yes | No | No |
| Read security audit log | Yes | No | No |
| Revoke all users' sessions | Yes | No | No |

An observer is read-only, but read access can still reveal workflow inputs, outputs, errors, scripts, connector metadata, and operational details. Deployments that need data-level confidentiality between groups will require a later resource-scope or tenant model.

### 6.3 Per-User Entitlement Overrides

Each user has one built-in role and zero or more overrides:

- `allow`: add a permission not provided by the role.
- `deny`: remove a permission provided by the role.
- Explicit deny wins over explicit allow and role defaults.
- A disabled user has no effective permissions.
- An `admin` may receive denies, but invariants still require at least one active user with effective `user.manage` and `entitlement.manage` permissions.

Effective permissions are calculated on the server and returned by `GET /api/auth/session`. The browser must not calculate authority from the role name alone.

Only an actor with `entitlement.manage` can edit overrides. Prevent an administrator from using overrides to remove the final active user manager.

### 6.4 Object-Level Rules

In addition to permission checks:

- Developers may rotate/revoke only API keys they created unless they have `api_key.manage_all`.
- Users may change their own password and revoke their own sessions without `user.manage`.
- Administrators cannot disable themselves when they are the final active administrator/user manager.
- Administrators cannot delete the last effective administrator. Prefer disabling accounts over hard deletion.
- API keys may access only granted workflow definitions and instance scopes described in Section 11.
- API-key authorization resolves the target run from the database before deciding access. It never trusts a caller-supplied definition ID for a run operation.

## 7. Persistence Model

All security tables use the active Orchestra database and dialect. Store UTC timestamps consistently using the repository's existing string timestamp convention for the first implementation, then consider native PostgreSQL timestamp types in a separate migration.

### 7.1 `users`

| Column | Notes |
| --- | --- |
| `id` | Random stable ID such as `usr_<uuid>`; primary key |
| `username` | Display form |
| `username_normalized` | Trimmed and Unicode-normalized/case-folded login key; unique |
| `display_name` | Optional display name |
| `password_hash` | Argon2id PHC string containing algorithm, parameters, salt, and hash |
| `role` | `admin`, `developer`, or `observer` |
| `status` | `active` or `disabled` |
| `must_change_password` | True for bootstrap and admin-reset passwords |
| `failed_login_count` | Security telemetry and throttling input |
| `locked_until` | Optional temporary login throttle |
| `password_changed_at` | Used for session invalidation and audit |
| `last_login_at` | Optional successful login timestamp |
| `authz_version` | Incremented on role, status, or entitlement changes |
| `created_by` | Nullable actor user ID; null for bootstrap |
| `created_at`, `updated_at` | Audit timestamps |

Usernames should allow ordinary Unicode names but be normalized consistently before uniqueness checks. If Unicode normalization is deferred, constrain usernames to a documented ASCII subset for the first release rather than applying an unsafe lowercase-only approximation.

### 7.2 `user_entitlements`

| Column | Notes |
| --- | --- |
| `user_id` | Foreign key to `users` |
| `permission` | Permission string from the catalog |
| `effect` | `allow` or `deny` |
| `created_by` | Administrator who applied the override |
| `created_at` | Audit timestamp |

Primary key: (`user_id`, `permission`). Validate permission strings against the compiled catalog before writing.

### 7.3 `sessions`

| Column | Notes |
| --- | --- |
| `id` | Random internal session ID |
| `token_hash` | SHA-256 hash of a 256-bit random bearer token; unique |
| `csrf_token` | Separate random synchronizer token; not an authentication bearer token |
| `user_id` | Authenticated user |
| `created_at` | Login time |
| `last_seen_at` | Activity timestamp, updated at a bounded interval |
| `idle_expires_at` | Inactivity deadline |
| `absolute_expires_at` | Non-extendable session deadline |
| `password_version_at_login` | Password timestamp/version used to invalidate stale sessions |
| `authz_version_at_login` | Diagnostic snapshot, not the source of authorization truth |
| `revoked_at`, `revoke_reason` | Explicit invalidation data |
| `source_ip` | Canonical client IP at login |
| `user_agent_hash` | Correlation without storing an unbounded raw header |

Store only the authentication token hash. The raw session token exists only in the browser cookie and transient request memory. The CSRF synchronizer token is stored server-side so the SPA can recover it after a page reload; possession of it does not authenticate a request without the HttpOnly session cookie.

### 7.4 `api_keys`

| Column | Notes |
| --- | --- |
| `id` | Random internal ID such as `key_<uuid>` |
| `name` | Human-readable integration name |
| `description` | Optional purpose/owner information |
| `key_prefix` | Public lookup prefix shown in lists; unique |
| `secret_hash` | SHA-256 hash of a 256-bit random secret |
| `created_by_user_id` | Owner and audit actor |
| `status` | `active` or `revoked` |
| `expires_at` | Required by default; nullable only with elevated authorization and warning |
| `last_used_at` | Updated no more than once per configured interval |
| `last_used_ip` | Optional operational metadata |
| `created_at`, `updated_at` | Audit timestamps |
| `revoked_at`, `revoked_by` | Revocation details |
| `rotated_from_id` | Optional lineage for incident response |

Recommended key format:

```text
orch_<public-prefix>_<base64url-256-bit-secret>
```

The prefix permits indexed lookup. Compare the computed hash in constant time. A high-entropy random API-key secret may use a fast cryptographic hash because it is not human-selected and is not practically brute-forceable; passwords must use Argon2id.

### 7.5 `api_key_workflow_grants`

| Column | Notes |
| --- | --- |
| `api_key_id` | Foreign key to `api_keys` |
| `workflow_definition_id` | Granted workflow definition |
| `action` | `start`, `signal`, `status.read`, or `result.read` |
| `instance_scope` | `own` or `definition`; relevant to run operations |
| `allow_pinned_versions` | Whether `start` may request a published non-active version |
| `allow_callback_url` | Whether `start` may supply `X-Callback-URL` |
| `signal_names_json` | Optional allowlist of signal names; null means any signal name |
| `created_at` | Audit timestamp |

Primary key: (`api_key_id`, `workflow_definition_id`, `action`).

Defaults must be least-privileged:

- No workflow is selected automatically.
- No actions are selected automatically.
- `instance_scope` defaults to `own`.
- `allow_pinned_versions` defaults to false.
- `allow_callback_url` defaults to false.

### 7.6 `security_audit_events`

| Column | Notes |
| --- | --- |
| `id` | Auto-increment/identity primary key |
| `occurred_at` | UTC event time |
| `request_id` | Existing Chi request ID when applicable |
| `actor_type` | `user`, `api_key`, `system`, or `anonymous` |
| `actor_id` | Nullable stable actor ID |
| `action` | Stable action such as `auth.login`, `user.disable`, `workflow.start` |
| `resource_type`, `resource_id` | Target when applicable |
| `outcome` | `success`, `denied`, or `failure` |
| `source_ip` | Canonical client address |
| `user_agent` | Length-limited and sanitized, or a hash plus short product string |
| `metadata_json` | Strictly allowlisted metadata; never credentials or full workflow payloads |

Indexes should support time-ordered listing, actor lookup, action lookup, and resource lookup. Audit events are append-only through the application. No UI endpoint deletes them.

### 7.7 Workflow Attribution Columns

Add these nullable columns to `workflow_instances`:

- `trigger_principal_type`
- `trigger_principal_id`

Populate them for UI users, API keys, and internal starts. Existing rows remain null and are treated as legacy/system runs. These columns are required for an API key's default `own` instance scope.

### 7.8 `security_rate_limits`

Use a small shared fixed-window or token-bucket table so throttles cannot be bypassed by moving between PostgreSQL controllers:

| Column | Notes |
| --- | --- |
| `bucket_key_hash` | HMAC or hash of the bounded account/IP/key bucket identifier; primary key |
| `bucket_type` | `login_account`, `login_ip`, or `api_key` |
| `window_started_at` | Current window start |
| `attempt_count` | Atomic count in the window |
| `blocked_until` | Optional progressive throttle deadline |
| `expires_at` | Cleanup deadline |

Never store submitted passwords or raw API-key values in limiter keys. Keep an in-memory short-burst limiter in front of the shared limiter to reduce database writes, but treat the database-backed limit as the cluster-wide authority.

## 8. Shared Database and Migration Foundation

Authentication should not be implemented by opening a second database pool in `internal/auth`. Refactor database ownership first:

1. Introduce `internal/database` to open/configure SQLite or PostgreSQL and own dialect rebinding.
2. Pass a shared `*sql.DB` and dialect to workflow and authentication services.
3. Keep a compatibility `workflow.NewService` wrapper for focused tests while production startup uses the shared connection.
4. Add numbered, idempotent migrations for the security tables and workflow attribution columns.
5. Add a `schema_migrations` table so future changes are ordered and observable.
6. Preserve current behavior:
   - SQLite applies pending migrations during startup.
   - PostgreSQL prints/applies all required DDL through `orchestra schema [--create]` and the server fails with a useful message when required security tables are absent.
7. Ensure `orchestra schema --driver postgres` includes the complete authentication schema.
8. Add foreign keys where they are safe for both dialects. Use `ON DELETE RESTRICT` for audit/ownership records and disable users/keys instead of deleting them.

Bootstrap and migrations must happen before the HTTP listener starts. The application must never temporarily expose an unauthenticated control plane while initialization is incomplete.

## 9. Password Authentication

### 9.1 Password Storage

- Use `golang.org/x/crypto/argon2` and encode hashes in a versioned PHC-style string.
- Initial baseline: Argon2id with at least 19 MiB memory, 2 iterations, parallelism 1, a 16-byte random salt, and a 32-byte result.
- Benchmark the production target and raise the work factor when practical while keeping normal verification below roughly one second.
- Parse and validate encoded parameters defensively before allocating memory.
- Rehash on successful login when stored parameters are weaker than the current policy.
- Keep password hashing in a small `internal/auth/passwords.go` unit with fixed upper bounds and dedicated tests.

### 9.2 Password Policy

- Minimum 12 characters for normal users and bootstrap changes.
- Maximum 128 Unicode code points and a bounded byte size to prevent resource abuse.
- Allow spaces and password-manager-generated strings.
- Do not require arbitrary composition rules or periodic password changes.
- Reject a local list of common passwords. A network breach-password check can be added later but must not make login depend on an external service.
- Never trim a submitted password. Validate it exactly as entered.

### 9.3 Login Behavior

`POST /api/auth/login` accepts username and password and always returns the same failure message for unknown user, wrong password, disabled user, or temporary lock:

```json
{ "error": "invalid username or password" }
```

Controls:

- Apply request-body limits before JSON decoding.
- Use a dummy Argon2id verification path for unknown usernames to reduce timing differences.
- Add shared account-oriented throttling and per-IP throttling.
- Apply progressive delays and a short temporary lock rather than a permanent lock requiring administrator intervention.
- Reset failure counters after a successful login.
- Record success and failure in the security audit log without recording the password.
- Return `Cache-Control: no-store` for authentication and user/session responses.

## 10. Browser Session Design

### 10.1 Opaque Server-Side Session

On successful login:

1. Generate a 256-bit random session token and independent CSRF token with `crypto/rand`.
2. Store the session-token hash and the CSRF synchronizer token in `sessions`.
3. Set the raw session token in an HttpOnly cookie.
4. Return the raw CSRF token in the login/session response for in-memory use by the SPA.
5. Rotate any pre-authentication session identifier; do not accept session IDs from URLs or headers.

Recommended defaults:

- Idle timeout: 30 minutes.
- Absolute timeout: 8 hours.
- No "remember me" in the first release.
- Update `last_seen_at` no more than once every five minutes to avoid a write per request.

### 10.2 Cookie Policy

Production cookie:

```text
HttpOnly; Secure; SameSite=Lax; Path=/
```

Use a `__Host-` cookie name when HTTPS is active and no Domain attribute is set. Local HTTP development may use a separate non-`Secure` development cookie name. Production startup must reject an insecure cookie configuration when the public app URL is HTTPS or the environment is production.

### 10.3 CSRF Protection

For `POST`, `PUT`, `PATCH`, and `DELETE` under `/api/*`:

- Require `X-CSRF-Token` matching the active session.
- Validate `Origin` against the configured public application origin.
- Fall back to strict `Referer` validation only when appropriate.
- Reject missing or mismatched tokens with `403`.
- Keep `SameSite=Lax` as an additional layer, not the only layer.

Login should enforce the same-origin policy even though it has no existing session. `/ext/*` uses bearer API keys and is not subject to browser-cookie CSRF checks.

### 10.4 Session Invalidation

- Logout revokes the server row and expires the cookie.
- Password change revokes all other sessions for the user.
- Admin password reset and user disable revoke all sessions immediately.
- Role and entitlement changes take effect on the next request. Load effective permissions from current user state rather than trusting a role snapshot embedded in the session.
- Expired/revoked sessions return `401` and are periodically deleted by a cleanup job.
- The UI clears cached queries and returns to login on `401`.

## 11. Workflow-Scoped API Keys

### 11.1 External Authentication

Require this header on every protected `/ext/*` request:

```http
Authorization: Bearer orch_<prefix>_<secret>
```

Do not accept keys in query strings. Query strings are commonly retained in browser history, proxies, logs, and metrics.

Validation order:

1. Parse format and prefix with fixed length limits.
2. Look up the active key by prefix.
3. Hash and constant-time compare the secret.
4. Reject revoked or expired keys.
5. Resolve the target workflow definition or run.
6. Check the requested action and object scope.
7. Apply per-key and per-IP rate limits.
8. Execute the operation and record an audit event.

Return `401` with `WWW-Authenticate: Bearer` for missing/invalid/expired credentials. Return `403` for a valid key without the required grant. For resources outside the key's visible scope, prefer `404` when that avoids confirming existence.

### 11.2 Route-to-Grant Mapping

| Existing route | Required grant | Object check |
| --- | --- | --- |
| `POST /ext/webhook/{definitionId}/start` | `start` | Path definition must be granted |
| `POST /ext/webhook/{workflowId}/signal` | `signal` | Resolve run definition; enforce instance scope and signal-name allowlist |
| `GET /ext/signal/{workflowId}` | `status.read` | Resolve run definition; enforce instance scope |
| `GET /ext/result/{workflowId}` | `result.read` | Resolve run definition; enforce instance scope |

For `instance_scope=own`, the run's `trigger_principal_type` must be `api_key` and `trigger_principal_id` must match the calling key. For `instance_scope=definition`, any run of the granted definition is accessible. The UI should label definition-wide scope as broader access and default to own runs.

### 11.3 Start Restrictions

- A key with `start` may start only a granted definition.
- Default to the active published version.
- Reject requested inactive/non-active versions unless `allow_pinned_versions` is true and the version is published.
- Reject `X-Callback-URL` unless `allow_callback_url` is true and the URL passes the existing callback allowlist.
- Continue to validate workflow state and input schemas independently of authorization.
- Accept an optional `Idempotency-Key` and store a per-key result for a bounded period so webhook retries do not accidentally create duplicate runs.

### 11.4 Signal Restrictions

- Resolve the run before checking grants.
- Enforce run state transitions in the workflow service.
- If a grant has `signal_names_json`, reject signal names outside that list.
- Add idempotency handling for clients that retry signal delivery.
- Do not let API-key grant data override workflow-level validation.

### 11.5 Key Lifecycle

- The secret is shown exactly once after create or rotate.
- List/detail responses return name, prefix, grants, owner, created time, expiration, status, and last use, never `secret_hash`.
- Rotation creates a new key record, copies reviewed grants, and revokes the old record in one transaction.
- Revocation takes effect on the next request; do not cache active key status across requests initially.
- Default expiration is 90 days. Permit up to 365 days for normal key creation. A no-expiration key requires an elevated entitlement or explicit administrator-only confirmation.
- Update `last_used_at` at a bounded interval to avoid write amplification.

### 11.6 Legacy Webhook Migration

Use a controlled transition for existing integrations:

1. Add `webhook.authenticationMode = "required" | "audit"` with `required` as the secure default.
2. `audit` temporarily permits missing keys but emits prominent startup warnings, per-request audit events, and metrics. It must be documented as a migration mode, not a permanent operating mode.
3. Create keys and update callers while in `audit` mode.
4. Switch to `required` and verify that anonymous calls return `401`.
5. Do not provide a production `disabled` mode. Tests can inject a test authenticator rather than shipping a runtime bypass.

New installations start in `required` mode.

## 12. Initial Administrator Bootstrap

### 12.1 Required Behavior

At controller startup, after migrations and before listening:

1. Check whether any user exists.
2. If users exist, do nothing and never overwrite an account from bootstrap configuration.
3. If no user exists, create one active `admin` with `must_change_password=true`.
4. Ensure only one controller wins bootstrap in a shared PostgreSQL deployment.
5. Fail startup if the user cannot be safely created or the temporary credential cannot be delivered.

### 12.2 Credential Sources

Use this precedence:

1. `APP_AUTH_INITIAL_ADMIN_PASSWORD_FILE`, read from a mounted secret file.
2. `APP_AUTH_INITIAL_ADMIN_PASSWORD`, accepted for deployment systems that inject environment secrets.
3. Generate a 256-bit random base64url password and write it to `data/bootstrap-admin.txt` with mode `0600`.

Username source:

- `APP_AUTH_INITIAL_ADMIN_USERNAME`, default `admin`.

Do not put the bootstrap password in `config.toml`, `example.config.toml`, API metadata, or normal logs. Log only the username and the path from which the credential can be obtained.

After the first successful password change:

- Clear `must_change_password`.
- Revoke the bootstrap session and issue a fresh normal session.
- Best-effort remove the generated bootstrap file if this node created it.
- Emit an audit event reminding operators to remove bootstrap secrets from deployment configuration.

### 12.3 Cluster Race Handling

- Run bootstrap in a transaction.
- PostgreSQL: use a transaction-scoped advisory lock or serializable transaction around the empty-table check and insert.
- SQLite: startup uses one connection and a write transaction.
- Generate and persist the temporary credential only for the transaction winner.
- If a controller loses the race, discard its generated secret and load the existing state.

Never expose an open "claim the first admin" web endpoint. That design lets the first network caller become administrator.

## 13. Backend API Design

### 13.1 Public Routes

Keep the public surface intentionally small:

| Route | Purpose |
| --- | --- |
| `GET /livez` | Process liveness, no sensitive dependencies |
| `GET /api/health` | Minimal readiness status; consider a richer authenticated health response |
| `GET /api/meta/public` | Login-page brand name and build version only |
| `POST /api/auth/login` | Username/password authentication |

The SPA assets remain public so the login screen can load. Remove internal URL, Vite proxy, config paths, and detailed topology from public metadata.

### 13.2 Auth and Session Endpoints

| Method and route | Permission | Purpose |
| --- | --- | --- |
| `POST /api/auth/login` | Public, same-origin | Create session |
| `POST /api/auth/logout` | Authenticated + CSRF | Revoke current session |
| `GET /api/auth/session` | Authenticated | Current user, effective permissions, CSRF token, expiry |
| `POST /api/auth/change-password` | Authenticated + CSRF | Change own password |
| `GET /api/auth/sessions` | `session.manage_own` | List own session metadata |
| `DELETE /api/auth/sessions/{id}` | Own session or `session.manage_all` | Revoke a session |

`GET /api/auth/session` response shape:

```json
{
  "user": {
    "id": "usr_...",
    "username": "admin",
    "displayName": "Administrator",
    "role": "admin",
    "mustChangePassword": false
  },
  "permissions": ["dashboard.read", "user.manage"],
  "csrfToken": "...",
  "session": {
    "idleExpiresAt": "...",
    "absoluteExpiresAt": "..."
  }
}
```

### 13.3 User Administration Endpoints

| Method and route | Permission | Purpose |
| --- | --- | --- |
| `GET /api/users` | `user.read` | Paginated/filterable users |
| `POST /api/users` | `user.manage` | Create account with role and temporary password |
| `GET /api/users/{id}` | `user.read` | User detail and effective entitlements |
| `PATCH /api/users/{id}` | `user.manage` | Display name, username, role, status |
| `POST /api/users/{id}/reset-password` | `user.manage` | Generate/set temporary password and revoke sessions |
| `PUT /api/users/{id}/entitlements` | `entitlement.manage` | Replace validated override set transactionally |
| `GET /api/roles` | Authenticated | Built-in role descriptions and permission bundles |
| `GET /api/permissions` | `user.read` | Permission catalog for the management UI |

Prefer account disabling to `DELETE`. If a hard-delete endpoint is later required, it must preserve audit attribution and reject users referenced by historical events.

### 13.4 API-Key Administration Endpoints

| Method and route | Permission | Purpose |
| --- | --- | --- |
| `GET /api/api-keys` | `api_key.read` | List visible key metadata |
| `POST /api/api-keys` | `api_key.create` | Create key and grants; return secret once |
| `GET /api/api-keys/{id}` | Owner or `api_key.manage_all` | Metadata and grants |
| `PATCH /api/api-keys/{id}` | Owner + `manage_own`, or `manage_all` | Name, description, expiration, grants |
| `POST /api/api-keys/{id}/rotate` | Owner + `manage_own`, or `manage_all` | Atomically replace and return new secret once |
| `POST /api/api-keys/{id}/revoke` | Owner + `manage_own`, or `manage_all` | Revoke immediately |

Create and rotate responses get `Cache-Control: no-store`. Never include a secret in list responses, live events, logs, audit metadata, or URLs.

### 13.5 Audit Endpoint

`GET /api/audit-events` requires `audit.read` and supports bounded pagination plus filters for time, actor, action, outcome, and resource. Cap page size and metadata response size.

## 14. Middleware and Request Context

Add a typed principal to request context:

```go
type Principal struct {
    Type        PrincipalType
    ID          string
    DisplayName string
    Permissions PermissionSet
    SessionID   string
    APIKeyID    string
}
```

Recommended middleware order for `/api/*`:

1. Request ID
2. Trusted-proxy-aware real IP
3. Recovery and request logging
4. Security response headers
5. Request timeout and body-size limits
6. Session authentication
7. CSRF/origin validation for unsafe browser methods
8. Permission middleware
9. Handler and object-level authorization
10. Audit outcome recording

Recommended middleware order for `/ext/*`:

1. Request ID and trusted client IP
2. Recovery, security headers, timeout, and body limits
3. API-key authentication
4. Rate limit
5. Workflow grant and object-level authorization
6. Handler/state validation
7. Audit outcome recording

Refactor `internal/api/router.go` into explicit groups, for example:

- Public control-plane routes.
- Authenticated read routes.
- Workflow developer routes.
- Administrative routes.
- External API-key routes.

Use small wrappers such as `Require(permission)` and object authorizers. Do not duplicate role-name checks in handlers.

## 15. Existing Route Authorization Mapping

The following mapping should guide the router refactor:

| Route group | Read permission | Mutation/control permission |
| --- | --- | --- |
| `/scripts`, `/json-schemas`, `/agents`, `/mcp-servers` | `resource.read` | `resource.write`, including connector exploration |
| `/workflow-definitions` and versions | `workflow.definition.read` | `workflow.definition.write` or `workflow.definition.publish` |
| `/workflow-definitions/{id}/start` | `workflow.definition.read` | `workflow.run.start` |
| `/workflows` and histories | `workflow.run.read` | `workflow.run.control` |
| `/workflows/tasks` | `workflow.task.read` | `workflow.task.control` |
| `/workflows/events` | `operation.read` | None |
| `/import/analyze` | `import.analyze` | None |
| `/import/apply` | None | `import.apply` |
| `/ai/*` | Related resource permission | `ai.use` plus the related resource write permission where content is persisted |
| `/nodes` | `cluster.read` | `cluster.control` for health check |
| `/config/raw` | `settings.write` | `settings.write` |
| `/admin/restart` | None | `server.restart` |
| `/ws` | `operation.read` | None |

Exports inherit the corresponding read permission. A `GET` is not automatically harmless: config reads, workflow results, connector headers, and audit events still need explicit authorization and response filtering.

## 16. WebSocket Security

The live bus currently disables origin verification. Change it as follows:

- Authenticate the session cookie before upgrading.
- Require `operation.read`.
- Validate `Origin` against the configured app origin; remove `InsecureSkipVerify: true`.
- Close with an appropriate policy status when the session expires or is revoked.
- Do not put session or API-key tokens in the WebSocket URL.
- Avoid broadcasting security events, raw credentials, connector headers, or user password state.
- If future resource-scoped permissions are added, filter events per subscriber instead of broadcasting every event to every authenticated observer.

The current same-origin browser WebSocket automatically carries the session cookie, so no browser token storage is needed.

## 17. Frontend Plan

### 17.1 Shared API Client

Replace repeated raw `fetch` usage in `ui/src/services/api.ts` with one request helper that:

- Uses same-origin cookies via `credentials: 'same-origin'`.
- Adds `X-CSRF-Token` for unsafe `/api/*` requests.
- Sets `Accept: application/json` and content type consistently.
- Parses structured error codes and messages.
- On `401`, clears React Query state and sends the user to `/login` with a safe return path.
- On `403`, shows a permission-specific error without logging the user out.
- Never attaches the browser CSRF token or session data to `/ext/*` URLs.

### 17.2 Authentication State

Add:

- `ui/src/auth/AuthProvider.tsx`
- `ui/src/auth/ProtectedRoute.tsx`
- `ui/src/auth/PermissionGate.tsx`
- `ui/src/pages/LoginPage.tsx`
- `ui/src/pages/ChangePasswordPage.tsx`

`AuthProvider` calls `/api/auth/session`, exposes the current user, effective permission set, login/logout/change-password operations, and loading state. Keep the CSRF token in memory, not `localStorage` or `sessionStorage`.

Routing behavior:

- Unauthenticated users see `/login`.
- Authenticated users cannot remain on `/login`.
- A bootstrap/reset user is forced to `/change-password` before the rest of the app.
- Protected pages check named permissions.
- Unknown or forbidden administration routes render a proper 403 state.

### 17.3 Layout and Navigation

Update `Layout` to:

- Filter navigation by effective permissions.
- Add an Administration entry only when the user has at least one relevant admin/key permission.
- Add a compact user menu with display name, role, change password, session management, and logout.
- Keep observer navigation useful while removing edit, start, signal, queue-control, config, and restart commands.

Backend denial remains authoritative even when navigation or buttons are hidden.

### 17.4 Read-Only UX

Every existing page with mutations needs a permission pass:

- Workflow designer and version publish/activate controls.
- Start-workflow and signal controls.
- Run cancellation.
- Queue task action menus.
- Script, schema, agent, and connector editors.
- Import apply flows.
- AI assistance and connector exploration.
- Cluster health-check command.
- Settings editor and restart command.

Observers should receive a polished read-only view, not a page full of disabled controls. Hide clear commands, preserve inspect/export functions allowed by read permissions, and use a concise read-only indicator where editing would otherwise be ambiguous.

### 17.5 User Management UI

Add `/administration/users` with:

- Searchable, paginated table: username, display name, role, status, last login, created time.
- Create-user dialog: username, display name, role, generated or supplied temporary password.
- User detail/edit panel: identity fields, role, status, effective permissions, explicit overrides, active sessions.
- Role selector using the three fixed roles.
- Advanced entitlement editor grouped by domain, showing inherited, allowed, and denied states distinctly.
- Reset-password action that shows a generated temporary password once.
- Enable/disable action with confirmation and final-admin safeguards surfaced before submission.
- Session revocation controls.

Do not render stored password hashes, failed password values, session tokens, or full IP histories.

### 17.6 API-Key Management UI

Add `/administration/api-keys` for admins and developers:

- List name, prefix, owner, status, expiration, last use, and workflow count.
- Create flow with name, description, expiration, workflow multi-select, action checkboxes, run scope, callback permission, pinned-version permission, and optional signal allowlist.
- Secret reveal dialog shown only after create/rotate, with copy control and explicit acknowledgement that it cannot be retrieved again.
- Detail/edit view for metadata and grants.
- Rotation confirmation explaining that old callers will fail immediately after rotation.
- Revocation confirmation.
- Admins can view/manage all keys; developers see all metadata only if policy allows and can mutate their own keys by default.

The creation UI must submit workflow IDs and actions, while the server validates every submitted definition and permission.

### 17.7 Audit UI

Add `/administration/audit` for `audit.read`:

- Time-ordered events with filters for actor, action, outcome, and resource.
- Expandable sanitized metadata.
- Clear visual distinction for denied and failed actions.
- No raw cookie, authorization header, password, API-key secret, full connector header, or workflow payload values.

## 18. Security Audit Requirements

Always audit:

- Login success and failure.
- Logout, session expiry, and session revocation.
- Password change and admin password reset.
- User create, update, enable, disable, and role change.
- Entitlement override changes with before/after permission names.
- API-key create, grant change, rotate, revoke, expiration failure, and use.
- Authorization denials for administrative and external operations.
- Workflow start/control actions with actor attribution.
- Config write and server restart.
- Import apply and resource deletion.

Never audit:

- Passwords or password hashes.
- Raw session or CSRF tokens.
- Raw API keys or API-key hashes.
- Authorization and Cookie headers.
- AI provider secrets, connector secret headers, database URLs with credentials, or full config content.
- Unbounded user-supplied payloads.

Define retention in configuration, initially defaulting to 90 days, and provide an operator-run cleanup job. Cleanup itself emits an audit/operational event with counts, not deleted content.

## 19. HTTP and Deployment Hardening

Authentication should ship with these adjacent controls because they protect the new credentials:

- Require HTTPS at the ingress in production.
- Set HSTS only when HTTPS is correctly terminated and the deployment is ready for it.
- Add `X-Content-Type-Options: nosniff`, `Referrer-Policy`, `Permissions-Policy`, and frame protection.
- Introduce a Content Security Policy in report-only mode first, accounting for Vite production assets, Monaco workers, `blob:` workers, WebSockets, and inline styles currently required by the UI.
- Add `Cache-Control: no-store` to auth, user, API-key creation, config, and sensitive run-result responses.
- Add route-specific body-size limits, especially login, import, workflow start, signal, and config write.
- Configure trusted proxy CIDRs before honoring forwarded client IP headers. Do not blindly trust `X-Forwarded-For` from the public internet.
- Keep CORS disabled for the browser API unless a concrete cross-origin client is introduced.
- Redact cookies, authorization headers, secrets, and sensitive payloads from request logs.
- Preserve and strengthen the callback URL allowlist. Validate URL scheme, hostname resolution, redirects, and private-address policy to reduce SSRF risk.
- Add per-account/IP login throttles and per-key/IP external API throttles with `429` responses and `Retry-After`.

## 20. Configuration Additions

Suggested non-secret configuration:

```toml
[auth]
sessionIdleTimeout     = "30m"
sessionAbsoluteTimeout = "8h"
cookieSecure           = "auto"
bootstrapOutputPath    = "data/bootstrap-admin.txt"
auditRetention         = "2160h" # 90 days
trustedProxyCIDRs      = []

[auth.apiKeys]
defaultTTL       = "2160h" # 90 days
maximumTTL       = "8760h" # 365 days
usageWriteWindow = "5m"
requestsPerMinute = 60
burst             = 20

[webhook]
authenticationMode = "required"
```

Secret bootstrap inputs are environment/secret-file only:

```text
APP_AUTH_INITIAL_ADMIN_USERNAME
APP_AUTH_INITIAL_ADMIN_PASSWORD_FILE
APP_AUTH_INITIAL_ADMIN_PASSWORD
```

Do not return these values through `/api/meta`, `/api/config/raw`, or diagnostics. Extend config redaction tests for every future secret-looking auth key even if bootstrap secrets are not normally stored in TOML.

## 21. Error Contract

Standardize protected API errors:

```json
{
  "error": "permission denied",
  "code": "AUTH_FORBIDDEN",
  "requestId": "..."
}
```

Recommended status behavior:

| Status | Meaning |
| --- | --- |
| `400` | Invalid request shape |
| `401` | Missing, invalid, expired, or revoked authentication |
| `403` | Valid principal lacks permission/grant, including CSRF failure |
| `404` | Resource absent or intentionally hidden from this principal |
| `409` | Invariant conflict, such as disabling the last administrator |
| `422` | Valid JSON with invalid workflow/grant semantics |
| `429` | Authentication or API-key rate limit exceeded |

Login failures stay generic. Administrative mutation errors can be specific after authentication when doing so does not reveal another user's secret state.

## 22. Test Plan

### 22.1 Unit Tests

- Argon2id encode, verify, malformed-hash rejection, upper parameter bounds, and rehash detection.
- Cryptographic token/key generation entropy and format parsing.
- Constant-time secret comparison path.
- Username normalization and uniqueness.
- Permission catalog validity, duplicate detection, and role bundles.
- Effective permission calculation for role, allow, deny, disabled status, and deny precedence.
- Session idle/absolute expiry and revocation.
- API-key expiration, rotation lineage, and grant evaluation.
- Bootstrap credential-source precedence.
- Audit metadata sanitizer and size limits.

### 22.2 Repository and Migration Tests

Run against SQLite and PostgreSQL:

- Fresh schema creation.
- Upgrade from the current schema without data loss.
- Re-running migrations is idempotent.
- Required unique constraints and foreign keys.
- Concurrent bootstrap creates exactly one admin.
- Role/entitlement updates increment `authz_version` atomically.
- Key rotation copies grants and revokes the prior key transactionally.
- Disabling a user revokes sessions transactionally.
- Existing workflow rows survive attribution-column migration.

### 22.3 Authentication API Tests

- Successful login sets expected cookie flags and returns no-store.
- Unknown user, wrong password, disabled user, and locked user have generic equivalent responses.
- Login throttling produces `429` and recovers after its window.
- Missing/invalid/expired/revoked sessions produce `401`.
- Logout invalidates the server row and cookie.
- Password change requires the current password, revokes other sessions, and rotates the current session.
- Bootstrap users cannot navigate normal APIs until password change is complete.
- CSRF and Origin checks cover every unsafe method.

### 22.4 Authorization Matrix Tests

Build a table-driven test covering every registered `/api` route and HTTP method for:

- Anonymous caller.
- Admin.
- Developer.
- Observer.
- User with an explicit allow.
- User with an explicit deny.
- Disabled user.

The test fails when a newly registered route has no declared public/permission policy. Include verb-tampering cases such as trying `DELETE`, `PUT`, or action endpoints as an observer.

### 22.5 Object-Level Tests

- A developer can rotate their own key but not another developer's key.
- An administrator can manage any key.
- The final administrator cannot be disabled, demoted, or denied user-management capability.
- A user cannot update their own role or entitlement payload through mass assignment.
- API responses never serialize password hashes, session hashes, or key hashes. The CSRF synchronizer token appears only in authenticated login/session responses.

### 22.6 External API-Key Tests

- Missing/malformed/unknown/revoked/expired key behavior.
- Key for workflow A cannot start workflow B.
- Key for workflow A cannot read, signal, or retrieve a workflow B run by changing an ID.
- Own-scope key cannot control a same-definition run created by another user/key.
- Definition-scope key can control a matching-definition run only for granted actions.
- Signal-name restrictions.
- Pinned-version and callback restrictions.
- Secret appears once on create/rotate and never in later responses or logs.
- Rotation immediately invalidates the old secret.
- Rate limits and `Retry-After`.
- Idempotent retries do not duplicate starts/signals.

### 22.7 WebSocket Tests

- Anonymous and expired sessions cannot upgrade.
- Observer with `operation.read` can upgrade.
- User denied `operation.read` cannot upgrade.
- Untrusted Origin is rejected.
- Revoked session closes or loses access promptly.
- Events do not contain security credentials.

### 22.8 Frontend Tests

- Login, forced password change, normal logout, and session-expiry redirect.
- Navigation for all three roles.
- Observer cannot reach editor routes or invoke hidden actions directly.
- User create/edit/disable/reset and entitlement UI.
- API-key create/reveal-once/rotate/revoke and workflow grants.
- 401 and 403 handling does not leak stale React Query data between users.
- Desktop and mobile layout for login, user management, and API-key dialogs.

### 22.9 Security Verification

- `go test ./...`
- UI TypeScript build and lint.
- Dependency and vulnerability scanning for Go and npm packages.
- Static checks for hard-coded secrets and sensitive logging.
- Manual browser review of cookie flags, CSRF failures, CSP report-only findings, and cache headers.
- Negative testing for object ID substitution and function-level authorization bypass.

## 23. Delivery Plan

### Phase 0: Decisions and Route Inventory

Deliverables:

- Approve this role/permission matrix.
- Approve bootstrap credential delivery and webhook compatibility policy.
- Create a machine-readable inventory of all current routes and required permissions.
- Decide whether developers may list all API-key metadata or only keys they own. Recommended: visible metadata for keys they own; admins see all.

Exit criteria:

- Every current route has an owner, classification, and permission.
- No unresolved behavior can create an anonymous production control path.

### Phase 1: Shared Database, Schema, and Auth Domain

Likely files/packages:

- New `internal/database/*`
- New `internal/auth/model.go`
- New `internal/auth/repository.go`
- New `internal/auth/passwords.go`
- New `internal/auth/permissions.go`
- Updates to `internal/workflow/service.go`, `internal/workflow/dialect.go`, `internal/workflow/service_schema.go`
- Updates to `cmd/app/schema.go` and `cmd/app/serve.go`

Work:

- Share the database connection and dialect.
- Add migrations and all security tables.
- Implement users, password hashing, permissions, sessions, API-key persistence, and audit repository primitives.
- Implement transaction-safe initial-admin bootstrap.

Exit criteria:

- Fresh and upgraded SQLite/PostgreSQL schemas pass tests.
- Exactly one bootstrap administrator is created under concurrency.
- No HTTP behavior changes yet except startup schema validation.

### Phase 2: Browser Authentication and Session Enforcement

Likely files/packages:

- New `internal/auth/service.go`, `sessions.go`, and `middleware.go`
- New `internal/api/handler_auth.go`
- Updates to `internal/server/server.go`, `internal/api/router.go`, `internal/api/handler_stream.go`
- New frontend auth provider, login, and change-password pages
- Refactor `ui/src/services/api.ts`

Work:

- Add login/logout/session/password endpoints.
- Protect all non-public `/api` routes with session authentication.
- Add CSRF and Origin enforcement.
- Authenticate and origin-check WebSockets.
- Add frontend login and session lifecycle.

Exit criteria:

- Anonymous control-plane requests fail with `401`.
- Admin can log in and use the existing app.
- Bootstrap password change is mandatory.
- Existing API tests are updated to authenticate explicitly rather than bypass middleware.

### Phase 3: Authorization and Role-Aware Existing UI

Work:

- Apply named permissions to every route.
- Add object-level helper checks where ownership applies.
- Add principal attribution to workflow starts and controls.
- Gate existing UI routes, navigation, and commands by effective permissions.
- Make observer pages genuinely read-only.
- Add table-driven route coverage tests.

Exit criteria:

- Admin, developer, and observer matrix passes backend tests.
- Adding an unclassified route fails tests.
- Direct HTTP calls cannot bypass hidden frontend controls.

### Phase 4: User, Entitlement, and Session Management UI

Work:

- Add user/role/permission/session endpoints.
- Add Administration navigation and user management pages.
- Implement account creation, role/status changes, password reset, entitlement overrides, and session revocation.
- Enforce final-admin invariants transactionally.

Exit criteria:

- A fresh admin can create developer/observer accounts entirely through the UI.
- Role and entitlement changes take effect on the target user's next request.
- Disabled/reset users lose existing sessions.

### Phase 5: Workflow-Scoped API Keys and External Enforcement

Work:

- Implement key generation, one-time display, hashing, lifecycle, ownership, and grants.
- Add API-key management UI.
- Add `/ext/*` bearer authentication and workflow/run object authorization.
- Add workflow attribution, signal restrictions, callback restrictions, rate limits, and idempotency.
- Implement `audit` to `required` migration mode.

Exit criteria:

- No anonymous external workflow control in required mode.
- Cross-workflow and cross-instance authorization tests pass.
- Operators can create, rotate, revoke, and scope keys from the UI.

### Phase 6: Audit UI, Hardening, and Operational Readiness

Work:

- Complete security audit coverage and UI.
- Add retention cleanup and metrics.
- Add security headers, trusted-proxy configuration, body limits, and CSP report-only rollout.
- Update `README.md`, `example.config.toml`, Docker Compose, nginx example, and upgrade instructions.
- Add backup/restore and lost-admin recovery runbook.

Exit criteria:

- Security-sensitive actions are attributable without secret leakage.
- Deployment documentation requires HTTPS and explains bootstrap/key rotation.
- Upgrade and rollback are rehearsed against a production-like database copy.

## 24. Recommended Pull Request Boundaries

1. `database foundation and authentication schema`
   - Shared DB lifecycle, migrations, auth models/repositories, bootstrap tests.
2. `add secure local login and browser sessions`
   - Password verification, sessions, CSRF, login UI, WebSocket authentication.
3. `enforce roles and entitlements across the control plane`
   - Permission middleware, route matrix, observer/developer UI behavior.
4. `add user and entitlement administration`
   - User APIs, administration UI, last-admin safeguards, session revocation.
5. `secure external webhooks with workflow-scoped api keys`
   - Key lifecycle, grants, external authorization, idempotency, API-key UI.
6. `add security auditing and deployment hardening`
   - Audit UI/retention, headers, proxy/TLS guidance, metrics, runbooks.

Each PR should include its migrations, backend tests, frontend behavior, and documentation for the behavior it introduces. Avoid landing an authentication database schema without a tested recovery path, or landing UI gates before backend enforcement.

## 25. Deployment and Upgrade Runbook

Before upgrading:

1. Back up the SQLite file or PostgreSQL database.
2. Apply/review PostgreSQL schema changes with `orchestra schema`.
3. Prepare an initial-admin password secret file or secure environment secret.
4. Inventory current `/ext/*` callers and choose a webhook migration window.
5. Ensure HTTPS ingress and correct public `app.url`.

During upgrade:

1. Start one controller first and verify bootstrap/migrations.
2. Retrieve the bootstrap credential from the configured secret source or mode `0600` output file.
3. Log in and change the temporary password immediately.
4. Create named developer and observer accounts; do not share the admin credential.
5. In webhook `audit` mode, create one least-privileged key per integration and update callers.
6. Switch webhook mode to `required` and verify anonymous `401` responses.
7. Start remaining controllers and verify session/key behavior across nodes.

After upgrade:

- Remove bootstrap password environment variables/files from deployment secrets after successful password change.
- Review failed login, denied authorization, and anonymous webhook audit events.
- Confirm backup restoration includes security tables.
- Schedule API-key rotation before expiration.

Rollback considerations:

- Database migrations are additive for the first release; old binaries should ignore new tables/columns.
- Rolling back the binary reopens the old unauthenticated behavior, so rollback must also restore ingress restrictions. Treat this as a security event, not a routine transparent rollback.
- Do not drop security tables during rollback. Preserve users, audit events, and key revocation history.

## 26. Recovery Procedures

### Lost Administrator Password

Add a local CLI command, available only to an operator with host/database access:

```text
orchestra users reset-password --username admin --password-file /secure/path
```

The command must:

- Refuse a literal password command-line argument because process lists and shell history can expose it.
- Read from a mode-restricted file or interactive TTY.
- Set `must_change_password=true`.
- Revoke all sessions for the account.
- Emit a security audit event with actor type `system` and recovery reason.

### Lost or Leaked API Key

- Revoke by public prefix from the UI or CLI.
- Create a replacement with reviewed grants rather than reactivating the old key.
- Review audit events for the key ID, source IPs, workflows, and actions.
- Cancel or inspect suspicious workflow runs as an explicit operational decision.

### Corrupt Bootstrap State

- If users exist, bootstrap never overwrites them.
- If no users exist and the bootstrap output file exists, fail with an actionable error rather than silently replacing it.
- Provide an operator-confirmed CLI recovery command to regenerate bootstrap only after verifying the user table is empty.

## 27. Observability

Add low-cardinality metrics:

- Login successes/failures by outcome category, not username.
- Active and expired session counts.
- Authorization denials by permission and route template.
- API-key requests by outcome and action, not raw prefix.
- Webhook rate-limit responses.
- Bootstrap success/failure.
- Audit write failures.

Audit write failure policy:

- For high-risk administrative mutations and API-key lifecycle changes, fail the operation if its audit event cannot be committed in the same transaction or reliably queued.
- For read denials and login failures, log a structured server error if the audit database write fails without disclosing credentials.
- Expose an operator health signal for persistent audit failures.

## 28. Acceptance Criteria

The security project is complete when all of these are true:

- A new installation auto-creates exactly one temporary admin and never exposes public first-user signup.
- The admin can manage users, roles, overrides, sessions, and API keys through the UI.
- Developer and observer behavior matches the approved role matrix.
- No control-plane route or WebSocket is usable anonymously except the documented public routes.
- Every backend route has a tested authorization declaration.
- CSRF protection covers all browser-session mutations.
- Passwords are Argon2id-hashed; raw session tokens and API-key secrets are never stored.
- API-key secrets are shown only once and can be revoked/rotated immediately.
- API keys cannot operate outside their granted workflows, actions, run scopes, versions, callback policy, or signal restrictions.
- Anonymous external webhook requests fail in required mode.
- User disable/password reset and key revocation take effect without restarting any controller.
- Security audit events cover authentication, authorization, user/key administration, config/restart, and workflow control without secret leakage.
- SQLite and PostgreSQL upgrade, bootstrap, and concurrency tests pass.
- Documentation includes bootstrap, HTTPS, key migration, recovery, rotation, backup, and rollback procedures.

## 29. Security References

Implementation choices should be checked against the current versions of:

- OWASP Password Storage Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html
- OWASP Authentication Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/Authentication_Cheat_Sheet.html
- OWASP Session Management Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html
- OWASP CSRF Prevention Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html
- OWASP Authorization Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html
- OWASP REST Security Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/REST_Security_Cheat_Sheet.html
- OWASP API1 Broken Object Level Authorization: https://owasp.org/API-Security/editions/2023/en/0xa1-broken-object-level-authorization/
- OWASP API5 Broken Function Level Authorization: https://owasp.org/API-Security/editions/2023/en/0xa5-broken-function-level-authorization/
- OWASP Logging Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html

## 30. Decisions to Confirm Before Implementation

The plan recommends defaults for these product choices; they should be explicitly approved before Phase 1:

1. Developers can create and manage only their own workflow-scoped API keys; admins manage all keys.
2. Observers can read workflow inputs, outputs, scripts, and connector metadata, consistent with a global read-only role.
3. API-key run access defaults to runs created by that same key, with definition-wide access available as an explicit broader grant.
4. External webhook authentication supports a temporary `audit` migration mode, then becomes required.
5. The generated initial-admin credential is written to a mode `0600` local file when no deployment secret is provided.
6. Sessions use a 30-minute idle timeout and 8-hour absolute timeout with no remember-me option.
7. Local account recovery is performed by a host-only CLI command, not email recovery.
