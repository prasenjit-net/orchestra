package auth

import (
	"fmt"
	"slices"
	"strings"
)

var permissionCatalog = []Permission{
	PermissionDashboardRead,
	PermissionWorkflowDefinitionRead,
	PermissionWorkflowDefinitionWrite,
	PermissionWorkflowDefinitionPublish,
	PermissionWorkflowRunRead,
	PermissionWorkflowRunStart,
	PermissionWorkflowRunControl,
	PermissionWorkflowTaskRead,
	PermissionWorkflowTaskControl,
	PermissionResourceRead,
	PermissionResourceWrite,
	PermissionAIUse,
	PermissionImportAnalyze,
	PermissionImportApply,
	PermissionOperationRead,
	PermissionClusterRead,
	PermissionClusterControl,
	PermissionSettingsRead,
	PermissionSettingsWrite,
	PermissionServerRestart,
	PermissionUserRead,
	PermissionUserManage,
	PermissionEntitlementManage,
	PermissionAPIKeyRead,
	PermissionAPIKeyCreate,
	PermissionAPIKeyManageOwn,
	PermissionAPIKeyManageAll,
	PermissionAuditRead,
	PermissionSessionManageOwn,
	PermissionSessionManageAll,
}

var rolePermissionCatalog = map[Role][]Permission{
	RoleAdmin: append([]Permission(nil), permissionCatalog...),
	RoleDeveloper: {
		PermissionDashboardRead,
		PermissionWorkflowDefinitionRead,
		PermissionWorkflowDefinitionWrite,
		PermissionWorkflowDefinitionPublish,
		PermissionWorkflowRunRead,
		PermissionWorkflowRunStart,
		PermissionWorkflowRunControl,
		PermissionWorkflowTaskRead,
		PermissionWorkflowTaskControl,
		PermissionResourceRead,
		PermissionResourceWrite,
		PermissionAIUse,
		PermissionImportAnalyze,
		PermissionImportApply,
		PermissionOperationRead,
		PermissionClusterRead,
		PermissionClusterControl,
		PermissionSettingsRead,
		PermissionAPIKeyRead,
		PermissionAPIKeyCreate,
		PermissionAPIKeyManageOwn,
		PermissionSessionManageOwn,
	},
	RoleObserver: {
		PermissionDashboardRead,
		PermissionWorkflowDefinitionRead,
		PermissionWorkflowRunRead,
		PermissionWorkflowTaskRead,
		PermissionResourceRead,
		PermissionOperationRead,
		PermissionClusterRead,
		PermissionSettingsRead,
		PermissionSessionManageOwn,
	},
}

func AllPermissions() []Permission {
	result := append([]Permission(nil), permissionCatalog...)
	SortPermissions(result)
	return result
}

func SortPermissions(permissions []Permission) {
	slices.SortFunc(permissions, func(a, b Permission) int {
		return strings.Compare(string(a), string(b))
	})
}

func ValidPermission(permission Permission) bool {
	return slices.Contains(permissionCatalog, permission)
}

func ValidRole(role Role) bool {
	_, ok := rolePermissionCatalog[role]
	return ok
}

func PermissionsForRole(role Role) PermissionSet {
	set := make(PermissionSet)
	for _, permission := range rolePermissionCatalog[role] {
		set[permission] = struct{}{}
	}
	return set
}

func EffectivePermissions(role Role, status string, entitlements []Entitlement) PermissionSet {
	if status != "active" {
		return PermissionSet{}
	}
	set := PermissionsForRole(role)
	for _, entitlement := range entitlements {
		if entitlement.Effect == "allow" {
			set[entitlement.Permission] = struct{}{}
		}
	}
	for _, entitlement := range entitlements {
		if entitlement.Effect == "deny" {
			delete(set, entitlement.Permission)
		}
	}
	return set
}

func ValidateEntitlements(entitlements []Entitlement) error {
	seen := make(map[Permission]struct{}, len(entitlements))
	for _, entitlement := range entitlements {
		if !ValidPermission(entitlement.Permission) {
			return fmt.Errorf("unknown permission %q", entitlement.Permission)
		}
		if entitlement.Effect != "allow" && entitlement.Effect != "deny" {
			return fmt.Errorf("invalid entitlement effect %q", entitlement.Effect)
		}
		if _, ok := seen[entitlement.Permission]; ok {
			return fmt.Errorf("duplicate permission %q", entitlement.Permission)
		}
		seen[entitlement.Permission] = struct{}{}
	}
	return nil
}
