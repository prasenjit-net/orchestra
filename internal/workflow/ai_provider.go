package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/prasenjit-net/orchestra/internal/config"
)

const (
	aiProviderOpenAI  = "openai"
	aiProviderClaude  = "claude"
	aiProviderCopilot = "copilot"

	defaultOpenAIModel        = "gpt-4o"
	defaultClaudeModel        = "claude-sonnet-4-6"
	defaultCopilotModel       = "gpt-4o"
	defaultOpenAIEndpoint     = "https://api.openai.com/v1/chat/completions"
	defaultClaudeEndpoint     = "https://api.anthropic.com/v1/messages"
	defaultClaudeAPIVersion   = "2023-06-01"
	defaultCopilotBaseURL     = "https://api.githubcopilot.com"
	defaultCopilotTokenURL    = "https://api.github.com/copilot_internal/v2/token"
	defaultCopilotEditor      = "vscode/1.96.0"
	defaultCopilotPlugin      = "copilot/1.155.0"
	defaultCopilotIntegration = "vscode-chat"
	defaultCopilotIntent      = "conversation-panel"
	defaultAIRequestTimeout   = 120 * time.Second
	copilotRefreshBuffer      = 60 * time.Second
)

type aiMessage struct {
	Role       string
	Content    string
	ToolCallID string
	Name       string
	ToolCalls  []aiToolCall
}

type aiToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type aiToolDefinition struct {
	Name        string
	Description string
	Parameters  any
}

type aiUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type aiChatRequest struct {
	Provider     string
	Model        string
	SystemPrompt string
	Messages     []aiMessage
	MaxTokens    int
	Temperature  float64
	Tools        []aiToolDefinition
}

type aiChatResponse struct {
	Role         string
	Content      string
	FinishReason string
	ToolCalls    []aiToolCall
	Usage        aiUsage
}

type aiProviderClient struct {
	cfg        config.AIConfig
	httpClient *http.Client

	mu                 sync.Mutex
	copilotAccessToken string
	copilotTokenExpiry time.Time
}

func newAIProviderClient(cfg config.AIConfig) *aiProviderClient {
	transport := http.DefaultTransport
	if raw := strings.TrimSpace(cfg.HTTPProxy); raw != "" {
		if proxyURL, err := url.Parse(raw); err == nil {
			transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
		}
	}
	return &aiProviderClient{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: defaultAIRequestTimeout, Transport: transport},
	}
}

func normalizeAIProvider(provider string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", aiProviderOpenAI:
		return aiProviderOpenAI, nil
	case aiProviderClaude:
		return aiProviderClaude, nil
	case aiProviderCopilot:
		return aiProviderCopilot, nil
	default:
		return "", fmt.Errorf("unsupported AI provider %q", provider)
	}
}

// resolveAIProvider returns the provider to use for an AI request.
//
// If requested is non-empty it must be one of "openai", "claude", or "copilot";
// the returned provider is the normalised form.  Credentials are not validated
// here — that happens inside Complete() at call time.
//
// If requested is empty, the first configured provider is returned in
// preference order: openai → copilot → claude.  If no provider has credentials
// configured an error is returned so callers get a clear diagnostic instead of
// a silent fallback to a provider that will also fail.
func resolveAIProvider(cfg config.AIConfig, requested string) (string, error) {
	p := strings.TrimSpace(strings.ToLower(requested))
	if p != "" {
		switch p {
		case aiProviderOpenAI, aiProviderClaude, aiProviderCopilot:
			return p, nil
		default:
			return "", fmt.Errorf("unsupported AI provider %q", requested)
		}
	}
	// Auto-select: first configured in preference order.
	if strings.TrimSpace(cfg.OpenAIAPIKey) != "" {
		return aiProviderOpenAI, nil
	}
	if strings.TrimSpace(cfg.CopilotOAuthToken) != "" {
		return aiProviderCopilot, nil
	}
	if strings.TrimSpace(cfg.ClaudeAPIKey) != "" {
		return aiProviderClaude, nil
	}
	return "", fmt.Errorf("no AI provider configured; set ai.openaiAPIKey, ai.claudeAPIKey, or ai.copilotOAuthToken")
}

func defaultAIModel(provider string) string {
	switch provider {
	case aiProviderClaude:
		return defaultClaudeModel
	case aiProviderCopilot:
		return defaultCopilotModel
	default:
		return defaultOpenAIModel
	}
}

