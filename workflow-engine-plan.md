# Durable Workflow Engine Plan

## Goal

Build a **durable workflow engine** in this repository, implemented in Go and integrated into the existing server, **without using any third-party workflow library**. The engine should survive process restarts, resume in-flight workflows, support retries and timers, and provide a clear control plane for starting, observing, and operating workflows.

## Success Criteria

1. A workflow instance can be started, persisted, resumed after restart, and completed without losing state.
2. Activity execution is retried safely with explicit idempotency boundaries.
3. Timers, backoff, and delayed work survive crashes and deploys.
4. The engine can recover from worker death by reclaiming leased work.
5. Operators can inspect workflow state, history, pending tasks, failures, and retries through API endpoints and logs.
6. The design remains small enough to maintain in-house and does not depend on a workflow framework.

## Non-Goals

- Building a fully general distributed orchestration platform in v1
- Supporting arbitrary code replay like Temporal/Cadence
- Designing for multi-region consensus in the first iteration
- Running untrusted user-defined code

## Recommended Execution Model

Use a **command-driven state machine** model instead of goroutine-per-workflow durability tricks.

- A workflow definition is a Go type that:
  - reads current workflow state
  - consumes an input event
  - emits commands
  - transitions to a new persisted state
- Commands are interpreted by the engine, for example:
  - `ScheduleActivity`
  - `StartTimer`
  - `CompleteWorkflow`
  - `FailWorkflow`
  - `EmitSignal`
- Activities run outside workflow state transitions and report completion/failure back as events.
- Workflow code stays deterministic because state transitions depend only on persisted state plus explicit events.

This model is more durable and easier to reason about than trying to persist live goroutines, channels, or ad hoc in-memory orchestration.

## Simplest Mental Model

Treat each workflow as a durable state machine:

```text
START
  ->
Step A
  ->
Step B
  ->
DONE
```

After each step:

1. execute the side effect or transition
2. persist the result and history
3. mark the current step complete
4. enqueue the next durable unit of work

If the process crashes, recovery should reload persisted state and continue from the next durable step rather than from in-memory progress.

## Architectural Choice

Use an **orchestrator model** in v1, not choreography.

- The engine remains the single authority that decides the next step.
- Workflows stay easier to inspect, replay, and recover.
- This aligns with the existing single-service shape of this repository.

Choreography can be introduced later for specific integrations, but it should not be the primary execution model for the first durable engine.

## Product Requirement: Built-In UI

The application should include a **first-class web UI** for:

- defining and versioning workflows
- starting and monitoring workflow instances
- inspecting event history and current state
- managing workflow and activity queues
- retrying, requeuing, pausing, canceling, and dead-letter handling
- viewing worker health, timer backlog, and lease recovery status

This should be treated as part of the product, not as an afterthought or admin-only script layer.

## Proposed Repository Layout

```text
internal/
  workflow/
    engine.go          # orchestration loop and public service API
    registry.go        # workflow/activity registration
    types.go           # shared enums, IDs, payloads, command/event types
    errors.go
    clock.go           # real + fake clock for tests
    codec.go           # payload serialization/versioning
    workflowtest/      # engine-level test harness
  workflow/store/
    store.go           # persistence interfaces
    sqlstore.go        # SQL-backed durable implementation
    migrations.go
  workflow/runtime/
    dispatcher.go      # picks runnable items
    workflow_task.go   # workflow state transition executor
    activity_task.go   # activity executor
    timer_scanner.go   # promotes expired timers
    recovery.go        # lease timeout / requeue logic
  workflow/history/
    append.go          # event append helpers
    replay.go          # rebuild instance state from snapshots + events
  workflow/api/
    handler.go         # REST endpoints under /api/workflows
```

If the engine grows meaningfully, promote `internal/workflow` into a top-level domain package, but start inside `internal/` to keep scope controlled.

## Core Concepts

### 1. Workflow Definition

Each workflow definition should declare:

- workflow name and version
- input payload type
- persisted workflow state type
- event handler function
- terminal states

Suggested shape:

```go
type Workflow interface {
    Name() string
    Version() int
    Init(input json.RawMessage) (State, []Command, error)
    Apply(state State, event Event) (State, []Command, error)
}
```

For this product, split workflow definition into two layers:

1. **Engine runtime layer** in Go:
   - command/event model
   - activity registry
   - validation and execution semantics
2. **Definition layer** exposed in the UI:
   - versioned workflow metadata
   - step graph / transition configuration
   - activity bindings and retry/timer policy configuration
   - input schema and operator-facing labels/descriptions

The UI should manage the definition layer, while Go code owns the trusted execution primitives and activity implementations.

### 2. Activity Definition

Activities are side-effecting operations such as HTTP calls, DB writes in external systems, notifications, or file operations.

Each activity should define:

- name and version
- input/output payload types
- timeout
- retry policy
- idempotency contract

