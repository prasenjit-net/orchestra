package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/prasenjit-net/orchestra/internal/config"
	appdb "github.com/prasenjit-net/orchestra/internal/database"
)

func newTestService(t *testing.T) (*Service, config.Config) {
	t.Helper()
	cfg := config.Default()
	cfg.Workflow.DatabaseDriver = "sqlite"
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "auth.db")
	cfg.Auth.BootstrapOutputPath = filepath.Join(t.TempDir(), "bootstrap-admin.txt")
	db, dialect, err := appdb.Open(context.Background(), cfg.Workflow)
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := NewService(context.Background(), db, dialect, cfg.Auth, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, cfg
}

func bootstrapAdmin(t *testing.T, service *Service) SessionResult {
	t.Helper()
	t.Setenv("APP_AUTH_INITIAL_ADMIN_USERNAME", "admin")
	t.Setenv("APP_AUTH_INITIAL_ADMIN_PASSWORD", "Secur3-bootstrap-password!")
	result, err := service.BootstrapInitialAdmin(context.Background())
	if err != nil || !result.Created {
		t.Fatalf("BootstrapInitialAdmin() = %#v, %v", result, err)
	}
	second, err := service.BootstrapInitialAdmin(context.Background())
	if err != nil || second.Created {
		t.Fatalf("second BootstrapInitialAdmin() = %#v, %v", second, err)
	}
	login, err := service.Login(context.Background(), LoginInput{Username: "ADMIN", Password: "Secur3-bootstrap-password!", SourceIP: "127.0.0.1:4000", UserAgent: "test"})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	return login
}

func TestBootstrapLoginChangePasswordAndRecovery(t *testing.T) {
	service, _ := newTestService(t)
	login := bootstrapAdmin(t, service)
	if !login.Principal.User.MustChangePassword {
		t.Fatal("bootstrap administrator did not require a password change")
	}
	changed, err := service.ChangePassword(context.Background(), login.Principal, "Secur3-bootstrap-password!", "Secur3-admin-password-new!", "127.0.0.1", "test")
	if err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if changed.Principal.User.MustChangePassword {
		t.Fatal("password-change requirement remained after password update")
	}
	if _, _, err := service.AuthenticateSession(context.Background(), login.Token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old session authentication error = %v, want invalid credentials", err)
	}

	if _, err := service.RecoverPassword(context.Background(), "admin", "Secur3-recovered-password!"); err != nil {
		t.Fatalf("RecoverPassword() error = %v", err)
	}
	if _, _, err := service.AuthenticateSession(context.Background(), changed.Token); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("pre-recovery session authentication error = %v, want invalid credentials", err)
	}
	recovered, err := service.Login(context.Background(), LoginInput{Username: "admin", Password: "Secur3-recovered-password!", SourceIP: "127.0.0.1"})
	if err != nil || !recovered.Principal.User.MustChangePassword {
		t.Fatalf("recovered Login() mustChange = %v, error = %v", recovered.Principal.User.MustChangePassword, err)
	}
}

