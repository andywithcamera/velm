package main

import "net/http"

func handleForbiddenPage(w http.ResponseWriter, r *http.Request) {
	required := r.URL.Query().Get("required")
	appID := r.URL.Query().Get("app_id")

	data := newViewData(w, r, "/forbidden", "Forbidden", "Security")
	data["View"] = "forbidden"
	data["Required"] = required
	data["AppID"] = appID

	if err := templates.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, "Error rendering forbidden page", http.StatusInternalServerError)
	}
}
