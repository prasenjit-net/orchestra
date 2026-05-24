package workflow

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/prasenjit-net/orchestra/internal/config"
	"github.com/prasenjit-net/orchestra/internal/livebus"
)

var ErrNotFound = errors.New("workflow resource not found")

type Service struct {
	db         *sql.DB
	logger     *slog.Logger
	cfg        config.WorkflowConfig
	activities map[string]Activity
	workerID   string
	live       *livebus.Bus
}

func (s *Service) wakeTasksWaitingForSignalTx(ctx context.Context, tx *sql.Tx, workflowID string, signalName string, now time.Time) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
		       run_at, last_error, lease_owner, lease_expires_at, state_json, created_at, updated_at
		FROM workflow_tasks
		WHERE workflow_id = ? AND status = ?
		ORDER BY id ASC
	`, workflowID, StatusWaiting)
	if err != nil {
		return nil, fmt.Errorf("query signal-waiting tasks: %w", err)
	}
	defer rows.Close()

	var wokenIDs []int64
	for rows.Next() {
		task, err := scanWorkflowTask(rows)
		if err != nil {
			return nil, err
		}
		state, initialized, err := decodeWaitSignalState(task.State)
		if err != nil {
			return nil, err
		}
		if !initialized || strings.TrimSpace(state.SignalName) != signalName {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE workflow_tasks
			SET status = ?, run_at = ?, lease_owner = '', lease_expires_at = NULL, updated_at = ?
			WHERE id = ?
		`, StatusPending, formatTime(now), formatTime(now), task.ID); err != nil {
			return nil, fmt.Errorf("wake signal-waiting task %d: %w", task.ID, err)
		}
		wokenIDs = append(wokenIDs, task.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate signal-waiting tasks: %w", err)
	}
	return wokenIDs, nil
}

type workflowSnapshot struct {
	Status          string          `json:"status"`
	CurrentStepName string          `json:"currentStepName"`
	CurrentActivity string          `json:"currentActivity"`
	LastOutput      json.RawMessage `json:"lastOutput,omitempty"`
	LastError       string          `json:"lastError,omitempty"`
	Context         json.RawMessage `json:"context,omitempty"`
	PendingSignals  int             `json:"pendingSignals,omitempty"`
	NextRunAt       *time.Time      `json:"nextRunAt,omitempty"`
}

func parkedSignalRunAt(timeoutAt *time.Time) time.Time {
	if timeoutAt != nil {
		return timeoutAt.UTC()
	}
	return time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)
}

func NewService(cfg config.WorkflowConfig, logger *slog.Logger, buses ...*livebus.Bus) (*Service, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	if err := ensureDatabasePath(cfg.DatabasePath); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open workflow database: %w", err)
	}

	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA foreign_keys = ON`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure workflow database: %w", err)
		}
	}

	live := livebus.New()
	if len(buses) > 0 && buses[0] != nil {
		live = buses[0]
	}

	svc := &Service{
		db:         db,
		logger:     logger.With("component", "workflow"),
		cfg:        cfg,
		activities: make(map[string]Activity),
		workerID:   generateID("worker"),
		live:       live,
	}

	for _, activity := range builtInActivities(cfg, svc.logger) {
		svc.activities[activity.Descriptor().Name] = activity
	}
	// Always register script activity with DB-backed lookup so saved scripts work
	// regardless of the scriptEnabled config flag.
	svc.activities["script"] = newScriptActivity(cfg, svc.lookupScriptSource)
	svc.activities["agent"] = newAgentActivity(cfg, svc.lookupAgent, svc.GetAgentMCPServers)

	if err := svc.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return svc, nil
}

func (s *Service) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Service) SubscribeLiveEvents() (<-chan livebus.Event, func()) {
	if s == nil || s.live == nil {
		ch := make(chan livebus.Event)
		close(ch)
		return ch, func() {}
	}
	return s.live.Subscribe()
}

func (s *Service) emitLiveEvent(eventType, entity, entityID string, payload any) {
	if s == nil || s.live == nil {
		return
	}
	s.live.Publish(livebus.NewEvent(eventType, entity, entityID, payload))
}

func (s *Service) emitOperationEvent(workflowID, eventType string, payload any) {
	s.emitLiveEvent("operation.event", "operation", workflowID, map[string]any{
		"workflowId": workflowID,
		"eventType":  eventType,
		"payload":    payload,
	})
}

func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}

	go func() {
		ticker := time.NewTicker(s.cfg.PollInterval)
		defer ticker.Stop()

		s.runWorkerPass(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runWorkerPass(ctx)
			}
		}
	}()
}

func (s *Service) runWorkerPass(ctx context.Context) {
	if err := s.requeueExpiredTasks(ctx); err != nil {
		s.logger.Error("requeue expired tasks", "error", err)
	}
	for range 16 {
		processed, err := s.RunOnce(ctx)
		if err != nil {
			s.logger.Error("process workflow task", "error", err)
			return
		}
		if !processed {
			return
		}
	}
}

func (s *Service) ListActivities() []ActivityDescriptor {
	if s == nil {
		return nil
	}

	descriptors := make([]ActivityDescriptor, 0, len(s.activities))
	for _, activity := range s.activities {
		descriptors = append(descriptors, activity.Descriptor())
	}
	slices.SortFunc(descriptors, func(a, b ActivityDescriptor) int {
		return strings.Compare(a.Name, b.Name)
	})
	return descriptors
}

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
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workflow_definitions (id, name, description, status, active_version, created_at, updated_at)
		VALUES (?, ?, ?, 'published', 1, ?, ?)
	`, definitionID, document.Name, document.Description, timestamp, timestamp); err != nil {
		return DefinitionDetails{}, fmt.Errorf("insert workflow definition: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workflow_definition_versions (definition_id, version, status, document_json, created_at, updated_at, published_at)
		VALUES (?, 1, 'published', ?, ?, ?, ?)
	`, definitionID, string(documentJSON), timestamp, timestamp, timestamp); err != nil {
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

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workflow_definition_versions (definition_id, version, status, document_json, created_at, updated_at, published_at)
		VALUES (?, ?, 'draft', ?, ?, ?, NULL)
	`, definitionID, nextVersion, string(documentJSON), timestamp, timestamp); err != nil {
		return DefinitionDetails{}, fmt.Errorf("insert draft definition version: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_definitions
		SET status = 'draft', updated_at = ?
		WHERE id = ?
	`, timestamp, definitionID); err != nil {
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

	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_definition_versions
		SET status = 'archived', updated_at = ?
		WHERE definition_id = ? AND status = 'published'
	`, timestamp, definitionID); err != nil {
		return DefinitionDetails{}, fmt.Errorf("archive current published definition version: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_definition_versions
		SET status = 'published', updated_at = ?, published_at = ?
		WHERE definition_id = ? AND version = ?
	`, timestamp, timestamp, definitionID, version); err != nil {
		return DefinitionDetails{}, fmt.Errorf("publish definition version: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_definitions
		SET name = ?, description = ?, active_version = ?, status = 'published', updated_at = ?
		WHERE id = ?
	`, document.Name, document.Description, version, timestamp, definitionID); err != nil {
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.name, d.description, d.status, d.active_version,
		       COALESCE(MAX(v.version), d.active_version) AS latest_version,
		       COALESCE(MAX(CASE WHEN v.status = 'draft' THEN v.version END), 0) AS draft_version,
		       d.created_at, d.updated_at
		FROM workflow_definitions d
		LEFT JOIN workflow_definition_versions v ON v.definition_id = d.id
		GROUP BY d.id, d.name, d.description, d.status, d.active_version, d.created_at, d.updated_at
		ORDER BY d.updated_at DESC, d.created_at DESC
	`)
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
	row := s.db.QueryRowContext(ctx, `
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
	`, definitionID)

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
	row := s.db.QueryRowContext(ctx, `
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
	`, version, definitionID)

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

func (s *Service) StartWorkflow(ctx context.Context, definitionID string) (WorkflowInstance, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowInstance{}, fmt.Errorf("begin workflow transaction: %w", err)
	}
	defer tx.Rollback()

	details, err := s.getDefinitionTx(ctx, tx, definitionID)
	if err != nil {
		return WorkflowInstance{}, err
	}

	now := time.Now().UTC()
	instance := WorkflowInstance{
		ID:                generateID("wf"),
		DefinitionID:      details.ID,
		DefinitionVersion: details.ActiveVersion,
		Status:            StatusRunning,
		CurrentStepIndex:  -1,
		LastEventSequence: 0,
		Context:           buildInitialContext(details.ID, details.ActiveVersion),
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	stepName := ""
	activityName := ""
	if len(details.Document.Steps) > 0 {
		stepName = details.Document.Steps[0].Name
		activityName = details.Document.Steps[0].Activity
		instance.CurrentStepIndex = 0
		instance.CurrentStepName = stepName
		instance.CurrentActivity = activityName
	}

	snapshotJSON, err := buildSnapshotJSON(workflowSnapshot{
		Status:          instance.Status,
		CurrentStepName: stepName,
		CurrentActivity: activityName,
		Context:         instance.Context,
	})
	if err != nil {
		return WorkflowInstance{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workflow_instances (
			id, definition_id, definition_version, status, current_step_index, current_step_name,
			current_activity, snapshot_json, last_event_sequence, last_error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, '', ?, ?)
	`, instance.ID, instance.DefinitionID, instance.DefinitionVersion, instance.Status, instance.CurrentStepIndex, instance.CurrentStepName, instance.CurrentActivity, snapshotJSON, formatTime(now), formatTime(now)); err != nil {
		return WorkflowInstance{}, fmt.Errorf("insert workflow instance: %w", err)
	}

	sequence := 0
	sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "WorkflowStarted", map[string]any{
		"definitionId":      instance.DefinitionID,
		"definitionVersion": instance.DefinitionVersion,
	})
	if err != nil {
		return WorkflowInstance{}, err
	}

	if len(details.Document.Steps) == 0 {
		sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "WorkflowCompleted", map[string]any{
			"reason": "workflow has no steps",
		})
		if err != nil {
			return WorkflowInstance{}, err
		}
		instance.Status = StatusCompleted
		instance.LastEventSequence = sequence
		instance.CurrentStepIndex = -1
		instance.CurrentStepName = ""
		instance.CurrentActivity = ""
		if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
			return WorkflowInstance{}, err
		}
	} else {
		firstStep := details.Document.Steps[0]
		sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "ActivityScheduled", map[string]any{
			"stepIndex":   0,
			"stepName":    firstStep.Name,
			"activity":    firstStep.Activity,
			"maxAttempts": firstStep.Retry.MaxAttempts,
		})
		if err != nil {
			return WorkflowInstance{}, err
		}
		instance.LastEventSequence = sequence
		instance.NextRunAt = &now
		if err := insertTaskTx(ctx, tx, instance.ID, 0, firstStep, now); err != nil {
			return WorkflowInstance{}, err
		}
		if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
			return WorkflowInstance{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return WorkflowInstance{}, fmt.Errorf("commit workflow transaction: %w", err)
	}

	result, err := s.GetWorkflow(ctx, instance.ID)
	if err != nil {
		return WorkflowInstance{}, err
	}
	s.emitLiveEvent("workflow.started", "workflow", result.ID, result)
	s.emitOperationEvent(result.ID, "WorkflowStarted", map[string]any{
		"definitionId":      result.DefinitionID,
		"definitionVersion": result.DefinitionVersion,
	})
	return result, nil
}

