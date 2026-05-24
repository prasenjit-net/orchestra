package workflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Agent struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Model        string    `json:"model"`
	SystemPrompt string    `json:"systemPrompt"`
	MaxTokens    int       `json:"maxTokens,omitempty"`
	Temperature  float64   `json:"temperature,omitempty"`
	MCPServerIDs []string  `json:"mcpServerIds,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type CreateAgentInput struct {
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	Model        string  `json:"model"`
	SystemPrompt string  `json:"systemPrompt"`
	MaxTokens    int     `json:"maxTokens,omitempty"`
	Temperature  float64 `json:"temperature,omitempty"`
}

type AgentsResponse struct {
	Agents []Agent `json:"agents"`
}

func (s *Service) CreateAgent(ctx context.Context, input CreateAgentInput) (Agent, error) {
	now := time.Now().UTC()
	id := generateID("agt")
	model := input.Model
	if model == "" {
		model = "gpt-4o"
	}
	ts := formatTime(now)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agents (id, name, description, model, system_prompt, max_tokens, temperature, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, input.Name, input.Description, model, input.SystemPrompt, input.MaxTokens, input.Temperature, ts, ts); err != nil {
		return Agent{}, fmt.Errorf("insert agent: %w", err)
	}
	agent := Agent{
		ID:           id,
		Name:         input.Name,
		Description:  input.Description,
		Model:        model,
		SystemPrompt: input.SystemPrompt,
		MaxTokens:    input.MaxTokens,
		Temperature:  input.Temperature,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	s.emitLiveEvent("agent.updated", "agent", id, agent)
	return agent, nil
}

func (s *Service) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, model, system_prompt, max_tokens, temperature, created_at, updated_at
		FROM agents ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query agents: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		ag, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, ag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agents: %w", err)
	}
	if agents == nil {
		agents = []Agent{}
	}
	return agents, nil
}

func (s *Service) GetAgent(ctx context.Context, id string) (Agent, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, model, system_prompt, max_tokens, temperature, created_at, updated_at
		FROM agents WHERE id = ?
	`, id)
	ag, err := scanAgent(row)
	if err != nil {
		return Agent{}, err
	}
	mcpServers, err := s.GetAgentMCPServers(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	for _, srv := range mcpServers {
		ag.MCPServerIDs = append(ag.MCPServerIDs, srv.ID)
	}
	return ag, nil
}

func (s *Service) UpdateAgent(ctx context.Context, id string, input CreateAgentInput) (Agent, error) {
	now := time.Now().UTC()
	model := input.Model
	if model == "" {
		model = "gpt-4o"
	}
	ts := formatTime(now)
	res, err := s.db.ExecContext(ctx, `
		UPDATE agents SET name=?, description=?, model=?, system_prompt=?, max_tokens=?, temperature=?, updated_at=?
		WHERE id=?
	`, input.Name, input.Description, model, input.SystemPrompt, input.MaxTokens, input.Temperature, ts, id)
	if err != nil {
		return Agent{}, fmt.Errorf("update agent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Agent{}, ErrNotFound
	}
	ag, err := s.GetAgent(ctx, id)
	if err != nil {
		return Agent{}, err
	}
	s.emitLiveEvent("agent.updated", "agent", id, ag)
	return ag, nil
}

func (s *Service) DeleteAgent(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	s.emitLiveEvent("agent.deleted", "agent", id, nil)
	return nil
}

func (s *Service) lookupAgent(ctx context.Context, id string) (Agent, error) {
	return s.GetAgent(ctx, id)
}

type agentScanner interface {
	Scan(dest ...any) error
}

func scanAgent(row agentScanner) (Agent, error) {
	var ag Agent
	var createdAt, updatedAt string
	if err := row.Scan(&ag.ID, &ag.Name, &ag.Description, &ag.Model, &ag.SystemPrompt,
		&ag.MaxTokens, &ag.Temperature, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Agent{}, ErrNotFound
		}
		return Agent{}, fmt.Errorf("scan agent: %w", err)
	}
	ag.CreatedAt = mustParseTime(createdAt)
	ag.UpdatedAt = mustParseTime(updatedAt)
	return ag, nil
}
