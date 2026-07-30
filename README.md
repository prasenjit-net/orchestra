# Orchestra

Orchestra is a **durable workflow engine** that ships as a single Go binary with a React control plane embedded directly inside it. Define multi-step workflows, run them against a SQLite database, monitor execution in real time, and wire in AI agents, saved scripts, and external HTTP APIs — all from a visual canvas designer.

## Features

- **Visual workflow designer** — drag-and-drop canvas, double-click to configure any step, conditional branching with per-edge conditions
- **Durable execution** — SQLite-backed state machine with lease-based task polling, automatic retries, and crash recovery
- **Rich activity catalog** — HTTP request, delay, log, script (Starlark), AI agent (OpenAI, Claude, or GitHub Copilot), branch, wait-signal, approval, manual-task, webhook, and more
- **Conditional branching** — draw multiple outgoing edges from any step, attach path/operator/value conditions, and fan back in to a shared step
- **Script management** — write and reuse Starlark scripts with typed exports; attach them to workflow steps by reference
- **AI agents** — configure OpenAI, Claude, or GitHub Copilot agents with system prompts, model, and MCP server tool sets; invoke them as workflow steps
- **MCP connectors** — connect agents to Model Context Protocol servers for real-time tool calling
- **Signal delivery** — send named signals to running workflows to wake waiting steps or resume paused branches
- **Operations console** — live task queue view with retry, requeue, pause/resume, and cancel controls
- **Live dashboard** — real-time workflow stats streamed over WebSocket
- **Single binary** — the compiled React SPA is embedded in the Go binary; one file to deploy, no Node.js at runtime
- **Light/dark mode** — full theme support across every page

## Design Documentation

The canonical implementation design is organized under [`design/`](design/README.md).
It covers runtime architecture, workflow semantics, persistence, APIs, security,
activities and integrations, frontend structure, deployment, reliability, testing, and
known limitations.

---

## Quick Start

### Prerequisites

- Go 1.22+
- Node.js 20+
- npm

### 1. Install dependencies

```bash
make install-deps
```

### 2. Configure

```bash
cp example.config.toml config.toml
# Edit config.toml as needed (port, database path, and AI provider keys)
```

### 3. Initialise the data directory

```bash
make init
```

### 4. Run in development mode

```bash
make dev-all          # Go server + Vite dev server together
```

Open `http://localhost:8080`. The API is at `http://localhost:8080/api`.

On the first controller start, Orchestra creates an `admin` user and writes a one-time temporary password to `data/bootstrap-admin.txt` with mode `0600`. Sign in and change it immediately. In deployments, provide `APP_AUTH_INITIAL_ADMIN_PASSWORD_FILE` instead.

---

## Common Commands

| Command | Description |
|---|---|
| `make build` | Build the UI then compile the Go binary |
| `make run` | Build and run the production binary |
| `make dev` | Run the Go server only (proxies UI to Vite on `:5173`) |
| `make dev-ui` | Run the Vite dev server only |
| `make dev-all` | Run backend and frontend together |
| `make test` | Run Go tests |
| `make lint` | `go vet` the backend |
| `make lint-ui` | ESLint the frontend |
| `make fmt` | `go fmt` the backend |
| `make clean` | Remove build artefacts and `node_modules` |

---

## Configuration

Configuration is resolved in this order — later sources override earlier ones:

1. Defaults (baked into `internal/config/config.go`)
2. `config.toml` (or `config.yaml`) — use `--config` to point elsewhere
3. `.env` / `.env.local`
4. Environment variables prefixed `APP_` (dots become underscores, e.g. `APP_WORKFLOW_OPENAI_API_KEY`)
5. CLI flags (`--port`, `--config`)

Copy `example.config.toml` to get started:

