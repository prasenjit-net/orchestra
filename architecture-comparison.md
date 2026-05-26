# Orchestra — Architecture Comparison: Plans A, B, and C

## Plans at a Glance

| | **Plan A** | **Plan B** | **Plan C** |
|---|---|---|---|
| Document | `controller-worker-plan.md` | `worker-grpc-plan.md` | `symmetric-nodes-plan.md` |
| Core idea | Workers poll DB directly; controller tracks health via HTTP pings | Workers are DB-free; controller owns all DB I/O and pushes tasks over gRPC | All nodes are identical; `--controller` / `--worker` flags toggle subsystems |
| Inter-node protocol | HTTP ping (health only) | gRPC bidirectional stream (tasks + config + health) | None — PostgreSQL is the only shared channel |
| New dependencies | None | `google.golang.org/grpc`, `protoc` / `buf` | None |

---

## Architecture Diagrams

### Plan A — Workers with Direct DB + HTTP Ping

```
Browser ──► [ Controller ] ──► PostgreSQL ◄── [ Worker ] [ Worker ] [ Worker ]
                │   ▲                                │
                │   └── POST /workers/{id}/ping ─────┘
                └── LISTEN orchestra_events ◄── pg_notify (from workers)
```

### Plan B — Workers via gRPC, No DB

```
Browser ──► [ Controller ] ──► PostgreSQL
                │   ▲
     gRPC stream│   │ TaskResult
   TaskAssignment   │
                ▼   │
            [ Worker ] [ Worker ] [ Worker ]
```

### Plan C — Symmetric Nodes, Flag-Controlled

```
Browser ──► [ --controller ] ──► PostgreSQL ◄── [ --worker ] [ --worker ]
                │                    │
                └── LISTEN ◄─ pg_notify (from --worker nodes)
```

---

## 1. Setup and Configuration

| Dimension | Plan A | Plan B | Plan C |
|---|---|---|---|
| **Worker startup requirements** | DB credentials + config file + `controllerURL` | Controller URL only (`APP_WORKER_CONTROLLERURL`) | DB credentials + config file (same as controller) |
| **Secrets on worker nodes** | DB password, OpenAI key (if applicable) | None — all secrets stay on controller | DB password, OpenAI key (same as Plan A) |
| **Config file needed on worker** | Yes — partial config with `[workflow]` section | No — env vars only is sufficient | Yes — identical config to controller |
| **New config sections** | `[node]`, `[worker]`, `[controller]` | `[node]`, `[worker]`, `[controller]` (with `grpcAddr`) | `[node]` with two booleans (`controller`, `worker`) |
| **Backward compatibility** | `role = "all"` default; no change | `role = "all"` default; no change | No flags = both on; no change |
| **Kubernetes Secrets required** | `orchestra-db` on both controller and worker pods | `orchestra-db` on controller pods only | `orchestra-db` on both controller and worker pods |

**Winner: Plan B** for minimal worker config. **Plan C** is simplest overall since there is nothing new to learn — workers are just controllers with the UI off.

---

## 2. Dependencies and Implementation Effort

| Dimension | Plan A | Plan B | Plan C |
|---|---|---|---|
| **New Go packages** | `pingtracker` (small) | `google.golang.org/grpc`, generated proto code, `workerregistry` | None |
| **New toolchain** | None | `protoc` or `buf`, proto codegen in CI | None |
| **Proto schema to maintain** | None | Yes — versioned `.proto` file; breaking changes require migration | None |
| **Existing code changes** | Moderate — split serve.go, add ping loop, add pg_notify | Large — rewrite task dispatch path; new dispatcher sits between DB and gRPC | Minimal — add two booleans to serve.go, skip starting HTTP or `svc.Start()` |
| **Estimated implementation phases** | 8 phases | 7 phases (but each is larger) | 6 phases (but phases 1–2 are trivial) |
| **Risk of regression** | Moderate — existing task path unchanged; new ping path is additive | High — dispatcher replaces core `RunOnce` execution path | Low — existing path unchanged; only `if isWorker` and `if isController` guards added |
| **Testing surface** | Unit tests for ping tracker; integration for pg_notify | Proto codegen, gRPC mock, dispatcher logic, reconnect logic | Near-zero new tests needed for flag wiring |

