package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type MCPServer struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Group       string            `json:"group,omitempty"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers,omitempty"`
	Enabled     bool              `json:"enabled"`
	Tools       []MCPTool         `json:"tools,omitempty"`
	ExploredAt  *time.Time        `json:"exploredAt,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type CreateMCPServerInput struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Group       string            `json:"group"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers,omitempty"`
	Enabled     bool              `json:"enabled"`
}

type MCPServersResponse struct {
	Servers []MCPServer `json:"servers"`
}

type SetAgentMCPServersInput struct {
	ServerIDs []string `json:"serverIds"`
}

func (s *Service) CreateMCPServer(ctx context.Context, input CreateMCPServerInput) (MCPServer, error) {
	now := time.Now().UTC()
	id := generateID("mcp")
	headers := input.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return MCPServer{}, fmt.Errorf("encode headers: %w", err)
	}
	enabled := 0
	if input.Enabled {
		enabled = 1
	}
	ts := formatTime(now)
	if _, err := s.db.ExecContext(ctx, s.rebind(`
		INSERT INTO mcp_servers (id, name, description, group_name, url, headers_json, enabled, tools_json, explored_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '[]', NULL, ?, ?)
	`), id, input.Name, input.Description, input.Group, input.URL, string(headersJSON), enabled, ts, ts); err != nil {
		return MCPServer{}, fmt.Errorf("insert mcp server: %w", err)
	}
	srv := MCPServer{
		ID:          id,
		Name:        input.Name,
		Description: input.Description,
		Group:       input.Group,
		URL:         input.URL,
		Headers:     headers,
		Enabled:     input.Enabled,
		Tools:       []MCPTool{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.emitLiveEvent("mcp_server.updated", "mcp_server", id, srv)
	// Trigger background exploration so tools are discovered immediately.
	go s.exploreInBackground(id, input.URL, headers) // #nosec G118 -- intentional: exploration must outlive the request
	return srv, nil
}

func (s *Service) ListMCPServers(ctx context.Context) ([]MCPServer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, group_name, url, headers_json, enabled, tools_json, explored_at, created_at, updated_at
		FROM mcp_servers ORDER BY group_name, name
	`)
	if err != nil {
		return nil, fmt.Errorf("query mcp servers: %w", err)
	}
	defer rows.Close()

	var servers []MCPServer
	for rows.Next() {
		srv, err := scanMCPServer(rows)
		if err != nil {
			return nil, err
		}
		servers = append(servers, srv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mcp servers: %w", err)
	}
	if servers == nil {
		servers = []MCPServer{}
	}
	return servers, nil
}

func (s *Service) GetMCPServer(ctx context.Context, id string) (MCPServer, error) {
	row := s.db.QueryRowContext(ctx, s.rebind(`
		SELECT id, name, description, group_name, url, headers_json, enabled, tools_json, explored_at, created_at, updated_at
		FROM mcp_servers WHERE id = ?
	`), id)
	return scanMCPServer(row)
}

func (s *Service) UpdateMCPServer(ctx context.Context, id string, input CreateMCPServerInput) (MCPServer, error) {
	now := time.Now().UTC()
	headers := input.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return MCPServer{}, fmt.Errorf("encode headers: %w", err)
	}
	enabled := 0
	if input.Enabled {
		enabled = 1
	}
	ts := formatTime(now)

	// Capture old URL to decide whether to re-explore.
	old, oldErr := s.GetMCPServer(ctx, id)

	res, err := s.db.ExecContext(ctx, s.rebind(`
		UPDATE mcp_servers SET name=?, description=?, group_name=?, url=?, headers_json=?, enabled=?, updated_at=?
		WHERE id=?
	`), input.Name, input.Description, input.Group, input.URL, string(headersJSON), enabled, ts, id)
	if err != nil {
		return MCPServer{}, fmt.Errorf("update mcp server: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return MCPServer{}, ErrNotFound
	}
	srv, err := s.GetMCPServer(ctx, id)
	if err != nil {
		return MCPServer{}, err
	}
	s.emitLiveEvent("mcp_server.updated", "mcp_server", id, srv)
	// Re-explore if the URL or headers changed.
	if oldErr != nil || old.URL != input.URL || headersChanged(old.Headers, headers) {
		go s.exploreInBackground(id, input.URL, headers) // #nosec G118 -- intentional: exploration must outlive the request
	}
	return srv, nil
}

func (s *Service) DeleteMCPServer(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM agent_mcp_servers WHERE server_id = ?`), id); err != nil {
		return fmt.Errorf("delete agent_mcp_servers: %w", err)
	}
	res, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM mcp_servers WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("delete mcp server: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	s.emitLiveEvent("mcp_server.deleted", "mcp_server", id, nil)
	return nil
}

