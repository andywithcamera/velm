package auth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/sessions"
)

func RequireAuth(next http.Handler, store *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := store.Get(r, "mysession")
		if err != nil {
			redirectToLogin(w, r, store)
			return
		}

		if auth, ok := session.Values["authenticated"].(bool); !ok || !auth {
			redirectToLogin(w, r, store)
			return
		}

		userID, _ := session.Values["user_id"].(string)
		if userID == "" {
			session.Options.MaxAge = -1
			_ = session.Save(r, w)
			redirectToLogin(w, r, store)
			return
		}

		userEmail, _ := session.Values["user_email"].(string)
		userName, _ := session.Values["user_name"].(string)
		userRole, _ := session.Values["user_role"].(string)
		if userRole == "" {
			userRole = "unknown"
		}

		ctx := WithUserContext(r.Context(), userID, userEmail, userName, userRole)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func redirectToLogin(w http.ResponseWriter, r *http.Request, store *sessions.CookieStore) {
	if store != nil {
		clearSessionCookie(w, store.Options)
	}

	loginTarget := "/login"
	if next := loginRedirectTarget(r); next != "" {
		loginTarget = "/login?next=" + url.QueryEscape(next)
	}

	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", loginTarget)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, loginTarget, http.StatusFound)
}

func loginRedirectTarget(r *http.Request) string {
	if r == nil {
		return ""
	}

	if strings.TrimSpace(r.Header.Get("HX-Request")) != "" {
		if target := sanitizeLoginRedirectTarget(strings.TrimSpace(r.Header.Get("HX-Current-URL"))); target != "" {
			return target
		}
		if target := sanitizeLoginRedirectTarget(strings.TrimSpace(r.Header.Get("Referer"))); target != "" {
			return target
		}
		return ""
	}

	if r.URL == nil {
		return ""
	}
	return sanitizeLoginRedirectTarget(r.URL.RequestURI())
}

func LoginRedirectTarget(r *http.Request) string {
	return loginRedirectTarget(r)
}

func sanitizeLoginRedirectTarget(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	target := raw
	if !strings.HasPrefix(target, "/") {
		parsed, err := url.Parse(target)
		if err != nil {
			return ""
		}
		target = parsed.EscapedPath()
		if parsed.RawQuery != "" {
			target += "?" + parsed.RawQuery
		}
	}

	if target == "" || target == "/login" || strings.HasPrefix(target, "/login?") {
		return ""
	}
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") {
		return ""
	}
	return target
}

func clearSessionCookie(w http.ResponseWriter, opts *sessions.Options) {
	cookie := &http.Cookie{
		Name:     "mysession",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
	}

	if opts != nil {
		if opts.Path != "" {
			cookie.Path = opts.Path
		}
		cookie.Domain = opts.Domain
		cookie.HttpOnly = opts.HttpOnly
		cookie.SameSite = opts.SameSite
		cookie.Partitioned = opts.Partitioned
	}

	http.SetCookie(w, cookie)
}
