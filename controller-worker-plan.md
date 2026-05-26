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
│  │  WebSocket   │   │  Heartbeat                       │ │
│  └──────────────┘   └──────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

### `role = "controller"`
- Serves the HTTP API and embedded React UI
- Accepts workflow triggers, signals, CRUD operations
- Does **not** execute tasks (no task poller goroutine)
- Listens on PostgreSQL `NOTIFY orchestra_events` to relay live updates to WebSocket clients
- Can run as multiple instances behind a load balancer; all share the same PostgreSQL DB

### `role = "worker"`
- **No** HTTP server (optional lightweight health port, e.g. `:8081/livez`)
- Polls PostgreSQL for pending tasks using the existing row-level lease mechanism
- Executes activities (HTTP, script, agent, webhook, etc.)
- Sends PostgreSQL `NOTIFY orchestra_events` after each state change so controllers can relay live events
- Registers itself in the `workers` table and writes heartbeats on schedule
- Deregisters gracefully on shutdown; tasks with expired leases are requeued automatically by any other worker

### `role = "all"` (default)
- Both controller and worker in one process
- Uses the existing in-process `livebus.Bus` — no `LISTEN/NOTIFY` overhead needed
- Unchanged from the current single-binary behavior

---

## 3. Task Distribution

The current row-level leasing mechanism already supports multiple concurrent workers with no changes to the core algorithm:

```sql
-- claimNextTask: atomic UPDATE ... RETURNING (or SELECT + UPDATE in a tx)
UPDATE workflow_tasks
SET    status = 'running',
       lease_owner     = '<worker-id>',
       lease_expires_at = now() + interval '30 seconds'
WHERE  id = (
    SELECT id FROM workflow_tasks
    WHERE  status = 'pending' AND run_at <= now()
    ORDER BY run_at ASC
    LIMIT  1
    FOR UPDATE SKIP LOCKED          -- <-- this line matters for Postgres
)
RETURNING *;
```

`FOR UPDATE SKIP LOCKED` (PostgreSQL) / busy-timeout + retry (SQLite) ensures that each task is claimed by exactly one worker even under high concurrency.

Workers compete purely through the database — no coordination protocol, no leader election, no message broker.

**What changes:**
- Add `FOR UPDATE SKIP LOCKED` to `claimNextTask` for PostgreSQL (SQLite already uses a single connection + busy_timeout)
- `lease_owner` becomes a meaningful identifier (`worker-<uuid>`) instead of a process-scoped string
- `leaseDuration` config should be generous enough for slow activities (currently 30 s; configurable per deployment)

---

## 4. Worker Registration and Health

A new `workers` table records every node that has ever connected, with a rolling heartbeat:

```sql
CREATE TABLE workers (
    id                 VARCHAR(64)  PRIMARY KEY,
    role               VARCHAR(16)  NOT NULL DEFAULT 'worker',   -- 'worker' | 'all'
    address            VARCHAR(255) NOT NULL DEFAULT '',         -- optional HTTP health endpoint
    capabilities       TEXT         NOT NULL DEFAULT '[]',       -- JSON: ["http-request","script",...]
    max_concurrent     INT          NOT NULL DEFAULT 4,
    status             VARCHAR(16)  NOT NULL DEFAULT 'active',   -- active | draining | offline
    last_heartbeat_at  TIMESTAMPTZ  NOT NULL,
    registered_at      TIMESTAMPTZ  NOT NULL
);
```

**Lifecycle:**

1. On startup, a worker `INSERT OR REPLACE INTO workers (...)` with status `active`
2. Every `heartbeatInterval` (default 10 s), it updates `last_heartbeat_at = now()`
3. On graceful shutdown, it sets `status = 'offline'`
4. Any worker whose `last_heartbeat_at < now() - 3 * heartbeatInterval` is considered `offline`; its leased tasks are already handled by `requeueExpiredTasks()` which runs on every worker pass

Controllers expose `GET /api/workers` so the UI can show a live worker roster.

---

## 5. Cross-Node Live Events (PostgreSQL LISTEN / NOTIFY)

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

**Implementation sketch:**

