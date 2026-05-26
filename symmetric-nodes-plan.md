# Orchestra — Symmetric Nodes (Plan C)

> **Alternative to** `controller-worker-plan.md` (Plan A — workers with direct DB,
> HTTP-ping health) and `worker-grpc-plan.md` (Plan B — workers via gRPC, no DB).
> This document describes Plan C: all nodes are identical binaries sharing one
> PostgreSQL instance. The only difference between a controller and a worker is
> which capabilities are switched on at startup.

---

## 1. Core Idea

Every node runs the same binary, connects to the same PostgreSQL database, and uses
the existing row-level lease mechanism for task distribution — exactly as the engine
works today in single-process mode. The two startup flags simply decide which
subsystems are active on that node:

| Flag | HTTP API + UI | Task poller |
|---|---|---|
| *(neither — default)* | ✓ | ✓ |
| `--controller` | ✓ | — |
| `--worker` | — | ✓ |
| `--controller --worker` | ✓ | ✓ |

There is no inter-node communication protocol. No gRPC. No HTTP pings. PostgreSQL
is the only shared resource and the only coordination channel.

---

## 2. What "Controller" and "Worker" Mean Here

### Controller node (`--controller`)
- Runs the HTTP API and the embedded React UI
- Handles workflow definition CRUD, triggers, signal delivery, task management
- Does **not** run the task poller — it submits work to the DB but does not execute it
- Any number of controller nodes can run behind a load balancer; they are stateless at
  the application layer (all state is in PostgreSQL)

### Worker node (`--worker`)
- Headless — no HTTP server, no UI
- Runs the task poller: calls `requeueExpiredTasks` and `claimNextTask` in a loop
- Executes activities; writes results directly to PostgreSQL
- Registers itself in the `workers` table on startup; updates a last-seen timestamp
  on every poll tick; deregisters on graceful shutdown
- Optionally exposes a minimal `/livez` health endpoint for Kubernetes probes

### Both flags (default)
- Identical to current single-process behaviour
- HTTP API + UI + task poller all run in one process
- Suitable for development, small deployments, and proof-of-concept

---

## 3. CLI Interface

```
orchestra serve                         # both controller and worker (default)
orchestra serve --controller            # API + UI only; no task execution
orchestra serve --worker                # headless; task execution only
orchestra serve --controller --worker   # explicit both; same as default
```

The flags are additive — specifying both is the same as specifying neither.
Specifying neither defaults to both, preserving full backward compatibility.

Flag precedence (highest to lowest): CLI flags → config file → defaults.

---

## 4. Configuration

```toml
[node]
# Stable identity for this node. Auto-generated on each start if empty.
# Set explicitly in production for stable worker entries in the DB.
id = ""

# Enable the HTTP API and embedded UI on this node.
# Equivalent to --controller flag.
controller = true

# Enable the task poller and activity executor on this node.
# Equivalent to --worker flag.
worker = true

[worker]
# Maximum number of tasks to execute concurrently.
maxConcurrentTasks = 4

# Optional HTTP address for a minimal liveness endpoint.
# Useful for Kubernetes liveness probes on headless worker nodes.
# Empty = disabled.
healthAddr = ""  # e.g. "0.0.0.0:8081"
```

Equivalent environment variables:

```sh
APP_NODE_ID=worker-us-east-1a
APP_NODE_CONTROLLER=false
APP_NODE_WORKER=true
APP_WORKER_MAXCONCURRENTTASKS=8
APP_WORKER_HEALTHADDR=0.0.0.0:8081
```

No database URL, no controller URL, no API keys are needed on worker-only nodes beyond
what is already in the standard `[workflow]` section — workers are full peers and read
the same config.

---

## 5. Task Distribution

Unchanged from the current implementation. All nodes that have `worker = true` run the
same `runWorkerPass` loop and compete for tasks via PostgreSQL row-level leasing:

