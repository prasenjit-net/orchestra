# Orchestra — Worker via gRPC (No-DB Architecture)

> **Alternative to** `controller-worker-plan.md` (Plan A — workers with direct DB access).
> This document describes Plan B: workers have no database connection and no configuration
> file beyond the controller URL.

---

## 1. Core Idea

```
Worker process                    Controller process
──────────────────────────────────────────────────────────────────
  orchestra serve \
    --role worker \
    --controller grpc://controller:9090

      │  gRPC bidirectional stream (persistent)
      ├──────────────────────────────────────────►
      │   WorkerHello{id, capabilities, maxConcurrent}
      │◄──────────────────────────────────────────
      │   ConfigSync{openaiAPIKey, scriptLimits, ...}
      │◄──────────────────────────────────────────
      │   TaskAssignment{taskId, activity, resolvedInput, activityPayload}
      │──────────────────────────────────────────►
      │   TaskResult{taskId, status, output, error}
      │◄──────────────────────────────────────────
      │   TaskAssignment{...}
      │   ...
```

**What the worker needs to start:**
- `--controller` (or `APP_CONTROLLER_URL`) — gRPC address of the controller
- Nothing else. No database credentials, no config file, no API keys.

**What the controller does on behalf of the worker:**
- Claims tasks from the database
- Resolves template inputs against workflow context
- Looks up script source, agent definitions, MCP servers
- Syncs runtime config (OpenAI key, script limits) to the worker over gRPC
- Receives results from the worker and writes them to the database

---

## 2. gRPC Service Definition

```protobuf
syntax = "proto3";
package orchestra.worker.v1;

import "google/protobuf/timestamp.proto";

// WorkerGateway is the single gRPC service exposed by the controller.
// Workers are clients; controllers are servers.
service WorkerGateway {
    // Session is a persistent bidirectional stream for the lifetime of the
    // worker process. The worker opens it on startup and keeps it alive.
    // Controller sends TaskAssignments and config updates;
    // Worker sends TaskResults and the initial WorkerHello.
    rpc Session(stream WorkerEnvelope) returns (stream ControllerEnvelope);
}

// ── Worker → Controller ──────────────────────────────────────────────────────

message WorkerEnvelope {
    oneof body {
        WorkerHello hello  = 1;   // sent once, immediately after stream open
        TaskResult  result = 2;   // sent when a task finishes (success or failure)
    }
}

message WorkerHello {
    string          worker_id       = 1;   // stable ID (auto-generated or configured)
    repeated string capabilities    = 2;   // activity names this worker supports
    int32           max_concurrent  = 3;
    string          version         = 4;   // binary version string
}

message TaskResult {
    int64  task_id         = 1;
    string outcome         = 2;   // "completed" | "failed" | "delay" | "wait_signal"
    bytes  output_json     = 3;   // JSON payload for "completed"
    string error_message   = 4;   // for "failed"
    google.protobuf.Timestamp delay_until = 5;  // for "delay"
    WaitSignalSpec wait_signal = 6;             // for "wait_signal"
    bytes  state_json      = 7;   // activity-specific state carried across retries
    map<string, bytes> context_updates = 8;    // partial context patches
}

message WaitSignalSpec {
    string signal_name = 1;
    google.protobuf.Timestamp timeout_at = 2;
}

// ── Controller → Worker ──────────────────────────────────────────────────────

message ControllerEnvelope {
    oneof body {
        ConfigSync      config      = 1;   // sent once on connect; re-sent on change
        TaskAssignment  task        = 2;
        CancelTask      cancel      = 3;
    }
}

message ConfigSync {
    string openai_api_key           = 1;
    int32  script_timeout_ms        = 2;
    int32  script_max_source_bytes  = 3;
    int32  script_max_output_bytes  = 4;
    uint64 script_max_exec_steps    = 5;
    bool   script_enabled           = 6;
}

message TaskAssignment {
    int64  task_id              = 1;
    string workflow_id          = 2;
    string definition_id        = 3;
    int32  definition_version   = 4;
    string step_name            = 5;
    string activity_name        = 6;
    bytes  resolved_input_json  = 7;   // template variables already substituted by controller
    bytes  workflow_context_json = 8;  // full context snapshot (for activities that need it)
    ActivityPayload payload     = 9;   // activity-specific pre-fetched data
    int32  attempt_number       = 10;
    int32  max_attempts         = 11;
    bytes  state_json           = 12;  // state carried from previous attempt (retry/resume)
}

// ActivityPayload carries data the controller pre-fetched from the DB so the
// worker never needs a database connection during execution.
message ActivityPayload {
    oneof data {
        ScriptPayload  script = 1;
        AgentPayload   agent  = 2;
    }
}

message ScriptPayload {
    string source = 1;   // full Starlark script source, looked up by controller
}

message AgentPayload {
    string   system_prompt = 1;
    string   model         = 2;
    float    temperature   = 3;
    repeated MCPServerSpec mcp_servers = 4;
}

message MCPServerSpec {
    string id      = 1;
    string name    = 2;
    string url     = 3;
    string api_key = 4;
}

message CancelTask {
    int64 task_id = 1;   // controller asks worker to abort an in-flight task
}
```

