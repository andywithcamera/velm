package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/sessions"
)

// fakeTokenStore is an in-memory TokenStore so agent-bearer authN resolves
// without a database.
type fakeTokenStore struct {
	mu   sync.Mutex
	byID map[string]AgentToken
}

func newFakeTokenStore() *fakeTokenStore {
	return &fakeTokenStore{byID: map[string]AgentToken{}}
}

func (f *fakeTokenStore) CreateAgentToken(_ context.Context, userID, tokenHash, label, scopes string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sc []string
	if scopes != "" {
		sc = strings.Split(scopes, "|")
	}
	f.byID[tokenHash] = AgentToken{ID: tokenHash, UserID: userID, Label: label, Scopes: sc}
	return nil
}

func (f *fakeTokenStore) LookupAgentToken(_ context.Context, tokenHash string) (AgentToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok, ok := f.byID[tokenHash]
	if !ok {
		return AgentToken{}, ErrNoAgentToken
	}
	return tok, nil
}

func (f *fakeTokenStore) TouchAgentToken(_ context.Context, _ string) error { return nil }

func TestRequireAuthWithAgents_InvalidBearerRedirects(t *testing.T) {
	t.Parallel()

	store := sessions.NewCookieStore(GenerateRandomKey(32))
	nextCalled := false
	handler := RequireAuthWithAgents(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nextCalled = true; w.WriteHeader(http.StatusOK) }),
		store, newFakeTokenStore(), nil,
	)

	req := httptest.NewRequest(http.MethodGet, "/api/_work", nil)
	req.Header.Set("Authorization", "Bearer "+"not-an-issued-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatalf("next should NOT be called for an unissued bearer token")
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want redirect (Found)", rec.Code)
	}
}

func TestRequireAuthWithAgents_BearerResolvesSharedContext(t *testing.T) {
	t.Parallel()

	const wantUserID = "wgr5prcaenm3j5g"
	tokens := newFakeTokenStore()
	if err := IssueAgentToken(context.Background(), tokens, wantUserID, "ci-runner", "ci-secret-token", []string{"base_core:bootstrap"}); err != nil {
		t.Fatalf("issue token: %v", err)
	}

	store := sessions.NewCookieStore(GenerateRandomKey(32))
	var gotUserID string
	var gotIsAgent bool
	handler := RequireAuthWithAgents(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUserID = UserIDFromRequest(r)
			gotIsAgent = IsAgentRequest(r)
			w.WriteHeader(http.StatusOK)
		}),
		store, tokens, nil,
	)

	req := httptest.NewRequest(http.MethodGet, "/api/_work", nil)
	req.Header.Set("Authorization", "Bearer ci-secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if gotUserID != wantUserID {
		t.Fatalf("UserIDFromRequest = %q, want %q (shared context miss)", gotUserID, wantUserID)
	}
	if !gotIsAgent {
		t.Fatalf("IsAgentRequest = false, want true (agent marker missing)")
	}
}

func TestRequireAuthWithAgents_MissingAuthRedirects(t *testing.T) {
	t.Parallel()

	store := sessions.NewCookieStore(GenerateRandomKey(32))
	nextCalled := false
	handler := RequireAuthWithAgents(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nextCalled = true; w.WriteHeader(http.StatusOK) }),
		store, newFakeTokenStore(), nil,
	)

	req := httptest.NewRequest(http.MethodGet, "/api/_work", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatalf("next should NOT be called with no credentials")
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want redirect", rec.Code)
	}
}

func TestRequireAuthWithAgents_TypeMarkerIsNotAgentWithoutAgentCtx(t *testing.T) {
	t.Parallel()

	someCtx := WithUserContext(context.Background(), "u1", "e@x.io", "Edge", "view")
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(someCtx)

	if IsAgentRequest(req) {
		t.Fatalf("IsAgentRequest = true for a plain user context, want false")
	}
}
