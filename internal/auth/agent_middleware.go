package auth

import (
	"context"
	"net/http"

	"github.com/gorilla/sessions"
)

// IdentityResolver turns a user_id into the name/email shown in the shared
// request context. Agents are plain _user rows, so this is the same lookup the
// session path uses — we only need it for the bearer case because a bearer
// token carries no name/email envelope.
type IdentityResolver interface {
	LookupUserIdentity(ctx context.Context, userID string) (email, name string, err error)
}

// RequireAuthWithAgents is RequireAuth plus the bearer-token path (issue #68).
// A request authenticates if EITHER a valid human session exists OR a valid
// agent bearer token is presented. Both resolve to the same context
// populated by WithUserContext, so authorization (roles/org membership) is
// identical and UserIDFromRequest(r) works the same for both.
func RequireAuthWithAgents(next http.Handler, store *sessions.CookieStore, tokens TokenStore, identity IdentityResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if agentCtx, ok := resolveSessionOrAgent(r, store, tokens, identity); ok {
			next.ServeHTTP(w, r.WithContext(agentCtx))
			return
		}
		redirectToLogin(w, r, store)
	})
}

// resolveSessionOrAgent returns a populated request context if the request
// authenticated (session or bearer), else (nil, false).
func resolveSessionOrAgent(r *http.Request, store *sessions.CookieStore, tokens TokenStore, identity IdentityResolver) (context.Context, bool) {
	if token := ParseBearer(r.Header.Get("Authorization")); token != "" && tokens != nil {
		return resolveAgent(r, tokens, identity, token)
	}
	return resolveSession(r, store)
}

func resolveAgent(r *http.Request, tokens TokenStore, identity IdentityResolver, token string) (context.Context, bool) {
	scope, err := ResolveAgentToken(r.Context(), tokens, token)
	if err != nil {
		return nil, false
	}
	email, name := "", ""
	if identity != nil {
		email, name, _ = identity.LookupUserIdentity(r.Context(), scope.UserID)
	}
	ctx := WithUserContext(r.Context(), scope.UserID, email, name, "unknown")
	return WithAgentContext(ctx), true
}

func resolveSession(r *http.Request, store *sessions.CookieStore) (context.Context, bool) {
	if store == nil {
		return nil, false
	}
	session, err := store.Get(r, "mysession")
	if err != nil {
		return nil, false
	}
	if authn, ok := session.Values["authenticated"].(bool); !ok || !authn {
		return nil, false
	}
	userID, _ := session.Values["user_id"].(string)
	if userID == "" {
		return nil, false
	}
	userEmail, _ := session.Values["user_email"].(string)
	userName, _ := session.Values["user_name"].(string)
	userRole, _ := session.Values["user_role"].(string)
	if userRole == "" {
		userRole = "unknown"
	}
	return WithUserContext(r.Context(), userID, userEmail, userName, userRole), true
}
