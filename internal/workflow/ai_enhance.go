package workflow

import (
	"context"
	"fmt"
	"strings"
)

const enhanceSystemMeta = `You are an expert prompt engineer specialising in writing system prompts for AI agents.

When given a draft system prompt, rewrite it to be:
- Clear and unambiguous about the agent's role, capabilities, and constraints
- Well-structured with logical sections where appropriate
- Specific enough to guide behaviour without over-constraining creativity
- Free of filler phrases, redundancy, and vague instructions

Return ONLY the improved system prompt text. No preamble, no explanation, no markdown code fences — just the prompt itself.`

func (s *Service) EnhancePrompt(ctx context.Context, draft, provider, model string) (string, error) {
	if strings.TrimSpace(draft) == "" {
		return "", fmt.Errorf("prompt is required")
	}

	resp, err := s.ai.Complete(ctx, aiChatRequest{
		Provider:     provider,
		Model:        model,
		SystemPrompt: enhanceSystemMeta,
		Messages: []aiMessage{
			{Role: "user", Content: draft},
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