func (s *Service) ListWorkflows(ctx context.Context) ([]WorkflowInstance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, definition_id, definition_version, status, current_step_index, current_step_name,
		       current_activity, snapshot_json, last_event_sequence, last_error, created_at, updated_at
		FROM workflow_instances
		ORDER BY updated_at DESC, created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query workflow instances: %w", err)
	}
	defer rows.Close()

	var workflows []WorkflowInstance
	for rows.Next() {
		instance, err := scanWorkflowInstance(rows)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, instance)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow instances: %w", err)
	}

	return workflows, nil
}

func (s *Service) GetWorkflow(ctx context.Context, workflowID string) (WorkflowInstance, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, definition_id, definition_version, status, current_step_index, current_step_name,
		       current_activity, snapshot_json, last_event_sequence, last_error, created_at, updated_at
		FROM workflow_instances
		WHERE id = ?
	`, workflowID)

	instance, err := scanWorkflowInstance(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowInstance{}, ErrNotFound
		}
		return WorkflowInstance{}, err
	}
	return instance, nil
}

func (s *Service) GetWorkflowHistory(ctx context.Context, workflowID string) ([]WorkflowEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence, event_type, payload, created_at
		FROM workflow_events
		WHERE workflow_id = ?
		ORDER BY sequence ASC
	`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("query workflow events: %w", err)
	}
	defer rows.Close()

	var events []WorkflowEvent
	for rows.Next() {
		var (
			event       WorkflowEvent
			payloadText string
			createdAt   string
		)
		if err := rows.Scan(&event.Sequence, &event.EventType, &payloadText, &createdAt); err != nil {
			return nil, fmt.Errorf("scan workflow event: %w", err)
		}
		event.WorkflowID = workflowID
		event.Payload = json.RawMessage(payloadText)
		event.CreatedAt = mustParseTime(createdAt)
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow events: %w", err)
	}

	return events, nil
}

