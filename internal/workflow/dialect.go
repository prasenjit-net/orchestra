package workflow

import "strconv"

// Dialect represents the SQL database dialect in use.
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

// DriverName returns the database/sql driver name for this dialect.
func (d Dialect) DriverName() string {
	if d == DialectPostgres {
		return "pgx"
	}
	return "sqlite"
}

// IsPostgres reports whether the dialect is PostgreSQL.
func (d Dialect) IsPostgres() bool { return d == DialectPostgres }

// Rebind rewrites SQLite-style ? placeholders to $1, $2, … for PostgreSQL.
// For SQLite the query is returned unchanged.
func (d Dialect) Rebind(query string) string {
	if d != DialectPostgres {
		return query
	}
	out := make([]byte, 0, len(query)+10)
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			out = append(out, '$')
			out = strconv.AppendInt(out, int64(n), 10)
		} else {
			out = append(out, query[i])
		}
	}
	return string(out)
}

// DDL returns the full set of CREATE TABLE and CREATE INDEX statements for this
// dialect.  For PostgreSQL the output is the complete up-to-date schema
// (including every column that SQLite adds via ALTER TABLE migrations).
// For SQLite it matches the statements executed by initSchema.
func (d Dialect) DDL() []string {
	if d == DialectPostgres {
		return postgresDDL
	}
	return sqliteDDL
}

// sqliteDDL is identical to what initSchema executes (full, current schema).
var sqliteDDL = []string{
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
	callback_url TEXT NOT NULL DEFAULT '',
	trigger_source TEXT NOT NULL DEFAULT 'ui',
	callback_status TEXT NOT NULL DEFAULT '',
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
	executed_by TEXT NOT NULL DEFAULT '',
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
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	group_name TEXT NOT NULL DEFAULT '',
	url TEXT NOT NULL,
	headers_json TEXT NOT NULL DEFAULT '{}',
	enabled INTEGER NOT NULL DEFAULT 1,
	tools_json TEXT NOT NULL DEFAULT '[]',
	explored_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS agent_mcp_servers (
	agent_id TEXT NOT NULL,
	server_id TEXT NOT NULL,
	PRIMARY KEY (agent_id, server_id)
)`,
	`CREATE TABLE IF NOT EXISTS nodes (
	id TEXT PRIMARY KEY,
	role TEXT NOT NULL DEFAULT 'all',
	address TEXT NOT NULL DEFAULT '',
	capabilities TEXT NOT NULL DEFAULT '[]',
	max_concurrent INTEGER NOT NULL DEFAULT 0,
	version TEXT NOT NULL DEFAULT '',
	hostname TEXT NOT NULL DEFAULT '',
	last_seen_at TEXT NOT NULL,
	registered_at TEXT NOT NULL
)`,
}

// postgresDDL is the full up-to-date schema for PostgreSQL.
// Notable differences from the SQLite DDL:
//   - AUTOINCREMENT → BIGINT GENERATED ALWAYS AS IDENTITY
//   - REAL → DOUBLE PRECISION
//   - workflow_instances includes callback_url/trigger_source/callback_status inline
//   - No PRAGMA statements
var postgresDDL = []string{
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
	callback_url TEXT NOT NULL DEFAULT '',
	trigger_source TEXT NOT NULL DEFAULT 'ui',
	callback_status TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS workflow_events (
	id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	workflow_id TEXT NOT NULL,
	sequence INTEGER NOT NULL,
	event_type TEXT NOT NULL,
	payload TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE (workflow_id, sequence)
)`,
	`CREATE TABLE IF NOT EXISTS workflow_tasks (
	id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
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
	executed_by TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS workflow_signals (
	id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
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
	temperature DOUBLE PRECISION NOT NULL DEFAULT 0,
	tools_json TEXT NOT NULL DEFAULT '[]',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS mcp_servers (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	group_name TEXT NOT NULL DEFAULT '',
	url TEXT NOT NULL,
	headers_json TEXT NOT NULL DEFAULT '{}',
	enabled INTEGER NOT NULL DEFAULT 1,
	tools_json TEXT NOT NULL DEFAULT '[]',
	explored_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
)`,
	`CREATE TABLE IF NOT EXISTS agent_mcp_servers (
	agent_id TEXT NOT NULL,
	server_id TEXT NOT NULL,
	PRIMARY KEY (agent_id, server_id)
)`,
	`CREATE TABLE IF NOT EXISTS nodes (
	id TEXT PRIMARY KEY,
	role TEXT NOT NULL DEFAULT 'all',
	address TEXT NOT NULL DEFAULT '',
	capabilities TEXT NOT NULL DEFAULT '[]',
	max_concurrent INTEGER NOT NULL DEFAULT 0,
	version TEXT NOT NULL DEFAULT '',
	hostname TEXT NOT NULL DEFAULT '',
	last_seen_at TEXT NOT NULL,
	registered_at TEXT NOT NULL
)`,
}
