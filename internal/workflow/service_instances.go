package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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

func (s *Service) StartWorkflow(ctx context.Context, definitionID string) (WorkflowInstance, error) {
	return s.StartWorkflowWithInput(ctx, StartWorkflowInput{
		DefinitionID:  definitionID,
		TriggerSource: "ui",
	})
}

func (s *Service) StartWorkflowWithInput(ctx context.Context, in StartWorkflowInput) (WorkflowInstance, error) {
	if in.TriggerSource == "" {
		in.TriggerSource = "ui"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return WorkflowInstance{}, fmt.Errorf("begin workflow transaction: %w", err)
	}
	defer tx.Rollback()

	details, err := s.getDefinitionTx(ctx, tx, in.DefinitionID)
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
		Context:           buildInitialContext(details.ID, details.ActiveVersion, in.Input),
		CallbackURL:       in.CallbackURL,
		TriggerSource:     in.TriggerSource,
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

	if _, err := tx.ExecContext(ctx, s.rebind(`
		INSERT INTO workflow_instances (
			id, definition_id, definition_version, status, current_step_index, current_step_name,
			current_activity, snapshot_json, last_event_sequence, last_error,
			callback_url, trigger_source, callback_status,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, '', ?, ?, '', ?, ?)
	`), instance.ID, instance.DefinitionID, instance.DefinitionVersion, instance.Status,
		instance.CurrentStepIndex, instance.CurrentStepName, instance.CurrentActivity,
		snapshotJSON, instance.CallbackURL, instance.TriggerSource,
		formatTime(now), formatTime(now)); err != nil {
		return WorkflowInstance{}, fmt.Errorf("insert workflow instance: %w", err)
	}

	sequence := 0
	sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "WorkflowStarted", map[string]any{
		"definitionId":      instance.DefinitionID,
		"definitionVersion": instance.DefinitionVersion,
	})
	if err != nil {
		return WorkflowInstance{}, err
	}

	if len(details.Document.Steps) == 0 {
		sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "WorkflowCompleted", map[string]any{
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
		sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "ActivityScheduled", map[string]any{
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
		if err := insertTaskTx(ctx, tx, s.rebind, instance.ID, 0, firstStep, now); err != nil {
			return WorkflowInstance{}, err
		}
		if err := s.updateInstanceTx(ctx, tx, instance, snapshotFromInstance(instance), nil); err != nil {
			return WorkflowInstance{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return WorkflowInstance{}, fmt.Errorf("commit workflow transaction: %w", err)
	}
	s.notifyWorker()

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

// ListWorkflowsInput controls filtering and pagination for ListWorkflows.
// A zero Limit returns all rows (no LIMIT clause).
type ListWorkflowsInput struct {
	Limit             int
	Offset            int
	Status            string   // empty = all statuses
	CurrentActivities []string // when non-empty, filter to these activity names
}

// ListWorkflowsResult holds a page of workflow instances and related counts.
type ListWorkflowsResult struct {
	Workflows      []WorkflowInstance
	Total          int
	ActivityCounts map[string]int // counts per current_activity; populated when CurrentActivities filter is used
}

func (s *Service) ListWorkflows(ctx context.Context, input ListWorkflowsInput) (ListWorkflowsResult, error) {
	// Build the WHERE clause.
	conditions := []string{}
	args := []any{}
	if input.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, input.Status)
	}
	if len(input.CurrentActivities) > 0 {
		ph := strings.Repeat("?,", len(input.CurrentActivities))
		conditions = append(conditions, "current_activity IN ("+ph[:len(ph)-1]+")")
		for _, a := range input.CurrentActivities {
			args = append(args, a)
		}
	}
	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Total count.
	var total int
	if err := s.db.QueryRowContext(ctx, s.rebind("SELECT COUNT(*) FROM workflow_instances "+where), args...).Scan(&total); err != nil {
		return ListWorkflowsResult{}, fmt.Errorf("count workflow instances: %w", err)
	}

	// Per-activity counts (only when filtering by activities — used by the signals page).
	var activityCounts map[string]int
	if len(input.CurrentActivities) > 0 {
		activityCounts = make(map[string]int)
		countArgs := make([]any, 0, len(args))
		countArgs = append(countArgs, args...)
		countRows, err := s.db.QueryContext(ctx,
			s.rebind("SELECT current_activity, COUNT(*) FROM workflow_instances "+where+" GROUP BY current_activity"),
			countArgs...)
		if err != nil {
			return ListWorkflowsResult{}, fmt.Errorf("count workflow activities: %w", err)
		}
		defer countRows.Close()
		for countRows.Next() {
			var activity string
			var count int
			if err := countRows.Scan(&activity, &count); err != nil {
				return ListWorkflowsResult{}, err
			}
			activityCounts[activity] = count
		}
		if err := countRows.Err(); err != nil {
			return ListWorkflowsResult{}, fmt.Errorf("iterate activity counts: %w", err)
		}
	}

	query := `SELECT id, definition_id, definition_version, status, current_step_index, current_step_name,
		       current_activity, snapshot_json, last_event_sequence, last_error, created_at, updated_at,
		       callback_url, trigger_source, callback_status
		FROM workflow_instances ` + where + ` ORDER BY updated_at DESC, created_at DESC`

	pageArgs := make([]any, len(args))
	copy(pageArgs, args)
	if input.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		pageArgs = append(pageArgs, input.Limit, input.Offset)
	}

	rows, err := s.db.QueryContext(ctx, s.rebind(query), pageArgs...)
	if err != nil {
		return ListWorkflowsResult{}, fmt.Errorf("query workflow instances: %w", err)
	}
	defer rows.Close()

	workflows := make([]WorkflowInstance, 0)
	for rows.Next() {
		instance, err := scanWorkflowInstance(rows)
		if err != nil {
			return ListWorkflowsResult{}, err
		}
		workflows = append(workflows, instance)
	}

	if err := rows.Err(); err != nil {
		return ListWorkflowsResult{}, fmt.Errorf("iterate workflow instances: %w", err)
	}

	return ListWorkflowsResult{Workflows: workflows, Total: total, ActivityCounts: activityCounts}, nil
}

func (s *Service) GetWorkflow(ctx context.Context, workflowID string) (WorkflowInstance, error) {
	row := s.db.QueryRowContext(ctx, s.rebind(`
		SELECT id, definition_id, definition_version, status, current_step_index, current_step_name,
		       current_activity, snapshot_json, last_event_sequence, last_error, created_at, updated_at,
		       callback_url, trigger_source, callback_status
		FROM workflow_instances
		WHERE id = ?
	`), workflowID)

	instance, err := scanWorkflowInstance(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowInstance{}, ErrNotFound
		}
		return WorkflowInstance{}, err
	}
	return instance, nil
}

