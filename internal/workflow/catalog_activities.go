package workflow

import (
	"context"
	"crypto/md5"  // #nosec G501 -- data checksum, not a security primitive
	"crypto/sha1" // #nosec G505 -- data checksum, not a security primitive
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type transformActivity struct{}

func (transformActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "transform",
		DisplayName: "Transform",
		Description: "Returns a transformed value payload after template resolution, useful for shaping data between steps.",
		Category:    "system",
		Status:      "stable",
		Tags:        []string{"transform", "mapping"},
		ExampleInput: map[string]any{
			"value": map[string]any{
				"customerId": "{{steps.fetch.id}}",
				"status":     "ready",
			},
		},
		ExampleOutput: map[string]any{"customerId": "", "status": ""},
	}
}

func (transformActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	payload := decodeJSONValue(req.Step.Input)
	switch typed := payload.(type) {
	case map[string]any:
		if value, ok := typed["value"]; ok {
			return marshalActivityResult(value)
		}
		if value, ok := typed["data"]; ok {
			return marshalActivityResult(value)
		}
		return marshalActivityResult(typed)
	default:
		return marshalActivityResult(payload)
	}
}

type waitSignalActivity struct{}

type waitSignalInput struct {
	Signal              string `json:"signal"`
	PollIntervalSeconds int    `json:"pollIntervalSeconds"`
	TimeoutSeconds      int    `json:"timeoutSeconds"`
}

type waitSignalState struct {
	StartedAt     string `json:"startedAt"`
	ObservedCount int    `json:"observedCount"`
	SignalName    string `json:"signalName"`
	TimeoutAt     string `json:"timeoutAt,omitempty"`
}

func (waitSignalActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "wait-signal",
		DisplayName: "Wait signal",
		Description: "Pauses the workflow until a named signal arrives.",
		Category:    "flow-control",
		Status:      "stable",
		Tags:        []string{"signal", "pause", "async"},
		ExampleInput: map[string]any{
			"signal":              "approval",
			"pollIntervalSeconds": 1,
			"timeoutSeconds":      3600,
		},
		ExampleOutput: map[string]any{"type": "signal", "signal": "", "count": 1, "lastPayload": map[string]any{}, "receivedAt": ""},
	}
}

func (a waitSignalActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	return executeSignalWait(req, waitSignalConfig{
		DefaultSignal: "signal",
		RejectOnFalse: false,
		OutputType:    "signal",
	})
}

type branchActivity struct{}

type branchCase struct {
	Label    string          `json:"label"`
	Path     string          `json:"path"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value,omitempty"`
	Target   string          `json:"target,omitempty"`
}

type branchInput struct {
	Cases        []branchCase `json:"cases"`
	DefaultLabel string       `json:"defaultLabel"`
	DefaultTo    string       `json:"defaultTo,omitempty"`
}

func (branchActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "branch",
		DisplayName: "Branch",
		Description: "Evaluates ordered conditions and emits the selected branch label for later transitions or scripting.",
		Category:    "flow-control",
		Status:      "stable",
		Tags:        []string{"conditions", "routing"},
		ExampleInput: map[string]any{
			"cases": []map[string]any{
				{"label": "approved", "path": "steps.review.approved", "operator": "eq", "value": true},
				{"label": "rejected", "path": "steps.review.approved", "operator": "eq", "value": false},
			},
			"defaultLabel": "unknown",
		},
		ExampleOutput: map[string]any{"selected": "", "target": "", "matched": false},
	}
}

func (branchActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	var input branchInput
	if err := json.Unmarshal(req.Step.Input, &input); err != nil {
		return ActivityResult{}, fmt.Errorf("decode branch activity input: %w", err)
	}
	contextPayload := decodeJSONObject(req.WorkflowContext)
	for _, candidate := range input.Cases {
		condition := TransitionCondition{
			Path:     strings.TrimSpace(candidate.Path),
			Operator: strings.ToLower(strings.TrimSpace(candidate.Operator)),
			Value:    candidate.Value,
		}
		if condition.Path == "" {
			return ActivityResult{}, fmt.Errorf("branch activity case requires a path")
		}
		if condition.Operator == "" {
			condition.Operator = "eq"
		}
		if err := validateTransitionCondition(req.Step.Name, condition); err != nil {
			return ActivityResult{}, err
		}
		matched, err := transitionMatches(contextPayload, condition)
		if err != nil {
			return ActivityResult{}, fmt.Errorf("evaluate branch case %q: %w", candidate.Label, err)
		}
		if matched {
			return marshalActivityResult(map[string]any{
				"selected": candidate.Label,
				"target":   candidate.Target,
				"matched":  true,
			})
		}
	}
	return marshalActivityResult(map[string]any{
		"selected": strings.TrimSpace(input.DefaultLabel),
		"target":   strings.TrimSpace(input.DefaultTo),
		"matched":  false,
	})
}

