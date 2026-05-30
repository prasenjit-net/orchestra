# Safe Entity Deletion Plan — Workflows, Agents, Scripts, Connectors

## Plain-English summary

Only **four kinds of things** can be deleted: **workflow definitions, agents, scripts, and connectors (MCP servers)**. Everything else — workflow instances (runs), tasks, events, signals — is **transactional history** and stays in the database forever.

Two rules:

1. **No delete while in use.** Each entity is "in use" if some *other entity* still points at it. If it is, the API responds `409 Conflict` with the exact list of things that block the delete so the user knows what to fix.

   | Entity to delete | "In use" means… |
       |---|---|
   | Workflow Definition | a workflow run is currently active (pending/running/waiting/paused) |
   | Agent | at least one workflow definition version references its `agentId` |
   | Script | at least one workflow definition version references its `scriptId` |
   | Connector (MCP Server) | at least one agent is linked to it via `agent_mcp_servers` |

   Past, completed runs do **not** count as "in use" — they are history.

2. **Transactional data never breaks the UI.** When an entity is deleted, old runs/tasks/events that still reference its ID are kept untouched. The UI gracefully shows a `(deleted)` badge in place of the missing name, and the run-detail page falls back to the snapshot embedded in the instance so history still renders.

What changes:
- The existing `Delete{Script,Agent,MCPServer}` endpoints get an in-use pre-check.
- A new `DELETE /api/workflow-definitions/{id}` endpoint is added (with the active-instance check).
- **No** workflow-instance delete endpoint is added.
- UI lists/detail pages add defensive rendering for dangling references and surface the 409 reason in toasts.

---

## Context

Today, `DELETE` on Scripts, Agents, and MCP Servers is a blind hard-delete with no usage check, so it silently leaves dangling `scriptId` / `agentId` references inside workflow definition step JSON. Workflow definitions have **no delete endpoint at all**, so unwanted definitions accumulate forever. This plan closes both gaps while keeping the rule that transactional run history is immutable.

It keeps the existing single-`Service` architecture (`internal/workflow/`) and the chi-routed `/api/*` handlers (`internal/api/`). It reuses the existing JSON-step-dependency extractor at `internal/workflow/import_export.go:436` and the same `BeginTx → cascade → Commit` pattern already used by `DeleteMCPServer`.

---

## Reference graph

```
  WorkflowDefinition ──< WorkflowDefinitionVersion (document_json)
        │                        │
        │                        ├─ step.input.scriptId  ──> Script
        │                        └─ step.input.agentId   ──> Agent ──< agent_mcp_servers >── MCPServer
        │
        └─< WorkflowInstance ──< WorkflowTask        ← transactional, never deleted
                              ├─< WorkflowEvent      ← transactional, never deleted
                              └─< WorkflowSignal     ← transactional, never deleted
```

No FK constraints exist — every relationship is logical and must be enforced in Go.

The instance's `snapshot_json` column already contains the resolved step graph at the time the run started, so the run-detail page can still render even when its `WorkflowDefinition` row is later deleted.

---

## Design — per entity

### Shared: `EntityInUseError`
Add to `internal/workflow/errors.go` (next to `ErrNotFound`):

```go
type EntityRef struct {
    Kind  string `json:"kind"`  // "workflow_definition", "workflow_definition_version", "agent", "workflow_instance"
    ID    string `json:"id"`
    Label string `json:"label,omitempty"` // human-friendly (name + version)
}

type EntityInUseError struct {
    Kind       string      `json:"kind"`
    ID         string      `json:"id"`
    References []EntityRef `json:"references"`
}
```
Handlers translate this to **HTTP 409 Conflict** with a JSON body the UI can render in a toast/dialog:
```json
{ "error": "in_use", "kind": "script", "id": "scr_x", "references": [
  { "kind": "workflow_definition_version", "id": "def_y@3", "label": "Customer Onboarding v3" }
]}
```

### Shared usage lookup helpers
Add `internal/workflow/usage.go` (new):

- `findDefinitionsReferencingScript(ctx, scriptID)` — iterates `workflow_definition_versions`, decodes `document_json`, runs `extractStepDependencies` (`import_export.go:436`), returns matches as `[]EntityRef`.
- `findDefinitionsReferencingAgent(ctx, agentID)` — same shape.
- `findAgentsUsingMCPServer(ctx, serverID)` — single `SELECT agent_id FROM agent_mcp_servers WHERE server_id = ?`.
- `findActiveInstancesForDefinition(ctx, defID)` — `SELECT id FROM workflow_instances WHERE definition_id = ? AND status IN ('pending','running','waiting','paused')`.

