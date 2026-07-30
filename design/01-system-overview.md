# System Overview

## Purpose

Orchestra is a durable workflow engine with an embedded browser control plane. It lets
operators define versioned workflows, execute them through persistent tasks, inspect
history and queues, manage reusable resources, and invoke workflows through an
authorized external API.

The product is packaged as one Go binary containing the compiled React application.
SQLite supports local and single-process deployments. PostgreSQL supports multiple
processes sharing the same durable state.

## System boundary

Orchestra owns:

- workflow definitions, immutable versions, publication, and activation;
- workflow run state, execution context, history, tasks, retries, and signals;
- reusable scripts, JSON Schemas, agents, and MCP connector definitions;
- local users, sessions, role permissions, entitlement overrides, and API keys;
- operator HTTP APIs, the external webhook API, and the embedded control plane;
- node registration and heartbeat data;
- outbound activity calls, completion callbacks, AI provider calls, and MCP calls.

Orchestra does not provide:

- a general-purpose message broker;
- distributed transactions with external systems;
- arbitrary tenant isolation or row-level multi-tenancy;
- an identity-provider integration such as OIDC or SAML;
- a gRPC worker control plane;
- exactly-once execution of external side effects.

## Context diagram

```text
                          +----------------------+
                          | OpenAI / Claude /    |
                          | GitHub Copilot       |
                          +----------^-----------+
                                     |
+-----------+     HTTPS      +-------+--------+      SQL      +-------------+
| Operators |--------------->| Orchestra      |<------------->| SQLite or   |
| Browser   |<---------------| controller     |               | PostgreSQL  |
+-----------+   SPA/JSON/WS   +---+---------+-+               +------^------+
                                  |         |                        |
                     HTTP/MCP     |         | HTTP callbacks         |
                                  v         v                        |
                           +------+---+ +---+---------+              |
                           | External | | Callback    |              |
                           | services | | consumers   |              |
                           +----------+ +-------------+              |
                                                                     |
                                +------------------------------------+
                                |
                         +------v-------+
                         | Orchestra    |
                         | worker nodes |
                         +--------------+
```

Controller and worker nodes use the same binary and, in distributed mode, connect to
the same PostgreSQL database. Workers execute activities directly and therefore need
the database credentials and any integration credentials required by those activities.

## Major components

### CLI and process composition

`cmd/app` owns command parsing and lifecycle. Commands include `serve`, `init`,
`schema`, `users reset-password`, and `version`. `serve` composes the database,
workflow service, authentication service, node heartbeat, worker loop, and HTTP server
according to the selected role.

### HTTP server and API

`internal/server` provides request IDs, trusted-proxy handling, recovery, health,
security headers, request-size limits, logging, API mounting, and SPA delivery.
`internal/api` translates HTTP contracts into `auth` and `workflow` service calls.

### Workflow engine

`internal/workflow` is the domain and persistence layer. It validates definition
documents, creates durable runs and tasks, claims tasks through leases, executes an
activity registry, records events, advances transitions, and exposes resource stores.

### Authentication and authorization

`internal/auth` stores local identities, password hashes, sessions, entitlements,
workflow-scoped API keys, rate-limit buckets, and audit events. Middleware in
`internal/api` authenticates principals and enforces permissions before handlers run.

### Live event bus

`internal/livebus` is an in-process publish/subscribe bus. The WebSocket endpoint
streams its events to browsers. It accelerates UI freshness but is not the durable event
record and currently does not bridge separate controller/worker processes.

### Browser application

`ui/src` is a React 18 application using React Router, TanStack Query, Tailwind CSS,
Monaco, and XYFlow. It is an operator interface over the HTTP API; business rules and
authorization remain server-side.

## Primary end-to-end flows

### Browser request

1. The server applies transport and security middleware.
2. Public routes run directly; protected routes authenticate the session cookie.
3. Unsafe methods validate the CSRF token and exact request origin.
4. Permission middleware checks the effective permission set.
5. The handler validates HTTP input and calls a domain service.
6. The service commits durable state and emits an in-process live event.
7. The handler returns JSON; subscribed browsers invalidate relevant cached queries.

### Workflow execution

1. A published definition version is selected and validated.
2. A workflow instance, initial event, and first pending task are committed together.
3. A worker poll or wake signal claims the task and assigns a lease.
4. The activity executes outside the claim transaction.
5. Completion, delay, signal wait, retry, or terminal failure is committed.
6. A next task is scheduled or the workflow is marked terminal.
7. Optional completion callback delivery runs asynchronously after commit.

### External API key request

1. `/ext` middleware parses a Bearer key and authenticates its hash.
2. Rate limits, key status, and expiry are checked.
3. The route checks a workflow/action grant and object scope.
4. The operation records trigger attribution and a security audit event.
5. Unauthorized object-scoped reads are concealed as not found where appropriate.

## Design principles

- **Durability first:** state needed for recovery is written to the database.
- **Single artifact:** backend and frontend versions cannot drift in a release.
- **Explicit boundaries:** HTTP, auth, workflow, database, and UI concerns have separate
  packages even though they ship together.
- **Conservative distributed model:** shared PostgreSQL state coordinates nodes; no
  controller-to-worker RPC protocol is assumed.
- **Least privilege:** routes name permissions, API keys name workflow actions, and
  per-user deny entitlements override role grants.
- **Observable state transitions:** durable workflow events and security audit events
  explain changes independently from process logs.

## Compatibility commitments

- Existing published workflow versions remain executable after new drafts are created.
- API response fields use camelCase and persistent IDs remain opaque strings.
- SQLite remains the default development database.
- No CLI role flags means both controller and worker behavior, preserving the
  all-in-one deployment path.
- Frontend code must not assume access that the backend has not granted.