---

## 3. Connection Lifecycle

```
Worker                         Controller
──────                         ──────────
Start
  gRPC Dial(controllerURL)
  stream = Session()
  Send WorkerHello{...}  ──────────────────►  Register worker in memory registry
                         ◄──────────────────  Send ConfigSync{openaiAPIKey, ...}

                    ── steady state ──

                         ◄──────────────────  Claim task from DB
                                              Resolve inputs
                                              Fetch script/agent data
                         ◄──────────────────  Send TaskAssignment{taskId, ...}
  Execute activity
  (fully local, no DB)
  Send TaskResult{...}   ──────────────────►  Write result to DB
                                              Schedule next step
                         ◄──────────────────  Send next TaskAssignment{...}

                    ── shutdown ──

SIGTERM received
  Finish in-flight tasks
  Close stream gracefully  ────────────────►  Remove from registry
                                              Requeue unacknowledged tasks
```

---

## 4. Task Dispatch on the Controller Side

The controller side gains a **dispatcher** component that sits between the DB and connected workers:

```
DB polling loop                Worker registry             gRPC streams
──────────────────────────────────────────────────────────────────────────
requeueExpiredTasks()
claimTask() → lease = ctrl-id
  task ready
    pick worker (least loaded)  ──────────────────────────► send TaskAssignment
                                                            worker executes...
                                ◄──────────────────────────  TaskResult received
                                write result to DB
                                completeTask() / handleTaskFailure()
                                emit live event (pg_notify or in-process bus)
```

**Worker selection**: pick the connected worker with the fewest in-flight tasks that supports the required activity. Falls back to round-robin if all are equally loaded.

**Lease owner**: set to `ctrl-<controllerID>` (not the worker ID). This is intentional — the controller holds the DB lease and is responsible for writing the result. If the controller crashes, the lease expires and another controller reclaims and reassigns.

**In-flight tracking**: the controller keeps a small in-memory map `taskID → workerID` for every sent-but-not-yet-answered task. When a worker disconnects, all its in-flight tasks are immediately re-submitted to another available worker (or requeued in the DB if no worker is available) — no wait for lease expiry.

---

## 5. Failure Scenarios

### Worker crashes mid-execution
1. gRPC stream closes → controller removes worker from registry
2. Controller checks its in-flight map: finds tasks sent to that worker
3. Tasks are immediately requeued to another worker (or back to DB as pending)
4. DB lease is still held by the controller — no lease expiry wait needed

### Controller crashes
1. DB lease expires after `leaseDuration`
2. Another controller (or the same one after restart) reclaims the task
3. Worker may still be executing — when it sends `TaskResult`, the new controller
   receives it on the reconnected stream and writes the result
4. If worker also lost connection during crash: task runs again after lease expiry (idempotent activities handle this naturally; non-idempotent ones should be designed with the `state_json` carry-over mechanism)

### Controller overloaded / can't dispatch
- Tasks pile up as `pending` in the DB — the normal backpressure path
- Workers block on `Recv()` waiting for the next `TaskAssignment`
- No thundering herd: dispatcher only sends a task when a worker has capacity

### No workers connected
- Controller claims tasks but cannot dispatch
- Controller immediately releases the lease (sets status back to `pending`)
- Tasks wait for a worker to connect — no data loss, no timeout cascade

---

## 6. Config Sync

Config that workers need is sent once as `ConfigSync` immediately after the controller receives `WorkerHello`. Workers cache this locally for their session.

If config changes (e.g., a new `openaiAPIKey` is written to the config file via the Settings page and the server restarts), workers receive a new `ConfigSync` on their next reconnect. For live config updates without restart, the controller can push a new `ConfigSync` message at any time on the open stream.

**What is synced:**
- `openaiAPIKey` — used by agent activities
- Script sandbox limits (`timeout`, `maxSourceBytes`, `maxOutputBytes`, `maxExecSteps`)
- `scriptEnabled` flag

