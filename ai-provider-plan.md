Problem: Orchestra now has explicit multi-provider support for OpenAI, GitHub Copilot, and Claude, but provider-optional AI flows still effectively assume OpenAI. We need one consistent rule across the app: users can configure any subset of the three providers, agents can explicitly choose any provider, and whenever an AI feature runs without an explicit provider, Orchestra should auto-select the first configured provider in this order: `openai`, `copilot`, `claude`.

Approach:
- Preserve explicit provider choice for saved agents. If an agent record has `provider` set, that provider is authoritative for execution.
- Add a shared provider resolution step inside the AI runtime so omitted provider values are resolved once, centrally, from configured credentials using the preference order `openai -> copilot -> claude`.
- Use the same resolver everywhere AI provider selection is optional, so prompt enhancement, script assist, and future AI features all behave identically.
- Align frontend defaults with backend fallback behavior: when a user starts a new AI flow without having chosen a provider yet, the UI should preselect the preferred configured provider rather than hard-coding OpenAI.
- Keep model defaulting downstream of provider resolution so the default model always matches the actual resolved provider.

Design details:
- Shared resolver behavior:
  - explicit provider present -> normalize and validate it
  - provider omitted/blank -> select first configured provider in order `openai`, then `copilot`, then `claude`
  - none configured -> fail with one clear “no AI provider configured” error
- Provider readiness rules:
  - OpenAI is configured when `workflow.openaiAPIKey` is set
  - Copilot is configured when `workflow.copilotOAuthToken` is set
  - Claude is configured when `workflow.claudeAPIKey` is set
- The fallback rule applies only where provider is optional. It must not override a saved agent’s explicit provider.

Work items:
- `add-provider-fallback-resolver`: Extend `internal/workflow/ai_provider.go` with a shared resolver for explicit vs omitted provider selection and no-provider-configured errors.
- `wire-fallback-into-ai-endpoints`: Apply that resolver to prompt enhancement and migrate script assist onto the shared provider runtime so it is no longer OpenAI-only.
- `expose-provider-preferences-to-ui`: Add a small backend/frontend contract so the UI can determine which providers are configured and which provider should be the default selection.
- `update-agent-default-selection`: Make the new-agent page initialize with the preferred configured provider, while still allowing the user to choose any provider before saving.
- `cover-fallback-behavior`: Add tests and docs for preference order, explicit-provider precedence, missing-config behavior, and script-assist fallback.

Implementation hotspots:
- Backend runtime: `internal/workflow/ai_provider.go`, `internal/workflow/ai_enhance.go`, `internal/workflow/ai_script_assist.go`
- API surface: `internal/api/handler.go`, `internal/api/handler_script_assist.go`, and any metadata/config endpoint used to expose configured-provider availability
- Frontend: `ui/src/pages/AgentEditorPage.tsx`, script-assist UI entry points, `ui/src/services/api.ts`, and shared types

Notes:
- Current explicit provider support for agents should remain intact; this change adds fallback behavior only for omitted provider cases.
- Script assist is the main remaining AI feature still wired directly to OpenAI and should be brought under the shared runtime to avoid duplicated provider logic.
- Backend should remain the source of truth for provider availability because the UI should not infer secret presence from masked config text.
