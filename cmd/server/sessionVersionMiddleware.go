package main

import (
	"context"
	"net/http"
	"net/url"
	"velm/internal/auth"
	"velm/internal/db"
)

func requireFreshSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := auth.UserIDFromRequest(r)
		if userID == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Agent/API requests authenticate via scoped bearer tokens, not browser
		// cookies, so there is no session-freshness concept to enforce (issue
		// #68). Session-version checks apply to human session requests only.
		if auth.IsAgentRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		session, err := store.Get(r, "mysession")
		if err != nil {
			forceLogin(w, r)
			return
		}

		sessionVersionValue, ok := session.Values["session_version"]
		if !ok {
			forceLogin(w, r)
			return
		}
		sessionVersion, ok := sessionVersionFromValue(sessionVersionValue)
		if !ok {
			forceLogin(w, r)
			return
		}

		currentVersion, err := db.GetOrInitUserSessionVersion(context.Background(), userID)
		if err != nil {
			http.Error(w, "Session security check failed", http.StatusInternalServerError)
			return
		}
		if sessionVersion != currentVersion {
			forceLogin(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func sessionVersionFromValue(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

func forceLogin(w http.ResponseWriter, r *http.Request) {
	session, _ := store.Get(r, "mysession")
	session.Options.MaxAge = -1
	_ = session.Save(r, w)

	loginTarget := "/login"
	if next := auth.LoginRedirectTarget(r); next != "" {
		loginTarget = "/login?next=" + url.QueryEscape(next)
	}

	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", loginTarget)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, loginTarget, http.StatusFound)
}
