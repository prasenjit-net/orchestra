# Runtime Architecture

## Process roles

Every `orchestra serve` process can enable two independent roles:

| Role | Responsibilities |
|---|---|
| Controller | HTTP API, embedded UI, authentication service, public metadata, WebSocket stream, config editing, and restart control |
| Worker | Workflow polling, lease recovery, activity execution, and result persistence |
| All | Both controller and worker in one process; this is the default |

Role selection order is:

1. Explicit `--controller` or `--worker` flags.
2. `node.controller` and `node.worker` configuration.
3. If both resolve false, enable both for backward compatibility.

Flags are additive. `--controller --worker` is equivalent to the default all-in-one
mode. A worker-only process exposes only a small health server on `node.healthAddr`.

## Startup sequence

`cmd/app/serve.go` performs startup in this order:

1. Load and validate configuration; apply the `--port` override.
2. Resolve controller and worker roles.
3. Create structured logging, build metadata, and the in-process live bus.
4. Open the configured database and resolve its dialect.
5. Create the workflow service and activity registry.
6. On controller nodes, create the auth service and bootstrap the initial admin if no
   users exist.
7. Run immediate auth cleanup and schedule hourly cleanup on controller nodes.
8. Register the node and start its heartbeat loop.
9. Start the task poller when the worker role is enabled.
10. Start the main HTTP server for a controller, or the minimal health server for a
    worker-only node.
11. Wait for server error, restart request, SIGINT, or SIGTERM.

Startup fails when required configuration, schema, or database connectivity is
unavailable. PostgreSQL schema creation is deliberately operator-managed.

## Runtime object graph

```text
serve command
  +-- sql.DB
  +-- livebus.Bus
  +-- workflow.Service
  |     +-- activity registry
  |     +-- AI provider client
  |     +-- worker wake channel
  +-- auth.Service                 controller only
  +-- HTTP server                  controller only
  |     +-- /api router
  |     +-- /ext router
  |     +-- embedded SPA or Vite proxy
  +-- worker health HTTP server    worker-only
```

The workflow and auth services share the same `sql.DB`. In all-in-one mode they also
share one live bus with the WebSocket handler.

## Worker loop

`workflow.Service.Start` starts one goroutine with a poll ticker and a buffered wake
channel. Each pass:

1. Requeues running tasks whose lease has expired.
2. Calls `RunOnce` up to 16 times.
3. Stops the pass as soon as no runnable task is found or an error occurs.

Newly scheduled work sends a non-blocking wake signal so execution need not wait for
the next poll interval. Duplicate wake signals are intentionally coalesced.

### Current concurrency behavior

Task execution inside one service is sequential. `node.maxConcurrentTasks` is recorded
as node metadata but is not currently used to create a worker pool. Multiple worker
processes can execute concurrently, but the claim algorithm does not use PostgreSQL
`FOR UPDATE SKIP LOCKED`; it relies on a conditional update to let only one contender
claim a selected pending task.

This is an important current limitation, not a promised concurrency control.

## Node identity and health

At startup the process upserts a row in `nodes` containing:

- stable or generated node ID;
- role (`controller`, `worker`, or `all`);
- advertised address;
- registered activity names;
- configured maximum concurrency;
- binary version and hostname;
- registration and last-seen timestamps.

The node ID also becomes the workflow service's lease owner and `executed_by` value.
A heartbeat updates `last_seen_at` every `node.health.heartbeatInterval`. Read APIs
derive online/offline state by comparing that timestamp with
`node.health.offlineThreshold`. Graceful shutdown removes the row; crashed nodes remain
visible as offline.

The controller health-check action probes each registered node's advertised `/livez`
endpoint. Database heartbeat status and active HTTP probe status answer different
questions and should both be considered during diagnosis.

## HTTP runtime

Controller nodes use Go's `http.Server` with configured read, write, idle, and shutdown
timeouts plus a 1 MiB maximum header size. Router middleware adds:

- request IDs;
- trusted reverse-proxy client address resolution;
- panic recovery;
- `/livez` heartbeat response;
- browser and transport security headers;
- a 4 MiB request body limit;
- structured completion logging.

In production, the SPA is served from `ui/dist` embedded by `ui_embed.go`. In `--dev`
mode, non-API traffic is proxied to Vite and browser routes fall back to Vite's index.

## Shutdown and restart

SIGINT, SIGTERM, or an admin restart request cancels worker and maintenance contexts.
HTTP servers receive graceful shutdown with `server.shutdownTimeout`. The node row is
removed through deferred cleanup, owned resources are closed, and a restart request
re-executes the current binary.

Activities already running receive cancellation through the worker context. External
systems may still have accepted a request before cancellation; recovery therefore uses
leases and idempotency rather than assuming cancellation reverses side effects.

## Multi-node topology

The implemented distributed topology is symmetric shared-database coordination:

```text
load balancer
  +-- controller A --+
  +-- controller B --+-- PostgreSQL -- worker A
                     |              +- worker B
                     +--------------+- worker C
```

All nodes require database access. Worker nodes also require activity credentials and
network access to downstream systems. There is no worker gRPC channel, config sync, or
controller-owned dispatch queue.

### Distributed caveats

- The live bus is process-local. Events emitted by a worker are not forwarded to a
  WebSocket connected to another controller. Browser queries still observe durable
  state when they refetch.
- Node heartbeats are shared through the database and are visible to every controller.
- Session and authorization state are database-backed and can be shared by controllers.
- In-process wake signals do not wake other worker processes; their poll interval bounds
  discovery latency.
- SQLite's single connection and local file model are for all-in-one use, not a shared
  multi-node deployment.

## Ownership boundaries

| Concern | Owner |
|---|---|
| Process lifecycle and role composition | `cmd/app` |
| Database connection policy | `internal/database` |
| HTTP transport and SPA | `internal/server` |
| API translation and middleware | `internal/api` |
| Durable workflow execution | `internal/workflow` |
| Identity and policy | `internal/auth` |
| In-process notifications | `internal/livebus` |