func (s *Service) SignalWorkflow(ctx context.Context, workflowID string, input SignalWorkflowInput) (WorkflowInstance, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return WorkflowInstance{}, fmt.Errorf("workflow signal name is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowInstance{}, fmt.Errorf("begin signal transaction: %w", err)
	}
	defer tx.Rollback()

	instance, err := s.getWorkflowTx(ctx, tx, workflowID)
	if err != nil {
		return WorkflowInstance{}, err
	}
	if instance.Status == StatusCompleted || instance.Status == StatusCanceled {
		return WorkflowInstance{}, fmt.Errorf("workflow %s is not accepting signals in status %q", workflowID, instance.Status)
	}

	now := time.Now().UTC()
	payload := input.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	updatedContext, err := applySignalToContext(instance.Context, name, payload, now)
	if err != nil {
		return WorkflowInstance{}, err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workflow_signals (workflow_id, signal_name, payload, status, created_at, processed_at)
		VALUES (?, ?, ?, 'processed', ?, ?)
	`, workflowID, name, string(payload), formatTime(now), formatTime(now)); err != nil {
		return WorkflowInstance{}, fmt.Errorf("insert workflow signal: %w", err)
	}

	sequence, err := appendEventTx(ctx, tx, workflowID, instance.LastEventSequence, "WorkflowSignaled", map[string]any{
		"name":    name,
		"payload": decodePayloadForEvent(payload),
	})
	if err != nil {
		return WorkflowInstance{}, err
	}

	wokenTaskIDs, err := s.wakeTasksWaitingForSignalTx(ctx, tx, workflowID, name, now)
	if err != nil {
		return WorkflowInstance{}, err
	}

	instance.Context = updatedContext
	instance.LastEventSequence = sequence
	instance.UpdatedAt = now
	if len(wokenTaskIDs) > 0 {
		instance.Status = StatusRunning
		instance.LastError = ""
		instance.NextRunAt = &now
	}
	if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
		return WorkflowInstance{}, err
	}

	if err := tx.Commit(); err != nil {
		return WorkflowInstance{}, fmt.Errorf("commit workflow signal transaction: %w", err)
	}

	result, err := s.GetWorkflow(ctx, workflowID)
	if err != nil {
		return WorkflowInstance{}, err
	}
	s.emitLiveEvent("workflow.signaled", "workflow", workflowID, map[string]any{
		"workflowId": workflowID,
		"name":       name,
		"payload":    decodePayloadForEvent(payload),
	})
	s.emitOperationEvent(workflowID, "WorkflowSignaled", map[string]any{
		"name":    name,
		"payload": decodePayloadForEvent(payload),
	})
	for _, taskID := range wokenTaskIDs {
		s.emitLiveEvent("task.updated", "task", fmt.Sprintf("%d", taskID), map[string]any{
			"taskId":     taskID,
			"workflowId": workflowID,
			"status":     StatusPending,
			"signal":     name,
		})
	}
	return result, nil
}

func (s *Service) CancelWorkflow(ctx context.Context, workflowID string) (WorkflowInstance, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowInstance{}, fmt.Errorf("begin workflow cancel transaction: %w", err)
	}
	defer tx.Rollback()

	instance, err := s.getWorkflowTx(ctx, tx, workflowID)
	if err != nil {
		return WorkflowInstance{}, err
	}
	if instance.Status == StatusCompleted || instance.Status == StatusCanceled {
		return WorkflowInstance{}, fmt.Errorf("workflow %s cannot be canceled from status %q", workflowID, instance.Status)
	}

	now := time.Now().UTC()
	sequence, err := appendEventTx(ctx, tx, workflowID, instance.LastEventSequence, "WorkflowCanceled", map[string]any{
		"canceledAt": formatTime(now),
	})
	if err != nil {
		return WorkflowInstance{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_tasks
		SET status = ?, lease_owner = '', lease_expires_at = NULL, updated_at = ?
		WHERE workflow_id = ? AND status NOT IN (?, ?)
	`, StatusCanceled, formatTime(now), workflowID, StatusCompleted, StatusCanceled); err != nil {
		return WorkflowInstance{}, fmt.Errorf("cancel workflow tasks: %w", err)
	}

	instance.Status = StatusCanceled
	instance.LastError = ""
	instance.NextRunAt = nil
	instance.LastEventSequence = sequence
	instance.UpdatedAt = now
	if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
		return WorkflowInstance{}, err
	}

	if err := tx.Commit(); err != nil {
		return WorkflowInstance{}, fmt.Errorf("commit workflow cancel transaction: %w", err)
	}

	result, err := s.GetWorkflow(ctx, workflowID)
	if err != nil {
		return WorkflowInstance{}, err
	}
	s.emitLiveEvent("workflow.canceled", "workflow", workflowID, map[string]any{
		"workflowId": workflowID,
		"status":     StatusCanceled,
	})
	s.emitOperationEvent(workflowID, "WorkflowCanceled", map[string]any{
		"status": StatusCanceled,
	})
	return result, nil
}

func (s *Service) ReplayWorkflow(ctx context.Context, workflowID string) (WorkflowReplay, error) {
	events, err := s.GetWorkflowHistory(ctx, workflowID)
	if err != nil {
		return WorkflowReplay{}, err
	}
	replay := WorkflowReplay{
		WorkflowID: workflowID,
		Status:     StatusPending,
		Context:    buildInitialContext("", 0),
		EventCount: len(events),
	}

	for _, event := range events {
		replay.LastEventSequence = event.Sequence
		payloadMap, _ := decodePayloadForEvent(event.Payload).(map[string]any)
		switch event.EventType {
		case "WorkflowStarted":
			replay.Status = StatusRunning
			replay.WorkflowDefinition, _ = payloadMap["definitionId"].(string)
		case "ActivityScheduled":
			replay.Status = StatusRunning
			replay.CurrentStepName, _ = payloadMap["stepName"].(string)
			replay.CurrentActivity, _ = payloadMap["activity"].(string)
			replay.LastError = ""
		case "ActivityWaiting", "ActivityWaitingForSignal", "ActivityRetryScheduled":
			replay.Status = StatusRunning
			replay.LastError, _ = payloadMap["error"].(string)
		case "ActivityCompleted":
			if contextValue, ok := payloadMap["context"]; ok {
				if encoded, err := json.Marshal(contextValue); err == nil {
					replay.Context = json.RawMessage(encoded)
				}
			} else if replay.Context != nil {
				stepName, _ := payloadMap["stepName"].(string)
				if outputValue, ok := payloadMap["output"]; ok {
					if encoded, err := json.Marshal(outputValue); err == nil {
						replay.Context, _ = applyStepOutputToContext(replay.Context, stepName, json.RawMessage(encoded))
					}
				}
			}
			if outputValue, ok := payloadMap["output"]; ok {
				if encoded, err := json.Marshal(outputValue); err == nil {
					replay.LastOutput = json.RawMessage(encoded)
				}
			}
			replay.LastError = ""
		case "ActivityFailed", "WorkflowFailed":
			replay.Status = StatusFailed
			replay.LastError, _ = payloadMap["error"].(string)
		case "WorkflowCompleted":
			replay.Status = StatusCompleted
			replay.CurrentStepName = ""
			replay.CurrentActivity = ""
		case "WorkflowCanceled":
			replay.Status = StatusCanceled
			replay.CurrentStepName = ""
			replay.CurrentActivity = ""
		case "WorkflowSignaled":
			name, _ := payloadMap["name"].(string)
			if name != "" {
				encoded, _ := json.Marshal(payloadMap["payload"])
				replay.Context, _ = applySignalToContext(replay.Context, name, json.RawMessage(encoded), event.CreatedAt)
			}
		}
	}

	return replay, nil
}

func (s *Service) ListRecentEvents(ctx context.Context, limit int) ([]WorkflowEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT workflow_id, sequence, event_type, payload, created_at
		FROM workflow_events
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent workflow events: %w", err)
	}
	defer rows.Close()

	var events []WorkflowEvent
	for rows.Next() {
		var (
			event       WorkflowEvent
			payloadText string
			createdAt   string
		)
		if err := rows.Scan(&event.WorkflowID, &event.Sequence, &event.EventType, &payloadText, &createdAt); err != nil {
			return nil, fmt.Errorf("scan recent workflow event: %w", err)
		}
		event.Payload = json.RawMessage(payloadText)
		event.CreatedAt = mustParseTime(createdAt)
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent workflow events: %w", err)
	}

	return events, nil
}

func (s *Service) ListTasks(ctx context.Context) ([]WorkflowTask, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
		       run_at, last_error, lease_owner, lease_expires_at, state_json, created_at, updated_at
		FROM workflow_tasks
		ORDER BY CASE status
			WHEN 'running' THEN 0
			WHEN 'pending' THEN 1
			WHEN 'failed' THEN 2
			ELSE 3
		END, run_at ASC, id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query workflow tasks: %w", err)
	}
	defer rows.Close()

	var tasks []WorkflowTask
	for rows.Next() {
		task, err := scanWorkflowTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workflow tasks: %w", err)
	}

	return tasks, nil
}

func (s *Service) RetryTask(ctx context.Context, taskID int64) (WorkflowTask, error) {
	return s.applyTaskAction(ctx, taskID, "retry")
}

func (s *Service) RequeueTask(ctx context.Context, taskID int64) (WorkflowTask, error) {
	return s.applyTaskAction(ctx, taskID, "requeue")
}

func (s *Service) PauseTask(ctx context.Context, taskID int64) (WorkflowTask, error) {
	return s.applyTaskAction(ctx, taskID, "pause")
}

func (s *Service) ResumeTask(ctx context.Context, taskID int64) (WorkflowTask, error) {
	return s.applyTaskAction(ctx, taskID, "resume")
}

func (s *Service) CancelTask(ctx context.Context, taskID int64) (WorkflowTask, error) {
	return s.applyTaskAction(ctx, taskID, "cancel")
}

