package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type CreateAPIKeyInput struct {
	ID              string
	Name            string
	Description     string
	KeyPrefix       string
	SecretHash      string
	CreatedByUserID string
	ExpiresAt       *time.Time
	RotatedFromID   string
	Grants          []APIKeyGrant
	Now             time.Time
}

func (s *Store) CreateAPIKey(ctx context.Context, input CreateAPIKeyInput) (APIKey, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APIKey{}, fmt.Errorf("begin api key create: %w", err)
	}
	defer tx.Rollback()
	if err := s.insertAPIKeyTx(ctx, tx, input); err != nil {
		return APIKey{}, err
	}
	if err := tx.Commit(); err != nil {
		return APIKey{}, fmt.Errorf("commit api key create: %w", err)
	}
	return s.APIKeyByID(ctx, input.ID)
}

func (s *Store) insertAPIKeyTx(ctx context.Context, tx *sql.Tx, input CreateAPIKeyInput) error {
	var expires any
	if input.ExpiresAt != nil {
		expires = formatTime(*input.ExpiresAt)
	}
	now := formatTime(input.Now)
	_, err := s.txExec(ctx, tx, `INSERT INTO api_keys (
		id, name, description, key_prefix, secret_hash, created_by_user_id, status,
		expires_at, created_at, updated_at, rotated_from_id
	) VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)`,
		input.ID, input.Name, input.Description, input.KeyPrefix, input.SecretHash,
		input.CreatedByUserID, expires, now, now, nullableString(input.RotatedFromID),
	)
	if err != nil {
		return fmt.Errorf("insert api key: %w", err)
	}
	if err := s.insertAPIKeyGrantsTx(ctx, tx, input.ID, input.Grants, input.Now); err != nil {
		return err
	}
	return nil
}

func (s *Store) insertAPIKeyGrantsTx(ctx context.Context, tx *sql.Tx, keyID string, grants []APIKeyGrant, now time.Time) error {
	createdAt := formatTime(now)
	for _, grant := range grants {
		var signalNames any
		if grant.SignalNames != nil {
			encoded, err := json.Marshal(grant.SignalNames)
			if err != nil {
				return fmt.Errorf("encode api key signal names: %w", err)
			}
			signalNames = string(encoded)
		}
		pinned, callback := 0, 0
		if grant.AllowPinnedVersions {
			pinned = 1
		}
		if grant.AllowCallbackURL {
			callback = 1
		}
		_, err := s.txExec(ctx, tx, `INSERT INTO api_key_workflow_grants (
			api_key_id, workflow_definition_id, action, instance_scope,
			allow_pinned_versions, allow_callback_url, signal_names_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			keyID, grant.WorkflowDefinitionID, grant.Action, grant.InstanceScope,
			pinned, callback, signalNames, createdAt,
		)
		if err != nil {
			return fmt.Errorf("insert api key grant: %w", err)
		}
	}
	return nil
}

const apiKeyColumns = `id, name, description, key_prefix, secret_hash, created_by_user_id,
	status, expires_at, last_used_at, last_used_ip, created_at, updated_at, revoked_at,
	revoked_by, rotated_from_id`

func scanAPIKey(row scanner) (apiKeyRecord, error) {
	var record apiKeyRecord
	var expires, lastUsed, revoked sql.NullString
	var revokedBy, rotatedFrom sql.NullString
	var created, updated string
	if err := row.Scan(
		&record.ID, &record.Name, &record.Description, &record.KeyPrefix, &record.SecretHash,
		&record.CreatedByUserID, &record.Status, &expires, &lastUsed, &record.LastUsedIP,
		&created, &updated, &revoked, &revokedBy, &rotatedFrom,
	); err != nil {
		return apiKeyRecord{}, err
	}
	var err error
	if record.ExpiresAt, err = parseOptionalTime(expires); err != nil {
		return apiKeyRecord{}, err
	}
	if record.LastUsedAt, err = parseOptionalTime(lastUsed); err != nil {
		return apiKeyRecord{}, err
	}
	if record.RevokedAt, err = parseOptionalTime(revoked); err != nil {
		return apiKeyRecord{}, err
	}
	record.RevokedBy = revokedBy.String
	record.RotatedFromID = rotatedFrom.String
	if record.CreatedAt, err = parseTime(created); err != nil {
		return apiKeyRecord{}, err
	}
	if record.UpdatedAt, err = parseTime(updated); err != nil {
		return apiKeyRecord{}, err
	}
	return record, nil
}

func (s *Store) apiKeyRecordByPrefix(ctx context.Context, prefix string) (apiKeyRecord, error) {
	record, err := scanAPIKey(s.queryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_keys WHERE key_prefix = ?`, prefix))
	if errors.Is(err, sql.ErrNoRows) {
		return apiKeyRecord{}, ErrNotFound
	}
	if err != nil {
		return apiKeyRecord{}, fmt.Errorf("get api key by prefix: %w", err)
	}
	record.Grants, err = s.ListAPIKeyGrants(ctx, record.ID)
	if err != nil {
		return apiKeyRecord{}, err
	}
	return record, nil
}