### Activity Catalog Expansion Plan

Expand the activity model from the current small built-in set into a broader, opinionated catalog so most workflows can be authored without custom Go code.

Target activity families:

1. **System**
   - `noop`
   - `log`
   - `fail`
   - `transform`
   - `script`
2. **Flow control**
   - `delay`
   - `wait-signal`
   - `branch`
   - `foreach`
   - `parallel-group`
3. **Integration**
   - `http-request`
   - `webhook`
   - `email`
   - `slack`
   - `queue-publish`
4. **Data**
   - `set-context`
   - `json-patch`
   - `template-render`
   - `base64`
   - `hash`
5. **Operator / manual**
   - `approval`
   - `manual-task`
   - `human-wait`

The initial rule should remain: **prefer a curated activity catalog over arbitrary user code**, and only introduce carefully sandboxed scripting as an advanced escape hatch.

### Activity Descriptor Evolution

The existing activity descriptor should grow beyond runtime name/description/category so the UI can render richer editors and operators can understand execution risk.

Each activity descriptor should eventually include:

1. runtime identity
   - name
   - version
   - category
2. authoring metadata
   - display label
   - icon
   - short description
   - field schema
   - defaults
   - examples
3. execution metadata
   - timeout default
   - retry recommendations
   - idempotency notes
   - sensitive field paths
   - whether network access is allowed
   - whether the step may mutate workflow context directly

### Script Activity Plan (Starlark Sandbox)

Add one **sandboxed script activity** powered by **Starlark**, intended for lightweight data transformation and conditional logic inside workflows.

This should be treated as:

- a **trusted-but-constrained operator feature**
- not a general arbitrary code execution environment
- not a replacement for purpose-built integration activities

#### Why Starlark

Starlark is a strong fit because it is:

1. deterministic by design
2. Python-like and approachable for operators
3. embeddable in Go
4. easier to constrain than general JavaScript or shell execution
5. well suited for pure transformation logic over JSON-like data

#### Intended Use Cases

The script activity should support:

1. reshaping step output into a later-step payload
2. deriving flags, labels, and routing decisions from workflow context
3. lightweight validation and normalization
4. building request bodies, headers, or message payloads
5. small reusable helper logic that does not justify a new Go activity yet

It should **not** be used for:

- arbitrary network access
- filesystem access
- spawning processes
- sleeping, blocking, or long-running loops
- bypassing the curated integration/security model

#### Script Activity Contract

Suggested step shape:

```json
{
  "name": "prepare-payload",
  "activity": "script",
  "input": {
    "language": "starlark",
    "script": "result = {\"message\": \"hello %s\" % ctx[\"steps\"][\"fetch\"][\"name\"]}",
    "timeoutMs": 250,
    "exports": ["result"]
  }
}
```

Suggested execution contract:

1. The engine injects a read-only environment:
   - `ctx` → workflow context
   - `step` → current step metadata
   - `workflow` → workflow identifiers/version
   - `input` → resolved activity input payload
2. The script computes values and returns a final result object.
3. The activity output is JSON-encoded and appended like any other activity result.
4. Script failures become normal activity failures with explicit error history.

#### Sandbox Rules

The script runtime must be sandboxed with explicit limits:

1. **No I/O**
   - no filesystem
   - no network
   - no subprocesses
2. **Deterministic builtins only**
   - no wall-clock access from inside the script
   - no randomness unless explicitly injected and versioned
3. **Execution limits**
   - max source size
   - max output size
   - max CPU steps / instruction budget
   - strict timeout
   - bounded recursion / collection growth
4. **Memory safety**
   - small bounded values
   - reject pathological nested structures
5. **Read-only inputs**
   - `ctx`, `workflow`, and `step` should be presented as immutable values to the script

#### Starlark Builtins Plan

For builtins, take inspiration from this repository’s workflow/product focus and create a small standard library aimed at orchestration and payload shaping rather than general programming.

Proposed builtins/modules:

1. **JSON/data helpers**
   - `json.encode(value)`
   - `json.decode(text)`
   - `json.merge(a, b)`
   - `json.pick(value, paths)`
2. **Collection helpers**
   - `collections.compact(list_or_dict)`
   - `collections.group_by(list, key)`
   - `collections.flatten(list)`
3. **String helpers**
   - `strings.trim(value)`
   - `strings.lower(value)`
   - `strings.upper(value)`
   - `strings.contains(value, part)`
   - `strings.replace(value, old, new)`
4. **Time formatting helpers**
   - `time.parse_rfc3339(value)`
   - `time.format_rfc3339(value)`
   - `time.add_seconds(value, n)`
   - keep these deterministic and explicit
5. **Workflow helpers**
   - `workflow.step_output(name)`
   - `workflow.signal(name)`
   - `workflow.fail(message)` for explicit script-side failure
