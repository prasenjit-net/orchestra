# GitHub Copilot AI Provider — Implementation Guide

This document describes how the GitHub Copilot AI provider works in go-virtual so you can re-implement it in another project.

---

## How It Works (High Level)

GitHub Copilot does not expose a direct API key. Instead it uses a two-step auth flow:

1. **Token exchange** — POST/GET to `https://api.github.com/copilot_internal/v2/token` using a long-lived GitHub OAuth token (`gho_...`). The response is a short-lived Copilot API token (valid ~30 minutes).
2. **Chat completions** — POST to `https://api.githubcopilot.com/chat/completions` using the short-lived token as a Bearer token. The request/response format is identical to the OpenAI Chat Completions API.

---

## Step 1 — Get Your OAuth Token

The OAuth token (`gho_...`) is the one issued when you authorise the GitHub Copilot extension in VS Code or the CLI. It is stored locally at:

```
~/.config/github-copilot/apps.json   # Linux / macOS
%APPDATA%\GitHub Copilot\apps.json   # Windows
```

Inside that file find the `oauth_token` value, e.g.:
```json
{
  "github.com": {
    "oauth_token": "gho_XXXXXXXXXXXXXXXXXXXX",
    "user": "yourname"
  }
}
```

Copy that value. **Do not commit it.** Treat it like a password.

---

## Step 2 — Token Exchange

### Request

```
GET https://api.github.com/copilot_internal/v2/token
Authorization: token gho_XXXXXXXXXXXXXXXXXXXX
Accept: application/json
editor-version: vscode/1.96.0
editor-plugin-version: copilot/1.155.0
copilot-integration-id: vscode-chat
openai-intent: conversation-panel
```

All four identity headers are **required** — the API returns 400 if any is missing.

### Response

```json
{
  "token": "tid=...; exp=1717000000; sku=...; ...",
  "expires_at": 1717000000
}
```

- `token` — short-lived Bearer token for completions.
- `expires_at` — **Unix timestamp integer** (not an RFC 3339 string). Parse with `time.Unix(n, 0)`.

### Caching

Cache the token and reuse it until it is within 60 seconds of expiry, then re-exchange. This avoids a token-exchange round-trip on every request.

```go
const tokenRefreshBuffer = 60 * time.Second

func (p *provider) getToken(ctx context.Context) (string, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    if p.cachedTok != "" && time.Now().Add(tokenRefreshBuffer).Before(p.expiresAt) {
        return p.cachedTok, nil
    }
    tok, expiresAt, err := exchangeToken(ctx, ...)
    if err != nil {
        return "", err
    }
    p.cachedTok = tok
    p.expiresAt = expiresAt
    return tok, nil
}
```

---

## Step 3 — Chat Completions

### Endpoint

```
POST https://api.githubcopilot.com/chat/completions
```

### Request Headers

```
Content-Type: application/json
Authorization: Bearer <short-lived-token>
editor-version: vscode/1.96.0
editor-plugin-version: copilot/1.155.0
copilot-integration-id: vscode-chat
openai-intent: conversation-panel
```

### Request Body

Identical to OpenAI Chat Completions:

```json
{
  "model": "gpt-4o",
  "messages": [
    { "role": "system", "content": "You are a helpful assistant." },
    { "role": "user",   "content": "Say hello in JSON." }
  ],
  "temperature": 0.2,
  "response_format": { "type": "json_object" }
}
```

`response_format` is optional but useful when you need JSON output.

### Response Body

Identical to OpenAI:

```json
{
  "choices": [
    {
      "message": {
        "content": "{ \"hello\": \"world\" }"
      }
    }
  ]
}
```

Read `choices[0].message.content`.

---

## Listing Available Models

The Copilot API exposes a models endpoint that returns all models your token and plan can access. Use the **short-lived token** (from Step 2 above), not the raw `gho_` OAuth token.

### Request

```
GET https://api.githubcopilot.com/models
Authorization: Bearer <short-lived-token>
editor-version: vscode/1.96.0
editor-plugin-version: copilot/1.155.0
copilot-integration-id: vscode-chat
```

### Response

```json
{
  "data": [
    {
      "id": "gpt-4o",
      "name": "GPT-4o",
      "capabilities": {
        "type": "chat",
        "supports_streaming": true
      }
    },
    {
      "id": "claude-3.5-sonnet",
      "name": "Claude 3.5 Sonnet",
      "capabilities": {
        "type": "chat",
        "supports_streaming": true
      }
    }
  ]
}
```

Filter by `capabilities.type == "chat"` to get models usable with the chat completions endpoint.

### Quick curl

```bash
# 1. Exchange your OAuth token for a short-lived Copilot token
COPILOT_TOKEN=$(curl -s \
  -H "Authorization: token gho_XXXXXXXXXXXXXXXXXXXX" \
  -H "editor-version: vscode/1.96.0" \
  -H "editor-plugin-version: copilot/1.155.0" \
  -H "copilot-integration-id: vscode-chat" \
  https://api.github.com/copilot_internal/v2/token | jq -r .token)

# 2. List models
curl -s \
  -H "Authorization: Bearer $COPILOT_TOKEN" \
  -H "editor-version: vscode/1.96.0" \
  -H "editor-plugin-version: copilot/1.155.0" \
  -H "copilot-integration-id: vscode-chat" \
  https://api.githubcopilot.com/models | jq '[.data[] | select(.capabilities.type=="chat") | .id]'
```

### Quick Go snippet

