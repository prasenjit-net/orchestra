# Orchestra — Controller / Worker Deployment Model

## 1. Goals

| Goal | Notes |
|---|---|
| Scale task execution independently from API traffic | Add workers without touching controllers |
| Multiple controllers for HA / failover | All controllers are stateless at the application layer |
| Workers can be added or removed at any time | Kubernetes / any scheduler; Orchestra has no opinion on this |
| No new infrastructure required | PostgreSQL is the only shared resource |
| Single-process "all-in-one" mode remains the default | Zero friction for development and small deployments |

---

## 2. Node Roles

```
┌──────────────────────────────────────────────────────────┐
│  role = "all"  (default today, unchanged for dev/POC)    │
│  ┌──────────────┐   ┌──────────────────────────────────┐ │
│  │ Controller   │   │ Worker                           │ │
│  │  HTTP API    │   │  Task poller                     │ │
│  │  UI embed    │   │  Activity executor               │ │
│  │  WebSocket   │   │  Ping sender                     │ │
│  │  Ping recv.  │   │                                  │ │
│  └──────────────┘   └──────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

### `role = "controller"`
- Serves the HTTP API and embedded React UI
- Accepts workflow triggers, signals, CRUD operations
- Does **not** execute tasks (no task poller goroutine)
- Receives periodic health pings from workers and tracks liveness in memory
- Listens on PostgreSQL `NOTIFY orchestra_events` to relay live updates to WebSocket clients
- Can run as multiple instances behind a load balancer; all share the same PostgreSQL DB

### `role = "worker"`
- **No** HTTP server (optional lightweight health port for Kubernetes probes)
- Polls PostgreSQL for pending tasks using the existing row-level lease mechanism
- Executes activities (HTTP, script, agent, webhook, etc.)
- Sends PostgreSQL `NOTIFY orchestra_events` after each state change so controllers can relay live events
- Registers itself in the `workers` table on startup; deregisters on graceful shutdown
- Sends a periodic HTTP ping to the configured controller URL so the controller can track liveness
- Tasks with expired leases are requeued automatically by `requeueExpiredTasks()` — worker health has no effect on task scheduling

### `role = "all"` (default)
- Both controller and worker in one process
- Uses the existing in-process `livebus.Bus` — no `LISTEN/NOTIFY` overhead needed
- No ping loop needed — the worker component is in the same process as the controller
- Unchanged from the current single-binary behavior

---

## 3. Task Distribution

The current row-level leasing mechanism already supports multiple concurrent workers with no changes to the core algorithm:

```sql
-- claimNextTask: atomic UPDATE ... RETURNING (or SELECT + UPDATE in a tx)
UPDATE workflow_tasks
SET    status = 'running',
       lease_owner      = '<worker-id>',
       lease_expires_at = now() + interval '30 seconds'
WHERE  id = (
    SELECT id FROM workflow_tasks
    WHERE  status = 'pending' AND run_at <= now()
    ORDER BY run_at ASC
    LIMIT  1
    FOR UPDATE SKIP LOCKED          -- Postgres: skip rows locked by other workers
)
RETURNING *;
```

`FOR UPDATE SKIP LOCKED` (PostgreSQL) / busy-timeout + retry (SQLite) ensures each task is claimed by exactly one worker even under high concurrency.

Workers compete purely through the database — no coordination protocol, no leader election, no message broker.

**What changes:**
- Add `FOR UPDATE SKIP LOCKED` to `claimNextTask` for PostgreSQL
- `lease_owner` becomes a meaningful stable identifier (`worker-<uuid>`)
- `leaseDuration` should be generous enough for slow activities (currently 30 s; configurable)

---

## 4. Worker Registration

The `workers` table is a **registration record only** — it captures what workers exist and their static attributes. It is not used for health tracking.

```sql
CREATE TABLE workers (
    id             VARCHAR(64)  PRIMARY KEY,
    role           VARCHAR(16)  NOT NULL DEFAULT 'worker',   -- 'worker' | 'all'
    address        VARCHAR(255) NOT NULL DEFAULT '',         -- optional HTTP health endpoint
    capabilities   TEXT         NOT NULL DEFAULT '[]',       -- JSON: ["http-request","script",...]
    max_concurrent INT          NOT NULL DEFAULT 4,
    registered_at  TIMESTAMPTZ  NOT NULL
    -- No status column. No last_heartbeat_at column.
    -- Liveness is tracked in-memory by the controller via HTTP pings (see §5).
);
```

**Lifecycle:**
1. On startup: worker `INSERT OR REPLACE INTO workers (...)` — records its identity and capabilities
2. On graceful shutdown: worker `DELETE FROM workers WHERE id = ?`
3. If a worker crashes without cleanup its row remains; the controller's ping tracker will mark it offline independently (see §5)

Controllers expose `GET /api/workers` which merges the registration rows from the DB with the live ping state held in memory.

---

## 5. Worker Health — HTTP Ping Model

Worker liveness is tracked by the **controller in memory** via periodic HTTP pings from workers. This keeps the database out of the hot-path health loop entirely.

### Flow

```
Worker process                          Controller process
──────────────────────────────────────────────────────────
every pingInterval:
  POST /api/workers/{id}/ping ─────────────────────────►
                                          pingTracker.Record(id, now)
                                          return 200 OK
  ◄─────────────────────────────────────────────────────
