package main

import (
	"html/template"
	"net/http"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

type pageReadAccessResult struct {
	Allowed   bool
	ShowLogin bool
	NotFound  bool
	Forbidden bool
}

func renderPageBySlug(w http.ResponseWriter, r *http.Request, slug, section string) {
	pg, err := loadPageBySlug(slug)
	if err != nil {
		if !db.IsQualifiedPageSlug(slug) && pageSlugPattern.MatchString(strings.TrimSpace(strings.ToLower(slug))) {
			if legacy := normalizePageRouteSlug(slug); legacy != "" && legacy != slug {
				if _, legacyErr := loadPageBySlug(legacy); legacyErr == nil {
					http.Redirect(w, r, "/p/"+legacy, http.StatusMovedPermanently)
					return
				}
			}
		}
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	if section == "Public" && pg.Status != "published" {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	pageSecurityEvaluator, _, _, ok, err := db.LoadPageSecurityEvaluator(r.Context(), slug, userID)
	if err != nil {
		http.Error(w, "Failed to evaluate page security", http.StatusInternalServerError)
		return
	}
	allowed := !ok || pageSecurityEvaluator == nil || pageSecurityEvaluator.AllowsRecord("R", map[string]any{})
	access := pageReadAccess(section, userID, ok, allowed)
	switch {
	case access.ShowLogin:
		handleLoginPage(w, r)
		return
	case access.NotFound:
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	case access.Forbidden:
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	data := newViewData(w, r, "/p/"+pg.Slug, pg.Name, section)
	data["Page"] = "p/" + pg.Slug
	data["PageData"] = pg
	data["PageHTML"] = template.HTML(sanitizePageHTML(pg.Content))

	if r.Header.Get("HX-Request") != "" {
		err = templates.ExecuteTemplate(w, "page-view", data)
		if err != nil {
			http.NotFound(w, r)
		}
		return
	}

	err = templates.ExecuteTemplate(w, "layout.html", data)
	if err != nil {
		http.Error(w, "Error rendering page", http.StatusInternalServerError)
	}
}

// handlePage renders specific pages by slug
func handlePage(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/p/")
	if name == "" {
		name = "index"
	}
	renderPageBySlug(w, r, name, "Builder")
}

func pageReadAccess(section, userID string, hasSecurityDefinition, allowed bool) pageReadAccessResult {
	if !hasSecurityDefinition || allowed {
		return pageReadAccessResult{Allowed: true}
	}
	if section == "Public" {
		if strings.TrimSpace(userID) == "" {
			return pageReadAccessResult{ShowLogin: true}
		}
		return pageReadAccessResult{NotFound: true}
	}
	return pageReadAccessResult{Forbidden: true}
}
