# Orchestra — Cluster Mode (Refined Plan C)

> Supersedes `controller-worker-plan.md` (Plan A) and `worker-grpc-plan.md` (Plan B).
> Builds on and replaces `symmetric-nodes-plan.md` (Plan C draft).
>
> This document is the authoritative plan. Do not implement until approved.

---

## 1. Core Idea

Every node runs the **same binary** and connects to the **same PostgreSQL instance**.
Two additive startup flags decide which subsystems are active:

| Flags | HTTP API + UI | Task poller |
|---|---|---|
| *(neither — default)* | ✓ | ✓ |
| `--controller` | ✓ | — |
| `--worker` | — | ✓ |
| `--controller --worker` | ✓ | ✓ |

All nodes — regardless of role — write a **heartbeat row** to the `nodes` table at a
regular configurable interval. A dedicated goroutine handles this for every node type;
it does not piggyback on the task-poll cadence. Health is derived on read from
`last_seen_at`, with no status column stored in the database.

No inter-node protocol. No gRPC. No HTTP pings between nodes.
PostgreSQL is the only shared resource and the only coordination channel.

---

## 2. Node Roles

### `--controller` (controller-only)
- Serves the HTTP API and embedded React UI
- Handles workflow CRUD, triggers, signal delivery, task management
- Does **not** run the task poller — does not execute activities
- Multiple controllers can run behind a load balancer; all are stateless at the app layer
- Listens on `orchestra_events` NOTIFY channel (PostgreSQL) to relay live updates to WebSocket clients
- Writes a heartbeat row every `heartbeatInterval`

### `--worker` (worker-only)
- Headless — no main HTTP server, no UI
- Runs the task poller: `requeueExpiredTasks` + `claimNextTask` in a semaphore-gated loop
- Executes activities; writes results directly to PostgreSQL
- Always runs a minimal HTTP health server on `node.healthAddr` (default `0.0.0.0:8081`) exposing `/livez`
- Writes a heartbeat row every `heartbeatInterval`

### *(default — both)*
- Full single-process mode: HTTP API + UI + task poller in one process
- Identical to current behaviour; zero migration friction
- Writes a heartbeat row every `heartbeatInterval`

---

## 3. CLI Interface

```
orchestra serve                         # both (default, backward-compatible)
orchestra serve --controller            # API + UI only
orchestra serve --worker                # headless task executor only
orchestra serve --controller --worker   # explicit both; same as default
```

Both flags are additive. Specifying neither defaults to both.
Flag precedence: CLI flags → config file → defaults.

---

## 4. Configuration

```toml
[node]
# Stable identity for this node. Auto-generated (UUIDv4) on each start if empty.
# Set explicitly in production for stable DB entries.
id = ""

# Role switches. Both default to true (single-process all-in-one mode).
controller = true
worker = true

# Maximum number of tasks this node executes concurrently (worker role only).
# Ignored when worker = false.
maxConcurrentTasks = 4

# HTTP address for the health endpoint that exposes GET /livez.
#   controller nodes: /livez is served by the main HTTP server (cfg.Server.Host:Port); this field is ignored.
#   worker-only nodes: a dedicated minimal HTTP server is always started on this address.
#   all-in-one nodes:  /livez is served by the main HTTP server; this field is ignored.
# Default for worker-only: "0.0.0.0:8081"
healthAddr = "0.0.0.0:8081"

[node.health]
# How often every node writes its heartbeat to the `nodes` table.
heartbeatInterval = "10s"

# A node is considered offline when last_seen_at is older than this threshold.
# Should be at least 3 × heartbeatInterval to tolerate transient delays.
offlineThreshold = "30s"
```

Equivalent environment variables:

```sh
APP_NODE_ID=controller-us-east-1a
APP_NODE_CONTROLLER=false
APP_NODE_WORKER=true
APP_NODE_MAXCONCURRENTTASKS=8
APP_NODE_HEALTHADDR=0.0.0.0:8081
APP_NODE_HEALTH_HEARTBEATINTERVAL=10s
APP_NODE_HEALTH_OFFLINETHRESHOLD=30s
```

---

## 5. `nodes` Table

