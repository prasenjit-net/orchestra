# Persistence Model

## Database support

Orchestra uses `database/sql` with two dialects:

| Dialect | Driver | Intended use |
|---|---|---|
| SQLite | `modernc.org/sqlite` | Local development and all-in-one deployments |
| PostgreSQL | `pgx` stdlib | Shared-state controller/worker deployments |

SQLite opens one connection, enables WAL, waits up to five seconds on busy locks, and
enforces foreign keys. PostgreSQL uses a pool capped at 25 open and 5 idle connections
and verifies connectivity with a five-second startup ping.

Parameterized SQL is written with `?` placeholders and rebound to PostgreSQL `$N`
placeholders by the dialect. Values remain driver parameters.

## Schema ownership

Workflow schema definitions live in `internal/workflow/dialect.go`; SQLite migration
logic also exists in `service_schema.go`. Authentication schema lives in
`internal/auth/schema.go`.

- SQLite creates and incrementally upgrades tables during workflow/auth service startup.
- PostgreSQL never auto-creates workflow tables during normal service startup. Operators
  run `orchestra schema --create` before serving traffic.
- `orchestra schema` prints the complete dialect-specific DDL when `--create` is absent.
- DDL and migrations are idempotent where the database supports that behavior.

Schema changes must update complete DDL, migration paths, schema contract tests, and
this document together.

## Entity relationship map

```text
workflow_definitions 1---* workflow_definition_versions
        |
        +---* workflow_instances 1---* workflow_events
                               | 1---* workflow_tasks
                               + 1---* workflow_signals

agents *---* mcp_servers       through agent_mcp_servers

users 1---* user_entitlements
  |   1---* sessions
  +---* api_keys 1---* api_key_workflow_grants

security_audit_events          append-oriented audit records
security_rate_limits           expiring rate-limit buckets
nodes                          process registration and heartbeat
scripts, json_schemas          reusable resources
```

Workflow grant references to definitions intentionally do not use a database foreign
key, because auth and workflow schemas are maintained as separate domains and imported
resource IDs must remain portable.

## Workflow tables

### `workflow_definitions`

Stores the stable definition ID, display metadata, aggregate status, active version,
and timestamps. The active version pointer chooses the default version for new runs.

### `workflow_definition_versions`

Composite key `(definition_id, version)`. Stores status, complete JSON document,
creation/update/publication times, and optional source version. Version documents are
self-contained snapshots so a run does not depend on mutable editor state.

### `workflow_instances`

Stores one run and its pinned definition version. Important fields include:

- status and current step/activity;
- last durable event sequence;
- last error, last output, and full context JSON;
- pending signal count and next run time;
- callback URL and callback delivery status;
- trigger source, principal type, and principal ID;
- creation/update timestamps.

Run state is a materialized snapshot for efficient reads. `workflow_events` provides the
ordered history but is not replayed to reconstruct the instance during normal operation.

### `workflow_events`

Composite identity is workflow ID plus monotonically increasing sequence. Each row has
an event type, JSON payload, and timestamp. Events are appended in the same transaction
as the state change they describe.

### `workflow_tasks`

Autoincrement/identity task ID plus workflow/step identity, activity, status, attempts,
maximum attempts, schedule time, last error, lease owner/expiry, persisted activity
state, executing node, and timestamps.

Indexes support runnable queue scans by `(status, run_at)` and per-workflow task views.
Task payload is resolved from the pinned definition and instance context at execution
time rather than duplicated into the task row.

### `workflow_signals`

Stores workflow ID, signal name, payload JSON, processing status, and timestamps.
Signals are durable inputs and can be correlated with waiting tasks and history.

## Resource tables

### `scripts`

Stores opaque ID, name, description, language, source, exports JSON, and timestamps.
Workflow steps reference a saved script by ID; import/export preserves resource IDs.

### `agents`

Stores provider, model, system prompt, token/temperature settings, display metadata, and
timestamps. Secrets are not stored in agent rows; provider credentials come from server
configuration.

### `mcp_servers`

Stores connector name, URL, headers JSON, enabled state, discovered tools JSON,
exploration time, and timestamps. Header values may contain credentials and therefore
the database must be treated as sensitive.

### `agent_mcp_servers`

Join table keyed by `(agent_id, server_id)`. Foreign keys cascade when either resource
is deleted.

### `json_schemas`

Stores named JSON Schema documents and timestamps. Definitions may reference schemas at
the run input and final output boundaries.

### `nodes`

Stores process identity, role, address, capabilities JSON, advertised concurrency,
version, hostname, last heartbeat, and registration time. Online status is derived at
read time and is never persisted.

## Security tables

### `users`

Stores normalized unique username, display name, password hash, role, status,
must-change-password flag, failed-login state, lock time, password/login timestamps,
authorization version, creator, and timestamps.

### `user_entitlements`

One row per user and permission with `allow` or `deny`. These override role grants.
Changing entitlements increments the user's authorization version to invalidate old
sessions.

### `sessions`

Stores only the session token hash plus CSRF token, user ID, idle and absolute expiry,
last-seen time, password and authorization versions captured at login, revocation data,
source IP, and a user-agent hash.

### `api_keys`

Stores key prefix, secret hash, owner, lifecycle state, expiry, usage metadata,
rotation lineage, and display metadata. The full secret is returned only at creation or
rotation.

### `api_key_workflow_grants`

Composite key `(api_key_id, workflow_definition_id, action)`. Stores instance scope,
pinned-version and callback permissions, optional signal-name restrictions, and
creation time.

### `security_audit_events`

Append-oriented records containing request correlation, actor, action, resource,
outcome, source, user agent, metadata JSON, and occurrence time. Time, actor, and action
indexes support operational filtering.

### `security_rate_limits`

Hashed bucket key, bucket type, window start, count, block time, and expiry. Expired
buckets are removed by periodic auth cleanup.

## Transaction boundaries

Transactions protect these critical operations:

- definition and initial version creation;
- draft creation, publication, and activation;
- workflow start plus first task and events;
- task claim;
- task completion plus context, events, next task, and instance update;
- retry/failure, signal delivery, and operator task actions;
- API key creation/update/rotation with grants;
- user and entitlement changes that must preserve an administrator.

Network calls and activity execution do not occur inside database transactions. This
keeps lock duration bounded but creates an at-least-once side-effect model.

## Time and JSON representation

Application timestamps are normalized to UTC and persisted as text representations
compatible across SQLite and PostgreSQL. Boolean compatibility fields use integers in
portable DDL. Structured domain content is stored as JSON text and decoded at service
boundaries.

## Backup and restoration implications

A consistent database backup contains workflow state and security state. It may also
contain MCP header secrets and password/API-key hashes. Backups require encryption and
the same access controls as production credentials.

SQLite backups must account for WAL files or use a database-aware snapshot. PostgreSQL
backups should include schema and data from one transactionally consistent point.
Restoring the database does not restore external side effects or provider credentials
stored only in configuration.
