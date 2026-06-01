package workflow

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/prasenjit-net/orchestra/internal/config"
)

func TestBuildClaudeMessagesPreservesToolHistory(t *testing.T) {
	messages := buildClaudeMessages([]aiMessage{
		{Role: "user", Content: "hello"},
		{
			Role:    "assistant",
			Content: "Let me check",
			ToolCalls: []aiToolCall{
				{ID: "tool-1", Name: "lookup", Arguments: `{"id":42}`},
			},
		},
		{Role: "tool", ToolCallID: "tool-1", Content: `{"ok":true}`},
	})

	if len(messages) != 3 {
		t.Fatalf("expected 3 Claude messages, got %d", len(messages))
	}
	assistantContent, ok := messages[1]["content"].([]map[string]any)
	if !ok || len(assistantContent) != 2 {
		t.Fatalf("expected assistant content with text and tool_use, got %#v", messages[1]["content"])
	}
	if assistantContent[1]["type"] != "tool_use" {
		t.Fatalf("expected tool_use block, got %#v", assistantContent[1])
	}
	toolResult, ok := messages[2]["content"].([]map[string]any)
	if !ok || toolResult[0]["type"] != "tool_result" {
		t.Fatalf("expected tool_result block, got %#v", messages[2]["content"])
	}
}

func TestCopilotTokenCaching(t *testing.T) {
	var tokenCalls atomic.Int32
	var completionCalls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			tokenCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"token":"short-lived","expires_at":4102444800}`)
		case "/chat/completions":
			completionCalls.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer short-lived" {
				t.Fatalf("expected bearer token, got %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newAIProviderClient(config.WorkflowConfig{
		CopilotOAuthToken: "gho_test",
		CopilotBaseURL:    server.URL,
		CopilotTokenURL:   server.URL + "/copilot_internal/v2/token",
	})

	for i := 0; i < 2; i++ {
		resp, err := client.Complete(context.Background(), aiChatRequest{
			Provider: aiProviderCopilot,
			Messages: []aiMessage{{Role: "user", Content: "hello"}},
		})
		if err != nil {
			t.Fatalf("Complete returned error: %v", err)
		}
		if resp.Content != "ok" {
			t.Fatalf("expected ok response, got %q", resp.Content)
		}
	}

	if tokenCalls.Load() != 1 {
		t.Fatalf("expected one token exchange, got %d", tokenCalls.Load())
	}
	if completionCalls.Load() != 2 {
		t.Fatalf("expected two completion calls, got %d", completionCalls.Load())
	}
}

func TestEnhancePromptUsesSelectedProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Fatalf("expected Claude messages endpoint, got %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["model"] != "claude-sonnet-4-6" {
			t.Fatalf("expected Claude model, got %#v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"better prompt"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	cfg.Workflow.ClaudeAPIKey = "test"
	cfg.Workflow.ClaudeBaseURL = server.URL
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	prompt, err := service.EnhancePrompt(context.Background(), "draft prompt", aiProviderClaude, "")
	if err != nil {
		t.Fatalf("EnhancePrompt returned error: %v", err)
	}
	if prompt != "better prompt" {
		t.Fatalf("expected enhanced prompt, got %q", prompt)
	}
}

func TestResolveAIProvider(t *testing.T) {
tests := []struct {
name      string
cfg       config.WorkflowConfig
requested string
want      string
wantErr   bool
}{
{
name:      "explicit openai",
cfg:       config.WorkflowConfig{OpenAIAPIKey: "sk-test"},
requested: "openai",
want:      aiProviderOpenAI,
},
{
name:      "explicit claude",
cfg:       config.WorkflowConfig{ClaudeAPIKey: "key"},
requested: "claude",
want:      aiProviderClaude,
},
{
name:      "explicit copilot",
cfg:       config.WorkflowConfig{CopilotOAuthToken: "gho_test"},
requested: "copilot",
want:      aiProviderCopilot,
},
{
name:      "explicit unknown",
cfg:       config.WorkflowConfig{OpenAIAPIKey: "sk-test"},
requested: "anthropic",
wantErr:   true,
},
{
name:      "auto-select openai when all configured",
cfg:       config.WorkflowConfig{OpenAIAPIKey: "sk", ClaudeAPIKey: "claude", CopilotOAuthToken: "gho"},
requested: "",
want:      aiProviderOpenAI,
},
{
name:      "auto-select copilot when openai missing",
cfg:       config.WorkflowConfig{CopilotOAuthToken: "gho", ClaudeAPIKey: "claude"},
requested: "",
want:      aiProviderCopilot,
},
{
name:      "auto-select claude when only claude configured",
cfg:       config.WorkflowConfig{ClaudeAPIKey: "claude"},
requested: "",
want:      aiProviderClaude,
},
{
name:      "no provider configured returns error",
cfg:       config.WorkflowConfig{},
requested: "",
wantErr:   true,
},
{
name:      "case-insensitive explicit",
cfg:       config.WorkflowConfig{ClaudeAPIKey: "key"},
requested: "Claude",
want:      aiProviderClaude,
},
}
for _, tc := range tests {
t.Run(tc.name, func(t *testing.T) {
got, err := resolveAIProvider(tc.cfg, tc.requested)
if tc.wantErr {
if err == nil {
t.Fatalf("expected error, got provider %q", got)
}
return
}
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if got != tc.want {
t.Fatalf("expected %q, got %q", tc.want, got)
}
})
}
}
