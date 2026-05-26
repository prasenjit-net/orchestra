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

func (s *Service) CreateDefinition(ctx context.Context, input CreateDefinitionInput) (DefinitionDetails, error) {
	document, err := s.normalizeDefinition(input)
	if err != nil {
		return DefinitionDetails{}, err
	}

	now := time.Now().UTC()
	definitionID := generateID("def")
	documentJSON, err := json.Marshal(document)
	if err != nil {
		return DefinitionDetails{}, fmt.Errorf("encode definition document: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DefinitionDetails{}, fmt.Errorf("begin definition transaction: %w", err)
	}
	defer tx.Rollback()

	timestamp := formatTime(now)
	if _, err := tx.ExecContext(ctx, s.rebind(`
		INSERT INTO workflow_definitions (id, name, description, status, active_version, created_at, updated_at)
		VALUES (?, ?, ?, 'published', 1, ?, ?)
	`), definitionID, document.Name, document.Description, timestamp, timestamp); err != nil {
		return DefinitionDetails{}, fmt.Errorf("insert workflow definition: %w", err)
	}

	if _, err := tx.ExecContext(ctx, s.rebind(`
		INSERT INTO workflow_definition_versions (definition_id, version, status, document_json, created_at, updated_at, published_at)
		VALUES (?, 1, 'published', ?, ?, ?, ?)
	`), definitionID, string(documentJSON), timestamp, timestamp, timestamp); err != nil {
		return DefinitionDetails{}, fmt.Errorf("insert workflow definition version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return DefinitionDetails{}, fmt.Errorf("commit definition transaction: %w", err)
	}

	result := DefinitionDetails{
		DefinitionSummary: DefinitionSummary{
			ID:            definitionID,
			Name:          document.Name,
			Description:   document.Description,
			Status:        "published",
			ActiveVersion: 1,
			LatestVersion: 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		Document: document,
		Versions: []DefinitionVersionSummary{{
			Version:     1,
			Status:      "published",
			CreatedAt:   now,
			UpdatedAt:   now,
			PublishedAt: &now,
		}},
	}
	s.emitLiveEvent("definition.updated", "definition", definitionID, result.DefinitionSummary)
	return result, nil
}

func (s *Service) CreateDefinitionVersion(ctx context.Context, definitionID string, input CreateDefinitionInput) (DefinitionDetails, error) {
	document, err := s.normalizeDefinition(input)
	if err != nil {
		return DefinitionDetails{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DefinitionDetails{}, fmt.Errorf("begin definition version transaction: %w", err)
	}
	defer tx.Rollback()

	definition, err := s.getDefinitionSummaryTx(ctx, tx, definitionID)
	if err != nil {
		return DefinitionDetails{}, err
	}
	if definition.DraftVersion > 0 {
		return DefinitionDetails{}, fmt.Errorf("definition %s already has a draft version %d", definitionID, definition.DraftVersion)
	}

	nextVersion := definition.LatestVersion + 1
	now := time.Now().UTC()
	timestamp := formatTime(now)
	documentJSON, err := json.Marshal(document)
	if err != nil {
		return DefinitionDetails{}, fmt.Errorf("encode definition version document: %w", err)
	}

	if _, err := tx.ExecContext(ctx, s.rebind(`
		INSERT INTO workflow_definition_versions (definition_id, version, status, document_json, created_at, updated_at, published_at)
		VALUES (?, ?, 'draft', ?, ?, ?, NULL)
	`), definitionID, nextVersion, string(documentJSON), timestamp, timestamp); err != nil {
		return DefinitionDetails{}, fmt.Errorf("insert draft definition version: %w", err)
	}

	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_definitions
		SET status = 'draft', updated_at = ?
		WHERE id = ?
	`), timestamp, definitionID); err != nil {
		return DefinitionDetails{}, fmt.Errorf("update definition draft status: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return DefinitionDetails{}, fmt.Errorf("commit definition version transaction: %w", err)
	}

	result, err := s.GetDefinition(ctx, definitionID)
	if err != nil {
		return DefinitionDetails{}, err
	}
	s.emitLiveEvent("definition.updated", "definition", definitionID, result.DefinitionSummary)
	return result, nil
}

func (s *Service) PublishDefinitionVersion(ctx context.Context, definitionID string, version int) (DefinitionDetails, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DefinitionDetails{}, fmt.Errorf("begin publish transaction: %w", err)
	}
	defer tx.Rollback()

	definition, err := s.getDefinitionSummaryTx(ctx, tx, definitionID)
	if err != nil {
		return DefinitionDetails{}, err
	}
	if version <= 0 {
		version = definition.DraftVersion
	}
	if version <= 0 {
		return DefinitionDetails{}, fmt.Errorf("definition %s has no draft version to publish", definitionID)
	}

	currentVersion, err := s.getDefinitionVersionMetaTx(ctx, tx, definitionID, version)
	if err != nil {
		return DefinitionDetails{}, err
	}
	if currentVersion.Status != "draft" {
		return DefinitionDetails{}, fmt.Errorf("definition version %d is not a draft", version)
	}
	document, err := s.getDefinitionVersionDocumentTx(ctx, tx, definitionID, version)
	if err != nil {
		return DefinitionDetails{}, err
	}

	now := time.Now().UTC()
	timestamp := formatTime(now)

	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_definition_versions
		SET status = 'archived', updated_at = ?
		WHERE definition_id = ? AND status = 'published'
	`), timestamp, definitionID); err != nil {
		return DefinitionDetails{}, fmt.Errorf("archive current published definition version: %w", err)
	}

	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_definition_versions
		SET status = 'published', updated_at = ?, published_at = ?
		WHERE definition_id = ? AND version = ?
	`), timestamp, timestamp, definitionID, version); err != nil {
		return DefinitionDetails{}, fmt.Errorf("publish definition version: %w", err)
	}

	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_definitions
		SET name = ?, description = ?, active_version = ?, status = 'published', updated_at = ?
		WHERE id = ?
	`), document.Name, document.Description, version, timestamp, definitionID); err != nil {
		return DefinitionDetails{}, fmt.Errorf("update active definition version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return DefinitionDetails{}, fmt.Errorf("commit publish transaction: %w", err)
	}

	result, err := s.GetDefinition(ctx, definitionID)
	if err != nil {
		return DefinitionDetails{}, err
	}
	s.emitLiveEvent("definition.published", "definition", definitionID, result.DefinitionSummary)
	return result, nil
}

func (s *Service) ListDefinitions(ctx context.Context) ([]DefinitionSummary, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`
		SELECT d.id, d.name, d.description, d.status, d.active_version,
		       COALESCE(MAX(v.version), d.active_version) AS latest_version,
		       COALESCE(MAX(CASE WHEN v.status = 'draft' THEN v.version END), 0) AS draft_version,
		       d.created_at, d.updated_at
		FROM workflow_definitions d
		LEFT JOIN workflow_definition_versions v ON v.definition_id = d.id
		GROUP BY d.id, d.name, d.description, d.status, d.active_version, d.created_at, d.updated_at
		ORDER BY d.updated_at DESC, d.created_at DESC
	`))
	if err != nil {
		return nil, fmt.Errorf("query workflow definitions: %w", err)
	}
	defer rows.Close()

	var definitions []DefinitionSummary
	for rows.Next() {
		definition, err := scanDefinitionSummary(rows)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow definitions: %w", err)
	}

	return definitions, nil
}