```

### Controller-side ping tracker

```go
// internal/pingtracker/tracker.go
type Tracker struct {
    mu        sync.RWMutex
    lastSeen  map[string]time.Time   // workerID → time of last ping
}

func (t *Tracker) Record(workerID string)             { ... }
func (t *Tracker) IsOnline(workerID string) bool      { ... }  // last ping within threshold
func (t *Tracker) AllStatuses() map[string]string     { ... }  // "online" | "offline" | "unknown"
```

The tracker is a lightweight in-memory map — no DB writes on every ping. The controller combines it with the `workers` registration table when serving `GET /api/workers`.

### Threshold configuration

A worker is considered **offline** when the controller has not received a ping for `pingInterval × missedPingThreshold` (e.g., 10 s × 3 = 30 s without a ping → offline).

```toml
[worker]
# How often this worker sends a ping to the controller.
pingInterval = "10s"

# Controller URL this worker pings. Required when role = "worker".
# For HA setups, use the load-balancer address in front of controllers.
controllerURL = "http://orchestra-controller:8080"

[controller]
# Mark a worker offline after this many consecutive missed pings.
missedPingThreshold = 3
```

### Multi-controller note

When multiple controllers run behind a load balancer, pings may land on different controller instances. Each controller maintains its own independent in-memory view of worker health. This means the worker roster shown in the UI may differ slightly between controllers — acceptable for a health display. If strict consistency is required, the ping endpoint can write a `last_seen_at` timestamp to the DB, but that is an optional upgrade, not a requirement.

### No effect on task scheduling

Worker health state (online / offline) is **purely informational** — it is never consulted by the task scheduler. Tasks with expired leases are requeued by `requeueExpiredTasks()` which runs on every worker pass, regardless of what the ping tracker says. A crashed worker's tasks are recovered automatically within one `leaseDuration`, with zero dependency on the ping mechanism.

---

## 6. Cross-Node Live Events (PostgreSQL LISTEN / NOTIFY)

In single-process mode the in-process `livebus.Bus` works perfectly. In distributed mode, workers completing tasks on remote processes need to push events to controllers so those controllers can relay them to browser WebSocket connections.

### Mechanism: PostgreSQL LISTEN / NOTIFY

```
Worker process               PostgreSQL              Controller process
──────────────────────────────────────────────────────────────────────
completeTask()
  → tx.Commit()
  → NOTIFY orchestra_events, '{"type":"task.completed",...}'
                            ──────────────────────────────►
                                                         pgListener goroutine
                                                           receives notification
                                                           → local livebus.Publish()
                                                           → WebSocket fan-out
```

Workers call `pg_notify('orchestra_events', payload)` inside the same transaction that writes the state change, so notifications and state are always consistent.

**In `role = "all"` mode**: `livebus.Bus` is used directly; LISTEN/NOTIFY is not set up.

---

## 7. Configuration Changes

```toml
[node]
# Role this process plays.
#   all         - controller + worker in one process (default, current behaviour)
#   controller  - API server + UI; no task execution
#   worker      - task executor only; no HTTP server
role = "all"

