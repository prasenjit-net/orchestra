package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/prasenjit-net/orchestra/internal/auth"
	"github.com/prasenjit-net/orchestra/internal/config"
	appdb "github.com/prasenjit-net/orchestra/internal/database"
	"github.com/prasenjit-net/orchestra/internal/livebus"
	"github.com/prasenjit-net/orchestra/internal/version"
	"github.com/prasenjit-net/orchestra/internal/workflow"
)

type authIntegrationSetup struct {
	cfg      config.Config
	identity *auth.Service
	workflow *workflow.Service
	admin    auth.SessionResult
	router   http.Handler
}

func newAuthIntegrationSetup(t *testing.T) authIntegrationSetup {
	t.Helper()
	cfg := config.Default()
	cfg.Workflow.DatabaseDriver = "sqlite"
	cfg.Workflow.DatabasePath = filepath.Join(t.TempDir(), "integration.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, dialect, err := appdb.Open(context.Background(), cfg.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	workflowService, err := workflow.NewServiceWithDB(cfg.Workflow, cfg.AI, logger, db, dialect)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(context.Background(), db, dialect, cfg.Auth, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_AUTH_INITIAL_ADMIN_PASSWORD", "Secur3-integration-bootstrap!")
	if _, err := identity.BootstrapInitialAdmin(context.Background()); err != nil {
		t.Fatal(err)
	}
	login, err := identity.Login(context.Background(), auth.LoginInput{Username: "admin", Password: "Secur3-integration-bootstrap!", SourceIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := identity.ChangePassword(context.Background(), login.Principal, "Secur3-integration-bootstrap!", "Secur3-integration-admin!", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	return authIntegrationSetup{
		cfg: cfg, identity: identity, workflow: workflowService, admin: admin,
		router: NewRouter(cfg, logger, version.Current(), livebus.New(), workflowService, identity, nil, false),
	}
}

func authorizeSession(req *http.Request, cfg config.Config, session auth.SessionResult, csrf bool) {
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.Token})
	if csrf {
		req.Header.Set("Origin", cfg.App.URL)
		req.Header.Set("X-CSRF-Token", session.Principal.CSRFToken)
	}
}

func TestProtectedRouterAuthenticationCSRFAndObserverRole(t *testing.T) {
	setup := newAuthIntegrationSetup(t)

	request := httptest.NewRequest(http.MethodGet, "/workflow-definitions", nil)
	response := httptest.NewRecorder()
	setup.router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/workflow-definitions", bytes.NewBufferString(`{"name":"blocked","steps":[]}`))
	authorizeSession(request, setup.cfg, setup.admin, false)
	response = httptest.NewRecorder()
	setup.router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !bytes.Contains(response.Body.Bytes(), []byte("AUTH_CSRF_INVALID")) {
		t.Fatalf("missing-CSRF response = %d %s", response.Code, response.Body.String())
	}

	created, err := setup.identity.CreateUser(context.Background(), setup.admin.Principal, auth.CreateManagedUserInput{
		Username: "observer.api", DisplayName: "Observer", Role: auth.RoleObserver, Password: "Secur3-observer-api!",
	})
	if err != nil {
		t.Fatal(err)
	}
	observerLogin, err := setup.identity.Login(context.Background(), auth.LoginInput{Username: created.User.Username, Password: "Secur3-observer-api!", SourceIP: "127.0.0.2"})
	if err != nil {
		t.Fatal(err)
	}
	observer, err := setup.identity.ChangePassword(context.Background(), observerLogin.Principal, "Secur3-observer-api!", "Secur3-observer-api-new!", "127.0.0.2", "test")
	if err != nil {
		t.Fatal(err)
	}

	request = httptest.NewRequest(http.MethodGet, "/workflow-definitions", nil)
	authorizeSession(request, setup.cfg, observer, false)
	response = httptest.NewRecorder()
	setup.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("observer read status = %d, want 200", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/workflow-definitions", bytes.NewBufferString(`{"name":"blocked","steps":[]}`))
	request.Header.Set("Content-Type", "application/json")
	authorizeSession(request, setup.cfg, observer, true)
	response = httptest.NewRecorder()
	setup.router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !bytes.Contains(response.Body.Bytes(), []byte("AUTH_FORBIDDEN")) {
		t.Fatalf("observer write response = %d %s", response.Code, response.Body.String())
	}
}

func TestExternalWebhookAPIKeyOwnership(t *testing.T) {
	setup := newAuthIntegrationSetup(t)
	definition, err := setup.workflow.CreateDefinition(context.Background(), workflow.CreateDefinitionInput{
		Name: "Authorized webhook", Steps: []workflow.StepDefinition{{Name: "wait", Activity: "wait-signal", Input: json.RawMessage(`{"signal":"approved"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	createKey := func(name string) auth.APIKeySecret {
		created, err := setup.identity.CreateAPIKey(context.Background(), setup.admin.Principal, auth.CreateAPIKeyRequest{
			Name: name,
			Grants: []auth.APIKeyGrant{
				{WorkflowDefinitionID: definition.ID, Action: "start", InstanceScope: "own"},
				{WorkflowDefinitionID: definition.ID, Action: "status.read", InstanceScope: "own"},
				{WorkflowDefinitionID: definition.ID, Action: "result.read", InstanceScope: "own"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	owner := createKey("owner")
	other := createKey("other")
	router, err := NewExtRouter(setup.cfg, setup.workflow, setup.identity)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/webhook/"+definition.ID+"/start", bytes.NewBufferString(`{"orderId":"123"}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing key status = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/webhook/"+definition.ID+"/start", bytes.NewBufferString(`{"orderId":"123"}`))
	request.Header.Set("Authorization", "Bearer "+owner.Secret)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("authorized start response = %d %s", response.Code, response.Body.String())
	}
	var started struct {
		WorkflowID string `json:"workflowId"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &started); err != nil || started.WorkflowID == "" {
		t.Fatalf("decode start response: %v, body=%s", err, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/signal/"+started.WorkflowID, nil)
	request.Header.Set("Authorization", "Bearer "+owner.Secret)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("owner status response = %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/result/"+started.WorkflowID, nil)
	request.Header.Set("Authorization", "Bearer "+other.Secret)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("foreign result status = %d, want 404; body=%s", response.Code, response.Body.String())
	}
}