All nodes — controllers, workers, and all-in-one — register in this table.
The table name is `nodes` (not `workers`) because it covers every node role.

```sql
CREATE TABLE nodes (
    id              TEXT     PRIMARY KEY,
    role            TEXT     NOT NULL DEFAULT 'all',   -- 'controller' | 'worker' | 'all'
    address         TEXT     NOT NULL DEFAULT '',       -- HTTP URI of this node's reachable endpoint,
                                                        -- e.g. http://10.0.1.5:8080
    capabilities    TEXT     NOT NULL DEFAULT '[]',     -- JSON array of supported activity names
    max_concurrent  INTEGER  NOT NULL DEFAULT 0,        -- max task concurrency; 0 if controller-only
    version         TEXT     NOT NULL DEFAULT '',       -- binary version string (from internal/version)
    hostname        TEXT     NOT NULL DEFAULT '',       -- OS hostname from os.Hostname()
    last_seen_at    DATETIME NOT NULL,
    registered_at   DATETIME NOT NULL
);
```

### Address Resolution

`address` is stored as an HTTP URI (`http://host:port`) and is always non-empty —
every node exposes a `/livez` health endpoint, so every node has a reachable address.

| Node role | `address` value | Endpoint |
|---|---|---|
| `controller` | `http://<outboundIP>:<cfg.Server.Port>` | `/livez` on the main HTTP server |
| `worker` | `http://<outboundIP>:<healthPort>` | `/livez` on the dedicated health server |
| `all` (default) | `http://<outboundIP>:<cfg.Server.Port>` | `/livez` on the main HTTP server |

The outbound IP is resolved once at startup using a UDP dial that does not open an
actual connection but reveals which local interface the OS would route traffic through:

```go
func resolveOutboundIP() string {
    conn, err := net.Dial("udp", "8.8.8.8:80")
    if err != nil {
        return ""
    }
    defer conn.Close()
    return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
```

This works correctly in containers and multi-homed hosts.
`cfg.Node.HealthAddr` defaults to `"0.0.0.0:8081"` for worker-only nodes; the health
server always starts and the port is parsed from that value to build the URI.

### Node Lifecycle

| Event | DB Operation |
|---|---|
| Process starts | `INSERT OR REPLACE INTO nodes (...)` with `last_seen_at = now()` |
| Every `heartbeatInterval` | `UPDATE nodes SET last_seen_at = now() WHERE id = ?` |
| Graceful shutdown | `DELETE FROM nodes WHERE id = ?` |
| Crash (no cleanup) | Row remains; `last_seen_at` stops advancing; treated as offline after `offlineThreshold` |

### Status Derivation (on read, not stored)

```
status = "online"   when last_seen_at >= now() - offlineThreshold
status = "offline"  otherwise
```

The `GET /api/nodes` handler annotates each row with a derived `status` string.
No status column is written to the database — the DB stores facts, not derived state.

---

## 6. Heartbeat Goroutine

Every node (regardless of role) runs a dedicated heartbeat goroutine in `serve.go`:

```go
func runHeartbeat(ctx context.Context, svc *workflow.Service, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            _ = svc.HeartbeatNode(ctx)
        }
    }
}
```

This goroutine is started after `RegisterNode` completes and is always running,
independent of whether the node is a controller, worker, or both.
It is intentionally separate from the task-poll loop so that:

- Controller nodes (no task poller) still produce heartbeats.
- Worker heartbeat cadence is decoupled from task-poll cadence; each is tuned independently.
- Killing or pausing the task poller does not affect health visibility.

---

## 7. Task Distribution

Unchanged from current single-process behaviour. All worker nodes compete for tasks
via PostgreSQL row-level leasing:

```sql
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

`FOR UPDATE SKIP LOCKED` (PostgreSQL) ensures multiple workers claim different tasks
atomically with no contention.
`requeueExpiredTasks` automatically recovers tasks whose lease has expired — this is
the crash-recovery mechanism and requires no inter-node coordination.

---

## 8. Concurrent Task Execution

The current sequential-up-to-16 loop is replaced by a semaphore-gated goroutine pool
on worker nodes:

```
poll tick / wake
    │
    ▼