# Stable identity for this node. Auto-generated if empty.
# Set explicitly in production for stable worker DB entries.
id = ""

[worker]
# Maximum tasks this worker executes concurrently.
maxConcurrentTasks = 4

# How often this worker sends an HTTP ping to the controller.
pingInterval = "10s"

# Controller URL to ping. Required when role = "worker".
controllerURL = ""  # e.g. "http://orchestra-controller:8080"

# Optional HTTP address for a minimal liveness endpoint (Kubernetes probes).
healthAddr = ""  # e.g. "0.0.0.0:8081"

[controller]
# Mark a worker offline after this many consecutive missed pings.
missedPingThreshold = 3
```

The `[workflow]` section is unchanged; both controllers and workers read `databaseDriver` / `databaseURL`.

---

## 8. Cobra Command Changes

```
orchestra serve                    # role = "all" (default, backward-compatible)
orchestra serve --role controller
orchestra serve --role worker
```

Internal wiring in `cmd/app/serve.go`:

```go
switch cfg.Node.Role {
case "controller":
    // start HTTP server (with /api/workers/{id}/ping endpoint)
    // start pgnotify.Listener
    // start pingTracker
    // do NOT call svc.Start()
case "worker":
    // do NOT start HTTP server
    // call svc.Start()
    // start ping loop → POST controllerURL/api/workers/{id}/ping
    // optionally start health server on cfg.Worker.HealthAddr
default: // "all"
    // existing behaviour: HTTP server + svc.Start() + in-process livebus
    // no ping loop needed (worker is in-process)
}
```

---

## 9. Concurrency Model for Workers

Currently `runWorkerPass` runs up to 16 tasks sequentially. For a dedicated worker process with `maxConcurrentTasks = N`:

```
                ┌─ goroutine pool (N slots) ─────────────────┐
Worker loop     │                                            │
tick/wake  ──►  │  for each free slot:                       │
                │    task, ok = claimNextTask()              │
                │    if !ok: break                           │
                │    go executeTask(task)  ──► activity      │
                │                                            │
                └────────────────────────────────────────────┘
```

A `semaphore chan struct{}` of size `maxConcurrentTasks` gates concurrency. This replaces the sequential-up-to-16 loop with true parallelism, safe because each task touches only its own rows.

---

## 10. Database Schema Additions

```sql
-- Workers registration table (new)
-- Health / liveness is NOT stored here — tracked in-memory by the controller.
CREATE TABLE workers (
    id             VARCHAR(64)  PRIMARY KEY,
    role           VARCHAR(16)  NOT NULL DEFAULT 'worker',
    address        VARCHAR(255) NOT NULL DEFAULT '',
    capabilities   TEXT         NOT NULL DEFAULT '[]',
    max_concurrent INT          NOT NULL DEFAULT 4,
    registered_at  TIMESTAMPTZ  NOT NULL
);

-- workflow_tasks: already has lease_owner; no structural changes needed.
-- For PostgreSQL, claimNextTask gains FOR UPDATE SKIP LOCKED.
```

The `orchestra schema` command will include this table in its DDL output.

---

## 11. Kubernetes Deployment Example

```yaml
# controllers — stateless, scale horizontally, sit behind a Service/Ingress
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
        args: ["serve", "--role", "controller"]
        env:
        - name: APP_NODE_ROLE
          value: controller
        - name: APP_WORKFLOW_DATABASEURL
          valueFrom:
            secretKeyRef:
              name: orchestra-db
              key: url
        ports:
        - containerPort: 8080
---
# workers — scale out on load; no inbound HTTP traffic
apiVersion: apps/v1
kind: Deployment
metadata:
  name: orchestra-worker
spec:
  replicas: 3   # HPA can scale this
  template:
    spec:
      containers:
      - name: orchestra
        image: orchestra:latest
        args: ["serve", "--role", "worker"]
        env:
        - name: APP_NODE_ROLE
          value: worker
        - name: APP_WORKER_MAXCONCURRENTTASKS
          value: "8"
        - name: APP_WORKER_CONTROLLERURL
          value: "http://orchestra-controller:8080"
        - name: APP_WORKER_HEALTHADDR
          value: "0.0.0.0:8081"
        - name: APP_WORKFLOW_DATABASEURL
          valueFrom:
            secretKeyRef:
              name: orchestra-db
              key: url
        livenessProbe:
          httpGet:
            path: /livez
            port: 8081