func (s *Service) GetDefinition(ctx context.Context, definitionID string) (DefinitionDetails, error) {
	row := s.db.QueryRowContext(ctx, s.rebind(`
		SELECT d.id, d.name, d.description, d.status, d.active_version,
		       COALESCE(MAX(v_all.version), d.active_version) AS latest_version,
		       COALESCE(MAX(CASE WHEN v_all.status = 'draft' THEN v_all.version END), 0) AS draft_version,
		       d.created_at, d.updated_at, v.document_json
		FROM workflow_definitions d
		JOIN workflow_definition_versions v
		  ON v.definition_id = d.id AND v.version = d.active_version
		LEFT JOIN workflow_definition_versions v_all ON v_all.definition_id = d.id
		WHERE d.id = ?
		GROUP BY d.id, d.name, d.description, d.status, d.active_version, d.created_at, d.updated_at, v.document_json
	`), definitionID)

	var (
		documentJSON string
		createdAt    string
		updatedAt    string
		details      DefinitionDetails
	)

	if err := row.Scan(
		&details.ID,
		&details.Name,
		&details.Description,
		&details.Status,
		&details.ActiveVersion,
		&details.LatestVersion,
		&details.DraftVersion,
		&createdAt,
		&updatedAt,
		&documentJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DefinitionDetails{}, ErrNotFound
		}
		return DefinitionDetails{}, fmt.Errorf("load workflow definition: %w", err)
	}

	details.CreatedAt = mustParseTime(createdAt)
	details.UpdatedAt = mustParseTime(updatedAt)
	if err := json.Unmarshal([]byte(documentJSON), &details.Document); err != nil {
		return DefinitionDetails{}, fmt.Errorf("decode workflow definition: %w", err)
	}
	details.Name = details.Document.Name
	details.Description = details.Document.Description
	versions, err := s.listDefinitionVersions(ctx, definitionID)
	if err != nil {
		return DefinitionDetails{}, err
	}
	details.Versions = versions

	return details, nil
}

