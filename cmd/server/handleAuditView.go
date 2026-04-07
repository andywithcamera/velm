package main

import "net/http"

func handleAuditView(w http.ResponseWriter, r *http.Request) {
	target := "/t/_audit_log"
	if raw := r.URL.RawQuery; raw != "" {
		target += "?" + raw
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
