package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

func (s *Service) validateStartInput(ctx context.Context, definition DefinitionDocument, input map[string]any) error {
	if strings.TrimSpace(definition.StartSchemaID) == "" {
		return nil
	}
	schema, err := s.GetJSONSchema(ctx, definition.StartSchemaID)
	if err != nil {
		return fmt.Errorf("load start schema: %w", err)
	}
	var schemaDoc map[string]any
	if err := json.Unmarshal(schema.Schema, &schemaDoc); err != nil {
		return fmt.Errorf("decode start schema: %w", err)
	}
	if input == nil {
		input = map[string]any{}
	}
	if err := validateJSONSchemaValue(schemaDoc, input, "input"); err != nil {
		return fmt.Errorf("start input does not match schema %q: %w", schema.Name, err)
	}
	return nil
}

func (s *Service) validateStartInputTx(ctx context.Context, tx *sql.Tx, definition DefinitionDocument, input map[string]any) error {
	if strings.TrimSpace(definition.StartSchemaID) == "" {
		return nil
	}
	row := s.queryRowTx(ctx, tx, `
		SELECT id, name, description, schema_json, created_at, updated_at
		FROM json_schemas WHERE id = ?
	`, definition.StartSchemaID)
	schema, err := scanJSONSchema(row)
	if err != nil {
		return fmt.Errorf("load start schema: %w", err)
	}
	var schemaDoc map[string]any
	if err := json.Unmarshal(schema.Schema, &schemaDoc); err != nil {
		return fmt.Errorf("decode start schema: %w", err)
	}
	if input == nil {
		input = map[string]any{}
	}
	if err := validateJSONSchemaValue(schemaDoc, input, "input"); err != nil {
		return fmt.Errorf("start input does not match schema %q: %w", schema.Name, err)
	}
	return nil
}

func validateJSONSchemaValue(schema map[string]any, value any, path string) error {
	if ref, _ := schema["$ref"].(string); strings.TrimSpace(ref) != "" {
		return nil
	}
	if err := validateEnumValue(schema["enum"], value, path); err != nil {
		return err
	}
	schemaType, _ := schema["type"].(string)
	return validateJSONSchemaType(schemaType, schema, value, path)
}

func validateEnumValue(rawEnum, value any, path string) error {
	if enum, ok := rawEnum.([]any); ok && len(enum) > 0 {
		matched := false
		for _, allowed := range enum {
			if valuesEqualJSON(allowed, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s must be one of the allowed enum values", path)
		}
	}
	return nil
}

func validateJSONSchemaType(schemaType string, schema map[string]any, value any, path string) error {
	switch schemaType {
	case "", "object":
		return validateObjectValue(schemaType, schema, value, path)
	case "array":
		return validateArrayValue(schema, value, path)
	case "string":
		return validateStringValue(schema, value, path)
	case "number":
		return validateNumericValue(schema, value, path, false)
	case "integer":
		return validateNumericValue(schema, value, path, true)
	case "boolean":
		return validateBooleanValue(value, path)
	case "null":
		return validateNullValue(value, path)
	}
	return nil
}

func validateObjectValue(schemaType string, schema map[string]any, value any, path string) error {
	if schemaType == "" && schema["properties"] == nil && schema["required"] == nil {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be an object", path)
	}
	return validateObjectSchema(schema, object, path)
}

func validateArrayValue(schema map[string]any, value any, path string) error {
	items, ok := value.([]any)
	if !ok {
		return fmt.Errorf("%s must be an array", path)
	}
	return validateArraySchema(schema, items, path)
}

func validateStringValue(schema map[string]any, value any, path string) error {
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s must be a string", path)
	}
	return validateStringSchema(schema, text, path)
}

func validateNumericValue(schema map[string]any, value any, path string, integer bool) error {
	number, ok := numberValue(value)
	if !ok || integer && math.Trunc(number) != number {
		if integer {
			return fmt.Errorf("%s must be an integer", path)
		}
		return fmt.Errorf("%s must be a number", path)
	}
	return validateNumberSchema(schema, number, path)
}