func (s *Service) RunOnce(ctx context.Context) (bool, error) {
	task, claimed, err := s.claimNextTask(ctx)
	if err != nil || !claimed {
		return claimed, err
	}

	instance, err := s.GetWorkflow(ctx, task.WorkflowID)
	if err != nil {
		return true, s.failTaskNow(ctx, task, err)
	}
	details, err := s.GetDefinitionVersion(ctx, instance.DefinitionID, instance.DefinitionVersion)
	if err != nil {
		return true, s.failTaskNow(ctx, task, err)
	}
	if task.StepIndex < 0 || task.StepIndex >= len(details.Document.Steps) {
		return true, s.failTaskNow(ctx, task, fmt.Errorf("step index %d out of range", task.StepIndex))
	}

	step := details.Document.Steps[task.StepIndex]
	activity := s.activities[step.Activity]
	if activity == nil {
		return true, s.failTaskNow(ctx, task, fmt.Errorf("activity %q is not registered", step.Activity))
	}
	resolvedInput, err := resolveStepInput(step.Input, instance.Context)
	if err != nil {
		return true, s.failTaskNow(ctx, task, err)
	}
	step.Input = resolvedInput

	output, execErr := activity.Execute(ctx, ActivityExecutionRequest{
		WorkflowID:        task.WorkflowID,
		DefinitionID:      details.ID,
		DefinitionVersion: details.ActiveVersion,
		WorkflowContext:   instance.Context,
		Step:              step,
		Task:              task,
		Now:               time.Now().UTC(),
	})

	if execErr != nil {
		return true, s.handleTaskFailure(ctx, task, step, execErr)
	}

	if output.DelayUntil != nil {
		return true, s.delayTask(ctx, task, step, *output.DelayUntil, output.State)
	}
	if output.WaitForSignal != nil {
		return true, s.waitTaskForSignal(ctx, task, step, *output.WaitForSignal)
	}

	return true, s.completeTask(ctx, task, details, step, output.Output, output.ContextUpdates)
}

func (s *Service) applyTaskAction(ctx context.Context, taskID int64, action string) (WorkflowTask, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowTask{}, fmt.Errorf("begin task action transaction: %w", err)
	}
	defer tx.Rollback()

	task, err := s.getTaskTx(ctx, tx, taskID)
	if err != nil {
		return WorkflowTask{}, err
	}
	instance, err := s.getWorkflowTx(ctx, tx, task.WorkflowID)
	if err != nil {
		return WorkflowTask{}, err
	}

	now := time.Now().UTC()
	sequence := instance.LastEventSequence
	eventType := ""

	switch action {
	case "retry":
		if task.Status != StatusFailed && task.Status != StatusCanceled {
			return WorkflowTask{}, fmt.Errorf("task %d cannot be retried from status %q", taskID, task.Status)
		}
		task.Status = StatusPending
		task.Attempts = 0
		task.LastError = ""
		task.RunAt = now
		task.LeaseOwner = ""
		task.LeaseExpiresAt = nil
		task.State = nil
		eventType = "TaskRetried"
		instance.Status = StatusRunning
		instance.LastError = ""
		instance.NextRunAt = &now
	case "requeue":
		if task.Status == StatusCompleted {
			return WorkflowTask{}, fmt.Errorf("task %d cannot be requeued after completion", taskID)
		}
		task.Status = StatusPending
		task.RunAt = now
		task.LeaseOwner = ""
		task.LeaseExpiresAt = nil
		eventType = "TaskRequeued"
		if instance.Status != StatusCompleted && instance.Status != StatusCanceled {
			instance.Status = StatusRunning
			instance.LastError = ""
			instance.NextRunAt = &now
		}
	case "pause":
		if task.Status != StatusPending && task.Status != StatusRunning {
			return WorkflowTask{}, fmt.Errorf("task %d cannot be paused from status %q", taskID, task.Status)
		}
		task.Status = StatusPaused
		task.LeaseOwner = ""
		task.LeaseExpiresAt = nil
		eventType = "TaskPaused"
		if instance.Status == StatusRunning {
			instance.Status = StatusPaused
		}
		instance.NextRunAt = nil
	case "resume":
		if task.Status != StatusPaused {
			return WorkflowTask{}, fmt.Errorf("task %d cannot be resumed from status %q", taskID, task.Status)
		}
		task.Status = StatusPending
		task.RunAt = now
		task.LeaseOwner = ""
		task.LeaseExpiresAt = nil
		eventType = "TaskResumed"
		if instance.Status == StatusPaused {
			instance.Status = StatusRunning
		}
		instance.NextRunAt = &now
	case "cancel":
		if task.Status == StatusCompleted || task.Status == StatusCanceled {
			return WorkflowTask{}, fmt.Errorf("task %d cannot be canceled from status %q", taskID, task.Status)
		}
		task.Status = StatusCanceled
		task.LeaseOwner = ""
		task.LeaseExpiresAt = nil
		task.State = nil
		eventType = "TaskCanceled"
		instance.Status = StatusCanceled
		instance.LastError = ""
		instance.NextRunAt = nil
	default:
		return WorkflowTask{}, fmt.Errorf("unsupported task action %q", action)
	}

	task.UpdatedAt = now
	instance.UpdatedAt = now
	instance.CurrentStepIndex = task.StepIndex
	instance.CurrentStepName = task.StepName
	instance.CurrentActivity = task.ActivityName

	sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, eventType, map[string]any{
		"taskId":       task.ID,
		"stepIndex":    task.StepIndex,
		"stepName":     task.StepName,
		"activityName": task.ActivityName,
		"status":       task.Status,
	})
	if err != nil {
		return WorkflowTask{}, err
	}

	if action == "cancel" {
		sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "WorkflowCanceled", map[string]any{
			"taskId":    task.ID,
			"stepIndex": task.StepIndex,
			"stepName":  task.StepName,
		})
		if err != nil {
			return WorkflowTask{}, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_tasks
		SET status = ?, attempts = ?, run_at = ?, last_error = ?, lease_owner = ?, lease_expires_at = ?, state_json = ?, updated_at = ?
		WHERE id = ?
	`, task.Status, task.Attempts, formatTime(task.RunAt), task.LastError, task.LeaseOwner, nullableTime(task.LeaseExpiresAt), rawJSONString(task.State), formatTime(task.UpdatedAt), task.ID); err != nil {
		return WorkflowTask{}, fmt.Errorf("update workflow task action: %w", err)
	}

	instance.LastEventSequence = sequence
	if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
		return WorkflowTask{}, err
	}

	if err := tx.Commit(); err != nil {
		return WorkflowTask{}, fmt.Errorf("commit task action transaction: %w", err)
	}

	result, err := s.GetTask(ctx, taskID)
	if err != nil {
		return WorkflowTask{}, err
	}
	s.emitLiveEvent("task.updated", "task", fmt.Sprintf("%d", result.ID), result)
	s.emitLiveEvent("workflow.updated", "workflow", result.WorkflowID, map[string]any{
		"workflowId": result.WorkflowID,
		"status":     instance.Status,
	})
	s.emitOperationEvent(result.WorkflowID, eventType, map[string]any{
		"taskId":       result.ID,
		"stepIndex":    result.StepIndex,
		"stepName":     result.StepName,
		"activityName": result.ActivityName,
		"status":       result.Status,
	})
	return result, nil
}

func (s *Service) completeTask(ctx context.Context, task WorkflowTask, definition DefinitionDetails, step StepDefinition, output json.RawMessage, contextUpdates map[string]any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin task completion transaction: %w", err)
	}
	defer tx.Rollback()

	instance, err := s.getWorkflowTx(ctx, tx, task.WorkflowID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	sequence := instance.LastEventSequence
	updatedContext, err := applyActivityResultToContext(instance.Context, step.Name, output, contextUpdates)
	if err != nil {
		return err
	}
	instance.Context = updatedContext
	instance.LastOutput = output
	instance.NextRunAt = nil

	sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "ActivityCompleted", map[string]any{
		"taskId":    task.ID,
		"stepIndex": task.StepIndex,
		"stepName":  step.Name,
		"activity":  step.Activity,
		"attempt":   task.Attempts,
		"output":    decodePayloadForEvent(output),
		"context":   decodePayloadForEvent(updatedContext),
	})
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_tasks
		SET status = ?, lease_owner = '', lease_expires_at = NULL, state_json = '', updated_at = ?
		WHERE id = ?
	`, StatusCompleted, formatTime(now), task.ID); err != nil {
		return fmt.Errorf("mark workflow task complete: %w", err)
	}

	nextStepIndex, selectedTransition, err := resolveNextStep(definition.Document.Steps, task.StepIndex, updatedContext)
	if err != nil {
		return err
	}
	instance.UpdatedAt = now
	instance.LastEventSequence = sequence
	instance.LastError = ""

	if nextStepIndex < 0 {
		sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "WorkflowCompleted", map[string]any{
			"completedAt": formatTime(now),
		})
		if err != nil {
			return err
		}
		instance.Status = StatusCompleted
		instance.CurrentStepIndex = -1
		instance.CurrentStepName = ""
		instance.CurrentActivity = ""
		instance.LastEventSequence = sequence
		if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
			return err
		}
	} else {
		nextStep := definition.Document.Steps[nextStepIndex]
		if selectedTransition != nil {
			sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "TransitionSelected", map[string]any{
				"fromStep": task.StepName,
				"toStep":   nextStep.Name,
				"label":    selectedTransition.Label,
			})
			if err != nil {
				return err
			}
		}
		sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "ActivityScheduled", map[string]any{
			"stepIndex":   nextStepIndex,
			"stepName":    nextStep.Name,
			"activity":    nextStep.Activity,
			"maxAttempts": nextStep.Retry.MaxAttempts,
		})
		if err != nil {
			return err
		}
		if err := insertTaskTx(ctx, tx, instance.ID, nextStepIndex, nextStep, now); err != nil {
			return err
		}
		instance.Status = StatusRunning
		instance.CurrentStepIndex = nextStepIndex
		instance.CurrentStepName = nextStep.Name
		instance.CurrentActivity = nextStep.Activity
		instance.LastEventSequence = sequence
		instance.NextRunAt = &now
		if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit task completion transaction: %w", err)
	}

	s.emitLiveEvent("task.completed", "task", fmt.Sprintf("%d", task.ID), map[string]any{
		"taskId":     task.ID,
		"workflowId": task.WorkflowID,
		"stepIndex":  task.StepIndex,
		"stepName":   step.Name,
		"activity":   step.Activity,
		"status":     StatusCompleted,
	})
	if nextStepIndex >= len(definition.Document.Steps) {
		s.emitLiveEvent("workflow.completed", "workflow", task.WorkflowID, map[string]any{
			"workflowId": task.WorkflowID,
			"status":     StatusCompleted,
		})
		s.emitOperationEvent(task.WorkflowID, "WorkflowCompleted", map[string]any{
			"stepIndex": task.StepIndex,
			"stepName":  step.Name,
		})
	} else {
		s.emitLiveEvent("workflow.updated", "workflow", task.WorkflowID, map[string]any{
			"workflowId": task.WorkflowID,
			"status":     StatusRunning,
			"stepIndex":  nextStepIndex,
		})
		s.emitOperationEvent(task.WorkflowID, "ActivityCompleted", map[string]any{
			"taskId":    task.ID,
			"stepIndex": task.StepIndex,
			"stepName":  step.Name,
			"activity":  step.Activity,
		})
	}

	return nil
}

