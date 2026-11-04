package db

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The production failure: a stored definition whose public method predates the
// role requirement. Boot runs validating sync passes, so this used to be fatal.
func TestBackfillEntryPointRolesRepairsStoredPublicMethod(t *testing.T) {
	stored := `
name: base
namespace: ""
services:
  - name: observability
    methods:
      - name: ingest_event
        visibility: public
        script: |
          function run(ctx) { return { ok: true }; }
endpoints:
  - name: obs_ingest
    method: POST
    path: /obs/ingest
    call: observability.ingest_event
    enabled: true
`

	repaired, changed, err := backfillEntryPointRolesYAML(stored)
	if err != nil {
		t.Fatalf("backfillEntryPointRolesYAML() error = %v", err)
	}
	if !changed {
		t.Fatal("backfillEntryPointRolesYAML() reported no change, want the empty roles backfilled")
	}

	var definition AppDefinition
	if err := yaml.Unmarshal([]byte(repaired), &definition); err != nil {
		t.Fatalf("unmarshal repaired definition: %v", err)
	}
	if got := definition.Services[0].Methods[0].Roles; len(got) != 1 || got[0] != AuthenticatedRole {
		t.Fatalf("method roles = %v, want [%q]", got, AuthenticatedRole)
	}
	if got := definition.Endpoints[0].Roles; len(got) != 1 || got[0] != AuthenticatedRole {
		t.Fatalf("endpoint roles = %v, want [%q]", got, AuthenticatedRole)
	}

	// The repaired definition must now pass the validation that aborted boot.
	if err := validateAppEntryPointRoleDeclarations(&definition); err != nil {
		t.Fatalf("repaired definition still invalid: %v", err)
	}
}

func TestBackfillEntryPointRolesLeavesDeclaredRolesAlone(t *testing.T) {
	stored := `
name: devworks
namespace: dw
services:
  - name: board
    methods:
      - name: data
        visibility: public
        roles: [reader]
        script: |
          function run(ctx) { return { ok: true }; }
endpoints:
  - name: report
    method: GET
    path: /report
    call: board.data
    roles: [reader]
    enabled: true
`

	_, changed, err := backfillEntryPointRolesYAML(stored)
	if err != nil {
		t.Fatalf("backfillEntryPointRolesYAML() error = %v", err)
	}
	if changed {
		t.Fatal("backfillEntryPointRolesYAML() rewrote a definition that already declared roles")
	}
}

func TestBackfillEntryPointRolesSkipsPrivateAndDisabled(t *testing.T) {
	stored := `
name: devworks
namespace: dw
services:
  - name: board
    methods:
      - name: helper
        visibility: private
        script: |
          function run(ctx) { return { ok: true }; }
endpoints:
  - name: parked
    method: GET
    path: /parked
    call: board.helper
    enabled: false
`

	_, changed, err := backfillEntryPointRolesYAML(stored)
	if err != nil {
		t.Fatalf("backfillEntryPointRolesYAML() error = %v", err)
	}
	if changed {
		t.Fatal("backfillEntryPointRolesYAML() touched a private method or a disabled endpoint; roles are not consulted for either")
	}
}

func TestBackfillEntryPointRolesHandlesBlankEntries(t *testing.T) {
	stored := `
name: devworks
namespace: dw
services:
  - name: board
    methods:
      - name: data
        visibility: public
        roles: ["", "  "]
        script: |
          function run(ctx) { return { ok: true }; }
`

	repaired, changed, err := backfillEntryPointRolesYAML(stored)
	if err != nil {
		t.Fatalf("backfillEntryPointRolesYAML() error = %v", err)
	}
	if !changed {
		t.Fatal("a whitespace-only role list is empty as far as the runtime is concerned and should be backfilled")
	}
	if !strings.Contains(repaired, AuthenticatedRole) {
		t.Fatalf("repaired definition missing %q:\n%s", AuthenticatedRole, repaired)
	}
}

func TestBackfillEntryPointRolesIgnoresEmptyDefinition(t *testing.T) {
	out, changed, err := backfillEntryPointRolesYAML("   ")
	if err != nil {
		t.Fatalf("backfillEntryPointRolesYAML() error = %v", err)
	}
	if changed || out != "" {
		t.Fatalf("got (%q, %v), want (\"\", false)", out, changed)
	}
}

// The shipped base definition is what production was running; it must survive
// the validating sync passes unchanged now that it declares roles explicitly.
func TestShippedBaseObservabilityServicePassesValidation(t *testing.T) {
	service := baseObservabilityServiceDefinition()
	if len(service.Methods) == 0 {
		t.Fatal("shipped base observability service has no methods")
	}
	for _, method := range service.Methods {
		if method.Visibility != "public" {
			continue
		}
		if len(declaredRoles(method.Roles)) == 0 {
			t.Fatalf("method %q is public and declares no roles; boot validation would reject it", method.Name)
		}
	}
	endpoint := baseObservabilityEndpointDefinition()
	if endpoint.Enabled && len(declaredRoles(endpoint.Roles)) == 0 {
		t.Fatalf("endpoint %q is enabled and declares no roles; boot validation would reject it", endpoint.Name)
	}
}

// Pins the exact production message to an unrepaired stored definition, so the
// diagnosis is tied to the reported log line rather than inferred from it.
func TestStoredDefinitionWithoutRolesReproducesBootFailure(t *testing.T) {
	stored := `
name: base
services:
  - name: observability
    methods:
      - name: ingest_event
        visibility: public
        script: |
          function run(ctx) { return { ok: true }; }
`

	var definition AppDefinition
	if err := yaml.Unmarshal([]byte(stored), &definition); err != nil {
		t.Fatalf("unmarshal stored definition: %v", err)
	}

	err := validateAppEntryPointRoleDeclarations(&definition)
	if err == nil {
		t.Fatal("expected the stored definition to be rejected before backfill")
	}
	want := `method "ingest_event" on service "observability" is public and must declare roles`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err.Error(), want)
	}
}