**Winner: Plan C** by a wide margin. **Plan B** carries the highest implementation risk because it restructures the core task execution path.

---

## 3. Task Distribution

| Dimension | Plan A | Plan B | Plan C |
|---|---|---|---|
| **Mechanism** | Workers poll DB with `FOR UPDATE SKIP LOCKED` | Controller polls DB, picks a worker from registry, pushes `TaskAssignment` over gRPC | Workers poll DB with `FOR UPDATE SKIP LOCKED` (identical to Plan A) |
| **Who holds the lease** | The worker that claimed the task | The controller (lease owner = `ctrl-<id>`) | The worker that claimed the task (identical to Plan A) |
| **Who writes the result** | The worker that executed the task | The controller, after receiving `TaskResult` from the worker | The worker that executed the task (identical to Plan A) |
| **Task dispatch latency** | Up to `pollInterval` (default 1 s) | Near-zero — task pushed immediately after claim | Up to `pollInterval` (default 1 s) |
| **Controller on hot path** | No — workers claim and complete independently | Yes — every task claim, dispatch, and result write goes through the controller | No — workers claim and complete independently |
| **Back-pressure when workers full** | Natural — workers stop claiming when semaphore saturated | Controller must stop claiming when no worker has capacity; risks holding leases it cannot dispatch | Natural — same as Plan A |
| **Task routing by capability** | Worker filters by supported activity when claiming | Controller selects worker by capability before sending | Worker filters by supported activity when claiming |
| **Duplicate execution risk** | Lease expiry guard (standard) | Double: lease expiry OR controller reconnection sends task to new worker while old still executing | Lease expiry guard (standard) |

**Winner: Plans A and C** for task distribution simplicity and correctness. **Plan B** introduces a subtle double-execution window during controller failover that needs careful handling.

---

## 4. Worker Health Monitoring

| Dimension | Plan A | Plan B | Plan C |
|---|---|---|---|
| **Mechanism** | Worker sends `POST /api/workers/{id}/ping` to controller at `pingInterval` | gRPC keepalive — connection liveness = worker liveness | Worker updates `last_seen_at` in DB on every `runWorkerPass` tick |
| **Where health state lives** | Controller in-memory `pingTracker` map | Controller in-memory `WorkerRegistry` | PostgreSQL `workers` table |
| **Health state survives controller restart** | No — tracker is lost; workers must re-ping | No — registry is lost; workers must reconnect | Yes — DB persists; any controller reading the table sees current state |
| **Health state consistent across multiple controllers** | No — each controller has its own tracker; pings may hit different controllers behind a LB | No — each controller's registry reflects only its own connected workers | Yes — all controllers read the same DB table |
| **Staleness of health data** | `pingInterval` (default 10 s) | Near-real-time (gRPC keepalive fires in seconds) | `pollInterval` (default 1 s) — very fresh |
| **Extra network traffic** | `N workers × 1 ping / 10 s` — small but nonzero | Built into gRPC keepalive; negligible overhead | None — piggybacks on existing DB poll |
| **Worker self-reports health** | Yes — active push | Implicit — stream is alive or not | Yes — passive DB write |
| **Offline detection threshold** | `pingInterval × missedPingThreshold` (configurable) | Immediate on stream close | `3 × pollInterval` (derived, no new config) |
| **Effect on task scheduling** | None — health is purely informational | None — task recovery via lease expiry or immediate re-dispatch | None — health is purely informational |

**Winner: Plan C** for health monitoring — it's consistent across all controllers, survives restarts, and adds zero extra network traffic. **Plan B** has the fastest detection but is ephemeral. **Plan A** has the most consistency problems in multi-controller deployments.

---

## 5. Live Event Propagation (WebSocket / UI Updates)