6. **Validation helpers**
   - `asserts.non_empty(value, message)`
   - `asserts.equals(a, b, message)`
   - `asserts.has_keys(obj, keys)`

The first version should keep the builtin surface **very small** and grow only after observing repeated workflow-authoring needs.

#### Security Review Requirements for Script Activity

Before shipping script execution:

1. review Starlark interpreter extension points and limit hooks
2. explicitly document allowed builtins and forbidden capabilities
3. add denial-of-service tests for loops, recursion, large allocations, and oversized outputs
4. ensure activity history redacts script source or secrets when needed
5. make script activity disabled by configuration until the sandbox is proven stable

#### Rollout Plan for Script Activity

1. **Phase A**
   - land descriptor/schema only
   - expose in UI as planned but feature-flagged
2. **Phase B**
   - implement pure Starlark evaluation with read-only context and basic builtins
   - no external side effects
3. **Phase C**
   - add richer helper modules and validation helpers
   - add reusable script snippets/templates in the UI
4. **Phase D**
   - review whether some common scripts should instead become first-class built-in activities

### 3. Durable Workflow Instance

Each instance needs:

- stable workflow instance ID
- workflow type + version
- current status
- current materialized state or snapshot
- last processed event sequence
- next runnable time
- ownership lease metadata
- created/updated timestamps

### 4. Event Stream

Persist an append-only event stream as the **system of record** for auditability, recovery, and replay:

- workflow started
- workflow task scheduled
- activity scheduled
- activity completed
- activity failed
- timer started
- timer fired
- signal received
- workflow completed / failed / canceled

The workflow instance row should be treated as a **projection/snapshot for fast reads**, not the canonical source of truth.

### 5. Leases

All runnable work should be claimed with a lease:

- `lease_owner`
- `lease_expires_at`
- `attempt`

This allows safe recovery if a worker crashes after claiming work.

## Event-Sourced Persistence Design

Use a **storage abstraction first**, with a production-grade SQL implementation, and make **event sourcing the primary persistence model**.

### Storage Model

- `workflow_events` is the canonical source of truth.
- `workflow_instances` stores a materialized projection of the latest known workflow status and snapshot for efficient reads.
- State recovery should be possible by replaying the event stream, optionally starting from the latest snapshot.
- Commands are not persisted as durable truth; resulting events are.

### Event Sourcing Rules

1. Every workflow state transition must append one or more events.
2. A workflow instance snapshot must be derived from the committed event stream.
3. Rebuild and repair operations must be able to reconstruct instance state from events.
4. External side effects belong in activities; workflow state changes belong in events.
5. Event schemas must be versioned so workflow evolution remains safe over time.

### Recommended Tables

1. `workflow_instances`
2. `workflow_events`
3. `workflow_definitions`
4. `workflow_definition_versions`
5. `workflow_tasks`
6. `activity_tasks`
7. `workflow_timers`
8. `workflow_signals`
9. `workflow_dead_letters` (optional but useful)

### Key Table Responsibilities

**workflow_instances**
- current materialized state / snapshot
- workflow metadata
- terminal status
- optimistic concurrency version
- last included event sequence

This table exists for efficient reads, list endpoints, and restart speed. It should be rebuildable from the event stream.

**workflow_events**
- immutable event log
- canonical source of audit and replay
- ordered by per-workflow sequence number
- stores event type, payload, metadata, causation, and correlation identifiers

**workflow_definitions**
- logical workflow identity
- active/published version pointer
- display metadata for the UI
- lifecycle status such as draft, published, deprecated

**workflow_definition_versions**
- immutable versioned workflow definition document
- UI-authored graph or DSL payload
- validation result and publish metadata
- references to allowed activity types and policies

**workflow_tasks**
- units that ask the engine to run a workflow transition

**activity_tasks**
- units that ask workers to execute side effects

**workflow_timers**
- durable delayed wakeups

### Storage Requirements

- transactional updates for event append + snapshot update + task enqueue
- optimistic concurrency on event sequence or workflow version
- indexed lookup by status, lease expiry, scheduled time
- pagination support for operations endpoints
- support for a DB-backed queue as the first delivery model
- event ordering guarantees within each workflow instance
- snapshotting support to avoid replaying the full history on every load

### Snapshot Strategy

- Start with full event retention.
- Add periodic snapshots in `workflow_instances` or a dedicated snapshot table once replay cost justifies it.
- A snapshot should record the last included event sequence so replay can resume from there.
- Snapshot creation should be deterministic and safe to repeat.

### Database Choice

For planning:

- **SQLite** is acceptable for local development and early iteration.
- **Postgres** is the better target for durable multi-process production use.

The engine API should not depend on one database. The store interface should hide vendor details, but the abstraction must preserve event ordering and optimistic concurrency semantics.

