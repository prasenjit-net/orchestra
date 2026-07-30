# Operations and Reliability

## Reliability model

Orchestra's durable database is the recovery authority. Process memory contains live
subscriptions, poll wakeups, provider token cache, and temporary execution state, none
of which is required to reconstruct persisted workflow status.

The execution guarantee is at-least-once at the activity boundary. Database transitions
are transactional, but external calls are outside the transaction and may be repeated
after a crash or lease expiry.

## Health signals

### Process liveness

Every controller exposes `GET /livez` on the main server. Worker-only processes expose
the same path on `node.healthAddr`. This proves the HTTP process is responsive, not that
the database or every downstream integration is healthy.

### Application health

The public `GET /api/health` route returns service and build metadata. Controllers
publish periodic health updates to their local live bus every 15 seconds.

### Cluster status

The `nodes` table records heartbeats. A node is online when `last_seen_at` is within the
configured offline threshold. The cluster health-check API also actively probes each
advertised address. Stale rows indicate ungraceful termination or lost database access.

## Logging

Structured logging uses Go `slog` with text or JSON output and configurable level.
Request logs include method, path, status, bytes, duration, and request ID. Workflow
logs add component and task/run context where available. Startup logs identify role,
address, environment, and development mode.

Logs must not include passwords, session tokens, CSRF tokens, API key secrets, provider
keys, raw authorization headers, or connector headers. Error messages sent to clients
must also avoid downstream credential leakage.

## Durable observability

Three persistent views complement logs:

- `workflow_events`: ordered execution history per run;
- `workflow_tasks`: queue, attempts, leases, errors, and executing node;
- `security_audit_events`: actor/action/outcome records for security operations.

The UI exposes run history, queues, operations, cluster state, and security audit panels.
These are operational tools over primary tables, not a metrics or tracing backend.

## Common failure scenarios

### Worker process exits during an activity

The running task retains its lease. Another worker pass requeues it after expiry and
executes it again. Any accepted external side effect may be duplicated. The old node row
becomes offline if graceful deregistration did not occur.

### Controller process exits

Worker-only nodes continue polling and persisting workflow state. Browser/API traffic
must fail over through the reverse proxy. Sessions remain valid because they are stored
in the shared database. In-process WebSocket subscriptions and provider token caches are
lost and reconnect/rebuild naturally.

### Database outage

Controllers cannot authenticate or serve stateful APIs, and workers cannot claim or
commit tasks. An activity already executing may complete its external call but fail to
persist the result; it can repeat after recovery. Do not extend lease duration as a
substitute for database availability monitoring.

### Downstream HTTP or AI outage

The activity returns an error and follows its retry policy. Provider and activity HTTP
clients apply timeouts. Repeated failures eventually mark the workflow failed. Operators
can retry or requeue after correcting the dependency.

### MCP disconnect or tool failure

Connection/initialization failure fails the agent activity. A tool call failure is
converted to tool result text and returned to the model, which may recover or finish.
Malformed or unknown tool calls are likewise represented in conversation context.

### Callback failure

The run remains terminal because callback delivery happens after completion commit.
Callback status records success/failure, but there is no durable callback outbox or
automatic retry queue. Consumers needing guaranteed delivery should poll the result API
or the application should gain an outbox before relying on callbacks.

Callbacks are currently emitted only for successfully completed runs. Failure and
cancellation are observable through polling and authenticated APIs, not callbacks.

### WebSocket interruption

The frontend reconnects and switches query stale time to zero. No durable state is lost.
Events committed on a different process may never reach the socket; REST refetch remains
the consistency path. A full 64-event subscriber buffer drops its oldest event and
attempts to emit `missed_events`, which also requires a REST refetch.

## Idempotency guidance

Side-effecting activity designs should use a stable operation key, preferably derived
from workflow ID, task ID, and logical action. Remote systems should reject duplicate
keys or return the original result. Activities should persist remote operation IDs in
output or activity state when a later retry can query status instead of repeating work.

Do not derive an idempotency key from attempt number; attempts intentionally change on
retry while the logical operation remains the same.

## Capacity and performance

Important controls and limits:

| Resource | Current bound |
|---|---|
| HTTP request body | 4 MiB |
| HTTP headers | 1 MiB |
| Activity HTTP response | 1 MiB |
| MCP SSE line buffer | 1 MiB |
| Script source/output/steps | Configurable, defaults 16 KiB/256 KiB/25,000 |
| API request timeout | 30 seconds at `/api` router |
| AI HTTP request | Provider client timeout |
| Tasks per worker pass | 16 sequential attempts |
| SQLite connections | 1 |
| PostgreSQL pool | 25 open, 5 idle per process |

Increasing worker process count increases PostgreSQL connections and claim contention.
`node.maxConcurrentTasks` is currently informational and should not be used for capacity
calculation. Effective throughput is one activity at a time per worker process, bounded
by downstream latency and poll scheduling.

## Maintenance jobs

Controller startup performs auth cleanup, then repeats hourly. Cleanup removes expired
sessions, old revoked sessions, expired rate-limit buckets, and audit events beyond
retention as implemented by the auth store.

Workflow task lease cleanup occurs at the beginning of every worker pass. There is no
separate scheduler or leader election; duplicate cleanup updates are safe.

## Backup runbook

1. Use a database-native consistent snapshot.
2. Encrypt backup data and restrict access.
3. Record the binary version and configuration schema used at backup time.
4. Back up deployment configuration/secret references separately; do not place raw
   secrets in the same broadly accessible archive.
5. Test restoration into an isolated environment.
6. Verify users, definitions/versions, active runs, tasks, and resource references.

For SQLite, include WAL consistency. For PostgreSQL, use logical or physical backups
appropriate to the operator's recovery objectives.

## Restore runbook

1. Stop controllers and workers to prevent writes.
2. Restore the database to a compatible server.
3. Apply only migrations expected by the target Orchestra version.
4. Start one controller and validate schema/login.
5. Inspect running leases; allow expiry or requeue deliberately.
6. Start workers, then additional controllers.
7. Verify external API keys and callbacks without exposing their secrets.

After point-in-time restore, external effects performed after the restore point may be
repeated because database state no longer records them.

## Security recovery

### Lost administrator password

Use `users reset-password` with a protected file or stdin. Confirm session revocation,
sign in, and change the temporary password.

### Leaked API key

Revoke or rotate immediately, update the caller, review audit events and affected run
attribution, and narrow grants if overbroad.

### Compromised provider or connector credential

Rotate at the provider, update configuration/database connector headers, restart or
re-explore as required, and review workflow/audit history for unexpected execution.

## Operational alerts to implement externally

Orchestra does not expose a Prometheus endpoint. Logs/database/API polling should alert
on:

- repeated lease requeues;
- rising pending or failed task count;
- workflows stuck running beyond expected duration;
- offline controller/worker nodes;
- database connection failures;
- repeated login or API key denials/rate limits;
- callback failures;
- restart loops and schema startup errors.

## Reliability limitations

- No cross-process live event transport.
- No lease heartbeat for long-running activities.
- No durable outbox for callbacks or live events.
- No exactly-once external side effects.
- No process-local worker pool despite configured concurrency metadata.
- No first-class metrics, distributed tracing, or alert manager integration.
- No workflow retention/archival job; database growth is operator-managed.
