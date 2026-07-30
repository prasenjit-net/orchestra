# Testing and Delivery

## Quality objectives

Changes must preserve workflow durability, authorization boundaries, database dialect
compatibility, API/UI contract alignment, and single-artifact build integrity. Test
depth should scale with blast radius.

## Backend tests

Go tests cover:

- workflow definition validation, versioning, execution, retries, delays, signals,
  task actions, imports, and schemas;
- SQLite behavior and workflow schema contracts;
- PostgreSQL-specific SQL generation/DDL contracts where direct integration is not used;
- authentication password hashing, sessions, roles, entitlements, API keys, rate limits,
  bootstrap, and user-management invariants;
- route/middleware integration for login, CSRF, permission checks, external keys, and
  workflow scope;
- AI provider payload translation, token caching, model filtering, and prompt context;
- server proxy, SPA, security headers, trusted addresses, and request limits.

Tests generally use temporary SQLite databases and `httptest` provider/downstream
servers. A test must not use production credentials or external network calls.

## Frontend checks

The current frontend has TypeScript build and ESLint checks but no committed unit,
component, or browser automation suite. Required automated checks are:

```text
cd ui && npm run lint
cd ui && npm run build
```

UI changes also require manual verification for relevant desktop/mobile states,
light/dark/system themes, permission variants, loading/error/empty/pending states,
keyboard behavior, and modal/editor layout.

Adding Vitest/Testing Library for stateful components and Playwright for critical user
flows is a recommended future improvement, not a current CI capability.

## Standard local validation

For a cross-stack change:

```text
go test ./...
go vet ./...
cd ui && npm run lint
cd ui && npm run build
git diff --check
```

The Makefile excludes accidental Go packages under `ui/node_modules` for normal test and
vet targets. Building UI first is required before Go package compilation when embedded
assets are absent.

## Risk-based test matrix

| Change | Minimum validation |
|---|---|
| Pure documentation | link/path checks, Markdown review, `git diff --check` |
| Isolated Go helper | focused unit test plus package test |
| Workflow transition/persistence | workflow suite, schema contracts, full Go suite |
| Auth/permission/API key | unit tests, route integration, role/object matrix |
| HTTP contract | handler test, TypeScript contract build, full API path |
| Frontend component | lint, build, browser state/viewport verification |
| Database schema | new DDL, SQLite migration, PostgreSQL DDL, upgrade test |
| Build/deployment | UI build, Go binary, container/config smoke test |

## Security test expectations

Security changes should test both allow and deny paths:

- missing, invalid, expired, revoked, and stale sessions;
- missing/wrong CSRF and invalid origins;
- each affected role and entitlement override;
- forced password change restrictions;
- owned versus foreign API keys;
- each external workflow action and instance scope;
- pinned version, callback, and signal-name restrictions;
- not-found concealment and audit outcome;
- rate-limit boundaries and cleanup.

Do not accept a UI-only permission test as proof of authorization.

## Workflow test expectations

Workflow engine changes should assert durable rows and observable API behavior for:

- successful first-to-terminal execution;
- retry scheduling and exhaustion;
- crash/expired lease requeue;
- delay state persistence;
- signal before/after wait and timeout;
- transition ordering/default/terminal behavior;
- context and end-output mapping;
- version pinning and published-state checks;
- task control actions;
- callback status without coupling completion to delivery.

Tests should use deterministic clocks or bounded waits where available and avoid sleep as
the only synchronization mechanism.

## CI pipeline

`.github/workflows/ci.yml` runs on pull requests and pushes to `main`:

1. Checkout.
2. Install Go and Node 22 with caches.
3. Download Go modules and install UI packages with `npm ci`.
4. Build UI so embedded assets exist.
5. Run backend vet.
6. Run frontend lint.
7. Run backend tests.
8. Compile the Go binary.

Concurrent runs for the same ref are canceled. CI currently has no PostgreSQL service,
browser test, race detector, vulnerability scan, or container smoke test.

## Static analysis and quality gates

Sonar analysis is expected to enforce new-code issues and duplication thresholds in the
repository's configured GitHub integration. Contributors should remove duplicated test
setup with table-driven tests or focused helpers when that improves clarity, but should
not create abstractions solely to satisfy a metric at the cost of domain readability.

Warnings about large generated Monaco worker chunks are build characteristics; feature
code is route-lazy and vendor chunks are manually separated. Bundle changes should be
reviewed by actual gzip/runtime impact, not only raw editor worker size.

## Release process

The release workflow is manually dispatched with patch/minor/major selection. It:

1. determines the next semantic version from tags;
2. creates and pushes an annotated tag;
3. installs Go and Node toolchains;
4. runs GoReleaser;
5. builds archives/checksums and publishes the GitHub release.

GoReleaser rebuilds the UI before compiling binaries and injects version, commit, and
build date. Release notes omit conventional docs/test/chore commits from generated
changelog entries.

## Change-review checklist

- Does the change preserve the invariants in `design/README.md`?
- Are current behavior and limitations documented without presenting plans as facts?
- Are SQLite and PostgreSQL paths both considered?
- Are route permissions and object-level checks explicit?
- Are API and TypeScript contracts updated together?
- Are external effects bounded, cancellable, and idempotency-aware?
- Are secrets excluded from logs, responses, fixtures, and exported examples?
- Are migration and rollback implications described?
- Did the author run the relevant focused and full checks?

## Documentation acceptance

Design docs are part of the change contract. A feature that adds a new table, route,
permission, activity, runtime role behavior, external protocol, or significant UI flow
must update the corresponding numbered document. Temporary implementation plans belong
in issues or pull requests, not as new root-level Markdown files.
