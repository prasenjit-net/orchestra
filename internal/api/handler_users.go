package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/prasenjit-net/orchestra/internal/auth"
)

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	users, total, err := h.auth.ListUsers(r.Context(), principal, limit, offset, r.URL.Query().Get("search"))
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"users": users, "total": total, "limit": limit, "offset": offset})
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	var body struct {
		Username    string    `json:"username"`
		DisplayName string    `json:"displayName"`
		Role        auth.Role `json:"role"`
		Password    string    `json:"password,omitempty"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "REQUEST_INVALID", "invalid JSON body")
		return
	}
	created, err := h.auth.CreateUser(r.Context(), principal, auth.CreateManagedUserInput{
		Username: body.Username, DisplayName: body.DisplayName, Role: body.Role, Password: body.Password,
	})
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respondJSON(w, http.StatusCreated, created)
}

func (h *Handler) GetUser(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	user, err := h.auth.GetUser(r.Context(), principal, chi.URLParam(r, "userID"))
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	permissions := auth.EffectivePermissions(user.Role, user.Status, user.Entitlements).Slice()
	respondJSON(w, http.StatusOK, map[string]any{"user": user, "effectivePermissions": permissions})
}

func (h *Handler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	var body struct {
		Username    string    `json:"username"`
		DisplayName string    `json:"displayName"`
		Role        auth.Role `json:"role"`
		Status      string    `json:"status"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "REQUEST_INVALID", "invalid JSON body")
		return
	}
	user, err := h.auth.UpdateUser(r.Context(), principal, chi.URLParam(r, "userID"), auth.UpdateManagedUserInput{
		Username: body.Username, DisplayName: body.DisplayName, Role: body.Role, Status: body.Status,
	})
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, user)
}

func (h *Handler) ReplaceUserEntitlements(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	var body struct {
		Entitlements []auth.Entitlement `json:"entitlements"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "REQUEST_INVALID", "invalid JSON body")
		return
	}
	user, err := h.auth.ReplaceEntitlements(r.Context(), principal, chi.URLParam(r, "userID"), body.Entitlements)
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, user)
}

func (h *Handler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	var body struct {
		Password string `json:"password,omitempty"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &body); err != nil {
			writeAPIError(w, http.StatusBadRequest, "REQUEST_INVALID", "invalid JSON body")
			return
		}
	}
	temporary, err := h.auth.ResetPassword(r.Context(), principal, chi.URLParam(r, "userID"), body.Password)
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respondJSON(w, http.StatusOK, map[string]string{"temporaryPassword": temporary})
}

func (h *Handler) ListRoles(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"roles": []map[string]any{
			{"name": auth.RoleAdmin, "permissions": auth.PermissionsForRole(auth.RoleAdmin).Slice()},
			{"name": auth.RoleDeveloper, "permissions": auth.PermissionsForRole(auth.RoleDeveloper).Slice()},
			{"name": auth.RoleObserver, "permissions": auth.PermissionsForRole(auth.RoleObserver).Slice()},
		},
	})
}

func (h *Handler) ListPermissions(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{"permissions": auth.AllPermissions()})
}

func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	sessions, err := h.auth.ListSessions(r.Context(), principal, r.URL.Query().Get("userId"))
	if err != nil {
		writeAuthServiceError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFromRequest(r)
	if err := h.auth.RevokeSession(r.Context(), principal, chi.URLParam(r, "sessionID")); err != nil {
		writeAuthServiceError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}
