package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/prasenjit-net/orchestra/internal/config"
)

type Activity interface {
	Descriptor() ActivityDescriptor
	Execute(ctx context.Context, req ActivityExecutionRequest) (ActivityResult, error)
}

type ActivityExecutionRequest struct {
	WorkflowID        string
	DefinitionID      string
	DefinitionVersion int
	WorkflowContext   json.RawMessage
	Step              StepDefinition
	Task              WorkflowTask
	Now               time.Time
}

type ActivityResult struct {
	Output         json.RawMessage
	DelayUntil     *time.Time
	State          json.RawMessage
	ContextUpdates map[string]any
}

func builtInActivities(cfg config.WorkflowConfig, logger *slog.Logger) []Activity {
	activities := []Activity{
		noopActivity{},
		transformActivity{},
		delayActivity{},
		waitSignalActivity{},
		branchActivity{},
		httpActivity{},
		webhookActivity{},
		emailActivity{},
		slackActivity{},
		queuePublishActivity{},
		setContextActivity{},
		jsonPatchActivity{},
		templateRenderActivity{},
		base64Activity{},
		hashActivity{},
		approvalActivity{},
		manualTaskActivity{},
		humanWaitActivity{},
		logActivity{logger: logger},
		failActivity{},
	}
	if cfg.ScriptEnabled {
		activities = append(activities, newScriptActivity(cfg))
	}
	return activities
}

type noopActivity struct{}

func (noopActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:         "noop",
		DisplayName:  "No-op",
		Description:  "Completes immediately without side effects.",
		Category:     "system",
		Status:       "stable",
		Tags:         []string{"utility", "pass-through"},
		ExampleInput: map[string]any{"note": "optional context"},
	}
}

func (noopActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	if len(req.Step.Input) == 0 {
		return ActivityResult{Output: json.RawMessage(`{"ok":true}`)}, nil
	}
	return ActivityResult{Output: req.Step.Input}, nil
}

type delayActivity struct{}

type delayActivityInput struct {
	DurationSeconds int    `json:"durationSeconds"`
	Until           string `json:"until"`
}

type delayActivityState struct {
	TargetTime string `json:"targetTime"`
}

func (delayActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "delay",
		DisplayName: "Delay",
		Description: "Defers the workflow step until a future timestamp without relying on in-memory sleep.",
		Category:    "timers",
		Status:      "stable",
		Tags:        []string{"timer", "scheduling"},
		ExampleInput: map[string]any{
			"durationSeconds": 30,
		},
	}
}

func (delayActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	target, statePayload, err := resolveDelayTarget(req)
	if err != nil {
		return ActivityResult{}, err
	}
	if !req.Now.Before(target) {
		output, err := json.Marshal(map[string]any{
			"waitedUntil": target.UTC().Format(time.RFC3339Nano),
		})
		if err != nil {
			return ActivityResult{}, fmt.Errorf("encode delay activity output: %w", err)
		}
		return ActivityResult{Output: output}, nil
	}
	return ActivityResult{
		DelayUntil: &target,
		State:      statePayload,
	}, nil
}

func resolveDelayTarget(req ActivityExecutionRequest) (time.Time, json.RawMessage, error) {
	if len(req.Task.State) > 0 {
		var state delayActivityState
		if err := json.Unmarshal(req.Task.State, &state); err != nil {
			return time.Time{}, nil, fmt.Errorf("decode delay activity state: %w", err)
		}
		target, err := time.Parse(time.RFC3339Nano, state.TargetTime)
		if err != nil {
			return time.Time{}, nil, fmt.Errorf("parse delay activity state target: %w", err)
		}
		return target.UTC(), req.Task.State, nil
	}

	var payload delayActivityInput
	if len(req.Step.Input) > 0 {
		if err := json.Unmarshal(req.Step.Input, &payload); err != nil {
			return time.Time{}, nil, fmt.Errorf("decode delay activity input: %w", err)
		}
	}

	var target time.Time
	switch {
	case strings.TrimSpace(payload.Until) != "":
		parsed, err := time.Parse(time.RFC3339Nano, payload.Until)
		if err != nil {
			return time.Time{}, nil, fmt.Errorf("parse delay activity until: %w", err)
		}
		target = parsed.UTC()
	case payload.DurationSeconds >= 0:
		target = req.Now.Add(time.Duration(payload.DurationSeconds) * time.Second).UTC()
	default:
		return time.Time{}, nil, fmt.Errorf("delay activity requires durationSeconds >= 0 or a valid until timestamp")
	}

	statePayload, err := json.Marshal(delayActivityState{
		TargetTime: target.Format(time.RFC3339Nano),
	})
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("encode delay activity state: %w", err)
	}

	return target, statePayload, nil
}

type logActivity struct {
	logger *slog.Logger
}

type httpActivity struct{}

