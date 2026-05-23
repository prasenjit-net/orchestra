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

# Initialise a project directory (writes config.yaml, .env, data/)
make init
```

> **Important**: `ui/dist` must be populated before Go can compile (the binary embeds it via `//go:embed`). Run `make build-ui` before `make build-go` or `go test` on a fresh clone.

## Architecture

Orchestra is a **single-binary durable workflow engine** — a Go HTTP server with a React control plane embedded directly into the binary at compile time.

### Request flow

```
HTTP request
  └─ chi router (server.go) /livez heartbeat
       ├─ /api/*  → api.NewRouter  → api.Handler  (workflow CRUD, WebSocket live-bus)
       └─ /*      → SPA handler (embedded ui/dist) or Vite dev proxy (--dev flag)
```

### Backend layers

| Package | Role |
|---|---|
| `cmd/app` | Cobra CLI (`serve`, `init`, `version`). Wires config → services → HTTP server. |
| `internal/config` | Viper-based config. Reads `config.yaml`, then env vars prefixed `APP_` (dots → underscores). |
| `internal/server` | Mounts `/api` sub-router and serves the embedded SPA or proxies to Vite. |
| `internal/api` | Chi router + handler. All REST endpoints live here. |
| `internal/workflow` | Core engine: SQLite-backed state machine, task poller, activity execution, signals. |
| `internal/livebus` | In-process pub/sub (`Bus`). Backend publishes events; `/api/ws` streams them to browsers via WebSocket. |
| `internal/logging` | `slog`-based structured logger. |
| `internal/version` | Version info injected at link time via `-ldflags`. |

### Workflow engine internals

A **workflow definition** is a JSON document (`DefinitionDocument`) with named steps. Each step names an `activity` and optional `transitions` with conditions for branching.

At runtime, a **workflow instance** moves through steps by executing **tasks** (`WorkflowTask`). The engine polls SQLite for `pending` tasks, acquires a lease (`lease_owner`, `lease_expires_at`), runs the activity, and writes the result back. Task status lifecycle: `pending → running → completed/failed/waiting/paused`.

**Signals** (`WorkflowSignal`) are delivered externally via `POST /workflows/{id}/signals`. Tasks in `waiting` status (produced by `wait_signal` activity) are woken when a matching signal arrives.

**Activities** implement the `Activity` interface (`Descriptor() + Execute()`). Built-ins live in `activities.go` and `catalog_activities.go`. The `script` activity runs sandboxed Starlark (`go.starlark.net`); it is disabled by default (`workflow.scriptEnabled: false`).

### Frontend

React SPA in `ui/src/`, built with Vite + TypeScript + Tailwind + `@xyflow/react`.

- **Routing**: React Router v6 in `ui/src/pages/workflowUi.tsx`
- **Data fetching**: TanStack Query (`@tanstack/react-query`) for REST calls; a shared WebSocket connection to `/api/ws` provides live updates
- **Visual designer**: `WorkflowDesignerPage.tsx` uses `@xyflow/react` to render and edit step graphs
- The production build output lands in `ui/dist/` and is embedded into the Go binary via `ui_embed.go`

### Configuration

Config is resolved in order: defaults (`config.Default()`) → `config.yaml` → `.env` / `.env.local` → `APP_*` environment variables. The `--config` flag overrides the config file path.

Key config knobs for development:

```yaml
ui:
  devProxyURL: http://localhost:5173   # used when --dev flag is passed

workflow:
  databasePath: data/workflows.db
  scriptEnabled: false                 # enable to use the starlark script activity
```