For the initial implementation, prefer a **database-backed queue** over adding Kafka, RabbitMQ, Redis Streams, or NATS. It is simpler, fits the current service footprint, and keeps durability and scheduling in one transactional boundary.

## Processing Model

### Workflow Task Loop

1. Claim a runnable workflow task with a lease.
2. Load current workflow snapshot and any events after the last applied sequence.
3. Run deterministic transition logic.
4. In one transaction:
   - append new workflow events
   - update materialized workflow snapshot
   - enqueue downstream tasks/timers/activities
   - mark current task complete
5. Commit.

### Activity Task Loop

1. Claim activity task with lease.
2. Execute the activity with timeout and context.
3. Persist either:
   - `ActivityCompleted` event, or
   - `ActivityFailed` event
4. Enqueue a workflow task so the workflow can react.

### Timer Loop

1. Scan for expired timers.
2. Convert each expired timer into a durable `TimerFired` event or workflow task.
3. Mark timer consumed atomically.

### Recovery Loop

1. Scan for expired leases on workflow and activity tasks.
2. Requeue work that was claimed but not finished.
3. Increment attempt counters and emit recovery logs/metrics.

### End-to-End Execution Flow

```text
API starts workflow
    ->
Append WorkflowStarted event
    ->
Build/update workflow snapshot
    ->
Insert initial workflow task
    ->
Worker claims task
    ->
Execute transition / schedule activity
    ->
Persist events + snapshot update
    ->
Insert next workflow task or terminal event
```

This flow should remain true even as timers, signals, and retries are added.

## Durability Rules

These rules should guide all implementation decisions:

1. **Persist before side effects** when scheduling work.
2. **Never acknowledge a task before durable state is committed.**
3. **All side-effecting activities must be idempotent or deduplicated.**
4. **Workflow transitions must be deterministic.**
5. **Retries must create explicit history.**
6. **Recovery must rely on leases, not in-memory ownership.**
7. **Avoid long-running open transactions; persist small, explicit units of work.**
8. **Workflow state must always be explainable from the event stream.**

## UI and Control Plane Plan

Use the existing React app as the operator console for the workflow engine.

### Main UI Areas

1. **Workflow Definitions**
   - create/edit workflow definitions
   - visualize step graph and transitions
   - configure retries, timers, and activity bindings
   - draft vs published version management
2. **Workflow Instances**
   - list/filter active and completed instances
   - show current state, current step, and next scheduled work
   - inspect event stream and failure details
3. **Queue Management**
   - view workflow task queue, activity queue, timer backlog, and dead letters
   - retry, requeue, pause, resume, cancel, or drain selected items
   - inspect lease ownership, attempt counts, and stuck work
4. **Operations Dashboard**
   - worker health
   - queue depth and age
   - timer lag
   - recent failures and recovery actions

### Definition UX Constraints

- The UI should save workflow definitions as versioned durable documents.
- Publishing a definition version should require validation.
- Running workflow instances must stay pinned to the definition version they started with.
- Editing a draft must not mutate already-running instances.
- The initial UI can be form- and graph-based; a raw JSON/YAML editor can exist as an advanced mode, but the stored model should remain structured and versioned.

## Visual Workflow Builder Plan

Build the definition authoring experience around a **rich drag-and-drop canvas** so most users never need to edit raw JSON.

### Recommended Canvas Library

Use **React Flow (`@xyflow/react`)** as the primary workflow designer canvas.

Why this is the best fit for this repository:

1. It is **React-native**, which matches the existing UI stack and keeps custom nodes, side panels, validation hints, and inline forms easy to build.
2. It ships the core editor behaviors we need out of the box: **dragging, zooming, panning, selection, custom nodes/edges, minimap, controls, and resizing**.
3. It is widely adopted and actively maintained, with strong production evidence and a large install base.
4. It is a **UI library**, not a workflow runtime, so it does not conflict with the goal of avoiding a third-party workflow engine.

### Alternatives Considered

1. **Rete.js**
   - Strong for visual-programming systems and graph processing
   - Better when the graph itself is the execution engine
   - Less natural than React Flow for a React-first product UI
2. **AntV X6**
   - Powerful and feature-rich for enterprise graph editing
   - Strong second choice if we outgrow React Flow’s graph tooling
   - Heavier and less ergonomic for this codebase than React Flow
3. **JointJS / Drawflow**
   - Capable in some scenarios
   - Less attractive than React Flow for a modern React-native workflow builder here

### Authoring UX Goal

The builder should feel like a lightweight BPMN-style editor without forcing BPMN complexity onto the data model.

Users should be able to:

1. drag activities from a palette onto a canvas
2. connect nodes visually to define flow
3. configure each step through forms, toggles, pickers, and schema-aware inputs
4. add branches, waits, retries, conditions, and signals without hand-editing JSON
5. validate the draft before publish and see errors directly on the canvas

