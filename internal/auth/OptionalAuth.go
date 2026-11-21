package auth

import (
	"net/http"

	"github.com/gorilla/sessions"
)

// OptionalAuth attaches the session user to the request context when the
// request carries a valid session, and passes the request through untouched
// when it does not. It never redirects and never refuses.
//
// It exists for routes that serve two audiences from one URL: a signed-in user
// sees their normal view, while a visitor without a session sees whatever the
// handler decides is public (public docs, a public landing page). Those routes
// must therefore do their own authorization in the handler — OptionalAuth is
// authentication only, and grants nothing.
//
// A tampered or expired cookie is treated as no cookie: the visitor is
// anonymous, not redirected, because /docs must stay readable when a session
// has expired.
func OptionalAuth(next http.Handler, store *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			next.ServeHTTP(w, r)
			return
		}
		session, err := store.Get(r, "mysession")
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		authenticated, _ := session.Values["authenticated"].(bool)
		if !authenticated {
			next.ServeHTTP(w, r)
			return
		}
		userID, _ := session.Values["user_id"].(string)
		if userID == "" {
			next.ServeHTTP(w, r)
			return
		}
		userEmail, _ := session.Values["user_email"].(string)
		userName, _ := session.Values["user_name"].(string)
		userRole, _ := session.Values["user_role"].(string)
		if userRole == "" {
			userRole = "unknown"
		}
		next.ServeHTTP(w, r.WithContext(WithUserContext(r.Context(), userID, userEmail, userName, userRole)))
	})
}
