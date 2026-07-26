package api

import (
	"net/http"

	"github.com/prasenjit-net/orchestra/internal/workflow"
)

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

func (h *Handler) ListJSONSchemas(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	schemas, err := h.workflow.ListJSONSchemas(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, workflow.JSONSchemasResponse{Schemas: schemas})
}

func (h *Handler) CreateJSONSchema(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var input workflow.CreateJSONSchemaInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	schema, err := h.workflow.CreateJSONSchema(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, schema)
}

func (h *Handler) GetJSONSchema(w http.ResponseWriter, r *http.Request, schemaID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	schema, err := h.workflow.GetJSONSchema(r.Context(), schemaID)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, schema)
}

func (h *Handler) UpdateJSONSchema(w http.ResponseWriter, r *http.Request, schemaID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	var input workflow.CreateJSONSchemaInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	schema, err := h.workflow.UpdateJSONSchema(r.Context(), schemaID, input)
	if err != nil {
		writeWorkflowError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, schema)
}

func (h *Handler) DeleteJSONSchema(w http.ResponseWriter, r *http.Request, schemaID string) {
	if h.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "workflow service unavailable")
		return
	}
	if err := h.workflow.DeleteJSONSchema(r.Context(), schemaID); err != nil {
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
