package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	starlarkjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"

	"github.com/prasenjit-net/orchestra/internal/config"
)

const scriptThreadLocalKey = "workflow-script-runtime"

// scriptFileOptions enables the full Starlark feature set needed for
// workflow scripts: top-level if/for/while, global reassignment, and set literals.
var scriptFileOptions = &syntax.FileOptions{
	TopLevelControl: true,
	GlobalReassign:  true,
	Set:             true,
}

type scriptActivity struct {
	cfg          config.WorkflowConfig
	sourceLookup func(ctx context.Context, id string) (string, error)
}

type scriptActivityInput struct {
	Language  string   `json:"language"`
	Script    string   `json:"script"`
	ScriptID  string   `json:"scriptId,omitempty"`
	TimeoutMs int      `json:"timeoutMs"`
	Exports   []string `json:"exports"`
	Data      any      `json:"data"`
}

type scriptRuntimeContext struct {
	context map[string]any
}

func newScriptActivity(cfg config.WorkflowConfig, lookup func(context.Context, string) (string, error)) Activity {
	return scriptActivity{cfg: cfg, sourceLookup: lookup}
}

func (a scriptActivity) Descriptor() ActivityDescriptor {
	return ActivityDescriptor{
		Name:        "script",
		DisplayName: "Script",
		Description: "Runs a sandboxed Starlark script for lightweight transformation and workflow logic.",
		Category:    "system",
		Status:      "beta",
		Tags:        []string{"starlark", "sandbox", "transform"},
		ExampleInput: map[string]any{
			"language":  "starlark",
			"script":    "result = {\"message\": strings.upper(input[\"name\"]), \"workflow_id\": workflow.id}",
			"timeoutMs": 100,
			"exports":   []string{"result"},
			"data": map[string]any{
				"name": "orchestra",
			},
		},
		ExampleOutput: map[string]any{"result": nil},
	}
}

func (a scriptActivity) Execute(ctx context.Context, req ActivityExecutionRequest) (ActivityResult, error) {
	input := scriptActivityInput{
		Language: "starlark",
		Exports:  []string{"result"},
		Data:     map[string]any{},
	}
	if len(req.Step.Input) > 0 {
		if err := json.Unmarshal(req.Step.Input, &input); err != nil {
			return ActivityResult{}, fmt.Errorf("decode script activity input: %w", err)
		}
	}

	// Resolve source: saved script reference takes priority over inline body.
	if input.ScriptID != "" {
		if a.sourceLookup == nil {
			return ActivityResult{}, fmt.Errorf("script activity: scriptId %q provided but script lookup is not configured", input.ScriptID)
		}
		fetched, err := a.sourceLookup(ctx, input.ScriptID)
		if err != nil {
			return ActivityResult{}, fmt.Errorf("script activity: lookup script %q: %w", input.ScriptID, err)
		}
		input.Script = fetched
	}

	language := strings.ToLower(strings.TrimSpace(input.Language))
	if language == "" {
		language = "starlark"
	}
	if language != "starlark" {
		return ActivityResult{}, fmt.Errorf("script activity only supports starlark, got %q", input.Language)
	}
	if strings.TrimSpace(input.Script) == "" {
		return ActivityResult{}, fmt.Errorf("script activity requires a script")
	}
	if a.cfg.ScriptMaxSourceBytes > 0 && len(input.Script) > a.cfg.ScriptMaxSourceBytes {
		return ActivityResult{}, fmt.Errorf("script activity source exceeds %d bytes", a.cfg.ScriptMaxSourceBytes)
	}

	timeout := a.cfg.ScriptTimeout
	if timeout <= 0 {
		timeout = 250 * time.Millisecond
	}
	if input.TimeoutMs > 0 {
		requested := time.Duration(input.TimeoutMs) * time.Millisecond
		if requested < timeout {
			timeout = requested
		}
	}

	outputValue, err := a.executeStarlark(ctx, req, input, timeout)
	if err != nil {
		return ActivityResult{}, err
	}

	outputJSON, err := json.Marshal(outputValue)
	if err != nil {
		return ActivityResult{}, fmt.Errorf("encode script activity output: %w", err)
	}
	if a.cfg.ScriptMaxOutputBytes > 0 && len(outputJSON) > a.cfg.ScriptMaxOutputBytes {
		return ActivityResult{}, fmt.Errorf("script activity output exceeds %d bytes", a.cfg.ScriptMaxOutputBytes)
	}

	return ActivityResult{Output: json.RawMessage(outputJSON)}, nil
}