```go
// internal/pgnotify/listener.go
type Listener struct {
    conn   *pgxpool.Pool
    local  *livebus.Bus
}

func (l *Listener) Start(ctx context.Context) {
    // Use pgx low-level conn with LISTEN; reconnect on error
    conn, _ := l.pool.Acquire(ctx)
    conn.Exec(ctx, "LISTEN orchestra_events")
    for {
        n, err := conn.Conn().WaitForNotification(ctx)
        if err != nil { /* reconnect */ }
        var evt livebus.Event
        json.Unmarshal([]byte(n.Payload), &evt)
        l.local.Publish(evt)
    }
}
```

Workers call `pg_notify('orchestra_events', payload)` inside the same transaction that writes the state change, so notifications and state are always consistent.

**In `role = "all"` mode**: `livebus.Bus` is used directly; LISTEN/NOTIFY is not set up (avoids the extra connection and latency).

---

## 6. Configuration Changes

New `[node]` section in `config.toml`:

```toml
[node]
# Role this process plays. Choices:
#   all         - controller + worker in one process (default, current behaviour)
#   controller  - API server + UI; no task execution
#   worker      - task executor only; no HTTP server
role = "all"

# Stable identity for this node. Auto-generated (and not persisted) if empty.
# Set this explicitly in production so worker entries in the DB are stable.
id = ""

[worker]
# Maximum number of tasks this worker will execute concurrently.
# Each task runs in its own goroutine; set to match available CPU/memory.
maxConcurrentTasks = 4

# How often the worker writes a heartbeat row to the `workers` table.
heartbeatInterval = "10s"

# Optional HTTP address for a minimal health endpoint (empty = disabled).
# Useful for Kubernetes liveness probes on worker pods.
healthAddr = ""  # e.g. "0.0.0.0:8081"
```

The `[workflow]` section is unchanged; both controllers and workers read `databaseDriver` / `databaseURL`.

---

## 7. Cobra Command Changes

The `serve` command grows a `--role` flag that overrides `node.role` in config:

```
orchestra serve                    # role = "all" (default, backward-compatible)
orchestra serve --role controller  # controller only
orchestra serve --role worker      # worker only
```

Internal wiring in `cmd/app/serve.go`:

```go
switch cfg.Node.Role {
case "controller":
    // start HTTP server; start pgnotify.Listener; do NOT call svc.Start()
case "worker":
    // do NOT start HTTP server; call svc.Start(); start heartbeat loop
    // optionally start a tiny health server on cfg.Worker.HealthAddr
default: // "all"
    // existing behaviour: HTTP server + svc.Start() + in-process livebus
}
```

---

## 8. Concurrency Model for Workers

Currently `runWorkerPass` runs up to 16 tasks sequentially in one goroutine. For a dedicated worker process with `maxConcurrentTasks = N` the model changes:

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

A `semaphore chan struct{}` of size `maxConcurrentTasks` gates how many tasks run in parallel. The worker loop keeps claiming and launching until the semaphore is full or no tasks are available.

This replaces the current sequential-up-to-16 loop with true concurrency, which is safe because each task touches only its own rows (the lease is per-task-ID).

---

## 9. Database Schema Additions

```sql
-- Workers table (new)
CREATE TABLE workers (
    id                 VARCHAR(64)  PRIMARY KEY,
    role               VARCHAR(16)  NOT NULL DEFAULT 'worker',
    address            VARCHAR(255) NOT NULL DEFAULT '',
    capabilities       TEXT         NOT NULL DEFAULT '[]',
    max_concurrent     INT          NOT NULL DEFAULT 4,
    status             VARCHAR(16)  NOT NULL DEFAULT 'active',
    last_heartbeat_at  TIMESTAMPTZ  NOT NULL,
    registered_at      TIMESTAMPTZ  NOT NULL
);

-- workflow_tasks: already has lease_owner; no structural changes needed.
-- For PostgreSQL, claimNextTask gains FOR UPDATE SKIP LOCKED.
```

The `orchestra schema` command will include this table in its DDL output.

---

