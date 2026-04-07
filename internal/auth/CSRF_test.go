package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/sessions"
)

func TestRequireCSRF_AllowsValidToken(t *testing.T) {
	t.Parallel()

	store := sessions.NewCookieStore(GenerateRandomKey(32), GenerateRandomKey(32))

	getReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	getRec := httptest.NewRecorder()
	token, err := EnsureCSRFToken(getRec, getReq, store)
	if err != nil {
		t.Fatalf("EnsureCSRFToken failed: %v", err)
	}

	postReq := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader("email=a%40b.com"))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("X-CSRF-Token", token)
	for _, c := range getRec.Result().Cookies() {
		postReq.AddCookie(c)
	}
	postRec := httptest.NewRecorder()

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	})
	RequireCSRF(next, store).ServeHTTP(postRec, postReq)

	if !nextCalled {
		t.Fatal("expected next handler to be called")
	}
	if postRec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, postRec.Code)
	}
}

func TestRequireCSRF_RejectsMissingToken(t *testing.T) {
	t.Parallel()

	store := sessions.NewCookieStore(GenerateRandomKey(32), GenerateRandomKey(32))

	getReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	getRec := httptest.NewRecorder()
	_, err := EnsureCSRFToken(getRec, getReq, store)
	if err != nil {
		t.Fatalf("EnsureCSRFToken failed: %v", err)
	}

	form := url.Values{}
	form.Set("email", "a@b.com")
	postReq := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range getRec.Result().Cookies() {
		postReq.AddCookie(c)
	}
	postRec := httptest.NewRecorder()

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	RequireCSRF(next, store).ServeHTTP(postRec, postReq)

	if postRec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, postRec.Code)
	}
}
