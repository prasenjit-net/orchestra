package api

import (
	"time"

	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/livebus"
	"github.com/prasenjit-net/orchestra/internal/version"
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