```

---

## 12. Implementation Phases

### Phase 1 — Worker registration table (low risk, no behaviour change)
1. Add `workers` table to DDL (SQLite + PostgreSQL dialects)
2. `RegisterWorker` / `DeregisterWorker` / `ListWorkers` in `workflow` package
3. Call `RegisterWorker` in `NewService`; `DeregisterWorker` in `Close`
4. `GET /api/workers` endpoint — returns DB rows (all workers that registered)
5. Workers panel on Dashboard UI (registration info only; status = "unknown" until Phase 3)

### Phase 2 — Node role: suppress task polling on controller
1. Add `[node]` / `[worker]` / `[controller]` config sections and `--role` flag
2. When `role = "controller"`: skip `svc.Start()`
3. When `role = "worker"`: proceed as today; `svc.Start()` runs

### Phase 3 — Worker-to-controller HTTP ping
1. Controller: add `POST /api/workers/{id}/ping` endpoint; wire `pingTracker`
2. Worker: start ping goroutine on `svc.Start()` when `cfg.Node.Role == "worker"`
3. `GET /api/workers` merges DB registration rows with `pingTracker.AllStatuses()`
4. Dashboard shows live online/offline status per worker

### Phase 4 — Worker-only mode (no HTTP server)
1. When `role = "worker"`: skip starting the HTTP server entirely
2. Optionally start minimal health server on `cfg.Worker.HealthAddr`
3. Graceful shutdown: stop claiming new tasks, wait for in-flight tasks to finish

### Phase 5 — Concurrent task execution on workers
1. Replace sequential `runWorkerPass` with semaphore-gated goroutine pool
2. Configurable via `worker.maxConcurrentTasks`

### Phase 6 — PostgreSQL LISTEN / NOTIFY livebus
1. Extract `livebus.Bus` into an interface
2. Implement `pgnotify.Bus` backed by `pg_notify` / `LISTEN`
3. Wire in `serve.go` when `role = "controller"` and driver is postgres

### Phase 7 — `FOR UPDATE SKIP LOCKED` in PostgreSQL
1. Update `claimNextTask` to append `FOR UPDATE SKIP LOCKED` for Postgres dialect

### Phase 8 — UI additions
1. Worker roster: ID, role, status (online/offline/unknown), capabilities, concurrency
2. Task detail: show `lease_owner` (worker ID holding the current lease)

---

## 13. What Does NOT Change

| Area | Status |
|---|---|
| `DefinitionDocument` JSON format | Unchanged |
| Activity interface (`Execute`) | Unchanged |
| External webhook API (`/ext/*`) | Served by controller only; unchanged |
| SQLite single-process deployments | `role = "all"` + SQLite = exactly today's behaviour |
| Task scheduling correctness | Completely independent of worker health / ping state |
| Worker spin-up / spin-down | Out of scope; handled by Kubernetes or operator |
| Authentication / authorisation | Deferred to a separate session |
| Migration tooling | Covered by existing `orchestra schema` command |

---

## 14. Open Questions / Future Considerations

- **Activity routing**: `capabilities` is recorded at registration but not yet filtered in `claimNextTask`. A `task.requiredCapabilities` column + matching logic would enable GPU/privileged routing.
- **Worker draining**: Worker sets itself to "draining" state (stops claiming), finishes in-flight tasks, then shuts down. Kubernetes `preStop` hook is the natural trigger.
- **Controller leader election**: For singleton operations (scheduled triggers, periodic cleanup), a lightweight DB-level lease (`SELECT ... FOR UPDATE`) is sufficient.
- **Ping fan-out in multi-controller**: If strict consistency of worker status across controllers is needed, the ping endpoint can optionally write `last_seen_at` to the DB. Not required for the initial implementation.
- **Observability**: Prometheus `/metrics` for task queue depth, worker concurrency, ping latency — natural follow-on.