```sql
-- claimNextTask (Postgres path gains FOR UPDATE SKIP LOCKED)
UPDATE workflow_tasks
SET    status           = 'running',
       lease_owner      = '<node-id>',
       lease_expires_at = now() + interval '30 seconds'
WHERE  id = (
    SELECT id FROM workflow_tasks
    WHERE  status = 'pending' AND run_at <= now()
    ORDER BY run_at ASC
    LIMIT  1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;
```

`FOR UPDATE SKIP LOCKED` ensures multiple workers claim different tasks atomically.
`requeueExpiredTasks` (also running on every worker node) automatically recovers tasks
whose lease has expired — this is the crash-recovery mechanism, and it requires no
coordination beyond the DB.

No changes to task scheduling logic, lease duration, or retry behaviour.

---

## 6. Worker Health and Registration

Because all nodes have direct database access, worker health can be tracked entirely
through the DB — no HTTP ping, no gRPC heartbeat.

### `workers` table

```sql
CREATE TABLE workers (
    id              VARCHAR(64)  PRIMARY KEY,
    role            VARCHAR(32)  NOT NULL DEFAULT 'worker',  -- 'controller' | 'worker' | 'all'
    address         VARCHAR(255) NOT NULL DEFAULT '',        -- HTTP address if controller is on
    capabilities    TEXT         NOT NULL DEFAULT '[]',      -- JSON array of activity names
    max_concurrent  INT          NOT NULL DEFAULT 4,
    last_seen_at    TIMESTAMPTZ  NOT NULL,
    registered_at   TIMESTAMPTZ  NOT NULL
);
```

### Lifecycle

1. **On startup**: `INSERT OR REPLACE INTO workers (...)` with `last_seen_at = now()`
2. **On every `runWorkerPass` tick**: piggyback a cheap `UPDATE workers SET last_seen_at = now() WHERE id = ?`  
   — no extra goroutine, no extra timer, no extra config knob. The poll interval is already the natural heartbeat cadence.
3. **On graceful shutdown**: `DELETE FROM workers WHERE id = ?`
4. **If a worker crashes** without cleanup: its row remains, but `last_seen_at` stops
   advancing. Any node reading the table can infer offline status as
   `last_seen_at < now() - (3 × pollInterval)`. Its leased tasks are reclaimed by
   `requeueExpiredTasks` within one `leaseDuration`.

Controller nodes (with the UI) expose `GET /api/workers` which reads this table and
annotates each row with a derived `status`:

```
status = "online"   if last_seen_at >= now() - 3 × pollInterval
status = "offline"  otherwise
```

No in-memory state on the controller. No separate health protocol. The DB is the
source of truth for worker health, same as it is for everything else.

---

## 7. Live Events Across Nodes

### Single controller + N workers (most common)

Workers write task results to PostgreSQL and call `pg_notify('orchestra_events', payload)`
in the same transaction. The controller listens with `LISTEN orchestra_events` on a
dedicated connection and relays received notifications to its local in-process
`livebus.Bus`, which fans events out to connected WebSocket clients.

```
Worker node                 PostgreSQL              Controller node
────────────────────────────────────────────────────────────────────
completeTask()
  → tx.Commit()
  → NOTIFY orchestra_events ──────────────────────►
                                                   LISTEN goroutine
                                                     → livebus.Publish()
                                                     → WebSocket fan-out
```

### Multiple controllers

Each controller node runs its own `LISTEN` goroutine. All receive the same
notifications and all relay them to their own WebSocket subscribers independently.
Browser clients connected to any controller get the same events. No cross-controller
coordination is needed.

### `role = "all"` (default, single process)

The in-process `livebus.Bus` is used directly. `pg_notify` / `LISTEN` is not set up,
avoiding the extra connection. This is the zero-overhead path for single-process use.

---

## 8. Changes to `serve.go`

