package auth

import (
	"context"
	"database/sql"
	"fmt"

	appdb "github.com/prasenjit-net/orchestra/internal/database"
)

func SchemaStatements(dialect appdb.Dialect) []string {
	auditID := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if dialect.IsPostgres() {
		auditID = "BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY"
	}
	return []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			username_normalized TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			must_change_password INTEGER NOT NULL DEFAULT 0,
			failed_login_count INTEGER NOT NULL DEFAULT 0,
			locked_until TEXT,
			password_changed_at TEXT NOT NULL,
			last_login_at TEXT,
			authz_version BIGINT NOT NULL DEFAULT 1,
			created_by TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_users_status_role ON users(status, role)`,
		`CREATE TABLE IF NOT EXISTS user_entitlements (
			user_id TEXT NOT NULL,
			permission TEXT NOT NULL,
			effect TEXT NOT NULL,
			created_by TEXT,
			created_at TEXT NOT NULL,
			PRIMARY KEY (user_id, permission),
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			token_hash TEXT NOT NULL UNIQUE,
			csrf_token TEXT NOT NULL,
			user_id TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			idle_expires_at TEXT NOT NULL,
			absolute_expires_at TEXT NOT NULL,
			password_changed_at_login TEXT NOT NULL,
			authz_version_at_login BIGINT NOT NULL,
			revoked_at TEXT,
			revoke_reason TEXT NOT NULL DEFAULT '',
			source_ip TEXT NOT NULL DEFAULT '',
			user_agent_hash TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_active ON sessions(user_id, revoked_at, absolute_expires_at)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			key_prefix TEXT NOT NULL UNIQUE,
			secret_hash TEXT NOT NULL,
			created_by_user_id TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'active',
			expires_at TEXT,
			last_used_at TEXT,
			last_used_ip TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			revoked_at TEXT,
			revoked_by TEXT,
			rotated_from_id TEXT,
			FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE RESTRICT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_api_keys_owner_status ON api_keys(created_by_user_id, status)`,
		`CREATE TABLE IF NOT EXISTS api_key_workflow_grants (
			api_key_id TEXT NOT NULL,
			workflow_definition_id TEXT NOT NULL,
			action TEXT NOT NULL,
			instance_scope TEXT NOT NULL DEFAULT 'own',
			allow_pinned_versions INTEGER NOT NULL DEFAULT 0,
			allow_callback_url INTEGER NOT NULL DEFAULT 0,
			signal_names_json TEXT,
			created_at TEXT NOT NULL,
			PRIMARY KEY (api_key_id, workflow_definition_id, action),
			FOREIGN KEY (api_key_id) REFERENCES api_keys(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_api_key_grants_workflow ON api_key_workflow_grants(workflow_definition_id, action)`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS security_audit_events (
			id %s,
			occurred_at TEXT NOT NULL,
			request_id TEXT NOT NULL DEFAULT '',
			actor_type TEXT NOT NULL,
			actor_id TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			resource_type TEXT NOT NULL DEFAULT '',
			resource_id TEXT NOT NULL DEFAULT '',
			outcome TEXT NOT NULL,
			source_ip TEXT NOT NULL DEFAULT '',
			user_agent TEXT NOT NULL DEFAULT '',
			metadata_json TEXT NOT NULL DEFAULT '{}'
		)`, auditID),
		`CREATE INDEX IF NOT EXISTS idx_security_audit_time ON security_audit_events(occurred_at)`,
		`CREATE INDEX IF NOT EXISTS idx_security_audit_actor ON security_audit_events(actor_type, actor_id, occurred_at)`,
		`CREATE INDEX IF NOT EXISTS idx_security_audit_action ON security_audit_events(action, occurred_at)`,
		`CREATE TABLE IF NOT EXISTS security_rate_limits (
			bucket_key_hash TEXT PRIMARY KEY,
			bucket_type TEXT NOT NULL,
			window_started_at TEXT NOT NULL,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			blocked_until TEXT,
			expires_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_security_rate_limits_expiry ON security_rate_limits(expires_at)`,
	}
}

func ApplySchema(ctx context.Context, db *sql.DB, dialect appdb.Dialect) error {
	for _, statement := range SchemaStatements(dialect) {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize authentication schema: %w", err)
		}
	}
	return nil
}

func ValidateSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `SELECT 1 FROM users LIMIT 1`); err != nil {
		return fmt.Errorf("authentication schema unavailable; run `orchestra schema --create`: %w", err)
	}
	return nil
}