type webhookActivity struct{}

func (webhookActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "webhook",
		DisplayName: "Webhook",
		Description: "Posts a payload to an HTTP endpoint using webhook-friendly defaults.",
		Category:    "integration",
		Status:      "stable",
		Tags:        []string{"http", "webhook", "notify"},
		ExampleInput: map[string]any{
			"url":            "https://example.com/hooks/workflow",
			"body":           map[string]any{"status": "completed"},
			"timeoutSeconds": 10,
		},
		ExampleOutput: map[string]any{"statusCode": 200, "headers": map[string]any{}, "body": ""},
	}
}

func (webhookActivity) Execute(ctx context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	return executeHTTPAlias(ctx, req, "POST", "url")
}

type emailActivity struct{}

func (emailActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "email",
		DisplayName: "Email",
		Description: "Publishes an email payload to an external email provider endpoint.",
		Category:    "integration",
		Status:      "beta",
		Tags:        []string{"notification", "email", "provider"},
		ExampleInput: map[string]any{
			"providerUrl":    "https://email.example.com/send",
			"to":             []string{"ops@example.com"},
			"subject":        "Workflow completed",
			"text":           "Run {{workflow.id}} completed",
			"timeoutSeconds": 10,
		},
	}
}

func (emailActivity) Execute(ctx context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	return executeHTTPAlias(ctx, req, "POST", "providerUrl")
}

type slackActivity struct{}

func (slackActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "slack",
		DisplayName: "Slack",
		Description: "Publishes a Slack-compatible webhook payload to a configured webhook URL.",
		Category:    "integration",
		Status:      "beta",
		Tags:        []string{"slack", "webhook", "chatops"},
		ExampleInput: map[string]any{
			"webhookUrl":     "https://hooks.slack.com/services/...",
			"text":           "Workflow {{workflow.id}} completed",
			"timeoutSeconds": 10,
		},
	}
}

func (slackActivity) Execute(ctx context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	return executeHTTPAlias(ctx, req, "POST", "webhookUrl")
}

type queuePublishActivity struct{}

func (queuePublishActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "queue-publish",
		DisplayName: "Queue publish",
		Description: "Posts a queue message payload to an external queue gateway endpoint.",
		Category:    "integration",
		Status:      "beta",
		Tags:        []string{"queue", "publish", "async"},
		ExampleInput: map[string]any{
			"url":            "https://queue.example.com/publish",
			"topic":          "workflow-events",
			"message":        map[string]any{"workflowId": "{{workflow.id}}"},
			"timeoutSeconds": 10,
		},
	}
}

func (queuePublishActivity) Execute(ctx context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	return executeHTTPAlias(ctx, req, "POST", "url")
}

type setContextActivity struct{}

type setContextInput struct {
	Path   string         `json:"path"`
	Value  any            `json:"value"`
	Values map[string]any `json:"values"`
}

func (setContextActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "set-context",
		DisplayName: "Set context",
		Description: "Writes explicit values into workflow context paths for later steps.",
		Category:    "data",
		Status:      "stable",
		Tags:        []string{"context", "state", "assign"},
		ExampleInput: map[string]any{
			"values": map[string]any{
				"vars.customerId": "{{steps.lookup.id}}",
				"vars.priority":   "high",
			},
		},
	}
}