func TestUserEntitlementsAndFinalAdministratorProtection(t *testing.T) {
	service, _ := newTestService(t)
	bootstrap := bootstrapAdmin(t, service)
	admin, err := service.ChangePassword(context.Background(), bootstrap.Principal, "Secur3-bootstrap-password!", "Secur3-admin-password-new!", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateUser(context.Background(), admin.Principal, CreateManagedUserInput{
		Username: "observer.one", DisplayName: "Observer One", Role: RoleObserver, Password: "Secur3-observer-password!",
	})
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	observerLogin, err := service.Login(context.Background(), LoginInput{Username: "observer.one", Password: "Secur3-observer-password!", SourceIP: "127.0.0.2"})
	if err != nil {
		t.Fatalf("observer Login() error = %v", err)
	}
	if observerLogin.Principal.Has(PermissionResourceWrite) {
		t.Fatal("observer unexpectedly had resource.write")
	}
	_, err = service.ReplaceEntitlements(context.Background(), admin.Principal, created.User.ID, []Entitlement{
		{Permission: PermissionResourceWrite, Effect: "allow"},
		{Permission: PermissionWorkflowRunRead, Effect: "deny"},
	})
	if err != nil {
		t.Fatalf("ReplaceEntitlements() error = %v", err)
	}
	refreshed, _, err := service.AuthenticateSession(context.Background(), observerLogin.Token)
	if err != nil {
		t.Fatalf("AuthenticateSession() error = %v", err)
	}
	if !refreshed.Has(PermissionResourceWrite) || refreshed.Has(PermissionWorkflowRunRead) {
		t.Fatalf("refreshed permissions = %#v", refreshed.Permissions.Slice())
	}

	_, err = service.UpdateUser(context.Background(), admin.Principal, admin.Principal.ID, UpdateManagedUserInput{
		Username: "admin", DisplayName: "Administrator", Role: RoleObserver, Status: "active",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("final administrator demotion error = %v, want conflict", err)
	}
}

func TestAPIKeyWorkflowAuthorizationAndRevocation(t *testing.T) {
	service, _ := newTestService(t)
	bootstrap := bootstrapAdmin(t, service)
	admin, err := service.ChangePassword(context.Background(), bootstrap.Principal, "Secur3-bootstrap-password!", "Secur3-admin-password-new!", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	created, err := service.CreateAPIKey(context.Background(), admin.Principal, CreateAPIKeyRequest{
		Name: "webhook client", ExpiresAt: &expires,
		Grants: []APIKeyGrant{
			{WorkflowDefinitionID: "orders", Action: "start", InstanceScope: "own"},
			{WorkflowDefinitionID: "orders", Action: "signal", InstanceScope: "own", SignalNames: []string{"approved"}},
			{WorkflowDefinitionID: "orders", Action: "result.read", InstanceScope: "own"},
		},
	})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	principal, key, err := service.AuthenticateAPIKey(context.Background(), created.Secret, "192.0.2.10:443")
	if err != nil || principal.Type != PrincipalAPIKey {
		t.Fatalf("AuthenticateAPIKey() principal = %#v, error = %v", principal, err)
	}
	if _, err := AuthorizeAPIKey(key, APIKeyAuthorizationInput{DefinitionID: "orders", Action: "start"}); err != nil {
		t.Fatalf("start authorization error = %v", err)
	}
	if _, err := AuthorizeAPIKey(key, APIKeyAuthorizationInput{DefinitionID: "orders", Action: "start", PinnedVersion: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("pinned start authorization error = %v, want forbidden", err)
	}
	if _, err := AuthorizeAPIKey(key, APIKeyAuthorizationInput{DefinitionID: "orders", Action: "signal", SignalName: "approved", WorkflowTriggerType: string(PrincipalAPIKey), WorkflowTriggerID: key.ID}); err != nil {
		t.Fatalf("owned signal authorization error = %v", err)
	}
	if _, err := AuthorizeAPIKey(key, APIKeyAuthorizationInput{DefinitionID: "orders", Action: "signal", SignalName: "rejected", WorkflowTriggerType: string(PrincipalAPIKey), WorkflowTriggerID: key.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("restricted signal authorization error = %v, want forbidden", err)
	}
	if _, err := AuthorizeAPIKey(key, APIKeyAuthorizationInput{DefinitionID: "orders", Action: "result.read", WorkflowTriggerType: string(PrincipalAPIKey), WorkflowTriggerID: "another-key"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("foreign result authorization error = %v, want forbidden", err)
	}
	if err := service.RevokeAPIKey(context.Background(), admin.Principal, key.ID); err != nil {
		t.Fatalf("RevokeAPIKey() error = %v", err)
	}
	if _, _, err := service.AuthenticateAPIKey(context.Background(), created.Secret, "192.0.2.10"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked key authentication error = %v, want invalid credentials", err)
	}
}