func providerDisplayName(provider string) string {
	switch provider {
	case aiProviderClaude:
		return "Claude"
	case aiProviderCopilot:
		return "GitHub Copilot"
	default:
		return "OpenAI"
	}
}

func normalizeAIModel(provider, model string) string {
	model = strings.TrimSpace(model)
	if model != "" {
		return model
	}
	return defaultAIModel(provider)
}

func (c *aiProviderClient) Complete(ctx context.Context, req aiChatRequest) (aiChatResponse, error) {
	provider, err := resolveAIProvider(c.cfg, req.Provider)
	if err != nil {
		return aiChatResponse{}, err
	}
	req.Provider = provider
	req.Model = normalizeAIModel(provider, req.Model)

	switch provider {
	case aiProviderOpenAI:
		if strings.TrimSpace(c.cfg.OpenAIAPIKey) == "" {
			return aiChatResponse{}, fmt.Errorf("OpenAI API key not configured (set ai.openaiAPIKey or APP_AI_OPENAI_API_KEY)")
		}
		return c.callOpenAICompatible(ctx, c.openAIEndpoint(), "Bearer "+c.cfg.OpenAIAPIKey, nil, req)
	case aiProviderClaude:
		if strings.TrimSpace(c.cfg.ClaudeAPIKey) == "" {
			return aiChatResponse{}, fmt.Errorf("Claude API key not configured (set ai.claudeAPIKey or APP_AI_CLAUDE_API_KEY)")
		}
		return c.callClaude(ctx, req)
	case aiProviderCopilot:
		if strings.TrimSpace(c.cfg.CopilotOAuthToken) == "" {
			return aiChatResponse{}, fmt.Errorf("GitHub Copilot OAuth token not configured (set ai.copilotOAuthToken or APP_AI_COPILOT_OAUTH_TOKEN)")
		}
		token, err := c.copilotToken(ctx)
		if err != nil {
			return aiChatResponse{}, err
		}
		return c.callOpenAICompatible(ctx, c.copilotEndpoint(), "Bearer "+token, c.copilotHeaders(), req)
	default:
		return aiChatResponse{}, fmt.Errorf("unsupported AI provider %q", provider)
	}
}

