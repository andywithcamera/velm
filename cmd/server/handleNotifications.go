package main

import (
	"net/http"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

type notificationMenuViewData struct {
	ReturnTo    string
	CSRFToken   string
	UnreadCount int
	Items       []notificationMenuItem
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

	data := notificationMenuViewData{
		ReturnTo:    returnTo,
		CSRFToken:   ensureCSRFToken(w, r),
		UnreadCount: unreadCount,
		Items:       buildNotificationMenuItems(items),
	}
	return templates.ExecuteTemplate(w, "notification-menu-panel", data)
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
