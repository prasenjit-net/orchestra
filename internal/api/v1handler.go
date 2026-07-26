package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjit-net/orchestra/internal/auth"
	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/webhooks"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

type WebhookHandler struct {
	cfg       config.Config
	workflow  *workflow.Service
	allowlist *webhooks.CallbackAllowlist
	auth      *auth.Service
}

func NewWebhookHandler(cfg config.Config, workflowService *workflow.Service, authService *auth.Service) (*WebhookHandler, error) {
	al, err := webhooks.NewCallbackAllowlist(cfg.Webhook.CallbackAllowlist)
	if err != nil {
		return nil, err
	}
	return &WebhookHandler{cfg: cfg, workflow: workflowService, allowlist: al, auth: authService}, nil
}

// POST /ext/webhook/{definitionId}/start
func (h *WebhookHandler) StartWorkflow(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	definitionID := chi.URLParam(r, "definitionId")
	versionRaw := r.URL.Query().Get("version")
	if versionRaw == "" {
		versionRaw = r.Header.Get("X-Workflow-Version")
	}
	definitionVersion := 0
	if versionRaw != "" {
		var err error
		definitionVersion, err = parseVersion(versionRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

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
	principal, key, err := externalPrincipalFromRequest(r)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "AUTH_UNAUTHENTICATED", "valid API key required")
		return
	}
	if key != nil {
		if _, err := auth.AuthorizeAPIKey(*key, auth.APIKeyAuthorizationInput{
			DefinitionID: definitionID, Action: "start", PinnedVersion: definitionVersion > 0,
			HasCallbackURL: callbackURL != "",
		}); err != nil {
			h.auditExternal(r, principal, "authorization.denied", "workflow_definition", definitionID, "denied")
			writeAPIError(w, http.StatusForbidden, "AUTH_FORBIDDEN", "API key is not authorized for this workflow action")
			return
		}
	}

	instance, err := h.workflow.StartWorkflowWithInput(r.Context(), workflow.StartWorkflowInput{
		DefinitionID:         definitionID,
		DefinitionVersion:    definitionVersion,
		Input:                input,
		CallbackURL:          callbackURL,
		TriggerSource:        "webhook",
		TriggerPrincipalType: string(principal.Type),
		TriggerPrincipalID:   principal.ID,
	})
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	h.auditExternal(r, principal, "workflow.start", "workflow", instance.ID, "success")

	respondJSON(w, http.StatusCreated, map[string]any{
		"workflowId":        instance.ID,
		"status":            instance.Status,
		"definitionVersion": instance.DefinitionVersion,
		"resultUrl":         "/ext/result/" + instance.ID,
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
	instanceBefore, err := h.workflow.GetWorkflow(r.Context(), workflowID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	principal, key, err := externalPrincipalFromRequest(r)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "AUTH_UNAUTHENTICATED", "valid API key required")
		return
	}
	if key != nil {
		if _, err := auth.AuthorizeAPIKey(*key, auth.APIKeyAuthorizationInput{
			DefinitionID: instanceBefore.DefinitionID, Action: "signal", SignalName: body.Name,
			WorkflowTriggerType: instanceBefore.TriggerPrincipalType, WorkflowTriggerID: instanceBefore.TriggerPrincipalID,
		}); err != nil {
			h.auditExternal(r, principal, "authorization.denied", "workflow", workflowID, "denied")
			writeAPIError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "workflow not found")
			return
		}
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
	h.auditExternal(r, principal, "workflow.signal", "workflow", workflowID, "success")
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
	principal, key, authErr := externalPrincipalFromRequest(r)
	if authErr != nil {
		writeAPIError(w, http.StatusUnauthorized, "AUTH_UNAUTHENTICATED", "valid API key required")
		return
	}
	if key != nil {
		if _, err := auth.AuthorizeAPIKey(*key, auth.APIKeyAuthorizationInput{
			DefinitionID: instance.DefinitionID, Action: "status.read",
			WorkflowTriggerType: instance.TriggerPrincipalType, WorkflowTriggerID: instance.TriggerPrincipalID,
		}); err != nil {
			h.auditExternal(r, principal, "authorization.denied", "workflow", workflowID, "denied")
			writeAPIError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "workflow not found")
			return
		}
	}
	h.auditExternal(r, principal, "workflow.status_read", "workflow", workflowID, "success")
	respondJSON(w, http.StatusOK, map[string]any{
		"workflowId":      instance.ID,
		"status":          instance.Status,
		"pendingSignals":  instance.PendingSignals,
		"currentStep":     instance.CurrentStepName,
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
	principal, key, authErr := externalPrincipalFromRequest(r)
	if authErr != nil {
		writeAPIError(w, http.StatusUnauthorized, "AUTH_UNAUTHENTICATED", "valid API key required")
		return
	}
	if key != nil {
		if _, err := auth.AuthorizeAPIKey(*key, auth.APIKeyAuthorizationInput{
			DefinitionID: instance.DefinitionID, Action: "result.read",
			WorkflowTriggerType: instance.TriggerPrincipalType, WorkflowTriggerID: instance.TriggerPrincipalID,
		}); err != nil {
			h.auditExternal(r, principal, "authorization.denied", "workflow", workflowID, "denied")
			writeAPIError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "workflow not found")
			return
		}
	}
	h.auditExternal(r, principal, "workflow.result_read", "workflow", workflowID, "success")

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

func NewExtRouter(cfg config.Config, workflowService *workflow.Service, authService *auth.Service) (http.Handler, error) {
	h, err := NewWebhookHandler(cfg, workflowService, authService)
	if err != nil {
		return nil, err
	}

	r := chi.NewRouter()
	r.Use(externalAPIKeyAuthentication(cfg, authService))
	r.Post("/webhook/{definitionId}/start", h.StartWorkflow)
	r.Post("/webhook/{workflowId}/signal", h.SendSignal)
	r.Get("/signal/{workflowId}", h.ListSignals)
	r.Get("/result/{workflowId}", h.GetResult)
	return r, nil
}

type externalKeyContextKey struct{}

func externalAPIKeyAuthentication(cfg config.Config, identity *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := strings.TrimSpace(r.Header.Get("Authorization"))
			if header == "" && cfg.Webhook.AuthenticationMode == "audit" {
				principal := auth.Principal{Type: auth.PrincipalAnonymous, Permissions: auth.PermissionSet{}}
				next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
				return
			}
			if identity == nil || !strings.HasPrefix(header, "Bearer ") || strings.Contains(header[7:], " ") {
				w.Header().Set("WWW-Authenticate", `Bearer realm="orchestra-webhooks"`)
				auditExternalAuthenticationFailure(r, identity, "invalid_or_missing_key")
				writeAPIError(w, http.StatusUnauthorized, "AUTH_UNAUTHENTICATED", "valid API key required")
				return
			}
			principal, key, err := identity.AuthenticateAPIKey(r.Context(), strings.TrimSpace(header[7:]), r.RemoteAddr)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="orchestra-webhooks"`)
				auditExternalAuthenticationFailure(r, identity, "invalid_key")
				status := http.StatusUnauthorized
				code := "AUTH_UNAUTHENTICATED"
				message := "valid API key required"
				if errors.Is(err, auth.ErrForbidden) {
					status = http.StatusTooManyRequests
					code = "RATE_LIMITED"
					message = "API key rate limit exceeded"
				}
				writeAPIError(w, status, code, message)
				return
			}
			ctx := auth.WithPrincipal(r.Context(), principal)
			ctx = context.WithValue(ctx, externalKeyContextKey{}, key)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func auditExternalAuthenticationFailure(r *http.Request, identity *auth.Service, reason string) {
	if identity == nil {
		return
	}
	metadata, _ := json.Marshal(map[string]string{"reason": reason})
	_ = identity.Audit(r.Context(), auth.AuditEvent{
		ActorType: auth.PrincipalAnonymous, Action: "auth.api_key", Outcome: "denied",
		SourceIP: r.RemoteAddr, UserAgent: r.UserAgent(), Metadata: metadata,
	})
}

func externalPrincipalFromRequest(r *http.Request) (auth.Principal, *auth.APIKey, error) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		return auth.Principal{}, nil, auth.ErrInvalidCredentials
	}
	if principal.Type == auth.PrincipalAnonymous {
		return principal, nil, nil
	}
	key, ok := r.Context().Value(externalKeyContextKey{}).(auth.APIKey)
	if !ok {
		return auth.Principal{}, nil, auth.ErrInvalidCredentials
	}
	return principal, &key, nil
}

func (h *WebhookHandler) auditExternal(r *http.Request, principal auth.Principal, action, resourceType, resourceID, outcome string) {
	if h.auth == nil {
		return
	}
	_ = h.auth.Audit(r.Context(), auth.AuditEvent{
		ActorType: principal.Type, ActorID: principal.ID, Action: action,
		ResourceType: resourceType, ResourceID: resourceID, Outcome: outcome,
		SourceIP: r.RemoteAddr, UserAgent: r.UserAgent(),
	})
}
