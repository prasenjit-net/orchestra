package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/prasenjit-net/orchestra/internal/config"
	appdb "github.com/prasenjit-net/orchestra/internal/database"
)

const SessionCookieName = "orchestra_session"

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`)
var bootstrapMu sync.Mutex
var userManagementMu sync.Mutex

type Service struct {
	store     *Store
	cfg       config.AuthConfig
	dialect   appdb.Dialect
	logger    *slog.Logger
	dummyHash string
	now       func() time.Time
}

func NewService(ctx context.Context, db *sql.DB, dialect appdb.Dialect, cfg config.AuthConfig, logger *slog.Logger) (*Service, error) {
	if !dialect.IsPostgres() {
		if err := ApplySchema(ctx, db, dialect); err != nil {
			return nil, err
		}
	} else if err := ValidateSchema(ctx, db); err != nil {
		return nil, err
	}
	dummyHash, err := HashPassword("dummy-password-never-valid")
	if err != nil {
		return nil, fmt.Errorf("initialize password verifier: %w", err)
	}
	return &Service{
		store:     NewStore(db, dialect),
		cfg:       cfg,
		dialect:   dialect,
		logger:    logger.With("component", "auth"),
		dummyHash: dummyHash,
		now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

type BootstrapResult struct {
	Created    bool
	Username   string
	OutputPath string
}

func (s *Service) BootstrapInitialAdmin(ctx context.Context) (BootstrapResult, error) {
	release, err := s.acquireBootstrapLock(ctx)
	if err != nil {
		return BootstrapResult{}, err
	}
	defer release()

	count, err := s.store.CountUsers(ctx)
	if err != nil {
		return BootstrapResult{}, err
	}
	if count > 0 {
		return BootstrapResult{}, nil
	}

	credential, err := s.prepareBootstrapAdmin()
	if err != nil {
		return BootstrapResult{}, err
	}

	now := s.now()
	user, err := s.store.CreateUser(ctx, CreateUserInput{
		ID:                 newID("usr"),
		Username:           credential.username,
		UsernameNormalized: credential.normalizedUsername,
		DisplayName:        "Administrator",
		PasswordHash:       credential.passwordHash,
		Role:               RoleAdmin,
		Status:             "active",
		MustChangePassword: true,
		Now:                now,
	})
	if err != nil {
		credential.removeOutput()
		if errors.Is(err, ErrConflict) {
			return BootstrapResult{}, nil
		}
		return BootstrapResult{}, err
	}
	_ = s.Audit(ctx, AuditEvent{
		OccurredAt:   now,
		ActorType:    PrincipalSystem,
		Action:       "user.bootstrap",
		ResourceType: "user",
		ResourceID:   user.ID,
		Outcome:      "success",
	})
	return BootstrapResult{Created: true, Username: credential.username, OutputPath: credential.outputPath}, nil
}

func (s *Service) acquireBootstrapLock(ctx context.Context) (func(), error) {
	bootstrapMu.Lock()
	if !s.dialect.IsPostgres() {
		return bootstrapMu.Unlock, nil
	}
	conn, err := s.store.db.Conn(ctx)
	if err != nil {
		bootstrapMu.Unlock()
		return nil, fmt.Errorf("open bootstrap lock connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(7349723410821)`); err != nil {
		_ = conn.Close()
		bootstrapMu.Unlock()
		return nil, fmt.Errorf("acquire bootstrap lock: %w", err)
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(7349723410821)`)
		_ = conn.Close()
		bootstrapMu.Unlock()
	}, nil
}

type bootstrapAdminCredential struct {
	username           string
	normalizedUsername string
	passwordHash       string
	outputPath         string
}

func (s *Service) prepareBootstrapAdmin() (bootstrapAdminCredential, error) {
	username := strings.TrimSpace(os.Getenv("APP_AUTH_INITIAL_ADMIN_USERNAME"))
	if username == "" {
		username = "admin"
	}
	normalized, err := NormalizeUsername(username)
	if err != nil {
		return bootstrapAdminCredential{}, fmt.Errorf("invalid initial admin username: %w", err)
	}
	password, generated, err := s.bootstrapPassword()
	if err != nil {
		return bootstrapAdminCredential{}, err
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return bootstrapAdminCredential{}, fmt.Errorf("invalid initial admin password: %w", err)
	}
	credential := bootstrapAdminCredential{username: username, normalizedUsername: normalized, passwordHash: passwordHash}
	if !generated {
		return credential, nil
	}
	credential.outputPath = s.cfg.BootstrapOutputPath
	if credential.outputPath == "" {
		return bootstrapAdminCredential{}, errors.New("auth.bootstrapOutputPath is required when generating an initial password")
	}
	if err := writeBootstrapCredential(credential.outputPath, username, password); err != nil {
		return bootstrapAdminCredential{}, err
	}
	return credential, nil
}

func (c bootstrapAdminCredential) removeOutput() {
	if c.outputPath != "" {
		_ = os.Remove(c.outputPath)
	}
}

func (s *Service) bootstrapPassword() (string, bool, error) {
	if path := strings.TrimSpace(os.Getenv("APP_AUTH_INITIAL_ADMIN_PASSWORD_FILE")); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", false, fmt.Errorf("read initial admin password file: %w", err)
		}
		password := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		return password, false, nil
	}
	if password, ok := os.LookupEnv("APP_AUTH_INITIAL_ADMIN_PASSWORD"); ok && password != "" {
		return password, false, nil
	}
	password, err := randomString(32)
	return password, true, err
}

func writeBootstrapCredential(path, username, password string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create bootstrap credential directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create bootstrap credential file: %w", err)
	}
	content := fmt.Sprintf("username=%s\npassword=%s\nchange_password_on_first_login=true\n", username, password)
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write bootstrap credential file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close bootstrap credential file: %w", err)
	}
	return nil
}

func NormalizeUsername(username string) (string, error) {
	trimmed := strings.TrimSpace(username)
	if !usernamePattern.MatchString(trimmed) {
		return "", errors.New("username must be 3-64 ASCII letters, numbers, dots, underscores, or hyphens")
	}
	return strings.ToLower(trimmed), nil
}

func randomString(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate random value: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

func newID(prefix string) string {
	random, err := randomString(18)
	if err != nil {
		panic(err)
	}
	return prefix + "_" + random
}

func hashValue(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func normalizeIP(value string) string {
	trimmed := strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(trimmed); err == nil {
		trimmed = host
	}
	if parsed := net.ParseIP(trimmed); parsed != nil {
		return parsed.String()
	}
	if len(trimmed) > 128 {
		return trimmed[:128]
	}
	return trimmed
}

type LoginInput struct {
	Username  string
	Password  string
	SourceIP  string
	UserAgent string
	RequestID string
}

type SessionResult struct {
	Token     string
	Session   Session
	Principal Principal
}

func (s *Service) Login(ctx context.Context, input LoginInput) (SessionResult, error) {
	now := s.now()
	normalized, normalizeErr := NormalizeUsername(input.Username)
	ip := normalizeIP(input.SourceIP)
	if err := s.checkLoginRateLimits(ctx, input, normalized, ip, now); err != nil {
		return SessionResult{}, err
	}
	record, err := s.loginRecord(ctx, input, normalized, normalizeErr, now)
	if err != nil {
		return SessionResult{}, err
	}
	valid, needsRehash := VerifyPassword(record.PasswordHash, input.Password)
	if !valid {
		return SessionResult{}, s.recordFailedLogin(ctx, input, record, now)
	}
	newHash, err := updatedPasswordHash(input.Password, needsRehash)
	if err != nil {
		return SessionResult{}, err
	}
	if err := s.store.UpdateLoginSuccess(ctx, record.ID, newHash, now); err != nil {
		return SessionResult{}, err
	}
	record, err = s.store.userRecordByID(ctx, record.ID)
	if err != nil {
		return SessionResult{}, err
	}
	result, err := s.createSession(ctx, record, ip, input.UserAgent, now)
	if err != nil {
		return SessionResult{}, err
	}
	_ = s.Audit(ctx, AuditEvent{
		OccurredAt:   now,
		RequestID:    input.RequestID,
		ActorType:    PrincipalUser,
		ActorID:      record.ID,
		Action:       "auth.login",
		ResourceType: "session",
		ResourceID:   result.Session.ID,
		Outcome:      "success",
		SourceIP:     ip,
		UserAgent:    input.UserAgent,
	})
	return result, nil
}

func (s *Service) checkLoginRateLimits(ctx context.Context, input LoginInput, normalizedUsername, ip string, now time.Time) error {
	buckets := []struct {
		hash       string
		bucketType string
		limit      int
	}{
		{hashValue("login-account:" + normalizedUsername), "login_account", 10},
		{hashValue("login-ip:" + ip), "login_ip", 60},
	}
	for _, bucket := range buckets {
		allowed, _, err := s.store.ConsumeRateLimit(ctx, bucket.hash, bucket.bucketType, now, time.Minute, bucket.limit)
		if err != nil {
			return err
		}
		if !allowed {
			s.auditLogin(ctx, input, "denied", "rate_limited")
			return ErrInvalidCredentials
		}
	}
	return nil
}

func (s *Service) loginRecord(ctx context.Context, input LoginInput, normalized string, normalizeErr error, now time.Time) (userRecord, error) {
	if normalizeErr != nil {
		return s.rejectUnknownLogin(ctx, input)
	}
	record, err := s.store.userRecordByUsername(ctx, normalized)
	if errors.Is(err, ErrNotFound) {
		return s.rejectUnknownLogin(ctx, input)
	}
	if err != nil {
		return userRecord{}, err
	}
	if record.Status != "active" || (record.LockedUntil != nil && now.Before(*record.LockedUntil)) {
		return s.rejectUnknownLogin(ctx, input)
	}
	return record, nil
}

func (s *Service) rejectUnknownLogin(ctx context.Context, input LoginInput) (userRecord, error) {
	VerifyPassword(s.dummyHash, input.Password)
	s.auditLogin(ctx, input, "failure", "invalid_credentials")
	return userRecord{}, ErrInvalidCredentials
}

func (s *Service) recordFailedLogin(ctx context.Context, input LoginInput, record userRecord, now time.Time) error {
	failed := record.FailedLoginCount + 1
	var lockedUntil *time.Time
	if failed >= 10 {
		locked := now.Add(15 * time.Minute)
		lockedUntil = &locked
	}
	_ = s.store.UpdateLoginFailure(ctx, record.ID, failed, lockedUntil, now)
	s.auditLogin(ctx, input, "failure", "invalid_credentials")
	return ErrInvalidCredentials
}

func updatedPasswordHash(password string, needsRehash bool) (string, error) {
	if !needsRehash {
		return "", nil
	}
	return HashPassword(password)
}

func (s *Service) auditLogin(ctx context.Context, input LoginInput, outcome, reason string) {
	metadata, _ := json.Marshal(map[string]string{"reason": reason})
	_ = s.Audit(ctx, AuditEvent{
		OccurredAt: s.now(), RequestID: input.RequestID, ActorType: PrincipalAnonymous,
		Action: "auth.login", Outcome: outcome, SourceIP: normalizeIP(input.SourceIP),
		UserAgent: input.UserAgent, Metadata: metadata,
	})
}

func (s *Service) createSession(ctx context.Context, record userRecord, sourceIP, userAgent string, now time.Time) (SessionResult, error) {
	token, err := randomString(32)
	if err != nil {
		return SessionResult{}, err
	}
	csrfToken, err := randomString(32)
	if err != nil {
		return SessionResult{}, err
	}
	session, err := s.store.CreateSession(ctx, CreateSessionInput{
		ID:                     newID("ses"),
		TokenHash:              hashValue(token),
		CSRFToken:              csrfToken,
		UserID:                 record.ID,
		CreatedAt:              now,
		IdleExpiresAt:          now.Add(s.cfg.SessionIdleTimeout),
		AbsoluteExpiresAt:      now.Add(s.cfg.SessionAbsoluteTimeout),
		PasswordChangedAtLogin: record.PasswordChangedAt,
		AuthzVersionAtLogin:    record.AuthzVersion,
		SourceIP:               sourceIP,
		UserAgentHash:          hashValue(userAgent),
	})
	if err != nil {
		return SessionResult{}, err
	}
	return s.sessionResult(ctx, token, session, record)
}

func (s *Service) sessionResult(ctx context.Context, token string, session Session, record userRecord) (SessionResult, error) {
	entitlements, err := s.store.ListEntitlements(ctx, record.ID)
	if err != nil {
		return SessionResult{}, err
	}
	record.Entitlements = entitlements
	user := record.User
	principal := Principal{
		Type:        PrincipalUser,
		ID:          user.ID,
		DisplayName: user.DisplayName,
		Permissions: EffectivePermissions(user.Role, user.Status, entitlements),
		SessionID:   session.ID,
		User:        &user,
		CSRFToken:   session.CSRFToken,
	}
	return SessionResult{Token: token, Session: session, Principal: principal}, nil
}

func (s *Service) AuthenticateSession(ctx context.Context, token string) (Principal, Session, error) {
	if token == "" {
		return Principal{}, Session{}, ErrInvalidCredentials
	}
	session, err := s.store.SessionByTokenHash(ctx, hashValue(token))
	if err != nil {
		return Principal{}, Session{}, ErrInvalidCredentials
	}
	now := s.now()
	if session.RevokedAt != nil || !now.Before(session.IdleExpiresAt) || !now.Before(session.AbsoluteExpiresAt) {
		if session.RevokedAt == nil {
			_ = s.store.RevokeSession(ctx, session.ID, "expired", now)
		}
		return Principal{}, Session{}, ErrInvalidCredentials
	}
	record, err := s.store.userRecordByID(ctx, session.UserID)
	if err != nil || record.Status != "active" || record.PasswordChangedAt.After(session.PasswordChangedAtLogin) {
		return Principal{}, Session{}, ErrInvalidCredentials
	}
	entitlements, err := s.store.ListEntitlements(ctx, record.ID)
	if err != nil {
		return Principal{}, Session{}, err
	}
	record.Entitlements = entitlements
	if now.Sub(session.LastSeenAt) >= 5*time.Minute {
		idleExpiry := now.Add(s.cfg.SessionIdleTimeout)
		if idleExpiry.After(session.AbsoluteExpiresAt) {
			idleExpiry = session.AbsoluteExpiresAt
		}
		if err := s.store.TouchSession(ctx, session.ID, now, idleExpiry); err == nil {
			session.LastSeenAt = now
			session.IdleExpiresAt = idleExpiry
		}
	}
	result, err := s.sessionResult(ctx, token, session, record)
	return result.Principal, session, err
}

func (s *Service) Logout(ctx context.Context, principal Principal, requestID, sourceIP, userAgent string) error {
	if principal.SessionID == "" {
		return ErrNotFound
	}
	now := s.now()
	err := s.store.RevokeSession(ctx, principal.SessionID, "logout", now)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	_ = s.Audit(ctx, AuditEvent{
		OccurredAt: now, RequestID: requestID, ActorType: PrincipalUser, ActorID: principal.ID,
		Action: "auth.logout", ResourceType: "session", ResourceID: principal.SessionID,
		Outcome: "success", SourceIP: normalizeIP(sourceIP), UserAgent: userAgent,
	})
	return nil
}

func (s *Service) ChangePassword(ctx context.Context, principal Principal, currentPassword, newPassword, sourceIP, userAgent string) (SessionResult, error) {
	record, err := s.store.userRecordByID(ctx, principal.ID)
	if err != nil {
		return SessionResult{}, err
	}
	valid, _ := VerifyPassword(record.PasswordHash, currentPassword)
	if !valid {
		return SessionResult{}, ErrInvalidCredentials
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return SessionResult{}, err
	}
	now := s.now()
	if err := s.store.SetPassword(ctx, record.ID, hash, false, now); err != nil {
		return SessionResult{}, err
	}
	record, err = s.store.userRecordByID(ctx, record.ID)
	if err != nil {
		return SessionResult{}, err
	}
	result, err := s.createSession(ctx, record, normalizeIP(sourceIP), userAgent, now)
	if err != nil {
		return SessionResult{}, err
	}
	if s.cfg.BootstrapOutputPath != "" {
		_ = os.Remove(s.cfg.BootstrapOutputPath)
	}
	_ = s.Audit(ctx, AuditEvent{OccurredAt: now, ActorType: PrincipalUser, ActorID: record.ID, Action: "auth.password_change", ResourceType: "user", ResourceID: record.ID, Outcome: "success", SourceIP: normalizeIP(sourceIP), UserAgent: userAgent})
	return result, nil
}

func (s *Service) Audit(ctx context.Context, event AuditEvent) error {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.now()
	}
	return s.store.AppendAuditEvent(ctx, event)
}

func (s *Service) Cleanup(ctx context.Context) error {
	now := s.now()
	if err := s.store.DeleteExpiredSessions(ctx, now); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	if err := s.store.DeleteExpiredRateLimits(ctx, now); err != nil {
		return fmt.Errorf("delete expired rate limits: %w", err)
	}
	if s.cfg.AuditRetention > 0 {
		if _, err := s.store.DeleteAuditEventsBefore(ctx, now.Add(-s.cfg.AuditRetention)); err != nil {
			return fmt.Errorf("delete expired audit events: %w", err)
		}
	}
	return nil
}

func (s *Service) RecoverPassword(ctx context.Context, username, password string) (User, error) {
	normalized, err := NormalizeUsername(username)
	if err != nil {
		return User{}, err
	}
	record, err := s.store.userRecordByUsername(ctx, normalized)
	if err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	now := s.now()
	if err := s.store.SetPassword(ctx, record.ID, hash, true, now); err != nil {
		return User{}, err
	}
	_ = s.Audit(ctx, AuditEvent{
		OccurredAt: now, ActorType: PrincipalSystem, Action: "user.password_recover",
		ResourceType: "user", ResourceID: record.ID, Outcome: "success",
	})
	record, err = s.store.userRecordByID(ctx, record.ID)
	return record.User, err
}

type CreateManagedUserInput struct {
	Username    string
	DisplayName string
	Role        Role
	Password    string
}

type CreatedUser struct {
	User              User   `json:"user"`
	TemporaryPassword string `json:"temporaryPassword,omitempty"`
}

func (s *Service) CreateUser(ctx context.Context, actor Principal, input CreateManagedUserInput) (CreatedUser, error) {
	if !actor.Has(PermissionUserManage) {
		return CreatedUser{}, ErrForbidden
	}
	normalized, err := NormalizeUsername(input.Username)
	if err != nil {
		return CreatedUser{}, err
	}
	if !ValidRole(input.Role) {
		return CreatedUser{}, errors.New("invalid role")
	}
	password := input.Password
	generated := false
	if password == "" {
		password, err = randomString(24)
		if err != nil {
			return CreatedUser{}, err
		}
		generated = true
	}
	hash, err := HashPassword(password)
	if err != nil {
		return CreatedUser{}, err
	}
	now := s.now()
	user, err := s.store.CreateUser(ctx, CreateUserInput{
		ID: newID("usr"), Username: strings.TrimSpace(input.Username), UsernameNormalized: normalized,
		DisplayName: strings.TrimSpace(input.DisplayName), PasswordHash: hash, Role: input.Role,
		Status: "active", MustChangePassword: true, CreatedBy: actor.ID, Now: now,
	})
	if err != nil {
		return CreatedUser{}, err
	}
	metadata, _ := json.Marshal(map[string]string{"role": string(input.Role)})
	_ = s.Audit(ctx, AuditEvent{OccurredAt: now, ActorType: PrincipalUser, ActorID: actor.ID, Action: "user.create", ResourceType: "user", ResourceID: user.ID, Outcome: "success", Metadata: metadata})
	result := CreatedUser{User: user}
	if generated {
		result.TemporaryPassword = password
	}
	return result, nil
}

func (s *Service) ListUsers(ctx context.Context, actor Principal, limit, offset int, search string) ([]User, int, error) {
	if !actor.Has(PermissionUserRead) {
		return nil, 0, ErrForbidden
	}
	return s.store.ListUsers(ctx, limit, offset, search)
}

func (s *Service) GetUser(ctx context.Context, actor Principal, id string) (User, error) {
	if !actor.Has(PermissionUserRead) && actor.ID != id {
		return User{}, ErrForbidden
	}
	return s.store.UserByID(ctx, id)
}

type UpdateManagedUserInput struct {
	Username    string
	DisplayName string
	Role        Role
	Status      string
}

func (s *Service) UpdateUser(ctx context.Context, actor Principal, id string, input UpdateManagedUserInput) (User, error) {
	if !actor.Has(PermissionUserManage) {
		return User{}, ErrForbidden
	}
	release, err := s.acquireUserManagementLock(ctx)
	if err != nil {
		return User{}, err
	}
	defer release()
	current, err := s.store.UserByID(ctx, id)
	if err != nil {
		return User{}, err
	}
	normalized, err := validateManagedUserUpdate(input)
	if err != nil {
		return User{}, err
	}
	if err := s.ensureUserManagerRemains(ctx, isUserManager(current.Role, current.Status, current.Entitlements), isUserManager(input.Role, input.Status, current.Entitlements)); err != nil {
		return User{}, err
	}
	now := s.now()
	updated, err := s.store.UpdateUser(ctx, id, UpdateUserInput{
		Username: strings.TrimSpace(input.Username), UsernameNormalized: normalized,
		DisplayName: strings.TrimSpace(input.DisplayName), Role: input.Role, Status: input.Status, Now: now,
	})
	if err != nil {
		return User{}, err
	}
	if input.Status == "disabled" {
		_ = s.store.RevokeUserSessions(ctx, id, "user disabled", "", now)
	}
	metadata, _ := json.Marshal(map[string]any{"beforeRole": current.Role, "afterRole": input.Role, "beforeStatus": current.Status, "afterStatus": input.Status})
	_ = s.Audit(ctx, AuditEvent{OccurredAt: now, ActorType: PrincipalUser, ActorID: actor.ID, Action: "user.update", ResourceType: "user", ResourceID: id, Outcome: "success", Metadata: metadata})
	return updated, nil
}

func validateManagedUserUpdate(input UpdateManagedUserInput) (string, error) {
	if !ValidRole(input.Role) {
		return "", errors.New("invalid role")
	}
	if input.Status != "active" && input.Status != "disabled" {
		return "", errors.New("invalid user status")
	}
	return NormalizeUsername(input.Username)
}

func isUserManager(role Role, status string, entitlements []Entitlement) bool {
	permissions := EffectivePermissions(role, status, entitlements)
	return permissions.Has(PermissionUserManage) && permissions.Has(PermissionEntitlementManage)
}

func (s *Service) ensureUserManagerRemains(ctx context.Context, beforeManager, afterManager bool) error {
	if !beforeManager || afterManager {
		return nil
	}
	managers, err := s.store.ActiveUserManagers(ctx)
	if err != nil {
		return err
	}
	if managers <= 1 {
		return fmt.Errorf("%w: cannot remove the final active administrator", ErrConflict)
	}
	return nil
}

func (s *Service) ReplaceEntitlements(ctx context.Context, actor Principal, userID string, entitlements []Entitlement) (User, error) {
	if !actor.Has(PermissionEntitlementManage) {
		return User{}, ErrForbidden
	}
	release, err := s.acquireUserManagementLock(ctx)
	if err != nil {
		return User{}, err
	}
	defer release()
	if err := ValidateEntitlements(entitlements); err != nil {
		return User{}, err
	}
	current, err := s.store.UserByID(ctx, userID)
	if err != nil {
		return User{}, err
	}
	if err := s.ensureUserManagerRemains(ctx, isUserManager(current.Role, current.Status, current.Entitlements), isUserManager(current.Role, current.Status, entitlements)); err != nil {
		return User{}, err
	}
	now := s.now()
	if err := s.store.ReplaceEntitlements(ctx, userID, actor.ID, entitlements, now); err != nil {
		return User{}, err
	}
	metadata, _ := json.Marshal(map[string]any{"entitlements": entitlements})
	_ = s.Audit(ctx, AuditEvent{OccurredAt: now, ActorType: PrincipalUser, ActorID: actor.ID, Action: "user.entitlements_update", ResourceType: "user", ResourceID: userID, Outcome: "success", Metadata: metadata})
	return s.store.UserByID(ctx, userID)
}

func (s *Service) acquireUserManagementLock(ctx context.Context) (func(), error) {
	userManagementMu.Lock()
	if !s.dialect.IsPostgres() {
		return userManagementMu.Unlock, nil
	}
	conn, err := s.store.db.Conn(ctx)
	if err != nil {
		userManagementMu.Unlock()
		return nil, fmt.Errorf("open user management lock connection: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(7349723410822)`); err != nil {
		_ = conn.Close()
		userManagementMu.Unlock()
		return nil, fmt.Errorf("acquire user management lock: %w", err)
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(7349723410822)`)
		_ = conn.Close()
		userManagementMu.Unlock()
	}, nil
}

func (s *Service) ResetPassword(ctx context.Context, actor Principal, userID, password string) (string, error) {
	if !actor.Has(PermissionUserManage) {
		return "", ErrForbidden
	}
	if _, err := s.store.UserByID(ctx, userID); err != nil {
		return "", err
	}
	generated := password == ""
	var err error
	if generated {
		password, err = randomString(24)
		if err != nil {
			return "", err
		}
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", err
	}
	now := s.now()
	if err := s.store.SetPassword(ctx, userID, hash, true, now); err != nil {
		return "", err
	}
	_ = s.Audit(ctx, AuditEvent{OccurredAt: now, ActorType: PrincipalUser, ActorID: actor.ID, Action: "user.password_reset", ResourceType: "user", ResourceID: userID, Outcome: "success"})
	if generated {
		return password, nil
	}
	return "", nil
}

func (s *Service) ListSessions(ctx context.Context, actor Principal, userID string) ([]Session, error) {
	if userID == "" {
		userID = actor.ID
	}
	if userID != actor.ID && !actor.Has(PermissionSessionManageAll) {
		return nil, ErrForbidden
	}
	if userID == actor.ID && !actor.Has(PermissionSessionManageOwn) {
		return nil, ErrForbidden
	}
	return s.store.ListUserSessions(ctx, userID, s.now())
}

func (s *Service) RevokeSession(ctx context.Context, actor Principal, sessionID string) error {
	session, err := s.store.SessionByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.UserID != actor.ID && !actor.Has(PermissionSessionManageAll) {
		return ErrForbidden
	}
	if session.UserID == actor.ID && !actor.Has(PermissionSessionManageOwn) {
		return ErrForbidden
	}
	return s.store.RevokeSession(ctx, sessionID, "revoked by user", s.now())
}

type CreateAPIKeyRequest struct {
	Name        string
	Description string
	ExpiresAt   *time.Time
	Grants      []APIKeyGrant
}

type APIKeySecret struct {
	APIKey APIKey `json:"apiKey"`
	Secret string `json:"secret"`
}

var apiKeyActions = map[string]struct{}{
	"start": {}, "signal": {}, "status.read": {}, "result.read": {},
}

func validateAPIKeyGrants(grants []APIKeyGrant) error {
	if len(grants) == 0 {
		return errors.New("at least one workflow grant is required")
	}
	seen := make(map[string]struct{}, len(grants))
	for index := range grants {
		if err := validateAPIKeyGrant(&grants[index], seen); err != nil {
			return err
		}
	}
	return nil
}

func validateAPIKeyGrant(grant *APIKeyGrant, seen map[string]struct{}) error {
	grant.WorkflowDefinitionID = strings.TrimSpace(grant.WorkflowDefinitionID)
	if grant.WorkflowDefinitionID == "" {
		return errors.New("workflow definition id is required")
	}
	if _, ok := apiKeyActions[grant.Action]; !ok {
		return fmt.Errorf("invalid api key action %q", grant.Action)
	}
	if grant.InstanceScope == "" {
		grant.InstanceScope = "own"
	}
	if grant.InstanceScope != "own" && grant.InstanceScope != "definition" {
		return fmt.Errorf("invalid instance scope %q", grant.InstanceScope)
	}
	key := grant.WorkflowDefinitionID + "\x00" + grant.Action
	if _, exists := seen[key]; exists {
		return errors.New("duplicate workflow grant")
	}
	seen[key] = struct{}{}
	return validateSignalNames(grant.SignalNames)
}

func validateSignalNames(signalNames []string) error {
	for _, signal := range signalNames {
		if strings.TrimSpace(signal) == "" || len(signal) > 128 {
			return errors.New("invalid signal name restriction")
		}
	}
	return nil
}

func (s *Service) normalizeAPIKeyExpiry(actor Principal, expiresAt *time.Time, now time.Time) (*time.Time, error) {
	if expiresAt == nil {
		expires := now.Add(s.cfg.APIKeys.DefaultTTL)
		return &expires, nil
	}
	if !expiresAt.After(now) {
		return nil, errors.New("api key expiration must be in the future")
	}
	if expiresAt.After(now.Add(s.cfg.APIKeys.MaximumTTL)) && !actor.Has(PermissionAPIKeyManageAll) {
		return nil, errors.New("api key expiration exceeds the maximum lifetime")
	}
	return expiresAt, nil
}

func (s *Service) CreateAPIKey(ctx context.Context, actor Principal, input CreateAPIKeyRequest) (APIKeySecret, error) {
	if !actor.Has(PermissionAPIKeyCreate) {
		return APIKeySecret{}, ErrForbidden
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 128 {
		return APIKeySecret{}, errors.New("api key name is required and must be at most 128 characters")
	}
	if len(input.Description) > 1024 {
		return APIKeySecret{}, errors.New("api key description must be at most 1024 characters")
	}
	if err := validateAPIKeyGrants(input.Grants); err != nil {
		return APIKeySecret{}, err
	}
	now := s.now()
	expiresAt, err := s.normalizeAPIKeyExpiry(actor, input.ExpiresAt, now)
	if err != nil {
		return APIKeySecret{}, err
	}
	prefix, err := randomHex(6)
	if err != nil {
		return APIKeySecret{}, err
	}
	secretPart, err := randomHex(32)
	if err != nil {
		return APIKeySecret{}, err
	}
	secret := "orch_" + prefix + "_" + secretPart
	key, err := s.store.CreateAPIKey(ctx, CreateAPIKeyInput{
		ID: newID("key"), Name: strings.TrimSpace(input.Name), Description: strings.TrimSpace(input.Description),
		KeyPrefix: prefix, SecretHash: hashValue(secretPart), CreatedByUserID: actor.ID,
		ExpiresAt: expiresAt, Grants: input.Grants, Now: now,
	})
	if err != nil {
		return APIKeySecret{}, err
	}
	_ = s.Audit(ctx, AuditEvent{OccurredAt: now, ActorType: PrincipalUser, ActorID: actor.ID, Action: "api_key.create", ResourceType: "api_key", ResourceID: key.ID, Outcome: "success"})
	return APIKeySecret{APIKey: key, Secret: secret}, nil
}

func (s *Service) canManageAPIKey(actor Principal, key APIKey) bool {
	return actor.Has(PermissionAPIKeyManageAll) || (actor.Has(PermissionAPIKeyManageOwn) && key.CreatedByUserID == actor.ID)
}

func (s *Service) ListAPIKeys(ctx context.Context, actor Principal, limit, offset int) ([]APIKey, int, error) {
	if !actor.Has(PermissionAPIKeyRead) {
		return nil, 0, ErrForbidden
	}
	return s.store.ListAPIKeys(ctx, actor.ID, actor.Has(PermissionAPIKeyManageAll), limit, offset)
}

func (s *Service) GetAPIKey(ctx context.Context, actor Principal, id string) (APIKey, error) {
	if !actor.Has(PermissionAPIKeyRead) {
		return APIKey{}, ErrForbidden
	}
	key, err := s.store.APIKeyByID(ctx, id)
	if err != nil {
		return APIKey{}, err
	}
	if key.CreatedByUserID != actor.ID && !actor.Has(PermissionAPIKeyManageAll) {
		return APIKey{}, ErrNotFound
	}
	return key, nil
}

func (s *Service) UpdateAPIKey(ctx context.Context, actor Principal, id string, input CreateAPIKeyRequest) (APIKey, error) {
	key, err := s.store.APIKeyByID(ctx, id)
	if err != nil {
		return APIKey{}, err
	}
	if !s.canManageAPIKey(actor, key) {
		return APIKey{}, ErrForbidden
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 128 {
		return APIKey{}, errors.New("api key name is required and must be at most 128 characters")
	}
	if len(input.Description) > 1024 {
		return APIKey{}, errors.New("api key description must be at most 1024 characters")
	}
	if err := validateAPIKeyGrants(input.Grants); err != nil {
		return APIKey{}, err
	}
	expires, err := s.normalizeAPIKeyExpiry(actor, input.ExpiresAt, s.now())
	if err != nil {
		return APIKey{}, err
	}
	updated, err := s.store.UpdateAPIKey(ctx, id, strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), expires, input.Grants, s.now())
	if err == nil {
		_ = s.Audit(ctx, AuditEvent{ActorType: PrincipalUser, ActorID: actor.ID, Action: "api_key.update", ResourceType: "api_key", ResourceID: id, Outcome: "success"})
	}
	return updated, err
}

func (s *Service) RevokeAPIKey(ctx context.Context, actor Principal, id string) error {
	key, err := s.store.APIKeyByID(ctx, id)
	if err != nil {
		return err
	}
	if !s.canManageAPIKey(actor, key) {
		return ErrForbidden
	}
	if err := s.store.RevokeAPIKey(ctx, id, actor.ID, s.now()); err != nil {
		return err
	}
	return s.Audit(ctx, AuditEvent{ActorType: PrincipalUser, ActorID: actor.ID, Action: "api_key.revoke", ResourceType: "api_key", ResourceID: id, Outcome: "success"})
}

func (s *Service) RotateAPIKey(ctx context.Context, actor Principal, id string) (APIKeySecret, error) {
	old, err := s.store.APIKeyByID(ctx, id)
	if err != nil {
		return APIKeySecret{}, err
	}
	if !s.canManageAPIKey(actor, old) {
		return APIKeySecret{}, ErrForbidden
	}
	prefix, err := randomHex(6)
	if err != nil {
		return APIKeySecret{}, err
	}
	secretPart, err := randomHex(32)
	if err != nil {
		return APIKeySecret{}, err
	}
	secret := "orch_" + prefix + "_" + secretPart
	now := s.now()
	rotated, err := s.store.RotateAPIKey(ctx, id, actor.ID, CreateAPIKeyInput{
		ID: newID("key"), Name: old.Name, Description: old.Description, KeyPrefix: prefix,
		SecretHash: hashValue(secretPart), CreatedByUserID: old.CreatedByUserID, ExpiresAt: old.ExpiresAt,
		RotatedFromID: old.ID, Grants: old.Grants, Now: now,
	})
	if err != nil {
		return APIKeySecret{}, err
	}
	_ = s.Audit(ctx, AuditEvent{OccurredAt: now, ActorType: PrincipalUser, ActorID: actor.ID, Action: "api_key.rotate", ResourceType: "api_key", ResourceID: rotated.ID, Outcome: "success"})
	return APIKeySecret{APIKey: rotated, Secret: secret}, nil
}

func (s *Service) AuthenticateAPIKey(ctx context.Context, rawKey, sourceIP string) (Principal, APIKey, error) {
	encoded, ok := strings.CutPrefix(rawKey, "orch_")
	if !ok {
		return Principal{}, APIKey{}, ErrInvalidCredentials
	}
	prefix, secretPart, ok := strings.Cut(encoded, "_")
	if !ok || len(prefix) != 12 || len(secretPart) != 64 {
		return Principal{}, APIKey{}, ErrInvalidCredentials
	}
	record, err := s.store.apiKeyRecordByPrefix(ctx, prefix)
	if err != nil {
		return Principal{}, APIKey{}, ErrInvalidCredentials
	}
	actual := hashValue(secretPart)
	if subtle.ConstantTimeCompare([]byte(actual), []byte(record.SecretHash)) != 1 || record.Status != "active" {
		return Principal{}, APIKey{}, ErrInvalidCredentials
	}
	now := s.now()
	if record.ExpiresAt != nil && !now.Before(*record.ExpiresAt) {
		return Principal{}, APIKey{}, ErrInvalidCredentials
	}
	allowed, _, err := s.store.ConsumeRateLimit(ctx, hashValue("api-key:"+record.ID), "api_key", now, time.Minute, s.cfg.APIKeys.RequestsPerMinute+s.cfg.APIKeys.Burst)
	if err != nil {
		return Principal{}, APIKey{}, err
	}
	if !allowed {
		return Principal{}, APIKey{}, fmt.Errorf("%w: rate limited", ErrForbidden)
	}
	ip := normalizeIP(sourceIP)
	if record.LastUsedAt == nil || now.Sub(*record.LastUsedAt) >= s.cfg.APIKeys.UsageWriteWindow {
		_ = s.store.TouchAPIKey(ctx, record.ID, ip, now)
	}
	principal := Principal{Type: PrincipalAPIKey, ID: record.ID, DisplayName: record.Name, APIKeyID: record.ID, Permissions: PermissionSet{}}
	return principal, record.APIKey, nil
}

type APIKeyAuthorizationInput struct {
	DefinitionID        string
	Action              string
	WorkflowTriggerType string
	WorkflowTriggerID   string
	SignalName          string
	PinnedVersion       bool
	HasCallbackURL      bool
}

func AuthorizeAPIKey(key APIKey, input APIKeyAuthorizationInput) (APIKeyGrant, error) {
	grant, ok := findGrant(key.Grants, input.DefinitionID, input.Action)
	if !ok {
		return APIKeyGrant{}, ErrForbidden
	}
	if input.Action != "start" && grant.InstanceScope == "own" {
		if input.WorkflowTriggerType != string(PrincipalAPIKey) || input.WorkflowTriggerID != key.ID {
			return APIKeyGrant{}, ErrForbidden
		}
	}
	if input.SignalName != "" && !includesSignal(grant, input.SignalName) {
		return APIKeyGrant{}, ErrForbidden
	}
	if input.PinnedVersion && !grant.AllowPinnedVersions {
		return APIKeyGrant{}, ErrForbidden
	}
	if input.HasCallbackURL && !grant.AllowCallbackURL {
		return APIKeyGrant{}, ErrForbidden
	}
	return grant, nil
}

func (s *Service) ListAuditEvents(ctx context.Context, actor Principal, input ListAuditInput) ([]AuditEvent, int, error) {
	if !actor.Has(PermissionAuditRead) {
		return nil, 0, ErrForbidden
	}
	return s.store.ListAuditEvents(ctx, input)
}