Volume in practice is bounded (dozens–hundreds of definitions); no new index needed. If volume grows, we can later add a denormalized `workflow_definition_dependencies` table without changing the API.

### 1. Script — `Service.DeleteScript` (`internal/workflow/scripts.go:147`)
- Open tx.
- Run `findDefinitionsReferencingScript`. If non-empty → return `EntityInUseError`.
- `DELETE FROM scripts WHERE id = ?`. Commit.
- Emit `script.deleted`.
- Old runs whose `snapshot_json` references this `scriptId` are untouched.

### 2. Agent — `Service.DeleteAgent` (`internal/workflow/agents.go:138`)
- Open tx.
- Run `findDefinitionsReferencingAgent`. If non-empty → `EntityInUseError`.
- `DELETE FROM agent_mcp_servers WHERE agent_id = ?` (the agent owns these links — internal cleanup, not a user-visible cascade).
- `DELETE FROM agents WHERE id = ?`. Commit.
- Emit `agent.deleted`.

### 3. MCP Server (Connector) — `Service.DeleteMCPServer` (`internal/workflow/mcp_servers.go:161`)
**Tighten** current behavior: today it silently cascades `agent_mcp_servers`, which can quietly break an agent's tool list.

- Open tx.
- Run `findAgentsUsingMCPServer`. If any agents reference it → `EntityInUseError` listing those agents. The user must un-link via `PUT /api/agents/{id}/mcp-servers` first.
- After the check `agent_mcp_servers` is empty for this server, so `DELETE FROM mcp_servers WHERE id = ?` is sufficient. Commit.
- Emit `mcp_server.deleted`.

### 4. Workflow Definition — **new** `Service.DeleteWorkflowDefinition`
New file `internal/workflow/definitions_delete.go` and a new route `DELETE /api/workflow-definitions/{definitionID}` in `internal/api/router.go` (around line 138).

- Open tx (on Postgres use `SELECT ... FOR UPDATE` on the `workflow_definitions` row to serialize with `StartWorkflow`).
- Run `findActiveInstancesForDefinition`. If any active (non-terminal) instances exist → `EntityInUseError` listing their IDs.
- `DELETE FROM workflow_definition_versions WHERE definition_id = ?`.
- `DELETE FROM workflow_definitions WHERE id = ?`.
- Commit. Emit `workflow_definition.deleted`.
- **Past instances, tasks, events, signals are intentionally untouched.** They keep pointing to the now-missing `definition_id`; the UI handles the dangling reference (next section).

### 5. Workflow Instance — **explicitly not deletable**
No `DELETE /api/workflows/{id}` endpoint. Runs, tasks, events, and signals are transactional data and live forever. Cancellation (`POST /api/workflows/{id}/cancel`) remains the only way to stop a run.

### Task poller safety
All in-use checks and deletes run inside the same tx, so a concurrent task lease (`UPDATE workflow_tasks SET lease_owner = ...`) and a concurrent `StartWorkflow` (which inserts a new active instance) either block (Postgres row lock) or serialize (SQLite single-writer). No extra synchronization needed.

---

## UI changes — defensive rendering for dangling refs

Goal: after an entity is deleted, every page that previously linked to it must still render and must clearly mark the missing reference.

Touch points (`ui/src/pages/`):

| Page | Behavior when ref target is missing |
|---|---|
| `RunDetailsPage` | If `definitionId` 404s, show `Definition: (deleted) <id>` in the header but still render the run from `snapshot_json` + event history. Per-step agent/script labels fall back to `Agent (deleted): agt_xxx` / `Script (deleted): scr_xxx`. |
| `WorkflowListPage` (runs list) | "Definition" column shows `(deleted)` badge in muted styling when the definition is missing; link to definition becomes inert. |
| `DashboardPage` (recent runs) | Same `(deleted)` badge convention. |
| `AgentEditorPage` | Connector chips for deleted MCP servers don't appear at all (the `agent_mcp_servers` join row was removed when the agent itself was deleted — for the reverse case where a connector somehow disappears with a stale join row, fall back to `Connector (deleted): mcp_xxx`). |
| `WorkflowDesignerPage` | If a step references a deleted agent/script, the node shows a yellow warning badge "Reference missing — pick a replacement" so the user fixes the next version before publishing. The activity dropdown lists existing entities; the missing one is shown as a non-selectable placeholder so the user can see what was there. |

