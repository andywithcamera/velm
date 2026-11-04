package db

import (
	"testing"
)

func TestLegacyScriptMethodRolesDoesNotLeavePublicMethodWithoutRoles(t *testing.T) {
	// A legacy script with no roles migrated to a public method with no roles,
	// which used to mean "any signed-in user" and now means "nobody". The
	// migration has to carry the old meaning across as an explicit role.
	roles := legacyScriptMethodRoles(AppDefinitionScript{
		Name:     "close_ticket",
		Enabled:  true,
		Language: "javascript",
		Script:   "function run(ctx) { return { ok: true }; }",
	}, true)
	if len(roles) != 1 || roles[0] != AuthenticatedRole {
		t.Fatalf("expected the migrated method to declare [%q], got %v", AuthenticatedRole, roles)
	}
}

func TestLegacyScriptMethodRolesKeepsDeclaredRoles(t *testing.T) {
	roles := legacyScriptMethodRoles(AppDefinitionScript{
		Name:     "close_ticket",
		Enabled:  true,
		Roles:    []string{" Agent ", "agent"},
		Language: "javascript",
		Script:   "function run(ctx) { return { ok: true }; }",
	}, true)
	if len(roles) != 1 || roles[0] != "agent" {
		t.Fatalf("expected the declared role to survive migration, got %v", roles)
	}
}

func TestLegacyScriptMethodRolesIgnoresBoundAndDisabledScripts(t *testing.T) {
	disabled := AppDefinitionScript{
		Name:     "close_ticket",
		Enabled:  false,
		Language: "javascript",
		Script:   "function run(ctx) { return { ok: true }; }",
	}
	if roles := legacyScriptMethodRoles(disabled, false); len(roles) != 0 {
		t.Fatalf("a disabled script should not declare roles, got %v", roles)
	}

	endpointBound := AppDefinitionScript{
		Name:     "close_ticket",
		Enabled:  true,
		Language: "javascript",
		Script:   "function run(ctx) { return { ok: true }; }",
		Endpoint: AppDefinitionScriptEndpoint{Enabled: true, Method: "post", Path: "/api/x/close"},
	}
	if roles := legacyScriptMethodRoles(endpointBound, true); len(roles) != 0 {
		t.Fatalf("a script bound to an endpoint should not declare method roles, got %v", roles)
	}
}

func TestMigrateLegacyScriptsIntoDefinitionGatesPublicMethodAndEndpoint(t *testing.T) {
	definition := &AppDefinition{Name: "devworks", Namespace: "dw"}

	migrateLegacyScriptsIntoDefinition(definition, []AppDefinitionScript{
		{
			Name:     "board_data",
			Enabled:  true,
			Language: "javascript",
			Script:   "function run(ctx) { return { ok: true }; }",
			Endpoint: AppDefinitionScriptEndpoint{Enabled: true, Method: "post", Path: "/api/devworks/board-data"},
		},
		{
			Name:     "close_ticket",
			Enabled:  true,
			Language: "javascript",
			Script:   "function run(ctx) { return { ok: true }; }",
		},
	})

	if len(definition.Endpoints) != 1 {
		t.Fatalf("expected one endpoint, got %#v", definition.Endpoints)
	}
	if got := definition.Endpoints[0].Roles; len(got) != 1 || got[0] != AuthenticatedRole {
		t.Fatalf("expected the migrated endpoint to declare [%q], got %v", AuthenticatedRole, got)
	}

	if err := validateAppEntryPointRoleDeclarations(definition); err != nil {
		t.Fatalf("migrated definition should pass the new validation, got %v", err)
	}
}