```toml
[app]
name        = "Orchestra"
env         = "development"
url         = "http://localhost:8080"

[server]
host = "0.0.0.0"
port = 8080

[logging]
level  = "info"   # debug | info | warn | error
format = "text"   # text | json

[ui]
devProxyURL = "http://localhost:5173"   # only used with --dev flag

[workflow]
databasePath           = "data/workflows.db"
pollInterval           = "1s"
leaseDuration          = "30s"
scriptEnabled          = false          # set true to enable Starlark scripts
scriptTimeout          = "250ms"
scriptMaxSourceBytes   = 16384
# openaiAPIKey = ""                     # or use APP_WORKFLOW_OPENAI_API_KEY
# claudeAPIKey = ""                     # or use APP_WORKFLOW_CLAUDE_API_KEY
# copilotOAuthToken = ""                # or use APP_WORKFLOW_COPILOT_OAUTH_TOKEN

[auth]
sessionIdleTimeout     = "30m"
sessionAbsoluteTimeout = "8h"
cookieSecure           = "auto"
bootstrapOutputPath    = "data/bootstrap-admin.txt"
auditRetention         = "2160h"
trustedProxyCIDRs      = []

[auth.apiKeys]
defaultTTL        = "2160h"
maximumTTL        = "8760h"
requestsPerMinute = 60

[webhook]
authenticationMode = "required"
```

---

## Production Deployment

```bash
make build
./build/orchestra serve
```

The binary contains the full React app. Copy a single file to your server — no Node.js, no separate asset hosting.

### Environment variables for containers

```bash
APP_SERVER_PORT=9090
APP_LOGGING_LEVEL=info
APP_LOGGING_FORMAT=json
APP_WORKFLOW_DATABASE_PATH=/data/workflows.db
APP_WORKFLOW_OPENAI_API_KEY=sk-...
APP_WORKFLOW_CLAUDE_API_KEY=sk-ant-...
APP_WORKFLOW_COPILOT_OAUTH_TOKEN=gho_...
APP_WORKFLOW_SCRIPT_ENABLED=true
APP_AUTH_INITIAL_ADMIN_PASSWORD_FILE=/run/secrets/orchestra_admin_password
APP_WEBHOOK_AUTHENTICATION_MODE=required
```

Set `APP_APP_URL` to the exact public origin, including `https://` and any non-default port. Only add the directly connected load balancer networks to `auth.trustedProxyCIDRs`; forwarded client IP headers are ignored from all other peers.
For containers, `APP_AUTH_TRUSTED_PROXY_CIDRS` accepts a comma-separated CIDR list.

### Authentication and roles

All `/api` routes except health, public metadata, and login require a local user session. Unsafe browser requests use a session-bound CSRF token and exact origin checking.

| Role | Access |
|---|---|
| `admin` | User manager, entitlements, audit, all API keys, settings, and all workflow operations |
| `developer` | Workflow and resource authoring, run control, and personally owned API keys |
| `observer` | Read-only workflows, runs, resources, operations, cluster state, and personal sessions |

Admins manage users and per-permission allow/deny overrides from **Access control**. Password resets revoke every active session and require a change at the next login. For offline recovery:

```bash
printf '%s\n' 'new-long-password' | ./build/orchestra users reset-password admin --password-file -
```

### Webhook API keys

Create an API key from **Access control → API keys** and bind each grant to one workflow definition and action (`start`, `signal`, `status.read`, or `result.read`). Instance scope `own` limits control and result access to runs started by that same key. Secrets are stored as hashes and shown only when created or rotated.

```bash
curl -H "Authorization: Bearer $ORCHESTRA_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"orderId":"123"}' \
  "$ORCHESTRA_URL/ext/webhook/order-workflow/start"
```

---

## Architecture

```
HTTP request
  └─ chi router
       ├─ /api/*  → api.Handler  (REST + WebSocket live-bus)
       └─ /*      → embedded SPA (or Vite proxy with --dev)
```

### Backend packages

| Package | Role |
|---|---|
| `cmd/app` | Cobra CLI — `serve`, `init`, `version` commands |
| `internal/config` | Viper config, env vars, `.env` loading |
| `internal/server` | HTTP server, mounts API sub-router and SPA handler |
| `internal/api` | Chi router + all REST handlers, WebSocket live-bus endpoint |
| `internal/workflow` | Core engine: state machine, task poller, activity registry, signals |
| `internal/livebus` | In-process pub/sub; `/api/ws` streams events to browsers |
| `internal/logging` | `slog`-based structured logger |
| `internal/version` | Version info injected at link time |

### Workflow engine internals

A **workflow definition** is a versioned JSON document containing named **steps**. Each step names an **activity** and optional **transitions** with conditions for branching.

