package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

type notificationMenuViewData struct {
	ReturnTo    string
	CSRFToken   string
	UnreadCount int
	Items       []notificationMenuItem
	// PushSubscribed drives the server-rendered "Send test notification"
	// control. It is decided here, not in JavaScript, because the client that
	// would reveal it is the one served through a cache that has repeatedly
	// gone stale on an installed PWA (#351).
	PushSubscribed  bool
	PushTestOutcome string
}

type notificationMenuItem struct {
	ID             string
	Title          string
	Body           string
	Href           string
	Level          string
	CreatedAtLabel string
	IsUnread       bool
}

func handleNotificationsPanel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := renderNotificationMenuPanel(w, r, sanitizeLoginNext(strings.TrimSpace(r.URL.Query().Get("return_to")))); err != nil {
		http.Error(w, "Failed to render notifications", http.StatusInternalServerError)
	}
}

func handleMarkAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if _, err := db.MarkAllUserNotificationsRead(r.Context(), userID); err != nil {
		http.Error(w, "Failed to mark notifications read", http.StatusInternalServerError)
		return
	}

	returnTo := sanitizeLoginNext(strings.TrimSpace(r.FormValue("return_to")))
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("HX-Request")), "true") {
		if err := renderNotificationMenuPanel(w, r, returnTo); err != nil {
			http.Error(w, "Failed to render notifications", http.StatusInternalServerError)
		}
		return
	}
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

func handleOpenNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	returnTo := sanitizeLoginNext(strings.TrimSpace(r.FormValue("return_to")))
	notificationID := strings.TrimSpace(r.FormValue("notification_id"))
	item, ok, err := db.ReadUserNotification(r.Context(), userID, notificationID)
	if err != nil {
		http.Error(w, "Failed to open notification", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Redirect(w, r, returnTo, http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, notificationRedirectTarget(item.Href, returnTo), http.StatusSeeOther)
}

func renderNotificationMenuPanel(w http.ResponseWriter, r *http.Request, returnTo string) error {
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		return http.ErrNoCookie
	}

	items, err := db.ListUnreadUserNotifications(r.Context(), userID, 8)
	if err != nil {
		return err
	}
	unreadCount, err := db.CountUnreadUserNotifications(r.Context(), userID)
	if err != nil {
		return err
	}

	// A push subscription is a server fact, so the panel can state it directly
	// instead of waiting for /api/push/status to load and reveal a button.
	states, err := db.ListPushSubscriptionStates(r.Context(), userID)
	if err != nil {
		log.Printf("notifications: reading push subscriptions failed: %v", err)
	}

	// The redirect after a form-driven test lands on the page with
	// ?push_test=1, and the panel's return_to is the page URI — so the flag
	// arrives inside return_to rather than in the panel's own query. Strip it
	// from the value used for links, so repeated tests do not accumulate it.
	showOutcome := r.URL.Query().Get("push_test") != ""
	if cleaned, hadFlag := stripQueryFlag(returnTo, "push_test"); hadFlag {
		showOutcome = true
		returnTo = cleaned
	}

	data := notificationMenuViewData{
		ReturnTo:        returnTo,
		CSRFToken:       ensureCSRFToken(w, r),
		UnreadCount:     unreadCount,
		Items:           buildNotificationMenuItems(items),
		PushSubscribed:  len(states) > 0,
		PushTestOutcome: pushTestOutcome(states, showOutcome),
	}
	return templates.ExecuteTemplate(w, "notification-menu-panel", data)
}

// stripQueryFlag removes one query parameter from a relative URL and reports
// whether it was present.
func stripQueryFlag(raw, name string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw, false
	}
	q := u.Query()
	if _, ok := q[name]; !ok {
		return raw, false
	}
	q.Del(name)
	u.RawQuery = q.Encode()
	return u.RequestURI(), true
}

// pushTestOutcome renders the last attempted delivery per endpoint, using the
// columns added with the status-code classification (#347). Empty unless the
// panel was reached by pressing the test control, so an idle menu stays quiet.
func pushTestOutcome(states []db.PushSubscriptionState, show bool) string {
	if !show {
		return ""
	}
	if len(states) == 0 {
		return "No subscription stored for this account."
	}
	parts := make([]string, 0, len(states))
	for _, s := range states {
		host := "push service"
		if u, err := url.Parse(s.Endpoint); err == nil && u.Host != "" {
			host = u.Host
		}
		switch {
		case s.LastStatus == nil:
			parts = append(parts, host+" — no response recorded")
		case *s.LastStatus >= 200 && *s.LastStatus < 300:
			parts = append(parts, fmt.Sprintf("✓ %s %d delivered", host, *s.LastStatus))
		default:
			detail := strings.TrimSpace(s.LastError)
			if detail == "" {
				detail = "no error detail"
			}
			parts = append(parts, fmt.Sprintf("✗ %s %d — %s", host, *s.LastStatus, detail))
		}
	}
	return strings.Join(parts, " · ")
}

func buildNotificationMenuItems(items []db.UserNotification) []notificationMenuItem {
	result := make([]notificationMenuItem, 0, len(items))
	for _, item := range items {
		result = append(result, notificationMenuItem{
			ID:             item.ID,
			Title:          item.Title,
			Body:           item.Body,
			Href:           item.Href,
			Level:          item.Level,
			CreatedAtLabel: item.CreatedAt.Local().Format("2006-01-02 15:04"),
			IsUnread:       !item.IsRead,
		})
	}
	return result
}

func notificationRedirectTarget(href, returnTo string) string {
	href = strings.TrimSpace(href)
	if href != "" {
		return href
	}
	return sanitizeLoginNext(returnTo)
}
