package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Store) AppendAuditEvent(ctx context.Context, event AuditEvent) error {
	metadata := event.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(metadata) {
		return fmt.Errorf("audit metadata must be valid JSON")
	}
	if len(metadata) > 16*1024 {
		return fmt.Errorf("audit metadata exceeds 16 KiB")
	}
	userAgent := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, event.UserAgent)
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	_, err := s.exec(ctx, `INSERT INTO security_audit_events (
		occurred_at, request_id, actor_type, actor_id, action, resource_type,
		resource_id, outcome, source_ip, user_agent, metadata_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(event.OccurredAt), event.RequestID, string(event.ActorType), event.ActorID,
		event.Action, event.ResourceType, event.ResourceID, event.Outcome, event.SourceIP,
		userAgent, string(metadata),
	)
	if err != nil {
		return fmt.Errorf("append security audit event: %w", err)
	}
	return nil
}

type ListAuditInput struct {
	Limit   int
	Offset  int
	ActorID string
	Action  string
	Outcome string
}

func (s *Store) ListAuditEvents(ctx context.Context, input ListAuditInput) ([]AuditEvent, int, error) {
	if input.Limit <= 0 || input.Limit > 200 {
		input.Limit = 50
	}
	if input.Offset < 0 {
		input.Offset = 0
	}
	conditions := make([]string, 0, 3)
	args := make([]any, 0, 5)
	if input.ActorID != "" {
		conditions = append(conditions, "actor_id = ?")
		args = append(args, input.ActorID)
	}
	if input.Action != "" {
		conditions = append(conditions, "action = ?")
		args = append(args, input.Action)
	}
	if input.Outcome != "" {
		conditions = append(conditions, "outcome = ?")
		args = append(args, input.Outcome)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM security_audit_events`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, input.Limit, input.Offset)
	rows, err := s.query(ctx, `SELECT id, occurred_at, request_id, actor_type, actor_id, action, resource_type, resource_id, outcome, source_ip, user_agent, metadata_json FROM security_audit_events`+where+` ORDER BY occurred_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	result := make([]AuditEvent, 0)
	for rows.Next() {
		var event AuditEvent
		var occurred, actorType, metadata string
		if err := rows.Scan(&event.ID, &occurred, &event.RequestID, &actorType, &event.ActorID, &event.Action, &event.ResourceType, &event.ResourceID, &event.Outcome, &event.SourceIP, &event.UserAgent, &metadata); err != nil {
			return nil, 0, err
		}
		event.ActorType = PrincipalType(actorType)
		event.Metadata = json.RawMessage(metadata)
		event.OccurredAt, err = parseTime(occurred)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, event)
	}
	return result, total, rows.Err()
}

func (s *Store) DeleteAuditEventsBefore(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.exec(ctx, `DELETE FROM security_audit_events WHERE occurred_at < ?`, formatTime(before))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) DeleteExpiredRateLimits(ctx context.Context, before time.Time) error {
	_, err := s.exec(ctx, `DELETE FROM security_rate_limits WHERE expires_at < ?`, formatTime(before))
	return err
}

func (s *Store) ConsumeRateLimit(ctx context.Context, bucketHash, bucketType string, now time.Time, window time.Duration, limit int) (bool, time.Time, error) {
	if limit <= 0 {
		return true, time.Time{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, time.Time{}, err
	}
	defer tx.Rollback()
	query := `SELECT window_started_at, attempt_count, blocked_until FROM security_rate_limits WHERE bucket_key_hash = ?`
	if s.dialect.IsPostgres() {
		query += ` FOR UPDATE`
	}
	var windowStartedRaw string
	var count int
	var blockedRaw sql.NullString
	err = tx.QueryRowContext(ctx, s.rebind(query), bucketHash).Scan(&windowStartedRaw, &count, &blockedRaw)
	if errorsIsNoRows(err) {
		expires := now.Add(2 * window)
		_, err = tx.ExecContext(ctx, s.rebind(`INSERT INTO security_rate_limits (bucket_key_hash, bucket_type, window_started_at, attempt_count, expires_at) VALUES (?, ?, ?, 1, ?)`), bucketHash, bucketType, formatTime(now), formatTime(expires))
		if err != nil {
			return false, time.Time{}, err
		}
		return true, time.Time{}, tx.Commit()
	}
	if err != nil {
		return false, time.Time{}, err
	}
	blockedUntil, err := parseOptionalTime(blockedRaw)
	if err != nil {
		return false, time.Time{}, err
	}
	if blockedUntil != nil && now.Before(*blockedUntil) {
		return false, *blockedUntil, tx.Commit()
	}
	windowStarted, err := parseTime(windowStartedRaw)
	if err != nil {
		return false, time.Time{}, err
	}
	if now.Sub(windowStarted) >= window {
		_, err = tx.ExecContext(ctx, s.rebind(`UPDATE security_rate_limits SET bucket_type = ?, window_started_at = ?, attempt_count = 1, blocked_until = NULL, expires_at = ? WHERE bucket_key_hash = ?`), bucketType, formatTime(now), formatTime(now.Add(2*window)), bucketHash)
		if err != nil {
			return false, time.Time{}, err
		}
		return true, time.Time{}, tx.Commit()
	}
	count++
	var block any
	retryAt := windowStarted.Add(window)
	if count > limit {
		block = formatTime(retryAt)
	}
	_, err = tx.ExecContext(ctx, s.rebind(`UPDATE security_rate_limits SET attempt_count = ?, blocked_until = ?, expires_at = ? WHERE bucket_key_hash = ?`), count, block, formatTime(now.Add(2*window)), bucketHash)
	if err != nil {
		return false, time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return false, time.Time{}, err
	}
	return count <= limit, retryAt, nil
}

func errorsIsNoRows(err error) bool { return err == sql.ErrNoRows }