```go
func runServe(cfg config.Config, ...) {
    isController := cfg.Node.Controller
    isWorker     := cfg.Node.Worker
    // default: both
    if !isController && !isWorker {
        isController, isWorker = true, true
    }

    svc, _ := workflow.NewService(cfg.Workflow, logger, live)

    if isWorker {
        svc.Start(ctx)              // task poller + requeueExpiredTasks
        svc.RegisterWorker(ctx)     // writes to workers table; updates on each tick
    }

    if isController {
        if cfg.Workflow.DatabaseDriver == "postgres" {
            startPGNotifyListener(ctx, cfg, live)
        }
        httpServer := buildHTTPServer(cfg, svc, live, restartCh)
        httpServer.ListenAndServe()
    } else {
        // Worker-only: optional lightweight health endpoint
        if cfg.Worker.HealthAddr != "" {
            startHealthServer(cfg.Worker.HealthAddr)
        }
        <-ctx.Done()   // block until signal
    }
}
```

---

## 9. Concurrency on Worker Nodes

The current `runWorkerPass` executes up to 16 tasks sequentially per tick. For
dedicated worker nodes, this becomes a semaphore-gated goroutine pool:

```
poll tick / wake
    │
    ▼
while semaphore has free slot AND DB has a pending task:
    slot = acquire semaphore
    task = claimNextTask()
    go func() {
        defer release(slot)
        executeTask(task)        // activity.Execute(...)
        writeResult(task, ...)   // completeTask / handleTaskFailure / etc.
        notifyWorker()           // wake loop for next task
    }()
```

`maxConcurrentTasks` sets the semaphore size. The loop keeps claiming and launching
tasks until the semaphore is saturated or no more pending tasks exist.

Each task goroutine is fully independent — it holds no shared state beyond its own
database rows, so concurrent execution is safe with no additional locking.

---

## 10. Kubernetes Deployment

```yaml
# Controllers — serve HTTP + UI; no task execution
apiVersion: apps/v1
kind: Deployment
metadata:
  name: orchestra-controller
spec:
  replicas: 2
  template:
    spec:
      containers:
      - name: orchestra
        image: orchestra:latest
        args: ["serve", "--controller"]
        env:
        - name: APP_WORKFLOW_DATABASEURL
          valueFrom:
            secretKeyRef:
              name: orchestra-db
              key: url
        ports:
        - containerPort: 8080
        readinessProbe:
          httpGet: { path: /livez, port: 8080 }
---
# Workers — headless; task execution only
apiVersion: apps/v1
kind: Deployment
metadata:
  name: orchestra-worker
spec:
  replicas: 5          # scale independently; HPA can drive this
  template:
    spec:
      containers:
      - name: orchestra
        image: orchestra:latest
        args: ["serve", "--worker"]
        env:
        - name: APP_WORKER_MAXCONCURRENTTASKS
          value: "8"
        - name: APP_WORKER_HEALTHADDR
          value: "0.0.0.0:8081"
        - name: APP_WORKFLOW_DATABASEURL
          valueFrom:
            secretKeyRef:
              name: orchestra-db
              key: url
        livenessProbe:
          httpGet: { path: /livez, port: 8081 }
```

Both deployments share the same `orchestra-db` secret. Workers need the same DB
credentials as controllers — this is the trade-off versus Plan B.

---

## 11. Implementation Phases

### Phase 1 — `--controller` / `--worker` flags (no behaviour change by default)
1. Add `node.controller` / `node.worker` booleans to config + `--controller` / `--worker` CLI flags
2. In `serve.go`: when `node.controller = false`, skip starting the HTTP server; when `node.worker = false`, skip calling `svc.Start()`
3. Default (neither flag): both enabled — identical to current behaviour
4. Add optional health server on `worker.healthAddr` for worker-only nodes

### Phase 2 — Worker registration table
1. Add `workers` table to DDL for both SQLite and PostgreSQL dialects
2. `RegisterWorker` called in `NewService` when worker is enabled
3. `DeregisterWorker` called in `Close` / on graceful shutdown
4. Piggyback `last_seen_at` update inside `runWorkerPass` — no new goroutine or timer

