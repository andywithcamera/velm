package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"velm/internal/auth"
)

func TestAgentTokensCreateMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAgentTokensCreate(rec, httptest.NewRequest(http.MethodGet, "/api/agent-tokens", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

func TestAgentTokensCreateMintsHashableToken(t *testing.T) {
	rec := httptest.NewRecorder()
	body := `{"label":"velm machine"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agent-tokens", strings.NewReader(body))
	// Mint path does not touch the DB in this test: userID empty -> 401 first.
	handleAgentTokensCreate(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-identity status = %d, want 401", rec.Code)
	}

	// With an identity, generation must produce a well-formed raw token whose
	// hash round-trips through HashAgentToken (DB store is exercised by the
	// integration test in internal/db).
	persisted := [][2]string{}
	origPersist := persistAgentToken
	persistAgentToken = func(_ *http.Request, userID, tokenHash, label string) error {
		persisted = append(persisted, [2]string{userID, label})
		return nil
	}
	defer func() { persistAgentToken = origPersist }()
	rec2 := httptest.NewRecorder()
	mintAgentToken(rec2, httptest.NewRequest(http.MethodPost, "/api/agent-tokens", nil), "user-1", "velm machine")
	if rec2.Code != http.StatusCreated {
		t.Fatalf("mint status = %d, want 201: %s", rec2.Code, rec2.Body.String())
	}
	if len(persisted) != 1 || persisted[0][0] != "user-1" || persisted[0][1] != "velm machine" {
		t.Fatalf("persisted = %v", persisted)
	}
	var resp struct {
		Token string `json:"token"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp.Token, "velm_") || len(resp.Token) != len("velm_")+64 {
		t.Fatalf("token shape wrong: %q", resp.Token)
	}
	if resp.Label != "velm machine" {
		t.Fatalf("label = %q", resp.Label)
	}
	if auth.HashAgentToken(resp.Token) == "" {
		t.Fatal("hash empty")
	}
}

func TestAgentTokensCreateBadBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/agent-tokens", bytes.NewReader([]byte("{not json")))
	handleAgentTokensCreate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body status = %d, want 400", rec.Code)
	}
}