At runtime a **workflow instance** advances through steps by executing **tasks** (`workflow_tasks` table). The engine polls SQLite for `pending` tasks, acquires a lease, runs the activity, and writes the result back. The task status lifecycle is:

```
pending → running → completed
                 → failed      (retried up to maxAttempts)
                 → waiting     (blocked on a signal)
                 → paused      (manually paused via operations console)
```

**Signals** are delivered externally via `POST /api/workflows/{id}/signals/{name}`. Tasks in `waiting` status are woken when a matching signal arrives.

**Transitions** control routing after a step completes:
- No transitions declared → advance to the next step by index (backward-compatible linear behaviour)
- Empty transitions list → step is an explicit terminal; workflow ends here
- Non-empty transitions → conditions are evaluated against the workflow context; the first match wins, with an optional unconditional default

### Frontend

React SPA (`ui/src/`) built with Vite + TypeScript + Tailwind CSS + `@xyflow/react`.

- **Routing** — React Router v6 (`ui/src/pages/workflowUi.tsx`)
- **Data fetching** — TanStack Query for REST; shared WebSocket connection to `/api/ws` for live updates
- **Visual designer** — `WorkflowDesignerPage.tsx` uses `@xyflow/react` with custom node and edge components

---

## Activities Reference

### Built-in

| Activity | Category | Description |
|---|---|---|
| `noop` | control | No-op placeholder step |
| `log` | control | Write a structured log message |
| `fail` | control | Deliberately fail with a message |
| `delay` | control | Wait for a duration or until an absolute timestamp |
| `http-request` | integration | Make an HTTP request and capture status, headers, body |

### Catalog

| Activity | Category | Description |
|---|---|---|
| `transform` | data | Apply a template transform to the workflow context |
| `wait-signal` | signal | Pause until a named signal arrives |
| `branch` | control | Evaluate a list of cases and route to a target step |
| `webhook` | integration | Emit an outbound webhook and capture the response |
| `approval` | human | Wait for an approval signal (`approved: true/false`) |
| `manual-task` | human | Create a manual task and wait for completion signal |
| `human-wait` | human | Generic human-in-the-loop wait with a resume signal |

### Script (`script`)

Runs sandboxed **Starlark** code. Enable with `workflow.scriptEnabled = true`.

```json
{
  "script": "result = input['value'] * 2",
  "language": "starlark",
  "exports": ["result"]
}
```

Alternatively, reference a saved script by ID:

```json
{
  "scriptId": "scr_abc123"
}
```

Output keys match the `exports` list and are available as `{{steps.StepName.result}}` in subsequent steps.

### AI Agent (`agent`)

Calls the selected agent provider — OpenAI, Claude, or GitHub Copilot — with optional MCP tool calling.

```json
{
  "agentId": "agt_abc123",
  "prompt": "Summarise this order: {{steps.fetch.body}}"
}
```

Requires the matching provider credentials in `workflow.*` config (`openaiAPIKey`, `claudeAPIKey`, or `copilotOAuthToken`). The agent loops until the model stops or finishes its tool calls, executing any MCP tool calls along the way.

Output: `{ "content": "...", "usage": { "promptTokens": N, "completionTokens": N } }`

### Branch (`branch`)

Routes to different steps based on runtime context values.

```json
{
  "cases": [
    { "label": "approved", "path": "steps.review.approved", "operator": "eq", "value": true, "target": "SendApproval" },
    { "label": "rejected", "path": "steps.review.approved", "operator": "eq", "value": false, "target": "SendRejection" }
  ]
}
```

Supported operators: `eq`, `neq`, `exists`, `not_exists`, `truthy`, `falsy`

---

## Context Expressions

Any string input field can reference the workflow context using `{{ }}` templates:

| Expression | Value |
|---|---|
| `{{workflow.id}}` | Current workflow instance ID |
| `{{workflow.name}}` | Workflow definition name |
| `{{last}}` | Full output of the most recently completed step |
| `{{last.field}}` | A specific field from the last step output |
| `{{steps.StepName}}` | Full output of a named step |
| `{{steps.StepName.field}}` | A specific field from a named step |
| `{{signals.name.lastPayload}}` | Most recent payload of a named signal |
| `{{signals.name.count}}` | How many times a signal was received |

The expression picker (`{}` button) in the workflow designer shows all available expressions for the current step based on actual activity output schemas.

