package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/sessions"
)

func optionalAuthStore(t *testing.T) *sessions.CookieStore {
	t.Helper()
	return sessions.NewCookieStore(
		[]byte("0123456789abcdef0123456789abcdef"),
		[]byte("0123456789abcdef0123456789abcdef"),
	)
}

// A request with no session must reach the handler, and the handler must see no
// user: that is the whole contract for public docs.
func TestOptionalAuthPassesAnonymousThrough(t *testing.T) {
	store := optionalAuthStore(t)

	var gotUser string
	handler := OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserIDFromRequest(r)
		w.WriteHeader(http.StatusOK)
	}), store)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/docs", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (OptionalAuth must not redirect)", recorder.Code)
	}
	if gotUser != "" {
		t.Fatalf("anonymous request carried user %q", gotUser)
	}
}

// A request carrying a valid session must arrive with the user attached, so the
// same URL serves the full view to a signed-in user.
func TestOptionalAuthAttachesSessionUser(t *testing.T) {
	store := optionalAuthStore(t)

	// Build a session cookie the way the login handler does, then send it back.
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	session, err := store.Get(req, "mysession")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	session.Values["authenticated"] = true
	session.Values["user_id"] = "user-1"
	session.Values["user_email"] = "u@example.com"
	session.Values["user_name"] = "User One"
	session.Values["user_role"] = "admin"
	if err := session.Save(req, recorder); err != nil {
		t.Fatalf("session.Save: %v", err)
	}

	authed := httptest.NewRequest(http.MethodGet, "/docs", nil)
	for _, cookie := range recorder.Result().Cookies() {
		authed.AddCookie(cookie)
	}

	var gotUser, gotRole string
	handler := OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserIDFromRequest(r)
		gotRole = UserRoleFromRequest(r)
	}), store)
	handler.ServeHTTP(httptest.NewRecorder(), authed)

	if gotUser != "user-1" {
		t.Fatalf("user id = %q, want user-1", gotUser)
	}
	if gotRole != "admin" {
		t.Fatalf("role = %q, want admin", gotRole)
	}
}

// A tampered cookie is no cookie: /docs must stay readable instead of erroring.
func TestOptionalAuthIgnoresInvalidSession(t *testing.T) {
	store := optionalAuthStore(t)

	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	req.AddCookie(&http.Cookie{Name: "mysession", Value: "not-a-valid-signed-value"})

	reached := false
	var gotUser string
	handler := OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		gotUser = UserIDFromRequest(r)
	}), store)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !reached {
		t.Fatal("invalid cookie blocked the request")
	}
	if gotUser != "" {
		t.Fatalf("invalid cookie produced user %q", gotUser)
	}
}

// A nil store must not panic: routes are registered before the store exists in
// some test setups.
func TestOptionalAuthToleratesNilStore(t *testing.T) {
	reached := false
	handler := OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}), nil)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/docs", nil))
	if !reached {
		t.Fatal("nil store blocked the request")
	}
}
