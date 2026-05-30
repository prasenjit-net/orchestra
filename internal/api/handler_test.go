package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/livebus"
	"github.com/prasenjit-net/orchestra/internal/version"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

func TestHealthEndpoint(t *testing.T) {
	router := NewRouter(config.Default(), slog.New(slog.NewTextHandler(io.Discard, nil)), version.Current(), nil, nil, nil, false)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"status":"ok"`) {
		t.Fatalf("expected ok payload, got %s", res.Body.String())
	}
}

func TestWorkflowActivitiesEndpoint(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := workflow.NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	router := NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), version.Current(), livebus.New(), service, nil, false)
	req := httptest.NewRequest(http.MethodGet, "/workflows/activities", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"name":"log"`) {
		t.Fatalf("expected built-in log activity, got %s", res.Body.String())
	}
}

func TestWorkflowActivitiesEndpointIncludesScriptWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	cfg.Workflow.ScriptEnabled = true
	service, err := workflow.NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	router := NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), version.Current(), livebus.New(), service, nil, false)
	req := httptest.NewRequest(http.MethodGet, "/workflows/activities", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"name":"script"`) {
		t.Fatalf("expected script activity when enabled, got %s", res.Body.String())
	}
}

func TestCreateWorkflowDefinitionEndpoint(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := workflow.NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	router := NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), version.Current(), livebus.New(), service, nil, false)
	payload, err := json.Marshal(map[string]any{
		"name":        "Hello workflow",
		"description": "Logs once",
		"steps": []map[string]any{
			{
				"name":     "log-start",
				"activity": "log",
				"input": map[string]any{
					"message": "hello from test",
					"level":   "info",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/workflow-definitions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"activeVersion":1`) {
		t.Fatalf("expected active version in payload, got %s", res.Body.String())
	}
}

func TestListWorkflowDefinitionsEndpoint(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := workflow.NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	if _, err := service.CreateDefinition(context.Background(), workflow.CreateDefinitionInput{
		Name:        "Listable workflow",
		Description: "Shows up in definitions list",
		Steps: []workflow.StepDefinition{
			{Name: "log-start", Activity: "log", Input: []byte(`{"message":"hello list"}`)},
		},
	}); err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	router := NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), version.Current(), livebus.New(), service, nil, false)
	req := httptest.NewRequest(http.MethodGet, "/workflow-definitions", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"definitions":[`) {
		t.Fatalf("expected definitions list payload, got %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"name":"Listable workflow"`) {
		t.Fatalf("expected created definition in payload, got %s", res.Body.String())
	}
}

func TestRetryWorkflowTaskEndpoint(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := workflow.NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), workflow.CreateDefinitionInput{
		Name:        "Retry via API",
		Description: "Fails then retries",
		Steps: []workflow.StepDefinition{
			{
				Name:     "explode",
				Activity: "fail",
				Input:    []byte(`{"message":"boom from api"}`),
				Retry: workflow.RetryPolicy{
					MaxAttempts: 1,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}
	if _, err := service.StartWorkflow(context.Background(), definition.ID); err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}

	tasksResult, err := service.ListTasks(context.Background(), workflow.ListTasksInput{})
	if err != nil {
		t.Fatalf("ListTasks returned error: %v", err)
	}
	if len(tasksResult.Tasks) == 0 {
		t.Fatal("expected failed task to exist")
	}

	router := NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), version.Current(), livebus.New(), service, nil, false)
	req := httptest.NewRequest(http.MethodPost, "/workflows/tasks/"+strconv.FormatInt(tasksResult.Tasks[0].ID, 10)+"/retry", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"status":"pending"`) {
		t.Fatalf("expected pending task payload, got %s", res.Body.String())
	}
}

func TestWorkflowOperationsEndpoint(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := workflow.NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), workflow.CreateDefinitionInput{
		Name:        "Operations workflow",
		Description: "Emits audit events",
		Steps: []workflow.StepDefinition{
			{Name: "wait", Activity: "noop", Input: []byte(`{"note":"audit me"}`)},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	instance, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}

	tasksResult, err := service.ListTasks(context.Background(), workflow.ListTasksInput{})
	if err != nil {
		t.Fatalf("ListTasks returned error: %v", err)
	}
	if len(tasksResult.Tasks) == 0 {
		t.Fatal("expected task to exist")
	}

	if _, err := service.PauseTask(context.Background(), tasksResult.Tasks[0].ID); err != nil {
		t.Fatalf("PauseTask returned error: %v", err)
	}

	router := NewRouter(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), version.Current(), livebus.New(), service, nil, false)
	req := httptest.NewRequest(http.MethodGet, "/workflows/events?limit=2", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		Events []workflow.WorkflowEvent `json:"events"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if len(payload.Events) != 2 {
		t.Fatalf("expected 2 recent events, got %d", len(payload.Events))
	}
	if payload.Events[0].EventType != "TaskPaused" {
		t.Fatalf("expected most recent event TaskPaused, got %s", payload.Events[0].EventType)
	}
	if payload.Events[0].WorkflowID != instance.ID {
		t.Fatalf("expected workflow id %s, got %s", instance.ID, payload.Events[0].WorkflowID)
	}
}