func (s *Store) apiKeyRecordByID(ctx context.Context, id string) (apiKeyRecord, error) {
	record, err := scanAPIKey(s.queryRow(ctx, `SELECT `+apiKeyColumns+` FROM api_keys WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return apiKeyRecord{}, ErrNotFound
	}
	if err != nil {
		return apiKeyRecord{}, fmt.Errorf("get api key: %w", err)
	}
	record.Grants, err = s.ListAPIKeyGrants(ctx, record.ID)
	if err != nil {
		return apiKeyRecord{}, err
	}
	return record, nil
}

func (s *Store) APIKeyByID(ctx context.Context, id string) (APIKey, error) {
	record, err := s.apiKeyRecordByID(ctx, id)
	return record.APIKey, err
}

func (s *Store) ListAPIKeys(ctx context.Context, ownerID string, includeAll bool, limit, offset int) ([]APIKey, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	var rows *sql.Rows
	var err error
	if includeAll {
		err = s.queryRow(ctx, `SELECT COUNT(*) FROM api_keys`).Scan(&total)
		if err == nil {
			rows, err = s.query(ctx, `SELECT `+apiKeyColumns+` FROM api_keys ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
		}
	} else {
		err = s.queryRow(ctx, `SELECT COUNT(*) FROM api_keys WHERE created_by_user_id = ?`, ownerID).Scan(&total)
		if err == nil {
			rows, err = s.query(ctx, `SELECT `+apiKeyColumns+` FROM api_keys WHERE created_by_user_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`, ownerID, limit, offset)
		}
	}
	if err != nil {
		return nil, 0, fmt.Errorf("count api keys: %w", err)
	}
	result := make([]APIKey, 0)
	for rows.Next() {
		record, err := scanAPIKey(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		result = append(result, record.APIKey)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()
	for index := range result {
		result[index].Grants, err = s.ListAPIKeyGrants(ctx, result[index].ID)
		if err != nil {
			return nil, 0, err
		}
	}
	return result, total, nil
}

func (s *Store) ListAPIKeyGrants(ctx context.Context, keyID string) ([]APIKeyGrant, error) {
	rows, err := s.query(ctx, `SELECT workflow_definition_id, action, instance_scope, allow_pinned_versions, allow_callback_url, signal_names_json, created_at FROM api_key_workflow_grants WHERE api_key_id = ? ORDER BY workflow_definition_id, action`, keyID)
	if err != nil {
		return nil, fmt.Errorf("list api key grants: %w", err)
	}
	defer rows.Close()
	result := make([]APIKeyGrant, 0)
	for rows.Next() {
		var grant APIKeyGrant
		var pinned, callback int
		var signalNames sql.NullString
		var created string
		if err := rows.Scan(&grant.WorkflowDefinitionID, &grant.Action, &grant.InstanceScope, &pinned, &callback, &signalNames, &created); err != nil {
			return nil, err
		}
		grant.AllowPinnedVersions = pinned != 0
		grant.AllowCallbackURL = callback != 0
		if signalNames.Valid {
			if err := json.Unmarshal([]byte(signalNames.String), &grant.SignalNames); err != nil {
				return nil, fmt.Errorf("decode api key signal names: %w", err)
			}
		}
		grant.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		result = append(result, grant)
	}
	return result, rows.Err()
}

func (s *Store) UpdateAPIKey(ctx context.Context, id, name, description string, expiresAt *time.Time, grants []APIKeyGrant, now time.Time) (APIKey, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APIKey{}, err
	}
	defer tx.Rollback()
	var expires any
	if expiresAt != nil {
		expires = formatTime(*expiresAt)
	}
	result, err := s.txExec(ctx, tx, `UPDATE api_keys SET name = ?, description = ?, expires_at = ?, updated_at = ? WHERE id = ? AND status = 'active'`,
		name, description, expires, formatTime(now), id)
	if err != nil {
		return APIKey{}, fmt.Errorf("update api key: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return APIKey{}, ErrNotFound
	}
	if _, err := s.txExec(ctx, tx, `DELETE FROM api_key_workflow_grants WHERE api_key_id = ?`, id); err != nil {
		return APIKey{}, err
	}
	if err := s.insertAPIKeyGrantsTx(ctx, tx, id, grants, now); err != nil {
		return APIKey{}, err
	}
	if err := tx.Commit(); err != nil {
		return APIKey{}, err
	}
	return s.APIKeyByID(ctx, id)
}

func (s *Store) RevokeAPIKey(ctx context.Context, id, actorID string, now time.Time) error {
	result, err := s.exec(ctx, `UPDATE api_keys SET status = 'revoked', revoked_at = ?, revoked_by = ?, updated_at = ? WHERE id = ? AND status = 'active'`,
		formatTime(now), actorID, formatTime(now), id)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RotateAPIKey(ctx context.Context, oldID, actorID string, input CreateAPIKeyInput) (APIKey, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return APIKey{}, err
	}
	defer tx.Rollback()
	result, err := s.txExec(ctx, tx, `UPDATE api_keys SET status = 'revoked', revoked_at = ?, revoked_by = ?, updated_at = ? WHERE id = ? AND status = 'active'`,
		formatTime(input.Now), actorID, formatTime(input.Now), oldID)
	if err != nil {
		return APIKey{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return APIKey{}, ErrNotFound
	}
	if err := s.insertAPIKeyTx(ctx, tx, input); err != nil {
		return APIKey{}, err
	}
	if err := tx.Commit(); err != nil {
		return APIKey{}, err
	}
	return s.APIKeyByID(ctx, input.ID)
}

func (s *Store) TouchAPIKey(ctx context.Context, id, sourceIP string, now time.Time) error {
	_, err := s.exec(ctx, `UPDATE api_keys SET last_used_at = ?, last_used_ip = ? WHERE id = ?`, formatTime(now), sourceIP, id)
	return err
}

func findGrant(grants []APIKeyGrant, definitionID, action string) (APIKeyGrant, bool) {
	for _, grant := range grants {
		if grant.WorkflowDefinitionID == definitionID && grant.Action == action {
			return grant, true
		}
	}
	return APIKeyGrant{}, false
}

func includesSignal(grant APIKeyGrant, name string) bool {
	if grant.SignalNames == nil {
		return true
	}
	for _, allowed := range grant.SignalNames {
		if strings.EqualFold(allowed, name) {
			return true
		}
	}
	return false
}