func (a scriptActivity) executeStarlark(ctx context.Context, req ActivityExecutionRequest, input scriptActivityInput, timeout time.Duration) (any, error) {
	scriptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	thread := &starlark.Thread{
		Name: "workflow-script",
		OnMaxSteps: func(thread *starlark.Thread) {
			thread.Cancel("script exceeded execution step limit")
		},
	}
	if a.cfg.ScriptMaxExecutionSteps > 0 {
		thread.SetMaxExecutionSteps(a.cfg.ScriptMaxExecutionSteps)
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-scriptCtx.Done():
			thread.Cancel(scriptCtx.Err().Error())
		case <-done:
		}
	}()

	workflowContext := decodeJSONObject(req.WorkflowContext)
	thread.SetLocal(scriptThreadLocalKey, &scriptRuntimeContext{context: workflowContext})

	predeclared, err := buildScriptPredeclared(req, workflowContext, input.Data)
	if err != nil {
		return nil, err
	}

	globals, err := starlark.ExecFileOptions(scriptFileOptions, thread, "workflow.star", input.Script, predeclared)
	if err != nil {
		return nil, fmt.Errorf("execute script activity: %w", err)
	}

	exports := input.Exports
	if len(exports) == 0 {
		exports = []string{"result"}
	}
	if len(exports) == 1 {
		value, ok := globals[exports[0]]
		if !ok {
			return nil, fmt.Errorf("script activity did not set export %q", exports[0])
		}
		return starlarkToJSONValue(value)
	}

	result := make(map[string]any, len(exports))
	for _, exportName := range exports {
		value, ok := globals[exportName]
		if !ok {
			return nil, fmt.Errorf("script activity did not set export %q", exportName)
		}
		converted, err := starlarkToJSONValue(value)
		if err != nil {
			return nil, fmt.Errorf("convert script export %q: %w", exportName, err)
		}
		result[exportName] = converted
	}
	return result, nil
}

func buildScriptPredeclared(req ActivityExecutionRequest, workflowContext map[string]any, data any) (starlark.StringDict, error) {
	ctxValue, err := goToStarlarkValue(workflowContext)
	if err != nil {
		return nil, fmt.Errorf("convert workflow context for script: %w", err)
	}
	ctxValue.Freeze()

	inputValue, err := goToStarlarkValue(data)
	if err != nil {
		return nil, fmt.Errorf("convert script data input: %w", err)
	}
	inputValue.Freeze()

	stepValue, err := goToStarlarkValue(map[string]any{
		"name":        req.Step.Name,
		"activity":    req.Step.Activity,
		"transitions": req.Step.Transitions,
	})
	if err != nil {
		return nil, fmt.Errorf("convert script step metadata: %w", err)
	}
	stepValue.Freeze()

	predeclared := starlark.StringDict{
		"json":        starlarkjson.Module,
		"strings":     newStringsModule(),
		"collections": newCollectionsModule(),
		"workflow":    newWorkflowModule(req),
		"asserts":     newAssertsModule(),
		"ctx":         ctxValue,
		"input":       inputValue,
		"step":        stepValue,
	}
	predeclared.Freeze()
	return predeclared, nil
}

func newStringsModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "strings",
		Members: starlark.StringDict{
			"lower":    starlark.NewBuiltin("strings.lower", starlarkLower),
			"upper":    starlark.NewBuiltin("strings.upper", starlarkUpper),
			"trim":     starlark.NewBuiltin("strings.trim", starlarkTrim),
			"contains": starlark.NewBuiltin("strings.contains", starlarkContains),
			"replace":  starlark.NewBuiltin("strings.replace", starlarkReplace),
		},
	}
}

func newCollectionsModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "collections",
		Members: starlark.StringDict{
			"compact": starlark.NewBuiltin("collections.compact", starlarkCompact),
			"flatten": starlark.NewBuiltin("collections.flatten", starlarkFlatten),
		},
	}
}