// WorkflowHistoryInput controls pagination for GetWorkflowHistory.
// Limit 0 means no limit (returns all events). Offset 0 starts from the beginning.
type WorkflowHistoryInput struct {
	Limit  int
	Offset int
}

// WorkflowHistoryResult is the paginated result of GetWorkflowHistory.
type WorkflowHistoryResult struct {
	Events []WorkflowEvent `json:"events"`
	Total  int             `json:"total"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

func (s *Service) GetWorkflowHistory(ctx context.Context, workflowID string, input WorkflowHistoryInput) (WorkflowHistoryResult, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, s.rebind(`
		SELECT COUNT(*) FROM workflow_events WHERE workflow_id = ?
	`), workflowID).Scan(&total); err != nil {
		return WorkflowHistoryResult{}, fmt.Errorf("count workflow events: %w", err)
	}

	query := `
		SELECT sequence, event_type, payload, created_at
		FROM workflow_events
		WHERE workflow_id = ?
		ORDER BY sequence ASC`
	args := []any{workflowID}
	if input.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, input.Limit, input.Offset)
	} else if input.Offset > 0 {
		query += ` LIMIT -1 OFFSET ?`
		args = append(args, input.Offset)
	}

	rows, err := s.db.QueryContext(ctx, s.rebind(query), args...)
	if err != nil {
		return WorkflowHistoryResult{}, fmt.Errorf("query workflow events: %w", err)
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
			return WorkflowHistoryResult{}, fmt.Errorf("scan workflow event: %w", err)
		}
		event.WorkflowID = workflowID
		event.Payload = json.RawMessage(payloadText)
		event.CreatedAt = mustParseTime(createdAt)
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return WorkflowHistoryResult{}, fmt.Errorf("iterate workflow events: %w", err)
	}

	return WorkflowHistoryResult{
		Events: events,
		Total:  total,
		Limit:  input.Limit,
		Offset: input.Offset,
	}, nil
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

	if _, err := tx.ExecContext(ctx, s.rebind(`
		INSERT INTO workflow_signals (workflow_id, signal_name, payload, status, created_at, processed_at)
		VALUES (?, ?, ?, 'processed', ?, ?)
	`), workflowID, name, string(payload), formatTime(now), formatTime(now)); err != nil {
		return WorkflowInstance{}, fmt.Errorf("insert workflow signal: %w", err)
	}

	sequence, err := appendEventTx(ctx, tx, s.rebind, workflowID, instance.LastEventSequence, "WorkflowSignaled", map[string]any{
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
	if len(wokenTaskIDs) > 0 {
		s.notifyWorker()
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
	sequence, err := appendEventTx(ctx, tx, s.rebind, workflowID, instance.LastEventSequence, "WorkflowCanceled", map[string]any{
		"canceledAt": formatTime(now),
	})
	if err != nil {
		return WorkflowInstance{}, err
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_tasks
		SET status = ?, lease_owner = '', lease_expires_at = NULL, updated_at = ?
		WHERE workflow_id = ? AND status NOT IN (?, ?)
	`), StatusCanceled, formatTime(now), workflowID, StatusCompleted, StatusCanceled); err != nil {
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

type ListRecentEventsInput struct {
	Limit  int
	Offset int
}

type ListRecentEventsResult struct {
	Events []WorkflowEvent
	Total  int
}

func (s *Service) ListRecentEvents(ctx context.Context, input ListRecentEventsInput) (ListRecentEventsResult, error) {
	if input.Limit <= 0 {
		input.Limit = 50
	}
	if input.Limit > 200 {
		input.Limit = 200
	}

	var total int
	if err := s.db.QueryRowContext(ctx, s.rebind("SELECT COUNT(*) FROM workflow_events")).Scan(&total); err != nil {
		return ListRecentEventsResult{}, fmt.Errorf("count workflow events: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, s.rebind(`
		SELECT workflow_id, sequence, event_type, payload, created_at
		FROM workflow_events
		ORDER BY id DESC
		LIMIT ? OFFSET ?
	`), input.Limit, input.Offset)
	if err != nil {
		return ListRecentEventsResult{}, fmt.Errorf("query recent workflow events: %w", err)
	}
	defer rows.Close()

	events := make([]WorkflowEvent, 0)
	for rows.Next() {
		var (
			event       WorkflowEvent
			payloadText string
			createdAt   string
		)
		if err := rows.Scan(&event.WorkflowID, &event.Sequence, &event.EventType, &payloadText, &createdAt); err != nil {
			return ListRecentEventsResult{}, fmt.Errorf("scan recent workflow event: %w", err)
		}
		event.Payload = json.RawMessage(payloadText)
		event.CreatedAt = mustParseTime(createdAt)
		events = append(events, event)
	}

	if err := rows.Err(); err != nil {
		return ListRecentEventsResult{}, fmt.Errorf("iterate recent workflow events: %w", err)
	}

	return ListRecentEventsResult{Events: events, Total: total}, nil
}

func (s *Service) wakeTasksWaitingForSignalTx(ctx context.Context, tx *sql.Tx, workflowID string, signalName string, now time.Time) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, s.rebind(`
		SELECT id, workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
		       run_at, last_error, lease_owner, lease_expires_at, state_json, executed_by, created_at, updated_at
		FROM workflow_tasks
		WHERE workflow_id = ? AND status = ?
		ORDER BY id ASC
	`), workflowID, StatusWaiting)
	if err != nil {
		return nil, fmt.Errorf("query signal-waiting tasks: %w", err)
	}

	// Collect matching IDs while the cursor is open, then close it before
	// executing any DML. With pgx the connection is still reading rows until
	// rows.Close(), so running ExecContext inside the loop causes a "conn busy"
	// error on PostgreSQL, silently dropping the wake-up.
	var idsToWake []int64
	for rows.Next() {
		task, err := scanWorkflowTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		state, initialized, err := decodeWaitSignalState(task.State)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if !initialized || strings.TrimSpace(state.SignalName) != signalName {
			continue
		}
		idsToWake = append(idsToWake, task.ID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close signal-waiting tasks cursor: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate signal-waiting tasks: %w", err)
	}

	for _, id := range idsToWake {
		if _, err := tx.ExecContext(ctx, s.rebind(`
			UPDATE workflow_tasks
			SET status = ?, run_at = ?, lease_owner = '', lease_expires_at = NULL, updated_at = ?
			WHERE id = ?
		`), StatusPending, formatTime(now), formatTime(now), id); err != nil {
			return nil, fmt.Errorf("wake signal-waiting task %d: %w", id, err)
		}
	}
	return idsToWake, nil
}

func (s *Service) getWorkflowTx(ctx context.Context, tx *sql.Tx, workflowID string) (WorkflowInstance, error) {
	row := tx.QueryRowContext(ctx, s.rebind(`
		SELECT id, definition_id, definition_version, status, current_step_index, current_step_name,
		       current_activity, snapshot_json, last_event_sequence, last_error, created_at, updated_at,
		       callback_url, trigger_source, callback_status
		FROM workflow_instances
		WHERE id = ?
	`), workflowID)

	instance, err := scanWorkflowInstance(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowInstance{}, ErrNotFound
		}
		return WorkflowInstance{}, err
	}

	return instance, nil
}

func (s *Service) updateInstanceTx(ctx context.Context, tx *sql.Tx, instance WorkflowInstance, snapshot workflowSnapshot, lastOutput json.RawMessage) error {
	if len(lastOutput) > 0 {
		snapshot.LastOutput = lastOutput
	}
	snapshotJSON, err := buildSnapshotJSON(snapshot)
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_instances
		SET status = ?, current_step_index = ?, current_step_name = ?, current_activity = ?,
		    snapshot_json = ?, last_event_sequence = ?, last_error = ?, updated_at = ?
		WHERE id = ?
	`), instance.Status, instance.CurrentStepIndex, instance.CurrentStepName, instance.CurrentActivity, snapshotJSON, instance.LastEventSequence, instance.LastError, formatTime(instance.UpdatedAt), instance.ID); err != nil {
		return fmt.Errorf("update workflow instance: %w", err)
	}

	return nil
}

func (s *Service) deliverCallback(instance WorkflowInstance) {
	if instance.CallbackURL == "" {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"workflowId":   instance.ID,
		"definitionId": instance.DefinitionID,
		"status":       instance.Status,
		"output":       decodeJSONObject(instance.LastOutput),
		"context":      decodeJSONObject(instance.Context),
		"completedAt":  formatTime(instance.UpdatedAt),
	})
	if err != nil {
		s.logger.Error("callback: marshal payload", "workflowId", instance.ID, "error", err)
		return
	}

	delays := []time.Duration{0, 5 * time.Second, 15 * time.Second, 45 * time.Second}
	var lastErr error
	for attempt, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		lastErr = s.doCallbackAttempt(instance.CallbackURL, instance.ID, payload)
		if lastErr == nil {
			s.setCallbackStatus(instance.ID, "delivered")
			return
		}
		s.logger.Warn("callback attempt failed", "workflowId", instance.ID, "attempt", attempt+1, "error", lastErr)
	}
	s.logger.Error("callback delivery failed after all attempts", "workflowId", instance.ID, "error", lastErr)
	s.setCallbackStatus(instance.ID, "failed")
}

