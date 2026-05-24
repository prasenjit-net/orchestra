package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/livebus"
	"github.com/prasenjit-net/orchestra/internal/version"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

type Handler struct {
	config   config.Config
	version  version.Info
	live     *livebus.Bus
	workflow *workflow.Service
}

type HealthResponse struct {
	Status    string       `json:"status"`
	Service   string       `json:"service"`
	Env       string       `json:"env"`
	Time      time.Time    `json:"time"`
	Version   version.Info `json:"version"`
	Documents []string     `json:"documents"`
}

type exampleResponse struct {
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	Features    []string `json:"features"`
	Quickstart  []string `json:"quickstart"`
	Repository  string   `json:"repository"`
	FrontendDir string   `json:"frontendDir"`
}

type metaResponse struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Environment string       `json:"environment"`
	URL         string       `json:"url"`
	UIProxy     string       `json:"uiProxy"`
	Version     version.Info `json:"version"`
}

func NewHandler(cfg config.Config, build version.Info, live *livebus.Bus, workflowService *workflow.Service) *Handler {
	return &Handler{config: cfg, version: build, live: live, workflow: workflowService}
}

func BuildHealthResponse(cfg config.Config, build version.Info) HealthResponse {
	return HealthResponse{
		Status:  "ok",
		Service: cfg.App.Name,
		Env:     cfg.App.Env,
		Time:    time.Now().UTC(),
		Version: build,
		Documents: []string{
			"README.md",
			"config.yaml",
			"ui/src/pages",
		},
	}
}

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
		Name:        h.config.App.Name,
		Description: h.config.App.Description,
		Environment: h.config.App.Env,
		URL:         h.config.App.URL,
		UIProxy:     h.config.UI.DevProxyURL,
		Version:     h.version,
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

func (h *Handler) WorkflowStream(w http.ResponseWriter, r *http.Request) {
	if h.live == nil {
		writeError(w, http.StatusServiceUnavailable, "live bus unavailable")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	readCtx, readCancel := context.WithCancel(r.Context())
	defer readCancel()
	go func() {
		_ = conn.CloseRead(readCtx)
	}()

	events, unsubscribe := h.live.Subscribe()
	defer unsubscribe()

	if err := wsjson.Write(r.Context(), conn, livebus.NewEvent("connection.ready", "connection", "app-live-bus", map[string]any{
		"status": "connected",
	})); err != nil {
		return
	}

	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			_ = conn.Close(websocket.StatusNormalClosure, "client disconnected")
			return
		case event, ok := <-events:
			if !ok {
				_ = conn.Close(websocket.StatusGoingAway, "workflow stream closed")
				return
			}
			if err := wsjson.Write(r.Context(), conn, event); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := wsjson.Write(r.Context(), conn, livebus.NewEvent("heartbeat", "connection", "", nil)); err != nil {
				return
			}
		}
	}
}

func (h *Handler) CreateWorkflowDefinition(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	var input workflow.CreateDefinitionInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	definition, err := h.workflow.CreateDefinition(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, definition)
}

func (h *Handler) ListWorkflowDefinitions(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	definitions, err := h.workflow.ListDefinitions(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"definitions": definitions})
}

func (h *Handler) GetWorkflowDefinition(w http.ResponseWriter, r *http.Request, definitionID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	definition, err := h.workflow.GetDefinition(r.Context(), definitionID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, definition)
}

func (h *Handler) CreateWorkflowDefinitionVersion(w http.ResponseWriter, r *http.Request, definitionID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	var input workflow.CreateDefinitionInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	definition, err := h.workflow.CreateDefinitionVersion(r.Context(), definitionID, input)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, definition)
}

func (h *Handler) PublishWorkflowDefinitionVersion(w http.ResponseWriter, r *http.Request, definitionID string, version int) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	definition, err := h.workflow.PublishDefinitionVersion(r.Context(), definitionID, version)
	if err != nil {
		if errors.Is(err, workflow.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, definition)
}

func (h *Handler) StartWorkflow(w http.ResponseWriter, r *http.Request, definitionID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	instance, err := h.workflow.StartWorkflow(r.Context(), definitionID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, instance)
}

