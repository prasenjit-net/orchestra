package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/prasenjit-net/orchestra/internal/auth"
	"github.com/prasenjit-net/orchestra/internal/config"
)

func authenticateSession(identity *auth.Service, cfg config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(auth.SessionCookieName)
			if err != nil || cookie.Value == "" {
				writeAPIError(w, http.StatusUnauthorized, "AUTH_UNAUTHENTICATED", "authentication required")
				return
			}
			principal, _, err := identity.AuthenticateSession(r.Context(), cookie.Value)
			if err != nil {
				clearSessionCookie(w, cookieSecureConfig(cfg))
				writeAPIError(w, http.StatusUnauthorized, "AUTH_UNAUTHENTICATED", "authentication required")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), principal)))
		})
	}
}

func (h *Handler) requirePermission(permission auth.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := auth.PrincipalFromContext(r.Context())
			if !ok {
				writeAPIError(w, http.StatusUnauthorized, "AUTH_UNAUTHENTICATED", "authentication required")
				return
			}
			if principal.User != nil && principal.User.MustChangePassword {
				h.auditAuthorizationDenied(r, principal, permission, "password_change_required")
				writeAPIError(w, http.StatusForbidden, "AUTH_PASSWORD_CHANGE_REQUIRED", "password change required")
				return
			}
			if !principal.Has(permission) {
				h.auditAuthorizationDenied(r, principal, permission, "permission_missing")
				writeAPIError(w, http.StatusForbidden, "AUTH_FORBIDDEN", "permission denied")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (h *Handler) auditAuthorizationDenied(r *http.Request, principal auth.Principal, permission auth.Permission, reason string) {
	if h.auth == nil {
		return
	}
	metadata, _ := json.Marshal(map[string]string{
		"permission": string(permission), "reason": reason, "method": r.Method, "path": r.URL.Path,
	})
	_ = h.auth.Audit(r.Context(), auth.AuditEvent{
		RequestID: middleware.GetReqID(r.Context()), ActorType: principal.Type, ActorID: principal.ID,
		Action: "authorization.denied", Outcome: "denied", SourceIP: r.RemoteAddr,
		UserAgent: r.UserAgent(), Metadata: metadata,
	})
}

func requireCSRF(cfg config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isUnsafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			principal, ok := auth.PrincipalFromContext(r.Context())
			if !ok || principal.CSRFToken == "" {
				writeAPIError(w, http.StatusForbidden, "AUTH_CSRF_INVALID", "request verification failed")
				return
			}
			provided := r.Header.Get("X-CSRF-Token")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(principal.CSRFToken)) != 1 || !validRequestOrigin(r, cfg) {
				writeAPIError(w, http.StatusForbidden, "AUTH_CSRF_INVALID", "request verification failed")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func auditAuthenticatedRequests(identity *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if identity == nil || !isUnsafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r)
			principal, ok := auth.PrincipalFromContext(r.Context())
			if !ok {
				return
			}
			outcome := "success"
			if wrapped.Status() >= 400 {
				outcome = "failure"
			}
			if wrapped.Status() == http.StatusUnauthorized || wrapped.Status() == http.StatusForbidden {
				outcome = "denied"
			}
			metadata, _ := json.Marshal(map[string]any{"method": r.Method, "path": r.URL.Path, "status": wrapped.Status()})
			_ = identity.Audit(r.Context(), auth.AuditEvent{
				RequestID: middleware.GetReqID(r.Context()), ActorType: principal.Type, ActorID: principal.ID,
				Action: "api.request", ResourceType: "http_route", ResourceID: r.URL.Path,
				Outcome: outcome, SourceIP: r.RemoteAddr, UserAgent: r.UserAgent(), Metadata: metadata,
			})
		})
	}
}

func validRequestOrigin(r *http.Request, cfg config.Config) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
			parsed, err := url.Parse(referer)
			if err != nil {
				return false
			}
			origin = parsed.Scheme + "://" + parsed.Host
		}
	}
	if origin == "" {
		return false
	}
	allowed := []string{cfg.App.URL}
	if strings.EqualFold(cfg.App.Env, "development") && cfg.UI.DevProxyURL != "" {
		allowed = append(allowed, cfg.UI.DevProxyURL)
	}
	for _, candidate := range allowed {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSuffix(origin, "/"), parsed.Scheme+"://"+parsed.Host) {
			return true
		}
	}
	return false
}

func principalFromRequest(r *http.Request) (auth.Principal, error) {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		return auth.Principal{}, errors.New("principal missing")
	}
	return principal, nil
}