Raw JSON should be limited to an **advanced inspector**, not the primary authoring path.

### Target Builder Architecture

Split the builder into four UI layers:

1. **Canvas layer**
   - node placement
   - edge creation
   - zoom/pan/minimap
   - multi-select and keyboard actions
2. **Palette layer**
   - built-in activity catalog
   - trigger nodes
   - control-flow nodes such as branch, delay, signal wait, merge, end
3. **Properties layer**
   - step name and description
   - activity-specific input form
   - retry policy
   - timeout and scheduling controls
   - validation messages
4. **Compilation layer**
   - converts the visual graph into the stored `workflow_definition_versions.document`
   - enforces deterministic ordering and structural validation

### Proposed Visual Node Types

Start with a small, opinionated node system:

1. **Start**
2. **Activity**
3. **Delay / Timer**
4. **Condition**
5. **Signal Wait**
6. **Parallel Split**
7. **Merge / Join**
8. **End**

These should compile to the internal definition document rather than becoming the runtime model directly.

### “Almost No JSON” Strategy

To minimize raw JSON editing:

1. Every registered activity should publish a **UI metadata descriptor** in addition to runtime metadata:
   - label
   - category
   - icon
   - description
   - input field schema
   - defaults
   - validation hints
2. The builder should render **activity-specific forms** from that metadata.
3. Common value types should use first-class controls:
   - text
   - number
   - boolean
   - select
   - key/value headers
   - duration
   - expression/reference picker
4. JSON should only appear for:
   - unknown activity payloads
   - escape-hatch advanced fields
   - import/export and debugging

### Stored Model Strategy

Do not store arbitrary canvas-only blobs as the source of truth.

Instead, keep two related representations:

1. **Canonical definition document**
   - normalized graph/step model used by the engine
   - stable and versioned
2. **Builder layout metadata**
   - node positions
   - collapsed/expanded UI state
   - edge routing preferences
   - optional comments/annotations

The runtime should depend on the canonical document, while the UI rehydrates the canvas from canonical data plus layout metadata.

### Validation Model

Validation should run continuously in the builder and also on publish.

Checks should include:

1. single start node
2. at least one terminal path
3. no disconnected nodes
4. valid edge directions
5. branch nodes with complete outcomes
6. signal and timer nodes with required configuration
7. activity input validation against registered field metadata
8. cycle rules based on what the runtime actually supports

### Suggested Backend Additions for the Builder

Add API support for the visual builder, not just the runtime:

1. `GET /api/workflows/activities`
   - extend response with UI field metadata
   - include activity capability flags such as `supportsSandbox`, `networkAccess`, `pureTransform`, `featureFlag`
2. `GET /api/workflow-definitions/{id}`
   - include builder layout metadata if present
3. `POST /api/workflow-definitions`
   - accept canonical document plus optional layout metadata
4. `POST /api/workflow-definitions/{id}/versions`
   - same as above for draft versions
5. `POST /api/workflow-definitions/validate`
   - validate without saving

### Activity Authoring UX Plan

The builder palette should evolve into a categorized activity catalog with strong defaults and templates.

1. Show top-level categories:
   - system
   - flow control
   - integration
   - data
   - manual/operator
2. Each activity card should expose:
   - concise description
   - risk/safety badge
   - common use-case examples
   - whether the activity is deterministic, side-effecting, or sandboxed
3. The script activity editor should include:
   - Starlark code editor with syntax highlighting
   - builtin reference panel
   - context reference browser
   - snippet/template picker
   - dry-run validation endpoint before publish

### Implementation Phases

1. **Phase 1: Foundation**
   - adopt React Flow
   - create node/edge model adapters
   - add builder layout metadata support
   - keep current form editor as fallback
2. **Phase 2: Activity-first canvas**
   - drag/drop palette
   - start/activity/end nodes
   - side-panel properties editor
   - compile graph to current linear definition model
3. **Phase 3: Rich control flow**
   - condition nodes
   - delay nodes
   - signal nodes
   - parallel split/join support once runtime semantics are defined
4. **Phase 4: Advanced usability**
   - auto-layout
   - copy/paste and keyboard shortcuts
   - inline validation badges
   - node templates/snippets
   - undo/redo
5. **Phase 5: Advanced mode**
   - import/export
   - JSON inspector for expert users
   - migration tools for older definitions

### Recommendation

Proceed with **React Flow** for the workflow designer and treat it as the default authoring experience, while keeping a narrow advanced JSON inspector only as an escape hatch.

## UI Route Reorganization Plan

Reorganize the UI around **task-focused pages** instead of a single broad workflow screen.

### UX Goals

1. Keep navigation obvious for first-time users.
2. Separate **authoring**, **operations**, and **instance investigation** into distinct routes.
3. Make deep links stable so users can bookmark a workflow, instance, queue view, or designer state.
4. Reduce context switching inside oversized pages by using route changes for major tasks.

