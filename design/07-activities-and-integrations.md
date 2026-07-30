# Activities and Integrations

## Activity contract

An activity implements two methods:

- `Descriptor()` returns stable name, display metadata, category, maturity, tags, and
  example input/output for the catalog and designer.
- `Execute(context, ActivityExecutionRequest)` returns an `ActivityResult` or error.

Execution receives workflow/definition identity, the pinned version, step, task,
current time, and an optional signal snapshot. It does not receive a mutable database
transaction.

An activity result can contain output JSON, a future delay, a signal wait, persisted
state, and context updates. Returning incompatible outcomes is unsupported; activities
should choose one primary outcome.

## Activity catalog

| Activity | Category | Behavior |
|---|---|---|
| `noop` | system | Completes immediately and passes through input |
| `log` | system | Writes structured application log output |
| `fail` | system | Produces an intentional execution error |
| `delay` | timers | Persists a target time and reschedules without sleeping |
| `transform` | data | Resolves mapped values against context |
| `set-context` | data | Writes explicit, validated context paths |
| `json-patch` | data | Applies add/replace/remove operations to JSON-like data |
| `template-render` | data | Renders a string from step-local data |
| `base64` | data | Standard/URL-safe Base64 encode or decode |
| `hash` | data | Computes a configured content digest |
| `branch` | control | Evaluates cases and returns selected target metadata |
| `wait-signal` | signal | Parks until a named signal or timeout |
| `approval` | operator | Waits for an approving signal and fails on rejection |
| `manual-task` | operator | Waits for a manual completion signal |
| `human-wait` | operator | Waits for a human resume signal |
| `http-request` | integration | Performs a bounded HTTP request |
| `webhook` | integration | POST-oriented alias over HTTP request |
| `email` | integration | POSTs an email-shaped payload to a provider endpoint |
| `slack` | integration | POSTs a Slack-compatible webhook payload |
| `queue-publish` | integration | POSTs a message to an external queue gateway |
| `script` | system | Executes sandboxed Starlark |
| `agent` | AI | Runs a saved multi-provider agent with optional MCP tools |

Maturity in descriptors distinguishes stable and beta activities. Email, Slack, and
queue publish are HTTP adapters, not native vendor SDK integrations.

## HTTP activities

The base HTTP activity accepts method, URL, headers, body, timeout, and optional expected
status. JSON bodies are encoded and content type is inferred. Responses include status,
headers, and bounded body content; response bodies are limited to 1 MiB.

Target URL validation rejects unsupported or unsafe forms according to the activity
validator. The activity uses a per-request timeout and propagates workflow cancellation.
Aliases rewrite their provider-specific URL field into the base HTTP contract.

External HTTP operations are at-least-once because a lease may expire after the remote
system accepts a request. Workflows should send an idempotency key derived from stable
run/task identity when the remote endpoint supports it.

## Signal activities

Signal-wait activities persist observed signal count, signal name, start time, and
optional timeout. On resume, they compare the current durable signal snapshot with the
observed count. This prevents an old signal from being repeatedly treated as new.

Approval additionally requires the latest payload's `approved` value to be truthy.
Timeout is represented as persisted task scheduling, not an in-memory timer. The
current task claim update only transitions pending tasks, so an expired waiting task is
not yet resumed by timeout alone; see the limitations register.

## Starlark scripts

The `script` activity supports inline source or a saved `scriptId`; saved source takes
precedence. Only Starlark is accepted. Execution is constrained by:

- maximum source bytes;
- context timeout, with a step request allowed only to reduce the configured timeout;
- maximum interpreter execution steps;
- maximum serialized output bytes;
- explicit exported globals, defaulting to `result`.

Predeclared values/modules include immutable `input`, step metadata, JSON helpers,
string helpers, collection helpers, workflow identity/failure helper, and assertions.
There is no filesystem, process, socket, or arbitrary Go API access.

The service currently registers the DB-backed script activity unconditionally so saved
scripts remain executable. Consequently `workflow.scriptEnabled` does not currently
disable runtime execution even though it remains in configuration. Operators must not
treat that flag as a security boundary until implementation is aligned.