func (setContextActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	var input setContextInput
	if err := json.Unmarshal(req.Step.Input, &input); err != nil {
		return ActivityResult{}, fmt.Errorf("decode set-context activity input: %w", err)
	}
	updates := make(map[string]any)
	if path := strings.TrimSpace(input.Path); path != "" {
		if err := validateContextUpdatePath(path); err != nil {
			return ActivityResult{}, err
		}
		updates[path] = input.Value
	}
	for path, value := range input.Values {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			return ActivityResult{}, fmt.Errorf("set-context activity requires non-empty paths")
		}
		if err := validateContextUpdatePath(trimmed); err != nil {
			return ActivityResult{}, err
		}
		updates[trimmed] = value
	}
	if len(updates) == 0 {
		return ActivityResult{}, fmt.Errorf("set-context activity requires path/value or values")
	}
	output, err := json.Marshal(map[string]any{"set": updates})
	if err != nil {
		return ActivityResult{}, fmt.Errorf("encode set-context activity output: %w", err)
	}
	return ActivityResult{Output: output, ContextUpdates: updates}, nil
}

type jsonPatchActivity struct{}

type jsonPatchInput struct {
	Document   any                  `json:"document"`
	Operations []jsonPatchOperation `json:"operations"`
}

type jsonPatchOperation struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

func (jsonPatchActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "json-patch",
		DisplayName: "JSON patch",
		Description: "Applies add, replace, and remove patch operations to a JSON-like document.",
		Category:    "data",
		Status:      "beta",
		Tags:        []string{"json", "patch", "document"},
		ExampleInput: map[string]any{
			"document": map[string]any{"status": "draft"},
			"operations": []map[string]any{
				{"op": "replace", "path": "/status", "value": "approved"},
				{"op": "add", "path": "/reviewedBy", "value": "system"},
			},
		},
	}
}

func (jsonPatchActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	var input jsonPatchInput
	if err := json.Unmarshal(req.Step.Input, &input); err != nil {
		return ActivityResult{}, fmt.Errorf("decode json-patch activity input: %w", err)
	}
	if len(input.Operations) == 0 {
		return ActivityResult{}, fmt.Errorf("json-patch activity requires operations")
	}
	result, err := applyJSONPatchOperations(input.Document, input.Operations)
	if err != nil {
		return ActivityResult{}, err
	}
	return marshalActivityResult(result)
}

type templateRenderActivity struct{}

type templateRenderInput struct {
	Template string `json:"template"`
	Data     any    `json:"data"`
}

func (templateRenderActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "template-render",
		DisplayName: "Template render",
		Description: "Renders a string template against workflow context plus step-local data.",
		Category:    "data",
		Status:      "stable",
		Tags:        []string{"template", "string", "render"},
		ExampleInput: map[string]any{
			"template": "Hello {{steps.fetch.name}} from {{data.source}}",
			"data":     map[string]any{"source": "workflow"},
		},
	}
}

func (templateRenderActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	var input templateRenderInput
	if err := json.Unmarshal(req.Step.Input, &input); err != nil {
		return ActivityResult{}, fmt.Errorf("decode template-render activity input: %w", err)
	}
	if strings.TrimSpace(input.Template) == "" {
		return ActivityResult{}, fmt.Errorf("template-render activity requires a template")
	}
	contextPayload := decodeJSONObject(req.WorkflowContext)
	contextPayload["data"] = input.Data
	rendered := resolveTemplateValue(input.Template, contextPayload)
	return marshalActivityResult(map[string]any{
		"rendered": stringifyTemplateValue(rendered),
	})
}

type base64Activity struct{}

type base64Input struct {
	Mode    string `json:"mode"`
	Value   string `json:"value"`
	URLSafe bool   `json:"urlSafe"`
	Raw     bool   `json:"raw"`
}

func (base64Activity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "base64",
		DisplayName: "Base64",
		Description: "Encodes or decodes string data using standard or URL-safe Base64.",
		Category:    "data",
		Status:      "stable",
		Tags:        []string{"encoding", "base64"},
		ExampleInput: map[string]any{
			"mode":  "encode",
			"value": "hello world",
		},
	}
}

