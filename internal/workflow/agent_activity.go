package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/prasenjit-net/orchestra/internal/config"
)

type agentActivity struct {
	cfg         config.WorkflowConfig
	agentLookup func(ctx context.Context, id string) (Agent, error)
	mcpLookup   func(ctx context.Context, agentID string) ([]MCPServer, error)
	httpClient  *http.Client
}

func newAgentActivity(cfg config.WorkflowConfig, agentLookup func(ctx context.Context, id string) (Agent, error), mcpLookup func(ctx context.Context, agentID string) ([]MCPServer, error)) *agentActivity {
	return &agentActivity{
		cfg:         cfg,
		agentLookup: agentLookup,
		mcpLookup:   mcpLookup,
		httpClient:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (a *agentActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "agent",
		DisplayName: "AI Agent",
		Description: "Invoke a saved AI agent via OpenAI chat completions.",
		Category:    "ai",
		Status:      "beta",
		Tags:        []string{"ai", "llm", "openai"},
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

// --- OpenAI types ---

type openAIRequest struct {
	Model       string      `json:"model"`
	Messages    []openAIMsg `json:"messages"`
	MaxTokens   int         `json:"max_tokens,omitempty"`
	Temperature float64     `json:"temperature,omitempty"`
	Tools       []any       `json:"tools,omitempty"`
	ToolChoice  string      `json:"tool_choice,omitempty"`
}

type openAIMsg struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	Name       string          `json:"name,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []openAIToolCall  `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
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

	apiKey := a.cfg.OpenAIAPIKey
	if apiKey == "" {
		return ActivityResult{}, fmt.Errorf("OpenAI API key not configured (set workflow.openaiAPIKey or APP_WORKFLOW_OPENAI_API_KEY)")
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
	var mcpTools []any
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
				mcpTools = append(mcpTools, map[string]any{
					"type": "function",
					"function": map[string]any{
						"name":        t.Name,
						"description": t.Description,
						"parameters":  t.InputSchema,
					},
				})
			}
		}
	}

	allTools := mcpTools

	// Build initial messages.
	messages := []openAIMsg{}
	if agent.SystemPrompt != "" {
		messages = append(messages, openAIMsg{Role: "system", Content: agent.SystemPrompt})
	}
	for _, m := range input.Messages {
		messages = append(messages, openAIMsg{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, openAIMsg{Role: "user", Content: resolvedPrompt})

	// Agentic loop.
	var lastResponse openAIResponse
	for {
		oaiReq := openAIRequest{
			Model:       agent.Model,
			Messages:    messages,
			MaxTokens:   agent.MaxTokens,
			Temperature: agent.Temperature,
		}
		if len(allTools) > 0 {
			oaiReq.Tools = allTools
			oaiReq.ToolChoice = "auto"
		}

		resp, err := a.callOpenAI(ctx, apiKey, oaiReq)
		if err != nil {
			return ActivityResult{}, err
		}
		lastResponse = resp

		if len(resp.Choices) == 0 {
			return ActivityResult{}, fmt.Errorf("openai returned no choices")
		}
		choice := resp.Choices[0]

		if choice.FinishReason == "stop" || len(choice.Message.ToolCalls) == 0 {
			break
		}

		// Append the assistant turn with its tool_calls.
		toolCallsJSON, _ := json.Marshal(choice.Message.ToolCalls)
		messages = append(messages, openAIMsg{
			Role:      "assistant",
			Content:   choice.Message.Content,
			ToolCalls: toolCallsJSON,
		})

		// Execute each tool call.
		for _, tc := range choice.Message.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = map[string]any{"raw": tc.Function.Arguments}
			}

			var toolResult string
			if sess, ok := toolOwner[tc.Function.Name]; ok {
				toolResult, err = sess.CallTool(ctx, tc.Function.Name, args)
				if err != nil {
					toolResult = fmt.Sprintf("error: %s", err.Error())
				}
			} else {
				toolResult = fmt.Sprintf("unknown tool: %s", tc.Function.Name)
			}

			messages = append(messages, openAIMsg{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    toolResult,
			})
		}
	}

	choice := lastResponse.Choices[0]
	result := map[string]any{
		"content":      choice.Message.Content,
		"role":         choice.Message.Role,
		"finishReason": choice.FinishReason,
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

func (a *agentActivity) callOpenAI(ctx context.Context, apiKey string, oaiReq openAIRequest) (openAIResponse, error) {
	body, err := json.Marshal(oaiReq)
	if err != nil {
		return openAIResponse{}, fmt.Errorf("encode openai request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return openAIResponse{}, fmt.Errorf("build openai request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return openAIResponse{}, fmt.Errorf("call openai: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return openAIResponse{}, fmt.Errorf("read openai response: %w", err)
	}

	var oaiResp openAIResponse
	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return openAIResponse{}, fmt.Errorf("decode openai response: %w", err)
	}
	if oaiResp.Error != nil {
		return openAIResponse{}, fmt.Errorf("openai error (%s): %s", oaiResp.Error.Type, oaiResp.Error.Message)
	}
	return oaiResp, nil
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