### Recommended Top-Level Navigation

1. **Dashboard**
   - high-level health, queue depth, recent failures, worker status
2. **Workflows**
   - definition list and entry point into authoring
3. **Runs**
   - workflow instance list, filters, statuses, recent activity
4. **Queues**
   - pending/running/paused/failed tasks, dead letters, lease issues
5. **Operations**
   - event feed, worker health, recovery actions, audit timeline
6. **Settings**
   - environment and product settings

### Recommended Workflow Route Map

Use nested routes so workflow UX scales without turning into a single monolithic page:

```text
/dashboard

/workflows
/workflows/new
/workflows/:definitionId
/workflows/:definitionId/designer
/workflows/:definitionId/versions
/workflows/:definitionId/versions/:version
/workflows/:definitionId/versions/:version/designer

/runs
/runs/:workflowId
/runs/:workflowId/history
/runs/:workflowId/tasks
/runs/:workflowId/signals

/queues
/queues/tasks
/queues/timers
/queues/dead-letters

/operations
/operations/feed
/operations/workers
/operations/recovery
```

### Page Responsibilities

1. **Workflow list page**
   - search/filter definitions
   - show status, active version, draft version, last updated
   - actions: create, open designer, publish draft, start workflow
2. **Workflow details page**
   - summary, versions, linked recent runs, validation state
3. **Workflow designer page**
   - full-page authoring experience
   - compact metadata/palette/properties tabs
   - validation sidebar and publish/save actions
4. **Runs page**
   - dedicated list for active and historical workflow instances
5. **Run details page**
   - current state, event history, signals, related tasks, failure details
6. **Queues pages**
   - operational controls separated from design concerns
7. **Operations pages**
   - system-wide audit feed, worker health, lease recovery, throughput visibility

### Navigation and Layout Rules

1. Use the left nav for product areas, not individual workflow actions.
2. Use tabs inside a page only for **closely related sub-surfaces**.
3. Use route transitions for major task changes:
   - list -> details
   - details -> designer
   - definition -> run investigation
4. Keep full-page surfaces for:
   - designer
   - run investigation
   - queue management
5. Preserve context with breadcrumbs like:
   - `Workflows / Order Fulfillment / Designer`
   - `Runs / wf_123 / History`

### Incremental UI Reorganization Sequence

1. Split current workflow UX into:
   - definition list page
   - designer page
   - operations page
2. Add a dedicated **Runs** area for instance-first investigation.
3. Move queue views into their own **Queues** routes.
4. Add workflow details/version pages between list and designer.
5. Add breadcrumbs, saved filters, and route-level search state.

## WebSocket Live Update Plan

Replace most polling-driven UI refreshes with a **single authenticated WebSocket connection** for workflow-related live updates.

### Goals

1. Reduce repeated polling across definitions, runs, tasks, and event feeds.
2. Improve perceived responsiveness for async operations.
3. Push state changes to the UI as soon as the engine commits them.
4. Keep React Query caches in sync without page-wide reload behavior.

### Recommended Connection Model

Use one browser WebSocket connection per session:

```text
GET /api/ws
```

The socket should carry:

1. server lifecycle events
2. workflow instance updates
3. task queue updates
4. workflow definition changes
5. operation/audit events
6. async command acknowledgements

### Event Envelope

Standardize all socket messages with one envelope:

```json
{
  "type": "workflow.updated",
  "entity": "workflow",
  "entityId": "wf_123",
  "timestamp": "2026-05-22T18:00:00Z",
  "version": 42,
  "payload": {}
}
```

### Recommended Event Types

1. `workflow.started`
2. `workflow.updated`
3. `workflow.completed`
4. `workflow.failed`
5. `workflow.canceled`
6. `workflow.signal-received`
7. `task.created`
8. `task.updated`
9. `task.completed`
10. `task.failed`
11. `definition.updated`
12. `definition.published`
13. `operation.event`
14. `worker.updated`
15. `queue.snapshot`
16. `command.accepted`
17. `command.failed`

### UI Consumption Model

Use REST for:

1. initial page load
2. explicit fetches
3. durable mutations

Use WebSocket for:

1. cache invalidation triggers
2. incremental entity updates
3. live event feeds
4. mutation progress/acknowledgement updates

### React Integration Strategy

1. Create a single app-level live bus provider near the app root.
2. Maintain connection lifecycle, reconnect logic, and heartbeat handling there.
3. Update React Query caches by event type:
   - `setQueryData` for precise entity updates
   - `invalidateQueries` for broader changes where needed
4. Expose small hooks such as:
   - `useLiveBus()`
   - `useWorkflowSubscription(workflowId)`
   - `useQueueSubscription()`

### Subscription Model

Do not broadcast everything to every page forever.

Support scoped subscriptions after socket connect:

1. subscribe to all workflow operations
2. subscribe to a single workflow instance
3. subscribe to queue/task updates
4. subscribe to definition updates

Example client message:

```json
{
  "action": "subscribe",
  "topics": [
    "workflow:wf_123",
    "queue:tasks",
    "definitions"
  ]
}
```

### Backend Delivery Plan

1. Add an in-process event hub inside the workflow service.
2. Publish hub events whenever durable workflow events/tasks/definitions change.
3. Attach a WebSocket handler in the API layer.
4. Support topic-based filtering per connection.
5. Add ping/pong heartbeats and reconnect-safe sequencing.

### Reliability Rules

1. WebSocket events are **for UX freshness**, not the source of truth.
2. Durable REST/DB state remains authoritative.
3. Each pushed event should include enough metadata for idempotent client handling.
4. On reconnect, the client should refetch key route data before trusting only live updates.

### Migration Strategy from Polling

1. Keep existing polling as a fallback initially.
2. Introduce WebSocket updates for:
   - operations feed
   - run details
   - queue views
3. Then reduce polling intervals or disable polling on routes with active live subscriptions.
4. Retain a low-frequency safety refresh for drift detection.

### Suggested Backend Additions

1. `GET /api/ws`
2. internal workflow event hub / broadcaster
3. typed outbound event envelope
4. topic subscription protocol
5. connection metrics and debug visibility

## API Plan

Expose control-plane endpoints under `/api/workflows`.

### Initial Endpoints

- `POST /api/workflows/{name}`: start workflow
- `GET /api/workflows/{id}`: get current state
- `GET /api/workflows/{id}/history`: get event history
- `POST /api/workflows/{id}/signals/{signal}`: send signal
- `POST /api/workflows/{id}/cancel`: request cancellation
- `GET /api/workflows`: list/filter instances
- `GET /api/workflows/tasks`: operator view for pending/running tasks

### Definition and Queue Endpoints

- `GET /api/workflow-definitions`: list definitions
- `POST /api/workflow-definitions`: create draft definition
- `GET /api/workflow-definitions/{id}`: fetch definition metadata
- `POST /api/workflow-definitions/{id}/versions`: create definition version
- `POST /api/workflow-definitions/{id}/publish`: publish a version
- `POST /api/workflows/tasks/{taskId}/retry`: retry a task
- `POST /api/workflows/tasks/{taskId}/requeue`: requeue a task
- `POST /api/workflows/tasks/{taskId}/pause`: pause task or queue segment
- `POST /api/workflows/tasks/{taskId}/resume`: resume task or queue segment
- `GET /api/workflows/queues`: queue overview
- `GET /api/workflows/dead-letters`: dead-letter queue view

### API Response Expectations

- stable instance IDs
- current workflow status
- attempt counters
- timestamps for start, update, next run, last failure
- failure details with structured codes/messages
- definition version identifiers for both definitions and running instances
- operator-safe queue action results with explicit status transitions

## Observability

Add first-class observability from the beginning.

### Logs

Structured logs should include:

- workflow_id
- workflow_name
- activity_name
- task_id
- event_type
- lease_owner
- attempt
- status
- duration

### Metrics

Track:

- workflow starts/completions/failures/cancellations
- event append throughput and append failures
- snapshot rebuild latency
- activity successes/failures/retries/timeouts
- runnable queue depth
- oldest pending task age
- timer scan lag
- lease recovery count
- heartbeat timeout count for long-running activities
- definition publish count and validation failures
- operator queue action counts such as retry, requeue, pause, resume

## Testing Strategy

### Unit Tests

- workflow state transition determinism
- event replay correctness
- snapshot rebuild from event stream
- retry policy calculations
- timer scheduling
- lease expiry behavior
- optimistic concurrency conflicts

### Integration Tests

- start -> run -> complete happy path
- replay full workflow from events only
- crash between activity completion and workflow resume
- process restart with in-flight timers
- worker death and lease reclamation
- duplicate activity completion event handling
- signal delivery while workflow is blocked on timer/activity
- definition publish -> start instance pinned to published version
- queue action from UI/API changes task state correctly

### Failure Injection

Add targeted tests that simulate:

- transaction rollback
- worker panic
- activity timeout
- database unavailable during commit
- duplicate task dispatch
- worker crash after external side effect but before final commit
- corrupted or stale snapshot rebuilt from canonical events

## Security and Safety Boundaries

- Validate workflow names, versions, and payload schemas at the API boundary.
- Keep activity execution behind a registry; do not execute arbitrary function names from requests.
- Store payloads as structured JSON with size limits.
- Redact sensitive fields from logs and history when needed.
- Restrict destructive queue actions such as cancel, purge, and dead-letter replay behind explicit authorization.
- Validate UI-authored definitions against an allowlist of supported step types and registered activities.
- Keep script execution behind an explicit feature flag and sandbox policy.
- Never allow script activities to reach filesystem, shell, or unrestricted network capabilities.
- Treat new builtins as security-sensitive API surface and review each one before exposing it to workflow authors.