func (base64Activity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	var input base64Input
	if err := json.Unmarshal(req.Step.Input, &input); err != nil {
		return ActivityResult{}, fmt.Errorf("decode base64 activity input: %w", err)
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = "encode"
	}
	encoding := selectBase64Encoding(input.URLSafe, input.Raw)
	switch mode {
	case "encode":
		return marshalActivityResult(map[string]any{
			"mode":  mode,
			"value": encoding.EncodeToString([]byte(input.Value)),
		})
	case "decode":
		decoded, err := encoding.DecodeString(input.Value)
		if err != nil {
			return ActivityResult{}, fmt.Errorf("decode base64 activity value: %w", err)
		}
		return marshalActivityResult(map[string]any{
			"mode":  mode,
			"value": string(decoded),
		})
	default:
		return ActivityResult{}, fmt.Errorf("base64 activity mode must be encode or decode")
	}
}

type hashActivity struct{}

type hashInput struct {
	Algorithm string `json:"algorithm"`
	Value     any    `json:"value"`
	Encoding  string `json:"encoding"`
}

func (hashActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "hash",
		DisplayName: "Hash",
		Description: "Computes a content hash over a string or JSON-like value.",
		Category:    "data",
		Status:      "stable",
		Tags:        []string{"hash", "digest", "checksum"},
		ExampleInput: map[string]any{
			"algorithm": "sha256",
			"value":     "{{steps.fetch.payload}}",
			"encoding":  "hex",
		},
	}
}

func (hashActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	var input hashInput
	if err := json.Unmarshal(req.Step.Input, &input); err != nil {
		return ActivityResult{}, fmt.Errorf("decode hash activity input: %w", err)
	}
	algorithm := strings.ToLower(strings.TrimSpace(input.Algorithm))
	if algorithm == "" {
		algorithm = "sha256"
	}
	encoding := strings.ToLower(strings.TrimSpace(input.Encoding))
	if encoding == "" {
		encoding = "hex"
	}
	payload, err := normalizeHashInput(input.Value)
	if err != nil {
		return ActivityResult{}, err
	}
	sum, err := digestBytes(algorithm, payload)
	if err != nil {
		return ActivityResult{}, err
	}
	encoded, err := encodeDigest(sum, encoding)
	if err != nil {
		return ActivityResult{}, err
	}
	return marshalActivityResult(map[string]any{
		"algorithm": algorithm,
		"encoding":  encoding,
		"value":     encoded,
	})
}

type approvalActivity struct{}
type manualTaskActivity struct{}
type humanWaitActivity struct{}

func (approvalActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "approval",
		DisplayName: "Approval",
		Description: "Waits for an approval signal and fails the step if the payload marks it rejected.",
		Category:    "operator",
		Status:      "beta",
		Tags:        []string{"manual", "approval", "signal"},
		ExampleInput: map[string]any{
			"signal":              "approval",
			"timeoutSeconds":      86400,
			"pollIntervalSeconds": 1,
		},
		ExampleOutput: map[string]any{"type": "approval", "signal": "approval", "count": 1, "lastPayload": map[string]any{"approved": true}, "receivedAt": ""},
	}
}

func (approvalActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	return executeSignalWait(req, waitSignalConfig{
		DefaultSignal: "approval",
		RejectOnFalse: true,
		OutputType:    "approval",
	})
}

func (manualTaskActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "manual-task",
		DisplayName: "Manual task",
		Description: "Waits for an operator completion signal before continuing the workflow.",
		Category:    "operator",
		Status:      "beta",
		Tags:        []string{"manual", "operator", "signal"},
		ExampleInput: map[string]any{
			"signal":              "manual-complete",
			"timeoutSeconds":      86400,
			"pollIntervalSeconds": 1,
		},
		ExampleOutput: map[string]any{"type": "manual-task", "signal": "manual-complete", "count": 1, "lastPayload": map[string]any{}, "receivedAt": ""},
	}
}

func (manualTaskActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	return executeSignalWait(req, waitSignalConfig{
		DefaultSignal: "manual-complete",
		RejectOnFalse: false,
		OutputType:    "manual-task",
	})
}

func (humanWaitActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "human-wait",
		DisplayName: "Human wait",
		Description: "Waits for a human resume signal before continuing.",
		Category:    "operator",
		Status:      "beta",
		Tags:        []string{"manual", "wait", "resume"},
		ExampleInput: map[string]any{
			"signal":              "resume",
			"timeoutSeconds":      86400,
			"pollIntervalSeconds": 1,
		},
		ExampleOutput: map[string]any{"type": "human-wait", "signal": "resume", "count": 1, "lastPayload": map[string]any{}, "receivedAt": ""},
	}
}

