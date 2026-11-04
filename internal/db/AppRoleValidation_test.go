package db

import (
	"strings"
	"testing"
)

func TestValidateAppEntryPointRoleDeclarationsRejectsPublicMethodWithoutRoles(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: devworks
namespace: dw
services:
  - name: board
    methods:
      - name: data
        visibility: public
        script: |
          function run(ctx) { return { ok: true }; }
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}

	err = validateAppEntryPointRoleDeclarations(definition)
	if err == nil {
		t.Fatal("expected a public method with no roles to fail validation")
	}
	if !strings.Contains(err.Error(), "board.data") && !strings.Contains(err.Error(), `method "data" on service "board"`) {
		t.Fatalf("error should name the method, got %q", err)
	}
	if !strings.Contains(err.Error(), AuthenticatedRole) {
		t.Fatalf("error should point at the %q role, got %q", AuthenticatedRole, err)
	}
}

func TestValidateAppEntryPointRoleDeclarationsAcceptsExplicitRoles(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: devworks
namespace: dw
roles:
  - name: agent
services:
  - name: board
    methods:
      - name: data
        visibility: public
        roles: [agent]
        script: |
          function run(ctx) { return { ok: true }; }
      - name: helper
        visibility: private
        script: |
          function run(ctx) { return { ok: true }; }
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}
	if err := validateAppEntryPointRoleDeclarations(definition); err != nil {
		t.Fatalf("validateAppEntryPointRoleDeclarations() error = %v", err)
	}
}

func TestValidateAppEntryPointRoleDeclarationsRejectsEnabledEndpointWithoutRoles(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: devworks
namespace: dw
services:
  - name: board
    methods:
      - name: data
        visibility: public
        roles: [authenticated]
        script: |
          function run(ctx) { return { ok: true }; }
endpoints:
  - name: board_data
    enabled: true
    method: post
    path: /api/devworks/board-data
    call: board.data
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}
	err = validateAppEntryPointRoleDeclarations(definition)
	if err == nil {
		t.Fatal("expected an enabled endpoint with no roles to fail validation")
	}
	if !strings.Contains(err.Error(), `endpoint "board_data"`) {
		t.Fatalf("error should name the endpoint, got %q", err)
	}

	definition.Endpoints[0].Roles = []string{AuthenticatedRole}
	if err := validateAppEntryPointRoleDeclarations(definition); err != nil {
		t.Fatalf("validateAppEntryPointRoleDeclarations() error = %v", err)
	}
}

func TestValidateAppEntryPointRoleDeclarationsIgnoresDisabledEndpoint(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: devworks
namespace: dw
services:
  - name: board
    methods:
      - name: data
        visibility: private
        script: |
          function run(ctx) { return { ok: true }; }
endpoints:
  - name: board_data
    enabled: false
    method: post
    path: /api/devworks/board-data
    call: board.data
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}
	if err := validateAppEntryPointRoleDeclarations(definition); err != nil {
		t.Fatalf("a disabled endpoint and a private method need no roles, got %v", err)
	}
}

func TestValidateIdentifierRolesAcceptsReservedAuthenticatedRole(t *testing.T) {
	roleSet := map[string]bool{"agent": true}
	if err := validateIdentifierRoles(roleSet, []string{AuthenticatedRole, "agent"}, "roles for endpoint \"x\""); err != nil {
		t.Fatalf("validateIdentifierRoles() error = %v", err)
	}
	if err := validateIdentifierRoles(roleSet, []string{"nobody"}, "roles for endpoint \"x\""); err == nil {
		t.Fatal("expected an unknown role to be rejected")
	}
}

func TestFindUngatedAppEntryPointsReportsBothDefinitions(t *testing.T) {
	published, err := ParseAppDefinition(`
name: devworks
namespace: dw
services:
  - name: board
    methods:
      - name: data
        visibility: public
        script: |
          function run(ctx) { return { ok: true }; }
endpoints:
  - name: board_data
    enabled: true
    method: post
    path: /api/devworks/board-data
    call: board.data
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}

	findings := findUngatedAppEntryPoints([]RegisteredApp{{
		Name:       "devworks",
		Namespace:  "dw",
		Definition: published,
	}})

	if len(findings) != 2 {
		t.Fatalf("expected the ungated method and endpoint, got %#v", findings)
	}
	if findings[0].Kind != "endpoint" || findings[1].Kind != "method" {
		t.Fatalf("expected one endpoint and one method finding, got %#v", findings)
	}
	if !findings[0].Published {
		t.Fatal("a finding from the published definition should be marked published")
	}
	if got := findings[1].String(); !strings.Contains(got, "board.data") {
		t.Fatalf("finding string should name the call, got %q", got)
	}
}
