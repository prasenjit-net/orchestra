package api

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/prasenjit-net/orchestra/internal/auth"
	"github.com/prasenjit-net/orchestra/internal/config"
)

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if !validRequestOrigin(r, h.config) {
		writeAPIError(w, http.StatusForbidden, "AUTH_ORIGIN_INVALID", "request origin is not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "REQUEST_INVALID", invalidJSONBodyMessage)
		return
	}
	result, err := h.auth.Login(r.Context(), auth.LoginInput{
		Username: body.Username, Password: body.Password, SourceIP: r.RemoteAddr,
		UserAgent: r.UserAgent(), RequestID: middleware.GetReqID(r.Context()),
	})
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeAPIError(w, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS", "invalid username or password")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "AUTH_LOGIN_FAILED", "login failed")
		return
	}
	setSessionCookie(w, h, result.Token)
	preventCaching(w)
	respondJSON(w, http.StatusOK, buildSessionResponse(result.Principal, result.Session))
}

func (h *Handler) CurrentSession(w http.ResponseWriter, r *http.Request) {
	principal, err := principalFromRequest(r)
	if err != nil {
		writeAuthenticationRequired(w)
		return
	}
	_, session, err := h.auth.AuthenticateSession(r.Context(), mustSessionCookie(r))
	if err != nil {
		writeAuthenticationRequired(w)
		return
	}
	preventCaching(w)
	respondJSON(w, http.StatusOK, buildSessionResponse(principal, session))
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	principal, err := principalFromRequest(r)
	if err != nil {
		writeAuthenticationRequired(w)
		return
	}
	if err := h.auth.Logout(r.Context(), principal, middleware.GetReqID(r.Context()), r.RemoteAddr, r.UserAgent()); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "AUTH_LOGOUT_FAILED", "logout failed")
		return
	}
	clearSessionCookie(w, cookieSecure(h))
	preventCaching(w)
	respondJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	principal, err := principalFromRequest(r)
	if err != nil {
		writeAuthenticationRequired(w)
		return
	}
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "REQUEST_INVALID", invalidJSONBodyMessage)
		return
	}
	result, err := h.auth.ChangePassword(r.Context(), principal, body.CurrentPassword, body.NewPassword, r.RemoteAddr, r.UserAgent())
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeAPIError(w, http.StatusUnauthorized, "AUTH_INVALID_CREDENTIALS", "current password is incorrect")
			return
		}
		writeAuthServiceError(w, err)
		return
	}
	setSessionCookie(w, h, result.Token)
	preventCaching(w)
	respondJSON(w, http.StatusOK, buildSessionResponse(result.Principal, result.Session))
}

func buildSessionResponse(principal auth.Principal, session auth.Session) map[string]any {
	return map[string]any{
		"user":        principal.User,
		"permissions": principal.Permissions.Slice(),
		"csrfToken":   principal.CSRFToken,
		"session": map[string]any{
			"id": session.ID, "idleExpiresAt": session.IdleExpiresAt,
			"absoluteExpiresAt": session.AbsoluteExpiresAt,
		},
	}
}

func setSessionCookie(w http.ResponseWriter, h *Handler, token string) {
	secure := cookieSecure(h)
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode, // NOSONAR -- false is limited to explicitly configured local HTTP development.
	})
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1, // NOSONAR -- must match the explicitly configured local HTTP session cookie.
		Expires: time.Unix(1, 0),
	})
}

func mustSessionCookie(r *http.Request) string {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func cookieSecure(h *Handler) bool {
	return cookieSecureConfig(h.config)
}

func cookieSecureConfig(cfg config.Config) bool {
	switch strings.ToLower(cfg.Auth.CookieSecure) {
	case "true", "required":
		return true
	case "false", "disabled":
		return false
	default:
		parsed, err := url.Parse(cfg.App.URL)
		return err == nil && parsed.Scheme == "https"
	}
}
