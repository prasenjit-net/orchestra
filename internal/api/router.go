package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/livebus"
	"github.com/prasenjit-net/orchestra/internal/version"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

func NewRouter(cfg config.Config, logger *slog.Logger, build version.Info, live *livebus.Bus, workflowService *workflow.Service) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Timeout(30 * time.Second))

	h := NewHandler(cfg, build, live, workflowService)
	r.Get("/health", h.Health)
	r.Get("/example", h.Example)
	r.Get("/meta", h.Meta)
	if live != nil {
		r.Get("/ws", h.WorkflowStream)
	}
	if workflowService != nil {
		r.Get("/scripts", h.ListScripts)
		r.Post("/scripts", h.CreateScript)
		r.Route("/scripts/{scriptID}", func(r chi.Router) {
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				h.GetScript(w, r, chi.URLParam(r, "scriptID"))
			})
			r.Put("/", func(w http.ResponseWriter, r *http.Request) {
				h.UpdateScript(w, r, chi.URLParam(r, "scriptID"))
			})
			r.Delete("/", func(w http.ResponseWriter, r *http.Request) {
				h.DeleteScript(w, r, chi.URLParam(r, "scriptID"))
			})
		})
		r.Get("/agents", h.ListAgents)
		r.Post("/agents", h.CreateAgent)
		r.Route("/agents/{agentID}", func(r chi.Router) {
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				h.GetAgent(w, r, chi.URLParam(r, "agentID"))
			})
			r.Put("/", func(w http.ResponseWriter, r *http.Request) {
				h.UpdateAgent(w, r, chi.URLParam(r, "agentID"))
			})
			r.Delete("/", func(w http.ResponseWriter, r *http.Request) {
				h.DeleteAgent(w, r, chi.URLParam(r, "agentID"))
			})
			r.Get("/mcp-servers", func(w http.ResponseWriter, r *http.Request) {
				h.GetAgentMCPServers(w, r, chi.URLParam(r, "agentID"))
			})
			r.Put("/mcp-servers", func(w http.ResponseWriter, r *http.Request) {
				h.SetAgentMCPServers(w, r, chi.URLParam(r, "agentID"))
			})
		})
		r.Get("/mcp-servers", h.ListMCPServers)
		r.Post("/mcp-servers", h.CreateMCPServer)
		r.Route("/mcp-servers/{serverID}", func(r chi.Router) {
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				h.GetMCPServer(w, r, chi.URLParam(r, "serverID"))
			})
			r.Put("/", func(w http.ResponseWriter, r *http.Request) {
				h.UpdateMCPServer(w, r, chi.URLParam(r, "serverID"))
			})
			r.Delete("/", func(w http.ResponseWriter, r *http.Request) {
				h.DeleteMCPServer(w, r, chi.URLParam(r, "serverID"))
			})
			r.Post("/explore", func(w http.ResponseWriter, r *http.Request) {
				h.ExploreMCPServer(w, r, chi.URLParam(r, "serverID"))
			})
		})
		r.Get("/workflows/activities", h.ListWorkflowActivities)
		r.Get("/workflows", h.ListWorkflows)
		r.Get("/workflows/events", h.ListWorkflowOperations)
		r.Get("/workflows/tasks", h.ListWorkflowTasks)
		r.Route("/workflows/tasks/{taskID}", func(r chi.Router) {
			r.Post("/retry", func(w http.ResponseWriter, r *http.Request) {
				taskID, err := parseTaskID(chi.URLParam(r, "taskID"))
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				h.RetryWorkflowTask(w, r, taskID)
			})
			r.Post("/requeue", func(w http.ResponseWriter, r *http.Request) {
				taskID, err := parseTaskID(chi.URLParam(r, "taskID"))
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				h.RequeueWorkflowTask(w, r, taskID)
			})
			r.Post("/pause", func(w http.ResponseWriter, r *http.Request) {
				taskID, err := parseTaskID(chi.URLParam(r, "taskID"))
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				h.PauseWorkflowTask(w, r, taskID)
			})
			r.Post("/resume", func(w http.ResponseWriter, r *http.Request) {
				taskID, err := parseTaskID(chi.URLParam(r, "taskID"))
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				h.ResumeWorkflowTask(w, r, taskID)
			})
			r.Post("/cancel", func(w http.ResponseWriter, r *http.Request) {
				taskID, err := parseTaskID(chi.URLParam(r, "taskID"))
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				h.CancelWorkflowTask(w, r, taskID)
			})
		})
		r.Get("/workflow-definitions", h.ListWorkflowDefinitions)
		r.Post("/workflow-definitions", h.CreateWorkflowDefinition)
		r.Route("/workflow-definitions/{definitionID}", func(r chi.Router) {
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				h.GetWorkflowDefinition(w, r, chi.URLParam(r, "definitionID"))
			})
			r.Post("/versions", func(w http.ResponseWriter, r *http.Request) {
				h.CreateWorkflowDefinitionVersion(w, r, chi.URLParam(r, "definitionID"))
			})
			r.Post("/start", func(w http.ResponseWriter, r *http.Request) {
				h.StartWorkflow(w, r, chi.URLParam(r, "definitionID"))
			})
			r.Post("/versions/{version}/publish", func(w http.ResponseWriter, r *http.Request) {
				version, err := parseVersion(chi.URLParam(r, "version"))
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				h.PublishWorkflowDefinitionVersion(w, r, chi.URLParam(r, "definitionID"), version)
			})
		})
		r.Route("/workflows/{workflowID}", func(r chi.Router) {
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				h.GetWorkflow(w, r, chi.URLParam(r, "workflowID"))
			})
			r.Get("/history", func(w http.ResponseWriter, r *http.Request) {
				h.GetWorkflowHistory(w, r, chi.URLParam(r, "workflowID"))
			})
			r.Post("/cancel", func(w http.ResponseWriter, r *http.Request) {
				h.CancelWorkflow(w, r, chi.URLParam(r, "workflowID"))
			})
			r.Post("/signals", func(w http.ResponseWriter, r *http.Request) {
				h.SignalWorkflow(w, r, chi.URLParam(r, "workflowID"))
			})
			r.Get("/replay", func(w http.ResponseWriter, r *http.Request) {
				h.ReplayWorkflow(w, r, chi.URLParam(r, "workflowID"))
			})
		})
	}
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		routes := []string{"/api/health", "/api/example", "/api/meta"}
		if live != nil {
			routes = append(routes, "/api/ws")
		}
		if workflowService != nil {
			routes = append(routes,
				"/api/workflows/activities",
				"/api/workflow-definitions",
				"/api/workflow-definitions/{definitionID}/versions",
				"/api/workflows",
				"/api/workflows/events",
				"/api/workflows/tasks",
				"/api/workflows/tasks/{taskID}/retry",
			)
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"service": cfg.App.Name,
			"message": "API ready",
			"routes":  routes,
		})
	})

	logger.Debug("api router initialized")
	return r
}
