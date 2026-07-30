# Workflow Engine

## Domain model

The workflow domain has three layers:

1. A **definition** is the stable identity, name, description, status, and active
   version pointer.
2. A **definition version** is an immutable execution document once published. Draft
   versions can be updated only through explicit draft/version operations.
3. A **workflow instance** is a run pinned to one definition version with durable
   context, current position, output, status, trigger attribution, and event sequence.

Execution work is represented by **tasks**. Inputs arriving asynchronously are stored
as **signals**. Every material run transition is appended to **workflow events**.

## Definition document

A definition document contains:

- `name` and `description`;
- optional input and output JSON Schema IDs;
- optional end-output mapping;
- ordered steps.

Each step contains:

- unique `name`;
- registered `activity` name;
- activity-specific JSON `input`;
- retry policy (`maxAttempts`, `backoffSeconds`);
- visual layout (`x`, `y`);
- transitions.

Normalization validates names, activity existence, retries, schemas, transition targets,
and condition operators before persistence. Layout is part of the stored document but
does not affect execution semantics.

## Version lifecycle

The first definition version is created as published and active version 1. Subsequent
semantic edits create a monotonically numbered draft linked by `based_on_version`.

Version states are:

- `draft`: editable candidate, not eligible for normal execution;
- `published`: immutable and eligible for execution;
- `active`: represented by the definition's `active_version` pointer, not a separate
  version status.

Publishing may optionally activate the version. Activation requires an already
published version. Starting without an explicit version uses the active version.
Starting a pinned version requires that version to be published and, for external API
keys, an explicit `allowPinnedVersions` grant.

The layout-only update endpoint updates a draft document only when semantic fields are
unchanged. The UI classifies movement separately, but the service remains responsible
for rejecting semantic changes sent to the layout path.

## Starting a run

Run creation validates the selected definition version and optional start schema, then
commits in one transaction:

- a `workflow_instances` row with status `running`;
- normalized initial context containing the supplied input;
- initial workflow and activity-scheduled events;
- the first `workflow_tasks` row.

The instance records trigger source, principal type, and principal ID. Browser starts
are attributed to the authenticated user; webhook starts are attributed to the API key
or anonymous migration-mode principal.

## Task state machine

```text
                         +-----------------------+
                         |                       v
pending --claim------> running --success----> completed
   ^                     |  |
   |                     |  +--wait signal--> waiting --signal/timeout--+
   |                     +-----delay--------> pending (future run_at)    |
   |                     +-----pause--------> paused --resume------------+
   |                     +-----retry--------> pending (backoff)
   |                     +-----cancel-------> canceled
   +----lease expiry-----+
                         +-----final error---> failed
```

Workflow instance states use the same status vocabulary where applicable. A waiting
task does not change the instance to a special waiting state; the instance remains
running with `pendingSignals` and `nextRunAt` describing progress.

## Claim and lease protocol

A worker transaction selects the earliest runnable pending task, then conditionally
updates it from `pending` to `running`. The update increments attempts and writes:

- `lease_owner` as the registered node ID;
- `lease_expires_at` as now plus `workflow.leaseDuration`;
- `executed_by` as the node ID.

If another worker changed the row first, zero rows are updated and the claim is lost
cleanly. Before each worker pass, expired running leases are returned to pending.

The lease provides crash recovery, not exactly-once execution. A slow activity can
continue after its lease expires because leases are not renewed during execution. A
second worker may then repeat the task. Side-effecting activities and downstream APIs
must use idempotency keys or equivalent deduplication where correctness requires it.

## Activity execution result

An activity returns one of four effective outcomes:

- output and optional context updates: complete and advance;
- `DelayUntil`: persist state and reschedule the same task;
- `WaitForSignal`: park the same task in waiting state;
- error: retry according to policy or fail the workflow.

Delay and signal-wait outcomes decrement the attempt count because parking is not
treated as a failed execution attempt. Activity state is persisted in `state_json` and
passed back on resume.

## Context model

The workflow context is JSON stored on the instance. It begins with run input and grows
as steps complete. Activity output is attached under the step's name, and explicit
`ContextUpdates` can modify additional keys.

Templates and transition conditions read from this context. Context updates and task
completion are committed in one transaction, so the next step never observes a partial
result. Context is a durable snapshot rather than an event replay product.

## Transition semantics

After a step completes:

- omitted legacy transitions advance to the next ordered step;
- an explicit empty transition list terminates the workflow;
- non-empty transitions are evaluated in order;
- the first matching condition wins;
- an unconditional transition acts as a default;
- `__end__` is the terminal target.

Conditions select a JSON path, operator, and optional comparison value. Invalid targets,
ambiguous defaults, and invalid conditions are rejected during definition validation.
When no valid transition matches, execution fails rather than silently guessing.

## Completion and output

On terminal completion, optional `endOutput` mapping resolves a final public output from
the accumulated context. The engine stores final output, clears the current step, and
appends `WorkflowCompleted` in the same transaction.

If a callback URL was supplied, callback delivery begins asynchronously after a
successful completion commit. The callback reports completed status and output.
Callback status is persisted, but the callback is not part of the workflow completion
transaction and has no distributed outbox guarantee. Failed and canceled runs do not
currently trigger callbacks.

## Retry and failure

Every claim increments `attempts`. When an activity fails before `maxAttempts`, the task
returns to pending with `run_at = now + backoffSeconds`, the last error is stored, and an
`ActivityRetryScheduled` event is appended. Exhaustion marks both task and workflow
failed and appends terminal failure events.

Operator retry resets attempts and state. Requeue preserves the broader task identity
but clears its lease. Pause and resume operate on eligible task states. Canceling a task
cancels its workflow; canceling a workflow cancels unfinished tasks transactionally.

## Signals

A signal has a workflow ID, name, JSON payload, status, creation time, and optional
processing time. Signal delivery:

1. Stores the signal durably.
2. Finds waiting tasks whose persisted wait state names that signal.
3. Makes matching tasks runnable and records signal metadata.
4. Wakes the local worker loop.

Signals may arrive before or after a task begins waiting. Activities receive a signal
snapshot containing count, last payload, and received time. Timeout-capable waits store
a future `run_at`; non-timeout waits are parked until a signal changes their state. The
current claim transition does not promote an expired waiting task to running, so the
persisted timeout is not yet a complete timeout-wakeup implementation.

## Durable events and live events

Workflow events are ordered per instance by `sequence` and are the audit trail for
domain execution. Examples include scheduling, completion, retries, transitions,
signals, task controls, and terminal outcomes.

Live events are separate process-local notifications used for UI freshness. Consumers
must fetch durable state after receiving a live event and must tolerate missed or
duplicate live events.

## Core invariants

- A run is permanently pinned to one definition version.
- Published version documents are not modified by layout or semantic saves.
- At most one task claim transaction changes a pending row to running at a time.
- Durable event sequence increases with each committed workflow transition.
- Scheduling the next task and updating instance position happen in one transaction.
- Recovery may repeat external effects, so correctness cannot depend on exactly-once
  activity execution.