---

## REST API

Base path: `/api`

### Workflow Definitions

| Method | Path | Description |
|---|---|---|
| `GET` | `/workflow-definitions` | List all definitions |
| `POST` | `/workflow-definitions` | Create a new definition |
| `GET` | `/workflow-definitions/{id}` | Get definition with versions |
| `POST` | `/workflow-definitions/{id}/versions` | Save a new draft version |
| `POST` | `/workflow-definitions/{id}/versions/{v}/publish` | Publish a draft |

### Workflow Instances

| Method | Path | Description |
|---|---|---|
| `GET` | `/workflows` | List instances |
| `POST` | `/workflows` | Start a new instance |
| `GET` | `/workflows/{id}` | Get instance details |
| `GET` | `/workflows/{id}/history` | Event history |
| `GET` | `/workflows/{id}/tasks` | Task list |
| `POST` | `/workflows/{id}/tasks/{taskId}/{action}` | Task action (retry/requeue/pause/resume/cancel) |
| `POST` | `/workflows/{id}/signals/{name}` | Send a signal |

### Scripts

| Method | Path | Description |
|---|---|---|
| `GET` | `/scripts` | List scripts |
| `POST` | `/scripts` | Create script |
| `GET` | `/scripts/{id}` | Get script |
| `PUT` | `/scripts/{id}` | Update script |
| `DELETE` | `/scripts/{id}` | Delete script |

### Agents

| Method | Path | Description |
|---|---|---|
| `GET` | `/agents` | List agents |
| `POST` | `/agents` | Create agent |
| `GET` | `/agents/{id}` | Get agent |
| `PUT` | `/agents/{id}` | Update agent |
| `DELETE` | `/agents/{id}` | Delete agent |
| `GET` | `/agents/{id}/connectors` | List attached MCP servers |
| `POST` | `/agents/{id}/connectors/{serverId}` | Attach an MCP server |
| `DELETE` | `/agents/{id}/connectors/{serverId}` | Detach an MCP server |

### Connectors (MCP Servers)

| Method | Path | Description |
|---|---|---|
| `GET` | `/connectors` | List connectors |
| `POST` | `/connectors` | Create connector |
| `GET` | `/connectors/{id}` | Get connector |
| `PUT` | `/connectors/{id}` | Update connector |
| `DELETE` | `/connectors/{id}` | Delete connector |
| `POST` | `/connectors/{id}/explore` | Discover tools from the MCP server |

### Other

| Method | Path | Description |
|---|---|---|
| `GET` | `/activities` | List all registered activities with schemas |
| `GET` | `/health` | Health check |
| `GET` | `/ws` | WebSocket live event stream |

---

## Project Layout

```
.
├── cmd/app/                   # Cobra CLI (serve, init, version)
├── internal/
│   ├── api/                   # Chi router + all HTTP handlers
│   ├── config/                # Viper config + defaults
│   ├── livebus/               # In-process pub/sub for WebSocket fan-out
│   ├── logging/               # slog-based structured logger
│   ├── server/                # HTTP server wiring + SPA handler
│   ├── version/               # Build-time version info
│   └── workflow/              # Core engine, activities, scripts, agents
├── ui/
│   └── src/
│       ├── components/        # Shared React components
│       ├── hooks/             # Custom React hooks
│       ├── pages/             # Page components (one per route)
│       ├── services/          # API client functions
│       └── types/             # TypeScript type definitions
├── main.go                    # Entry point
├── ui_embed.go                # go:embed directive for ui/dist
├── example.config.toml        # Annotated configuration reference
├── Makefile                   # All dev/build/test commands
└── .github/workflows/         # CI (lint + test + build) and release
```

---

## Development Tips

- `ui/dist` must exist before `go build` — `make build-ui` populates it; `make build` does both in order
- Use `--dev` flag to proxy the UI through Vite for hot reload: `go run . serve --dev`
- Starlark scripts are disabled by default; set `workflow.scriptEnabled = true` in config
- The workflow database is auto-migrated on start — safe to run against an existing database
- Run a single Go test: `go test ./internal/workflow/... -run TestFunctionName`
- The WebSocket endpoint at `/api/ws` streams all live events; open the browser console on any page to see them arrive