func (s *Service) delayTask(ctx context.Context, task WorkflowTask, step StepDefinition, runAt time.Time, state json.RawMessage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin task delay transaction: %w", err)
	}
	defer tx.Rollback()

	instance, err := s.getWorkflowTx(ctx, tx, task.WorkflowID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	sequence := instance.LastEventSequence
	sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "ActivityWaiting", map[string]any{
		"taskId":    task.ID,
		"stepIndex": task.StepIndex,
		"stepName":  step.Name,
		"activity":  step.Activity,
		"runAt":     formatTime(runAt),
	})
	if err != nil {
		return err
	}

	attempts := task.Attempts
	if attempts > 0 {
		attempts--
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_tasks
		SET status = ?, attempts = ?, run_at = ?, last_error = '', lease_owner = '', lease_expires_at = NULL, state_json = ?, updated_at = ?
		WHERE id = ?
	`, StatusPending, attempts, formatTime(runAt), rawJSONString(state), formatTime(now), task.ID); err != nil {
		return fmt.Errorf("delay workflow task: %w", err)
	}

	instance.Status = StatusRunning
	instance.LastEventSequence = sequence
	instance.LastError = ""
	instance.NextRunAt = &runAt
	instance.UpdatedAt = now
	if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit task delay transaction: %w", err)
	}

	s.emitLiveEvent("task.updated", "task", fmt.Sprintf("%d", task.ID), map[string]any{
		"taskId":     task.ID,
		"workflowId": task.WorkflowID,
		"status":     StatusPending,
		"runAt":      formatTime(runAt),
	})
	s.emitLiveEvent("workflow.updated", "workflow", task.WorkflowID, map[string]any{
		"workflowId": task.WorkflowID,
		"status":     StatusRunning,
	})
	s.emitOperationEvent(task.WorkflowID, "ActivityWaiting", map[string]any{
		"taskId":    task.ID,
		"stepIndex": task.StepIndex,
		"stepName":  step.Name,
		"activity":  step.Activity,
		"runAt":     formatTime(runAt),
	})

	return nil
}

func (s *Service) waitTaskForSignal(ctx context.Context, task WorkflowTask, step StepDefinition, wait ActivitySignalWait) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin task wait transaction: %w", err)
	}
	defer tx.Rollback()

	instance, err := s.getWorkflowTx(ctx, tx, task.WorkflowID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	sequence := instance.LastEventSequence
	payload := map[string]any{
		"taskId":     task.ID,
		"stepIndex":  task.StepIndex,
		"stepName":   step.Name,
		"activity":   step.Activity,
		"signalName": wait.SignalName,
	}
	if wait.TimeoutAt != nil {
		payload["timeoutAt"] = formatTime(*wait.TimeoutAt)
	}
	sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "ActivityWaitingForSignal", payload)
	if err != nil {
		return err
	}

	attempts := task.Attempts
	if attempts > 0 {
		attempts--
	}
	waitRunAt := parkedSignalRunAt(wait.TimeoutAt)
	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_tasks
		SET status = ?, attempts = ?, run_at = ?, last_error = '', lease_owner = '', lease_expires_at = NULL, state_json = ?, updated_at = ?
		WHERE id = ?
	`, StatusWaiting, attempts, formatTime(waitRunAt), rawJSONString(wait.State), formatTime(now), task.ID); err != nil {
		return fmt.Errorf("park workflow task for signal: %w", err)
	}

	instance.Status = StatusRunning
	instance.LastEventSequence = sequence
	instance.LastError = ""
	if wait.TimeoutAt != nil {
		instance.NextRunAt = wait.TimeoutAt
	} else {
		instance.NextRunAt = nil
	}
	instance.UpdatedAt = now
	if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit task wait transaction: %w", err)
	}

	s.emitLiveEvent("task.updated", "task", fmt.Sprintf("%d", task.ID), map[string]any{
		"taskId":     task.ID,
		"workflowId": task.WorkflowID,
		"status":     StatusWaiting,
		"signalName": wait.SignalName,
	})
	s.emitLiveEvent("workflow.updated", "workflow", task.WorkflowID, map[string]any{
		"workflowId": task.WorkflowID,
		"status":     StatusRunning,
	})
	s.emitOperationEvent(task.WorkflowID, "ActivityWaitingForSignal", payload)
	return nil
}

