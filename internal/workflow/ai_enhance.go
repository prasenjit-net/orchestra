package workflow

import (
	"context"
	"fmt"
	"strings"
)

const enhanceSystemMeta = `You are an expert prompt engineer specialising in writing system prompts for AI agents.

When given an existing system prompt and additional enhancement instructions, rewrite the prompt to be:
- Clear and unambiguous about the agent's role, capabilities, and constraints
- Well-structured with logical sections where appropriate
- Specific enough to guide behaviour without over-constraining creativity
- Free of filler phrases, redundancy, and vague instructions

Return ONLY the improved system prompt text. No preamble, no explanation, no markdown code fences — just the prompt itself.`

func (s *Service) EnhancePrompt(ctx context.Context, draft, message, provider, model string) (string, error) {
	if strings.TrimSpace(draft) == "" {
		return "", fmt.Errorf("prompt is required")
	}
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("enhancement message is required")
	}

	enhancementContext := fmt.Sprintf("Existing system prompt:\n\n%s\n\nAdditional enhancement instructions:\n\n%s", draft, message)

	resp, err := s.ai.Complete(ctx, aiChatRequest{
		Provider:     provider,
		Model:        model,
		SystemPrompt: enhanceSystemMeta,
		Messages: []aiMessage{
			{Role: "user", Content: enhancementContext},
		},
		MaxTokens: 2048,
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return "", fmt.Errorf("%s returned no text content", providerDisplayName(provider))
	}
	return resp.Content, nil
}
