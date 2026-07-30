# API and Event Design

## HTTP surfaces

Orchestra exposes three route families:

| Surface | Prefix | Authentication |
|---|---|---|
| Browser/operator API | `/api` | Public exceptions or local session cookie |
| External workflow API | `/ext` | Bearer API key; anonymous only in migration audit mode |
| Browser application | all other paths | Embedded SPA or Vite development proxy |

All API responses are JSON except the WebSocket upgrade. Server middleware limits
request bodies to 4 MiB and protected API routers add a 30-second request timeout.

## Public operator routes

| Method | Route | Purpose |
|---|---|---|
| GET | `/api/health` | Build, service, and health metadata |
| GET | `/api/meta/public` | Non-sensitive application metadata for login |
| POST | `/api/auth/login` | Authenticate a local user and establish a session |

Login still requires an allowed Origin/Referer. Every other `/api` route requires a
valid session.

## Account and security routes

| Area | Routes | Permissions |
|---|---|---|
| Session | current session, logout, password change | authenticated; available during forced password change |
| Session management | list and revoke sessions | `session.manage_own`, with service-level all-session checks |
| Roles and permissions | list catalogs | `user.read` |
| Users | list/get/create/update/reset password | `user.read` or `user.manage` |
| Entitlements | replace user overrides | `entitlement.manage` |
| API keys | list/get/create/update/rotate/revoke | `api_key.*`; ownership rechecked in service |
| Audit | filtered event list | `audit.read` |

Collection APIs use `limit` and `offset`; user lists also support `search`, and audit
lists support actor/action/outcome filters.

## Workflow and resource routes

### Definitions and runs

| Capability | Route shape | Permission |
|---|---|---|
| List/create definitions | `/api/workflow-definitions` | `workflow.definition.read/write` |
| Get definition/version | `/api/workflow-definitions/{id}` and `/versions/{n}` | `workflow.definition.read` |
| Create draft version | `/api/workflow-definitions/{id}/versions` | `workflow.definition.write` |
| Save draft layout | `/api/workflow-definitions/{id}/versions/{n}/layout` | `workflow.definition.write` |
| Publish/activate | version action routes | `workflow.definition.publish` |
| Start run | `/api/workflow-definitions/{id}/start` | `workflow.run.start` |
| List/get/history runs | `/api/workflows...` | `workflow.run.read` |
| Signal/cancel run | `/api/workflows/{id}/signals` or `/cancel` | `workflow.run.control` |
| List/control tasks | `/api/workflows/tasks...` | `workflow.task.read/control` |
| Operations feed | `/api/workflows/events` | `operation.read` |

Run, history, operations, and task collections support status and pagination parameters
as appropriate. Invalid numeric pagination returns a client error rather than silently
changing the query.

### Reusable resources

Scripts, JSON Schemas, agents, and MCP servers expose list, create, get, update, delete,
and export operations under `/api`. Agent-to-MCP attachment and connector exploration
are explicit subresources. Reads require `resource.read`; writes require
`resource.write`.

Import is a two-step API: analyze a bundle, then apply it with an explicit override set.
This lets the UI display conflicts before mutation. Permissions are `import.analyze` and
`import.apply`.

### AI features

`/api/ai/models`, `/enhance-prompt`, `/script-assist`, and `/validate-script` require
`ai.use`. The prompt enhancement contract sends current prompt, mandatory enhancement
message, provider, and model. The service validates both text inputs again.

### Cluster, settings, and administration

- `/api/nodes` and `/nodes/healthcheck` use cluster read/control permissions.
- `/api/config/raw` is mounted only for an all-in-one controller/worker process; read
  and write require settings permissions.
- `/api/admin/restart` requires `server.restart`.
- Metadata and example routes require `settings.read`.

## External workflow API

| Method | Route | Grant action |
|---|---|---|
| POST | `/ext/webhook/{definitionId}/start` | `start` |
| POST | `/ext/webhook/{workflowId}/signal` | `signal` |
| GET | `/ext/signal/{workflowId}` | `status.read` |
| GET | `/ext/result/{workflowId}` | `result.read` |

Start input is the JSON request body. Optional version comes from the `version` query
or `X-Workflow-Version`; optional callback comes from `X-Callback-URL`. A callback must
match the configured regular-expression allowlist and the key grant must permit callback
use. Pinned versions require an explicit grant flag.

Signal requests contain `name` and optional JSON `payload`. Signal-name restrictions
and instance scope are enforced. `own` scope means the target run must have been started
by the same API key. Unauthorized scoped run reads/signals return not found to reduce
resource enumeration.

Result reads return `202 Accepted` until a run is completed, failed, or canceled, then
return status, output, context, and completion timestamp.

`webhook.authenticationMode = required` requires a valid Bearer key. `audit` temporarily
allows anonymous external requests while still recording an anonymous principal. Audit
mode is a migration control, not a secure production default.

## Request and response conventions

- JSON field names use camelCase.
- Resource IDs are opaque and must not be parsed by clients.
- Timestamps are RFC 3339-compatible UTC values.
- Create operations generally return `201 Created`; reads/updates return `200 OK`.
- Long-running workflow execution is asynchronous; start returns the run identity.
- Empty collections are returned as arrays, not `null`, where services normalize them.

Errors use a JSON API error with an HTTP status, stable code for auth/security paths,
and a human-readable message. Handlers translate not found, invalid input, forbidden,
conflict, and workflow publication errors into appropriate statuses. Internal provider
or downstream failures are not exposed with credentials.

## WebSocket protocol

`GET /api/ws` requires `operation.read` and an authenticated browser session. The
server subscribes the connection to the process-local live bus and sends JSON events:

```json
{
  "type": "workflow.updated",
  "entity": "workflow",
  "entityId": "wf_...",
  "payload": {},
  "timestamp": "2026-01-01T00:00:00Z"
}
```

Connection-ready and heartbeat events maintain liveness. The frontend watchdog closes
silent connections and reconnects with bounded exponential backoff.

Event entities include definition, workflow, task, operation, queue, script, agent,
MCP server, node, and health. Events are invalidation hints; payload shape can vary by
event type and consumers must fetch canonical REST state.

Each subscriber has a 64-event buffer. When it fills, the bus discards the oldest event
and attempts to send a `missed_events` notification. Clients receiving that notice must
refetch relevant REST queries.

## Event consistency

Durable workflow events and live bus events have different guarantees:

| Property | Workflow event table | WebSocket live event |
|---|---|---|
| Durable | Yes | No |
| Ordered | Per workflow sequence | Per process publication order |
| Replayable | Through history API | No |
| Cross-process | Shared database | No |
| Purpose | Domain history | UI cache freshness |

Clients must never derive authoritative workflow state solely from WebSocket delivery.

## API evolution rules

- Additive response fields are compatible; clients ignore unknown fields.
- New required request fields require coordinated UI/API rollout in the same binary.
- Route permission changes are security-sensitive and require matrix tests.
- Persisted action names and event types should remain stable once externally consumed.
- Breaking external API changes should use a new route version rather than silently
  changing `/ext` semantics.