func (humanWaitActivity) Execute(_ context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	return executeSignalWait(req, waitSignalConfig{
		DefaultSignal: "resume",
		RejectOnFalse: false,
		OutputType:    "human-wait",
	})
}

type waitSignalConfig struct {
	DefaultSignal string
	RejectOnFalse bool
	OutputType    string
}

func executeSignalWait(req ActivityExecutionRequest, cfg waitSignalConfig) (ActivityResult, error) {
	var input waitSignalInput
	if len(req.Step.Input) > 0 {
		if err := json.Unmarshal(req.Step.Input, &input); err != nil {
			return ActivityResult{}, fmt.Errorf("decode %s activity input: %w", req.Step.Activity, err)
		}
	}
	signalName := strings.TrimSpace(input.Signal)
	if signalName == "" {
		signalName = cfg.DefaultSignal
	}
	if signalName == "" {
		return ActivityResult{}, fmt.Errorf("%s activity requires a signal name", req.Step.Activity)
	}
	if input.PollIntervalSeconds < 0 || input.TimeoutSeconds < 0 {
		return ActivityResult{}, fmt.Errorf("%s activity requires non-negative timeout and poll interval", req.Step.Activity)
	}
	state, initialized, err := decodeWaitSignalState(req.Task.State)
	if err != nil {
		return ActivityResult{}, err
	}
	currentCount, payload, receivedAt := lookupSignalSnapshot(req.WorkflowContext, signalName)
	if !initialized {
		state = waitSignalState{
			StartedAt:     formatTime(req.Now),
			ObservedCount: currentCount,
		}
	}
	if currentCount > state.ObservedCount {
		if cfg.RejectOnFalse {
			approved, ok := lookupPathValue(payload, "approved")
			if !ok || !isTruthy(approved) {
				return ActivityResult{}, fmt.Errorf("%s activity received a non-approving signal", req.Step.Activity)
			}
		}
		output, err := json.Marshal(map[string]any{
			"type":        cfg.OutputType,
			"signal":      signalName,
			"count":       currentCount,
			"lastPayload": payload,
			"receivedAt":  receivedAt,
		})
		if err != nil {
			return ActivityResult{}, fmt.Errorf("encode %s activity output: %w", req.Step.Activity, err)
		}
		return ActivityResult{Output: output}, nil
	}
	if !initialized {
		state.SignalName = signalName
		if input.TimeoutSeconds > 0 {
			state.TimeoutAt = formatTime(req.Now.Add(time.Duration(input.TimeoutSeconds) * time.Second).UTC())
		}
	} else if state.SignalName == "" {
		state.SignalName = signalName
	}
	if state.TimeoutAt != "" {
		timeoutAt, err := time.Parse(time.RFC3339Nano, state.TimeoutAt)
		if err != nil {
			return ActivityResult{}, fmt.Errorf("parse %s activity timeout: %w", req.Step.Activity, err)
		}
		if !req.Now.Before(timeoutAt) {
			return ActivityResult{}, fmt.Errorf("%s activity timed out waiting for signal %q", req.Step.Activity, signalName)
		}
	}
	statePayload, err := json.Marshal(state)
	if err != nil {
		return ActivityResult{}, fmt.Errorf("encode %s activity state: %w", req.Step.Activity, err)
	}
	wait := &ActivitySignalWait{
		SignalName: signalName,
		State:      statePayload,
	}
	if state.TimeoutAt != "" {
		timeoutAt, err := time.Parse(time.RFC3339Nano, state.TimeoutAt)
		if err != nil {
			return ActivityResult{}, fmt.Errorf("parse %s activity timeout: %w", req.Step.Activity, err)
		}
		timeoutAt = timeoutAt.UTC()
		wait.TimeoutAt = &timeoutAt
	}
	return ActivityResult{WaitForSignal: wait, State: statePayload}, nil
}