// ExploreMCPServer connects to the server, fetches its tool list, stores it, and returns the updated record.
func (s *Service) ExploreMCPServer(ctx context.Context, id string) (MCPServer, error) {
	srv, err := s.GetMCPServer(ctx, id)
	if err != nil {
		return MCPServer{}, err
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	sess, tools, err := ConnectMCPServer(ctx, httpClient, srv.URL, srv.Headers)
	if err != nil {
		return MCPServer{}, fmt.Errorf("explore mcp server: %w", err)
	}
	sess.Close()

	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return MCPServer{}, fmt.Errorf("encode tools: %w", err)
	}
	now := time.Now().UTC()
	ts := formatTime(now)
	if _, err := s.db.ExecContext(ctx, s.rebind(`
		UPDATE mcp_servers SET tools_json=?, explored_at=?, updated_at=? WHERE id=?
	`), string(toolsJSON), ts, ts, id); err != nil {
		return MCPServer{}, fmt.Errorf("save explored tools: %w", err)
	}

	updated, err := s.GetMCPServer(ctx, id)
	if err != nil {
		return MCPServer{}, err
	}
	s.emitLiveEvent("mcp_server.updated", "mcp_server", id, updated)
	return updated, nil
}

func (s *Service) exploreInBackground(id, url string, headers map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.ExploreMCPServer(ctx, id); err != nil {
		s.logger.Warn("background mcp exploration failed", "server_id", id, "url", url, "error", err)
	}
}

func (s *Service) GetAgentMCPServers(ctx context.Context, agentID string) ([]MCPServer, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`
		SELECT ms.id, ms.name, ms.description, ms.group_name, ms.url, ms.headers_json, ms.enabled, ms.tools_json, ms.explored_at, ms.created_at, ms.updated_at
		FROM mcp_servers ms
		JOIN agent_mcp_servers ams ON ams.server_id = ms.id
		WHERE ams.agent_id = ?
		ORDER BY ms.group_name, ms.name
	`), agentID)
	if err != nil {
		return nil, fmt.Errorf("query agent mcp servers: %w", err)
	}
	defer rows.Close()

	var servers []MCPServer
	for rows.Next() {
		srv, err := scanMCPServer(rows)
		if err != nil {
			return nil, err
		}
		servers = append(servers, srv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent mcp servers: %w", err)
	}
	if servers == nil {
		servers = []MCPServer{}
	}
	return servers, nil
}

func (s *Service) SetAgentMCPServers(ctx context.Context, agentID string, serverIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM agent_mcp_servers WHERE agent_id = ?`), agentID); err != nil {
		return fmt.Errorf("clear agent mcp servers: %w", err)
	}
	for _, sid := range serverIDs {
		if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO agent_mcp_servers (agent_id, server_id) VALUES (?, ?)`), agentID, sid); err != nil {
			return fmt.Errorf("insert agent mcp server %s: %w", sid, err)
		}
	}
	return tx.Commit()
}

func headersChanged(a, b map[string]string) bool {
	if len(a) != len(b) {
		return true
	}
	for k, v := range a {
		if b[k] != v {
			return true
		}
	}
	return false
}

type mcpServerScanner interface {
	Scan(dest ...any) error
}

func scanMCPServer(row mcpServerScanner) (MCPServer, error) {
	var srv MCPServer
	var headersJSON, toolsJSON string
	var enabledInt int
	var createdAt, updatedAt string
	var exploredAt sql.NullString
	if err := row.Scan(&srv.ID, &srv.Name, &srv.Description, &srv.Group, &srv.URL,
		&headersJSON, &enabledInt, &toolsJSON, &exploredAt, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MCPServer{}, ErrNotFound
		}
		return MCPServer{}, fmt.Errorf("scan mcp server: %w", err)
	}
	if err := json.Unmarshal([]byte(headersJSON), &srv.Headers); err != nil {
		srv.Headers = map[string]string{}
	}
	if err := json.Unmarshal([]byte(toolsJSON), &srv.Tools); err != nil || srv.Tools == nil {
		srv.Tools = []MCPTool{}
	}
	srv.Enabled = enabledInt == 1
	if exploredAt.Valid && exploredAt.String != "" {
		t := mustParseTime(exploredAt.String)
		srv.ExploredAt = &t
	}
	srv.CreatedAt = mustParseTime(createdAt)
	srv.UpdatedAt = mustParseTime(updatedAt)
	return srv, nil
}
