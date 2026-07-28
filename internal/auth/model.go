package auth

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrNotFound           = errors.New("authentication resource not found")
	ErrForbidden          = errors.New("permission denied")
	ErrConflict           = errors.New("authentication resource conflict")
)

type Role string

const (
	RoleAdmin     Role = "admin"
	RoleDeveloper Role = "developer"
	RoleObserver  Role = "observer"
)

type Permission string

const (
	PermissionDashboardRead             Permission = "dashboard.read"
	PermissionWorkflowDefinitionRead    Permission = "workflow.definition.read"
	PermissionWorkflowDefinitionWrite   Permission = "workflow.definition.write"
	PermissionWorkflowDefinitionPublish Permission = "workflow.definition.publish"
	PermissionWorkflowRunRead           Permission = "workflow.run.read"
	PermissionWorkflowRunStart          Permission = "workflow.run.start"
	PermissionWorkflowRunControl        Permission = "workflow.run.control"
	PermissionWorkflowTaskRead          Permission = "workflow.task.read"
	PermissionWorkflowTaskControl       Permission = "workflow.task.control"
	PermissionResourceRead              Permission = "resource.read"
	PermissionResourceWrite             Permission = "resource.write"
	PermissionAIUse                     Permission = "ai.use"
	PermissionImportAnalyze             Permission = "import.analyze"
	PermissionImportApply               Permission = "import.apply"
	PermissionOperationRead             Permission = "operation.read"
	PermissionClusterRead               Permission = "cluster.read"
	PermissionClusterControl            Permission = "cluster.control"
	PermissionSettingsRead              Permission = "settings.read"
	PermissionSettingsWrite             Permission = "settings.write"
	PermissionServerRestart             Permission = "server.restart"
	PermissionUserRead                  Permission = "user.read"
	PermissionUserManage                Permission = "user.manage"
	PermissionEntitlementManage         Permission = "entitlement.manage"
	PermissionAPIKeyRead                Permission = "api_key.read"
	PermissionAPIKeyCreate              Permission = "api_key.create"
	PermissionAPIKeyManageOwn           Permission = "api_key.manage_own"
	PermissionAPIKeyManageAll           Permission = "api_key.manage_all"
	PermissionAuditRead                 Permission = "audit.read"
	PermissionSessionManageOwn          Permission = "session.manage_own"
	PermissionSessionManageAll          Permission = "session.manage_all"
)

type PermissionSet map[Permission]struct{}

func (s PermissionSet) Has(permission Permission) bool {
	_, ok := s[permission]
	return ok
}

func (s PermissionSet) Slice() []Permission {
	result := make([]Permission, 0, len(s))
	for permission := range s {
		result = append(result, permission)
	}
	SortPermissions(result)
	return result
}

type User struct {
	ID                 string        `json:"id"`
	Username           string        `json:"username"`
	DisplayName        string        `json:"displayName"`
	Role               Role          `json:"role"`
	Status             string        `json:"status"`
	MustChangePassword bool          `json:"mustChangePassword"`
	FailedLoginCount   int           `json:"failedLoginCount,omitempty"`
	LockedUntil        *time.Time    `json:"lockedUntil,omitempty"`
	PasswordChangedAt  time.Time     `json:"passwordChangedAt"`
	LastLoginAt        *time.Time    `json:"lastLoginAt,omitempty"`
	AuthzVersion       int64         `json:"authzVersion"`
	CreatedBy          string        `json:"createdBy,omitempty"`
	CreatedAt          time.Time     `json:"createdAt"`
	UpdatedAt          time.Time     `json:"updatedAt"`
	Entitlements       []Entitlement `json:"entitlements,omitempty"`
}

type userRecord struct {
	User
	UsernameNormalized string
	PasswordHash       string
}

type Entitlement struct {
	Permission Permission `json:"permission"`
	Effect     string     `json:"effect"`
	CreatedBy  string     `json:"createdBy,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

type Session struct {
	ID                     string     `json:"id"`
	UserID                 string     `json:"userId"`
	CSRFToken              string     `json:"-"`
	CreatedAt              time.Time  `json:"createdAt"`
	LastSeenAt             time.Time  `json:"lastSeenAt"`
	IdleExpiresAt          time.Time  `json:"idleExpiresAt"`
	AbsoluteExpiresAt      time.Time  `json:"absoluteExpiresAt"`
	PasswordChangedAtLogin time.Time  `json:"-"`
	AuthzVersionAtLogin    int64      `json:"-"`
	RevokedAt              *time.Time `json:"revokedAt,omitempty"`
	RevokeReason           string     `json:"revokeReason,omitempty"`
	SourceIP               string     `json:"sourceIp,omitempty"`
	UserAgentHash          string     `json:"userAgentHash,omitempty"`
}

type PrincipalType string

const (
	PrincipalUser      PrincipalType = "user"
	PrincipalAPIKey    PrincipalType = "api_key"
	PrincipalSystem    PrincipalType = "system"
	PrincipalAnonymous PrincipalType = "anonymous"
)

type Principal struct {
	Type        PrincipalType
	ID          string
	DisplayName string
	Permissions PermissionSet
	SessionID   string
	APIKeyID    string
	User        *User
	CSRFToken   string
}

func (p Principal) Has(permission Permission) bool {
	return p.Permissions.Has(permission)
}

type principalContextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

type APIKey struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Description     string        `json:"description"`
	KeyPrefix       string        `json:"keyPrefix"`
	CreatedByUserID string        `json:"createdByUserId"`
	Status          string        `json:"status"`
	ExpiresAt       *time.Time    `json:"expiresAt,omitempty"`
	LastUsedAt      *time.Time    `json:"lastUsedAt,omitempty"`
	LastUsedIP      string        `json:"lastUsedIp,omitempty"`
	CreatedAt       time.Time     `json:"createdAt"`
	UpdatedAt       time.Time     `json:"updatedAt"`
	RevokedAt       *time.Time    `json:"revokedAt,omitempty"`
	RevokedBy       string        `json:"revokedBy,omitempty"`
	RotatedFromID   string        `json:"rotatedFromId,omitempty"`
	Grants          []APIKeyGrant `json:"grants"`
}

type apiKeyRecord struct {
	APIKey
	SecretHash string
}

type APIKeyGrant struct {
	WorkflowDefinitionID string    `json:"workflowDefinitionId"`
	Action               string    `json:"action"`
	InstanceScope        string    `json:"instanceScope"`
	AllowPinnedVersions  bool      `json:"allowPinnedVersions"`
	AllowCallbackURL     bool      `json:"allowCallbackUrl"`
	SignalNames          []string  `json:"signalNames,omitempty"`
	CreatedAt            time.Time `json:"createdAt"`
}

type AuditEvent struct {
	ID           int64           `json:"id"`
	OccurredAt   time.Time       `json:"occurredAt"`
	RequestID    string          `json:"requestId,omitempty"`
	ActorType    PrincipalType   `json:"actorType"`
	ActorID      string          `json:"actorId,omitempty"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resourceType,omitempty"`
	ResourceID   string          `json:"resourceId,omitempty"`
	Outcome      string          `json:"outcome"`
	SourceIP     string          `json:"sourceIp,omitempty"`
	UserAgent    string          `json:"userAgent,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}