## Incremental Delivery Plan

### Phase 1: Foundations

- Create workflow package skeleton and interfaces
- Define command, event, status, and retry types
- Define store interfaces and transaction boundaries
- Define event envelope, sequence model, and snapshot contract
- Define workflow definition document model for UI authoring
- Choose initial DB backend for development

**Exit criteria:** code compiles, interfaces are stable enough to build a vertical slice.

### Phase 2: Minimal Durable Vertical Slice

- Implement one SQL-backed store
- Add workflow instance/event/definition/task tables
- Make event append the primary write path
- Support `StartWorkflow`, `ScheduleActivity`, `ActivityCompleted`, `CompleteWorkflow`
- Implement a single worker loop
- Create one sample workflow end to end

**Exit criteria:** a workflow survives restart and completes after resuming.

### Phase 3: Timers and Retries

- Add durable timers
- Add retry policies with exponential backoff
- Add failure event handling
- Add activity timeout support

**Exit criteria:** delayed and retried tasks continue correctly after restart.

### Phase 4: Recovery and Multi-Worker Safety

- Add leases to workflow/activity tasks
- Requeue expired work
- Add optimistic concurrency protection
- Test duplicate delivery and competing workers
- Add worker claim strategy appropriate to the chosen database

For Postgres, task claiming can use patterns such as `FOR UPDATE SKIP LOCKED`. For SQLite, keep the claim logic simpler and explicit, even if concurrency is lower in the first cut.

**Exit criteria:** multiple workers can safely process tasks without losing or corrupting workflow state.

### Phase 5: Control Plane and Visibility

- Add REST endpoints
- Add instance/history listing
- Add operator-focused task inspection
- Add structured engine metrics/logging
- Add UI pages for definitions, instances, and queues

**Exit criteria:** workflows are operable without database spelunking.

### Phase 6: Hardening

- Add schema/version migration strategy
- Add payload versioning
- Add event upcasting/version-compatibility strategy
- Add dead-letter handling
- Add cancellation semantics
- Add load/perf profiling
- Add heartbeats for long-running activities
- Add role-aware controls for queue operations and definition publishing

**Exit criteria:** engine is reliable enough for production pilots.

## Suggested First Workflow

Implement a small but realistic workflow first, such as:

- document ingestion pipeline
- approval workflow
- order processing
- deployment request flow

Pick one that includes:

- at least one activity
- at least one timer or retry
- at least one failure path

This keeps the engine honest and prevents over-design.

## Main Risks

1. **Non-deterministic workflow logic** causing inconsistent replay or duplicate commands.
2. **Weak idempotency boundaries** causing duplicate side effects during retries/recovery.
3. **Over-coupling runtime and storage** making later scaling difficult.
4. **Trying to build too much generic flexibility too early.**
5. **Letting workflow code depend on wall clock, randomness, or live external responses without persisting them first.**
6. **Allowing snapshots/projections to drift from the canonical event log without repair tooling.**
7. **Allowing the UI definition model to outgrow the execution model and create unsupported workflow shapes.**

## Decisions to Make Early

1. Initial durable backend: SQLite first or Postgres first
2. Payload encoding: JSON only or pluggable codec
3. Event retention: full retention only, or full retention plus compaction/snapshot strategy
4. Worker topology: in-process only first, or separate worker binary soon after
5. Cancellation model: cooperative only or hard timeouts for activities
6. Event versioning and upcasting strategy
7. Definition authoring model: graph DSL, form-driven model, raw JSON, or hybrid

## Recommended Next Implementation Order

1. Define engine interfaces and event/command model.
2. Design the event envelope, sequence rules, and snapshot contract.
3. Design the workflow definition document and publish/version model.
4. Design SQL schema and transaction rules.
5. Build one sample workflow with one activity.
6. Add restart recovery.
7. Add timers and retries.
8. Add APIs and UI for operational visibility and queue management.

## Practical Design Constraints

Keep the first version intentionally boring:

- persist constantly
- prefer small workflow transitions
- treat events as canonical and snapshots as rebuildable
- treat crashes as routine recovery scenarios
- make activity idempotency explicit in the API and code review checklist
- avoid embedding smart implicit behavior in the worker loop

## Notes for This Repository

- The current server already exposes JSON APIs through `chi`, so the workflow control plane should fit naturally under `internal/api`.
- Configuration should be extended with a workflow section for storage backend, worker concurrency, lease durations, scan intervals, and retention settings.
- If background workers initially run in the same process as the HTTP server, keep the interfaces clean so they can move into a dedicated worker command later.
- The existing React UI shell is a good fit for a left-nav operator console with sections for Definitions, Instances, Queues, Dead Letters, and Operations.