func newWorkflowModule(req ActivityExecutionRequest) *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "workflow",
		Members: starlark.StringDict{
			"id":                 starlark.String(req.WorkflowID),
			"definition_id":      starlark.String(req.DefinitionID),
			"definition_version": starlark.MakeInt(req.DefinitionVersion),
			"step_name":          starlark.String(req.Step.Name),
			"activity_name":      starlark.String(req.Step.Activity),
			"step_output":        starlark.NewBuiltin("workflow.step_output", starlarkWorkflowStepOutput),
			"signal":             starlark.NewBuiltin("workflow.signal", starlarkWorkflowSignal),
			"fail":               starlark.NewBuiltin("workflow.fail", starlarkWorkflowFail),
		},
	}
}

func newAssertsModule() *starlarkstruct.Module {
	return &starlarkstruct.Module{
		Name: "asserts",
		Members: starlark.StringDict{
			"non_empty": starlark.NewBuiltin("asserts.non_empty", starlarkAssertNonEmpty),
			"equals":    starlark.NewBuiltin("asserts.equals", starlarkAssertEquals),
		},
	}
}

func starlarkLower(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "value", &value); err != nil {
		return nil, err
	}
	return starlark.String(strings.ToLower(value)), nil
}

func starlarkUpper(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "value", &value); err != nil {
		return nil, err
	}
	return starlark.String(strings.ToUpper(value)), nil
}

func starlarkTrim(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "value", &value); err != nil {
		return nil, err
	}
	return starlark.String(strings.TrimSpace(value)), nil
}

func starlarkContains(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value, part string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "value", &value, "part", &part); err != nil {
		return nil, err
	}
	return starlark.Bool(strings.Contains(value, part)), nil
}

func starlarkReplace(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value, old, new string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "value", &value, "old", &old, "new", &new); err != nil {
		return nil, err
	}
	return starlark.String(strings.ReplaceAll(value, old, new)), nil
}

func starlarkCompact(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value starlark.Value
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "value", &value); err != nil {
		return nil, err
	}

	switch typed := value.(type) {
	case *starlark.List:
		values := make([]starlark.Value, 0, typed.Len())
		iter := typed.Iterate()
		defer iter.Done()
		var item starlark.Value
		for iter.Next(&item) {
			if isStarlarkEmpty(item) {
				continue
			}
			values = append(values, item)
		}
		return starlark.NewList(values), nil
	case *starlark.Dict:
		result := starlark.NewDict(typed.Len())
		for _, item := range typed.Items() {
			if isStarlarkEmpty(item[1]) {
				continue
			}
			if err := result.SetKey(item[0], item[1]); err != nil {
				return nil, err
			}
		}
		return result, nil
	default:
		return nil, fmt.Errorf("collections.compact expects a list or dict, got %s", value.Type())
	}
}

func starlarkFlatten(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value *starlark.List
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "value", &value); err != nil {
		return nil, err
	}

	values := make([]starlark.Value, 0, value.Len())
	iter := value.Iterate()
	defer iter.Done()
	var item starlark.Value
	for iter.Next(&item) {
		switch nested := item.(type) {
		case *starlark.List:
			nestedIter := nested.Iterate()
			defer nestedIter.Done()
			var nestedItem starlark.Value
			for nestedIter.Next(&nestedItem) {
				values = append(values, nestedItem)
			}
		case starlark.Tuple:
			for _, nestedItem := range nested {
				values = append(values, nestedItem)
			}
		default:
			values = append(values, nested)
		}
	}
	return starlark.NewList(values), nil
}

func starlarkWorkflowStepOutput(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &name); err != nil {
		return nil, err
	}

	runtimeCtx, _ := thread.Local(scriptThreadLocalKey).(*scriptRuntimeContext)
	if runtimeCtx == nil {
		return starlark.None, nil
	}
	value, ok := lookupPathValue(runtimeCtx.context, "steps."+name)
	if !ok {
		return starlark.None, nil
	}
	return goToStarlarkValue(value)
}

func starlarkWorkflowSignal(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "name", &name); err != nil {
		return nil, err
	}

	runtimeCtx, _ := thread.Local(scriptThreadLocalKey).(*scriptRuntimeContext)
	if runtimeCtx == nil {
		return starlark.None, nil
	}
	value, ok := lookupPathValue(runtimeCtx.context, "signals."+name)
	if !ok {
		return starlark.None, nil
	}
	return goToStarlarkValue(value)
}

func starlarkWorkflowFail(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var message string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "message", &message); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("workflow.fail: %s", message)
}