func validateBooleanValue(value any, path string) error {
	if _, ok := value.(bool); !ok {
		return fmt.Errorf("%s must be a boolean", path)
	}
	return nil
}

func validateNullValue(value any, path string) error {
	if value != nil {
		return fmt.Errorf("%s must be null", path)
	}
	return nil
}

func validateObjectSchema(schema map[string]any, object map[string]any, path string) error {
	properties, _ := schema["properties"].(map[string]any)
	for _, raw := range arrayOfStrings(schema["required"]) {
		if _, ok := object[raw]; !ok {
			return fmt.Errorf("%s.%s is required", path, raw)
		}
	}
	if min, ok := numberKeyword(schema, "minProperties"); ok && float64(len(object)) < min {
		return fmt.Errorf("%s must have at least %d properties", path, int(min))
	}
	if max, ok := numberKeyword(schema, "maxProperties"); ok && float64(len(object)) > max {
		return fmt.Errorf("%s must have at most %d properties", path, int(max))
	}
	for key, item := range object {
		child, ok := properties[key].(map[string]any)
		if !ok {
			if schema["additionalProperties"] == false {
				return fmt.Errorf("%s.%s is not allowed", path, key)
			}
			continue
		}
		if err := validateJSONSchemaValue(child, item, path+"."+key); err != nil {
			return err
		}
	}
	return nil
}

func validateArraySchema(schema map[string]any, items []any, path string) error {
	if min, ok := numberKeyword(schema, "minItems"); ok && float64(len(items)) < min {
		return fmt.Errorf("%s must have at least %d items", path, int(min))
	}
	if max, ok := numberKeyword(schema, "maxItems"); ok && float64(len(items)) > max {
		return fmt.Errorf("%s must have at most %d items", path, int(max))
	}
	itemSchema, _ := schema["items"].(map[string]any)
	if itemSchema == nil {
		return nil
	}
	for index, item := range items {
		if err := validateJSONSchemaValue(itemSchema, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
			return err
		}
	}
	return nil
}

func validateStringSchema(schema map[string]any, text, path string) error {
	if min, ok := numberKeyword(schema, "minLength"); ok && float64(len([]rune(text))) < min {
		return fmt.Errorf("%s is shorter than minLength %d", path, int(min))
	}
	if max, ok := numberKeyword(schema, "maxLength"); ok && float64(len([]rune(text))) > max {
		return fmt.Errorf("%s is longer than maxLength %d", path, int(max))
	}
	if pattern, _ := schema["pattern"].(string); pattern != "" {
		matched, err := regexp.MatchString(pattern, text)
		if err != nil {
			return fmt.Errorf("%s has invalid schema pattern", path)
		}
		if !matched {
			return fmt.Errorf("%s does not match pattern", path)
		}
	}
	return nil
}

func validateNumberSchema(schema map[string]any, value float64, path string) error {
	if min, ok := numberKeyword(schema, "minimum"); ok && value < min {
		return fmt.Errorf("%s is below minimum %v", path, min)
	}
	if max, ok := numberKeyword(schema, "maximum"); ok && value > max {
		return fmt.Errorf("%s is above maximum %v", path, max)
	}
	if min, ok := numberKeyword(schema, "exclusiveMinimum"); ok && value <= min {
		return fmt.Errorf("%s must be greater than %v", path, min)
	}
	if max, ok := numberKeyword(schema, "exclusiveMaximum"); ok && value >= max {
		return fmt.Errorf("%s must be less than %v", path, max)
	}
	if multipleOf, ok := numberKeyword(schema, "multipleOf"); ok && multipleOf > 0 {
		remainder := math.Mod(value, multipleOf)
		if remainder > 1e-9 && math.Abs(remainder-multipleOf) > 1e-9 {
			return fmt.Errorf("%s must be a multiple of %v", path, multipleOf)
		}
	}
	return nil
}

func numberKeyword(schema map[string]any, key string) (float64, bool) {
	return numberValue(schema[key])
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func arrayOfStrings(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func valuesEqualJSON(left, right any) bool {
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return string(leftJSON) == string(rightJSON)
}