### Phase 3 — `GET /api/workers` + Dashboard panel
1. `ListWorkers` reads the `workers` table; annotates `status` from `last_seen_at` vs `now()`
2. `GET /api/workers` endpoint on controller nodes
3. Worker roster panel on the Dashboard: node ID, role, status, capabilities, max concurrent, last seen

### Phase 4 — Concurrent task execution
1. Replace sequential-up-to-16 loop with semaphore-gated goroutine pool
2. Controlled by `worker.maxConcurrentTasks`

### Phase 5 — PostgreSQL LISTEN / NOTIFY live events
1. Extract `livebus.Bus` to an interface with an in-process and a PG-backed implementation
2. Controller nodes with PostgreSQL driver start a `LISTEN orchestra_events` goroutine
3. Worker nodes call `pg_notify` inside each state-change transaction

### Phase 6 — `FOR UPDATE SKIP LOCKED` in PostgreSQL
1. `claimNextTask` appends `FOR UPDATE SKIP LOCKED` when dialect is postgres
2. Removes application-level retry contention under high worker concurrency

---

## 12. Comparison Across All Three Plans

| Dimension | Plan A (DB-polling + HTTP ping) | Plan B (gRPC, no worker DB) | Plan C (symmetric nodes) ← this plan |
|---|---|---|---|
| Worker DB access | Yes | **No** | Yes |
| Worker config needed | Full config + DB creds | Controller URL only | DB creds (same config as controller) |
| Inter-node protocol | HTTP ping (health only) | gRPC (tasks + config) | **None — PostgreSQL only** |
| New dependencies | None | gRPC + protoc | **None** |
| Task dispatch | Worker polls DB | Controller pushes | Worker polls DB |
| Task dispatch latency | Poll interval (1 s) | Near-zero (push) | Poll interval (1 s) |
| Worker health tracking | Controller in-memory (ping) | gRPC connection state | DB `last_seen_at` (poll tick) |
| In-flight recovery | Lease expiry | Immediate re-dispatch | Lease expiry |
| Controller is SPOF for tasks | No | Yes | **No** |
| Multiple controllers | ✓ | ✓ | ✓ |
| Operational complexity | Medium | High | **Low** |
| Code change from today | Moderate | Large (new gRPC layer) | **Minimal** |
| Best suited for | Trusted clusters, moderate scale | Restricted envs, edge, SaaS | **All sizes; simplest ops** |

---

## 13. What Does NOT Change

| Area | Status |
|---|---|
| `DefinitionDocument` JSON format | Unchanged |
| Activity interface (`Execute`) | Unchanged |
| External webhook API (`/ext/*`) | Served by controller nodes only |
| SQLite single-process deployments | `--controller --worker` (default) = exactly today |
| Worker spin-up / spin-down | Out of scope; handled by Kubernetes or operator |
| Authentication / authorisation | Deferred to a separate session |
| Migration tooling | Covered by existing `orchestra schema` command |

---

## 14. Open Questions / Future Considerations

- **Controller-only nodes in small deployments**: A single `--controller` node with no
  `--worker` node is a valid but idle setup — workflows trigger but never execute. The
  UI could warn when no worker nodes have been seen recently in the `workers` table.
- **Mixed flag deployments**: Running some nodes as `--controller --worker` and others
  as `--worker` only is valid. All task-execution nodes compete equally through the
  DB lease.
- **Activity capability filtering**: The `capabilities` column in `workers` allows the
  future option of routing tasks to workers that support a specific activity type.
  `claimNextTask` would gain a `WHERE capabilities LIKE '%activity-name%'` clause.
  Not required for Phase 1.
- **Worker drain before scale-down**: A worker could stop claiming new tasks
  (`worker.draining = true`, set via an API call or signal) while finishing in-flight
  tasks before the process exits — clean pod termination for Kubernetes rolling updates.
