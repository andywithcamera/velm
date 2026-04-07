package main

import (
	"net/http"
	"velm/internal/auth"
)

func userDataFromRequest(r *http.Request) map[string]any {
	return map[string]any{
		"ID":    auth.UserIDFromRequest(r),
		"Name":  auth.UserNameFromRequest(r),
		"Email": auth.UserEmailFromRequest(r),
		"Role":  auth.UserRoleFromRequest(r),
	}
}