while semaphore has a free slot AND DB has a pending task:
    slot = acquire semaphore
    task = claimNextTask()
    go func() {
        defer release(slot)
        executeTask(task)
        writeResult(task)
        notifyWorker()   // wake loop immediately for next task
    }()
```

`maxConcurrentTasks` sets the semaphore size (default 4).
The existing `wakeCh` pattern already wakes the loop after each completed task.

---

## 9. Live Events Across Nodes

### Single controller + N workers

Workers call `pg_notify('orchestra_events', payload)` inside each state-change
transaction. Controllers listen with `LISTEN orchestra_events` and relay notifications
to their in-process `livebus.Bus`, which fans events to connected WebSocket clients.

```
Worker                   PostgreSQL              Controller
──────────────────────────────────────────────────────────
completeTask()
  → NOTIFY orchestra_events ──────────────────►
                                               LISTEN goroutine
                                                 → livebus.Publish()
                                                 → WebSocket fan-out
```

### Multiple controllers

Each controller has its own `LISTEN` goroutine. All controllers receive the same
notifications independently and relay to their own WebSocket subscribers. No
cross-controller coordination is needed.

### All-in-one (default)

The in-process `livebus.Bus` is used directly. `pg_notify` / `LISTEN` is not set up
(avoids an extra DB connection in the common single-process case).

---

## 10. New UI Page — Cluster

### Route and Navigation

- Route: `/cluster`
- Nav label: **Cluster**
- Nav icon: `Network` (lucide-react)
- Position: after Operations, before Settings

### Page Layout

```
┌──────────────────────────────────────────────────────────────────────────┐
│  Cluster                                                  [Refresh] [↺]  │
├────────────┬─────────────┬──────────────┬───────────────────────────────┤
│ Total nodes│  Online     │  Offline     │                               │
│    5       │    4        │    1         │                               │
├────────────┴─────────────┴──────────────┴───────────────────────────────┤
│  NODE ID          ROLE         STATUS    VERSION   HOST    LAST SEEN     │
├──────────────────────────────────────────────────────────────────────────┤
│  ctrl-abc…  [controller]  ● Online   v1.2.0  host-1  12s ago            │
│  ctrl-def…  [controller]  ● Online   v1.2.0  host-2  8s ago             │
│  wrkr-ghi…  [worker]      ● Online   v1.2.0  host-3  5s ago             │
│  wrkr-jkl…  [worker]      ● Online   v1.2.0  host-4  3s ago             │
│  wrkr-mno…  [worker]      ○ Offline  v1.2.0  host-5  4m ago             │
└──────────────────────────────────────────────────────────────────────────┘
```

Columns:
- Node ID (truncated, with copy-to-clipboard icon)
- Role badge: `controller` / `worker` / `all` (colour-coded)
- Status badge: green ● Online / red ○ Offline
- Address (`http://host:port` URI — always populated for all node types)
- Version (from `internal/version`)
- Hostname
- Max Concurrent Tasks (`—` for controller-only nodes)
- Capabilities (chip list, collapsible if long)
- Last Seen (relative: "3s ago", "4m ago")
- Registered (absolute date/time, formatted)

### Auto-Refresh Strategy

1. Poll `GET /api/nodes` every 10 seconds via TanStack Query `refetchInterval`
2. Also re-fetch on WebSocket `nodes.updated` events (published after each heartbeat write by any node)

### Warning Banner

When the page loads and all nodes in the `nodes` table have `status = "offline"` or
the table is empty, show an information banner:

> **No active nodes found.** Workflow tasks will not execute until a worker node comes online.

