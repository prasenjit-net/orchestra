package workflow

import (
	"context"
	"fmt"
)

// DeleteWorkflowDefinition deletes a workflow definition and all its versions, provided
// no active (non-terminal) instances are currently running against it.
// Past/completed/failed/canceled instances are intentionally left untouched.
func (s *Service) DeleteWorkflowDefinition(ctx context.Context, id string) error {
	activeRefs, err := s.findActiveInstancesForDefinition(ctx, id)
	if err != nil {
		return fmt.Errorf("usage check for workflow definition: %w", err)
	}
	if len(activeRefs) > 0 {
		return &EntityInUseError{Kind: "workflow_definition", ID: id, References: activeRefs}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM workflow_definition_versions WHERE definition_id = ?`), id); err != nil {
		return fmt.Errorf("delete workflow definition versions: %w", err)
	}
	res, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM workflow_definitions WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("delete workflow definition: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	s.emitLiveEvent("workflow_definition.deleted", "workflow_definition", id, nil)
	return nil
}