func (s *Service) GetDefinitionVersion(ctx context.Context, definitionID string, version int) (DefinitionDetails, error) {
	row := s.db.QueryRowContext(ctx, s.rebind(`
		SELECT d.id, d.name, d.description, d.status, d.active_version,
		       COALESCE(MAX(v_all.version), d.active_version) AS latest_version,
		       COALESCE(MAX(CASE WHEN v_all.status = 'draft' THEN v_all.version END), 0) AS draft_version,
		       d.created_at, d.updated_at, v.document_json
		FROM workflow_definitions d
		JOIN workflow_definition_versions v
		  ON v.definition_id = d.id AND v.version = ?
		LEFT JOIN workflow_definition_versions v_all ON v_all.definition_id = d.id
		WHERE d.id = ?
		GROUP BY d.id, d.name, d.description, d.status, d.active_version, d.created_at, d.updated_at, v.document_json
	`), version, definitionID)

	var (
		documentJSON string
		createdAt    string
		updatedAt    string
		details      DefinitionDetails
	)

	if err := row.Scan(
		&details.ID,
		&details.Name,
		&details.Description,
		&details.Status,
		&details.ActiveVersion,
		&details.LatestVersion,
		&details.DraftVersion,
		&createdAt,
		&updatedAt,
		&documentJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DefinitionDetails{}, ErrNotFound
		}
		return DefinitionDetails{}, fmt.Errorf("load workflow definition version: %w", err)
	}

	details.CreatedAt = mustParseTime(createdAt)
	details.UpdatedAt = mustParseTime(updatedAt)
	if err := json.Unmarshal([]byte(documentJSON), &details.Document); err != nil {
		return DefinitionDetails{}, fmt.Errorf("decode workflow definition version: %w", err)
	}
	details.Name = details.Document.Name
	details.Description = details.Document.Description
	versions, err := s.listDefinitionVersions(ctx, definitionID)
	if err != nil {
		return DefinitionDetails{}, err
	}
	details.Versions = versions

	return details, nil
}

