package workflow

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestWorkflowBranchesUsingTransitionConditions(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Branching workflow",
		Description: "Routes to different steps based on output context",
		Steps: []StepDefinition{
			{
				Name:     "decision",
				Activity: "noop",
				Input:    []byte(`{"approved":true}`),
				Transitions: []StepTransition{
					{
						To:    "approved",
						Label: "approved-route",
						Condition: &TransitionCondition{
							Path:     "steps.decision.approved",
							Operator: "eq",
							Value:    []byte(`true`),
						},
					},
					{To: "rejected", Label: "default-route"},
				},
			},
			{
				Name:     "approved",
				Activity: "noop",
				Input:    []byte(`{"status":"approved"}`),
				Transitions: []StepTransition{
					{To: "done"},
				},
			},
			{
				Name:     "rejected",
				Activity: "noop",
				Input:    []byte(`{"status":"rejected"}`),
				Transitions: []StepTransition{
					{To: "done"},
				},
			},
			{
				Name:     "done",
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

	for i := 0; i < 4; i++ {
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce returned error: %v", err)
		}
	}

	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	if instance.Status != StatusCompleted {
		t.Fatalf("expected completed workflow, got %s", instance.Status)
	}

	var contextPayload map[string]any
	if err := json.Unmarshal(instance.Context, &contextPayload); err != nil {
		t.Fatalf("Unmarshal workflow context returned error: %v", err)
	}
	steps, _ := contextPayload["steps"].(map[string]any)
	if _, executedRejected := steps["rejected"]; executedRejected {
		t.Fatalf("expected rejected branch to be skipped, got context %#v", steps["rejected"])
	}
	if _, executedApproved := steps["approved"]; !executedApproved {
		t.Fatal("expected approved branch to execute")
	}

	events, err := service.GetWorkflowHistory(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflowHistory returned error: %v", err)
	}
	foundTransition := false
	for _, event := range events {
		if event.EventType == "TransitionSelected" {
			foundTransition = true
		}
	}
	if !foundTransition {
		t.Fatal("expected TransitionSelected event to be recorded")
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

func TestDefinitionRejectsUnknownTransitionTarget(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	_, err = service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Invalid transition workflow",
		Description: "References a missing target",
		Steps: []StepDefinition{
			{
				Name:     "start",
				Activity: "noop",
				Transitions: []StepTransition{
					{To: "missing"},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected CreateDefinition to reject unknown transition target")
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

func TestScriptActivityExecutesStarlark(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	cfg.Workflow.ScriptEnabled = true
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Script workflow",
		Description: "Transforms data with Starlark",
		Steps: []StepDefinition{
			{
				Name:     "seed",
				Activity: "noop",
				Input:    []byte(`{"message":"orchestra","count":2}`),
			},
			{
				Name:     "script-step",
				Activity: "script",
				Input: []byte(`{
					"language":"starlark",
					"script":"result = {\"message\": strings.upper(input[\"name\"]), \"original\": workflow.step_output(\"seed\")[\"message\"], \"count\": workflow.step_output(\"seed\")[\"count\"]}",
					"exports":["result"],
					"data":{"name":"{{steps.seed.message}}"}
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
	for i := 0; i < 4; i++ {
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce returned error: %v", err)
		}
	}

	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	if instance.Status != StatusCompleted {
		t.Fatalf("expected completed workflow, got %s", instance.Status)
	}

	var contextPayload map[string]any
	if err := json.Unmarshal(instance.Context, &contextPayload); err != nil {
		t.Fatalf("Unmarshal workflow context returned error: %v", err)
	}
	steps, _ := contextPayload["steps"].(map[string]any)
	result, _ := steps["script-step"].(map[string]any)
	if result["message"] != "ORCHESTRA" {
		t.Fatalf("expected upper-cased result, got %#v", result["message"])
	}
	if result["original"] != "orchestra" {
		t.Fatalf("expected original value from workflow.step_output, got %#v", result["original"])
	}
}

func TestScriptActivityHonorsExecutionStepLimit(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	cfg.Workflow.ScriptEnabled = true
	cfg.Workflow.ScriptMaxExecutionSteps = 100
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Script limit workflow",
		Description: "Fails when the script exceeds the execution budget",
		Steps: []StepDefinition{
			{
				Name:     "script-step",
				Activity: "script",
				Retry: RetryPolicy{
					MaxAttempts: 1,
				},
				Input: []byte(`{
					"language":"starlark",
					"script":"result = [i for i in range(100000)]"
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
	if instance.Status != StatusFailed {
		t.Fatalf("expected failed workflow, got %s", instance.Status)
	}
	if !strings.Contains(instance.LastError, "execution step") && !strings.Contains(instance.LastError, "too many steps") {
		t.Fatalf("expected step limit error, got %q", instance.LastError)
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

func TestWorkflowCarriesContextBetweenSteps(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Context workflow",
		Description: "Uses prior step output in later inputs",
		Steps: []StepDefinition{
			{Name: "produce", Activity: "noop", Input: []byte(`{"customer":{"id":42},"name":"Ada"}`)},
			{Name: "consume", Activity: "noop", Input: []byte(`{"customerId":"{{steps.produce.customer.id}}","name":"{{steps.produce.name}}","greeting":"Hello {{steps.produce.name}}"}`)},
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
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce returned error: %v", err)
		}
	}

	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	if instance.Status != StatusCompleted {
		t.Fatalf("expected completed workflow, got %s", instance.Status)
	}

	var contextPayload map[string]any
	if err := json.Unmarshal(instance.Context, &contextPayload); err != nil {
		t.Fatalf("Unmarshal workflow context returned error: %v", err)
	}
	steps, ok := contextPayload["steps"].(map[string]any)
	if !ok {
		t.Fatalf("expected steps map in context, got %#v", contextPayload["steps"])
	}
	consume, ok := steps["consume"].(map[string]any)
	if !ok {
		t.Fatalf("expected consume step output, got %#v", steps["consume"])
	}
	if consume["customerId"] != float64(42) {
		t.Fatalf("expected templated numeric value 42, got %#v", consume["customerId"])
	}
	if consume["greeting"] != "Hello Ada" {
		t.Fatalf("expected templated greeting, got %#v", consume["greeting"])
	}
}

func TestWorkflowSignalUpdatesContextAndReplay(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Signal workflow",
		Description: "Accepts external signals",
		Steps: []StepDefinition{
			{Name: "wait", Activity: "delay", Input: []byte(`{"durationSeconds":1}`)},
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

	instance, err = service.SignalWorkflow(context.Background(), instance.ID, SignalWorkflowInput{
		Name:    "approval",
		Payload: []byte(`{"approved":true}`),
	})
	if err != nil {
		t.Fatalf("SignalWorkflow returned error: %v", err)
	}

	var contextPayload map[string]any
	if err := json.Unmarshal(instance.Context, &contextPayload); err != nil {
		t.Fatalf("Unmarshal workflow context returned error: %v", err)
	}
	signals, ok := contextPayload["signals"].(map[string]any)
	if !ok {
		t.Fatalf("expected signals map in context, got %#v", contextPayload["signals"])
	}
	approval, ok := signals["approval"].(map[string]any)
	if !ok {
		t.Fatalf("expected approval signal payload, got %#v", signals["approval"])
	}
	if approval["count"] != float64(1) {
		t.Fatalf("expected approval signal count 1, got %#v", approval["count"])
	}

	replay, err := service.ReplayWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("ReplayWorkflow returned error: %v", err)
	}
	if replay.EventCount == 0 {
		t.Fatal("expected replay to include events")
	}
	if !json.Valid(replay.Context) {
		t.Fatalf("expected replay context JSON, got %s", string(replay.Context))
	}
}

func TestSetContextActivityUpdatesWorkflowContext(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Set context workflow",
		Description: "Writes custom workflow context values",
		Steps: []StepDefinition{
			{Name: "seed", Activity: "set-context", Input: []byte(`{"values":{"vars.customerId":42,"vars.customerName":"Ada"}}`)},
			{Name: "consume", Activity: "noop", Input: []byte(`{"id":"{{vars.customerId}}","message":"Hello {{vars.customerName}}"}`)},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	instance, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce returned error: %v", err)
		}
	}

	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	var contextPayload map[string]any
	if err := json.Unmarshal(instance.Context, &contextPayload); err != nil {
		t.Fatalf("Unmarshal workflow context returned error: %v", err)
	}
	vars, ok := contextPayload["vars"].(map[string]any)
	if !ok {
		t.Fatalf("expected vars context, got %#v", contextPayload["vars"])
	}
	if vars["customerId"] != float64(42) {
		t.Fatalf("expected customerId 42, got %#v", vars["customerId"])
	}
	steps, _ := contextPayload["steps"].(map[string]any)
	consume, _ := steps["consume"].(map[string]any)
	if consume["message"] != "Hello Ada" {
		t.Fatalf("expected templated message, got %#v", consume["message"])
	}
}

func TestWaitSignalActivityCompletesAfterSignal(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Wait signal workflow",
		Description: "Waits for a signal and resumes",
		Steps: []StepDefinition{
			{Name: "wait", Activity: "wait-signal", Input: []byte(`{"signal":"approval","pollIntervalSeconds":0,"timeoutSeconds":30}`)},
			{Name: "done", Activity: "noop", Input: []byte(`{"approved":"{{steps.wait.lastPayload.approved}}"}`)},
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
	tasks, err := service.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks returned error: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != StatusWaiting {
		t.Fatalf("expected waiting task after initial signal wait, got %+v", tasks)
	}
	processed, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("second RunOnce returned error: %v", err)
	}
	if processed {
		t.Fatal("expected no work while task is parked waiting for signal")
	}
	history, err := service.GetWorkflowHistory(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflowHistory returned error: %v", err)
	}
	waitEvents := 0
	for _, event := range history {
		if event.EventType == "ActivityWaitingForSignal" {
			waitEvents++
		}
	}
	if waitEvents != 1 {
		t.Fatalf("expected exactly one ActivityWaitingForSignal event, got %d", waitEvents)
	}
	instance, err = service.SignalWorkflow(context.Background(), instance.ID, SignalWorkflowInput{
		Name:    "approval",
		Payload: []byte(`{"approved":true}`),
	})
	if err != nil {
		t.Fatalf("SignalWorkflow returned error: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce returned error: %v", err)
		}
	}
	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	if instance.Status != StatusCompleted {
		t.Fatalf("expected completed workflow, got %s", instance.Status)
	}
}

func TestDataActivitiesTransformRenderBase64HashAndPatch(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Data activity workflow",
		Description: "Exercises pure data activities",
		Steps: []StepDefinition{
			{Name: "transform", Activity: "transform", Input: []byte(`{"value":{"name":"Ada","status":"draft"}}`)},
			{Name: "render", Activity: "template-render", Input: []byte(`{"template":"Hello {{steps.transform.name}}","data":{"source":"test"}}`)},
			{Name: "encode", Activity: "base64", Input: []byte(`{"mode":"encode","value":"{{steps.render.rendered}}"}`)},
			{Name: "digest", Activity: "hash", Input: []byte(`{"algorithm":"sha256","value":"{{steps.render.rendered}}","encoding":"hex"}`)},
			{Name: "patch", Activity: "json-patch", Input: []byte(`{"document":{"status":"draft"},"operations":[{"op":"replace","path":"/status","value":"approved"},{"op":"add","path":"/by","value":"system"}]}`)},
		},
	})
	if err != nil {
		t.Fatalf("CreateDefinition returned error: %v", err)
	}

	instance, err := service.StartWorkflow(context.Background(), definition.ID)
	if err != nil {
		t.Fatalf("StartWorkflow returned error: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := service.RunOnce(context.Background()); err != nil {
			t.Fatalf("RunOnce returned error: %v", err)
		}
	}
	instance, err = service.GetWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("GetWorkflow returned error: %v", err)
	}
	if instance.Status != StatusCompleted {
		t.Fatalf("expected completed workflow, got %s", instance.Status)
	}
	var contextPayload map[string]any
	if err := json.Unmarshal(instance.Context, &contextPayload); err != nil {
		t.Fatalf("Unmarshal workflow context returned error: %v", err)
	}
	steps, _ := contextPayload["steps"].(map[string]any)
	render, _ := steps["render"].(map[string]any)
	if render["rendered"] != "Hello Ada" {
		t.Fatalf("expected rendered string, got %#v", render["rendered"])
	}
	patch, _ := steps["patch"].(map[string]any)
	if patch["status"] != "approved" {
		t.Fatalf("expected patched status approved, got %#v", patch["status"])
	}
}

func TestCancelWorkflowCancelsOpenTasks(t *testing.T) {
	cfg := config.Default()
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "workflows.db")
	service, err := NewService(cfg.Workflow, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService returned error: %v", err)
	}
	defer service.Close()

	definition, err := service.CreateDefinition(context.Background(), CreateDefinitionInput{
		Name:        "Cancelable workflow",
		Description: "Cancels delayed work",
		Steps: []StepDefinition{
			{Name: "wait", Activity: "delay", Input: []byte(`{"durationSeconds":10}`)},
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

	instance, err = service.CancelWorkflow(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("CancelWorkflow returned error: %v", err)
	}
	if instance.Status != StatusCanceled {
		t.Fatalf("expected canceled workflow, got %s", instance.Status)
	}

	tasks, err := service.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks returned error: %v", err)
	}
	if len(tasks) == 0 || tasks[0].Status != StatusCanceled {
		t.Fatalf("expected canceled task, got %+v", tasks)
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
