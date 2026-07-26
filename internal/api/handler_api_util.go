package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/prasenjit-net/orchestra/internal/auth"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	respondJSON(w, status, map[string]string{"error": message, "code": code})
}

func writeAuthServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "AUTH_NOT_FOUND", "resource not found")
	case errors.Is(err, auth.ErrForbidden):
		writeAPIError(w, http.StatusForbidden, "AUTH_FORBIDDEN", "permission denied")
	case errors.Is(err, auth.ErrConflict):
		writeAPIError(w, http.StatusConflict, "AUTH_CONFLICT", err.Error())
	default:
		writeAPIError(w, http.StatusBadRequest, "REQUEST_INVALID", err.Error())
	}
}

func writeWorkflowError(w http.ResponseWriter, err error) {
	if errors.Is(err, workflow.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if errors.Is(err, workflow.ErrVersionNotPublished) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("JSON body must contain a single value")
	}
	return nil
}

func parseTaskID(raw string) (int64, error) {
	taskID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, errors.New("invalid task id")
	}
	return taskID, nil
}

func parseVersion(raw string) (int, error) {
	version, err := strconv.Atoi(raw)
	if err != nil || version <= 0 {
		return 0, errors.New("invalid version")
	}
	return version, nil
}
