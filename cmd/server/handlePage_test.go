package main

import "testing"

func TestPageReadAccessAllowsOpenPages(t *testing.T) {
	result := pageReadAccess("Builder", "", false, true)
	if !result.Allowed {
		t.Fatalf("expected open page access, got %#v", result)
	}
}

func TestPageReadAccessRedirectsAnonymousDeniedPublicUsers(t *testing.T) {
	result := pageReadAccess("Public", "", true, false)
	if !result.ShowLogin {
		t.Fatalf("expected anonymous denied public access to show login, got %#v", result)
	}
}

func TestPageReadAccessHidesDeniedPublicPagesFromAuthenticatedUsers(t *testing.T) {
	result := pageReadAccess("Public", "user-1", true, false)
	if !result.NotFound {
		t.Fatalf("expected authenticated denied public access to be hidden, got %#v", result)
	}
}

func TestPageReadAccessForbidsDeniedBuilderPages(t *testing.T) {
	result := pageReadAccess("Builder", "user-1", true, false)
	if !result.Forbidden {
		t.Fatalf("expected denied builder page to be forbidden, got %#v", result)
	}
}