func (s *Service) handleTaskFailure(ctx context.Context, task WorkflowTask, step StepDefinition, execErr error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin task failure transaction: %w", err)
	}
	defer tx.Rollback()

	instance, err := s.getWorkflowTx(ctx, tx, task.WorkflowID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	sequence := instance.LastEventSequence

	if task.Attempts < task.MaxAttempts {
		nextRunAt := now.Add(time.Duration(step.Retry.BackoffSeconds) * time.Second)
		sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "ActivityRetryScheduled", map[string]any{
			"taskId":      task.ID,
			"stepIndex":   task.StepIndex,
			"stepName":    step.Name,
			"activity":    step.Activity,
			"attempt":     task.Attempts,
			"maxAttempts": task.MaxAttempts,
			"error":       execErr.Error(),
			"runAt":       formatTime(nextRunAt),
		})
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE workflow_tasks
			SET status = ?, run_at = ?, last_error = ?, lease_owner = '', lease_expires_at = NULL, state_json = '', updated_at = ?
			WHERE id = ?
		`, StatusPending, formatTime(nextRunAt), execErr.Error(), formatTime(now), task.ID); err != nil {
			return fmt.Errorf("requeue workflow task: %w", err)
		}

		instance.Status = StatusRunning
		instance.LastEventSequence = sequence
		instance.LastError = execErr.Error()
		instance.NextRunAt = &nextRunAt
		instance.UpdatedAt = now
		if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
			return err
		}
	} else {
		sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "ActivityFailed", map[string]any{
			"taskId":      task.ID,
			"stepIndex":   task.StepIndex,
			"stepName":    step.Name,
			"activity":    step.Activity,
			"attempt":     task.Attempts,
			"maxAttempts": task.MaxAttempts,
			"error":       execErr.Error(),
		})
		if err != nil {
			return err
		}
		sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "WorkflowFailed", map[string]any{
			"stepIndex": task.StepIndex,
			"stepName":  step.Name,
			"error":     execErr.Error(),
		})
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE workflow_tasks
			SET status = ?, last_error = ?, lease_owner = '', lease_expires_at = NULL, state_json = '', updated_at = ?
			WHERE id = ?
		`, StatusFailed, execErr.Error(), formatTime(now), task.ID); err != nil {
			return fmt.Errorf("mark workflow task failed: %w", err)
		}

		instance.Status = StatusFailed
		instance.LastEventSequence = sequence
		instance.LastError = execErr.Error()
		instance.NextRunAt = nil
		instance.UpdatedAt = now
		if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit task failure transaction: %w", err)
	}

	if task.Attempts < task.MaxAttempts {
		s.emitLiveEvent("task.updated", "task", fmt.Sprintf("%d", task.ID), map[string]any{
			"taskId":     task.ID,
			"workflowId": task.WorkflowID,
			"status":     StatusPending,
			"error":      execErr.Error(),
		})
		s.emitLiveEvent("workflow.updated", "workflow", task.WorkflowID, map[string]any{
			"workflowId": task.WorkflowID,
			"status":     StatusRunning,
			"error":      execErr.Error(),
		})
		s.emitOperationEvent(task.WorkflowID, "ActivityRetryScheduled", map[string]any{
			"taskId":    task.ID,
			"stepIndex": task.StepIndex,
			"stepName":  step.Name,
			"activity":  step.Activity,
			"error":     execErr.Error(),
		})
	} else {
		s.emitLiveEvent("task.failed", "task", fmt.Sprintf("%d", task.ID), map[string]any{
			"taskId":     task.ID,
			"workflowId": task.WorkflowID,
			"status":     StatusFailed,
			"error":      execErr.Error(),
		})
		s.emitLiveEvent("workflow.failed", "workflow", task.WorkflowID, map[string]any{
			"workflowId": task.WorkflowID,
			"status":     StatusFailed,
			"error":      execErr.Error(),
		})
		s.emitOperationEvent(task.WorkflowID, "WorkflowFailed", map[string]any{
			"taskId":    task.ID,
			"stepIndex": task.StepIndex,
			"stepName":  step.Name,
			"activity":  step.Activity,
			"error":     execErr.Error(),
		})
	}

	return nil
}

func (s *Service) failTaskNow(ctx context.Context, task WorkflowTask, cause error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin terminal failure transaction: %w", err)
	}
	defer tx.Rollback()

	instance, err := s.getWorkflowTx(ctx, tx, task.WorkflowID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	sequence := instance.LastEventSequence
	sequence, err = appendEventTx(ctx, tx, instance.ID, sequence, "WorkflowFailed", map[string]any{
		"stepIndex": task.StepIndex,
		"stepName":  task.StepName,
		"error":     cause.Error(),
	})
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_tasks
		SET status = ?, last_error = ?, lease_owner = '', lease_expires_at = NULL, state_json = '', updated_at = ?
		WHERE id = ?
	`, StatusFailed, cause.Error(), formatTime(now), task.ID); err != nil {
		return fmt.Errorf("mark task failed immediately: %w", err)
	}

	instance.Status = StatusFailed
	instance.LastEventSequence = sequence
	instance.LastError = cause.Error()
	instance.NextRunAt = nil
	instance.UpdatedAt = now
	if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit terminal failure transaction: %w", err)
	}
	s.emitLiveEvent("task.failed", "task", fmt.Sprintf("%d", task.ID), map[string]any{
		"taskId":     task.ID,
		"workflowId": task.WorkflowID,
		"status":     StatusFailed,
		"error":      cause.Error(),
	})
	s.emitLiveEvent("workflow.failed", "workflow", task.WorkflowID, map[string]any{
		"workflowId": task.WorkflowID,
		"status":     StatusFailed,
		"error":      cause.Error(),
	})
	s.emitOperationEvent(task.WorkflowID, "WorkflowFailed", map[string]any{
		"taskId":    task.ID,
		"stepIndex": task.StepIndex,
		"stepName":  task.StepName,
		"error":     cause.Error(),
	})
	return nil
}

func (s *Service) claimNextTask(ctx context.Context) (WorkflowTask, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowTask{}, false, fmt.Errorf("begin task claim transaction: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
		       run_at, last_error, lease_owner, lease_expires_at, state_json, created_at, updated_at
		FROM workflow_tasks
		WHERE (status = ? AND run_at <= ?) OR (status = ? AND run_at <= ?)
		ORDER BY run_at ASC, id ASC
		LIMIT 1
	`, StatusPending, formatTime(now), StatusWaiting, formatTime(now))
	if err != nil {
		return WorkflowTask{}, false, fmt.Errorf("select runnable workflow task: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return WorkflowTask{}, false, nil
	}

	task, err := scanWorkflowTask(rows)
	if err != nil {
		return WorkflowTask{}, false, err
	}

	leaseExpiresAt := now.Add(s.cfg.LeaseDuration)
	result, err := tx.ExecContext(ctx, `
		UPDATE workflow_tasks
		SET status = ?, attempts = attempts + 1, lease_owner = ?, lease_expires_at = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`, StatusRunning, s.workerID, formatTime(leaseExpiresAt), formatTime(now), task.ID, StatusPending)
	if err != nil {
		return WorkflowTask{}, false, fmt.Errorf("claim workflow task: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return WorkflowTask{}, false, fmt.Errorf("count task claim rows: %w", err)
	}
	if rowsAffected == 0 {
		return WorkflowTask{}, false, nil
	}

	if err := tx.Commit(); err != nil {
		return WorkflowTask{}, false, fmt.Errorf("commit task claim transaction: %w", err)
	}

	task.Status = StatusRunning
	task.Attempts++
	task.LeaseOwner = s.workerID
	task.LeaseExpiresAt = &leaseExpiresAt
	task.UpdatedAt = now
	return task, true, nil
}

func (s *Service) GetTask(ctx context.Context, taskID int64) (WorkflowTask, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
		       run_at, last_error, lease_owner, lease_expires_at, state_json, created_at, updated_at
		FROM workflow_tasks
		WHERE id = ?
	`, taskID)
	task, err := scanWorkflowTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowTask{}, ErrNotFound
		}
		return WorkflowTask{}, err
	}
	return task, nil
}

func (s *Service) requeueExpiredTasks(ctx context.Context) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE workflow_tasks
		SET status = ?, lease_owner = '', lease_expires_at = NULL, updated_at = ?
		WHERE status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?
	`, StatusPending, formatTime(now), StatusRunning, formatTime(now))
	if err != nil {
		return fmt.Errorf("requeue expired workflow tasks: %w", err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count expired workflow tasks: %w", err)
	}

	if count > 0 {
		s.logger.Warn("requeued expired workflow tasks", "count", count)
		s.emitLiveEvent("queue.snapshot", "queue", "tasks", map[string]any{
			"queue":         "tasks",
			"requeuedCount": count,
		})
	}

	return nil
}

func (s *Service) updateInstanceTx(ctx context.Context, tx *sql.Tx, instance WorkflowInstance, snapshot workflowSnapshot, lastOutput json.RawMessage) error {
	if len(lastOutput) > 0 {
		snapshot.LastOutput = lastOutput
	}
	snapshotJSON, err := buildSnapshotJSON(snapshot)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE workflow_instances
		SET status = ?, current_step_index = ?, current_step_name = ?, current_activity = ?,
		    snapshot_json = ?, last_event_sequence = ?, last_error = ?, updated_at = ?
		WHERE id = ?
	`, instance.Status, instance.CurrentStepIndex, instance.CurrentStepName, instance.CurrentActivity, snapshotJSON, instance.LastEventSequence, instance.LastError, formatTime(instance.UpdatedAt), instance.ID); err != nil {
		return fmt.Errorf("update workflow instance: %w", err)
	}

	return nil
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT version, status, created_at, updated_at, published_at
		FROM workflow_definition_versions
		WHERE definition_id = ?
		ORDER BY version DESC
	`, definitionID)
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
	row := tx.QueryRowContext(ctx, `
		SELECT d.id, d.name, d.description, d.status, d.active_version,
		       COALESCE(MAX(v.version), d.active_version) AS latest_version,
		       COALESCE(MAX(CASE WHEN v.status = 'draft' THEN v.version END), 0) AS draft_version,
		       d.created_at, d.updated_at
		FROM workflow_definitions d
		LEFT JOIN workflow_definition_versions v ON v.definition_id = d.id
		WHERE d.id = ?
		GROUP BY d.id, d.name, d.description, d.status, d.active_version, d.created_at, d.updated_at
	`, definitionID)

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
	row := tx.QueryRowContext(ctx, `
		SELECT version, status, created_at, updated_at, published_at
		FROM workflow_definition_versions
		WHERE definition_id = ? AND version = ?
	`, definitionID, version)

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
	row := tx.QueryRowContext(ctx, `
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
	`, definitionID)

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
	row := tx.QueryRowContext(ctx, `
		SELECT document_json
		FROM workflow_definition_versions
		WHERE definition_id = ? AND version = ?
	`, definitionID, version)
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

func (s *Service) getWorkflowTx(ctx context.Context, tx *sql.Tx, workflowID string) (WorkflowInstance, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, definition_id, definition_version, status, current_step_index, current_step_name,
		       current_activity, snapshot_json, last_event_sequence, last_error, created_at, updated_at
		FROM workflow_instances
		WHERE id = ?
	`, workflowID)

	instance, err := scanWorkflowInstance(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowInstance{}, ErrNotFound
		}
		return WorkflowInstance{}, err
	}

	return instance, nil
}

func insertTaskTx(ctx context.Context, tx *sql.Tx, workflowID string, stepIndex int, step StepDefinition, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO workflow_tasks (
			workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
			run_at, lease_owner, lease_expires_at, last_error, state_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 0, ?, ?, '', NULL, '', '', ?, ?)
	`, workflowID, stepIndex, step.Name, step.Activity, StatusPending, step.Retry.MaxAttempts, formatTime(now), formatTime(now), formatTime(now))
	if err != nil {
		return fmt.Errorf("insert workflow task: %w", err)
	}
	return nil
}

