package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/prasenjit-net/orchestra/internal/auth"
	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/livebus"
	"github.com/prasenjit-net/orchestra/internal/version"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

const (
	exportRoute     = "/export"
	mcpServersRoute = "/mcp-servers"
)

type RouterOptions struct {
	Live           *livebus.Bus
	Workflow       *workflow.Service
	Auth           *auth.Service
	RestartCh      chan struct{}
	ConfigEditable bool
}

func NewRouter(cfg config.Config, logger *slog.Logger, build version.Info, options RouterOptions) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Timeout(30 * time.Second))

	h := NewHandler(cfg, build, options.Live, options.Workflow, options.Auth, options.RestartCh, options.ConfigEditable)

	// Explicit public surface.
	r.Get("/health", h.Health)
	r.Get("/meta/public", h.PublicMeta)
	r.Post("/auth/login", h.Login)

	r.Group(func(r chi.Router) {
		r.Use(authenticateSession(options.Auth, cfg))
		r.Use(requireCSRF(cfg))
		r.Use(auditAuthenticatedRequests(options.Auth))

		// Account lifecycle routes remain available while a temporary password is active.
		r.Get("/auth/session", h.CurrentSession)
		r.Post("/auth/logout", h.Logout)
		r.Post("/auth/change-password", h.ChangePassword)
		r.With(h.requirePermission(auth.PermissionSessionManageOwn)).Get("/auth/sessions", h.ListSessions)
		r.With(h.requirePermission(auth.PermissionSessionManageOwn)).Delete("/auth/sessions/{sessionID}", h.RevokeSession)

		r.With(h.requirePermission(auth.PermissionUserRead)).Get("/roles", h.ListRoles)
		r.With(h.requirePermission(auth.PermissionUserRead)).Get("/permissions", h.ListPermissions)
		r.With(h.requirePermission(auth.PermissionUserRead)).Get("/users", h.ListUsers)
		r.With(h.requirePermission(auth.PermissionUserManage)).Post("/users", h.CreateUser)
		r.Route("/users/{userID}", func(r chi.Router) {
			r.With(h.requirePermission(auth.PermissionUserRead)).Get("/", h.GetUser)
			r.With(h.requirePermission(auth.PermissionUserManage)).Patch("/", h.UpdateUser)
			r.With(h.requirePermission(auth.PermissionUserManage)).Post("/reset-password", h.ResetUserPassword)
			r.With(h.requirePermission(auth.PermissionEntitlementManage)).Put("/entitlements", h.ReplaceUserEntitlements)
		})
		r.With(h.requirePermission(auth.PermissionAuditRead)).Get("/audit-events", h.ListAuditEvents)

		r.With(h.requirePermission(auth.PermissionAPIKeyRead)).Get("/api-keys", h.ListAPIKeys)
		r.With(h.requirePermission(auth.PermissionAPIKeyCreate)).Post("/api-keys", h.CreateAPIKey)
		r.Route("/api-keys/{keyID}", func(r chi.Router) {
			r.With(h.requirePermission(auth.PermissionAPIKeyRead)).Get("/", h.GetAPIKey)
			r.With(h.requirePermission(auth.PermissionAPIKeyManageOwn)).Patch("/", h.UpdateAPIKey)
			r.With(h.requirePermission(auth.PermissionAPIKeyManageOwn)).Post("/rotate", h.RotateAPIKey)
			r.With(h.requirePermission(auth.PermissionAPIKeyManageOwn)).Post("/revoke", h.RevokeAPIKey)
		})

		r.With(h.requirePermission(auth.PermissionSettingsRead)).Get("/example", h.Example)
		r.With(h.requirePermission(auth.PermissionSettingsRead)).Get("/meta", h.Meta)
		if options.ConfigEditable {
			r.With(h.requirePermission(auth.PermissionSettingsRead)).Get("/config/raw", h.GetConfigRaw)
			r.With(h.requirePermission(auth.PermissionSettingsWrite)).Put("/config/raw", h.PutConfigRaw)
		}
		r.With(h.requirePermission(auth.PermissionServerRestart)).Post("/admin/restart", h.Restart)
		if options.Live != nil {
			r.With(h.requirePermission(auth.PermissionOperationRead)).Get("/ws", h.WorkflowStream)
		}

		if options.Workflow != nil {
			mountWorkflowRoutes(r, h)
		}

		r.With(h.requirePermission(auth.PermissionSettingsRead)).Get("/", func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, http.StatusOK, map[string]any{
				"service": cfg.App.Name,
				"message": "API ready",
			})
		})
	})

	logger.Debug("api router initialized")
	return r
}

