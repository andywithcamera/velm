package main

import (
	"net/http/httptest"
	"testing"
)

func TestHandleFormGraphMethodGuard(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/f/graph/base_entity/00000000-0000-0000-0000-000000000000", nil)
	handleFormGraph(rr, req)
	if rr.Code != 405 {
		t.Fatalf("expected 405 for POST, got %d", rr.Code)
	}
}

func TestHandleFormGraphBadPath(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/f/graph/base_entity", nil)
	handleFormGraph(rr, req)
	if rr.Code != 404 {
		t.Fatalf("expected 404 for short path, got %d", rr.Code)
	}
}
