# Orchestra

Orchestra is a durable workflow engine with:

- Go backend at the repository root
- React + Vite frontend in `ui/`
- Embedded production UI served directly from the Go binary
- SQLite-backed workflow runtime, control plane routes, and WebSocket live updates

App repository: `https://github.com/prasenjit-net/orchestra`

## What You Get

- `serve`, `init`, and `version` CLI commands
- `chi`-based API routing under `/api`
- Example endpoints at `/api/health` and `/api/example`
- Embedded React build via Go `embed`
- Development mode with Vite proxy support
- Structured logging with `slog`
- GitHub Actions for lint, test, and build

## Folder Structure

```text
.
├── .github/
│   └── workflows/
├── cmd/
│   └── app/
├── internal/
│   ├── api/
│   ├── config/
│   ├── logging/
│   ├── server/
│   └── version/
├── ui/
│   ├── dist/
│   ├── public/
│   └── src/
├── .env.example
├── config.yaml
├── main.go
├── ui_embed.go
├── Makefile
└── README.md
```

## How Embedding Works

1. The frontend lives in `ui/`.
2. `npm run build` writes the production bundle to `ui/dist`.
3. `ui_embed.go` embeds `ui/dist` into the Go binary.
4. The server mounts API routes under `/api` and serves the React SPA for every other route.

That gives you one deployment artifact: the compiled Go executable.

## Development Workflow

### Prerequisites

- Go 1.23+
- Node.js 20+
- npm

### Initial Setup

```bash
cp .env.example .env
make install-deps
make dev-all
```

Open:

- UI: `http://localhost:8080`
- API: `http://localhost:8080/api`
- Health: `http://localhost:8080/api/health`

### Common Commands

```bash
make dev        # backend only, proxies UI requests to Vite when APP_UI_DEV_PROXY_URL is set
make dev-ui     # Vite dev server on :5173
make dev-all    # backend + Vite together
make build      # build UI, embed it, compile one binary
make run        # build and run the production binary
make test       # run Go tests
make lint       # go vet
make lint-ui    # eslint for the React app
```

## Production Build

```bash
make build
./build/$(basename "$PWD") serve
```

The binary contains the compiled React app. No separate Node.js server is required in production.

## Configuration

Configuration is loaded in this order:

1. defaults from the Go config package
2. `config.yaml`
3. `.env` and `.env.local`
4. environment variables prefixed with `APP_`
5. CLI flags

Example environment overrides:

```bash
APP_SERVER_PORT=9090
APP_LOGGING_LEVEL=debug
APP_UI_DEV_PROXY_URL=http://localhost:5173
```

## UI Notes

The frontend includes:

- fixed left sidebar shell
- card-based dashboard and operations pages
- Tailwind utility styling with shared badges and section headers
- light/dark/system theme toggle
- React Query plus a shared WebSocket live bus for server-pushed updates

## Files to Review First

- `main.go`
- `ui_embed.go`
- `cmd/app/root.go`
- `cmd/app/serve.go`
- `internal/config/config.go`
- `internal/server/server.go`
- `ui/src/App.tsx`
- `ui/src/components/Layout.tsx`
