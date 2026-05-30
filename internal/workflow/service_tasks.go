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

// ListTasksInput controls filtering and pagination for ListTasks. A zero Limit returns all rows.
type ListTasksInput struct {
	Limit            int
	Offset           int
	Status           string // empty = all statuses
	ExcludeCompleted bool   // when true, tasks with status = 'completed' are excluded
}

// ListTasksResult holds a page of tasks, the total for the current filter, and global status counts.
type ListTasksResult struct {
	Tasks  []WorkflowTask
	Total  int
	Counts map[string]int // global counts per status regardless of the Status filter
}

func (s *Service) ListTasks(ctx context.Context, input ListTasksInput) (ListTasksResult, error) {
	// Global status counts (always across all tasks, ignoring the Status filter).
	counts := make(map[string]int)
	countRows, err := s.db.QueryContext(ctx, "SELECT status, COUNT(*) FROM workflow_tasks GROUP BY status")
	if err != nil {
		return ListTasksResult{}, fmt.Errorf("count workflow tasks by status: %w", err)
	}
	defer countRows.Close()
	for countRows.Next() {
		var st string
		var n int
		if err := countRows.Scan(&st, &n); err != nil {
			return ListTasksResult{}, err
		}
		counts[st] = n
	}
	if err := countRows.Err(); err != nil {
		return ListTasksResult{}, fmt.Errorf("iterate task counts: %w", err)
	}

	// Filtered total.
	var conds []string
	var filterArgs []any
	if input.Status != "" {
		conds = append(conds, "status = ?")
		filterArgs = append(filterArgs, input.Status)
	}
	if input.ExcludeCompleted {
		conds = append(conds, "status != 'completed'")
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, s.rebind("SELECT COUNT(*) FROM workflow_tasks "+where), filterArgs...).Scan(&total); err != nil {
		return ListTasksResult{}, fmt.Errorf("count workflow tasks: %w", err)
	}

	query := `SELECT id, workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
		       run_at, last_error, lease_owner, lease_expires_at, state_json, executed_by, created_at, updated_at
		FROM workflow_tasks ` + where + `
		ORDER BY CASE status
			WHEN 'failed'  THEN 0
			WHEN 'paused'  THEN 1
			WHEN 'waiting' THEN 2
			WHEN 'running' THEN 3
			WHEN 'pending' THEN 4
			ELSE 5
		END, run_at ASC, id ASC`

	pageArgs := make([]any, len(filterArgs))
	copy(pageArgs, filterArgs)
	if input.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		pageArgs = append(pageArgs, input.Limit, input.Offset)
	}

	rows, err := s.db.QueryContext(ctx, s.rebind(query), pageArgs...)
	if err != nil {
		return ListTasksResult{}, fmt.Errorf("query workflow tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]WorkflowTask, 0)
	for rows.Next() {
		task, err := scanWorkflowTask(rows)
		if err != nil {
			return ListTasksResult{}, err
		}
		tasks = append(tasks, task)
	}

	if err := rows.Err(); err != nil {
		return ListTasksResult{}, fmt.Errorf("iterate workflow tasks: %w", err)
	}

	return ListTasksResult{Tasks: tasks, Total: total, Counts: counts}, nil
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

	sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, eventType, map[string]any{
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
		sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "WorkflowCanceled", map[string]any{
			"taskId":    task.ID,
			"stepIndex": task.StepIndex,
			"stepName":  task.StepName,
		})
		if err != nil {
			return WorkflowTask{}, err
		}
	}

	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_tasks
		SET status = ?, attempts = ?, run_at = ?, last_error = ?, lease_owner = ?, lease_expires_at = ?, state_json = ?, updated_at = ?
		WHERE id = ?
	`), task.Status, task.Attempts, formatTime(task.RunAt), task.LastError, task.LeaseOwner, nullableTime(task.LeaseExpiresAt), rawJSONString(task.State), formatTime(task.UpdatedAt), task.ID); err != nil {
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

	sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "ActivityCompleted", map[string]any{
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

	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_tasks
		SET status = ?, lease_owner = '', lease_expires_at = NULL, state_json = '', updated_at = ?
		WHERE id = ?
	`), StatusCompleted, formatTime(now), task.ID); err != nil {
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
		sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "WorkflowCompleted", map[string]any{
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
			sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "TransitionSelected", map[string]any{
				"fromStep": task.StepName,
				"toStep":   nextStep.Name,
				"label":    selectedTransition.Label,
			})
			if err != nil {
				return err
			}
		}
		sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "ActivityScheduled", map[string]any{
			"stepIndex":   nextStepIndex,
			"stepName":    nextStep.Name,
			"activity":    nextStep.Activity,
			"maxAttempts": nextStep.Retry.MaxAttempts,
		})
		if err != nil {
			return err
		}
		if err := insertTaskTx(ctx, tx, s.rebind, instance.ID, nextStepIndex, nextStep, now); err != nil {
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
	if nextStepIndex >= 0 {
		s.notifyWorker()
	}

	s.emitLiveEvent("task.completed", "task", fmt.Sprintf("%d", task.ID), map[string]any{
		"taskId":     task.ID,
		"workflowId": task.WorkflowID,
		"stepIndex":  task.StepIndex,
		"stepName":   step.Name,
		"activity":   step.Activity,
		"status":     StatusCompleted,
	})
	if nextStepIndex < 0 {
		s.emitLiveEvent("workflow.completed", "workflow", task.WorkflowID, map[string]any{
			"workflowId": task.WorkflowID,
			"status":     StatusCompleted,
		})
		s.emitOperationEvent(task.WorkflowID, "WorkflowCompleted", map[string]any{
			"stepIndex": task.StepIndex,
			"stepName":  step.Name,
		})
		if instance.CallbackURL != "" {
			callbackURL := instance.CallbackURL
			workflowID := task.WorkflowID
			go func() {
				completed, err := s.GetWorkflow(context.Background(), workflowID)
				if err != nil {
					s.logger.Error("callback: fetch completed instance", "workflowId", workflowID, "error", err)
					return
				}
				completed.CallbackURL = callbackURL
				s.deliverCallback(completed)
			}()
		}
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
	sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "ActivityWaiting", map[string]any{
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

	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_tasks
		SET status = ?, attempts = ?, run_at = ?, last_error = '', lease_owner = '', lease_expires_at = NULL, state_json = ?, updated_at = ?
		WHERE id = ?
	`), StatusPending, attempts, formatTime(runAt), rawJSONString(state), formatTime(now), task.ID); err != nil {
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
	sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "ActivityWaitingForSignal", payload)
	if err != nil {
		return err
	}

	attempts := task.Attempts
	if attempts > 0 {
		attempts--
	}
	waitRunAt := parkedSignalRunAt(wait.TimeoutAt)
	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_tasks
		SET status = ?, attempts = ?, run_at = ?, last_error = '', lease_owner = '', lease_expires_at = NULL, state_json = ?, updated_at = ?
		WHERE id = ?
	`), StatusWaiting, attempts, formatTime(waitRunAt), rawJSONString(wait.State), formatTime(now), task.ID); err != nil {
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
		sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "ActivityRetryScheduled", map[string]any{
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

		if _, err := tx.ExecContext(ctx, s.rebind(`
			UPDATE workflow_tasks
			SET status = ?, run_at = ?, last_error = ?, lease_owner = '', lease_expires_at = NULL, state_json = '', updated_at = ?
			WHERE id = ?
		`), StatusPending, formatTime(nextRunAt), execErr.Error(), formatTime(now), task.ID); err != nil {
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
		sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "ActivityFailed", map[string]any{
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
		sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "WorkflowFailed", map[string]any{
			"stepIndex": task.StepIndex,
			"stepName":  step.Name,
			"error":     execErr.Error(),
		})
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, s.rebind(`
			UPDATE workflow_tasks
			SET status = ?, last_error = ?, lease_owner = '', lease_expires_at = NULL, state_json = '', updated_at = ?
			WHERE id = ?
		`), StatusFailed, execErr.Error(), formatTime(now), task.ID); err != nil {
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
	sequence, err = appendEventTx(ctx, tx, s.rebind, instance.ID, sequence, "WorkflowFailed", map[string]any{
		"stepIndex": task.StepIndex,
		"stepName":  task.StepName,
		"error":     cause.Error(),
	})
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_tasks
		SET status = ?, last_error = ?, lease_owner = '', lease_expires_at = NULL, state_json = '', updated_at = ?
		WHERE id = ?
	`), StatusFailed, cause.Error(), formatTime(now), task.ID); err != nil {
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
	rows, err := tx.QueryContext(ctx, s.rebind(`
		SELECT id, workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
		       run_at, last_error, lease_owner, lease_expires_at, state_json, executed_by, created_at, updated_at
		FROM workflow_tasks
		WHERE (status = ? AND run_at <= ?) OR (status = ? AND run_at <= ?)
		ORDER BY run_at ASC, id ASC
		LIMIT 1
	`), StatusPending, formatTime(now), StatusWaiting, formatTime(now))
	if err != nil {
		return WorkflowTask{}, false, fmt.Errorf("select runnable workflow task: %w", err)
	}

	if !rows.Next() {
		rows.Close()
		return WorkflowTask{}, false, nil
	}
	task, err := scanWorkflowTask(rows)
	// Close the cursor before executing any DML on the same connection.
	// pgx streams rows lazily; the connection is busy until rows.Close(),
	// so calling ExecContext with an open cursor returns a "conn busy" error.
	if closeErr := rows.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return WorkflowTask{}, false, err
	}

	leaseExpiresAt := now.Add(s.cfg.LeaseDuration)
	result, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE workflow_tasks
		SET status = ?, attempts = attempts + 1, lease_owner = ?, lease_expires_at = ?, executed_by = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`), StatusRunning, s.workerID, formatTime(leaseExpiresAt), s.workerID, formatTime(now), task.ID, StatusPending)
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
	row := s.db.QueryRowContext(ctx, s.rebind(`
		SELECT id, workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
		       run_at, last_error, lease_owner, lease_expires_at, state_json, executed_by, created_at, updated_at
		FROM workflow_tasks
		WHERE id = ?
	`), taskID)
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
	result, err := s.db.ExecContext(ctx, s.rebind(`
		UPDATE workflow_tasks
		SET status = ?, lease_owner = '', lease_expires_at = NULL, updated_at = ?
		WHERE status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?
	`), StatusPending, formatTime(now), StatusRunning, formatTime(now))
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

func (s *Service) getTaskTx(ctx context.Context, tx *sql.Tx, taskID int64) (WorkflowTask, error) {
	row := tx.QueryRowContext(ctx, s.rebind(`
		SELECT id, workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
		       run_at, last_error, lease_owner, lease_expires_at, state_json, executed_by, created_at, updated_at
		FROM workflow_tasks
		WHERE id = ?
	`), taskID)
	task, err := scanWorkflowTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WorkflowTask{}, ErrNotFound
		}
		return WorkflowTask{}, err
	}
	return task, nil
}

