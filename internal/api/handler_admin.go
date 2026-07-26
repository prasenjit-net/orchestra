package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
)

func (h *Handler) Restart(w http.ResponseWriter, r *http.Request) {
	if h.restartCh == nil {
		writeError(w, http.StatusServiceUnavailable, "restart not supported in this mode")
		return
	}
	select {
	case h.restartCh <- struct{}{}:
		respondJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
	default:
		respondJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
	}
}

// sensitiveConfigEntries maps each sensitive config key to its redaction placeholder.
// The placeholder is intentionally key-specific so it cannot be confused with a real value.
var sensitiveConfigEntries = map[string]string{
	"openaiAPIKey":        "<openaiAPIKey>",
	"claudeAPIKey":        "<claudeAPIKey>",
	"copilotOAuthToken":   "<copilotOAuthToken>",
	"databaseURL":         "<databaseURL>",
	"openai_api_key":      "<openaiAPIKey>",
	"claude_api_key":      "<claudeAPIKey>",
	"copilot_oauth_token": "<copilotOAuthToken>",
	"database_url":        "<databaseURL>",
}

var sensitiveConfigKeys = regexp.MustCompile(
	`(?im)^(\s*(?:openaiAPIKey|claudeAPIKey|copilotOAuthToken|databaseURL|openai_api_key|claude_api_key|copilot_oauth_token|database_url)\s*=\s*)(".+"|'.+'|[^\s#]+)`)

func redactConfigSecrets(content string) string {
	return sensitiveConfigKeys.ReplaceAllStringFunc(content, func(line string) string {
		loc := sensitiveConfigKeys.FindStringSubmatchIndex(line)
		if len(loc) < 6 {
			return line
		}
		keyPart := strings.ToLower(line[loc[2]:loc[3]]) // normalise for lookup
		placeholder := "<secret>"
		for key, ph := range sensitiveConfigEntries {
			if strings.Contains(keyPart, strings.ToLower(key)) {
				placeholder = ph
				break
			}
		}
		return line[loc[2]:loc[3]] + `"` + placeholder + `"`
	})
}

// restoreRedactedSecrets replaces placeholder values in newContent with the
// real values from currentContent (the on-disk file). Values the user actually
// changed (i.e. not equal to the known placeholder) are kept as-is.
func restoreRedactedSecrets(newContent, currentContent string) string {
	for key, placeholder := range sensitiveConfigEntries {
		maskedRe := regexp.MustCompile(`(?im)^(\s*` + key + `\s*=\s*)"` + regexp.QuoteMeta(placeholder) + `"`)
		if !maskedRe.MatchString(newContent) {
			continue
		}
		realRe := regexp.MustCompile(`(?im)^\s*` + key + `\s*=\s*(".+"|'.+'|[^\s#\n]+)`)
		m := realRe.FindStringSubmatch(currentContent)
		if m == nil {
			continue // key absent or empty in on-disk file — preserve placeholder
		}
		realValue := m[1]
		newContent = maskedRe.ReplaceAllStringFunc(newContent, func(match string) string {
			loc := maskedRe.FindStringSubmatchIndex(match)
			if len(loc) < 4 {
				return match
			}
			return match[loc[2]:loc[3]] + realValue
		})
	}
	return newContent
}

func (h *Handler) GetConfigRaw(w http.ResponseWriter, r *http.Request) {
	path := h.config.ConfigFilePath
	if path == "" {
		writeError(w, http.StatusNotFound, "no config file in use (server started without a config file)")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("read config file: %s", err))
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{
		"path":    path,
		"content": redactConfigSecrets(string(data)),
	})
}

func (h *Handler) PutConfigRaw(w http.ResponseWriter, r *http.Request) {
	path := h.config.ConfigFilePath
	if path == "" {
		writeError(w, http.StatusNotFound, "no config file in use")
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Content == "" {
		writeError(w, http.StatusBadRequest, "content must not be empty")
		return
	}

	// Restore any "***" placeholders that the user left unchanged so we never
	// write the masked sentinel to disk and overwrite the real secret.
	content := body.Content
	if current, err := os.ReadFile(path); err == nil {
		content = restoreRedactedSecrets(content, string(current))
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("write config file: %s", err))
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"path": path, "status": "saved"})
}
