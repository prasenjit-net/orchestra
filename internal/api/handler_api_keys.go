package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjit-net/orchestra/internal/auth"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

func (h *Handler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	keys, total, err := h.auth.ListAPIKeys(r.Context(), principal, limit, offset)
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"apiKeys": keys, "total": total})
}

func (h *Handler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	var body auth.CreateAPIKeyRequest
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "REQUEST_INVALID", "invalid JSON body")
		return
	}
	if err := h.validateAPIKeyWorkflowGrants(r, body.Grants); err != nil {
		writeWorkflowError(w, err)
		return
	}
	created, err := h.auth.CreateAPIKey(r.Context(), principal, body)
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respondJSON(w, http.StatusCreated, created)
}

func (h *Handler) GetAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	key, err := h.auth.GetAPIKey(r.Context(), principal, chi.URLParam(r, "keyID"))
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, key)
}

func (h *Handler) UpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	var body auth.CreateAPIKeyRequest
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "REQUEST_INVALID", "invalid JSON body")
		return
	}
	if err := h.validateAPIKeyWorkflowGrants(r, body.Grants); err != nil {
		writeWorkflowError(w, err)
		return
	}
	key, err := h.auth.UpdateAPIKey(r.Context(), principal, chi.URLParam(r, "keyID"), body)
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, key)
}

func (h *Handler) RotateAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	rotated, err := h.auth.RotateAPIKey(r.Context(), principal, chi.URLParam(r, "keyID"))
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respondJSON(w, http.StatusOK, rotated)
}

func (h *Handler) RevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	if err := h.auth.RevokeAPIKey(r.Context(), principal, chi.URLParam(r, "keyID")); err != nil {
		writeAuthServiceError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (h *Handler) validateAPIKeyWorkflowGrants(r *http.Request, grants []auth.APIKeyGrant) error {
	if h.workflow == nil {
		return errors.New("workflow service unavailable")
	}
	seen := make(map[string]struct{})
	for _, grant := range grants {
		if _, ok := seen[grant.WorkflowDefinitionID]; ok {
			continue
		}
		if _, err := h.workflow.GetDefinition(r.Context(), grant.WorkflowDefinitionID); err != nil {
			if errors.Is(err, workflow.ErrNotFound) {
				return workflow.ErrNotFound
			}
			return err
		}
		seen[grant.WorkflowDefinitionID] = struct{}{}
	}
	return nil
}

func (h *Handler) ListAuditEvents(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	events, total, err := h.auth.ListAuditEvents(r.Context(), principal, auth.ListAuditInput{
		Limit: limit, Offset: offset, ActorID: r.URL.Query().Get("actorId"),
		Action: r.URL.Query().Get("action"), Outcome: r.URL.Query().Get("outcome"),
	})
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"events": events, "total": total})
}
