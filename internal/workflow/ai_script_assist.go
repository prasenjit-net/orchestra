package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

### ctx — dict
Full workflow context. Structure:
  ctx["input"]             → dict of workflow start input (keys from the start payload)
  ctx["steps"]["name"]     → output dict of the step named "name"
  ctx["signals"]["name"]   → last received signal payload dict

### input — any
The step's static ` + "`data`" + ` field configured in the workflow definition.

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
  workflow.step_output("step_name")  → output dict of a past step
  workflow.signal("signal_name")     → last signal payload dict
  workflow.fail("message")           → fails the step with an error message

### asserts module
  asserts.non_empty(value, message?)    → fails the step if value is empty or falsy
  asserts.equals(left, right, message?) → fails the step if left != right

## Common patterns

Access workflow input:
  amount = ctx["input"].get("amount", 0)

Read a previous step's output:
  review = ctx["steps"].get("review-step", {})
  approved = review.get("approved", False)
  # or equivalently:
  review = workflow.step_output("review-step")

Conditional branching result:
  if approved:
      result = {"decision": "approved"}
  else:
      result = {"decision": "rejected"}

Fail on bad data:
  asserts.non_empty(ctx["input"].get("userId"), "userId is required")

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
	Role    string `json:"role"`    // "user" | "assistant"
	Content string `json:"content"`
}

// ScriptAssist sends the conversation history to GPT-4o with the script system
// prompt and returns the assistant's next message.
func (s *Service) ScriptAssist(ctx context.Context, messages []ScriptChatMessage, currentScript string) (string, error) {
	apiKey := s.cfg.OpenAIAPIKey
	if apiKey == "" {
		return "", fmt.Errorf("OpenAI API key not configured (set workflow.openaiAPIKey or APP_WORKFLOW_OPENAI_API_KEY)")
	}

	systemContent := scriptAssistSystemPrompt
	if strings.TrimSpace(currentScript) != "" {
		systemContent += "\n\n## User's current script\n```python\n" + currentScript + "\n```"
	}

	oaiMessages := []map[string]string{
		{"role": "system", "content": systemContent},
	}
	for _, m := range messages {
		oaiMessages = append(oaiMessages, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	reqBody, err := json.Marshal(map[string]any{
		"model":      "gpt-4o",
		"messages":   oaiMessages,
		"max_tokens": 2048,
	})
	if err != nil {
		return "", fmt.Errorf("encode script assist request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("build script assist request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call OpenAI: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read OpenAI response: %w", err)
	}

	var oaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &oaiResp); err != nil {
		return "", fmt.Errorf("decode OpenAI response: %w", err)
	}
	if oaiResp.Error != nil {
		return "", fmt.Errorf("OpenAI error (%s): %s", oaiResp.Error.Type, oaiResp.Error.Message)
	}
	if len(oaiResp.Choices) == 0 {
		return "", fmt.Errorf("OpenAI returned no choices")
	}
	return oaiResp.Choices[0].Message.Content, nil
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
		WorkflowContext: json.RawMessage(`{}`),
		Now:             time.Now().UTC(),
	}

	predeclared, err := buildScriptPredeclared(dummyReq, map[string]any{}, map[string]any{})
	if err != nil {
		return ValidateScriptResult{Error: fmt.Sprintf("build env: %s", err)}
	}

	if _, _, err := starlark.SourceProgramOptions(scriptFileOptions, "workflow.star", source, predeclared.Has); err != nil {
		return ValidateScriptResult{Error: err.Error()}
	}
	return ValidateScriptResult{Valid: true}
}