func starlarkAssertNonEmpty(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value starlark.Value
	var message string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "value", &value, "message?", &message); err != nil {
		return nil, err
	}
	if !isStarlarkEmpty(value) {
		return starlark.True, nil
	}
	if message == "" {
		message = "value must not be empty"
	}
	return nil, fmt.Errorf("asserts.non_empty: %s", message)
}

func starlarkAssertEquals(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var left, right starlark.Value
	var message string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "left", &left, "right", &right, "message?", &message); err != nil {
		return nil, err
	}
	equal, err := starlark.Equal(left, right)
	if err != nil {
		return nil, err
	}
	if equal {
		return starlark.True, nil
	}
	if message == "" {
		message = fmt.Sprintf("%s != %s", left, right)
	}
	return nil, fmt.Errorf("asserts.equals: %s", message)
}

func isStarlarkEmpty(value starlark.Value) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case starlark.NoneType:
		return true
	case starlark.String:
		return typed.GoString() == ""
	case *starlark.List:
		return typed.Len() == 0
	case starlark.Tuple:
		return len(typed) == 0
	case *starlark.Dict:
		return typed.Len() == 0
	default:
		return false
	}
}

func goToStarlarkValue(value any) (starlark.Value, error) {
	switch typed := value.(type) {
	case nil:
		return starlark.None, nil
	case starlark.Value:
		return typed, nil
	case bool:
		return starlark.Bool(typed), nil
	case string:
		return starlark.String(typed), nil
	case []byte:
		return starlark.String(string(typed)), nil
	case int:
		return starlark.MakeInt(typed), nil
	case int8:
		return starlark.MakeInt64(int64(typed)), nil
	case int16:
		return starlark.MakeInt64(int64(typed)), nil
	case int32:
		return starlark.MakeInt64(int64(typed)), nil
	case int64:
		return starlark.MakeInt64(typed), nil
	case uint:
		return starlark.MakeUint64(uint64(typed)), nil
	case uint8:
		return starlark.MakeUint64(uint64(typed)), nil
	case uint16:
		return starlark.MakeUint64(uint64(typed)), nil
	case uint32:
		return starlark.MakeUint64(uint64(typed)), nil
	case uint64:
		return starlark.MakeUint64(typed), nil
	case float32:
		return starlark.Float(typed), nil
	case float64:
		return starlark.Float(typed), nil
	case []any:
		values := make([]starlark.Value, 0, len(typed))
		for _, item := range typed {
			converted, err := goToStarlarkValue(item)
			if err != nil {
				return nil, err
			}
			values = append(values, converted)
		}
		return starlark.NewList(values), nil
	case map[string]any:
		dict := starlark.NewDict(len(typed))
		for key, item := range typed {
			converted, err := goToStarlarkValue(item)
			if err != nil {
				return nil, err
			}
			if err := dict.SetKey(starlark.String(key), converted); err != nil {
				return nil, err
			}
		}
		return dict, nil
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("unsupported script value %T", value)
		}
		var decoded any
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return nil, fmt.Errorf("normalize script value %T: %w", value, err)
		}
		return goToStarlarkValue(decoded)
	}
}

func starlarkToJSONValue(value starlark.Value) (any, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(typed), nil
	case starlark.String:
		return string(typed), nil
	case starlark.Bytes:
		return string(typed), nil
	case starlark.Int:
		if v, ok := typed.Int64(); ok {
			return v, nil
		}
		return typed.String(), nil
	case starlark.Float:
		return float64(typed), nil
	case *starlark.List:
		result := make([]any, 0, typed.Len())
		iter := typed.Iterate()
		defer iter.Done()
		var item starlark.Value
		for iter.Next(&item) {
			converted, err := starlarkToJSONValue(item)
			if err != nil {
				return nil, err
			}
			result = append(result, converted)
		}
		return result, nil
	case starlark.Tuple:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			converted, err := starlarkToJSONValue(item)
			if err != nil {
				return nil, err
			}
			result = append(result, converted)
		}
		return result, nil
	case *starlark.Dict:
		result := make(map[string]any, typed.Len())
		for _, item := range typed.Items() {
			key, ok := starlark.AsString(item[0])
			if !ok {
				return nil, fmt.Errorf("script output dict key %s is not a string", item[0].Type())
			}
			converted, err := starlarkToJSONValue(item[1])
			if err != nil {
				return nil, err
			}
			result[key] = converted
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported script output type %s", value.Type())
	}
}
