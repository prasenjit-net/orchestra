package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/livebus"
	"github.com/prasenjit-net/orchestra/internal/version"
	"github.com/prasenjit-net/orchestra/internal/webhooks"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

type Handler struct {
	config         config.Config
	version        version.Info
	live           *livebus.Bus
	workflow       *workflow.Service
	restartCh      chan struct{}
	configEditable bool
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
	Name           string       `json:"name"`
	Description    string       `json:"description"`
	Environment    string       `json:"environment"`
	URL            string       `json:"url"`
	UIProxy        string       `json:"uiProxy"`
	Version        version.Info `json:"version"`
	ConfigEditable bool         `json:"configEditable"`
}

func NewHandler(cfg config.Config, build version.Info, live *livebus.Bus, workflowService *workflow.Service, restartCh chan struct{}, configEditable bool) *Handler {
	return &Handler{config: cfg, version: build, live: live, workflow: workflowService, restartCh: restartCh, configEditable: configEditable}
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
		Name:           h.config.App.Name,
		Description:    h.config.App.Description,
		Environment:    h.config.App.Env,
		URL:            h.config.App.URL,
		UIProxy:        h.config.UI.DevProxyURL,
		Version:        h.version,
		ConfigEditable: h.configEditable,
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

	var body struct {
		Input       map[string]any `json:"input"`
		CallbackURL string         `json:"callbackUrl"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}

	if body.CallbackURL != "" {
		al, err := webhooks.NewCallbackAllowlist(h.config.Webhook.CallbackAllowlist)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "invalid callback allowlist configuration")
			return
		}
		if !al.Allows(body.CallbackURL) {
			writeError(w, http.StatusUnprocessableEntity, "callback URL is not in the allowed list")
			return
		}
	}

	instance, err := h.workflow.StartWorkflowWithInput(r.Context(), workflow.StartWorkflowInput{
		DefinitionID:  definitionID,
		Input:         body.Input,
		CallbackURL:   body.CallbackURL,
		TriggerSource: "ui",
	})
	if err != nil {
		writeWorkflowError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, instance)
}

func (h *Handler) ListNodes(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	nodes, err := h.workflow.ListNodes(r.Context(), h.config.Node.Health.OfflineThreshold)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, nodes)
}

type nodeHealthResult struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	OK        bool   `json:"ok"`
	Status    int    `json:"status,omitempty"`
	LatencyMs int64  `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
	CheckedAt string `json:"checkedAt"`
}

func (h *Handler) CheckNodeHealth(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	nodes, err := h.workflow.ListNodes(r.Context(), h.config.Node.Health.OfflineThreshold)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	results := make([]nodeHealthResult, len(nodes))
	client := &http.Client{Timeout: 5 * time.Second}
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func(i int, id, address string) {
			defer wg.Done()
			res := nodeHealthResult{ID: id, Address: address, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
			if address == "" {
				res.Error = "no address registered"
				results[i] = res
				return
			}
			start := time.Now()
			resp, err := client.Get(address + "/livez")
			res.LatencyMs = time.Since(start).Milliseconds()
			if err != nil {
				res.Error = err.Error()
			} else {
				resp.Body.Close()
				res.Status = resp.StatusCode
				res.OK = resp.StatusCode == http.StatusOK
			}
			results[i] = res
		}(i, n.ID, n.Address)
	}
	wg.Wait()

	respondJSON(w, http.StatusOK, results)
}

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

func (h *Handler) Restart(w http.ResponseWriter, r *http.Request) {
	if h.restartCh == nil {
		writeError(w, http.StatusServiceUnavailable, "restart not supported in this mode")
		return
	}
	select {
	case h.restartCh <- struct{}{}:
		respondJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
	default:
		respondJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
	}
}

// sensitiveConfigEntries maps each sensitive config key to its redaction placeholder.
// The placeholder is intentionally key-specific so it cannot be confused with a real value.
var sensitiveConfigEntries = map[string]string{
	"openaiAPIKey":  "<openaiAPIKey>",
	"databaseURL":   "<databaseURL>",
	"openai_api_key": "<openaiAPIKey>",
	"database_url":  "<databaseURL>",
}

var sensitiveConfigKeys = regexp.MustCompile(
	`(?im)^(\s*(?:openaiAPIKey|databaseURL|openai_api_key|database_url)\s*=\s*)(".+"|'.+'|[^\s#]+)`)

func redactConfigSecrets(content string) string {
	return sensitiveConfigKeys.ReplaceAllStringFunc(content, func(line string) string {
		loc := sensitiveConfigKeys.FindStringSubmatchIndex(line)
		if len(loc) < 6 {
			return line
		}
		keyPart := strings.ToLower(line[loc[2]:loc[3]]) // normalise for lookup
		placeholder := "<secret>"
		for key, ph := range sensitiveConfigEntries {
			if strings.Contains(keyPart, strings.ToLower(key)) {
				placeholder = ph
				break
			}
		}
		return line[loc[2]:loc[3]] + `"` + placeholder + `"`
	})
}

// restoreRedactedSecrets replaces placeholder values in newContent with the
// real values from currentContent (the on-disk file). Values the user actually
// changed (i.e. not equal to the known placeholder) are kept as-is.
func restoreRedactedSecrets(newContent, currentContent string) string {
	for key, placeholder := range sensitiveConfigEntries {
		maskedRe := regexp.MustCompile(`(?im)^(\s*` + key + `\s*=\s*)"` + regexp.QuoteMeta(placeholder) + `"`)
		if !maskedRe.MatchString(newContent) {
			continue
		}
		realRe := regexp.MustCompile(`(?im)^\s*` + key + `\s*=\s*(".+"|'.+'|[^\s#\n]+)`)
		m := realRe.FindStringSubmatch(currentContent)
		if m == nil {
			continue // key absent or empty in on-disk file — preserve placeholder
		}
		realValue := m[1]
		newContent = maskedRe.ReplaceAllStringFunc(newContent, func(match string) string {
			loc := maskedRe.FindStringSubmatchIndex(match)
			if len(loc) < 4 {
				return match
			}
			return match[loc[2]:loc[3]] + realValue
		})
	}
	return newContent
}

func (h *Handler) GetConfigRaw(w http.ResponseWriter, r *http.Request) {
	path := h.config.ConfigFilePath
	if path == "" {
		writeError(w, http.StatusNotFound, "no config file in use (server started without a config file)")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("read config file: %s", err))
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{
		"path":    path,
		"content": redactConfigSecrets(string(data)),
	})
}

func (h *Handler) PutConfigRaw(w http.ResponseWriter, r *http.Request) {
	path := h.config.ConfigFilePath
	if path == "" {
		writeError(w, http.StatusNotFound, "no config file in use")
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Content == "" {
		writeError(w, http.StatusBadRequest, "content must not be empty")
		return
	}

	// Restore any "***" placeholders that the user left unchanged so we never
	// write the masked sentinel to disk and overwrite the real secret.
	content := body.Content
	if current, err := os.ReadFile(path); err == nil {
		content = restoreRedactedSecrets(content, string(current))
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("write config file: %s", err))
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"path": path, "status": "saved"})
}

func (h *Handler) EnhancePrompt(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	enhanced, err := h.workflow.EnhancePrompt(r.Context(), body.Prompt)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"prompt": enhanced})
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