func appendEventTx(ctx context.Context, tx *sql.Tx, workflowID string, sequence int, eventType string, payload any) (int, error) {
	nextSequence := sequence + 1
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return sequence, fmt.Errorf("encode workflow event %s: %w", eventType, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workflow_events (workflow_id, sequence, event_type, payload, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, workflowID, nextSequence, eventType, string(payloadJSON), formatTime(time.Now().UTC())); err != nil {
		return sequence, fmt.Errorf("insert workflow event %s: %w", eventType, err)
	}

	return nextSequence, nil
}

func decodePayloadForEvent(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}

	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return string(raw)
	}
	return payload
}

var templateTokenPattern = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

func buildInitialContext(definitionID string, version int) json.RawMessage {
	payload, _ := json.Marshal(map[string]any{
		"workflow": map[string]any{
			"definitionId":      definitionID,
			"definitionVersion": version,
		},
		"steps":   map[string]any{},
		"signals": map[string]any{},
	})
	return json.RawMessage(payload)
}

func snapshotFromInstance(instance WorkflowInstance) workflowSnapshot {
	return workflowSnapshot{
		Status:          instance.Status,
		CurrentStepName: instance.CurrentStepName,
		CurrentActivity: instance.CurrentActivity,
		LastOutput:      instance.LastOutput,
		LastError:       instance.LastError,
		Context:         instance.Context,
		PendingSignals:  instance.PendingSignals,
		NextRunAt:       instance.NextRunAt,
	}
}

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

func buildSnapshotJSON(snapshot workflowSnapshot) (string, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("encode workflow snapshot: %w", err)
	}
	return string(payload), nil
}

func ensureDatabasePath(path string) error {
	if path == "" || path == ":memory:" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create workflow database directory: %w", err)
	}
	return nil
}

