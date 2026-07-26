package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/prasenjit-net/orchestra/internal/webhooks"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

func (h *Handler) CreateWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	var input workflow.CreateDefinitionInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	definition, err := h.workflow.CreateDefinition(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, definition)
}

func (h *Handler) ListWorkflowDefinitions(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	definitions, err := h.workflow.ListDefinitions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"definitions": definitions})
}

func (h *Handler) GetWorkflowDefinition(w http.ResponseWriter, r *http.Request, definitionID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	definition, err := h.workflow.GetDefinition(r.Context(), definitionID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, definition)
}

func (h *Handler) GetWorkflowDefinitionVersion(w http.ResponseWriter, r *http.Request, definitionID string, version int) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	definition, err := h.workflow.GetDefinitionVersion(r.Context(), definitionID, version)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, definition)
}

func (h *Handler) CreateWorkflowDefinitionVersion(w http.ResponseWriter, r *http.Request, definitionID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	var input workflow.CreateDefinitionInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	definition, err := h.workflow.CreateDefinitionVersion(r.Context(), definitionID, input)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, definition)
}

func (h *Handler) PublishWorkflowDefinitionVersion(w http.ResponseWriter, r *http.Request, definitionID string, version int) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	var input struct {
		Activate bool `json:"activate"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	definition, err := h.workflow.PublishDefinitionVersion(r.Context(), definitionID, version, input.Activate)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, definition)
}

func (h *Handler) ActivateWorkflowDefinitionVersion(w http.ResponseWriter, r *http.Request, definitionID string, version int) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	definition, err := h.workflow.ActivateDefinitionVersion(r.Context(), definitionID, version)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, definition)
}

func (h *Handler) StartWorkflow(w http.ResponseWriter, r *http.Request, definitionID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	var body struct {
		Input       map[string]any `json:"input"`
		CallbackURL string         `json:"callbackUrl"`
		Version     int            `json:"version"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}

	if body.CallbackURL != "" {
		al, err := webhooks.NewCallbackAllowlist(h.config.Webhook.CallbackAllowlist)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "invalid callback allowlist configuration")
			return
		}
		if !al.Allows(body.CallbackURL) {
			writeError(w, http.StatusUnprocessableEntity, "callback URL is not in the allowed list")
			return
		}
	}

	instance, err := h.workflow.StartWorkflowWithInput(r.Context(), workflow.StartWorkflowInput{
		DefinitionID:      definitionID,
		DefinitionVersion: body.Version,
		Input:             body.Input,
		CallbackURL:       body.CallbackURL,
		TriggerSource:     "ui",
	})
	if err != nil {
		writeWorkflowError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, instance)
}
