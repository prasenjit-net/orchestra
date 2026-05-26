package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/webhooks"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

type WebhookHandler struct {
	cfg       config.Config
	workflow  *workflow.Service
	allowlist *webhooks.CallbackAllowlist
}

func NewWebhookHandler(cfg config.Config, workflowService *workflow.Service) (*WebhookHandler, error) {
	al, err := webhooks.NewCallbackAllowlist(cfg.Webhook.CallbackAllowlist)
	if err != nil {
		return nil, err
	}
	return &WebhookHandler{cfg: cfg, workflow: workflowService, allowlist: al}, nil
}

// POST /ext/webhook/{definitionId}/start
func (h *WebhookHandler) StartWorkflow(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	definitionID := chi.URLParam(r, "definitionId")

	callbackURL := r.Header.Get("X-Callback-URL")
	if callbackURL != "" && !h.allowlist.Allows(callbackURL) {
		writeError(w, http.StatusUnprocessableEntity, "callback URL is not in the allowed list")
		return
	}

	var input map[string]any
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}

	instance, err := h.workflow.StartWorkflowWithInput(r.Context(), workflow.StartWorkflowInput{
		DefinitionID:  definitionID,
		Input:         input,
		CallbackURL:   callbackURL,
		TriggerSource: "webhook",
	})
	if err != nil {
		writeWorkflowError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{
		"workflowId": instance.ID,
		"status":     instance.Status,
		"resultUrl":  "/ext/result/" + instance.ID,
	})
}

// POST /ext/webhook/{workflowId}/signal
func (h *WebhookHandler) SendSignal(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	workflowID := chi.URLParam(r, "workflowId")

	var body struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "signal name is required")
		return
	}

	instance, err := h.workflow.SignalWorkflow(r.Context(), workflowID, workflow.SignalWorkflowInput{
		Name:    body.Name,
		Payload: body.Payload,
	})
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			writeError(w, http.StatusNotFound, "workflow not found")
			return
		}
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"workflowId": instance.ID,
		"status":     instance.Status,
	})
}

// GET /ext/signal/{workflowId}
func (h *WebhookHandler) ListSignals(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	workflowID := chi.URLParam(r, "workflowId")

	instance, err := h.workflow.GetWorkflow(r.Context(), workflowID)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			writeError(w, http.StatusNotFound, "workflow not found")
			return
		}
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"workflowId":     instance.ID,
		"status":         instance.Status,
		"pendingSignals": instance.PendingSignals,
		"currentStep":    instance.CurrentStepName,
		"currentActivity": instance.CurrentActivity,
	})
}

// GET /ext/result/{workflowId}
func (h *WebhookHandler) GetResult(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	workflowID := chi.URLParam(r, "workflowId")

	instance, err := h.workflow.GetWorkflow(r.Context(), workflowID)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			writeError(w, http.StatusNotFound, "workflow not found")
			return
		}
		writeWorkflowError(w, err)
		return
	}

	if instance.Status != "completed" && instance.Status != "failed" && instance.Status != "canceled" {
		respondJSON(w, http.StatusAccepted, map[string]any{
			"workflowId": instance.ID,
			"status":     instance.Status,
			"message":    "workflow not yet completed",
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"workflowId":  instance.ID,
		"status":      instance.Status,
		"output":      instance.LastOutput,
		"context":     instance.Context,
		"completedAt": instance.UpdatedAt,
	})
}

func NewExtRouter(cfg config.Config, workflowService *workflow.Service) (http.Handler, error) {
	h, err := NewWebhookHandler(cfg, workflowService)
	if err != nil {
		return nil, err
	}

	r := chi.NewRouter()
	r.Post("/webhook/{definitionId}/start", h.StartWorkflow)
	r.Post("/webhook/{workflowId}/signal", h.SendSignal)
	r.Get("/signal/{workflowId}", h.ListSignals)
	r.Get("/result/{workflowId}", h.GetResult)
	return r, nil
}
