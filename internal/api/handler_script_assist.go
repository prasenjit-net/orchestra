package api

import (
	"encoding/json"
	"net/http"

	"github.com/prasenjit-net/orchestra/internal/workflow"
)

func (h *Handler) ScriptAssist(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	var body struct {
		Messages      []workflow.ScriptChatMessage `json:"messages"`
		CurrentScript string                       `json:"currentScript"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	content, err := h.workflow.ScriptAssist(r.Context(), body.Messages, body.CurrentScript)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"content": content})
}

func (h *Handler) ValidateScript(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	var body struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result := h.workflow.ValidateScript(body.Source)
	respondJSON(w, http.StatusOK, result)
}
