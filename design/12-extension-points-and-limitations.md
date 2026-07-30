# Extension Points and Limitations

## Purpose

This document separates supported extension paths from current constraints. It prevents
future design work from assuming that configuration fields or old proposals are already
implemented.

## Supported extension points

### Activities

Implement `workflow.Activity`, register a stable descriptor/name, and add UI property
controls when authoring support is required. The result contract supports immediate
completion, durable delay, durable signal wait, persisted state, and context updates.

Activity names and input/output JSON become persisted compatibility contracts.

### AI providers

The internal AI request/response model supports messages, system prompt, model,
temperature, token limit, tool definitions, tool calls, and usage. A provider adapter
must implement model discovery and completion translation, credential validation,
timeouts, safe errors, and tool-history mapping.

Provider identifiers are persisted on agents and therefore require migration/aliasing if
renamed.

### Reusable resources

Scripts, agents, connectors, and schemas follow CRUD stores plus import/export models.
New resource types should define ownership, permission mapping, dependency traversal,
bundle representation, and live invalidation entity.

### HTTP API

Routes are composed in `internal/api/router.go` and should name one permission at the
boundary. Handler code owns HTTP validation/translation; domain services own invariants
and object authorization. New external operations need explicit API key grant actions.

### UI pages

Add a route, navigation item, typed API client, TanStack Query keys, permission gate, and
live-event invalidation strategy. Large editors should be lazy-loaded and remain inside
the existing layout/theme system.

### Database dialects

The current portability layer supports SQLite and PostgreSQL placeholder/timestamp/DDL
differences. Adding a dialect requires connection policy, complete DDL, migrations,
schema tests, claim semantics, and transaction compatibility.

## Architectural decision rules

Before adding infrastructure, prefer:

1. Durable database state over process memory for correctness.
2. Existing service boundaries over new cross-package shortcuts.
3. Explicit versioned contracts over inference from UI state.
4. Permission and object checks close to the resource operation.
5. Bounded retries/timeouts over unbounded background work.
6. Additive API evolution over breaking changes.
7. One clear implementation design over multiple unlabelled proposal documents.

## Current distributed-runtime limitations

### Process-local live bus

WebSocket events do not cross process boundaries. A worker commit may not notify a
browser connected to a controller process. A production-grade enhancement would use a
database-backed notification/outbox or message broker while preserving REST as the
canonical state source.

### Sequential worker execution

One workflow service executes tasks sequentially, up to 16 per pass.
`node.maxConcurrentTasks` is metadata only. A worker-pool design must define lease
renewal, cancellation, shutdown draining, per-activity limits, and testable backpressure
before enabling concurrency.

### Claim contention

PostgreSQL claims do not currently use `FOR UPDATE SKIP LOCKED`. Conditional update
prevents two workers from owning one pending row, but contenders can select the same row
and reduce throughput. Any optimization must retain SQLite behavior and conditional
ownership safety.

### No lease renewal

Long-running activities can outlive the lease and be duplicated. A renewal design needs
task-scoped heartbeat ownership and must stop renewing immediately on cancellation or
loss of ownership.

### Shared secrets on workers

Workers connect directly to the database and integrations. There is no DB-free worker,
gRPC gateway, or controller config sync. Remote/untrusted worker environments are not a
supported security boundary.

### No durable callback outbox

Completion callbacks are asynchronous best effort after commit. Durable delivery needs
an outbox table, retry policy, idempotency key, status history, and operator controls.
Callbacks currently cover successful completion only, not failure or cancellation.

### Timed signal waits do not self-resume

Signal waits persist their timeout in `run_at`, and the scheduler selects expired
waiting rows. The conditional claim update currently accepts only `pending` status, so
an expired `waiting` task is not promoted to `running`. A fix needs an atomic transition
for due waiting tasks plus timeout-focused workflow tests.

## Current security/product limitations

- Local username/password identity only; no MFA or federated SSO.
- Global installation scope; no tenant boundary or per-workflow user ACL.
- Audit data is not cryptographically tamper-evident.
- Connector headers are stored as plain JSON in the application database.
- No centralized secret-provider abstraction.
- API keys authorize only the four implemented external workflow actions.
- No automated data retention for completed workflow history.

## Configuration mismatches requiring care

### `workflow.scriptEnabled`

The DB-backed script activity is registered unconditionally. Until code changes, this
flag is not an execution kill switch.

### `webhook.enabled`

The external router is mounted regardless of this parsed field. Network controls and
`authenticationMode = required` remain necessary.

### `node.maxConcurrentTasks`

The value is advertised in the node table and UI but does not control a semaphore.

These mismatches should either be implemented fully or removed in a future compatibility
change. Documentation and UI must not imply enforcement that does not exist.

## Frontend quality limitations

There is no automated component or end-to-end browser test suite. Monaco and workflow
designer behavior depend on manual visual verification plus TypeScript/lint/build checks.
Introducing tests should prioritize login/password change, permission gating, workflow
layout-versus-semantic save, version publication, agent enhancement, and API key grants.

## Observability limitations

There is no Prometheus endpoint, OpenTelemetry tracing, or built-in alerting. Request IDs,
structured logs, durable workflow events, tasks, audit events, and node heartbeats are
the available primitives.

## Deferred designs

Potential future capabilities should be tracked in issues/ADRs with explicit status and
acceptance criteria, for example:

- cross-process event distribution;
- concurrent worker pool and lease renewal;
- durable external-effect outbox;
- OIDC/MFA and secret-manager integration;
- tenant/workflow-scoped user authorization;
- metrics and tracing;
- browser test infrastructure;
- workflow archival/retention.

Do not add mutually exclusive root-level `*-plan.md` files. Once a direction is accepted,
record the decision in an ADR under `design/decisions/` and update the canonical design
documents when implementation lands.

## ADR format

Future architecture decisions should use a compact record:

```text
# ADR-NNN: Decision title
Status: proposed | accepted | superseded
Date: YYYY-MM-DD

## Context
## Decision
## Consequences
## Alternatives considered
## Implementation and migration
```

An accepted ADR explains why; the numbered design documents continue to explain how the
current system works.