func insertTaskTx(ctx context.Context, tx *sql.Tx, rebind func(string) string, workflowID string, stepIndex int, step StepDefinition, now time.Time) error {
	_, err := tx.ExecContext(ctx, rebind(`
		INSERT INTO workflow_tasks (
			workflow_id, step_index, step_name, activity_name, status, attempts, max_attempts,
			run_at, lease_owner, lease_expires_at, last_error, state_json, executed_by, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 0, ?, ?, '', NULL, '', '', '', ?, ?)
	`), workflowID, stepIndex, step.Name, step.Activity, StatusPending, step.Retry.MaxAttempts, formatTime(now), formatTime(now), formatTime(now))
	if err != nil {
		return fmt.Errorf("insert workflow task: %w", err)
	}
	return nil
}

func appendEventTx(ctx context.Context, tx *sql.Tx, rebind func(string) string, workflowID string, sequence int, eventType string, payload any) (int, error) {
	nextSequence := sequence + 1
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return sequence, fmt.Errorf("encode workflow event %s: %w", eventType, err)
	}

	if _, err := tx.ExecContext(ctx, rebind(`
		INSERT INTO workflow_events (workflow_id, sequence, event_type, payload, created_at)
		VALUES (?, ?, ?, ?, ?)
	`), workflowID, nextSequence, eventType, string(payloadJSON), formatTime(time.Now().UTC())); err != nil {
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

func scanWorkflowTask(scanner interface{ Scan(...any) error }) (WorkflowTask, error) {
	var (
		task         WorkflowTask
		runAt        string
		lastError    sql.NullString
		leaseOwner   sql.NullString
		leaseExpires sql.NullString
		state        sql.NullString
		executedBy   sql.NullString
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
		&executedBy,
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
	task.ExecutedBy = executedBy.String
	task.CreatedAt = mustParseTime(createdAt)
	task.UpdatedAt = mustParseTime(updatedAt)
	return task, nil
}