## 10. Kubernetes Deployment Example

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
# workers — scale out on load; no inbound traffic needed
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
        - name: APP_WORKFLOW_DATABASEURL
          valueFrom:
            secretKeyRef:
              name: orchestra-db
              key: url
        # Optional liveness probe via health port
        livenessProbe:
          httpGet:
            path: /livez
            port: 8081
```

---

## 11. Implementation Phases

### Phase 1 — Worker registration and heartbeat (low risk)
1. Add `workers` table to DDL (both SQLite and PostgreSQL dialects)
2. Add `workers.go` to the `workflow` package: `RegisterWorker`, `HeartbeatWorker`, `DeregisterWorker`, `ListWorkers`
3. Call `RegisterWorker` in `NewService`; start a heartbeat goroutine in `Start`
4. Add `GET /api/workers` endpoint and a Workers panel in the UI dashboard
5. No behaviour change for existing deployments

### Phase 2 — Node role: suppress task polling on controller
1. Add `[node]` / `[worker]` config sections and `--role` flag
2. In `serve.go`: when `role = "controller"`, skip `svc.Start()` (no task poller)
3. Controllers still write tasks (via `StartWorkflow`, `completeTask` etc.) — they just don't execute them
4. Workers continue as today (`role = "all"` or `role = "worker"` both call `svc.Start()`)

### Phase 3 — Worker-only mode (no HTTP server)
1. When `role = "worker"`, skip starting the HTTP server entirely
2. Optionally start a minimal health server on `cfg.Worker.HealthAddr`
3. Worker process exits cleanly on SIGTERM after draining in-flight tasks

### Phase 4 — Concurrent task execution on workers
1. Replace sequential `runWorkerPass` with a semaphore-gated goroutine pool
2. Configurable via `worker.maxConcurrentTasks`
3. Each task goroutine writes its own result; no shared state beyond the DB

### Phase 5 — PostgreSQL LISTEN / NOTIFY livebus
1. Extract `livebus.Bus` into an interface (`Publisher`, `Subscriber`)
2. Implement `pgnotify.Bus` backed by `pg_notify` / `LISTEN`
3. In `serve.go`: when `role = "controller"` and driver is postgres, wire `pgnotify.Bus`; otherwise keep in-process bus
4. Workers call `pg_notify` inside each state-change transaction

### Phase 6 — `FOR UPDATE SKIP LOCKED` in PostgreSQL
1. Update `claimNextTask` to append `FOR UPDATE SKIP LOCKED` when `dialect == postgres`
2. Remove the busy-wait retry at application level for Postgres; rely on DB-level skipping

### Phase 7 — UI additions
1. Worker roster on Dashboard: ID, role, status, heartbeat age, capabilities, concurrent tasks
2. Controller list (from workers table where role IN ('controller','all'))
3. Task detail: show which worker ID holds the current lease

---

## 12. What Does NOT Change

| Area | Status |
|---|---|
| `DefinitionDocument` JSON format | Unchanged |
| Activity interface (`Execute`) | Unchanged |
| External webhook API (`/ext/*`) | Served by controller only; unchanged |
| SQLite single-process deployments | `role = "all"` + SQLite = exactly today's behaviour |
| Worker spin-up / spin-down | Out of scope; handled by Kubernetes or operator |
| Authentication / authorisation | Deferred to a separate session |
| Migration tooling | Covered by existing `orchestra schema` command |

---

## 13. Open Questions / Future Considerations

- **Activity routing**: Should certain activities only run on workers with specific capabilities (e.g., GPU for ML, privileged for Docker)? Phase 1 records `capabilities` but doesn't filter on them yet. A `task.requiredCapabilities` column and matching logic in `claimNextTask` would enable this.
- **Worker draining**: Before scaling down, a worker could set `status = "draining"` and stop claiming new tasks while finishing in-flight ones. Kubernetes pre-stop hook is a natural trigger.
- **Controller leader election**: For operations that should run on exactly one controller (e.g., scheduled triggers, periodic cleanup), a lightweight leader-election row in the DB (`SELECT ... FOR UPDATE`) is sufficient without adding ZooKeeper/etcd.
- **Observability**: Expose Prometheus metrics (`/metrics`) for task queue depth, worker concurrency, lease age, etc. — a natural follow-on once the multi-node deployment exists.