type httpActivityInput struct {
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Headers        map[string]string `json:"headers"`
	Body           any               `json:"body"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
	ExpectedStatus int               `json:"expectedStatus"`
}

const maxHTTPResponseBodyBytes = 1 << 20

func (httpActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "http-request",
		DisplayName: "HTTP request",
		Description: "Performs an HTTP request and returns the response status, headers, and body.",
		Category:    "integration",
		Status:      "stable",
		Tags:        []string{"http", "api", "webhook"},
		ExampleInput: map[string]any{
			"method":         "POST",
			"url":            "https://example.com/hooks/workflow",
			"headers":        map[string]string{"Content-Type": "application/json"},
			"body":           map[string]any{"status": "started"},
			"timeoutSeconds": 10,
			"expectedStatus": 200,
		},
	}
}

func (httpActivity) Execute(ctx context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	input := httpActivityInput{
		Method:         http.MethodGet,
		Headers:        map[string]string{},
		TimeoutSeconds: 10,
	}
	if len(req.Step.Input) > 0 {
		if err := json.Unmarshal(req.Step.Input, &input); err != nil {
			return ActivityResult{}, fmt.Errorf("decode http-request activity input: %w", err)
		}
	}

	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		method = http.MethodGet
	}
	if strings.TrimSpace(input.URL) == "" {
		return ActivityResult{}, fmt.Errorf("http-request activity requires a url")
	}
	parsedURL, err := validateHTTPRequestURL(input.URL)
	if err != nil {
		return ActivityResult{}, err
	}
	if input.TimeoutSeconds <= 0 {
		input.TimeoutSeconds = 10
	}

	bodyReader, contentType, err := encodeHTTPRequestBody(input.Body)
	if err != nil {
		return ActivityResult{}, err
	}

	request, err := http.NewRequestWithContext(ctx, method, parsedURL.String(), bodyReader)
	if err != nil {
		return ActivityResult{}, fmt.Errorf("create http-request activity request: %w", err)
	}
	for key, value := range input.Headers {
		request.Header.Set(key, value)
	}
	if contentType != "" && request.Header.Get("Content-Type") == "" {
		request.Header.Set("Content-Type", contentType)
	}

	client := &http.Client{Timeout: time.Duration(input.TimeoutSeconds) * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return ActivityResult{}, fmt.Errorf("execute http-request activity: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPResponseBodyBytes+1))
	if err != nil {
		return ActivityResult{}, fmt.Errorf("read http-request activity response body: %w", err)
	}
	if len(responseBody) > maxHTTPResponseBodyBytes {
		return ActivityResult{}, fmt.Errorf("http-request activity response exceeded %d bytes", maxHTTPResponseBodyBytes)
	}

	if input.ExpectedStatus > 0 && response.StatusCode != input.ExpectedStatus {
		return ActivityResult{}, fmt.Errorf("http-request activity expected status %d, got %d", input.ExpectedStatus, response.StatusCode)
	}
	if input.ExpectedStatus == 0 && (response.StatusCode < 200 || response.StatusCode >= 300) {
		return ActivityResult{}, fmt.Errorf("http-request activity expected 2xx status, got %d", response.StatusCode)
	}

	output, err := json.Marshal(map[string]any{
		"statusCode": response.StatusCode,
		"headers":    response.Header,
		"body":       string(responseBody),
	})
	if err != nil {
		return ActivityResult{}, fmt.Errorf("encode http-request activity output: %w", err)
	}

	return ActivityResult{Output: output}, nil
}

func validateHTTPRequestURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("parse http-request activity url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("http-request activity requires an http or https url")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return nil, fmt.Errorf("http-request activity requires a host")
	}
	return parsed, nil
}

func encodeHTTPRequestBody(body any) (io.Reader, string, error) {
	if body == nil {
		return nil, "", nil
	}
	switch typed := body.(type) {
	case string:
		return strings.NewReader(typed), "text/plain; charset=utf-8", nil
	case []byte:
		return strings.NewReader(string(typed)), "application/octet-stream", nil
	default:
		payload, err := json.Marshal(typed)
		if err != nil {
			return nil, "", fmt.Errorf("encode http-request activity body: %w", err)
		}
		return strings.NewReader(string(payload)), "application/json; charset=utf-8", nil
	}
}

type logActivityInput struct {
	Message string `json:"message"`
	Level   string `json:"level"`
}

func (a logActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "log",
		DisplayName: "Log",
		Description: "Writes a structured log entry from workflow input.",
		Category:    "observability",
		Status:      "stable",
		Tags:        []string{"logging", "debugging"},
		ExampleInput: map[string]any{
			"message": "workflow step executed",
			"level":   "info",
		},
	}
}

func (a logActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	payload := logActivityInput{Level: "info"}
	if len(req.Step.Input) > 0 {
		if err := json.Unmarshal(req.Step.Input, &payload); err != nil {
			return ActivityResult{}, fmt.Errorf("decode log activity input: %w", err)
		}
	}
	if strings.TrimSpace(payload.Message) == "" {
		return ActivityResult{}, fmt.Errorf("log activity requires a message")
	}

	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(payload.Level)) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	a.logger.Log(context.Background(), level, payload.Message,
		"workflow_id", req.WorkflowID,
		"definition_id", req.DefinitionID,
		"definition_version", req.DefinitionVersion,
		"step_name", req.Step.Name,
		"activity_name", req.Step.Activity,
	)

	output, err := json.Marshal(map[string]any{
		"logged":  true,
		"message": payload.Message,
		"level":   strings.ToLower(strings.TrimSpace(payload.Level)),
	})
	if err != nil {
		return ActivityResult{}, fmt.Errorf("encode log activity output: %w", err)
	}

	return ActivityResult{Output: output}, nil
}

type failActivity struct{}

type failActivityInput struct {
	Message string `json:"message"`
}

func (failActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:         "fail",
		DisplayName:  "Fail",
		Description:  "Fails the step intentionally to exercise retries and terminal failures.",
		Category:     "testing",
		Status:       "stable",
		Tags:         []string{"testing", "control"},
		ExampleInput: map[string]any{"message": "intentional failure"},
	}
}

func (failActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	payload := failActivityInput{Message: "activity failed intentionally"}
	if len(req.Step.Input) > 0 {
		if err := json.Unmarshal(req.Step.Input, &payload); err != nil {
			return ActivityResult{}, fmt.Errorf("decode fail activity input: %w", err)
		}
	}
	return ActivityResult{}, fmt.Errorf("%s", payload.Message)
}