func (s *Service) doCallbackAttempt(callbackURL, workflowID string, payload []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, strings.NewReader(string(payload)))
	if err != nil {
		return fmt.Errorf("build callback request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Orchestra-Workflow-ID", workflowID)
	req.Header.Set("X-Orchestra-Event", "workflow.completed")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("callback request: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("callback returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *Service) setCallbackStatus(workflowID, status string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.db.ExecContext(ctx,
		s.rebind(`UPDATE workflow_instances SET callback_status = ? WHERE id = ?`),
		status, workflowID,
	); err != nil {
		s.logger.Error("update callback_status", "workflowId", workflowID, "error", err)
	}
}

func buildInitialContext(definitionID string, version int, input map[string]any) json.RawMessage {
	if input == nil {
		input = map[string]any{}
	}
	ctx := map[string]any{
		"workflow": map[string]any{
			"definitionId":      definitionID,
			"definitionVersion": version,
		},
		"input":   input,
		"steps":   map[string]any{},
		"signals": map[string]any{},
	}
	payload, _ := json.Marshal(ctx)
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

func buildSnapshotJSON(snapshot workflowSnapshot) (string, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("encode workflow snapshot: %w", err)
	}
	return string(payload), nil
}

func scanWorkflowInstance(scanner interface{ Scan(...any) error }) (WorkflowInstance, error) {
	var (
		instance     WorkflowInstance
		snapshotJSON string
		lastError    sql.NullString
		createdAt    string
		updatedAt    string
		callbackURL  sql.NullString
		triggerSrc   sql.NullString
		callbackSt   sql.NullString
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
		&callbackURL,
		&triggerSrc,
		&callbackSt,
	); err != nil {
		return WorkflowInstance{}, fmt.Errorf("scan workflow instance: %w", err)
	}
	instance.LastError = lastError.String
	instance.CreatedAt = mustParseTime(createdAt)
	instance.UpdatedAt = mustParseTime(updatedAt)
	instance.CallbackURL = callbackURL.String
	instance.TriggerSource = triggerSrc.String
	instance.CallbackStatus = callbackSt.String
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