func mountWorkflowRoutes(r chi.Router, h *Handler) {
	r.With(h.requirePermission(auth.PermissionClusterRead)).Get("/nodes", h.ListNodes)
	r.With(h.requirePermission(auth.PermissionClusterControl)).Post("/nodes/healthcheck", h.CheckNodeHealth)

	r.With(h.requirePermission(auth.PermissionResourceRead)).Get("/scripts", h.ListScripts)
	r.With(h.requirePermission(auth.PermissionResourceWrite)).Post("/scripts", h.CreateScript)
	r.Route("/scripts/{scriptID}", func(r chi.Router) {
		r.With(h.requirePermission(auth.PermissionResourceRead)).Get("/", func(w http.ResponseWriter, r *http.Request) {
			h.GetScript(w, r, chi.URLParam(r, "scriptID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceWrite)).Put("/", func(w http.ResponseWriter, r *http.Request) {
			h.UpdateScript(w, r, chi.URLParam(r, "scriptID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceWrite)).Delete("/", func(w http.ResponseWriter, r *http.Request) {
			h.DeleteScript(w, r, chi.URLParam(r, "scriptID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceRead)).Get(exportRoute, h.ExportScript)
	})

	r.With(h.requirePermission(auth.PermissionResourceRead)).Get("/json-schemas", h.ListJSONSchemas)
	r.With(h.requirePermission(auth.PermissionResourceWrite)).Post("/json-schemas", h.CreateJSONSchema)
	r.With(h.requirePermission(auth.PermissionResourceRead)).Get("/json-schemas/export", h.ExportJSONSchemas)
	r.Route("/json-schemas/{schemaID}", func(r chi.Router) {
		r.With(h.requirePermission(auth.PermissionResourceRead)).Get("/", func(w http.ResponseWriter, r *http.Request) {
			h.GetJSONSchema(w, r, chi.URLParam(r, "schemaID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceWrite)).Put("/", func(w http.ResponseWriter, r *http.Request) {
			h.UpdateJSONSchema(w, r, chi.URLParam(r, "schemaID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceWrite)).Delete("/", func(w http.ResponseWriter, r *http.Request) {
			h.DeleteJSONSchema(w, r, chi.URLParam(r, "schemaID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceRead)).Get(exportRoute, h.ExportJSONSchema)
	})

	r.With(h.requirePermission(auth.PermissionResourceRead)).Get("/agents", h.ListAgents)
	r.With(h.requirePermission(auth.PermissionResourceWrite)).Post("/agents", h.CreateAgent)
	r.Route("/agents/{agentID}", func(r chi.Router) {
		r.With(h.requirePermission(auth.PermissionResourceRead)).Get("/", func(w http.ResponseWriter, r *http.Request) {
			h.GetAgent(w, r, chi.URLParam(r, "agentID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceWrite)).Put("/", func(w http.ResponseWriter, r *http.Request) {
			h.UpdateAgent(w, r, chi.URLParam(r, "agentID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceWrite)).Delete("/", func(w http.ResponseWriter, r *http.Request) {
			h.DeleteAgent(w, r, chi.URLParam(r, "agentID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceRead)).Get(mcpServersRoute, func(w http.ResponseWriter, r *http.Request) {
			h.GetAgentMCPServers(w, r, chi.URLParam(r, "agentID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceWrite)).Put(mcpServersRoute, func(w http.ResponseWriter, r *http.Request) {
			h.SetAgentMCPServers(w, r, chi.URLParam(r, "agentID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceRead)).Get(exportRoute, h.ExportAgent)
	})

	r.With(h.requirePermission(auth.PermissionResourceRead)).Get(mcpServersRoute, h.ListMCPServers)
	r.With(h.requirePermission(auth.PermissionResourceWrite)).Post(mcpServersRoute, h.CreateMCPServer)
	r.Route("/mcp-servers/{serverID}", func(r chi.Router) {
		r.With(h.requirePermission(auth.PermissionResourceRead)).Get("/", func(w http.ResponseWriter, r *http.Request) {
			h.GetMCPServer(w, r, chi.URLParam(r, "serverID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceWrite)).Put("/", func(w http.ResponseWriter, r *http.Request) {
			h.UpdateMCPServer(w, r, chi.URLParam(r, "serverID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceWrite)).Delete("/", func(w http.ResponseWriter, r *http.Request) {
			h.DeleteMCPServer(w, r, chi.URLParam(r, "serverID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceWrite)).Post("/explore", func(w http.ResponseWriter, r *http.Request) {
			h.ExploreMCPServer(w, r, chi.URLParam(r, "serverID"))
		})
		r.With(h.requirePermission(auth.PermissionResourceRead)).Get(exportRoute, h.ExportConnector)
	})

	r.With(h.requirePermission(auth.PermissionAIUse)).Post("/ai/enhance-prompt", h.EnhancePrompt)
	r.With(h.requirePermission(auth.PermissionAIUse)).Post("/ai/script-assist", h.ScriptAssist)
	r.With(h.requirePermission(auth.PermissionAIUse)).Post("/ai/validate-script", h.ValidateScript)

	r.With(h.requirePermission(auth.PermissionWorkflowDefinitionRead)).Get("/workflows/activities", h.ListWorkflowActivities)
	r.With(h.requirePermission(auth.PermissionWorkflowRunRead)).Get("/workflows", h.ListWorkflows)
	r.With(h.requirePermission(auth.PermissionOperationRead)).Get("/workflows/events", h.ListWorkflowOperations)
	r.With(h.requirePermission(auth.PermissionWorkflowTaskRead)).Get("/workflows/tasks", h.ListWorkflowTasks)
	r.Route("/workflows/tasks/{taskID}", func(r chi.Router) {
		taskAction := func(action func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				taskID, err := parseTaskID(chi.URLParam(r, "taskID"))
				if err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				action(w, r, taskID)
			}
		}
		r.With(h.requirePermission(auth.PermissionWorkflowTaskControl)).Post("/retry", taskAction(h.RetryWorkflowTask))
		r.With(h.requirePermission(auth.PermissionWorkflowTaskControl)).Post("/requeue", taskAction(h.RequeueWorkflowTask))
		r.With(h.requirePermission(auth.PermissionWorkflowTaskControl)).Post("/pause", taskAction(h.PauseWorkflowTask))
		r.With(h.requirePermission(auth.PermissionWorkflowTaskControl)).Post("/resume", taskAction(h.ResumeWorkflowTask))
		r.With(h.requirePermission(auth.PermissionWorkflowTaskControl)).Post("/cancel", taskAction(h.CancelWorkflowTask))
	})

	r.With(h.requirePermission(auth.PermissionWorkflowDefinitionRead)).Get("/workflow-definitions", h.ListWorkflowDefinitions)
	r.With(h.requirePermission(auth.PermissionWorkflowDefinitionWrite)).Post("/workflow-definitions", h.CreateWorkflowDefinition)
	r.Route("/workflow-definitions/{definitionID}", func(r chi.Router) {
		r.With(h.requirePermission(auth.PermissionWorkflowDefinitionRead)).Get("/", func(w http.ResponseWriter, r *http.Request) {
			h.GetWorkflowDefinition(w, r, chi.URLParam(r, "definitionID"))
		})
		r.With(h.requirePermission(auth.PermissionWorkflowDefinitionWrite)).Post("/versions", func(w http.ResponseWriter, r *http.Request) {
			h.CreateWorkflowDefinitionVersion(w, r, chi.URLParam(r, "definitionID"))
		})
		r.With(h.requirePermission(auth.PermissionWorkflowRunStart)).Post("/start", func(w http.ResponseWriter, r *http.Request) {
			h.StartWorkflow(w, r, chi.URLParam(r, "definitionID"))
		})
		r.With(h.requirePermission(auth.PermissionWorkflowDefinitionRead)).Get("/versions/{version}", func(w http.ResponseWriter, r *http.Request) {
			version, err := parseVersion(chi.URLParam(r, "version"))
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			h.GetWorkflowDefinitionVersion(w, r, chi.URLParam(r, "definitionID"), version)
		})
		r.With(h.requirePermission(auth.PermissionWorkflowDefinitionPublish)).Post("/versions/{version}/publish", func(w http.ResponseWriter, r *http.Request) {
			version, err := parseVersion(chi.URLParam(r, "version"))
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			h.PublishWorkflowDefinitionVersion(w, r, chi.URLParam(r, "definitionID"), version)
		})
		r.With(h.requirePermission(auth.PermissionWorkflowDefinitionPublish)).Post("/versions/{version}/activate", func(w http.ResponseWriter, r *http.Request) {
			version, err := parseVersion(chi.URLParam(r, "version"))
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			h.ActivateWorkflowDefinitionVersion(w, r, chi.URLParam(r, "definitionID"), version)
		})
		r.With(h.requirePermission(auth.PermissionWorkflowDefinitionRead)).Get(exportRoute, h.ExportWorkflowDefinition)
	})

	r.With(h.requirePermission(auth.PermissionImportAnalyze)).Post("/import/analyze", h.AnalyzeImport)
	r.With(h.requirePermission(auth.PermissionImportApply)).Post("/import/apply", h.ApplyImport)
	r.Route("/workflows/{workflowID}", func(r chi.Router) {
		r.With(h.requirePermission(auth.PermissionWorkflowRunRead)).Get("/", func(w http.ResponseWriter, r *http.Request) {
			h.GetWorkflow(w, r, chi.URLParam(r, "workflowID"))
		})
		r.With(h.requirePermission(auth.PermissionWorkflowRunRead)).Get("/history", func(w http.ResponseWriter, r *http.Request) {
			h.GetWorkflowHistory(w, r, chi.URLParam(r, "workflowID"))
		})
		r.With(h.requirePermission(auth.PermissionWorkflowRunControl)).Post("/cancel", func(w http.ResponseWriter, r *http.Request) {
			h.CancelWorkflow(w, r, chi.URLParam(r, "workflowID"))
		})
		r.With(h.requirePermission(auth.PermissionWorkflowRunControl)).Post("/signals", func(w http.ResponseWriter, r *http.Request) {
			h.SignalWorkflow(w, r, chi.URLParam(r, "workflowID"))
		})
	})
}
