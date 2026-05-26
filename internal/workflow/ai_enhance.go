package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const enhanceSystemMeta = `You are an expert prompt engineer specialising in writing system prompts for AI agents.

When given a draft system prompt, rewrite it to be:
- Clear and unambiguous about the agent's role, capabilities, and constraints
- Well-structured with logical sections where appropriate
- Specific enough to guide behaviour without over-constraining creativity
- Free of filler phrases, redundancy, and vague instructions

Return ONLY the improved system prompt text. No preamble, no explanation, no markdown code fences — just the prompt itself.`

func (s *Service) EnhancePrompt(ctx context.Context, draft string) (string, error) {
	apiKey := s.cfg.OpenAIAPIKey
	if apiKey == "" {
		return "", fmt.Errorf("OpenAI API key not configured (set workflow.openaiAPIKey or APP_WORKFLOW_OPENAI_API_KEY)")
	}

	reqBody, err := json.Marshal(map[string]any{
		"model": "gpt-4o",
		"messages": []map[string]string{
			{"role": "system", "content": enhanceSystemMeta},
			{"role": "user", "content": draft},
		},
		"max_tokens": 2048,
	})
	if err != nil {
		return "", fmt.Errorf("encode enhance request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("build enhance request: %w", err)
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