func generateID(prefix string) string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(raw[:]))
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func mustParseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func rawJSONString(value json.RawMessage) string {
	if len(value) == 0 {
		return ""
	}
	return string(value)
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

func scanWorkflowInstance(scanner interface{ Scan(...any) error }) (WorkflowInstance, error) {
	var (
		instance     WorkflowInstance
		snapshotJSON string
		lastError    sql.NullString
		createdAt    string
		updatedAt    string
	)
	if err := scanner.Scan(
		&instance.ID,
		&instance.DefinitionID,
		&instance.DefinitionVersion,
		&instance.Status,
		&instance.CurrentStepIndex,
		&instance.CurrentStepName,
		&instance.CurrentActivity,
		&snapshotJSON,
		&instance.LastEventSequence,
		&lastError,
		&createdAt,
		&updatedAt,
	); err != nil {
		return WorkflowInstance{}, fmt.Errorf("scan workflow instance: %w", err)
	}
	instance.LastError = lastError.String
	instance.CreatedAt = mustParseTime(createdAt)
	instance.UpdatedAt = mustParseTime(updatedAt)
	if snapshotJSON != "" {
		var snapshot workflowSnapshot
		if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err == nil {
			instance.LastOutput = snapshot.LastOutput
			instance.Context = snapshot.Context
			instance.PendingSignals = snapshot.PendingSignals
			instance.NextRunAt = snapshot.NextRunAt
			if instance.LastError == "" {
				instance.LastError = snapshot.LastError
			}
		}
	}
	return instance, nil
}

func scanWorkflowTask(scanner interface{ Scan(...any) error }) (WorkflowTask, error) {
	var (
		task         WorkflowTask
		runAt        string
		lastError    sql.NullString
		leaseOwner   sql.NullString
		leaseExpires sql.NullString
		state        sql.NullString
		createdAt    string
		updatedAt    string
	)
	if err := scanner.Scan(
		&task.ID,
		&task.WorkflowID,
		&task.StepIndex,
		&task.StepName,
		&task.ActivityName,
		&task.Status,
		&task.Attempts,
		&task.MaxAttempts,
		&runAt,
		&lastError,
		&leaseOwner,
		&leaseExpires,
		&state,
		&createdAt,
		&updatedAt,
	); err != nil {
		return WorkflowTask{}, fmt.Errorf("scan workflow task: %w", err)
	}
	task.RunAt = mustParseTime(runAt)
	task.LastError = lastError.String
	task.LeaseOwner = leaseOwner.String
	if leaseExpires.Valid {
		parsed := mustParseTime(leaseExpires.String)
		task.LeaseExpiresAt = &parsed
	}
	task.State = json.RawMessage(state.String)
	task.CreatedAt = mustParseTime(createdAt)
	task.UpdatedAt = mustParseTime(updatedAt)
	return task, nil
}

func (s *Service) getTaskTx(ctx context.Context, tx *sql.Tx, taskID int64) (WorkflowTask, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
		       run_at, last_error, lease_owner, lease_expires_at, state_json, created_at, updated_at
		FROM workflow_tasks
		WHERE id = ?
	`, taskID)
	task, err := scanWorkflowTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowTask{}, ErrNotFound
		}
		return WorkflowTask{}, err
	}
	return task, nil
}

func (s *Service) initSchema(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS workflow_definitions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			status TEXT NOT NULL,
			active_version INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS workflow_definition_versions (
			definition_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'published',
			document_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT '',
			published_at TEXT,
			PRIMARY KEY (definition_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS workflow_instances (
			id TEXT PRIMARY KEY,
			definition_id TEXT NOT NULL,
			definition_version INTEGER NOT NULL,
			status TEXT NOT NULL,
			current_step_index INTEGER NOT NULL,
			current_step_name TEXT NOT NULL,
			current_activity TEXT NOT NULL,
			snapshot_json TEXT NOT NULL,
			last_event_sequence INTEGER NOT NULL,
			last_error TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS workflow_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workflow_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			event_type TEXT NOT NULL,
			payload TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE (workflow_id, sequence)
		)`,
		`CREATE TABLE IF NOT EXISTS workflow_tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workflow_id TEXT NOT NULL,
			step_index INTEGER NOT NULL,
			step_name TEXT NOT NULL,
			activity_name TEXT NOT NULL,
			status TEXT NOT NULL,
			attempts INTEGER NOT NULL,
			max_attempts INTEGER NOT NULL,
			run_at TEXT NOT NULL,
			lease_owner TEXT NOT NULL,
			lease_expires_at TEXT,
			last_error TEXT NOT NULL,
			state_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS workflow_signals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workflow_id TEXT NOT NULL,
			signal_name TEXT NOT NULL,
			payload TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			processed_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_tasks_status_run_at ON workflow_tasks(status, run_at)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_tasks_workflow_status ON workflow_tasks(workflow_id, status, run_at)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_events_workflow_sequence ON workflow_events(workflow_id, sequence)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_signals_workflow_created_at ON workflow_signals(workflow_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_instances_updated_at ON workflow_instances(updated_at)`,
		`CREATE TABLE IF NOT EXISTS scripts (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			language TEXT NOT NULL DEFAULT 'starlark',
			source TEXT NOT NULL DEFAULT '',
			timeout_ms INTEGER NOT NULL DEFAULT 0,
			exports_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT 'gpt-4o',
			system_prompt TEXT NOT NULL DEFAULT '',
			max_tokens INTEGER NOT NULL DEFAULT 0,
			temperature REAL NOT NULL DEFAULT 0,
			tools_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS mcp_servers (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			group_name  TEXT NOT NULL DEFAULT '',
			url         TEXT NOT NULL,
			headers_json TEXT NOT NULL DEFAULT '{}',
			enabled     INTEGER NOT NULL DEFAULT 1,
			tools_json  TEXT NOT NULL DEFAULT '[]',
			explored_at TEXT,
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS agent_mcp_servers (
			agent_id  TEXT NOT NULL,
			server_id TEXT NOT NULL,
			PRIMARY KEY (agent_id, server_id)
		)`,
	}

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize workflow schema: %w", err)
		}
	}

	if err := ensureWorkflowTaskColumns(ctx, s.db); err != nil {
		return err
	}
	if err := ensureWorkflowDefinitionVersionColumns(ctx, s.db); err != nil {
		return err
	}
	if err := ensureMCPServerColumns(ctx, s.db); err != nil {
		return err
	}

	return nil
}

func ensureWorkflowTaskColumns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(workflow_tasks)`)
	if err != nil {
		return fmt.Errorf("inspect workflow_tasks schema: %w", err)
	}
	defer rows.Close()

	hasStateJSON := false
	for rows.Next() {
		var (
			cid        int
			name       string
			dataType   string
			notNull    int
			defaultV   sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultV, &primaryKey); err != nil {
			return fmt.Errorf("scan workflow_tasks schema: %w", err)
		}
		if name == "state_json" {
			hasStateJSON = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate workflow_tasks schema: %w", err)
	}
	if hasStateJSON {
		return nil
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE workflow_tasks ADD COLUMN state_json TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add workflow_tasks.state_json column: %w", err)
	}
	return nil
}

func ensureWorkflowDefinitionVersionColumns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(workflow_definition_versions)`)
	if err != nil {
		return fmt.Errorf("inspect workflow_definition_versions schema: %w", err)
	}
	defer rows.Close()

	hasStatus := false
	hasUpdatedAt := false
	hasPublishedAt := false
	for rows.Next() {
		var (
			cid        int
			name       string
			dataType   string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultVal, &primaryKey); err != nil {
			return fmt.Errorf("scan workflow_definition_versions schema: %w", err)
		}
		switch name {
		case "status":
			hasStatus = true
		case "updated_at":
			hasUpdatedAt = true
		case "published_at":
			hasPublishedAt = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate workflow_definition_versions schema: %w", err)
	}

	if !hasStatus {
		if _, err := db.ExecContext(ctx, `ALTER TABLE workflow_definition_versions ADD COLUMN status TEXT NOT NULL DEFAULT 'published'`); err != nil {
			return fmt.Errorf("add workflow_definition_versions.status column: %w", err)
		}
	}
	if !hasUpdatedAt {
		if _, err := db.ExecContext(ctx, `ALTER TABLE workflow_definition_versions ADD COLUMN updated_at TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add workflow_definition_versions.updated_at column: %w", err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE workflow_definition_versions SET updated_at = created_at WHERE updated_at = ''`); err != nil {
			return fmt.Errorf("backfill workflow_definition_versions.updated_at: %w", err)
		}
	}
	if !hasPublishedAt {
		if _, err := db.ExecContext(ctx, `ALTER TABLE workflow_definition_versions ADD COLUMN published_at TEXT`); err != nil {
			return fmt.Errorf("add workflow_definition_versions.published_at column: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE workflow_definition_versions SET published_at = created_at WHERE status = 'published' AND (published_at IS NULL OR published_at = '')`); err != nil {
		return fmt.Errorf("backfill workflow_definition_versions.published_at: %w", err)
	}
	return nil
}

func ensureMCPServerColumns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(mcp_servers)`)
	if err != nil {
		return fmt.Errorf("inspect mcp_servers schema: %w", err)
	}
	defer rows.Close()

	hasToolsJSON := false
	hasExploredAt := false
	for rows.Next() {
		var (
			cid        int
			name       string
			dataType   string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultVal, &primaryKey); err != nil {
			return fmt.Errorf("scan mcp_servers schema: %w", err)
		}
		switch name {
		case "tools_json":
			hasToolsJSON = true
		case "explored_at":
			hasExploredAt = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate mcp_servers schema: %w", err)
	}

	if !hasToolsJSON {
		if _, err := db.ExecContext(ctx, `ALTER TABLE mcp_servers ADD COLUMN tools_json TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return fmt.Errorf("add mcp_servers.tools_json column: %w", err)
		}
	}
	if !hasExploredAt {
		if _, err := db.ExecContext(ctx, `ALTER TABLE mcp_servers ADD COLUMN explored_at TEXT`); err != nil {
			return fmt.Errorf("add mcp_servers.explored_at column: %w", err)
		}
	}
	return nil
}