func (s *Service) normalizeDefinition(input CreateDefinitionInput) (DefinitionDocument, error) {
	document := DefinitionDocument{
		Name:        strings.TrimSpace(input.Name),
		Description: strings.TrimSpace(input.Description),
		Steps:       make([]StepDefinition, 0, len(input.Steps)),
	}

	if document.Name == "" {
		return DefinitionDocument{}, fmt.Errorf("workflow definition name is required")
	}
	if len(input.Steps) == 0 {
		return DefinitionDocument{}, fmt.Errorf("workflow definition requires at least one step")
	}

	seenNames := make(map[string]struct{}, len(input.Steps))
	for i, step := range input.Steps {
		normalized := StepDefinition{
			Name:     strings.TrimSpace(step.Name),
			Activity: strings.TrimSpace(step.Activity),
			Input:    step.Input,
			Retry:    step.Retry,
			Layout:   step.Layout,
		}
		if step.Transitions != nil {
			normalized.Transitions = make([]StepTransition, 0, len(step.Transitions))
		}
		if normalized.Name == "" {
			return DefinitionDocument{}, fmt.Errorf("step %d requires a name", i)
		}
		if normalized.Activity == "" {
			return DefinitionDocument{}, fmt.Errorf("step %q requires an activity", normalized.Name)
		}
		if _, ok := s.activities[normalized.Activity]; !ok {
			return DefinitionDocument{}, fmt.Errorf("step %q references unknown activity %q", normalized.Name, normalized.Activity)
		}
		if _, exists := seenNames[normalized.Name]; exists {
			return DefinitionDocument{}, fmt.Errorf("step name %q must be unique", normalized.Name)
		}
		seenNames[normalized.Name] = struct{}{}
		if normalized.Retry.MaxAttempts <= 0 {
			normalized.Retry.MaxAttempts = 1
		}
		if normalized.Retry.BackoffSeconds < 0 {
			return DefinitionDocument{}, fmt.Errorf("step %q has invalid backoffSeconds", normalized.Name)
		}
		if len(normalized.Input) == 0 {
			normalized.Input = json.RawMessage(`{}`)
		}
		for _, transition := range step.Transitions {
			next := StepTransition{
				To:    strings.TrimSpace(transition.To),
				Label: strings.TrimSpace(transition.Label),
			}
			if next.To == "" {
				return DefinitionDocument{}, fmt.Errorf("step %q has a transition with no target", normalized.Name)
			}
			if transition.Condition != nil {
				next.Condition = &TransitionCondition{
					Path:     strings.TrimSpace(transition.Condition.Path),
					Operator: strings.TrimSpace(strings.ToLower(transition.Condition.Operator)),
					Value:    transition.Condition.Value,
				}
				if next.Condition.Path == "" {
					return DefinitionDocument{}, fmt.Errorf("step %q has a transition with no condition path", normalized.Name)
				}
				if next.Condition.Operator == "" {
					next.Condition.Operator = "eq"
				}
			}
			normalized.Transitions = append(normalized.Transitions, next)
		}
		document.Steps = append(document.Steps, normalized)
	}

	stepNames := make(map[string]struct{}, len(document.Steps))
	for _, step := range document.Steps {
		stepNames[step.Name] = struct{}{}
	}
	for _, step := range document.Steps {
		defaultTransitions := 0
		for _, transition := range step.Transitions {
			if _, ok := stepNames[transition.To]; !ok {
				return DefinitionDocument{}, fmt.Errorf("step %q references unknown transition target %q", step.Name, transition.To)
			}
			if transition.Condition == nil {
				defaultTransitions++
			} else if err := validateTransitionCondition(step.Name, *transition.Condition); err != nil {
				return DefinitionDocument{}, err
			}
		}
		if defaultTransitions > 1 {
			return DefinitionDocument{}, fmt.Errorf("step %q can only have one default transition", step.Name)
		}
	}

	return document, nil
}

func (s *Service) listDefinitionVersions(ctx context.Context, definitionID string) ([]DefinitionVersionSummary, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`
		SELECT version, status, created_at, updated_at, published_at
		FROM workflow_definition_versions
		WHERE definition_id = ?
		ORDER BY version DESC
	`), definitionID)
	if err != nil {
		return nil, fmt.Errorf("query workflow definition versions: %w", err)
	}
	defer rows.Close()

	var versions []DefinitionVersionSummary
	for rows.Next() {
		version, err := scanDefinitionVersionSummary(rows)
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow definition versions: %w", err)
	}
	return versions, nil
}

func (s *Service) getDefinitionSummaryTx(ctx context.Context, tx *sql.Tx, definitionID string) (DefinitionSummary, error) {
	row := tx.QueryRowContext(ctx, s.rebind(`
		SELECT d.id, d.name, d.description, d.status, d.active_version,
		       COALESCE(MAX(v.version), d.active_version) AS latest_version,
		       COALESCE(MAX(CASE WHEN v.status = 'draft' THEN v.version END), 0) AS draft_version,
		       d.created_at, d.updated_at
		FROM workflow_definitions d
		LEFT JOIN workflow_definition_versions v ON v.definition_id = d.id
		WHERE d.id = ?
		GROUP BY d.id, d.name, d.description, d.status, d.active_version, d.created_at, d.updated_at
	`), definitionID)

	definition, err := scanDefinitionSummary(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DefinitionSummary{}, ErrNotFound
		}
		return DefinitionSummary{}, err
	}
	return definition, nil
}

func (s *Service) getDefinitionVersionMetaTx(ctx context.Context, tx *sql.Tx, definitionID string, version int) (DefinitionVersionSummary, error) {
	row := tx.QueryRowContext(ctx, s.rebind(`
		SELECT version, status, created_at, updated_at, published_at
		FROM workflow_definition_versions
		WHERE definition_id = ? AND version = ?
	`), definitionID, version)

	meta, err := scanDefinitionVersionSummary(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DefinitionVersionSummary{}, ErrNotFound
		}
		return DefinitionVersionSummary{}, err
	}
	return meta, nil
}