func executeHTTPAlias(ctx context.Context, req ActivityExecutionRequest, method string, urlField string) (ActivityResult, error) {
	payload := decodeJSONObject(req.Step.Input)
	rawURL, _ := payload[urlField].(string)
	if strings.TrimSpace(rawURL) == "" {
		return ActivityResult{}, fmt.Errorf("%s activity requires %s", req.Step.Activity, urlField)
	}
	payload["method"] = method
	payload["url"] = rawURL
	delete(payload, urlField)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ActivityResult{}, fmt.Errorf("encode %s activity input: %w", req.Step.Activity, err)
	}
	nextReq := req
	nextReq.Step.Input = encoded
	return httpActivity{}.Execute(ctx, nextReq)
}

func marshalActivityResult(value any) (ActivityResult, error) {
	output, err := json.Marshal(value)
	if err != nil {
		return ActivityResult{}, fmt.Errorf("encode activity output: %w", err)
	}
	return ActivityResult{Output: output}, nil
}

func validateContextUpdatePath(path string) error {
	root := path
	if index := strings.Index(root, "."); index >= 0 {
		root = root[:index]
	}
	switch root {
	case "last", "steps", "signals":
		return fmt.Errorf("set-context activity cannot write reserved path root %q", root)
	default:
		return nil
	}
}

func decodeWaitSignalState(raw json.RawMessage) (waitSignalState, bool, error) {
	if len(raw) == 0 {
		return waitSignalState{}, false, nil
	}
	var state waitSignalState
	if err := json.Unmarshal(raw, &state); err != nil {
		return waitSignalState{}, false, fmt.Errorf("decode wait-signal activity state: %w", err)
	}
	return state, true, nil
}

func lookupSignalSnapshot(contextRaw json.RawMessage, signalName string) (int, any, string) {
	contextPayload := decodeJSONObject(contextRaw)
	value, ok := lookupPathValue(contextPayload, "signals."+signalName)
	if !ok {
		return 0, nil, ""
	}
	signalMap, _ := value.(map[string]any)
	if signalMap == nil {
		return 0, nil, ""
	}
	count, _ := signalMap["count"].(float64)
	payload := signalMap["lastPayload"]
	receivedAt, _ := signalMap["receivedAt"].(string)
	return int(count), payload, receivedAt
}

func selectBase64Encoding(urlSafe bool, raw bool) *base64.Encoding {
	switch {
	case urlSafe && raw:
		return base64.RawURLEncoding
	case urlSafe:
		return base64.URLEncoding
	case raw:
		return base64.RawStdEncoding
	default:
		return base64.StdEncoding
	}
}

func normalizeHashInput(value any) ([]byte, error) {
	switch typed := value.(type) {
	case nil:
		return []byte{}, nil
	case string:
		return []byte(typed), nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("encode hash activity value: %w", err)
		}
		return encoded, nil
	}
}

func digestBytes(algorithm string, payload []byte) ([]byte, error) {
	switch algorithm {
	case "md5":
		sum := md5.Sum(payload) // #nosec G401 -- data checksum, not a security primitive
		return sum[:], nil
	case "sha1":
		sum := sha1.Sum(payload) // #nosec G401 -- data checksum, not a security primitive
		return sum[:], nil
	case "sha256":
		sum := sha256.Sum256(payload)
		return sum[:], nil
	case "sha512":
		sum := sha512.Sum512(payload)
		return sum[:], nil
	default:
		return nil, fmt.Errorf("hash activity does not support algorithm %q", algorithm)
	}
}

func encodeDigest(sum []byte, encoding string) (string, error) {
	switch encoding {
	case "hex":
		return hex.EncodeToString(sum), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(sum), nil
	default:
		return "", fmt.Errorf("hash activity does not support encoding %q", encoding)
	}
}

func applyJSONPatchOperations(document any, operations []jsonPatchOperation) (any, error) {
	current := document
	var err error
	for _, operation := range operations {
		current, err = applyJSONPatchOperation(current, operation)
		if err != nil {
			return nil, err
		}
	}
	return current, nil
}

func applyJSONPatchOperation(document any, operation jsonPatchOperation) (any, error) {
	op := strings.ToLower(strings.TrimSpace(operation.Op))
	switch op {
	case "add", "replace":
		return setJSONPointerValue(document, operation.Path, operation.Value, op == "add")
	case "remove":
		return removeJSONPointerValue(document, operation.Path)
	default:
		return nil, fmt.Errorf("json-patch activity does not support op %q", operation.Op)
	}
}

