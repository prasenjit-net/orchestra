package workflow

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/prasenjit-net/orchestra/internal/config"
)

func TestWorkflowCompletesBuiltInActivities(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Hello workflow",
		Description: "Runs log and noop",
		Steps: []StepDefinition{
			{
				Name:     "log-start",
				Activity: "log",
				Input:    []byte(`{"message":"hello workflow","level":"info"}`),
			},
			{
				Name:     "noop-finish",
				Activity: "noop",
				Input:    []byte(`{"done":true}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	instance, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}

	for i := 0; i < 4; i++ {
		processed, err := service.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce returned error: %v", err)
		}
		if !processed {
			break
		}
	}

	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	if instance.Status != StatusCompleted {
		t.Fatalf("expected completed workflow, got %s", instance.Status)
	}

	events, err := service.GetWorkflowHistory(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflowHistory returned error: %v", err)
	}
	if len(events) < 5 {
		t.Fatalf("expected event history, got %d events", len(events))
	}
	if events[len(events)-1].EventType != "WorkflowCompleted" {
		t.Fatalf("expected final event WorkflowCompleted, got %s", events[len(events)-1].EventType)
	}
}

func TestWorkflowFailsAfterFailActivity(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Failing workflow",
		Description: "Fails once",
		Steps: []StepDefinition{
			{
				Name:     "explode",
				Activity: "fail",
				Input:    []byte(`{"message":"boom"}`),
				Retry: RetryPolicy{
					MaxAttempts: 1,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	instance, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}

	processed, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected a task to be processed")
	}

	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	if instance.Status != StatusFailed {
		t.Fatalf("expected failed workflow, got %s", instance.Status)
	}
	if instance.LastError == "" {
		t.Fatal("expected workflow last error to be set")
	}
}

func TestPauseAndResumeTask(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Pauseable workflow",
		Description: "Pauses and resumes",
		Steps: []StepDefinition{
			{Name: "wait", Activity: "noop", Input: []byte(`{"note":"pause me"}`)},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	instance, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}

	tasks, err := service.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks returned error: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("expected at least one task")
	}

	task, err := service.PauseTask(context.Background(), tasks[0].ID)
	if err != nil {
		t.Fatalf("PauseTask returned error: %v", err)
	}
	if task.Status != StatusPaused {
		t.Fatalf("expected paused task, got %s", task.Status)
	}

	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	if instance.Status != StatusPaused {
		t.Fatalf("expected paused workflow, got %s", instance.Status)
	}

	task, err = service.ResumeTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("ResumeTask returned error: %v", err)
	}
	if task.Status != StatusPending {
		t.Fatalf("expected pending task after resume, got %s", task.Status)
	}
}

func TestRetryFailedTask(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Retry workflow",
		Description: "Retries a failed task manually",
		Steps: []StepDefinition{
			{
				Name:     "explode",
				Activity: "fail",
				Input:    []byte(`{"message":"boom-again"}`),
				Retry: RetryPolicy{
					MaxAttempts: 1,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	_, err = service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}

	tasks, err := service.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks returned error: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("expected failed task")
	}

	task, err := service.RetryTask(context.Background(), tasks[0].ID)
	if err != nil {
		t.Fatalf("RetryTask returned error: %v", err)
	}
	if task.Status != StatusPending {
		t.Fatalf("expected pending task after retry, got %s", task.Status)
	}
	if task.Attempts != 0 {
		t.Fatalf("expected attempts reset to 0, got %d", task.Attempts)
	}
}

func TestDelayActivityWaitsDurably(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	delayUntil := time.Now().UTC().Add(50 * time.Millisecond)
	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Delayed workflow",
		Description: "Waits and then continues",
		Steps: []StepDefinition{
			{
				Name:     "wait-a-bit",
				Activity: "delay",
				Input:    []byte(`{"until":"` + delayUntil.Format(time.RFC3339Nano) + `"}`),
			},
			{
				Name:     "finish",
				Activity: "noop",
				Input:    []byte(`{"ok":true}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	instance, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}

	processed, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected first delayed task to process")
	}

	tasks, err := service.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks returned error: %v", err)
	}
	if len(tasks) == 0 || tasks[0].Status != StatusPending {
		t.Fatalf("expected delayed task to remain pending, got %+v", tasks)
	}

	time.Sleep(70 * time.Millisecond)

	processed, err = service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce second pass returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected delayed step to resume")
	}

	processed, err = service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce third pass returned error: %v", err)
	}
	if !processed {
		t.Fatal("expected noop step to process")
	}

	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	if instance.Status != StatusCompleted {
		t.Fatalf("expected completed workflow after delay, got %s", instance.Status)
	}

	events, err := service.GetWorkflowHistory(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflowHistory returned error: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected events for delayed workflow")
	}
	hasCompleted := false
	hasWaiting := false
	for _, event := range events {
		if event.EventType == "ActivityWaiting" {
			hasWaiting = true
		}
		if event.EventType == "ActivityCompleted" {
			hasCompleted = true
		}
	}
	if !hasWaiting {
		t.Fatal("expected ActivityWaiting event for delayed workflow")
	}
	if !hasCompleted {
		t.Fatal("expected ActivityCompleted event after delay")
	}
}

func TestHTTPRequestActivitySucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("X-Workflow-Test") != "ok" {
			t.Fatalf("expected test header to be present")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll returned error: %v", err)
		}
		if string(body) != `{"hello":"world"}` {
			t.Fatalf("unexpected request body: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"received":true}`))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "HTTP workflow",
		Description: "Calls an HTTP endpoint",
		Steps: []StepDefinition{
			{
				Name:     "call-api",
				Activity: "http-request",
				Input: []byte(`{
					"method":"POST",
					"url":"` + server.URL + `",
					"headers":{"X-Workflow-Test":"ok"},
					"body":{"hello":"world"},
					"expectedStatus":201
				}`),
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	instance, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}

	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	if instance.Status != StatusCompleted {
		t.Fatalf("expected completed workflow, got %s", instance.Status)
	}

	events, err := service.GetWorkflowHistory(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflowHistory returned error: %v", err)
	}
	var completedPayload map[string]any
	for _, event := range events {
		if event.EventType == "ActivityCompleted" {
			if err := json.Unmarshal(event.Payload, &completedPayload); err != nil {
				t.Fatalf("Unmarshal returned error: %v", err)
			}
		}
	}
	output, ok := completedPayload["output"].(map[string]any)
	if !ok {
		t.Fatalf("expected activity output payload, got %#v", completedPayload["output"])
	}
	if int(output["statusCode"].(float64)) != http.StatusCreated {
		t.Fatalf("expected status code 201, got %#v", output["statusCode"])
	}
}

func TestHTTPRequestActivityFailsOnUnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream failed"))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "HTTP failure workflow",
		Description: "Fails on non-2xx",
		Steps: []StepDefinition{
			{
				Name:     "call-api",
				Activity: "http-request",
				Input:    []byte(`{"method":"GET","url":"` + server.URL + `"}`),
				Retry: RetryPolicy{
					MaxAttempts: 1,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	instance, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}
	if _, err := service.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce returned error: %v", err)
	}

	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	if instance.Status != StatusFailed {
		t.Fatalf("expected failed workflow, got %s", instance.Status)
	}
	if instance.LastError == "" {
		t.Fatal("expected last error for failed http workflow")
	}
}

func TestDefinitionVersioningPublishesNewActiveVersion(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Versioned workflow",
		Description: "Tracks version lifecycle",
		Steps: []StepDefinition{
			{Name: "step-v1", Activity: "noop", Input: []byte(`{"version":1}`)},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}
	if definition.ActiveVersion != 1 || definition.LatestVersion != 1 || definition.DraftVersion != 0 {
		t.Fatalf("unexpected initial definition summary: %+v", definition.DefinitionSummary)
	}

	drafted, err := service.CreateDefinitionVersion(context.Background(), definition.ID, CreateDefinitionInput{
		Name:        "Versioned workflow",
		Description: "Tracks version lifecycle",
		Steps: []StepDefinition{
			{Name: "step-v2", Activity: "noop", Input: []byte(`{"version":2}`)},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinitionVersion returned error: %v", err)
	}
	if drafted.ActiveVersion != 1 {
		t.Fatalf("expected active version to remain 1 before publish, got %d", drafted.ActiveVersion)
	}
	if drafted.DraftVersion != 2 || drafted.LatestVersion != 2 {
		t.Fatalf("expected draft/latest version to be 2, got %+v", drafted.DefinitionSummary)
	}

	beforePublish, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow before publish returned error: %v", err)
	}
	if beforePublish.DefinitionVersion != 1 {
		t.Fatalf("expected workflow to pin version 1 before publish, got %d", beforePublish.DefinitionVersion)
	}

	published, err := service.PublishDefinitionVersion(context.Background(), definition.ID, 2)
	if err != nil {
		t.Fatalf("PublishDefinitionVersion returned error: %v", err)
	}
	if published.ActiveVersion != 2 || published.DraftVersion != 0 {
		t.Fatalf("expected active version 2 and no draft after publish, got %+v", published.DefinitionSummary)
	}

	afterPublish, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow after publish returned error: %v", err)
	}
	if afterPublish.DefinitionVersion != 2 {
		t.Fatalf("expected workflow to pin version 2 after publish, got %d", afterPublish.DefinitionVersion)
	}

	reloaded, err := service.GetDefinition(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("GetDefinition returned error: %v", err)
	}
	if len(reloaded.Versions) != 2 {
		t.Fatalf("expected two definition versions, got %d", len(reloaded.Versions))
	}
	if reloaded.Versions[0].Version != 2 || reloaded.Versions[0].Status != "published" {
		t.Fatalf("expected latest version to be published v2, got %+v", reloaded.Versions[0])
	}
	if reloaded.Versions[1].Version != 1 || reloaded.Versions[1].Status != "archived" {
		t.Fatalf("expected original version to be archived, got %+v", reloaded.Versions[1])
	}
}

func TestDefinitionPersistsStepLayout(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Layout workflow",
		Description: "Persists step positions",
		Steps: []StepDefinition{
			{
				Name:     "http-step",
				Activity: "http-request",
				Input:    []byte(`{"url":"https://example.com"}`),
				Layout: StepLayout{
					X: 420,
					Y: 180,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	reloaded, err := service.GetDefinition(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("GetDefinition returned error: %v", err)
	}
	if len(reloaded.Document.Steps) != 1 {
		t.Fatalf("expected one step, got %d", len(reloaded.Document.Steps))
	}
	if reloaded.Document.Steps[0].Layout.X != 420 || reloaded.Document.Steps[0].Layout.Y != 180 {
		t.Fatalf("expected layout to persist, got %+v", reloaded.Document.Steps[0].Layout)
	}
}

func TestLiveEventsEmitOnWorkflowStart(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	events, unsubscribe := service.SubscribeLiveEvents()
	defer unsubscribe()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Live event workflow",
		Description: "Emits workflow.started",
		Steps: []StepDefinition{
			{Name: "noop-step", Activity: "noop", Input: []byte(`{"ok":true}`)},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	instance, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}

	timeout := time.After(2 * time.Second)
	var foundStarted bool
	for !foundStarted {
		select {
		case event := <-events:
			if event.Type == "workflow.started" && event.EntityID == instance.ID {
				foundStarted = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for workflow.started live event")
		}
	}
}
