package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/sessions"
)

func TestRequireAuthRedirectsOnInvalidSessionCookie(t *testing.T) {
	t.Parallel()

	store := sessions.NewCookieStore(GenerateRandomKey(32), GenerateRandomKey(32))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
	}

	nextCalled := false
	handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}), store)

	req := httptest.NewRequest(http.MethodGet, "/t/_work", nil)
	req.Header.Set("Cookie", "mysession=this-is-not-a-valid-securecookie-value")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if nextCalled {
		t.Fatalf("next handler should not be called for invalid session cookies")
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if location := rec.Header().Get("Location"); location != "/login?next=%2Ft%2F_work" {
		t.Fatalf("Location = %q, want bounced login redirect", location)
	}
	if setCookie := rec.Header().Get("Set-Cookie"); !strings.Contains(setCookie, "mysession=") || !strings.Contains(setCookie, "Max-Age=0") {
		t.Fatalf("Set-Cookie = %q, want expired mysession cookie", setCookie)
	}
}

func TestRequireAuthRedirectIncludesNextTarget(t *testing.T) {
	t.Parallel()

	store := sessions.NewCookieStore(GenerateRandomKey(32), GenerateRandomKey(32))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
	}

	handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), store)

	req := httptest.NewRequest(http.MethodGet, "/admin/app-editor?app=itsm", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if location := rec.Header().Get("Location"); location != "/login?next=%2Fadmin%2Fapp-editor%3Fapp%3Ditsm" {
		t.Fatalf("Location = %q, want bounced login redirect", location)
	}
}

func TestRequireAuthRedirectUsesHTMXCurrentURL(t *testing.T) {
	t.Parallel()

	store := sessions.NewCookieStore(GenerateRandomKey(32), GenerateRandomKey(32))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
	}

	handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), store)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/panel?return_to=%2Fadmin%2Fapp-editor", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Current-URL", "http://localhost:3000/admin/app-editor?app=system")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if location := rec.Header().Get("HX-Redirect"); location != "/login?next=%2Fadmin%2Fapp-editor%3Fapp%3Dsystem" {
		t.Fatalf("HX-Redirect = %q, want visible page redirect", location)
	}
}

func TestRequireAuthRedirectUsesHTMXRefererFallback(t *testing.T) {
	t.Parallel()

	store := sessions.NewCookieStore(GenerateRandomKey(32), GenerateRandomKey(32))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
	}

	handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), store)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/panel?return_to=%2Fadmin%2Fapp-editor", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Referer", "http://localhost:3000/admin/app-editor?app=system")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if location := rec.Header().Get("HX-Redirect"); location != "/login?next=%2Fadmin%2Fapp-editor%3Fapp%3Dsystem" {
		t.Fatalf("HX-Redirect = %q, want visible page redirect", location)
	}
}

func TestRequireAuthRedirectDoesNotUseHTMXAPIRequestURI(t *testing.T) {
	t.Parallel()

	store := sessions.NewCookieStore(GenerateRandomKey(32), GenerateRandomKey(32))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
	}

	handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), store)

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/panel?return_to=%2Fadmin%2Fapp-editor", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if location := rec.Header().Get("HX-Redirect"); location != "/login" {
		t.Fatalf("HX-Redirect = %q, want /login", location)
	}
}
