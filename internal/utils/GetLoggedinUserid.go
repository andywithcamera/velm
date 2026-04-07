package utils

import (
	"net/http"
	"velm/internal/auth"
)

func GetLoggedinUserid(r *http.Request) string {
	return auth.UserIDFromRequest(r)
}
