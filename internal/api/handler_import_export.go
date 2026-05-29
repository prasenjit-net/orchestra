package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

var nonAlphanumRE = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func exportFilename(name, bundleType string) string {
	slug := strings.Trim(nonAlphanumRE.ReplaceAllString(name, "-"), "-")
	if slug == "" {
		slug = bundleType
	}
	return fmt.Sprintf("%s.orchestra.json", strings.ToLower(slug))
}

func writeBundle(w http.ResponseWriter, bundle workflow.ImportBundle) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, exportFilename(bundleName(bundle), bundle.BundleType)))
	_ = json.NewEncoder(w).Encode(bundle)
}

func bundleName(b workflow.ImportBundle) string {
	if b.Definition != nil {
		return b.Definition.Name
	}
	if len(b.Agents) > 0 {
		return b.Agents[0].Name
	}
	if len(b.Scripts) > 0 {
		return b.Scripts[0].Name
	}
	if len(b.Connectors) > 0 {
		return b.Connectors[0].Name
	}
	return b.BundleType
}

func (h *Handler) ExportWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	id := chi.URLParam(r, "definitionID")
	bundle, err := h.workflow.ExportWorkflow(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeBundle(w, bundle)
}

func (h *Handler) ExportAgent(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	bundle, err := h.workflow.ExportAgent(r.Context(), chi.URLParam(r, "agentID"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeBundle(w, bundle)
}

func (h *Handler) ExportScript(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	bundle, err := h.workflow.ExportScript(r.Context(), chi.URLParam(r, "scriptID"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeBundle(w, bundle)
}

func (h *Handler) ExportConnector(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	bundle, err := h.workflow.ExportConnector(r.Context(), chi.URLParam(r, "serverID"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeBundle(w, bundle)
}

func (h *Handler) AnalyzeImport(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var bundle workflow.ImportBundle
	if err := json.NewDecoder(r.Body).Decode(&bundle); err != nil {
		writeError(w, http.StatusBadRequest, "invalid bundle JSON")
		return
	}
	analysis, err := h.workflow.AnalyzeImport(r.Context(), bundle)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, analysis)
}

func (h *Handler) ApplyImport(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var body struct {
		Bundle      workflow.ImportBundle `json:"bundle"`
		OverrideIDs []string              `json:"overrideIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	imported, err := h.workflow.ApplyImport(r.Context(), body.Bundle, body.OverrideIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"imported": imported})
}