func (s *Service) getDefinitionTx(ctx context.Context, tx *sql.Tx, definitionID string) (DefinitionDetails, error) {
	row := tx.QueryRowContext(ctx, s.rebind(`
		SELECT d.id, d.name, d.description, d.status, d.active_version,
		       COALESCE(MAX(v_all.version), d.active_version) AS latest_version,
		       COALESCE(MAX(CASE WHEN v_all.status = 'draft' THEN v_all.version END), 0) AS draft_version,
		       d.created_at, d.updated_at, v.document_json
		FROM workflow_definitions d
		JOIN workflow_definition_versions v
		  ON v.definition_id = d.id AND v.version = d.active_version
		LEFT JOIN workflow_definition_versions v_all ON v_all.definition_id = d.id
		WHERE d.id = ?
		GROUP BY d.id, d.name, d.description, d.status, d.active_version, d.created_at, d.updated_at, v.document_json
	`), definitionID)

	var (
		documentJSON string
		createdAt    string
		updatedAt    string
		details      DefinitionDetails
	)

	if err := row.Scan(
		&details.ID,
		&details.Name,
		&details.Description,
		&details.Status,
		&details.ActiveVersion,
		&details.LatestVersion,
		&details.DraftVersion,
		&createdAt,
		&updatedAt,
		&documentJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DefinitionDetails{}, ErrNotFound
		}
		return DefinitionDetails{}, fmt.Errorf("load workflow definition in transaction: %w", err)
	}

	details.CreatedAt = mustParseTime(createdAt)
	details.UpdatedAt = mustParseTime(updatedAt)
	if err := json.Unmarshal([]byte(documentJSON), &details.Document); err != nil {
		return DefinitionDetails{}, fmt.Errorf("decode workflow definition in transaction: %w", err)
	}
	details.Name = details.Document.Name
	details.Description = details.Document.Description
	details.Versions = nil

	return details, nil
}

func (s *Service) getDefinitionVersionDocumentTx(ctx context.Context, tx *sql.Tx, definitionID string, version int) (DefinitionDocument, error) {
	row := tx.QueryRowContext(ctx, s.rebind(`
		SELECT document_json
		FROM workflow_definition_versions
		WHERE definition_id = ? AND version = ?
	`), definitionID, version)
	var documentJSON string
	if err := row.Scan(&documentJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DefinitionDocument{}, ErrNotFound
		}
		return DefinitionDocument{}, fmt.Errorf("load definition version document: %w", err)
	}
	var document DefinitionDocument
	if err := json.Unmarshal([]byte(documentJSON), &document); err != nil {
		return DefinitionDocument{}, fmt.Errorf("decode definition version document: %w", err)
	}
	return document, nil
}

func scanDefinitionSummary(scanner interface{ Scan(...any) error }) (DefinitionSummary, error) {
	var (
		definition DefinitionSummary
		createdAt  string
		updatedAt  string
	)
	if err := scanner.Scan(
		&definition.ID,
		&definition.Name,
		&definition.Description,
		&definition.Status,
		&definition.ActiveVersion,
		&definition.LatestVersion,
		&definition.DraftVersion,
		&createdAt,
		&updatedAt,
	); err != nil {
		return DefinitionSummary{}, fmt.Errorf("scan workflow definition: %w", err)
	}
	definition.CreatedAt = mustParseTime(createdAt)
	definition.UpdatedAt = mustParseTime(updatedAt)
	return definition, nil
}

func scanDefinitionVersionSummary(scanner interface{ Scan(...any) error }) (DefinitionVersionSummary, error) {
	var (
		version     DefinitionVersionSummary
		createdAt   string
		updatedAt   string
		publishedAt sql.NullString
	)
	if err := scanner.Scan(&version.Version, &version.Status, &createdAt, &updatedAt, &publishedAt); err != nil {
		return DefinitionVersionSummary{}, fmt.Errorf("scan workflow definition version: %w", err)
	}
	version.CreatedAt = mustParseTime(createdAt)
	version.UpdatedAt = mustParseTime(updatedAt)
	if publishedAt.Valid {
		parsed := mustParseTime(publishedAt.String)
		version.PublishedAt = &parsed
	}
	return version, nil
}
