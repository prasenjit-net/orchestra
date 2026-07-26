package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type CreateSessionInput struct {
	ID                     string
	TokenHash              string
	CSRFToken              string
	UserID                 string
	CreatedAt              time.Time
	IdleExpiresAt          time.Time
	AbsoluteExpiresAt      time.Time
	PasswordChangedAtLogin time.Time
	AuthzVersionAtLogin    int64
	SourceIP               string
	UserAgentHash          string
}

func (s *Store) CreateSession(ctx context.Context, input CreateSessionInput) (Session, error) {
	created := formatTime(input.CreatedAt)
	_, err := s.exec(ctx, `INSERT INTO sessions (
		id, token_hash, csrf_token, user_id, created_at, last_seen_at, idle_expires_at,
		absolute_expires_at, password_changed_at_login, authz_version_at_login,
		source_ip, user_agent_hash
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.ID, input.TokenHash, input.CSRFToken, input.UserID, created, created,
		formatTime(input.IdleExpiresAt), formatTime(input.AbsoluteExpiresAt),
		formatTime(input.PasswordChangedAtLogin), input.AuthzVersionAtLogin,
		input.SourceIP, input.UserAgentHash,
	)
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	return s.SessionByID(ctx, input.ID)
}

const sessionColumns = `id, user_id, csrf_token, created_at, last_seen_at, idle_expires_at,
	absolute_expires_at, password_changed_at_login, authz_version_at_login, revoked_at,
	revoke_reason, source_ip, user_agent_hash`

func scanSession(row scanner) (Session, error) {
	var session Session
	var created, lastSeen, idleExpiry, absoluteExpiry, passwordChanged string
	var revoked sql.NullString
	if err := row.Scan(
		&session.ID, &session.UserID, &session.CSRFToken, &created, &lastSeen,
		&idleExpiry, &absoluteExpiry, &passwordChanged, &session.AuthzVersionAtLogin,
		&revoked, &session.RevokeReason, &session.SourceIP, &session.UserAgentHash,
	); err != nil {
		return Session{}, err
	}
	var err error
	if session.CreatedAt, err = parseTime(created); err != nil {
		return Session{}, err
	}
	if session.LastSeenAt, err = parseTime(lastSeen); err != nil {
		return Session{}, err
	}
	if session.IdleExpiresAt, err = parseTime(idleExpiry); err != nil {
		return Session{}, err
	}
	if session.AbsoluteExpiresAt, err = parseTime(absoluteExpiry); err != nil {
		return Session{}, err
	}
	if session.PasswordChangedAtLogin, err = parseTime(passwordChanged); err != nil {
		return Session{}, err
	}
	if session.RevokedAt, err = parseOptionalTime(revoked); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash string) (Session, error) {
	session, err := scanSession(s.queryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE token_hash = ?`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("get session by token: %w", err)
	}
	return session, nil
}

func (s *Store) SessionByID(ctx context.Context, id string) (Session, error) {
	session, err := scanSession(s.queryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("get session: %w", err)
	}
	return session, nil
}

func (s *Store) TouchSession(ctx context.Context, id string, lastSeen, idleExpires time.Time) error {
	_, err := s.exec(ctx, `UPDATE sessions SET last_seen_at = ?, idle_expires_at = ? WHERE id = ? AND revoked_at IS NULL`, formatTime(lastSeen), formatTime(idleExpires), id)
	return err
}

func (s *Store) RevokeSession(ctx context.Context, id, reason string, now time.Time) error {
	result, err := s.exec(ctx, `UPDATE sessions SET revoked_at = ?, revoke_reason = ? WHERE id = ? AND revoked_at IS NULL`, formatTime(now), reason, id)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RevokeUserSessions(ctx context.Context, userID, reason, exceptID string, now time.Time) error {
	query := `UPDATE sessions SET revoked_at = ?, revoke_reason = ? WHERE user_id = ? AND revoked_at IS NULL`
	args := []any{formatTime(now), reason, userID}
	if exceptID != "" {
		query += ` AND id <> ?`
		args = append(args, exceptID)
	}
	_, err := s.exec(ctx, query, args...)
	return err
}

func (s *Store) ListUserSessions(ctx context.Context, userID string, now time.Time) ([]Session, error) {
	formattedNow := formatTime(now)
	rows, err := s.query(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE user_id = ? AND revoked_at IS NULL AND absolute_expires_at > ? AND idle_expires_at > ? ORDER BY created_at DESC`, userID, formattedNow, formattedNow)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	result := make([]Session, 0)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, rows.Err()
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, before time.Time) error {
	_, err := s.exec(ctx, `DELETE FROM sessions WHERE absolute_expires_at < ? OR idle_expires_at < ? OR (revoked_at IS NOT NULL AND revoked_at < ?)`, formatTime(before), formatTime(before), formatTime(before.Add(-24*time.Hour)))
	return err
}
