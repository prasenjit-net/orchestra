package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/prasenjit-net/orchestra/internal/workflow"
)

func (h *Handler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	input := workflow.ListWorkflowsInput{Status: r.URL.Query().Get("status")}
	if raw := r.URL.Query().Get("currentActivities"); raw != "" {
		input.CurrentActivities = strings.Split(raw, ",")
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > 500 {
			n = 500
		}
		input.Limit = n
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		input.Offset = n
	}

	result, err := h.workflow.ListWorkflows(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"workflows":      result.Workflows,
		"total":          result.Total,
		"limit":          input.Limit,
		"offset":         input.Offset,
		"activityCounts": result.ActivityCounts,
	})
}

func (h *Handler) ListWorkflowOperations(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	input := workflow.ListRecentEventsInput{Limit: 50}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		input.Limit = parsed
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		input.Offset = parsed
	}

	result, err := h.workflow.ListRecentEvents(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"events": result.Events,
		"total":  result.Total,
		"limit":  input.Limit,
		"offset": input.Offset,
	})
}

func (h *Handler) GetWorkflow(w http.ResponseWriter, r *http.Request, workflowID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	instance, err := h.workflow.GetWorkflow(r.Context(), workflowID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, instance)
}

func (h *Handler) GetWorkflowHistory(w http.ResponseWriter, r *http.Request, workflowID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	result, err := h.workflow.GetWorkflowHistory(r.Context(), workflowID, workflow.WorkflowHistoryInput{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		writeWorkflowError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) CancelWorkflow(w http.ResponseWriter, r *http.Request, workflowID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	instance, err := h.workflow.CancelWorkflow(r.Context(), workflowID)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, instance)
}

func (h *Handler) SignalWorkflow(w http.ResponseWriter, r *http.Request, workflowID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	var input workflow.SignalWorkflowInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	instance, err := h.workflow.SignalWorkflow(r.Context(), workflowID, input)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, instance)
}
