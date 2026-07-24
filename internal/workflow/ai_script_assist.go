package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.starlark.net/starlark"
)

// ─── Script assistant system prompt ──────────────────────────────────────────

const scriptAssistSystemPrompt = `You are an expert script writer for the Orchestra workflow engine.
Scripts run in a sandboxed Starlark environment — Python-like syntax, no imports, no I/O.

## Output rules
Assign results to named variables. The default export is ` + "`result`" + `:
  result = {"status": "ok", "value": 42}
Always wrap scripts in fenced code blocks using ` + "```python" + ` … ` + "```" + ` so they can be extracted.
Write the complete, runnable script every time — not fragments.

## Available predeclared names

### input — any
The step's mapped ` + "`data`" + ` field configured in the workflow definition. Map workflow input, previous step outputs, or signal values into ` + "`data`" + ` before the script runs.

### step — dict
  step["name"]        → current step name (string)
  step["activity"]    → activity name (string)

### json module
  json.encode(value)  → JSON string
  json.decode(str)    → value

### strings module
  strings.lower(value)               → string
  strings.upper(value)               → string
  strings.trim(value)                → string (strips whitespace)
  strings.contains(value, part)      → bool
  strings.replace(value, old, new)   → string

### collections module
  collections.compact(list_or_dict)  → removes falsy/empty values
  collections.flatten(list)          → flattens one nesting level

### workflow module
  workflow.id                        → current run ID (string)
  workflow.definition_id             → definition ID (string)
  workflow.definition_version        → version number (int)
  workflow.step_name                 → current step name (string)
  workflow.fail("message")           → fails the step with an error message

### asserts module
  asserts.non_empty(value, message?)    → fails the step if value is empty or falsy
  asserts.equals(left, right, message?) → fails the step if left != right

## Common patterns

Access mapped workflow input:
  amount = input.get("amount", 0)

Read a mapped previous step output:
  review = input.get("review", {})
  approved = review.get("approved", False)

Conditional branching result:
  if approved:
      result = {"decision": "approved"}
  else:
      result = {"decision": "rejected"}

Fail on bad data:
  asserts.non_empty(input.get("userId"), "userId is required")

## Rules
- No import statements
- No file I/O, network calls, or goroutines
- Booleans are True / False (capitalised, not lowercase)
- None (not null, nil, or undefined)
- Starlark has no classes — use dicts
- Strings use double or single quotes
- Use .get(key, default) on dicts to avoid missing-key errors`

// ─── Service methods ──────────────────────────────────────────────────────────

// ScriptChatMessage is one turn in a script assistant conversation.
type ScriptChatMessage struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

// ScriptAssist sends the conversation history to the configured AI provider
// with the script system prompt and returns the assistant's next message.
// The provider is auto-selected from whichever credential is configured
// (openai → copilot → claude preference order).
func (s *Service) ScriptAssist(ctx context.Context, messages []ScriptChatMessage, currentScript string) (string, error) {
	systemContent := scriptAssistSystemPrompt
	if strings.TrimSpace(currentScript) != "" {
		systemContent += "\n\n## User's current script\n```python\n" + currentScript + "\n```"
	}

	aiMessages := make([]aiMessage, 0, len(messages))
	for _, m := range messages {
		aiMessages = append(aiMessages, aiMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	resp, err := s.ai.Complete(ctx, aiChatRequest{
		SystemPrompt: systemContent,
		Messages:     aiMessages,
		MaxTokens:    2048,
	})
	if err != nil {
		return "", fmt.Errorf("script assist: %w", err)
	}
	return resp.Content, nil
}

// ValidateScriptResult is the result of a dry-run script validation.
type ValidateScriptResult struct {
	Valid bool   `json:"valid"`
	Error string `json:"error,omitempty"`
}

// ValidateScript parses, resolves, and compiles a Starlark script without
// executing it. This catches syntax errors and undefined name references
// without triggering any runtime errors about missing context data.
func (s *Service) ValidateScript(source string) ValidateScriptResult {
	dummyReq := ActivityExecutionRequest{
		Now: time.Now().UTC(),
	}

	predeclared, err := buildScriptPredeclared(dummyReq, map[string]any{})
	if err != nil {
		return ValidateScriptResult{Error: fmt.Sprintf("build env: %s", err)}
	}

	if _, _, err := starlark.SourceProgramOptions(scriptFileOptions, "workflow.star", source, predeclared.Has); err != nil {
		return ValidateScriptResult{Error: err.Error()}
	}
	return ValidateScriptResult{Valid: true}
}