func setJSONPointerValue(document any, pointer string, value any, allowAdd bool) (any, error) {
	segments, err := parseJSONPointer(pointer)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return value, nil
	}
	root := cloneJSONValue(document)
	if root == nil {
		root = map[string]any{}
	}
	if err := setJSONPointerRecursive(&root, segments, value, allowAdd); err != nil {
		return nil, err
	}
	return root, nil
}

func setJSONPointerRecursive(current *any, segments []string, value any, allowAdd bool) error {
	segment := segments[0]
	last := len(segments) == 1
	switch container := (*current).(type) {
	case map[string]any:
		if last {
			if !allowAdd {
				if _, ok := container[segment]; !ok {
					return fmt.Errorf("json-patch replace path %q does not exist", strings.Join(segments, "/"))
				}
			}
			container[segment] = value
			return nil
		}
		next, ok := container[segment]
		if !ok {
			next = map[string]any{}
			container[segment] = next
		}
		return setJSONPointerRecursive(&next, segments[1:], value, allowAdd)
	case []any:
		index, appendMode, err := parseJSONArrayIndex(segment, len(container))
		if err != nil {
			return err
		}
		if last {
			if appendMode {
				if !allowAdd {
					return fmt.Errorf("json-patch replace cannot append to arrays")
				}
				*current = append(container, value)
				return nil
			}
			if index >= len(container) {
				if allowAdd && index == len(container) {
					*current = append(container, value)
					return nil
				}
				return fmt.Errorf("json-patch array index %d out of range", index)
			}
			container[index] = value
			return nil
		}
		if appendMode || index >= len(container) {
			return fmt.Errorf("json-patch path %q does not exist", segment)
		}
		next := container[index]
		if err := setJSONPointerRecursive(&next, segments[1:], value, allowAdd); err != nil {
			return err
		}
		container[index] = next
		return nil
	default:
		return fmt.Errorf("json-patch cannot traverse %T", *current)
	}
}

func removeJSONPointerValue(document any, pointer string) (any, error) {
	segments, err := parseJSONPointer(pointer)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, nil
	}
	root := cloneJSONValue(document)
	if err := removeJSONPointerRecursive(&root, segments); err != nil {
		return nil, err
	}
	return root, nil
}

func removeJSONPointerRecursive(current *any, segments []string) error {
	segment := segments[0]
	last := len(segments) == 1
	switch container := (*current).(type) {
	case map[string]any:
		if last {
			if _, ok := container[segment]; !ok {
				return fmt.Errorf("json-patch remove path %q does not exist", segment)
			}
			delete(container, segment)
			return nil
		}
		next, ok := container[segment]
		if !ok {
			return fmt.Errorf("json-patch path %q does not exist", segment)
		}
		if err := removeJSONPointerRecursive(&next, segments[1:]); err != nil {
			return err
		}
		container[segment] = next
		return nil
	case []any:
		index, appendMode, err := parseJSONArrayIndex(segment, len(container))
		if err != nil {
			return err
		}
		if appendMode || index >= len(container) {
			return fmt.Errorf("json-patch array index %d out of range", index)
		}
		if last {
			*current = append(container[:index], container[index+1:]...)
			return nil
		}
		next := container[index]
		if err := removeJSONPointerRecursive(&next, segments[1:]); err != nil {
			return err
		}
		container[index] = next
		return nil
	default:
		return fmt.Errorf("json-patch cannot traverse %T", *current)
	}
}

func parseJSONPointer(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("json-patch path %q must start with /", pointer)
	}
	parts := strings.Split(pointer[1:], "/")
	for i, part := range parts {
		parts[i] = strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
	}
	return parts, nil
}

func parseJSONArrayIndex(segment string, length int) (int, bool, error) {
	if segment == "-" {
		return length, true, nil
	}
	index, err := strconv.Atoi(segment)
	if err != nil || index < 0 {
		return 0, false, fmt.Errorf("json-patch array index %q is invalid", segment)
	}
	return index, false, nil
}

func cloneJSONValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned any
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return value
	}
	return cloned
}
