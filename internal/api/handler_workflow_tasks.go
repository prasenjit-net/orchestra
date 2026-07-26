package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/prasenjit-net/orchestra/internal/workflow"
)

func (h *Handler) ListWorkflowTasks(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	input := workflow.ListTasksInput{
		Status:           r.URL.Query().Get("status"),
		ExcludeCompleted: r.URL.Query().Get("excludeCompleted") == "true",
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

	result, err := h.workflow.ListTasks(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"tasks":  result.Tasks,
		"total":  result.Total,
		"limit":  input.Limit,
		"offset": input.Offset,
		"counts": result.Counts,
	})
}

func (h *Handler) RetryWorkflowTask(w http.ResponseWriter, r *http.Request, taskID int64) {
	h.applyTaskAction(w, r, taskID, h.workflow.RetryTask)
}

func (h *Handler) RequeueWorkflowTask(w http.ResponseWriter, r *http.Request, taskID int64) {
	h.applyTaskAction(w, r, taskID, h.workflow.RequeueTask)
}

func (h *Handler) PauseWorkflowTask(w http.ResponseWriter, r *http.Request, taskID int64) {
	h.applyTaskAction(w, r, taskID, h.workflow.PauseTask)
}

func (h *Handler) ResumeWorkflowTask(w http.ResponseWriter, r *http.Request, taskID int64) {
	h.applyTaskAction(w, r, taskID, h.workflow.ResumeTask)
}

func (h *Handler) CancelWorkflowTask(w http.ResponseWriter, r *http.Request, taskID int64) {
	h.applyTaskAction(w, r, taskID, h.workflow.CancelTask)
}

func (h *Handler) applyTaskAction(w http.ResponseWriter, r *http.Request, taskID int64, action func(context.Context, int64) (workflow.WorkflowTask, error)) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	task, err := action(r.Context(), taskID)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, task)
}
