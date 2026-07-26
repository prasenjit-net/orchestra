package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	appdb "github.com/prasenjit-net/orchestra/internal/database"
)

type Store struct {
	db      *sql.DB
	dialect appdb.Dialect
}

func NewStore(db *sql.DB, dialect appdb.Dialect) *Store {
	return &Store{db: db, dialect: dialect}
}

func (s *Store) rebind(query string) string { return s.dialect.Rebind(query) }

func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.rebind(query), args...)
}

func (s *Store) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.rebind(query), args...)
}

func (s *Store) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.rebind(query), args...)
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) {
	result, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp: %w", err)
	}
	return result, nil
}

func parseOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid || value.String == "" {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

type CreateUserInput struct {
	ID                 string
	Username           string
	UsernameNormalized string
	DisplayName        string
	PasswordHash       string
	Role               Role
	Status             string
	MustChangePassword bool
	CreatedBy          string
	Now                time.Time
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var count int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

func (s *Store) CreateUser(ctx context.Context, input CreateUserInput) (User, error) {
	status := input.Status
	if status == "" {
		status = "active"
	}
	mustChange := 0
	if input.MustChangePassword {
		mustChange = 1
	}
	now := formatTime(input.Now)
	_, err := s.exec(ctx, `INSERT INTO users (
		id, username, username_normalized, display_name, password_hash, role, status,
		must_change_password, failed_login_count, password_changed_at, authz_version,
		created_by, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 1, ?, ?, ?)`,
		input.ID, input.Username, input.UsernameNormalized, input.DisplayName, input.PasswordHash,
		string(input.Role), status, mustChange, now, nullableString(input.CreatedBy), now, now,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, fmt.Errorf("%w: username already exists", ErrConflict)
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return s.UserByID(ctx, input.ID)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}

const userColumns = `id, username, username_normalized, display_name, password_hash, role, status,
	must_change_password, failed_login_count, locked_until, password_changed_at, last_login_at,
	authz_version, created_by, created_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (userRecord, error) {
	var (
		record                            userRecord
		role                              string
		mustChange                        int
		lockedUntil, lastLogin            sql.NullString
		createdBy                         sql.NullString
		passwordChanged, created, updated string
	)
	if err := row.Scan(
		&record.ID, &record.Username, &record.UsernameNormalized, &record.DisplayName,
		&record.PasswordHash, &role, &record.Status, &mustChange, &record.FailedLoginCount,
		&lockedUntil, &passwordChanged, &lastLogin, &record.AuthzVersion, &createdBy,
		&created, &updated,
	); err != nil {
		return userRecord{}, err
	}
	record.Role = Role(role)
	record.MustChangePassword = mustChange != 0
	record.CreatedBy = createdBy.String
	var err error
	if record.LockedUntil, err = parseOptionalTime(lockedUntil); err != nil {
		return userRecord{}, err
	}
	if record.LastLoginAt, err = parseOptionalTime(lastLogin); err != nil {
		return userRecord{}, err
	}
	if record.PasswordChangedAt, err = parseTime(passwordChanged); err != nil {
		return userRecord{}, err
	}
	if record.CreatedAt, err = parseTime(created); err != nil {
		return userRecord{}, err
	}
	if record.UpdatedAt, err = parseTime(updated); err != nil {
		return userRecord{}, err
	}
	return record, nil
}

func (s *Store) userRecordByID(ctx context.Context, id string) (userRecord, error) {
	record, err := scanUser(s.queryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return userRecord{}, ErrNotFound
	}
	if err != nil {
		return userRecord{}, fmt.Errorf("get user: %w", err)
	}
	return record, nil
}

func (s *Store) userRecordByUsername(ctx context.Context, normalized string) (userRecord, error) {
	record, err := scanUser(s.queryRow(ctx, `SELECT `+userColumns+` FROM users WHERE username_normalized = ?`, normalized))
	if errors.Is(err, sql.ErrNoRows) {
		return userRecord{}, ErrNotFound
	}
	if err != nil {
		return userRecord{}, fmt.Errorf("get user by username: %w", err)
	}
	return record, nil
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	record, err := s.userRecordByID(ctx, id)
	if err != nil {
		return User{}, err
	}
	record.Entitlements, err = s.ListEntitlements(ctx, id)
	if err != nil {
		return User{}, err
	}
	return record.User, nil
}

func (s *Store) ListUsers(ctx context.Context, limit, offset int, search string) ([]User, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	where := ""
	args := make([]any, 0, 3)
	if strings.TrimSpace(search) != "" {
		where = ` WHERE username_normalized LIKE ? OR LOWER(display_name) LIKE ?`
		pattern := "%" + strings.ToLower(strings.TrimSpace(search)) + "%"
		args = append(args, pattern, pattern)
	}
	var total int
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count listed users: %w", err)
	}
	args = append(args, limit, offset)
	rows, err := s.query(ctx, `SELECT `+userColumns+` FROM users`+where+` ORDER BY username_normalized LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	users := make([]User, 0)
	for rows.Next() {
		record, err := scanUser(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan listed user: %w", err)
		}
		users = append(users, record.User)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, fmt.Errorf("iterate users: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("close listed users: %w", err)
	}
	for index := range users {
		entitlements, err := s.ListEntitlements(ctx, users[index].ID)
		if err != nil {
			return nil, 0, err
		}
		users[index].Entitlements = entitlements
	}
	return users, total, nil
}

func (s *Store) ListEntitlements(ctx context.Context, userID string) ([]Entitlement, error) {
	rows, err := s.query(ctx, `SELECT permission, effect, created_by, created_at FROM user_entitlements WHERE user_id = ? ORDER BY permission`, userID)
	if err != nil {
		return nil, fmt.Errorf("list entitlements: %w", err)
	}
	defer rows.Close()
	result := make([]Entitlement, 0)
	for rows.Next() {
		var entitlement Entitlement
		var permission, createdAt string
		var createdBy sql.NullString
		if err := rows.Scan(&permission, &entitlement.Effect, &createdBy, &createdAt); err != nil {
			return nil, fmt.Errorf("scan entitlement: %w", err)
		}
		entitlement.Permission = Permission(permission)
		entitlement.CreatedBy = createdBy.String
		entitlement.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		result = append(result, entitlement)
	}
	return result, rows.Err()
}

func (s *Store) ReplaceEntitlements(ctx context.Context, userID, actorID string, entitlements []Entitlement, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin entitlement update: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.rebind(`DELETE FROM user_entitlements WHERE user_id = ?`), userID); err != nil {
		return fmt.Errorf("clear entitlements: %w", err)
	}
	for _, entitlement := range entitlements {
		if _, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO user_entitlements (user_id, permission, effect, created_by, created_at) VALUES (?, ?, ?, ?, ?)`),
			userID, string(entitlement.Permission), entitlement.Effect, nullableString(actorID), formatTime(now)); err != nil {
			return fmt.Errorf("insert entitlement: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`UPDATE users SET authz_version = authz_version + 1, updated_at = ? WHERE id = ?`), formatTime(now), userID); err != nil {
		return fmt.Errorf("update authorization version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit entitlement update: %w", err)
	}
	return nil
}

type UpdateUserInput struct {
	Username           string
	UsernameNormalized string
	DisplayName        string
	Role               Role
	Status             string
	Now                time.Time
}

func (s *Store) UpdateUser(ctx context.Context, id string, input UpdateUserInput) (User, error) {
	result, err := s.exec(ctx, `UPDATE users SET username = ?, username_normalized = ?, display_name = ?, role = ?, status = ?, authz_version = authz_version + 1, updated_at = ? WHERE id = ?`,
		input.Username, input.UsernameNormalized, input.DisplayName, string(input.Role), input.Status, formatTime(input.Now), id)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, fmt.Errorf("%w: username already exists", ErrConflict)
		}
		return User{}, fmt.Errorf("update user: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return User{}, ErrNotFound
	}
	return s.UserByID(ctx, id)
}

func (s *Store) UpdateLoginFailure(ctx context.Context, id string, count int, lockedUntil *time.Time, now time.Time) error {
	var lock any
	if lockedUntil != nil {
		lock = formatTime(*lockedUntil)
	}
	_, err := s.exec(ctx, `UPDATE users SET failed_login_count = ?, locked_until = ?, updated_at = ? WHERE id = ?`, count, lock, formatTime(now), id)
	return err
}

func (s *Store) UpdateLoginSuccess(ctx context.Context, id, passwordHash string, now time.Time) error {
	if passwordHash == "" {
		_, err := s.exec(ctx, `UPDATE users SET failed_login_count = 0, locked_until = NULL, last_login_at = ?, updated_at = ? WHERE id = ?`, formatTime(now), formatTime(now), id)
		return err
	}
	_, err := s.exec(ctx, `UPDATE users SET password_hash = ?, failed_login_count = 0, locked_until = NULL, last_login_at = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`,
		passwordHash, formatTime(now), formatTime(now), formatTime(now), id)
	return err
}

func (s *Store) SetPassword(ctx context.Context, id, passwordHash string, mustChange bool, now time.Time) error {
	value := 0
	if mustChange {
		value = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, s.rebind(`UPDATE users SET password_hash = ?, must_change_password = ?, password_changed_at = ?, failed_login_count = 0, locked_until = NULL, updated_at = ? WHERE id = ?`),
		passwordHash, value, formatTime(now), formatTime(now), id)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`UPDATE sessions SET revoked_at = ?, revoke_reason = 'password changed' WHERE user_id = ? AND revoked_at IS NULL`), formatTime(now), id); err != nil {
		return fmt.Errorf("revoke password sessions: %w", err)
	}
	return tx.Commit()
}

func (s *Store) ActiveUserManagers(ctx context.Context) (int, error) {
	rows, err := s.query(ctx, `SELECT id FROM users WHERE status = 'active'`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	count := 0
	for _, id := range ids {
		user, err := s.UserByID(ctx, id)
		if err != nil {
			return 0, err
		}
		permissions := EffectivePermissions(user.Role, user.Status, user.Entitlements)
		if permissions.Has(PermissionUserManage) && permissions.Has(PermissionEntitlementManage) {
			count++
		}
	}
	return count, nil
}
