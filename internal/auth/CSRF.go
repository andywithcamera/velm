package auth

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gorilla/sessions"
)

func EnsureCSRFToken(w http.ResponseWriter, r *http.Request, store *sessions.CookieStore) (string, error) {
	session, err := getOrResetSession(w, r, store)
	if err != nil {
		return "", err
	}

	token, _ := session.Values["csrf_token"].(string)
	if token != "" {
		return token, nil
	}

	token = base64.StdEncoding.EncodeToString(GenerateRandomKey(32))
	session.Values["csrf_token"] = token
	if err := session.Save(r, w); err != nil {
		return "", err
	}
	return token, nil
}

func RequireCSRF(next http.Handler, store *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresCSRFCheck(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		session, err := getOrResetSession(w, r, store)
		if err != nil {
			http.Error(w, "Invalid session", http.StatusForbidden)
			return
		}

		expectedToken, _ := session.Values["csrf_token"].(string)
		if expectedToken == "" {
			http.Error(w, "Missing CSRF token", http.StatusForbidden)
			return
		}

		providedToken := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
		if providedToken == "" {
			if err := r.ParseForm(); err == nil {
				providedToken = strings.TrimSpace(r.FormValue("csrf_token"))
			}
		}

		if subtle.ConstantTimeCompare([]byte(providedToken), []byte(expectedToken)) != 1 {
			http.Error(w, "CSRF validation failed", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func getOrResetSession(w http.ResponseWriter, r *http.Request, store *sessions.CookieStore) (*sessions.Session, error) {
	session, err := store.Get(r, "mysession")
	if err == nil {
		return session, nil
	}

	// Clear unreadable/invalid cookie and start a fresh session so login can recover.
	stale, newErr := store.New(r, "mysession")
	if newErr == nil {
		stale.Options.MaxAge = -1
		_ = stale.Save(r, w)
	}
	return store.New(r, "mysession")
}

func requiresCSRFCheck(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
