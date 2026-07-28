package api

import (
	"net/http"
)

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, BuildHealthResponse(h.config, h.version))
}

func (h *Handler) Example(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, exampleResponse{
		Title:       "Orchestra workflow engine",
		Summary:     "Durable workflow orchestration with a Go backend and embedded React control plane.",
		Features:    []string{"Durable workflow runtime", "SQLite-backed state", "Chi API router", "Embedded SPA serving", "React Query + WebSocket live bus"},
		Quickstart:  []string{"make install-deps", "make dev-all", "make build", "./build/<binary> serve"},
		Repository:  "https://github.com/prasenjit-net/orchestra",
		FrontendDir: "ui",
	})
}

func (h *Handler) Meta(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, metaResponse{
		Name:           h.config.App.Name,
		Description:    h.config.App.Description,
		Environment:    h.config.App.Env,
		URL:            h.config.App.URL,
		UIProxy:        h.config.UI.DevProxyURL,
		Version:        h.version,
		ConfigEditable: h.configEditable,
	})
}

func (h *Handler) PublicMeta(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]any{
		"name":        h.config.App.Name,
		"description": h.config.App.Description,
		"version":     h.version.Version,
	})
}

func (h *Handler) ListWorkflowActivities(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"activities": h.workflow.ListActivities(),
	})
}
