package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type JSONSchema struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type CreateJSONSchemaInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type JSONSchemasResponse struct {
	Schemas []JSONSchema `json:"schemas"`
}

func (s *Service) CreateJSONSchema(ctx context.Context, input CreateJSONSchemaInput) (JSONSchema, error) {
	schema, err := normalizeJSONSchemaInput(input.Schema)
	if err != nil {
		return JSONSchema{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return JSONSchema{}, errors.New("name is required")
	}

	now := time.Now().UTC()
	ts := formatTime(now)
	id := generateID("jsn")
	if _, err := s.db.ExecContext(ctx, s.rebind(`
		INSERT INTO json_schemas (id, name, description, schema_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`), id, name, strings.TrimSpace(input.Description), string(schema), ts, ts); err != nil {
		return JSONSchema{}, fmt.Errorf("insert JSON schema: %w", err)
	}

	item := JSONSchema{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(input.Description),
		Schema:      schema,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.emitLiveEvent("json_schema.updated", "json_schema", id, item)
	return item, nil
}

func (s *Service) ListJSONSchemas(ctx context.Context) ([]JSONSchema, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, schema_json, created_at, updated_at
		FROM json_schemas ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query JSON schemas: %w", err)
	}
	defer rows.Close()

	var schemas []JSONSchema
	for rows.Next() {
		item, err := scanJSONSchema(rows)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate JSON schemas: %w", err)
	}
	if schemas == nil {
		schemas = []JSONSchema{}
	}
	return schemas, nil
}

func (s *Service) GetJSONSchema(ctx context.Context, id string) (JSONSchema, error) {
	row := s.db.QueryRowContext(ctx, s.rebind(`
		SELECT id, name, description, schema_json, created_at, updated_at
		FROM json_schemas WHERE id = ?
	`), id)
	return scanJSONSchema(row)
}

func (s *Service) UpdateJSONSchema(ctx context.Context, id string, input CreateJSONSchemaInput) (JSONSchema, error) {
	schema, err := normalizeJSONSchemaInput(input.Schema)
	if err != nil {
		return JSONSchema{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return JSONSchema{}, errors.New("name is required")
	}

	now := time.Now().UTC()
	ts := formatTime(now)
	res, err := s.db.ExecContext(ctx, s.rebind(`
		UPDATE json_schemas SET name = ?, description = ?, schema_json = ?, updated_at = ?
		WHERE id = ?
	`), name, strings.TrimSpace(input.Description), string(schema), ts, id)
	if err != nil {
		return JSONSchema{}, fmt.Errorf("update JSON schema: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return JSONSchema{}, ErrNotFound
	}
	item, err := s.GetJSONSchema(ctx, id)
	if err != nil {
		return JSONSchema{}, err
	}
	s.emitLiveEvent("json_schema.updated", "json_schema", id, item)
	return item, nil
}

func (s *Service) DeleteJSONSchema(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.rebind(`DELETE FROM json_schemas WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("delete JSON schema: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	s.emitLiveEvent("json_schema.deleted", "json_schema", id, nil)
	return nil
}

type jsonSchemaScanner interface {
	Scan(dest ...any) error
}

func scanJSONSchema(row jsonSchemaScanner) (JSONSchema, error) {
	var item JSONSchema
	var schemaJSON string
	var createdAt, updatedAt string
	if err := row.Scan(&item.ID, &item.Name, &item.Description, &schemaJSON, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return JSONSchema{}, ErrNotFound
		}
		return JSONSchema{}, fmt.Errorf("scan JSON schema: %w", err)
	}
	item.Schema = json.RawMessage(schemaJSON)
	item.CreatedAt = mustParseTime(createdAt)
	item.UpdatedAt = mustParseTime(updatedAt)
	return item, nil
}

func normalizeJSONSchemaInput(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{},"required":[]}`)
	}
	if !json.Valid(raw) {
		return nil, errors.New("schema must be valid JSON")
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, errors.New("schema must be a JSON object")
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("normalize schema: %w", err)
	}
	return json.RawMessage(normalized), nil
}