## Agent model

A saved agent contains provider, model, system prompt, maximum tokens, temperature, and
links to MCP servers. Workflow input supplies:

- `agentId`;
- required prompt template;
- optional prior messages;
- optional mapped data.

The activity resolves the prompt, loads the agent, connects enabled MCP servers, and
calls the provider. If the model requests tools, Orchestra executes each MCP tool,
appends tool results, and continues until the model returns no tool calls. The final
output includes provider, model, text, role, finish reason, and token usage.

There is no explicit maximum agentic-loop iteration count. Context cancellation,
provider timeout, and downstream failures are the current termination controls; this is
a known resource-governance limitation.

## AI provider abstraction

Supported providers are OpenAI, Anthropic Claude, and GitHub Copilot.

| Provider | Authentication | Protocol path |
|---|---|---|
| OpenAI | API key Bearer token | OpenAI-compatible chat completions |
| Claude | `x-api-key` plus Anthropic version | Claude messages API with translated tools/history |
| Copilot | OAuth token exchanged for cached short-lived token | OpenAI-compatible Copilot chat API with required integration headers |

When an optional provider is omitted, resolution order is OpenAI, Copilot, then Claude,
based on configured credentials. Explicit saved-agent provider selection is authoritative.
Blank model selection uses a provider-specific default.

Provider calls share an optional HTTP proxy and bounded HTTP client timeout. Copilot
token exchange is mutex-protected and refreshed before expiry.

### Model discovery

The model endpoint fetches provider catalogs with a 15-second timeout, deduplicates and
sorts entries, and filters out likely embedding, image, audio, realtime, transcription,
moderation, search-only, code-specialized, or otherwise unsuitable agent models.
Filtering is heuristic; a manually entered model may still be accepted when discovery
is unavailable.

### AI authoring features

- Prompt enhancement combines the existing system prompt with mandatory user-provided
  enhancement instructions under explicit context headings.
- Script assist sends a conversation plus current script under a script-authoring system
  prompt and uses shared provider fallback behavior.
- Script validation checks generated/current source before application.

These features require `ai.use` but do not save changes until the user applies or saves
them through the resource editor.

## MCP connectors

An MCP server record stores SSE URL, request headers, enabled state, and the last
discovered tool catalog. Exploration opens an SSE connection, receives a POST endpoint,
performs the MCP initialize handshake, sends initialized notification, and requests
`tools/list` using protocol version `2024-11-05`.

At agent runtime, Orchestra reconnects and initializes but uses stored tool descriptors
instead of listing tools again. Tool calls are JSON-RPC requests over the advertised
POST endpoint; responses arrive over SSE and are correlated by request ID.

Operational implications:

- re-explore after the MCP server's tool contract changes;
- connector headers may be secrets and are stored in the primary database;
- tool names attached from multiple servers are keyed by name, so collisions can route
  to the last registered owner;
- MCP output is supplied to the model and must be treated as untrusted content;
- runtime failure to connect to any enabled attached connector fails the activity.

## JSON Schemas

Reusable JSON Schema documents can validate workflow start input and mapped final
output. Schema references are IDs stored in a definition version. Validation occurs at
the workflow boundary so invalid external/browser input fails before a run is scheduled,
and invalid final mapping prevents a false successful completion.

## Import and export

Export bundles can include definitions and dependent scripts, agents, connectors, and
schemas. Single-resource exports use the same bundle format. Import analysis classifies
items and conflicts without mutation. Apply requires explicit override IDs for existing
resources and upserts the selected bundle transactionally where practical.

IDs are preserved to keep definition references valid. Bundles can include connector
headers and agent configuration, so exported files must be handled as sensitive data.

## Adding an activity

1. Define a stable input/output contract and descriptor.
2. Implement cancellation, bounded resource use, and deterministic validation.
3. Register it in the activity catalog.
4. Add designer property controls and TypeScript types where needed.
5. Test success, invalid input, cancellation, retry behavior, and serialization.
6. Document side effects and idempotency expectations here.

Activity names are persisted in definition versions and therefore become compatibility
contracts. Renaming requires an alias or data migration.
