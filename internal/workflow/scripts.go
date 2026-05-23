package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Script struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Language    string    `json:"language"`
	Source      string    `json:"source"`
	TimeoutMs   int       `json:"timeoutMs,omitempty"`
	Exports     []string  `json:"exports,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type CreateScriptInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Language    string   `json:"language"`
	Source      string   `json:"source"`
	TimeoutMs   int      `json:"timeoutMs,omitempty"`
	Exports     []string `json:"exports,omitempty"`
}

type ScriptsResponse struct {
	Scripts []Script `json:"scripts"`
}

func (s *Service) CreateScript(ctx context.Context, input CreateScriptInput) (Script, error) {
	now := time.Now().UTC()
	id := generateID("scr")
	lang := input.Language
	if lang == "" {
		lang = "starlark"
	}
	exports := input.Exports
	if exports == nil {
		exports = []string{}
	}
	exportsJSON, err := json.Marshal(exports)
	if err != nil {
		return Script{}, fmt.Errorf("encode exports: %w", err)
	}
	ts := formatTime(now)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO scripts (id, name, description, language, source, timeout_ms, exports_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, input.Name, input.Description, lang, input.Source, input.TimeoutMs, string(exportsJSON), ts, ts); err != nil {
		return Script{}, fmt.Errorf("insert script: %w", err)
	}
	script := Script{
		ID:          id,
		Name:        input.Name,
		Description: input.Description,
		Language:    lang,
		Source:      input.Source,
		TimeoutMs:   input.TimeoutMs,
		Exports:     exports,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.emitLiveEvent("script.updated", "script", id, script)
	return script, nil
}

func (s *Service) ListScripts(ctx context.Context) ([]Script, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, language, source, timeout_ms, exports_json, created_at, updated_at
		FROM scripts ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query scripts: %w", err)
	}
	defer rows.Close()

	var scripts []Script
	for rows.Next() {
		sc, err := scanScript(rows)
		if err != nil {
			return nil, err
		}
		scripts = append(scripts, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scripts: %w", err)
	}
	if scripts == nil {
		scripts = []Script{}
	}
	return scripts, nil
}

func (s *Service) GetScript(ctx context.Context, id string) (Script, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, language, source, timeout_ms, exports_json, created_at, updated_at
		FROM scripts WHERE id = ?
	`, id)
	sc, err := scanScript(row)
	if err != nil {
		return Script{}, err
	}
	return sc, nil
}

func (s *Service) UpdateScript(ctx context.Context, id string, input CreateScriptInput) (Script, error) {
	now := time.Now().UTC()
	lang := input.Language
	if lang == "" {
		lang = "starlark"
	}
	exports := input.Exports
	if exports == nil {
		exports = []string{}
	}
	exportsJSON, err := json.Marshal(exports)
	if err != nil {
		return Script{}, fmt.Errorf("encode exports: %w", err)
	}
	ts := formatTime(now)
	res, err := s.db.ExecContext(ctx, `
		UPDATE scripts SET name=?, description=?, language=?, source=?, timeout_ms=?, exports_json=?, updated_at=?
		WHERE id=?
	`, input.Name, input.Description, lang, input.Source, input.TimeoutMs, string(exportsJSON), ts, id)
	if err != nil {
		return Script{}, fmt.Errorf("update script: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Script{}, ErrNotFound
	}
	sc, err := s.GetScript(ctx, id)
	if err != nil {
		return Script{}, err
	}
	s.emitLiveEvent("script.updated", "script", id, sc)
	return sc, nil
}

func (s *Service) DeleteScript(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM scripts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete script: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	s.emitLiveEvent("script.deleted", "script", id, nil)
	return nil
}

func (s *Service) lookupScriptSource(ctx context.Context, id string) (string, error) {
	sc, err := s.GetScript(ctx, id)
	if err != nil {
		return "", err
	}
	return sc.Source, nil
}

type scriptScanner interface {
	Scan(dest ...any) error
}

func scanScript(row scriptScanner) (Script, error) {
	var sc Script
	var exportsJSON string
	var createdAt, updatedAt string
	if err := row.Scan(&sc.ID, &sc.Name, &sc.Description, &sc.Language, &sc.Source,
		&sc.TimeoutMs, &exportsJSON, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Script{}, ErrNotFound
		}
		return Script{}, fmt.Errorf("scan script: %w", err)
	}
	if err := json.Unmarshal([]byte(exportsJSON), &sc.Exports); err != nil {
		sc.Exports = []string{}
	}
	sc.CreatedAt = mustParseTime(createdAt)
	sc.UpdatedAt = mustParseTime(updatedAt)
	return sc, nil
}
