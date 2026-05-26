package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var templateTokenPattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

func decodeJSONObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
		return map[string]any{}
	}
	return payload
}

func encodeJSONObject(payload map[string]any) (json.RawMessage, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

func decodeJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func setPathValue(root map[string]any, path string, value any) {
	parts := strings.Split(strings.TrimSpace(strings.Trim(path, ".")), ".")
	if len(parts) == 0 || parts[0] == "" {
		return
	}

	current := root
	for _, rawPart := range parts[:len(parts)-1] {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			return
		}
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	lastPart := strings.TrimSpace(parts[len(parts)-1])
	if lastPart == "" {
		return
	}
	current[lastPart] = value
}

func lookupPathValue(root any, path string) (any, bool) {
	parts := strings.Split(strings.TrimSpace(strings.Trim(path, ".")), ".")
	current := root
	for _, rawPart := range parts {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			return nil, false
		}
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[part]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func stringifyTemplateValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool, float64, int, int64, uint64:
		return fmt.Sprint(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

func resolveTemplateValue(value any, context map[string]any) any {
	switch typed := value.(type) {
	case string:
		matches := templateTokenPattern.FindAllStringSubmatch(typed, -1)
		if len(matches) == 0 {
			return typed
		}
		if len(matches) == 1 && strings.TrimSpace(matches[0][0]) == strings.TrimSpace(typed) {
			resolved, ok := lookupPathValue(context, matches[0][1])
			if ok {
				return resolved
			}
			return typed
		}
		return templateTokenPattern.ReplaceAllStringFunc(typed, func(token string) string {
			match := templateTokenPattern.FindStringSubmatch(token)
			if len(match) < 2 {
				return token
			}
			resolved, ok := lookupPathValue(context, match[1])
			if !ok {
				return token
			}
			return stringifyTemplateValue(resolved)
		})
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, resolveTemplateValue(item, context))
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = resolveTemplateValue(item, context)
		}
		return result
	default:
		return value
	}
}

func resolveStepInput(raw json.RawMessage, contextRaw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode step input for templating: %w", err)
	}
	context := decodeJSONObject(contextRaw)
	resolved := resolveTemplateValue(payload, context)
	encoded, err := json.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("encode resolved step input: %w", err)
	}
	return json.RawMessage(encoded), nil
}

func applyStepOutputToContext(contextRaw json.RawMessage, stepName string, output json.RawMessage) (json.RawMessage, error) {
	context := decodeJSONObject(contextRaw)
	outputValue := decodeJSONValue(output)
	setPathValue(context, "last", outputValue)
	setPathValue(context, "steps."+stepName, outputValue)
	return encodeJSONObject(context)
}

func applyActivityResultToContext(contextRaw json.RawMessage, stepName string, output json.RawMessage, updates map[string]any) (json.RawMessage, error) {
	context := decodeJSONObject(contextRaw)
	for path, value := range updates {
		setPathValue(context, path, value)
	}
	outputValue := decodeJSONValue(output)
	setPathValue(context, "last", outputValue)
	setPathValue(context, "steps."+stepName, outputValue)
	return encodeJSONObject(context)
}

func applySignalToContext(contextRaw json.RawMessage, signalName string, payload json.RawMessage, now time.Time) (json.RawMessage, error) {
	context := decodeJSONObject(contextRaw)
	signals, _ := context["signals"].(map[string]any)
	if signals == nil {
		signals = map[string]any{}
		context["signals"] = signals
	}
	current, _ := signals[signalName].(map[string]any)
	if current == nil {
		current = map[string]any{}
	}
	count, _ := current["count"].(float64)
	current["count"] = count + 1
	current["lastPayload"] = decodeJSONValue(payload)
	current["receivedAt"] = formatTime(now)
	signals[signalName] = current
	return encodeJSONObject(context)
}

func validateTransitionCondition(stepName string, condition TransitionCondition) error {
	switch condition.Operator {
	case "eq", "neq", "exists", "not_exists", "truthy", "falsy":
		return nil
	default:
		return fmt.Errorf("step %q has unsupported transition operator %q", stepName, condition.Operator)
	}
}

func resolveNextStep(steps []StepDefinition, currentIndex int, contextRaw json.RawMessage) (int, *StepTransition, error) {
	if currentIndex < 0 || currentIndex >= len(steps) {
		return -1, nil, fmt.Errorf("step index %d out of range", currentIndex)
	}

	step := steps[currentIndex]
	if step.Transitions == nil {
		// No transitions defined: use linear index-based fallback (backward compat).
		nextIndex := currentIndex + 1
		if nextIndex >= len(steps) {
			return -1, nil, nil
		}
		return nextIndex, nil, nil
	}
	if len(step.Transitions) == 0 {
		// Explicit empty transitions: step is a terminal node.
		return -1, nil, nil
	}

	indexByName := make(map[string]int, len(steps))
	for idx, candidate := range steps {
		indexByName[candidate.Name] = idx
	}

	var defaultTransition *StepTransition
	context := decodeJSONObject(contextRaw)
	for i := range step.Transitions {
		transition := &step.Transitions[i]
		if transition.Condition == nil {
			defaultTransition = transition
			continue
		}
		matched, err := transitionMatches(context, *transition.Condition)
		if err != nil {
			return -1, nil, fmt.Errorf("evaluate transition from step %q to %q: %w", step.Name, transition.To, err)
		}
		if matched {
			nextIndex, ok := indexByName[transition.To]
			if !ok {
				return -1, nil, fmt.Errorf("transition target %q not found", transition.To)
			}
			return nextIndex, transition, nil
		}
	}

	if defaultTransition != nil {
		nextIndex, ok := indexByName[defaultTransition.To]
		if !ok {
			return -1, nil, fmt.Errorf("transition target %q not found", defaultTransition.To)
		}
		return nextIndex, defaultTransition, nil
	}

	return -1, nil, fmt.Errorf("step %q completed but no transition matched workflow context", step.Name)
}

func transitionMatches(context map[string]any, condition TransitionCondition) (bool, error) {
	value, found := lookupPathValue(context, condition.Path)
	switch condition.Operator {
	case "exists":
		return found, nil
	case "not_exists":
		return !found, nil
	case "truthy":
		return isTruthy(value), nil
	case "falsy":
		return !isTruthy(value), nil
	case "eq", "neq":
		expected := decodeJSONValue(condition.Value)
		actualJSON, err := json.Marshal(value)
		if err != nil {
			return false, fmt.Errorf("marshal transition actual value: %w", err)
		}
		expectedJSON, err := json.Marshal(expected)
		if err != nil {
			return false, fmt.Errorf("marshal transition expected value: %w", err)
		}
		matched := string(actualJSON) == string(expectedJSON)
		if condition.Operator == "neq" {
			return !matched, nil
		}
		return matched, nil
	default:
		return false, fmt.Errorf("unsupported transition operator %q", condition.Operator)
	}
}

func isTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}