This is shown only on the Cluster page, not on the Dashboard (to avoid alarm for
single-process deployments that haven't opted into cluster mode).

---

## 11. API

### `GET /api/nodes`

Returns all rows from the `nodes` table with a derived `status` field.
Only available on controller nodes (requires HTTP server to be running).

**Response:**
```json
[
  {
    "id": "ctrl-abc123",
    "role": "controller",
    "address": "http://10.0.1.5:8080",
    "capabilities": [],
    "maxConcurrent": 0,
    "version": "v1.2.0",
    "hostname": "host-1",
    "lastSeenAt": "2026-05-26T10:00:12Z",
    "registeredAt": "2026-05-26T09:00:00Z",
    "status": "online"
  },
  {
    "id": "wrkr-xyz789",
    "role": "worker",
    "address": "http://10.0.1.8:8081",
    "capabilities": ["http-request", "script", "agent", "delay"],
    "maxConcurrent": 8,
    "version": "v1.2.0",
    "hostname": "host-3",
    "lastSeenAt": "2026-05-26T10:00:09Z",
    "registeredAt": "2026-05-26T09:01:00Z",
    "status": "online"
  }
]
```

`address` is always a fully-formed HTTP URI. The UI can render it as a clickable link.

`status` is computed server-side as `"online"` or `"offline"` using the configured
`offlineThreshold`. It is never stored in the database.

---

## 12. serve.go Wiring

```go
func runServe(cfg config.Config, ...) {
    isController := cfg.Node.Controller
    isWorker     := cfg.Node.Worker
    if !isController && !isWorker {
        isController, isWorker = true, true
    }

    role := deriveRole(isController, isWorker)  // "controller" | "worker" | "all"

    svc, _ := workflow.NewService(cfg.Workflow, logger, live)
    svc.RegisterNode(ctx, workflow.NodeInfo{
        ID:            cfg.Node.ID,
        Role:          role,
        Address:       resolveNodeAddress(isController, cfg),  // http://outboundIP:port
        Capabilities:  workflow.AllActivityNames(),
        MaxConcurrent: cfg.Node.MaxConcurrentTasks,
        Version:       version.Version,
        Hostname:      hostname(),
    })
    defer svc.DeregisterNode(context.Background())

    go runHeartbeat(ctx, svc, cfg.Node.Health.HeartbeatInterval)

    if isWorker {
        svc.Start(ctx)     // task poller + requeueExpiredTasks
    }

    if isController {
        if cfg.Workflow.DatabaseDriver == "postgres" {
            go startPGNotifyListener(ctx, cfg, live)
        }
        httpServer := buildHTTPServer(cfg, svc, live, restartCh)
        httpServer.ListenAndServe()
    } else {
        // worker-only: always start the health server (healthAddr defaults to 0.0.0.0:8081)
        go startHealthServer(cfg.Node.HealthAddr)
        <-ctx.Done()
    }
}
```

---

## 13. Kubernetes Deployment Example

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
        - name: APP_NODE_CONTROLLER
          value: "true"
        - name: APP_NODE_WORKER
          value: "false"
        - name: APP_WORKFLOW_DATABASEURL
          valueFrom:
            secretKeyRef: { name: orchestra-db, key: url }
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
  replicas: 5
  template:
    spec:
      containers:
      - name: orchestra
        image: orchestra:latest
        args: ["serve", "--worker"]
        env:
        - name: APP_NODE_CONTROLLER
          value: "false"
        - name: APP_NODE_WORKER
          value: "true"
        - name: APP_NODE_MAXCONCURRENTTASKS
          value: "8"
        - name: APP_NODE_HEALTHADDR
          value: "0.0.0.0:8081"  # always required; worker always starts health server
        - name: APP_WORKFLOW_DATABASEURL
          valueFrom:
            secretKeyRef: { name: orchestra-db, key: url }
        livenessProbe:
          httpGet: { path: /livez, port: 8081 }
```

Both deployments share the same `orchestra-db` secret.

---

## 14. Implementation Phases

### Phase 1 — `--controller` / `--worker` flags (no behaviour change by default)
1. Add `NodeConfig` + `NodeHealthConfig` structs to `internal/config/config.go`
2. Register `--controller` and `--worker` CLI flags on the `serve` command in `cmd/app/serve.go`
3. In `serve.go`: resolve `isController` / `isWorker` from flags; default both true when neither is set
4. No behaviour change yet — simply wire the booleans

### Phase 2 — `nodes` table + registration + heartbeat
1. Add `nodes` DDL to `initSchema` (SQLite and PostgreSQL dialects)
2. Add `NodeInfo` struct and `RegisterNode`, `DeregisterNode`, `HeartbeatNode`, `ListNodes` methods to `internal/workflow/service_nodes.go` (new file)
3. Call `RegisterNode` in `serve.go` after service init; defer `DeregisterNode`
4. Start `runHeartbeat` goroutine in `serve.go`
5. After each `HeartbeatNode` call, publish a `nodes.updated` event on `livebus`

### Phase 3 — `GET /api/nodes` endpoint
1. Add `ListNodes` handler in `internal/api/handler.go`
2. Add route `GET /api/nodes` in `internal/api/router.go`
3. Handler calls `svc.ListNodes(ctx)`, annotates `status`, returns JSON

### Phase 4 — Worker-only mode (suppress HTTP server)
1. When `isController = false`: skip `buildHTTPServer` entirely
2. Always start the minimal `/livez` health server on `cfg.Node.HealthAddr` (default `0.0.0.0:8081`)
3. Block on `<-ctx.Done()`

### Phase 5 — Controller-only mode (suppress task poller)
1. When `isWorker = false`: skip `svc.Start()`
2. Test: trigger a workflow → task enters `pending` → no worker claims it → re-queue timer runs → still `pending`

### Phase 6 — Cluster UI page
1. Create `ui/src/pages/ClusterPage.tsx`
2. Add route `/cluster` in `ui/src/App.tsx`
3. Add nav item "Cluster" with `Network` icon in `ui/src/components/Layout.tsx`
4. Add `getNodes()` function to `ui/src/services/api.ts`
5. Add `Node` interface to `ui/src/types/index.ts`
6. Page: summary stats cards + nodes table; refetch every 10s + on `nodes.updated` WebSocket event
7. Warning banner when no online nodes

### Phase 7 — Concurrent task execution
1. Replace sequential `runWorkerPass` loop with semaphore-gated goroutine pool
2. Semaphore size = `cfg.Node.MaxConcurrentTasks` (default 4)
3. Each goroutine: claim → execute → write result → `notifyWorker()`

### Phase 8 — PostgreSQL LISTEN / NOTIFY
1. Extract `livebus.Bus` to an interface (`Bus`) with two implementations:
   - `inprocess.Bus` — current implementation (used in all-in-one mode)
   - `pgnotify.Bus` — wraps `pg_notify` publish + `LISTEN` subscription
2. In `serve.go`: when `isController = true` and driver is postgres, start `LISTEN` goroutine
3. Worker `completeTask`, `handleTaskFailure`, `waitTaskForSignal`: call `pg_notify` inside transaction

### Phase 9 — `FOR UPDATE SKIP LOCKED`
1. In `claimNextTask`: append `FOR UPDATE SKIP LOCKED` to the inner SELECT when dialect is postgres
2. No change for SQLite (uses existing busy-timeout retry)

---

## 15. What Does NOT Change

| Area | Status |
|---|---|
| `DefinitionDocument` JSON format | Unchanged |
| Activity interface (`Execute`) | Unchanged |
| External webhook API (`/ext/*`) | Served by controller nodes only |
| SQLite single-process deployments | Default `--controller --worker` = exactly today |
| Task scheduling correctness | Completely independent of node health state |
| Authentication / authorisation | Deferred |
| `orchestra schema` command | Will include `nodes` table in DDL output |

---

## 16. Open Questions / Future Considerations

- **Capability-based routing**: `capabilities` is recorded per node. A future
  `claimNextTask` variant could filter `WHERE capabilities LIKE '%activity-name%'` to
  route GPU or privileged activities to specific workers.
- **Worker draining**: A worker could stop claiming new tasks (via API call or OS
  signal) while finishing in-flight tasks before shutdown — clean Kubernetes rolling
  updates.
- **Controller leader election**: For singleton operations (scheduled triggers, periodic
  cleanup) a DB-level `SELECT ... FOR UPDATE` lease is sufficient.
- **Observability**: Prometheus `/metrics` for task queue depth, active goroutines,
  heartbeat lag — natural follow-on phase.
- **UI warning on Dashboard**: When a single-process user deploys in split-role mode
  and forgets to start workers, the Dashboard could warn "No online worker nodes" by
  checking `GET /api/nodes` for online workers.