| Dimension | Plan A | Plan B | Plan C |
|---|---|---|---|
| **Single-process mode** | In-process `livebus.Bus` | In-process `livebus.Bus` | In-process `livebus.Bus` |
| **Distributed mode mechanism** | Workers call `pg_notify`; controllers `LISTEN` | Controller writes results and calls `pg_notify`; other controllers `LISTEN` | Workers call `pg_notify`; controllers `LISTEN` |
| **Who calls `pg_notify`** | Workers — inside the task result transaction | Controller — after receiving `TaskResult` and committing to DB | Workers — inside the task result transaction |
| **Notification and state consistency** | Atomic — `pg_notify` is inside the same transaction that writes the result | Consistent — controller commits result then notifies | Atomic — same as Plan A |
| **Required for single controller + N workers** | Yes — otherwise browser goes silent | Yes — same reason | Yes — same reason |
| **Required for multiple controllers** | Yes — each controller must relay its own view | Yes | Yes |
| **Extra DB connections** | 1 persistent `LISTEN` connection per controller | 1 persistent `LISTEN` connection per controller | 1 persistent `LISTEN` connection per controller |
| **Implementation** | `pg_notify` in worker + `LISTEN` goroutine on controller | `pg_notify` in controller after commit + `LISTEN` on other controllers | `pg_notify` in worker + `LISTEN` goroutine on controller |

**All plans are equal** here — all require `pg_notify` / `LISTEN` for distributed live events. The only difference is who calls `pg_notify` (worker in A/C, controller in B).

---

## 6. Failure Handling

### Worker crashes mid-task

| | Plan A | Plan B | Plan C |
|---|---|---|---|
| **Detection** | Lease expires after `leaseDuration` (default 30 s) | gRPC stream closes → immediate | Lease expires after `leaseDuration` |
| **Recovery** | Any worker's `requeueExpiredTasks` resets task to `pending` | Controller re-dispatches immediately to another worker | Any worker's `requeueExpiredTasks` resets task to `pending` |
| **Recovery time** | Up to `leaseDuration` (30 s) | Seconds | Up to `leaseDuration` (30 s) |
| **Risk of duplicate execution** | None — lease prevents double-claim | Possible if worker was mid-execution and controller re-dispatches before old worker finishes | None — same as Plan A |

### Controller crashes

| | Plan A | Plan B | Plan C |
|---|---|---|---|
| **Impact on in-flight tasks** | None — workers hold their own leases and write results independently | High — all in-flight tasks were leased by the crashed controller; recovery waits for lease expiry | None — workers hold their own leases |
| **Recovery time** | Zero for in-flight tasks; health ping state is lost (workers re-ping) | Up to `leaseDuration` (lease held by crashed controller) | Zero for in-flight tasks |
| **Worker behaviour** | Workers continue executing and writing results normally | Workers lose stream; retry connection; tasks pause until new controller is available | Workers continue executing and writing results normally |
| **Multiple controllers mitigate?** | Yes — workers ping a different controller | Partially — another controller takes over DB polling, but in-flight tasks still wait for lease | Yes — workers continue as if nothing happened |

### Database failure

| | Plan A | Plan B | Plan C |
|---|---|---|---|
| **Impact** | All nodes stall — workers can't claim, controllers can't serve writes | Controllers stall — workers may finish already-assigned in-flight tasks before noticing | All nodes stall — same as Plan A |
| **In-flight tasks during outage** | Worker may complete execution but can't write result; retries on reconnect | Worker completes and returns result to controller; controller can't write; must buffer or retry | Same as Plan A |
| **Partial resilience** | None — DB is required for every step | Marginal — already-dispatched tasks can execute offline, but results can't be saved | None |

**Winner: Plans A and C** for controller crash resilience — workers are fully autonomous and unaffected. **Plan B** has the worst controller-crash story: every in-flight task stalls until lease expiry.

---

## 7. Security and Access Control

