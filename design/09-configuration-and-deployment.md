# Configuration and Deployment

## Configuration sources

Configuration is resolved from lowest to highest precedence:

1. defaults in `internal/config/config.go`;
2. `config.toml` or `config.yaml`, or `--config` path;
3. `.env` and `.env.local` loaded from the working directory;
4. `APP_` environment variables;
5. command-line overrides such as `--port` and role flags.

Environment keys replace dots with underscores. Selected camelCase settings also have
human-friendly explicit aliases, for example `APP_WORKFLOW_DATABASE_URL`. Operators
should prefer the names shown in `example.config.toml` and deployment examples.

## Configuration groups

| Group | Responsibility |
|---|---|
| `app` | Display name, environment, exact public URL, description |
| `server` | Bind host/port and HTTP timeouts |
| `logging` | Level and text/JSON format |
| `ui` | Vite development proxy URL |
| `ai` | Provider credentials, endpoints, Copilot headers, shared proxy |
| `workflow` | Enablement, database, polling/lease, and Starlark resource limits |
| `auth` | Session/cookie/bootstrap/audit/proxy settings |
| `auth.apiKeys` | Key lifetime, usage write window, and rate limits |
| `webhook` | External auth mode and callback allowlist |
| `node` | Stable ID, roles, advertised concurrency, and health address |
| `node.health` | Heartbeat and offline thresholds |

`app.url` is security-sensitive: CSRF origin checks compare against it. It must be the
exact browser-visible origin, including scheme and non-default port.

## Secret handling

AI keys, Copilot OAuth token, initial administrator password, PostgreSQL URL, and proxy
credentials should enter through environment variables or mounted secret files rather
than committed configuration.

The raw settings UI can read/write the active config file and may expose secrets. It is
mounted only when a process has both controller and worker roles, but permissions and
deployment policy must still restrict access. Controller-only production deployments
avoid exposing that endpoint.

## CLI

```text
orchestra init [--path PATH] [--force]
orchestra schema [--driver sqlite|postgres] [--create]
orchestra serve [--controller] [--worker] [--dev] [--port N]
orchestra users reset-password USERNAME --password-file FILE|-
orchestra version
```

`init` writes local starter files. `schema` prints or applies DDL. `serve` composes roles.
`users reset-password` is the offline recovery path. Build version fields are injected
through linker flags.

## Local development

Typical flow:

```text
make install-deps
make init
make dev-all
```

The Go server runs with `--dev` and proxies browser routes to Vite on port 5173 while
serving API routes itself. SQLite at `data/workflows.db` is the default. The configured
public URL and development proxy URL must match browser origins for login/CSRF.

## Production build

The build is ordered:

1. `npm run build` compiles the React application into `ui/dist`.
2. Go compilation embeds that directory through `ui_embed.go`.
3. Linker flags set version, commit, and UTC build date.

`make build` writes `build/orchestra`. At runtime no Node.js process or static file
server is required.

The multi-stage Dockerfile uses Node 22, Go 1.25, and a non-root Alpine runtime with CA
certificates and timezone data. It exposes controller port 8080 and worker health port
8081.

## SQLite deployment

Use one all-in-one process with a persistent local volume. SQLite auto-initializes its
schema and uses WAL. Do not mount one SQLite file concurrently across multiple hosts or
network filesystems. Back up using a SQLite-aware method that includes a consistent WAL
checkpoint/snapshot.

SQLite is appropriate for development and modest single-node workloads. One open
connection serializes database work and simplifies transactional behavior.

## PostgreSQL deployment

Provision the database, then run schema initialization before controllers/workers:

```text
orchestra schema --create
```

All processes use the same database URL. Controllers and workers are stateless with
respect to local workflow/auth state, apart from configuration and temporary in-memory
live subscriptions.

The supplied Compose topology contains:

- PostgreSQL 16;
- one schema-init job;
- two controller-only processes behind NGINX;
- three worker-only processes;
- controller and worker health checks;
- fixed node IDs and a shared internal network.

This topology demonstrates role separation but does not solve cross-process live event
propagation. Browser state can lag until REST refetch when a worker on another process
commits a change.

## Reverse proxy

The reverse proxy should:

- terminate TLS or forward TLS directly;
- preserve WebSocket upgrades;
- forward the original Host and approved client address headers;
- use sticky sessions only if operationally desired, not for auth correctness;
- target controller nodes only;
- restrict database and worker health ports to internal networks.

Set `auth.trustedProxyCIDRs` to only directly connected proxy networks. Orchestra ignores
forwarded address headers from other peers. If TLS terminates at the proxy, set secure
cookie behavior and HSTS at the appropriate layer.

## Role-specific requirements

| Requirement | Controller | Worker | All-in-one |
|---|---|---|---|
| Database access | Yes | Yes | Yes |
| Auth bootstrap secret | Yes | No | Yes |
| AI/MCP/downstream credentials | Needed for controller AI authoring; worker not executing | Needed for activities | Needed |
| Public HTTP exposure | Yes | No | Yes |
| Health endpoint | Main `/livez` | `node.healthAddr` | Main `/livez` |
| Embedded UI | Served | Not served | Served |

Because agents and HTTP activities execute on workers, activity credentials and network
routes must be present there. There is no controller-to-worker secret synchronization.

## Configuration caveats

- `node.maxConcurrentTasks` is advertised in node metadata but does not currently bound
  a worker pool; per-process execution is sequential.
- `workflow.scriptEnabled` does not currently disable the DB-backed script activity.
- `webhook.enabled` is parsed but the external router is currently mounted regardless;
  use required authentication and network policy rather than relying on this flag.
- `webhook.authenticationMode = audit` permits anonymous external operations and is only
  a temporary migration setting.

## Release artifacts

GoReleaser builds Linux, macOS, and Windows archives for amd64/arm64 combinations listed
in `.goreleaser.yml`, adds checksums, and embeds version data. The release workflow is
manually dispatched with semantic version bump type, creates an annotated tag, and
publishes a clean GoReleaser release.

## Deployment checklist

1. Set exact `app.url` and trusted proxy CIDRs.
2. Provision TLS and internal network policy.
3. Supply database and bootstrap credentials through secret management.
4. Apply PostgreSQL schema before starting nodes.
5. Use stable node IDs and persistent database storage.
6. Keep external API authentication `required`.
7. Configure callback allowlist narrowly.
8. Verify controller and worker `/livez`, login, WebSocket upgrade, and one test workflow.
9. Protect backups and exported bundles as sensitive data.
10. Monitor task lease expiry, failed runs, offline nodes, and auth audit events.
