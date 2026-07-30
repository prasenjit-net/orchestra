# Orchestra Software Design

This directory is the canonical design description for Orchestra. It documents the
software that is implemented in this repository, not competing proposals. When code
and documentation disagree, the code is authoritative and the affected design document
must be updated in the same change.

## Document map

| Document | Scope |
|---|---|
| [01-system-overview.md](01-system-overview.md) | Product boundaries, components, design principles, and end-to-end request flow |
| [02-runtime-architecture.md](02-runtime-architecture.md) | Process startup, controller and worker roles, concurrency, node lifecycle, and shutdown |
| [03-workflow-engine.md](03-workflow-engine.md) | Definition versioning, execution state machine, task leases, transitions, retries, signals, and callbacks |
| [04-persistence-model.md](04-persistence-model.md) | SQLite/PostgreSQL behavior, tables, relationships, migrations, and transaction boundaries |
| [05-api-and-events.md](05-api-and-events.md) | HTTP surfaces, route authorization, external webhook API, errors, pagination, and WebSocket events |
| [06-security.md](06-security.md) | Authentication, authorization, entitlements, sessions, CSRF, API keys, auditing, and hardening |
| [07-activities-and-integrations.md](07-activities-and-integrations.md) | Activity contract, built-ins, Starlark, AI providers, agents, MCP, schemas, and import/export |
| [08-frontend.md](08-frontend.md) | React architecture, routing, state, live updates, editors, theming, permissions, and UX boundaries |
| [09-configuration-and-deployment.md](09-configuration-and-deployment.md) | Configuration precedence, CLI, builds, containers, database setup, and deployment topologies |
| [10-operations-and-reliability.md](10-operations-and-reliability.md) | Observability, failure recovery, health, maintenance, capacity, backups, and operational runbooks |
| [11-testing-and-delivery.md](11-testing-and-delivery.md) | Test strategy, CI, quality gates, release process, and change validation |
| [12-extension-points-and-limitations.md](12-extension-points-and-limitations.md) | Supported extension seams, architectural constraints, known gaps, and decision rules |

## Audience

- Contributors use these documents to locate ownership and preserve invariants.
- Reviewers use them to assess behavior, security, and compatibility impact.
- Operators use the deployment and reliability documents as implementation-oriented
  references; the root `README.md` remains the concise getting-started guide.
- Product and security owners use the workflow, API, and security documents to reason
  about user-visible guarantees.

## Documentation rules

1. Describe implemented behavior in present tense.
2. Label limitations and future work explicitly; do not present planned behavior as live.
3. Prefer stable concepts and ownership boundaries over line-by-line code narration.
4. Link to source modules, configuration keys, route names, and table names when they
   are part of a contract.
5. Update every affected document when changing a cross-cutting contract.
6. Keep operational secrets, real credentials, and local environment values out of docs.

## Cross-cutting invariants

- Persistent workflow state is authoritative; in-memory state is an optimization only.
- Definition versions used by runs are immutable snapshots.
- Unsafe browser requests require both an authenticated session and CSRF verification.
- Authorization is deny-by-default at the route boundary and rechecked for object scope.
- API key secrets and session tokens are stored only as hashes and are never recoverable.
- Workflow state changes and durable workflow events are committed transactionally.
- External effects may be delivered more than once after lease expiry or process failure;
  activities that cause side effects must be designed for idempotency.
- The embedded UI and Go API are released as one versioned artifact.

## Primary source map

| Concern | Source |
|---|---|
| Startup and roles | `cmd/app/serve.go` |
| Configuration | `internal/config/config.go`, `example.config.toml` |
| HTTP composition | `internal/server/server.go`, `internal/api/router.go` |
| Workflow engine | `internal/workflow/service*.go`, `internal/workflow/types.go` |
| Activities and AI | `internal/workflow/activities.go`, `catalog_activities.go`, `ai_*.go` |
| Identity and policy | `internal/auth/`, `internal/api/auth_middleware.go` |
| Browser application | `ui/src/App.tsx`, `ui/src/services/api.ts`, `ui/src/` |
| Build and release | `Makefile`, `Dockerfile`, `.github/workflows/`, `.goreleaser.yml` |