func (h *Handler) ListWorkflows(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	input := workflow.ListWorkflowsInput{Status: r.URL.Query().Get("status")}
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
		"workflows": result.Workflows,
		"total":     result.Total,
		"limit":     input.Limit,
		"offset":    input.Offset,
	})
}

func (h *Handler) ListWorkflowOperations(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}

	events, err := h.workflow.ListRecentEvents(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"events": events})
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

	events, err := h.workflow.GetWorkflowHistory(r.Context(), workflowID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"events": events})
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

func (h *Handler) ReplayWorkflow(w http.ResponseWriter, r *http.Request, workflowID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	replay, err := h.workflow.ReplayWorkflow(r.Context(), workflowID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, replay)
}

func (h *Handler) ListWorkflowTasks(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}

	input := workflow.ListTasksInput{}
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

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func writeWorkflowError(w http.ResponseWriter, err error) {
	if errors.Is(err, workflow.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
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

func (h *Handler) ListScripts(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	scripts, err := h.workflow.ListScripts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, workflow.ScriptsResponse{Scripts: scripts})
}

func (h *Handler) CreateScript(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var input workflow.CreateScriptInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	script, err := h.workflow.CreateScript(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, script)
}

func (h *Handler) GetScript(w http.ResponseWriter, r *http.Request, scriptID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	script, err := h.workflow.GetScript(r.Context(), scriptID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, script)
}

func (h *Handler) UpdateScript(w http.ResponseWriter, r *http.Request, scriptID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var input workflow.CreateScriptInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	script, err := h.workflow.UpdateScript(r.Context(), scriptID, input)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, script)
}

func (h *Handler) DeleteScript(w http.ResponseWriter, r *http.Request, scriptID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	if err := h.workflow.DeleteScript(r.Context(), scriptID); err != nil {
		writeWorkflowError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListAgents(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	agents, err := h.workflow.ListAgents(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, workflow.AgentsResponse{Agents: agents})
}

func (h *Handler) CreateAgent(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var input workflow.CreateAgentInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	agent, err := h.workflow.CreateAgent(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, agent)
}

func (h *Handler) GetAgent(w http.ResponseWriter, r *http.Request, agentID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	agent, err := h.workflow.GetAgent(r.Context(), agentID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, agent)
}

func (h *Handler) UpdateAgent(w http.ResponseWriter, r *http.Request, agentID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var input workflow.CreateAgentInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	agent, err := h.workflow.UpdateAgent(r.Context(), agentID, input)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, agent)
}

func (h *Handler) DeleteAgent(w http.ResponseWriter, r *http.Request, agentID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	if err := h.workflow.DeleteAgent(r.Context(), agentID); err != nil {
		writeWorkflowError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetAgentMCPServers(w http.ResponseWriter, r *http.Request, agentID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	servers, err := h.workflow.GetAgentMCPServers(r.Context(), agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, workflow.MCPServersResponse{Servers: servers})
}

func (h *Handler) SetAgentMCPServers(w http.ResponseWriter, r *http.Request, agentID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var input workflow.SetAgentMCPServersInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.workflow.SetAgentMCPServers(r.Context(), agentID, input.ServerIDs); err != nil {
		writeWorkflowError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListMCPServers(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	servers, err := h.workflow.ListMCPServers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, workflow.MCPServersResponse{Servers: servers})
}

func (h *Handler) CreateMCPServer(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var input workflow.CreateMCPServerInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	srv, err := h.workflow.CreateMCPServer(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, srv)
}

func (h *Handler) GetMCPServer(w http.ResponseWriter, r *http.Request, serverID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	srv, err := h.workflow.GetMCPServer(r.Context(), serverID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, srv)
}

func (h *Handler) UpdateMCPServer(w http.ResponseWriter, r *http.Request, serverID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var input workflow.CreateMCPServerInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	srv, err := h.workflow.UpdateMCPServer(r.Context(), serverID, input)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, srv)
}

func (h *Handler) DeleteMCPServer(w http.ResponseWriter, r *http.Request, serverID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	if err := h.workflow.DeleteMCPServer(r.Context(), serverID); err != nil {
		writeWorkflowError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ExploreMCPServer(w http.ResponseWriter, r *http.Request, serverID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	srv, err := h.workflow.ExploreMCPServer(r.Context(), serverID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, srv)
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
