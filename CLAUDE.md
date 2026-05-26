# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Install all dependencies (Go + UI)
make install-deps

# Full production build (UI then Go binary)
make build

# Development — backend only (proxies UI to Vite at localhost:5173)
make dev

# Development — frontend only
make dev-ui

# Development — backend + frontend together
make dev-all

# Run Go tests
make test

# Run a single Go test
go test ./internal/workflow/... -run TestServiceName

# Lint backend
make lint          # go vet

# Lint frontend
make lint-ui       # eslint

# Format Go code
make fmt

# Initialise a project directory (writes config.toml, .env, data/)
make init
```

> **Important**: `ui/dist` must be populated before Go can compile (the binary embeds it via `//go:embed`). Run `make build-ui` before `make build-go` or `go test` on a fresh clone.

## Architecture

Orchestra is a **single-binary durable workflow engine** — a Go HTTP server with a React control plane embedded directly into the binary at compile time.

### Request flow

```
HTTP request
  └─ chi router (server.go) /livez heartbeat
       ├─ /api/*  → api.NewRouter   → api.Handler      (workflow CRUD, WebSocket live-bus)
       ├─ /ext/*  → api.NewExtRouter → WebhookHandler  (external / unauthenticated webhook API)
       └─ /*      → SPA handler (embedded ui/dist) or Vite dev proxy (--dev flag)
```

### Backend packages

| Package | Role |
|---|---|
| `cmd/app` | Cobra CLI (`serve`, `init`, `version`). Wires config → services → HTTP server. |
| `internal/config` | Viper-based config. Reads `config.toml` (or `config.yaml`), then env vars prefixed `APP_` (dots → underscores). |
| `internal/server` | Mounts `/api` and `/ext` sub-routers; serves the embedded SPA or proxies to Vite. |
| `internal/api` | Chi router + handler for all `/api/*` endpoints. `handler.go` holds all handler methods; `router.go` wires routes; `v1handler.go` is the `/ext/*` webhook router. |
| `internal/webhooks` | `CallbackAllowlist` — regex-based URL allowlist used to validate callback URLs on external workflow starts. |
| `internal/workflow` | Core engine: SQLite-backed state machine, task poller, activity execution, signals, AI prompt enhancement. |
| `internal/livebus` | In-process pub/sub (`Bus`). Backend publishes events; `/api/ws` streams them to browsers via WebSocket. |
| `internal/logging` | `slog`-based structured logger. |
| `internal/version` | Version info injected at link time via `-ldflags`. |

### API surface

**Internal API** (`/api/*`) — used by the React UI:

| Method | Path | Description |
|---|---|---|
| GET | `/api/health` | Health check |
| GET | `/api/meta` | App name, environment, version |
| GET | `/api/config/raw` | Read active config file content |
| PUT | `/api/config/raw` | Write config file to disk |
| POST | `/api/admin/restart` | Graceful shutdown + `syscall.Exec` self-restart |
| GET | `/api/ws` | WebSocket live-event stream |
| GET/POST | `/api/scripts[/{id}]` | Script CRUD |
| GET/POST | `/api/agents[/{id}]` | Agent CRUD |
| GET/PUT | `/api/agents/{id}/mcp-servers` | Agent ↔ MCP server wiring |
| GET/POST | `/api/mcp-servers[/{id}]` | MCP server CRUD |
| POST | `/api/mcp-servers/{id}/explore` | Discover MCP server tools |
| POST | `/api/ai/enhance-prompt` | Rewrite a prompt draft via GPT-4o |
| GET | `/api/workflows/activities` | List available activity types |
| GET/POST | `/api/workflow-definitions[/{id}]` | Workflow definition CRUD |
| POST | `/api/workflow-definitions/{id}/versions` | Create new definition version |
| POST | `/api/workflow-definitions/{id}/versions/{v}/publish` | Publish a version |
| POST | `/api/workflow-definitions/{id}/start` | Start a workflow run (accepts `{ input, callbackUrl }`) |
| GET | `/api/workflows[/{id}]` | List / get workflow instances |
| GET | `/api/workflows/{id}/history` | Event history for a run |
| POST | `/api/workflows/{id}/cancel` | Cancel a run |
| POST | `/api/workflows/{id}/signals` | Send a signal to a waiting run |
| GET | `/api/workflows/events` | Global operation log |
| GET | `/api/workflows/tasks` | List tasks (filterable by status) |
| POST | `/api/workflows/tasks/{id}/{action}` | Apply task action: retry / requeue / pause / resume / cancel |

**External webhook API** (`/ext/*`) — no authentication, for external callers:

| Method | Path | Description |
|---|---|---|
| POST | `/ext/webhook/{definitionId}/start` | Trigger a new workflow run; optional JSON body as input; optional `X-Callback-URL` header |
| POST | `/ext/webhook/{workflowId}/signal` | Send a named signal to a running workflow |
| GET | `/ext/signal/{workflowId}` | Poll whether a signal is required (`currentActivity`, `pendingSignals`) |
| GET | `/ext/result/{workflowId}` | Poll for the final result; returns 202 while still running |