**What is NOT synced** (workers don't need it):
- Database credentials
- Server port / host
- UI proxy settings
- Any operational config

---

## 7. Worker Configuration

```toml
# Minimal config for a worker node.
# Can also be set entirely via environment variables — no config file needed.

[node]
role = "worker"
id   = ""   # auto-generated if empty; set for stable identity

[worker]
# gRPC address of the controller.
# Required when role = "worker". Can be a load-balancer address.
controllerURL = "grpc://orchestra-controller:9090"

# Maximum tasks to execute concurrently.
maxConcurrentTasks = 4

# Optional HTTP health endpoint for Kubernetes liveness probes.
healthAddr = ""  # e.g. "0.0.0.0:8081"
```

Equivalently, via environment variables (no config file at all):

```sh
APP_NODE_ROLE=worker
APP_WORKER_CONTROLLERURL=grpc://orchestra-controller:9090
APP_WORKER_MAXCONCURRENTTASKS=8
APP_WORKER_HEALTHADDR=0.0.0.0:8081
```

---

## 8. Controller Configuration Changes

The controller gains a gRPC listener in addition to the existing HTTP server:

```toml
[controller]
# gRPC address for the worker gateway endpoint.
# Workers connect here. Disable by leaving empty.
grpcAddr = "0.0.0.0:9090"

# Maximum tasks to hold in the dispatch queue (pending dispatch to a worker).
# Tasks beyond this limit wait in the DB as pending.
dispatchQueueDepth = 256
```

The controller's HTTP API gains two new endpoints:

| Method | Path | Description |
|---|---|---|
| GET | `/api/workers` | List connected workers with status, capabilities, in-flight count |

---

## 9. Worker Registry (In-Memory, Controller Side)

```go
// internal/workerregistry/registry.go

type WorkerEntry struct {
    ID             string
    Capabilities   []string
    MaxConcurrent  int
    InFlight       int          // tasks currently executing on this worker
    ConnectedAt    time.Time
    Stream         grpc.Stream  // the live gRPC stream
}

type Registry struct {
    mu      sync.RWMutex
    workers map[string]*WorkerEntry
    // taskID → workerID for in-flight tracking
    inFlight map[int64]string
}

func (r *Registry) Register(entry *WorkerEntry)
func (r *Registry) Deregister(workerID string) []int64  // returns orphaned task IDs
func (r *Registry) PickWorker(activityName string) (*WorkerEntry, bool)
func (r *Registry) RecordSent(taskID int64, workerID string)
func (r *Registry) RecordResult(taskID int64) string   // returns workerID
func (r *Registry) List() []WorkerEntry
```

---

## 10. Database Schema

No new tables required for the worker in this model. The `workers` table from Plan A is **optional** here — worker identity is entirely in-memory since the controller owns the connection state.

If persistent worker visibility is desired (e.g., show historical workers in the UI), a lightweight registration record can still be written on connect:

```sql
CREATE TABLE workers (
    id            VARCHAR(64) PRIMARY KEY,
    capabilities  TEXT        NOT NULL DEFAULT '[]',
    max_concurrent INT        NOT NULL DEFAULT 4,
    registered_at TIMESTAMPTZ NOT NULL,
    last_seen_at  TIMESTAMPTZ NOT NULL
    -- No status column; status is derived from active gRPC connections in memory
);
```

This write happens once per connection (not on every heartbeat), so it is not a hot-path concern.

---

## 11. Kubernetes Deployment

```yaml
# Controller — serves HTTP + gRPC, owns DB connection
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
        - name: APP_CONTROLLER_GRPCADDR
          value: "0.0.0.0:9090"
        - name: APP_WORKFLOW_DATABASEURL
          valueFrom:
            secretKeyRef:
              name: orchestra-db
              key: url
        ports:
        - name: http
          containerPort: 8080
        - name: grpc
          containerPort: 9090
---
# Worker — needs only the gRPC address; no DB credentials
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
        env:
        - name: APP_NODE_ROLE
          value: worker
        - name: APP_WORKER_CONTROLLERURL
          value: "grpc://orchestra-controller:9090"
        - name: APP_WORKER_MAXCONCURRENTTASKS
          value: "8"
        - name: APP_WORKER_HEALTHADDR
          value: "0.0.0.0:8081"
        # No database secret needed at all
        livenessProbe:
          httpGet:
            path: /livez
            port: 8081
---
# Internal Service exposing gRPC port to workers
apiVersion: v1
kind: Service
metadata:
  name: orchestra-controller
spec:
  selector:
    app: orchestra-controller
  ports:
  - name: http
    port: 8080
  - name: grpc
    port: 9090
```

Workers need **no secrets** mounted. The only sensitive data (DB URL, OpenAI key) lives on the controller.

---

## 12. Implementation Phases

### Phase 1 — gRPC server skeleton on controller
1. Add `google.golang.org/grpc` dependency
2. Write `.proto` file; generate Go code with `protoc` / `buf`
3. Controller starts a gRPC listener on `controller.grpcAddr`
4. Implement `Session` stream handler: accept `WorkerHello`, send `ConfigSync`
5. Implement `WorkerRegistry` struct

### Phase 2 — Worker gRPC client
1. Worker dials `controllerURL` on startup with automatic reconnect (exponential backoff)
2. Sends `WorkerHello`; receives and applies `ConfigSync`
3. Worker blocks on stream `Recv()` waiting for `TaskAssignment`

### Phase 3 — Task dispatch loop on controller
1. Controller's `runWorkerPass` changes: instead of executing locally, after `claimNextTask` it calls `registry.PickWorker(activityName)` and sends `TaskAssignment` on the stream
2. Controller pre-fetches activity-specific data (script source, agent def) before sending
3. Controller resolves template inputs before sending (calls existing `resolveStepInput`)

### Phase 4 — Result processing on controller
1. Controller's stream handler receives `TaskResult`
2. Routes to `completeTask` / `handleTaskFailure` / `delayTask` / `waitTaskForSignal`
3. Decrements worker in-flight count in registry

### Phase 5 — Disconnection handling
1. On stream close, `registry.Deregister(workerID)` returns orphaned task IDs
2. Controller calls `releaseTask(taskID)` to reset status to `pending` for each
3. Attempts immediate re-dispatch to remaining workers

### Phase 6 — Health endpoint on worker
1. Worker starts a minimal HTTP server on `healthAddr`
2. `GET /livez` → 200 OK if gRPC stream is active; 503 if reconnecting

### Phase 7 — UI: connected worker roster
1. `GET /api/workers` returns in-memory registry snapshot
2. Dashboard panel: worker ID, capabilities, in-flight task count, connected duration

---

## 13. Plan A vs Plan B Comparison

| Dimension | Plan A (workers with DB) | Plan B (workers via gRPC) |
|---|---|---|
| Worker setup complexity | Needs DB credentials + config file | Just a controller URL |
| DB connections | N workers × pool size | Controller only (fixed pool) |
| Network security | Workers need DB network access | Workers need only controller port |
| Task latency | Poll interval delay (1 s default) | Push → near-zero dispatch latency |
| Controller load | Low (just API traffic) | Higher (owns all DB task I/O) |
| Controller is SPOF for execution | No (workers write DB directly) | Yes (mitigated by HA controllers) |
| In-flight recovery on crash | Lease expiry (up to 30 s) | Immediate re-dispatch on disconnect |
| gRPC dependency | None | `google.golang.org/grpc` + protoc |
| Worker binary size | Same as controller | Smaller (no DB driver needed) |
| Multi-tenancy / isolation | Harder (workers share DB creds) | Natural (workers have zero DB access) |
| Config management | Each worker needs its own config | Centralised on controller |
| Suitable for | Trusted internal clusters, simple ops | Restricted environments, edge workers, SaaS multi-tenant |

---

## 14. Open Questions / Future Considerations

- **gRPC-Web / TLS**: Workers connecting over the public internet need mTLS or token-based auth on the gRPC channel. This is deferred to the auth session but the proto should leave room for metadata headers.
- **Capability filtering**: The `ActivityPayload` oneof can be extended for new activity types without breaking existing workers (proto backward compatibility).
- **Streaming activity output**: Long-running activities (e.g., LLM streaming) could send incremental `TaskProgress` messages back on the stream for real-time UI updates — a natural extension of the bidirectional stream.
- **Worker pools**: Multiple worker deployments with different capability sets (e.g., one pool for CPU-heavy script tasks, another for I/O-bound HTTP tasks) — the dispatcher's `PickWorker` already selects by capability.
- **Back-pressure**: If the controller's dispatch queue fills up, it should stop claiming tasks from the DB rather than holding leases it cannot dispatch. This prevents lease expiry churn.
- **Hybrid mode**: A single deployment could run Plan A workers (with DB) alongside Plan B workers (gRPC-only) simultaneously — the controller dispatches to whichever pool is available, falling back to self-execution if no gRPC workers are connected.
