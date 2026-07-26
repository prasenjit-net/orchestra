package auth

import "testing"

func TestRoleAndEntitlementPermissions(t *testing.T) {
	observer := EffectivePermissions(RoleObserver, "active", nil)
	if !observer.Has(PermissionWorkflowRunRead) || observer.Has(PermissionWorkflowRunStart) {
		t.Fatalf("observer permissions are not read-only: %#v", observer.Slice())
	}

	effective := EffectivePermissions(RoleObserver, "active", []Entitlement{
		{Permission: PermissionResourceWrite, Effect: "allow"},
		{Permission: PermissionWorkflowRunRead, Effect: "deny"},
	})
	if !effective.Has(PermissionResourceWrite) {
		t.Fatal("allow entitlement was not applied")
	}
	if effective.Has(PermissionWorkflowRunRead) {
		t.Fatal("deny entitlement did not override role permission")
	}
	if got := EffectivePermissions(RoleAdmin, "disabled", nil); len(got) != 0 {
		t.Fatalf("disabled user retained permissions: %#v", got.Slice())
	}
}

func TestValidateEntitlements(t *testing.T) {
	err := ValidateEntitlements([]Entitlement{
		{Permission: PermissionResourceRead, Effect: "allow"},
		{Permission: PermissionResourceRead, Effect: "deny"},
	})
	if err == nil {
		t.Fatal("duplicate entitlement unexpectedly validated")
	}
	if err := ValidateEntitlements([]Entitlement{{Permission: "made.up", Effect: "allow"}}); err == nil {
		t.Fatal("unknown entitlement unexpectedly validated")
	}
}