Add a tiny helper in `ui/src/pages/workflowUi.tsx` (it already hosts `formatDate`, `statusClasses`, `EventCard`):

```tsx
export const MissingRefBadge = ({ kind, id }: { kind: string; id: string }) =>
  <span className="…muted-pill"> {kind} (deleted): {id}</span>;
```

Toast handling for 409:
- Wherever a Delete action exists (Scripts, Agents, Connectors, Definitions lists), parse the 409 JSON and render a dialog with the `references` list. Reuse the existing toast pattern.

---

## Handler / API surface changes

`internal/api/handler.go`:
- Wrap returned errors from `DeleteScript`, `DeleteAgent`, `DeleteMCPServer` so `EntityInUseError` → 409 with the JSON body shown above. Other errors keep their current mapping.
- Add `DeleteWorkflowDefinition(w, r, definitionID string)`.

`internal/api/router.go`:
- Add `r.Delete("/", h.DeleteWorkflowDefinition…)` inside the `/workflow-definitions/{definitionID}` block (around line 138).
- No new route under `/workflows/{workflowID}`.

---

## Files to modify

| File | Change |
|---|---|
| `internal/workflow/errors.go` (new or extend) | Add `EntityInUseError`, `EntityRef`. |
| `internal/workflow/usage.go` (new) | Shared "find references" helpers. |
| `internal/workflow/scripts.go` | Add in-use pre-check to `DeleteScript`; wrap in tx. |
| `internal/workflow/agents.go` | Add in-use pre-check + tx + `agent_mcp_servers` cleanup. |
| `internal/workflow/mcp_servers.go` | Replace silent cascade with strict pre-check. |
| `internal/workflow/definitions_delete.go` (new) | `DeleteWorkflowDefinition`. |
| `internal/api/handler.go` | Handler + 409 mapping. |
| `internal/api/router.go` | New `DELETE` route on workflow-definitions. |
| `ui/src/pages/workflowUi.tsx` | `MissingRefBadge`, dangling-ref helpers. |
| `ui/src/pages/RunDetailsPage.tsx` | Snapshot-based render when definition deleted; missing agent/script badges per step. |
| `ui/src/pages/WorkflowListPage.tsx`, `DashboardPage.tsx` | `(deleted)` badge in run rows. |
| `ui/src/pages/WorkflowDesignerPage.tsx` | Warning badge on steps with missing agent/script refs. |
| `ui/src/pages/{Scripts,Agents,McpServers,WorkflowList}Page.tsx` | Delete buttons + 409 dialog. |

Reused: `extractStepDependencies` (`internal/workflow/import_export.go:436`), `ErrNotFound`, `Service.emitLiveEvent`, `Service.rebind` for SQLite/Postgres portability.

---

## Verification

Backend (`make test`):
- `TestDeleteScript_InUse` — script referenced by a definition version → 409 with that version in `references`.
- `TestDeleteScript_NotInUse` — clean delete; `script.deleted` event fires.
- `TestDeleteAgent_InUse` / `..._NotInUse` (and confirms `agent_mcp_servers` rows are gone).
- `TestDeleteMCPServer_BlockedByAgent` — agent linked → 409; after unlink → succeeds.
- `TestDeleteWorkflowDefinition_BlockedByActiveInstance` — running instance → 409.
- `TestDeleteWorkflowDefinition_AllowedWithTerminalInstances` — completed/failed/canceled instances → succeeds; instance rows survive with dangling `definition_id`.
- `TestDeleteWorkflowDefinition_RemovesVersions` — after delete, `workflow_definition_versions` for that id is empty.
- Run against both SQLite (default) and Postgres (docker-compose) to confirm tx + `rebind` work.

End-to-end (`make dev-all`):
1. Create a workflow definition that uses an agent + a script, then publish.
2. `DELETE /api/scripts/{id}` → 409 with the definition listed. Remove the script step, publish a new version, retry → 200.
3. Start a workflow and immediately try `DELETE /api/workflow-definitions/{id}` → 409. Wait for run to complete, retry → 200. Confirm run page still renders with `(deleted)` badge.
4. `DELETE /api/mcp-servers/{id}` while linked to an agent → 409. Un-link via `PUT /api/agents/{id}/mcp-servers` → retry → 200.
5. `sqlite3 data/workflows.db "SELECT COUNT(*) FROM workflow_events WHERE workflow_id IN (SELECT id FROM workflow_instances WHERE definition_id='<deleted-id>')"` → confirms transactional history is intact.

Live-bus:
- `*.deleted` events appear on `/api/ws` so the UI list pages live-update without refresh.