| Dimension | Plan A | Plan B | Plan C |
|---|---|---|---|
| **DB credentials on worker** | Yes — full `databaseURL` required | No — zero DB access | Yes — full `databaseURL` required |
| **API keys (OpenAI etc.) on worker** | Yes — required in worker config | No — synced from controller over gRPC | Yes — required in worker config |
| **Network access worker needs** | DB port (5432) + controller HTTP port (ping) | Controller gRPC port only | DB port (5432) only |
| **Secret blast radius if worker is compromised** | DB + API keys exposed | Nothing — worker holds no secrets | DB + API keys exposed |
| **Suitable for untrusted worker environments** | No | Yes — worker has zero credentials | No |
| **Suitable for multi-tenant / SaaS** | Difficult — workers share DB access | Natural — controller mediates all tenant data | Difficult — same as Plan A |
| **Suitable for edge / remote workers** | Limited — requires DB network path | Yes — only HTTPS/gRPC to controller needed | Limited — same as Plan A |
| **mTLS / auth between nodes** | Not defined — ping is unauthenticated | Defined — gRPC metadata headers; mTLS native to gRPC | Not applicable — no inter-node communication |

**Winner: Plan B** for security isolation. Plans A and C are equivalent — both require DB credentials on every node.

---

## 8. Scalability

| Dimension | Plan A | Plan B | Plan C |
|---|---|---|---|
| **Worker scale-out** | Add workers pointing at same DB; no controller change | Add workers connecting to same controller; controller registry grows | Add workers with same config; no controller change |
| **DB connection growth with workers** | Linear — N workers × pool size | Constant — controller pool only | Linear — same as Plan A |
| **DB connection ceiling** | Can exhaust PgBouncer/pg `max_connections` at high worker count | Fixed ceiling regardless of worker count | Same as Plan A |
| **Controller scale-out** | Add controllers behind LB; ping state inconsistent across them | Add controllers behind LB; each has own registry; tasks distributed to workers per-controller | Add controllers behind LB; all read same DB; fully consistent |
| **Worker-count ceiling** | DB connection limit | Number of concurrent gRPC streams per controller (typically thousands) | Same as Plan A |
| **Horizontal scaling story** | Good for moderate worker counts | Best — controller buffers all DB pressure | Good for moderate worker counts |
| **Task throughput ceiling** | DB write throughput × number of workers | DB write throughput × number of controllers | Same as Plan A |

**Winner: Plan B** at very large scale (hundreds of workers) because DB connections stay constant. **Plans A and C** are fine for tens of workers but hit connection limits earlier.

---

## 9. Operational Complexity

| Dimension | Plan A | Plan B | Plan C |
|---|---|---|---|
| **Debugging a stuck task** | Check DB `lease_owner`, check worker logs | Check controller dispatcher log, check gRPC stream, check worker logs | Check DB `lease_owner`, check worker logs (identical to Plan A) |
| **Number of moving parts** | DB + HTTP ping + pg_notify | DB + gRPC stream + pg_notify + dispatcher | DB + pg_notify |
| **Ports to expose/firewall** | DB port (workers), HTTP port (ping + UI) | DB port (controller), gRPC port (workers), HTTP port (UI) | DB port (all nodes), HTTP port (controllers) |
| **Rolling upgrade of workers** | Stop old worker; start new; lease expiry handles in-flight | Graceful stream close; controller re-dispatches; clean | Stop old worker; start new; lease expiry handles in-flight |
| **Adding a new activity type** | Deploy new worker binary with activity registered | Deploy new worker binary; controller checks capability match before dispatch | Deploy new worker binary with activity registered |
| **Config drift risk** | Workers need subset of controller config; can drift | Workers have no config; no drift possible | All nodes share same config; no drift possible |
| **Observability requirements** | Logs on workers + controllers; DB state | Logs on workers + controllers; gRPC stream metrics; dispatcher queue depth | Logs on workers + controllers; DB state |
| **Single-process dev mode** | Supported — `role = "all"` | Supported — `role = "all"` | Supported — no flags |

**Winner: Plan C** — fewest moving parts, no new ports, no config drift. **Plan B** is the most operationally complex.

---

## 10. Deployment Context Fit

