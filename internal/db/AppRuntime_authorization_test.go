package db

import (
	"context"
	"testing"
)

func TestAppServiceMethodAccessAllowedDeniesWhenNoRolesDeclared(t *testing.T) {
	// The refusal happens before any role lookup, so no database is required.
	app := RegisteredApp{ID: "app-1", Name: "devworks", Namespace: "dw"}
	allowed, err := AppServiceMethodAccessAllowed(context.Background(), app, AppDefinitionMethod{
		Name:       "close_ticket",
		Visibility: "public",
	}, "user-1")
	if err != nil {
		t.Fatalf("AppServiceMethodAccessAllowed() error = %v", err)
	}
	if allowed {
		t.Fatal("a public method with no roles allowed an authenticated caller; the default must be deny")
	}
}

func TestAppServiceMethodAccessAllowedDeniesBlankRoleLists(t *testing.T) {
	app := RegisteredApp{ID: "app-1", Name: "devworks", Namespace: "dw"}
	allowed, err := AppServiceMethodAccessAllowed(context.Background(), app, AppDefinitionMethod{
		Name:       "close_ticket",
		Visibility: "public",
		Roles:      []string{" ", ""},
	}, "user-1")
	if err != nil {
		t.Fatalf("AppServiceMethodAccessAllowed() error = %v", err)
	}
	if allowed {
		t.Fatal("a role list of blank strings allowed an authenticated caller")
	}
}

func TestAppServiceMethodAccessAllowedAuthenticatedRoleAdmitsAnySignedInUser(t *testing.T) {
	app := RegisteredApp{ID: "app-1", Name: "devworks", Namespace: "dw"}
	method := AppDefinitionMethod{
		Name:       "ingest_event",
		Visibility: "public",
		Roles:      []string{AuthenticatedRole},
	}
	allowed, err := AppServiceMethodAccessAllowed(context.Background(), app, method, "user-1")
	if err != nil {
		t.Fatalf("AppServiceMethodAccessAllowed() error = %v", err)
	}
	if !allowed {
		t.Fatal("roles: [authenticated] did not admit a signed-in caller")
	}

	// An anonymous caller is still refused, which is the whole point of having a
	// token that means "signed in" rather than "open".
	allowed, err = AppServiceMethodAccessAllowed(context.Background(), app, method, "")
	if err != nil {
		t.Fatalf("AppServiceMethodAccessAllowed() error = %v", err)
	}
	if allowed {
		t.Fatal("roles: [authenticated] admitted a caller with no user id")
	}
}

func TestAppEndpointAccessAllowedDeniesWhenNoRolesDeclared(t *testing.T) {
	endpoint := AppRuntimeEndpoint{
		App:      RegisteredApp{ID: "app-1", Name: "devworks", Namespace: "dw"},
		Endpoint: AppDefinitionEndpoint{Name: "board_data", Enabled: true, Method: "post", Path: "/api/devworks/board-data", Call: "board.data"},
	}
	allowed, err := AppEndpointAccessAllowed(context.Background(), endpoint, "user-1")
	if err != nil {
		t.Fatalf("AppEndpointAccessAllowed() error = %v", err)
	}
	if allowed {
		t.Fatal("an enabled endpoint with no roles allowed an authenticated caller; the default must be deny")
	}

	endpoint.Endpoint.Roles = []string{AuthenticatedRole}
	allowed, err = AppEndpointAccessAllowed(context.Background(), endpoint, "user-1")
	if err != nil {
		t.Fatalf("AppEndpointAccessAllowed() error = %v", err)
	}
	if !allowed {
		t.Fatal("roles: [authenticated] did not admit a signed-in caller")
	}
}

func TestRolesGrantAnyAuthenticatedUser(t *testing.T) {
	cases := []struct {
		roles []string
		want  bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"admin"}, false},
		{[]string{AuthenticatedRole}, true},
		{[]string{" " + AuthenticatedRole + " "}, true},
		{[]string{"AUTHENTICATED"}, true},
		{[]string{"admin", AuthenticatedRole}, true},
	}
	for _, testCase := range cases {
		if got := rolesGrantAnyAuthenticatedUser(testCase.roles); got != testCase.want {
			t.Errorf("rolesGrantAnyAuthenticatedUser(%q) = %v, want %v", testCase.roles, got, testCase.want)
		}
	}
}
