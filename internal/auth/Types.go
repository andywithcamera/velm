package auth

import (
	"context"
	"net/http"
)

type contextKey string

const (
	userIDContextKey    contextKey = "user_id"
	userEmailContextKey contextKey = "user_email"
	userNameContextKey  contextKey = "user_name"
	userRoleContextKey  contextKey = "user_role"
	appIDContextKey     contextKey = "app_id"
	isAgentContextKey   contextKey = "is_agent"
)

func WithUserContext(ctx context.Context, userID, userEmail, userName, userRole string) context.Context {
	ctx = context.WithValue(ctx, userIDContextKey, userID)
	ctx = context.WithValue(ctx, userEmailContextKey, userEmail)
	ctx = context.WithValue(ctx, userNameContextKey, userName)
	ctx = context.WithValue(ctx, userRoleContextKey, userRole)
	return ctx
}

func UserIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	userID, _ := r.Context().Value(userIDContextKey).(string)
	return userID
}

func UserEmailFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	userEmail, _ := r.Context().Value(userEmailContextKey).(string)
	return userEmail
}

func UserNameFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	userName, _ := r.Context().Value(userNameContextKey).(string)
	return userName
}

func UserRoleFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	userRole, _ := r.Context().Value(userRoleContextKey).(string)
	return userRole
}

func WithAppContext(ctx context.Context, appID string) context.Context {
	return context.WithValue(ctx, appIDContextKey, appID)
}

func AppIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	appID, _ := r.Context().Value(appIDContextKey).(string)
	return appID
}

// WithAgentContext marks the request as authenticated via an agent API token
// (issue #68). Authorization stays identical -- only the authN mechanism
// differs, and this flag is what lets e.g. CAPTCHA/rate-opacity skip human-only
// prompts for API calls. It carries no scope/authority: ACLs are user-based and
// enforced downstream exactly as they are for a session.
func WithAgentContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, isAgentContextKey, true)
}

// IsAgentRequest reports whether the current request authenticated through an
// agent token rather than a human session.
func IsAgentRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	agent, _ := r.Context().Value(isAgentContextKey).(bool)
	return agent
}