| Use Case | Plan A | Plan B | Plan C |
|---|---|---|---|
| **Local development / POC** | ✓ (all-in-one default) | ✓ (all-in-one default) | ✓ (no flags = both) |
| **Small production (1–5 workers)** | ✓ Good | ✓ Overkill | ✓ Best fit |
| **Medium production (5–50 workers)** | ✓ Good | ✓ Good | ✓ Good |
| **Large production (50+ workers)** | ⚠ DB connections may strain | ✓ Best fit | ⚠ Same as Plan A |
| **Workers in restricted network / no DB access** | ✗ Requires DB access | ✓ Only gRPC needed | ✗ Requires DB access |
| **Edge / remote workers** | ✗ DB must be reachable | ✓ Controller is the only endpoint | ✗ DB must be reachable |
| **SaaS / multi-tenant** | ⚠ Shared DB creds | ✓ Controller mediates tenant isolation | ⚠ Same as Plan A |
| **Air-gapped / on-premise** | ✓ All on-prem | ✓ All on-prem | ✓ All on-prem |
| **Kubernetes with HPA on workers** | ✓ Workers are stateless | ✓ Workers are stateless | ✓ Workers are stateless |
| **Mixed capability worker pools** | ✓ Supported via capability column | ✓ Dispatcher routes by capability | ✓ Supported via capability column |

---

## 11. Summary Scorecard

Score: ✓✓ = strong advantage, ✓ = adequate, ⚠ = acceptable with caveats, ✗ = weakness

| Category | Plan A | Plan B | Plan C |
|---|---|---|---|
| Setup simplicity for workers | ⚠ | ✓✓ | ✓ |
| Implementation effort | ✓ | ⚠ | ✓✓ |
| Regression risk | ✓ | ⚠ | ✓✓ |
| Task distribution correctness | ✓✓ | ✓ | ✓✓ |
| Task dispatch latency | ⚠ | ✓✓ | ⚠ |
| Controller crash resilience | ✓✓ | ⚠ | ✓✓ |
| Worker crash recovery time | ⚠ (lease expiry) | ✓✓ (immediate) | ⚠ (lease expiry) |
| Health monitoring consistency | ⚠ | ✓ | ✓✓ |
| Worker security isolation | ⚠ | ✓✓ | ⚠ |
| DB connection efficiency | ✓ | ✓✓ | ✓ |
| Operational simplicity | ✓ | ⚠ | ✓✓ |
| No new dependencies | ✓✓ | ✗ | ✓✓ |
| Config consistency across nodes | ⚠ | ✓✓ | ✓✓ |
| Multi-controller consistency | ⚠ | ✓ | ✓✓ |
| Suitable for restricted networks | ✗ | ✓✓ | ✗ |

---

## 12. Recommendation Guide

```
Does your deployment require workers in restricted
networks without DB access (edge, SaaS, untrusted)?
│
├─ Yes ──► Plan B (gRPC)
│           Accept: gRPC dependency, higher controller load,
│           complex implementation.
│
└─ No
    │
    Do you expect 50+ concurrent worker nodes?
    │
    ├─ Yes ──► Plan B (gRPC) if the implementation cost is acceptable,
    │           otherwise Plan A/C with PgBouncer to manage connections.
    │
    └─ No
        │
        Is minimising implementation effort a priority?
        │
        ├─ Yes ──► Plan C (symmetric nodes)
        │           Two boolean flags in serve.go.
        │           All existing logic untouched.
        │           All nodes share config and DB.
        │
        └─ No / Need explicit role separation
            │
            └─► Plan A (DB-polling + HTTP ping)
                 Clearer role boundaries than C.
                 Workers are explicitly separate from controllers.
                 Health tracking is in-memory (not DB), keeping DB writes clean.
```

---

## 13. Hybrid Paths

These plans are not mutually exclusive. Possible combinations:

- **Start with Plan C** (easiest), then migrate to **Plan A** by adding HTTP ping for more explicit health tracking without touching core logic.
- **Plan A → Plan B** is feasible: Plan B's controller dispatcher can fall back to self-execution (Plan A style) when no gRPC workers are connected, making it a superset of Plan A at the cost of the gRPC layer.
- **Plan C + pg_notify** is the natural first step regardless of which plan is chosen — `LISTEN / NOTIFY` is required for any multi-node live event propagation and is independent of the task dispatch model.

The implementations share a common core: the `workers` table, `FOR UPDATE SKIP LOCKED` in `claimNextTask`, and `pg_notify` in state-change transactions are needed by all three and can be delivered in a single shared phase before committing to A, B, or C for the rest.
