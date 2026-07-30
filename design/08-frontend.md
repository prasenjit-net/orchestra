# Frontend Design

## Technology and packaging

The control plane is a React 18 and TypeScript SPA built by Vite. Tailwind CSS provides
the design tokens and utility styling. Major libraries are:

- React Router for browser routing;
- TanStack Query for server state;
- XYFlow for the visual workflow graph;
- Monaco for script, prompt, and schema editing;
- React Markdown for prompt preview;
- Lucide for interface icons.

`npm run build` runs project-reference TypeScript compilation and Vite production build.
The output is embedded into the Go binary. Large editor/designer routes are lazy-loaded,
and Vite manual chunking separates framework, query, router, XYFlow, Markdown, and
Monaco dependencies.

## Application composition

At startup, `main.tsx` initializes the theme and mounts providers for routing, query
state, theme, and authentication. `App.tsx` defines public and protected route trees.
Authenticated pages render inside `Layout` and `WorkflowLiveProvider`.

```text
ThemeProvider
  +-- QueryClientProvider
      +-- BrowserRouter
          +-- AuthProvider
              +-- App routes
                  +-- ProtectedRoute
                      +-- WorkflowLiveProvider
                          +-- Layout + page outlet
```

## Route model

Public routes are login only. Protected routes include:

- dashboard;
- workflow list, designer, and version management;
- scripts and script editor;
- JSON Schemas and schema editor;
- agents and agent editor;
- connectors and connector editor;
- runs and run details;
- signals, queues, operations, and cluster;
- settings;
- access control;
- forced password change.

Editor routes requiring mutation permissions are wrapped in `PermissionRoute`. Layout
navigation conditionally adds access control when any relevant security permission is
available. Pages must still handle API `403` because client checks are not security.

## Authentication state

`AuthProvider` loads `/api/auth/session` once, stores the user/session/permission
response in memory, and configures the API client's CSRF token. Login, logout, and
password change update this state. Any API `401` dispatches a global unauthorized event
that clears the session.

`ProtectedRoute` redirects unauthenticated users to login while preserving the desired
path. Users with temporary passwords are redirected to password change before the rest
of the application renders.

## API client

`ui/src/services/api.ts` centralizes URL construction, credentials, CSRF headers, JSON
decoding, error conversion, and typed resource clients. Feature pages do not call
`fetch` directly.

The API base defaults to `/api` and can be replaced by `VITE_API_BASE`. WebSocket URL
construction derives the correct `ws`/`wss` scheme and API base.

TypeScript contracts in `ui/src/types/index.ts` mirror backend response models. A backend
contract change must update both sides in one commit because they ship together.

## Server state and live updates

TanStack Query owns fetched collections and details. Query keys are stable by resource,
for example workflow lists, one definition, run history, tasks, agents, and connectors.

The live provider opens one WebSocket and translates entity events into targeted query
invalidations. Health events may update cached data directly. When connected, default
query stale time increases to five minutes; when disconnected it returns to zero so
focus/mount refetch remains the fallback.

The browser live client:

- ignores heartbeat events;
- closes a connection silent for 45 seconds;
- reconnects with bounded exponential backoff;
- tolerates malformed events;
- exposes connection status for UI indication.

Because live events are process-local, multi-controller deployments must not rely on
push alone. Page entry, focus, user refresh, and mutation invalidation remain required.

## Workflow designer

The designer uses XYFlow nodes and edges while maintaining a domain document compatible
with backend `StepDefinition`. It supports:

- adding and positioning activities;
- editing activity-specific properties in modal surfaces;
- defining conditional and terminal transitions;
- selecting start/end schemas and end-output mapping;
- distinguishing layout-only movement from semantic graph changes;
- creating a draft version for semantic changes;
- saving layout changes through the draft layout endpoint;
- publishing and activating versions;
- starting a published workflow.

Change classification compares normalized semantic fields separately from layout.
Deleting or changing an edge is semantic; moving nodes without changing activities,
inputs, retries, or transitions is layout-only. Backend validation is the final guard.

## Resource editors

### Script editor

Monaco edits Starlark source and exported values. AI assistance runs in a modal
conversation and proposed source is validated before application. Saved scripts are
referenced by workflow steps.

### JSON Schema editor

Provides schema-oriented editing and preview, with import/export integration. Schema
documents remain JSON and are validated by the backend on save/use.

### Agent editor

Edits provider, discovered/manual model, prompt, generation settings, and attached MCP
connectors. Prompt preview is the default view; edit and preview use a segmented control.
AI enhancement opens a centered modal and requires user instructions. The current prompt
and instructions are sent together, then the returned prompt replaces editor state.

### Connector editor

Edits server URL, headers, enabled state, and exploration. Discovered tools are displayed
from the persisted connector catalog.

## Operations and security UI

Run, queue, signals, operations, and cluster pages are optimized for scanning and direct
actions. Mutating controls are hidden or disabled based on permissions and show pending
and error states.

The access-control page contains user management, API key management, sessions, and
audit panels. Dialogs use native or modal semantics, bounded viewport height, centered
positioning, clear labels, and disabled close/actions while critical mutations run.

## Theme

Theme mode is `light`, `dark`, or `system`, stored under `orchestra-theme` in local
storage. Missing/invalid storage defaults to system. The document class and CSS
`color-scheme` are updated immediately, including on operating-system theme changes.
The login page follows the same resolved theme but intentionally has no selector.

## Responsive layout

Desktop uses a fixed sidebar and scrollable content region. Mobile uses a top bar,
modal navigation drawer, and body scroll lock. Editors use stable flex/grid dimensions
so Monaco and XYFlow own their internal scrolling without resizing surrounding controls.

## Accessibility and interaction rules

- Form controls have programmatic labels.
- Icon-only buttons have an accessible name and tooltip/title.
- Dialogs identify their heading and expose modal semantics.
- Keyboard focus must enter dialogs and critical actions remain reachable without a
  pointer.
- Color is not the only indication of status.
- Read-only permission states remove mutation affordances without hiding view data.
- Loading, empty, error, pending, and successful states are represented explicitly.

## Frontend boundaries

The frontend may validate early for usability but cannot define security or workflow
validity. It must not infer provider credential presence from raw/masked configuration,
trust WebSocket payloads as canonical data, or persist secrets in local storage.

There is currently no automated browser/unit test suite in `ui/package.json`; lint and
production compilation are the enforced frontend checks. High-risk interaction changes
therefore require deliberate manual browser verification until test infrastructure is
added.