func (c *aiProviderClient) openAIEndpoint() string {
	base := strings.TrimSpace(c.cfg.OpenAIBaseURL)
	if base == "" {
		return defaultOpenAIEndpoint
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return strings.TrimRight(base, "/") + "/chat/completions"
}

func (c *aiProviderClient) claudeEndpoint() string {
	base := strings.TrimSpace(c.cfg.ClaudeBaseURL)
	if base == "" {
		return defaultClaudeEndpoint
	}
	if strings.HasSuffix(base, "/messages") {
		return base
	}
	return strings.TrimRight(base, "/") + "/messages"
}

func (c *aiProviderClient) copilotEndpoint() string {
	base := strings.TrimSpace(c.cfg.CopilotBaseURL)
	if base == "" {
		base = defaultCopilotBaseURL
	}
	return strings.TrimRight(base, "/") + "/chat/completions"
}

func (c *aiProviderClient) copilotExchangeURL() string {
	if url := strings.TrimSpace(c.cfg.CopilotTokenURL); url != "" {
		return url
	}
	return defaultCopilotTokenURL
}

func (c *aiProviderClient) copilotHeaders() map[string]string {
	editor := strings.TrimSpace(c.cfg.CopilotEditorVersion)
	if editor == "" {
		editor = defaultCopilotEditor
	}
	plugin := strings.TrimSpace(c.cfg.CopilotEditorPluginVersion)
	if plugin == "" {
		plugin = defaultCopilotPlugin
	}
	integration := strings.TrimSpace(c.cfg.CopilotIntegrationID)
	if integration == "" {
		integration = defaultCopilotIntegration
	}
	intent := strings.TrimSpace(c.cfg.CopilotOpenAIIntent)
	if intent == "" {
		intent = defaultCopilotIntent
	}

	return map[string]string{
		"editor-version":         editor,
		"editor-plugin-version":  plugin,
		"copilot-integration-id": integration,
		"openai-intent":          intent,
	}
}

func (c *aiProviderClient) copilotToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.copilotAccessToken != "" && time.Now().Add(copilotRefreshBuffer).Before(c.copilotTokenExpiry) {
		return c.copilotAccessToken, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.copilotExchangeURL(), nil)
	if err != nil {
		return "", fmt.Errorf("build Copilot token exchange request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.cfg.CopilotOAuthToken)
	req.Header.Set("Accept", "application/json")
	for key, value := range c.copilotHeaders() {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange Copilot token: %w", err)
	}
	defer resp.Body.Close()

	var payload struct {
		Token     string      `json:"token"`
		ExpiresAt json.Number `json:"expires_at"`
		Message   string      `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode Copilot token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(payload.Token) == "" {
		msg := strings.TrimSpace(payload.Message)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return "", fmt.Errorf("Copilot token exchange error: %s", msg)
	}

	expiresAt := time.Now().Add(30 * time.Minute)
	if raw := payload.ExpiresAt.String(); raw != "" {
		if unix, err := payload.ExpiresAt.Int64(); err == nil {
			expiresAt = time.Unix(unix, 0)
		}
	}
	c.copilotAccessToken = payload.Token
	c.copilotTokenExpiry = expiresAt
	return c.copilotAccessToken, nil
}

func (c *aiProviderClient) callOpenAICompatible(ctx context.Context, endpoint, authHeader string, extraHeaders map[string]string, req aiChatRequest) (aiChatResponse, error) {
	body, err := json.Marshal(buildOpenAIRequest(req))
	if err != nil {
		return aiChatResponse{}, fmt.Errorf("encode %s request: %w", providerDisplayName(req.Provider), err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return aiChatResponse{}, fmt.Errorf("build %s request: %w", providerDisplayName(req.Provider), err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", authHeader)
	for key, value := range extraHeaders {
		httpReq.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return aiChatResponse{}, fmt.Errorf("call %s: %w", providerDisplayName(req.Provider), err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return aiChatResponse{}, fmt.Errorf("read %s response: %w", providerDisplayName(req.Provider), err)
	}

	var payload struct {
		Choices []struct {
			Message struct {
				Role      string               `json:"role"`
				Content   string               `json:"content"`
				ToolCalls []openAIWireToolCall `json:"tool_calls,omitempty"`
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
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return aiChatResponse{}, fmt.Errorf("decode %s response: %w", providerDisplayName(req.Provider), err)
	}
	if payload.Error != nil {
		if payload.Error.Type != "" {
			return aiChatResponse{}, fmt.Errorf("%s error (%s, HTTP %d): %s", providerDisplayName(req.Provider), payload.Error.Type, resp.StatusCode, payload.Error.Message)
		}
		return aiChatResponse{}, fmt.Errorf("%s error (HTTP %d): %s", providerDisplayName(req.Provider), resp.StatusCode, payload.Error.Message)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return aiChatResponse{}, fmt.Errorf("%s error (HTTP %d)", providerDisplayName(req.Provider), resp.StatusCode)
	}
	if len(payload.Choices) == 0 {
		return aiChatResponse{}, fmt.Errorf("%s returned no choices", providerDisplayName(req.Provider))
	}

	choice := payload.Choices[0]
	toolCalls := make([]aiToolCall, 0, len(choice.Message.ToolCalls))
	for _, call := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, aiToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}

	return aiChatResponse{
		Role:         choice.Message.Role,
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		ToolCalls:    toolCalls,
		Usage: aiUsage{
			PromptTokens:     payload.Usage.PromptTokens,
			CompletionTokens: payload.Usage.CompletionTokens,
			TotalTokens:      payload.Usage.TotalTokens,
		},
	}, nil
}

func (c *aiProviderClient) callClaude(ctx context.Context, req aiChatRequest) (aiChatResponse, error) {
	bodyMap := map[string]any{
		"model":      req.Model,
		"messages":   buildClaudeMessages(req.Messages),
		"max_tokens": 2048,
	}
	if req.SystemPrompt != "" {
		bodyMap["system"] = req.SystemPrompt
	}
	if req.MaxTokens > 0 {
		bodyMap["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != 0 {
		bodyMap["temperature"] = req.Temperature
	}
	if len(req.Tools) > 0 {
		bodyMap["tools"] = buildClaudeTools(req.Tools)
	}

	body, err := json.Marshal(bodyMap)
	if err != nil {
		return aiChatResponse{}, fmt.Errorf("encode Claude request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.claudeEndpoint(), bytes.NewReader(body))
	if err != nil {
		return aiChatResponse{}, fmt.Errorf("build Claude request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.cfg.ClaudeAPIKey)
	apiVersion := strings.TrimSpace(c.cfg.ClaudeAPIVersion)
	if apiVersion == "" {
		apiVersion = defaultClaudeAPIVersion
	}
	httpReq.Header.Set("anthropic-version", apiVersion)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return aiChatResponse{}, fmt.Errorf("call Claude: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return aiChatResponse{}, fmt.Errorf("read Claude response: %w", err)
	}

	var payload struct {
		Role       string `json:"role"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text,omitempty"`
			ID    string          `json:"id,omitempty"`
			Name  string          `json:"name,omitempty"`
			Input json.RawMessage `json:"input,omitempty"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return aiChatResponse{}, fmt.Errorf("decode Claude response: %w", err)
	}
	if payload.Error != nil {
		return aiChatResponse{}, fmt.Errorf("Claude error (%s, HTTP %d): %s", payload.Error.Type, resp.StatusCode, payload.Error.Message)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return aiChatResponse{}, fmt.Errorf("Claude error (HTTP %d)", resp.StatusCode)
	}

	var contentParts []string
	toolCalls := make([]aiToolCall, 0)
	for _, block := range payload.Content {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				contentParts = append(contentParts, block.Text)
			}
		case "tool_use":
			args := "{}"
			if len(block.Input) > 0 {
				args = string(block.Input)
			}
			toolCalls = append(toolCalls, aiToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: args,
			})
		}
	}

	return aiChatResponse{
		Role:         payload.Role,
		Content:      strings.Join(contentParts, "\n"),
		FinishReason: payload.StopReason,
		ToolCalls:    toolCalls,
		Usage: aiUsage{
			PromptTokens:     payload.Usage.InputTokens,
			CompletionTokens: payload.Usage.OutputTokens,
			TotalTokens:      payload.Usage.InputTokens + payload.Usage.OutputTokens,
		},
	}, nil
}

type openAIWireRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIWireMessage `json:"messages"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	Temperature float64             `json:"temperature,omitempty"`
	Tools       []any               `json:"tools,omitempty"`
	ToolChoice  string              `json:"tool_choice,omitempty"`
}

type openAIWireMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	Name       string          `json:"name,omitempty"`
}

type openAIWireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func buildOpenAIRequest(req aiChatRequest) openAIWireRequest {
	messages := make([]openAIWireMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, openAIWireMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, msg := range req.Messages {
		wire := openAIWireMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
			Name:       msg.Name,
		}
		if len(msg.ToolCalls) > 0 {
			toolCalls := make([]openAIWireToolCall, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				var wireCall openAIWireToolCall
				wireCall.ID = call.ID
				wireCall.Type = "function"
				wireCall.Function.Name = call.Name
				wireCall.Function.Arguments = call.Arguments
				toolCalls = append(toolCalls, wireCall)
			}
			data, _ := json.Marshal(toolCalls)
			wire.ToolCalls = data
		}
		messages = append(messages, wire)
	}

	tools := make([]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Parameters,
			},
		})
	}

	result := openAIWireRequest{
		Model:       req.Model,
		Messages:    messages,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	if len(tools) > 0 {
		result.Tools = tools
		result.ToolChoice = "auto"
	}
	return result
}

func buildClaudeTools(tools []aiToolDefinition) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		parameters := tool.Parameters
		if parameters == nil {
			parameters = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		result = append(result, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": parameters,
		})
	}
	return result
}

func buildClaudeMessages(messages []aiMessage) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == "tool" {
			result = append(result, map[string]any{
				"role": "user",
				"content": []map[string]any{
					{
						"type":        "tool_result",
						"tool_use_id": msg.ToolCallID,
						"content":     msg.Content,
					},
				},
			})
			continue
		}

		content := make([]map[string]any, 0, 1+len(msg.ToolCalls))
		if strings.TrimSpace(msg.Content) != "" {
			content = append(content, map[string]any{
				"type": "text",
				"text": msg.Content,
			})
		}
		for _, call := range msg.ToolCalls {
			var input any
			if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil || input == nil {
				input = map[string]any{"raw": call.Arguments}
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    call.ID,
				"name":  call.Name,
				"input": input,
			})
		}
		result = append(result, map[string]any{
			"role":    msg.Role,
			"content": content,
		})
	}
	return result
}
