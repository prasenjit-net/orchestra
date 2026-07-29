package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (h *Handler) ListAIModels(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	if provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required")
		return
	}
	catalog, err := h.workflow.ListAIModels(r.Context(), provider)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, catalog)
}

func (h *Handler) EnhancePrompt(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var body struct {
		Prompt   string `json:"prompt"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	enhanced, err := h.workflow.EnhancePrompt(r.Context(), body.Prompt, body.Provider, body.Model)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"prompt": enhanced})
}