```go
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
    tok, err := p.getToken(ctx)
    if err != nil {
        return nil, err
    }

    req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.githubcopilot.com/models", nil)
    req.Header.Set("Authorization", "Bearer "+tok)
    for k, v := range identityHeaders {
        req.Header.Set(k, v)
    }

    resp, err := p.client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    var result struct {
        Data []struct {
            ID           string `json:"id"`
            Capabilities struct {
                Type string `json:"type"`
            } `json:"capabilities"`
        } `json:"data"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, err
    }

    var ids []string
    for _, m := range result.Data {
        if m.Capabilities.Type == "chat" {
            ids = append(ids, m.ID)
        }
    }
    return ids, nil
}
```

### Notes

- The list is **filtered by your Copilot plan** — Copilot Free/Pro/Pro+ show different sets of models.
- Models visible in the Copilot Chat UI may not yet appear in the API; the API only exposes production-ready models.
- If a model ID from this list returns an error on chat completions, try a different one — some models may be behind additional feature flags.

---

## Required Headers Summary

| Header | Token exchange | Completions | Default value |
|---|---|---|---|
| `editor-version` | ✅ required | ✅ required | `vscode/1.96.0` |
| `editor-plugin-version` | — | ✅ required | `copilot/1.155.0` |
| `copilot-integration-id` | ✅ required | ✅ required | `vscode-chat` |
| `openai-intent` | ✅ required | ✅ required | `conversation-panel` |
| `Authorization` | `token gho_...` | `Bearer <short-token>` | — |

---

## Minimal Go Implementation

```go
package copilot

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "sync"
    "time"
)

const (
    tokenURL        = "https://api.github.com/copilot_internal/v2/token"
    completionsURL  = "https://api.githubcopilot.com/chat/completions"
    refreshBuffer   = 60 * time.Second
)

var identityHeaders = map[string]string{
    "editor-version":         "vscode/1.96.0",
    "editor-plugin-version":  "copilot/1.155.0",
    "copilot-integration-id": "vscode-chat",
    "openai-intent":          "conversation-panel",
}

type Provider struct {
    OAuthToken string
    Model      string
    client     *http.Client

    mu        sync.Mutex
    cachedTok string
    expiresAt time.Time
}

func New(oauthToken, model string) *Provider {
    if model == "" {
        model = "gpt-4o"
    }
    return &Provider{
        OAuthToken: oauthToken,
        Model:      model,
        client:     &http.Client{Timeout: 30 * time.Second},
    }
}

func (p *Provider) getToken(ctx context.Context) (string, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    if p.cachedTok != "" && time.Now().Add(refreshBuffer).Before(p.expiresAt) {
        return p.cachedTok, nil
    }

    req, _ := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
    req.Header.Set("Authorization", "token "+p.OAuthToken)
    req.Header.Set("Accept", "application/json")
    for k, v := range identityHeaders {
        req.Header.Set(k, v)
    }

    resp, err := p.client.Do(req)
    if err != nil {
        return "", fmt.Errorf("token exchange failed: %w", err)
    }
    defer resp.Body.Close()

    var body struct {
        Token     string      `json:"token"`
        ExpiresAt json.Number `json:"expires_at"` // Unix int, not RFC3339
    }
    if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
        return "", fmt.Errorf("decode token response: %w", err)
    }
    if resp.StatusCode != 200 || body.Token == "" {
        return "", fmt.Errorf("token exchange HTTP %d", resp.StatusCode)
    }

    p.cachedTok = body.Token
    p.expiresAt = time.Now().Add(30 * time.Minute) // safe default
    if unix, err := body.ExpiresAt.Int64(); err == nil {
        p.expiresAt = time.Unix(unix, 0)
    }
    return p.cachedTok, nil
}

func (p *Provider) Complete(ctx context.Context, systemPrompt, userMessage string) (string, error) {
    tok, err := p.getToken(ctx)
    if err != nil {
        return "", err
    }

    messages := []map[string]string{}
    if systemPrompt != "" {
        messages = append(messages, map[string]string{"role": "system", "content": systemPrompt})
    }
    messages = append(messages, map[string]string{"role": "user", "content": userMessage})

    body, _ := json.Marshal(map[string]any{
        "model":       p.Model,
        "messages":    messages,
        "temperature": 0.2,
    })

    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, completionsURL, bytes.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+tok)
    for k, v := range identityHeaders {
        req.Header.Set(k, v)
    }

    resp, err := p.client.Do(req)
    if err != nil {
        return "", fmt.Errorf("completions request failed: %w", err)
    }
    defer resp.Body.Close()

    var apiResp struct {
        Choices []struct {
            Message struct{ Content string `json:"content"` } `json:"message"`
        } `json:"choices"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
        return "", fmt.Errorf("decode response: %w", err)
    }
    if len(apiResp.Choices) == 0 {
        return "", fmt.Errorf("no choices returned")
    }
    return apiResp.Choices[0].Message.Content, nil
}
```

---

## HTTP Proxy Support

If you need to route Copilot requests through a corporate proxy, configure it on the `http.Transport`:

```go
import "net/url"

proxyURL, _ := url.Parse("http://user:pass@proxy.corp:8080")
transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
```

Authenticated proxies work using credentials embedded in the proxy URL.

---

## Common Errors

| Error | Cause | Fix |
|---|---|---|
| `HTTP 401` on token exchange | Wrong or expired `gho_` token | Re-copy `oauth_token` from `apps.json` |
| `HTTP 400 missing editor-version` | Missing identity header | Add all four identity headers |
| `cannot unmarshal number into ...expires_at of type string` | Treating `expires_at` as string | Use `json.Number`, parse with `.Int64()` |
| `HTTP 403` on completions | Short-lived token expired / not refreshed | Implement expiry check with 60s buffer |
| `no choices returned` | Model not available on your plan | Try `gpt-4o` or `gpt-4o-mini` |
