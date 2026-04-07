package main

import (
	"context"
	"net/http"
	"velm/internal/db"
	"strings"
)

func handleFailedLogin(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	_ = db.LogSecurityEvent(
		context.Background(),
		"login_failure",
		"warn",
		"",
		r.URL.Path,
		r.Method,
		r.RemoteAddr,
		r.UserAgent(),
		map[string]any{
			"email": strings.TrimSpace(strings.ToLower(r.FormValue("email"))),
		},
	)

	session, _ := store.Get(r, "mysession")
	session.Values["authenticated"] = false
	delete(session.Values, "user_id")
	delete(session.Values, "user_email")
	delete(session.Values, "user_name")
	delete(session.Values, "user_role")
	_ = session.Save(r, w)

	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}