### Workflow engine internals

A **workflow definition** is a JSON document (`DefinitionDocument`) with named steps. Each step names an `activity` and optional `transitions` with conditions for branching.

At runtime, a **workflow instance** moves through steps by executing **tasks** (`WorkflowTask`). The engine polls SQLite for `pending` tasks, acquires a lease (`lease_owner`, `lease_expires_at`), runs the activity, and writes the result back. Task status lifecycle: `pending → running → completed/failed/waiting/paused`.

**Signals** (`WorkflowSignal`) are delivered externally via `POST /workflows/{id}/signals` or `POST /ext/webhook/{id}/signal`. Tasks in `waiting` status (produced by `wait_signal` activity) are woken when a matching signal arrives.

**Callbacks**: when a workflow is started via `/ext/webhook/{id}/start` with an `X-Callback-URL` header, the engine delivers the final result to that URL via HTTP POST after the run completes (3 retries: 0/5/15/45 s). The URL must match the `webhook.callbackAllowlist` regex patterns in config.

**Initial context**: `StartWorkflowWithInput` accepts an `input map[string]any` stored as `context.input.*` so workflow steps can reference it as `{{input.fieldName}}`.

**Activities** implement the `Activity` interface (`Descriptor() + Execute()`). Built-ins live in `activities.go` and `catalog_activities.go`. The `script` activity runs sandboxed Starlark (`go.starlark.net`); it is disabled by default (`workflow.scriptEnabled: false`).

**AI enhancement**: `Service.EnhancePrompt(ctx, draft)` in `ai_enhance.go` calls GPT-4o with a meta system prompt to rewrite an agent system prompt draft. Requires `workflow.openaiAPIKey`.

### Server restart

`POST /api/admin/restart` sends on a buffered `chan struct{}` threaded from `serve.go` → `server.Options` → the API handler. The main select in `runServe` picks it up, shuts the HTTP server down gracefully, then calls `execSelf()` which uses `syscall.Exec` to replace the process with a fresh copy of itself (same args and environment).

### Frontend

React SPA in `ui/src/`, built with Vite + TypeScript + Tailwind + `@xyflow/react`.

- **Routing**: React Router v6, defined in `ui/src/App.tsx`
- **Shared utilities**: `ui/src/pages/workflowUi.tsx` exports `formatDate`, `statusClasses`, `availableTaskActions`, `EventCard`, and filter helpers used across multiple pages
- **Data fetching**: TanStack Query (`@tanstack/react-query`) for REST calls; a shared WebSocket connection to `/api/ws` provides live updates
- **Visual designer**: `WorkflowDesignerPage.tsx` uses `@xyflow/react` to render and edit step graphs
- The production build output lands in `ui/dist/` and is embedded into the Go binary via `ui_embed.go`

Key pages:

| Page | Route | Notes |
|---|---|---|
| `DashboardPage` | `/dashboard` | Live metrics: active runs, failed tasks, recent runs table, definitions grid |
| `WorkflowListPage` | `/workflows` | Definition list; Start run opens `StartWorkflowModal` (input + callback URL); shows webhook trigger URL |
| `WorkflowDesignerPage` | `/workflows/:id/designer` | ReactFlow canvas editor; compiles to `DefinitionDocument` |
| `RunDetailsPage` | `/runs/:id` | Run details; shows trigger source badge and callback delivery status |
| `AgentEditorPage` | `/agents/:id/editor` | Monaco editor for system prompt; "Enhance with AI" button calls `/api/ai/enhance-prompt` |
| `SettingsPage` | `/settings` | Monaco editor for `config.toml`; Save writes to disk; Restart button triggers graceful restart |

### Configuration

Config is resolved in order: defaults (`config.Default()`) → `config.toml` (or `config.yaml`) → `.env` / `.env.local` → `APP_*` environment variables. The `--config` flag overrides the config file path. Copy `example.config.toml` → `config.toml` to get started.

Key config knobs:

```toml
[ui]
devProxyURL = "http://localhost:5173"   # used when --dev flag is passed

[workflow]
databasePath  = "data/workflows.db"
scriptEnabled = false                   # enable to use the starlark script activity
openaiAPIKey  = ""                      # or set APP_WORKFLOW_OPENAI_API_KEY

[webhook]
enabled = true
# callbackAllowlist = ["^https://hooks\\.example\\.com/", "^https://.*\\.internal/"]
```

`Config.ConfigFilePath` is a runtime-only field (tagged `mapstructure:"-"`) populated in `serve.go` from `viper.ConfigFileUsed()` after loading. It is the path used by `GET /api/config/raw` and `PUT /api/config/raw`.
