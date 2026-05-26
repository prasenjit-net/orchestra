package workflow

import (
	"context"
	"database/sql"
	"fmt"
)

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
	if err := ensureWorkflowInstanceColumns(ctx, s.db); err != nil {
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

func ensureWorkflowInstanceColumns(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(workflow_instances)`)
	if err != nil {
		return fmt.Errorf("inspect workflow_instances schema: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]bool)
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
			return fmt.Errorf("scan workflow_instances schema: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate workflow_instances schema: %w", err)
	}

	migrations := []struct {
		column string
		ddl    string
	}{
		{"callback_url", `ALTER TABLE workflow_instances ADD COLUMN callback_url TEXT NOT NULL DEFAULT ''`},
		{"trigger_source", `ALTER TABLE workflow_instances ADD COLUMN trigger_source TEXT NOT NULL DEFAULT 'ui'`},
		{"callback_status", `ALTER TABLE workflow_instances ADD COLUMN callback_status TEXT NOT NULL DEFAULT ''`},
	}
	for _, m := range migrations {
		if existing[m.column] {
			continue
		}
		if _, err := db.ExecContext(ctx, m.ddl); err != nil {
			return fmt.Errorf("add workflow_instances.%s column: %w", m.column, err)
		}
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
