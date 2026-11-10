package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMapRedirectFallsBackToEntityList(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/map", nil)
	handleMapRedirect(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("expected 303, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/t/base_entity" {
		t.Fatalf("expected /t/base_entity, got %q", loc)
	}
}

func TestMapRedirectRejectsNonUUID(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/map?entity=not-a-uuid", nil)
	handleMapRedirect(rr, req)
	if loc := rr.Header().Get("Location"); loc != "/t/base_entity" {
		t.Fatalf("expected fallback, got %q", loc)
	}
}
