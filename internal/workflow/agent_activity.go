package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"
)

type agentActivity struct {
	ai          *aiProviderClient
	agentLookup func(ctx context.Context, id string) (Agent, error)
	mcpLookup   func(ctx context.Context, agentID string) ([]MCPServer, error)
	httpClient  *http.Client
}

func newAgentActivity(ai *aiProviderClient, agentLookup func(ctx context.Context, id string) (Agent, error), mcpLookup func(ctx context.Context, agentID string) ([]MCPServer, error)) *agentActivity {
	return &agentActivity{
		ai:          ai,
		agentLookup: agentLookup,
		mcpLookup:   mcpLookup,
		httpClient:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (a *agentActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "agent",
		DisplayName: "AI Agent",
		Description: "Invoke a saved AI agent via OpenAI, Claude, or GitHub Copilot.",
		Category:    "ai",
		Status:      "beta",
		Tags:        []string{"ai", "llm", "openai", "claude", "copilot"},
		ExampleInput: map[string]any{
			"agentId": "agt_abc123",
			"prompt":  "Summarize this: {{.input}}",
		},
		ExampleOutput: map[string]any{
			"content": "",
			"usage":   map[string]any{"promptTokens": 0, "completionTokens": 0},
		},
	}
}

type agentActivityMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type agentActivityInput struct {
	AgentID  string                 `json:"agentId"`
	Prompt   string                 `json:"prompt"`
	Messages []agentActivityMessage `json:"messages,omitempty"`
	Data     any                    `json:"data,omitempty"`
}

func (a *agentActivity) Execute(ctx context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	var input agentActivityInput
	if err := json.Unmarshal(req.Step.Input, &input); err != nil {
		return ActivityResult{}, fmt.Errorf("decode agent input: %w", err)
	}
	if input.AgentID == "" {
		return ActivityResult{}, fmt.Errorf("agentId is required")
	}
	if input.Prompt == "" {
		return ActivityResult{}, fmt.Errorf("prompt is required")
	}

	agent, err := a.agentLookup(ctx, input.AgentID)
	if err != nil {
		return ActivityResult{}, fmt.Errorf("lookup agent %q: %w", input.AgentID, err)
	}

	resolvedPrompt, err := resolveTemplate(input.Prompt, req.WorkflowContext)
	if err != nil {
		return ActivityResult{}, fmt.Errorf("resolve prompt template: %w", err)
	}

	// Connect to each enabled MCP server using stored (pre-explored) tools.
	// toolOwner maps toolName → session so we can route tool calls at runtime.
	toolOwner := map[string]*mcpSession{}
	var mcpTools []aiToolDefinition
	if a.mcpLookup != nil {
		mcpServers, err := a.mcpLookup(ctx, input.AgentID)
		if err != nil {
			return ActivityResult{}, fmt.Errorf("load mcp servers: %w", err)
		}
		for _, srv := range mcpServers {
			if !srv.Enabled {
				continue
			}
			// Use ConnectMCPSession — no tools/list call; tools come from the DB.
			sess, err := ConnectMCPSession(ctx, a.httpClient, srv.URL, srv.Headers)
			if err != nil {
				return ActivityResult{}, fmt.Errorf("connect mcp server %q: %w", srv.Name, err)
			}
			defer sess.Close()
			for _, t := range srv.Tools {
				toolOwner[t.Name] = sess
				mcpTools = append(mcpTools, aiToolDefinition{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				})
			}
		}
	}

	allTools := mcpTools

	// Build initial messages.
	messages := []aiMessage{}
	for _, m := range input.Messages {
		messages = append(messages, aiMessage{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, aiMessage{Role: "user", Content: resolvedPrompt})

	// Agentic loop.
	var lastResponse aiChatResponse
	for {
		resp, err := a.ai.Complete(ctx, aiChatRequest{
			Provider:     agent.Provider,
			Model:        agent.Model,
			SystemPrompt: agent.SystemPrompt,
			Messages:     messages,
			MaxTokens:    agent.MaxTokens,
			Temperature:  agent.Temperature,
			Tools:        allTools,
		})
		if err != nil {
			return ActivityResult{}, err
		}
		lastResponse = resp

		if len(resp.ToolCalls) == 0 {
			break
		}

		messages = append(messages, aiMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call.
		for _, tc := range resp.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
				args = map[string]any{"raw": tc.Arguments}
			}

			var toolResult string
			if sess, ok := toolOwner[tc.Name]; ok {
				toolResult, err = sess.CallTool(ctx, tc.Name, args)
				if err != nil {
					toolResult = fmt.Sprintf("error: %s", err.Error())
				}
			} else {
				toolResult = fmt.Sprintf("unknown tool: %s", tc.Name)
			}

			messages = append(messages, aiMessage{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    toolResult,
			})
		}
	}

	result := map[string]any{
		"provider":     agent.Provider,
		"model":        agent.Model,
		"content":      lastResponse.Content,
		"role":         lastResponse.Role,
		"finishReason": lastResponse.FinishReason,
		"usage": map[string]int{
			"promptTokens":     lastResponse.Usage.PromptTokens,
			"completionTokens": lastResponse.Usage.CompletionTokens,
			"totalTokens":      lastResponse.Usage.TotalTokens,
		},
	}

	out, err := json.Marshal(result)
	if err != nil {
		return ActivityResult{}, fmt.Errorf("encode agent result: %w", err)
	}
	return ActivityResult{Output: out}, nil
}

func resolveTemplate(tmpl string, workflowCtx json.RawMessage) (string, error) {
	if !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}
	var ctxData any
	if len(workflowCtx) > 0 {
		if err := json.Unmarshal(workflowCtx, &ctxData); err != nil {
			ctxData = nil
		}
	}
	t, err := template.New("prompt").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctxData); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}
	return buf.String(), nil
}
