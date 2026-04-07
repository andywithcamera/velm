package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

type docsLibrary struct {
	ID              string
	Slug            string
	Name            string
	Description     string
	Visibility      string
	OwnerUserID     string
	Status          string
	AppName         string
	AppLabel        string
	IsDefault       bool
	CreateRoles     []string
	ReadRoles       []string
	EditRoles       []string
	DeleteRoles     []string
	CreateRolesText string
	ReadRolesText   string
	EditRolesText   string
	DeleteRolesText string
	ArticleCount    int
	CanCreate       bool
	CanEdit         bool
	CanDelete       bool
}

type docsArticle struct {
	ID              string
	LibraryID       string
	LibrarySlug     string
	LibraryName     string
	LibraryApp      string
	Number          string
	Slug            string
	Title           string
	MarkdownBody    string
	RenderedHTML    string
	Status          string
	Tags            string
	VersionNum      int
	PublishedAt     string
	OwnerUserID     string
	ReadRoles       []string
	EditRoles       []string
	DeleteRoles     []string
	ReadRolesText   string
	EditRolesText   string
	DeleteRolesText string
	Excerpt         string
	TagItems        []string
	ReaderHref      string
	CanEdit         bool
	CanDelete       bool
}

type docsVersion struct {
	VersionNum   int
	MarkdownBody string
	RenderedHTML string
	Status       string
	CreatedBy    string
	CreatedAt    string
	Selected     bool
	Href         string
}

type docsAppOption struct {
	Name  string
	Label string
}

type docsPreviewResponse struct {
	HTML string `json:"html"`
}

type docsAccessContext struct {
	ctx           context.Context
	userID        string
	apps          map[string]db.RegisteredApp
	roleSets      map[string]map[string]bool
	globalRoleSet map[string]bool
}

var docsSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,63}$`)

type docsViewContext struct {
	Apps          []db.RegisteredApp
	Access        *docsAccessContext
	Libraries     []docsLibrary
	LibraryBySlug map[string]docsLibrary
}

func loadDocsViewContext(ctx context.Context, userID string, ensureDefaults bool) (*docsViewContext, error) {
	apps, err := db.ListActiveApps(ctx)
	if err != nil {
		return nil, err
	}
	if ensureDefaults {
		if err := ensureDefaultDocLibraries(ctx, apps); err != nil {
			return nil, err
		}
	}

	access, err := newDocsAccessContext(ctx, userID, apps)
	if err != nil {
		return nil, err
	}

	libraries, err := listDocsLibraries(ctx)
	if err != nil {
		return nil, err
	}
	appLabels := make(map[string]string, len(apps))
	for _, app := range apps {
		label := strings.TrimSpace(app.Label)
		if label == "" {
			label = humanizeMenuName(app.Name)
		}
		appLabels[app.Name] = label
	}
	for i := range libraries {
		libraries[i].AppLabel = appLabels[libraries[i].AppName]
	}
	libraries = filterReadableDocsLibraries(access, libraries)

	libraryBySlug := make(map[string]docsLibrary, len(libraries))
	for _, library := range libraries {
		libraryBySlug[library.Slug] = library
	}

	return &docsViewContext{
		Apps:          apps,
		Access:        access,
		Libraries:     libraries,
		LibraryBySlug: libraryBySlug,
	}, nil
}

func handleDocs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	docsCtx, err := loadDocsViewContext(ctx, userID, true)
	if err != nil {
		http.Error(w, "Failed to evaluate docs permissions", http.StatusInternalServerError)
		return
	}

	libraries := docsCtx.Libraries
	libraryBySlug := docsCtx.LibraryBySlug

	allPublishedArticles, err := listDocsArticles(ctx, "", "", true)
	if err != nil {
		http.Error(w, "Failed to load docs articles", http.StatusInternalServerError)
		return
	}
	allPublishedArticles = decorateDocsArticles(filterReadableDocsArticles(docsCtx.Access, allPublishedArticles, libraryBySlug))

	articleCountByLibrary := make(map[string]int, len(libraries))
	for _, article := range allPublishedArticles {
		articleCountByLibrary[article.LibrarySlug]++
	}
	for i := range libraries {
		libraries[i].ArticleCount = articleCountByLibrary[libraries[i].Slug]
		libraryBySlug[libraries[i].Slug] = libraries[i]
	}

	selectedLibrarySlug := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("library")))
	selectedArticleSlug := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("article")))
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	page := parsePositiveInt(r.URL.Query().Get("page"), 1, 1000000)
	pageSize := parsePositiveInt(r.URL.Query().Get("size"), 12, 48)

	selectedLibrary := docsLibrary{}
	if selectedLibrarySlug != "" {
		selectedLibrary = libraryBySlug[selectedLibrarySlug]
	}

	if selectedLibrary.Slug != "" && selectedArticleSlug != "" {
		article, err := getDocsArticle(ctx, selectedLibrary.Slug, selectedArticleSlug)
		if err == nil && article.Status == "published" && docsCtx.Access.canReadArticle(selectedLibrary, article) {
			http.Redirect(w, r, buildDocsArticleHref(article.ID), http.StatusMovedPermanently)
			return
		}
	}

	catalogArticles, err := listDocsArticles(ctx, selectedLibrarySlug, search, true)
	if err != nil {
		http.Error(w, "Failed to load docs articles", http.StatusInternalServerError)
		return
	}
	catalogArticles = decorateDocsArticles(filterReadableDocsArticles(docsCtx.Access, catalogArticles, libraryBySlug))

	totalArticles := len(catalogArticles)
	totalPages := 1
	if totalArticles > 0 {
		totalPages = (totalArticles + pageSize - 1) / pageSize
	}
	if page > totalPages {
		page = totalPages
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * pageSize
	if start > totalArticles {
		start = totalArticles
	}
	end := start + pageSize
	if end > totalArticles {
		end = totalArticles
	}
	pagedArticles := append([]docsArticle(nil), catalogArticles[start:end]...)

	canManage := false
	for _, library := range libraries {
		if library.CanCreate || library.CanEdit || library.CanDelete {
			canManage = true
			break
		}
	}

	manageHref := "/docs/manage"
	if selectedLibrary.Slug != "" {
		manageHref += "?library=" + url.QueryEscape(selectedLibrary.Slug)
	}

	viewData := newViewData(w, r, "/docs", "Docs", "Operations")
	viewData["View"] = "docs"
	viewData["DocsSaved"] = strings.TrimSpace(r.URL.Query().Get("saved"))
	viewData["DocsLibraries"] = libraries
	viewData["DocsLibrarySelected"] = selectedLibrary
	viewData["DocsArticles"] = pagedArticles
	viewData["DocsSearch"] = search
	viewData["DocsPage"] = page
	viewData["DocsPageSize"] = pageSize
	viewData["DocsTotal"] = totalArticles
	viewData["DocsTotalPages"] = totalPages
	viewData["DocsHasPrev"] = page > 1
	viewData["DocsHasNext"] = page < totalPages
	viewData["DocsPrevPage"] = page - 1
	viewData["DocsNextPage"] = page + 1
	viewData["DocsCanManage"] = canManage
	viewData["DocsManageHref"] = manageHref
	viewData["DocsHasSearch"] = search != ""
	viewData["DocsHasLibraryFilter"] = selectedLibrary.Slug != ""
	viewData["DocsPublishedCount"] = len(allPublishedArticles)

	if err := templates.ExecuteTemplate(w, "layout.html", viewData); err != nil {
		http.Error(w, "Error rendering docs", http.StatusInternalServerError)
	}
}

func handleDocsArticle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	articleID, err := parseDocsArticleID(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	docsCtx, err := loadDocsViewContext(ctx, userID, true)
	if err != nil {
		http.Error(w, "Failed to evaluate docs permissions", http.StatusInternalServerError)
		return
	}

	article, err := getDocsArticleByID(ctx, articleID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	library, ok := docsCtx.LibraryBySlug[article.LibrarySlug]
	if !ok || article.Status != "published" || !docsCtx.Access.canReadArticle(library, article) {
		http.NotFound(w, r)
		return
	}

	selectedArticle := decorateDocsArticlePresentation(decorateDocsArticlePermissions(docsCtx.Access, library, article))
	backlinks, err := listDocsArticleBacklinks(ctx, docsCtx.Access, docsCtx.LibraryBySlug, selectedArticle)
	if err != nil {
		http.Error(w, "Failed to load backlinks", http.StatusInternalServerError)
		return
	}

	canManage := selectedArticle.CanEdit || selectedArticle.CanDelete || library.CanEdit || library.CanCreate
	manageHref := "/docs/manage?library=" + url.QueryEscape(selectedArticle.LibrarySlug) + "&article=" + url.QueryEscape(selectedArticle.Slug)

	title := strings.TrimSpace(selectedArticle.Title)
	if strings.TrimSpace(selectedArticle.Number) != "" {
		title = selectedArticle.Number + " - " + title
	}

	viewData := newViewData(w, r, buildDocsArticleHref(selectedArticle.ID), title, "Operations")
	viewData["View"] = "docs-reader"
	viewData["DocsArticleSelected"] = selectedArticle
	viewData["DocsArticleRendered"] = renderMarkdownToSafeHTML(selectedArticle.MarkdownBody)
	viewData["DocsBacklinks"] = backlinks
	viewData["DocsCanManage"] = canManage
	viewData["DocsManageHref"] = manageHref
	viewData["DocsLibrarySelected"] = library

	if err := templates.ExecuteTemplate(w, "layout.html", viewData); err != nil {
		http.Error(w, "Error rendering doc", http.StatusInternalServerError)
	}
}

func handleDocsManage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	docsCtx, err := loadDocsViewContext(ctx, userID, true)
	if err != nil {
		http.Error(w, "Failed to evaluate docs permissions", http.StatusInternalServerError)
		return
	}

	libraries := docsCtx.Libraries

	selectedLibrarySlug := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("library")))
	selectedArticleSlug := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("article")))
	selectedVersionNum := parsePositiveInt(r.URL.Query().Get("version"), 0, 1000000)
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	page := parsePositiveInt(r.URL.Query().Get("page"), 1, 1000000)
	pageSize := parsePositiveInt(r.URL.Query().Get("size"), 20, 100)
	newLibraryMode := strings.TrimSpace(r.URL.Query().Get("new_library")) == "1"
	newArticleMode := strings.TrimSpace(r.URL.Query().Get("new_article")) == "1"

	libraryBySlug := make(map[string]docsLibrary, len(libraries))
	selectedLibrary := docsLibrary{}
	for _, library := range libraries {
		libraryBySlug[library.Slug] = library
		if library.Slug == selectedLibrarySlug {
			selectedLibrary = library
		}
	}

	articles, err := listDocsArticles(ctx, selectedLibrarySlug, search, false)
	if err != nil {
		http.Error(w, "Failed to load docs articles", http.StatusInternalServerError)
		return
	}
	articles = decorateDocsArticles(filterReadableDocsArticles(docsCtx.Access, articles, libraryBySlug))

	totalArticles := len(articles)
	totalPages := 1
	if totalArticles > 0 {
		totalPages = (totalArticles + pageSize - 1) / pageSize
	}
	if page > totalPages {
		page = totalPages
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * pageSize
	if start > totalArticles {
		start = totalArticles
	}
	end := start + pageSize
	if end > totalArticles {
		end = totalArticles
	}
	pagedArticles := append([]docsArticle(nil), articles[start:end]...)

	selectedArticle := docsArticle{}
	if selectedLibrary.Slug != "" && selectedArticleSlug != "" && !newArticleMode {
		if article, err := getDocsArticle(ctx, selectedLibrary.Slug, selectedArticleSlug); err == nil {
			if library, ok := libraryBySlug[article.LibrarySlug]; ok && docsCtx.Access.canReadArticle(library, article) {
				selectedArticle = decorateDocsArticlePresentation(decorateDocsArticlePermissions(docsCtx.Access, library, article))
			}
		}
	}
	if strings.TrimSpace(selectedArticle.ID) == "" && selectedLibrary.Slug != "" && len(pagedArticles) > 0 && !newArticleMode {
		selectedArticle = pagedArticles[0]
	}

	previewArticle := selectedArticle
	versions := []docsVersion{}
	if strings.TrimSpace(selectedArticle.ID) != "" {
		versions, err = listDocsArticleVersions(ctx, selectedArticle, selectedVersionNum)
		if err != nil {
			http.Error(w, "Failed to load docs versions", http.StatusInternalServerError)
			return
		}
		if selectedVersionNum > 0 {
			for _, version := range versions {
				if version.Selected {
					previewArticle.MarkdownBody = version.MarkdownBody
					previewArticle.RenderedHTML = version.RenderedHTML
					previewArticle.VersionNum = version.VersionNum
					previewArticle.Status = version.Status
					break
				}
			}
		}
	}

	if newLibraryMode {
		selectedLibrary = docsLibrary{Visibility: "app"}
	}
	if newArticleMode {
		selectedArticle = docsArticle{Status: "draft"}
		previewArticle = selectedArticle
	}

	viewData := newViewData(w, r, "/docs/manage", "Docs Manager", "Operations")
	viewData["View"] = "docs-manage"
	viewData["DocsSaved"] = strings.TrimSpace(r.URL.Query().Get("saved"))
	viewData["DocsLibraries"] = libraries
	viewData["DocsLibrarySelected"] = selectedLibrary
	viewData["DocsArticles"] = pagedArticles
	viewData["DocsArticleSelected"] = selectedArticle
	viewData["DocsPreviewArticle"] = previewArticle
	viewData["DocsPreviewRendered"] = renderMarkdownToSafeHTML(previewArticle.MarkdownBody)
	viewData["DocsSearch"] = search
	viewData["DocsPage"] = page
	viewData["DocsPageSize"] = pageSize
	viewData["DocsTotal"] = totalArticles
	viewData["DocsTotalPages"] = totalPages
	viewData["DocsHasPrev"] = page > 1
	viewData["DocsHasNext"] = page < totalPages
	viewData["DocsPrevPage"] = page - 1
	viewData["DocsNextPage"] = page + 1
	viewData["DocsApps"] = loadDocsAppOptions(docsCtx.Apps)
	viewData["DocsVersionSelected"] = selectedVersionNum
	viewData["DocsVersions"] = versions

	if err := templates.ExecuteTemplate(w, "layout.html", viewData); err != nil {
		http.Error(w, "Error rendering docs", http.StatusInternalServerError)
	}
}

func handleLegacyDocsRedirect(w http.ResponseWriter, r *http.Request) {
	target := "/docs"
	if raw := strings.TrimSpace(r.URL.RawQuery); raw != "" {
		target += "?" + raw
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

func handleDocsPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(docsPreviewResponse{
		HTML: string(renderMarkdownToSafeHTML(r.FormValue("markdown_body"))),
	})
}

func handleSaveDocsLibrary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	slug, err := saveDocsLibrary(r.Context(), userID, docsLibrary{
		Slug:        strings.TrimSpace(strings.ToLower(r.FormValue("slug"))),
		Name:        strings.TrimSpace(r.FormValue("name")),
		Description: strings.TrimSpace(r.FormValue("description")),
		Visibility:  strings.TrimSpace(strings.ToLower(r.FormValue("visibility"))),
		AppName:     strings.TrimSpace(strings.ToLower(r.FormValue("app_name"))),
		CreateRoles: normalizeDocsRoleInput(r.FormValue("create_roles")),
		ReadRoles:   normalizeDocsRoleInput(r.FormValue("read_roles")),
		EditRoles:   normalizeDocsRoleInput(r.FormValue("edit_roles")),
		DeleteRoles: normalizeDocsRoleInput(r.FormValue("delete_roles")),
	}, strings.TrimSpace(strings.ToLower(r.FormValue("original_slug"))))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/docs/manage?library="+url.QueryEscape(slug)+"&saved=library", http.StatusSeeOther)
}

func handleSaveDocsArticle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	librarySlug := strings.TrimSpace(strings.ToLower(r.FormValue("library_slug")))
	slug, err := saveDocsArticle(r.Context(), userID, librarySlug, docsArticle{
		Slug:         strings.TrimSpace(strings.ToLower(r.FormValue("slug"))),
		Title:        strings.TrimSpace(r.FormValue("title")),
		MarkdownBody: strings.TrimSpace(r.FormValue("markdown_body")),
		Status:       normalizeDocsArticleStatus(r.FormValue("status")),
		Tags:         strings.TrimSpace(r.FormValue("tags")),
		ReadRoles:    normalizeDocsRoleInput(r.FormValue("read_roles")),
		EditRoles:    normalizeDocsRoleInput(r.FormValue("edit_roles")),
		DeleteRoles:  normalizeDocsRoleInput(r.FormValue("delete_roles")),
	}, strings.TrimSpace(strings.ToLower(r.FormValue("original_slug"))))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/docs/manage?library="+url.QueryEscape(librarySlug)+"&article="+url.QueryEscape(slug)+"&saved=article", http.StatusSeeOther)
}

func handleArchiveDocsLibrary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	librarySlug := strings.TrimSpace(strings.ToLower(r.FormValue("library_slug")))
	if err := archiveDocsLibrary(r.Context(), userID, librarySlug); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/docs/manage?saved=library_archived", http.StatusSeeOther)
}

func handleArchiveDocsArticle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	librarySlug := strings.TrimSpace(strings.ToLower(r.FormValue("library_slug")))
	articleSlug := strings.TrimSpace(strings.ToLower(r.FormValue("article_slug")))
	if err := archiveDocsArticle(r.Context(), userID, librarySlug, articleSlug); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/docs/manage?library="+url.QueryEscape(librarySlug)+"&saved=article_archived", http.StatusSeeOther)
}

func newDocsAccessContext(ctx context.Context, userID string, apps []db.RegisteredApp) (*docsAccessContext, error) {
	appIndex := make(map[string]db.RegisteredApp, len(apps))
	for _, app := range apps {
		appIndex[app.Name] = app
	}

	globalRoleNames, err := db.ListEffectiveRoleNames(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	return &docsAccessContext{
		ctx:           ctx,
		userID:        userID,
		apps:          appIndex,
		roleSets:      map[string]map[string]bool{},
		globalRoleSet: docsRoleSet(globalRoleNames),
	}, nil
}

func (c *docsAccessContext) roleSetForLibrary(library docsLibrary) map[string]bool {
	appName := strings.TrimSpace(strings.ToLower(library.AppName))
	if appName == "" {
		return c.globalRoleSet
	}
	if roleSet, ok := c.roleSets[appName]; ok {
		return roleSet
	}
	app, ok := c.apps[appName]
	if !ok {
		c.roleSets[appName] = c.globalRoleSet
		return c.globalRoleSet
	}
	roleNames, err := db.ListEffectiveRoleNames(c.ctx, c.userID, app.ID)
	if err != nil {
		c.roleSets[appName] = c.globalRoleSet
		return c.globalRoleSet
	}
	roleSet := docsRoleSet(roleNames)
	c.roleSets[appName] = roleSet
	return roleSet
}

func (c *docsAccessContext) isAdmin(library docsLibrary) bool {
	roleSet := c.roleSetForLibrary(library)
	return roleSet["admin"]
}

func (c *docsAccessContext) roleMatch(library docsLibrary, roles []string) bool {
	roles = normalizeDocsRoleList(roles)
	if len(roles) == 0 {
		return false
	}
	app, hasApp := c.apps[strings.TrimSpace(strings.ToLower(library.AppName))]
	roleSet := c.roleSetForLibrary(library)
	for _, role := range roles {
		if roleSet[role] {
			return true
		}
		if !hasApp {
			continue
		}
		scope := strings.TrimSpace(strings.ToLower(app.Namespace))
		if scope == "" {
			scope = strings.TrimSpace(strings.ToLower(app.Name))
		}
		if scope != "" && roleSet[scope+"."+role] {
			return true
		}
	}
	return false
}

func (c *docsAccessContext) canReadLibrary(library docsLibrary) bool {
	if c.isAdmin(library) {
		return true
	}
	if len(library.ReadRoles) > 0 {
		return c.roleMatch(library, library.ReadRoles)
	}
	if library.Visibility == "app" || library.Visibility == "public" {
		return true
	}
	return strings.TrimSpace(library.OwnerUserID) != "" && strings.TrimSpace(library.OwnerUserID) == c.userID
}

func (c *docsAccessContext) canCreateInLibrary(library docsLibrary) bool {
	if c.isAdmin(library) {
		return true
	}
	if len(library.CreateRoles) > 0 {
		return c.roleMatch(library, library.CreateRoles)
	}
	return true
}

func (c *docsAccessContext) canEditLibrary(library docsLibrary) bool {
	if c.isAdmin(library) {
		return true
	}
	if len(library.EditRoles) > 0 {
		return c.roleMatch(library, library.EditRoles)
	}
	return strings.TrimSpace(library.OwnerUserID) != "" && strings.TrimSpace(library.OwnerUserID) == c.userID
}

func (c *docsAccessContext) canDeleteLibrary(library docsLibrary) bool {
	if library.IsDefault {
		return false
	}
	if c.isAdmin(library) {
		return true
	}
	if len(library.DeleteRoles) > 0 {
		return c.roleMatch(library, library.DeleteRoles)
	}
	return strings.TrimSpace(library.OwnerUserID) != "" && strings.TrimSpace(library.OwnerUserID) == c.userID
}

func (c *docsAccessContext) canReadArticle(library docsLibrary, article docsArticle) bool {
	if c.isAdmin(library) {
		return true
	}
	roles := effectiveDocsRoles(article.ReadRoles, library.ReadRoles)
	if len(roles) > 0 {
		return c.roleMatch(library, roles)
	}
	return c.canReadLibrary(library)
}

func (c *docsAccessContext) canEditArticle(library docsLibrary, article docsArticle) bool {
	if c.isAdmin(library) {
		return true
	}
	roles := effectiveDocsRoles(article.EditRoles, library.EditRoles)
	if len(roles) > 0 {
		return c.roleMatch(library, roles)
	}
	if strings.TrimSpace(article.OwnerUserID) != "" && strings.TrimSpace(article.OwnerUserID) == c.userID {
		return true
	}
	return c.canEditLibrary(library)
}

func (c *docsAccessContext) canDeleteArticle(library docsLibrary, article docsArticle) bool {
	if c.isAdmin(library) {
		return true
	}
	roles := effectiveDocsRoles(article.DeleteRoles, library.DeleteRoles)
	if len(roles) > 0 {
		return c.roleMatch(library, roles)
	}
	if strings.TrimSpace(article.OwnerUserID) != "" && strings.TrimSpace(article.OwnerUserID) == c.userID {
		return true
	}
	return c.canDeleteLibrary(library)
}

func filterReadableDocsLibraries(access *docsAccessContext, libraries []docsLibrary) []docsLibrary {
	filtered := make([]docsLibrary, 0, len(libraries))
	for _, library := range libraries {
		if !access.canReadLibrary(library) {
			continue
		}
		library.CanCreate = access.canCreateInLibrary(library)
		library.CanEdit = access.canEditLibrary(library)
		library.CanDelete = access.canDeleteLibrary(library)
		filtered = append(filtered, library)
	}
	return filtered
}

func filterReadableDocsArticles(access *docsAccessContext, articles []docsArticle, libraries map[string]docsLibrary) []docsArticle {
	filtered := make([]docsArticle, 0, len(articles))
	for _, article := range articles {
		library, ok := libraries[article.LibrarySlug]
		if !ok || !access.canReadArticle(library, article) {
			continue
		}
		filtered = append(filtered, decorateDocsArticlePermissions(access, library, article))
	}
	return filtered
}

func decorateDocsArticlePermissions(access *docsAccessContext, library docsLibrary, article docsArticle) docsArticle {
	article.CanEdit = access.canEditArticle(library, article)
	article.CanDelete = access.canDeleteArticle(library, article)
	return article
}

func decorateDocsArticles(articles []docsArticle) []docsArticle {
	decorated := make([]docsArticle, 0, len(articles))
	for _, article := range articles {
		decorated = append(decorated, decorateDocsArticlePresentation(article))
	}
	return decorated
}

func decorateDocsArticlePresentation(article docsArticle) docsArticle {
	article.Excerpt = summarizeDocsMarkdown(article.MarkdownBody)
	article.TagItems = splitDocsTags(article.Tags)
	article.ReaderHref = buildDocsArticleHref(article.ID)
	return article
}

func buildDocsArticleHref(articleID string) string {
	articleID = strings.TrimSpace(articleID)
	if articleID == "" || articleID == "new" || !db.IsValidRecordID("_docs_article", articleID) {
		return "/docs"
	}
	return "/d/" + url.PathEscape(articleID)
}

func parseDocsArticleID(path string) (string, error) {
	raw := strings.TrimSpace(strings.TrimPrefix(path, "/d/"))
	raw = strings.Trim(raw, "/")
	if raw == "" || strings.Contains(raw, "/") {
		return "", fmt.Errorf("invalid doc id")
	}
	if raw == "new" || !db.IsValidRecordID("_docs_article", raw) {
		return "", fmt.Errorf("invalid doc id")
	}
	return raw, nil
}

func listDocsArticleBacklinks(ctx context.Context, access *docsAccessContext, libraries map[string]docsLibrary, article docsArticle) ([]docsArticle, error) {
	candidates, err := listDocsArticles(ctx, "", "", true)
	if err != nil {
		return nil, err
	}
	candidates = decorateDocsArticles(filterReadableDocsArticles(access, candidates, libraries))

	backlinks := make([]docsArticle, 0, 8)
	for _, candidate := range candidates {
		if candidate.ID == article.ID {
			continue
		}
		if !markdownLinksToDocsArticle(candidate.MarkdownBody, article) {
			continue
		}
		backlinks = append(backlinks, candidate)
	}

	sort.SliceStable(backlinks, func(i, j int) bool {
		if backlinks[i].LibraryName == backlinks[j].LibraryName {
			return backlinks[i].Title < backlinks[j].Title
		}
		return backlinks[i].LibraryName < backlinks[j].LibraryName
	})
	return backlinks, nil
}

func markdownLinksToDocsArticle(markdown string, article docsArticle) bool {
	matches := mdLinkPattern.FindAllStringSubmatch(markdown, -1)
	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		if markdownLinkMatchesDocsArticle(match[2], article) {
			return true
		}
	}
	return false
}

func markdownLinkMatchesDocsArticle(rawHref string, article docsArticle) bool {
	href := strings.TrimSpace(rawHref)
	if href == "" {
		return false
	}

	parsed, err := url.Parse(href)
	if err != nil {
		return false
	}

	path := strings.Trim(strings.TrimSpace(parsed.Path), "/")
	canonicalPath := strings.Trim(buildDocsArticleHref(article.ID), "/")
	if path == canonicalPath {
		return true
	}

	query := parsed.Query()
	if (path == "" || path == "docs") &&
		strings.EqualFold(strings.TrimSpace(query.Get("library")), article.LibrarySlug) &&
		strings.EqualFold(strings.TrimSpace(query.Get("article")), article.Slug) {
		return true
	}
	return false
}

func summarizeDocsMarkdown(markdown string) string {
	lines := strings.Split(markdown, "\n")
	parts := make([]string, 0, len(lines))
	replacer := strings.NewReplacer(
		"#", "",
		"*", "",
		"`", "",
		"[", "",
		"]", "",
		"(", " ",
		")", " ",
	)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = replacer.Replace(line)
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		parts = append(parts, line)
		if len(strings.Join(parts, " ")) >= 180 {
			break
		}
	}
	summary := strings.TrimSpace(strings.Join(parts, " "))
	if len(summary) <= 180 {
		return summary
	}
	summary = strings.TrimSpace(summary[:180])
	lastSpace := strings.LastIndex(summary, " ")
	if lastSpace > 120 {
		summary = summary[:lastSpace]
	}
	return summary + "..."
}

func splitDocsTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	tags := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		tag := strings.TrimSpace(part)
		tagKey := strings.ToLower(tag)
		if tag == "" || seen[tagKey] {
			continue
		}
		seen[tagKey] = true
		tags = append(tags, tag)
		if len(tags) >= 4 {
			break
		}
	}
	return tags
}

func ensureDefaultDocLibraries(ctx context.Context, apps []db.RegisteredApp) error {
	rows, err := db.Pool.Query(ctx, `
		SELECT _id::text, slug, COALESCE(name, ''), COALESCE(app_name, ''), COALESCE(is_default, FALSE)
		FROM _docs_library
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type existingLibrary struct {
		Slug      string
		Name      string
		AppName   string
		IsDefault bool
	}
	existingBySlug := map[string]existingLibrary{}
	defaultByApp := map[string]existingLibrary{}
	for rows.Next() {
		var item existingLibrary
		var id string
		if err := rows.Scan(&id, &item.Slug, &item.Name, &item.AppName, &item.IsDefault); err != nil {
			return err
		}
		existingBySlug[item.Slug] = item
		if item.IsDefault && strings.TrimSpace(item.AppName) != "" {
			defaultByApp[item.AppName] = item
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, app := range apps {
		appName := strings.TrimSpace(strings.ToLower(app.Name))
		if appName == "" {
			continue
		}
		if _, ok := defaultByApp[appName]; ok {
			continue
		}
		slug := appName
		if existing, ok := existingBySlug[slug]; ok && strings.TrimSpace(existing.AppName) != appName {
			slug = appName + "-docs"
		}
		label := strings.TrimSpace(app.Label)
		if label == "" {
			label = humanizeMenuName(appName)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO _docs_library (slug, name, description, visibility, owner_user_id, status, app_name, is_default)
			VALUES ($1, $2, $3, 'app', '', 'active', $4, TRUE)
			ON CONFLICT DO NOTHING
		`, slug, label, fmt.Sprintf("Default docs library for %s.", label), appName); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func loadDocsAppOptions(apps []db.RegisteredApp) []docsAppOption {
	options := make([]docsAppOption, 0, len(apps)+1)
	options = append(options, docsAppOption{Name: "", Label: "Platform Shared"})
	for _, app := range apps {
		label := strings.TrimSpace(app.Label)
		if label == "" {
			label = humanizeMenuName(app.Name)
		}
		options = append(options, docsAppOption{Name: app.Name, Label: label})
	}
	sort.Slice(options[1:], func(i, j int) bool {
		return options[i+1].Label < options[j+1].Label
	})
	return options
}

func listDocsLibraries(ctx context.Context) ([]docsLibrary, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT _id::text, slug, name, description, visibility, owner_user_id, status,
		       COALESCE(app_name, ''), COALESCE(is_default, FALSE),
		       COALESCE(create_roles, '[]'::jsonb)::text,
		       COALESCE(read_roles, '[]'::jsonb)::text,
		       COALESCE(edit_roles, '[]'::jsonb)::text,
		       COALESCE(delete_roles, '[]'::jsonb)::text
		FROM _docs_library
		WHERE status = 'active'
		ORDER BY CASE WHEN app_name = '' THEN 1 ELSE 0 END, app_name ASC, is_default DESC, name ASC, slug ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]docsLibrary, 0, 32)
	for rows.Next() {
		var item docsLibrary
		var createRolesJSON string
		var readRolesJSON string
		var editRolesJSON string
		var deleteRolesJSON string
		if err := rows.Scan(
			&item.ID,
			&item.Slug,
			&item.Name,
			&item.Description,
			&item.Visibility,
			&item.OwnerUserID,
			&item.Status,
			&item.AppName,
			&item.IsDefault,
			&createRolesJSON,
			&readRolesJSON,
			&editRolesJSON,
			&deleteRolesJSON,
		); err != nil {
			return nil, err
		}
		item.CreateRoles = parseDocsRolesJSON(createRolesJSON)
		item.ReadRoles = parseDocsRolesJSON(readRolesJSON)
		item.EditRoles = parseDocsRolesJSON(editRolesJSON)
		item.DeleteRoles = parseDocsRolesJSON(deleteRolesJSON)
		item.CreateRolesText = strings.Join(item.CreateRoles, ", ")
		item.ReadRolesText = strings.Join(item.ReadRoles, ", ")
		item.EditRolesText = strings.Join(item.EditRoles, ", ")
		item.DeleteRolesText = strings.Join(item.DeleteRoles, ", ")
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const docsArticleSelectColumns = `
	a._id::text,
	a.docs_library_id::text,
	l.slug,
	l.name,
	COALESCE(l.app_name, ''),
	COALESCE(a.number, ''),
	a.slug,
	a.title,
	a.markdown_body,
	a.rendered_html,
	a.status,
	COALESCE(a.tags, ''),
	a.version_num,
	COALESCE(a.published_at::text, ''),
	a.owner_user_id,
	COALESCE(a.read_roles, '[]'::jsonb)::text,
	COALESCE(a.edit_roles, '[]'::jsonb)::text,
	COALESCE(a.delete_roles, '[]'::jsonb)::text
`

type docsRowScanner interface {
	Scan(dest ...any) error
}

func scanDocsArticle(scanner docsRowScanner) (docsArticle, error) {
	var item docsArticle
	var readRolesJSON string
	var editRolesJSON string
	var deleteRolesJSON string
	if err := scanner.Scan(
		&item.ID,
		&item.LibraryID,
		&item.LibrarySlug,
		&item.LibraryName,
		&item.LibraryApp,
		&item.Number,
		&item.Slug,
		&item.Title,
		&item.MarkdownBody,
		&item.RenderedHTML,
		&item.Status,
		&item.Tags,
		&item.VersionNum,
		&item.PublishedAt,
		&item.OwnerUserID,
		&readRolesJSON,
		&editRolesJSON,
		&deleteRolesJSON,
	); err != nil {
		return docsArticle{}, err
	}
	item.ReadRoles = parseDocsRolesJSON(readRolesJSON)
	item.EditRoles = parseDocsRolesJSON(editRolesJSON)
	item.DeleteRoles = parseDocsRolesJSON(deleteRolesJSON)
	item.ReadRolesText = strings.Join(item.ReadRoles, ", ")
	item.EditRolesText = strings.Join(item.EditRoles, ", ")
	item.DeleteRolesText = strings.Join(item.DeleteRoles, ", ")
	return item, nil
}

func listDocsArticles(ctx context.Context, librarySlug, search string, publishedOnly bool) ([]docsArticle, error) {
	clauses := []string{"l.status = 'active'", "a.status <> 'archived'"}
	args := []any{}
	if publishedOnly {
		clauses = append(clauses, "a.status = 'published'")
	}
	if librarySlug != "" {
		clauses = append(clauses, fmt.Sprintf("l.slug = $%d", len(args)+1))
		args = append(args, librarySlug)
	}
	if trimmed := strings.TrimSpace(strings.ToLower(search)); trimmed != "" {
		clauses = append(clauses, fmt.Sprintf(`(
			LOWER(a.title) LIKE $%d
			OR LOWER(COALESCE(a.number, '')) LIKE $%d
			OR LOWER(a.markdown_body) LIKE $%d
			OR LOWER(COALESCE(a.tags, '')) LIKE $%d
		)`, len(args)+1, len(args)+1, len(args)+1, len(args)+1))
		args = append(args, "%"+trimmed+"%")
	}

	query := `
		SELECT ` + docsArticleSelectColumns + `
		FROM _docs_article a
		JOIN _docs_library l ON l._id = a.docs_library_id
		WHERE ` + strings.Join(clauses, " AND ") + `
		ORDER BY CASE WHEN a.status = 'published' THEN 0 ELSE 1 END,
		         COALESCE(a.published_at, a._updated_at, a._created_at) DESC,
		         a.title ASC
	`

	rows, err := db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]docsArticle, 0, 64)
	for rows.Next() {
		item, err := scanDocsArticle(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func getDocsArticle(ctx context.Context, librarySlug, articleSlug string) (docsArticle, error) {
	item, err := scanDocsArticle(db.Pool.QueryRow(ctx, `
		SELECT `+docsArticleSelectColumns+`
		FROM _docs_article a
		JOIN _docs_library l ON l._id = a.docs_library_id
		WHERE l.slug = $1
		  AND a.slug = $2
		  AND l.status = 'active'
		LIMIT 1
	`, librarySlug, articleSlug))
	if err != nil {
		return docsArticle{}, err
	}
	return item, nil
}

func getDocsArticleByID(ctx context.Context, articleID string) (docsArticle, error) {
	item, err := scanDocsArticle(db.Pool.QueryRow(ctx, `
		SELECT `+docsArticleSelectColumns+`
		FROM _docs_article a
		JOIN _docs_library l ON l._id = a.docs_library_id
		WHERE a._id::text = $1
		  AND l.status = 'active'
		LIMIT 1
	`, articleID))
	if err != nil {
		return docsArticle{}, err
	}
	return item, nil
}

func listDocsArticleVersions(ctx context.Context, article docsArticle, selectedVersionNum int) ([]docsVersion, error) {
	rows, err := db.Pool.Query(ctx, `
		SELECT version_num, markdown_body, rendered_html, status, created_by, COALESCE(_created_at::text, '')
		FROM _docs_article_version
		WHERE docs_article_id::text = $1
		ORDER BY version_num DESC
	`, article.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]docsVersion, 0, 16)
	for rows.Next() {
		var item docsVersion
		if err := rows.Scan(&item.VersionNum, &item.MarkdownBody, &item.RenderedHTML, &item.Status, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.Selected = selectedVersionNum > 0 && item.VersionNum == selectedVersionNum
		item.Href = buildDocsVersionHref(article.LibrarySlug, article.Slug, item.VersionNum)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if selectedVersionNum == 0 && len(items) > 0 {
		items[0].Selected = true
	}
	return items, nil
}

func saveDocsLibrary(ctx context.Context, userID string, library docsLibrary, originalSlug string) (string, error) {
	library.Slug = strings.TrimSpace(strings.ToLower(library.Slug))
	originalSlug = strings.TrimSpace(strings.ToLower(originalSlug))
	library.Name = strings.TrimSpace(library.Name)
	library.Description = strings.TrimSpace(library.Description)
	library.Visibility = normalizeDocsLibraryVisibility(library.Visibility)
	library.AppName = strings.TrimSpace(strings.ToLower(library.AppName))
	library.CreateRoles = normalizeDocsRoleList(library.CreateRoles)
	library.ReadRoles = normalizeDocsRoleList(library.ReadRoles)
	library.EditRoles = normalizeDocsRoleList(library.EditRoles)
	library.DeleteRoles = normalizeDocsRoleList(library.DeleteRoles)
	createRolesJSON := marshalDocsRolesJSON(library.CreateRoles)
	readRolesJSON := marshalDocsRolesJSON(library.ReadRoles)
	editRolesJSON := marshalDocsRolesJSON(library.EditRoles)
	deleteRolesJSON := marshalDocsRolesJSON(library.DeleteRoles)

	if !docsSlugPattern.MatchString(library.Slug) {
		return "", fmt.Errorf("invalid library slug")
	}
	if library.Name == "" {
		return "", fmt.Errorf("library name is required")
	}

	apps, err := db.ListActiveApps(ctx)
	if err != nil {
		return "", err
	}
	access, err := newDocsAccessContext(ctx, userID, apps)
	if err != nil {
		return "", err
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var existing docsLibrary
	err = tx.QueryRow(ctx, `
		SELECT _id::text, slug, name, description, visibility, owner_user_id, status,
		       COALESCE(app_name, ''), COALESCE(is_default, FALSE),
		       COALESCE(create_roles, '[]'::jsonb)::text,
		       COALESCE(read_roles, '[]'::jsonb)::text,
		       COALESCE(edit_roles, '[]'::jsonb)::text,
		       COALESCE(delete_roles, '[]'::jsonb)::text
		FROM _docs_library
		WHERE slug = $1
		LIMIT 1
	`, firstNonEmpty(originalSlug, library.Slug)).Scan(
		&existing.ID,
		&existing.Slug,
		&existing.Name,
		&existing.Description,
		&existing.Visibility,
		&existing.OwnerUserID,
		&existing.Status,
		&existing.AppName,
		&existing.IsDefault,
		&existing.CreateRolesText,
		&existing.ReadRolesText,
		&existing.EditRolesText,
		&existing.DeleteRolesText,
	)
	switch err {
	case nil:
		existing.CreateRoles = parseDocsRolesJSON(existing.CreateRolesText)
		existing.ReadRoles = parseDocsRolesJSON(existing.ReadRolesText)
		existing.EditRoles = parseDocsRolesJSON(existing.EditRolesText)
		existing.DeleteRoles = parseDocsRolesJSON(existing.DeleteRolesText)
		if !access.canEditLibrary(existing) {
			return "", fmt.Errorf("you do not have permission to edit this library")
		}
		if existing.IsDefault {
			library.Slug = existing.Slug
			library.AppName = existing.AppName
		}
		_, err = tx.Exec(ctx, `
			UPDATE _docs_library
			SET slug = $2,
			    name = $3,
			    description = $4,
			    visibility = $5,
			    app_name = $6,
			    create_roles = $7::jsonb,
			    read_roles = $8::jsonb,
			    edit_roles = $9::jsonb,
			    delete_roles = $10::jsonb,
			    _updated_at = NOW()
			WHERE _id::text = $1
		`, existing.ID, library.Slug, library.Name, library.Description, library.Visibility, library.AppName, createRolesJSON, readRolesJSON, editRolesJSON, deleteRolesJSON)
		if err != nil {
			return "", err
		}
	case sql.ErrNoRows:
		_, err = tx.Exec(ctx, `
			INSERT INTO _docs_library (slug, name, description, visibility, owner_user_id, status, app_name, is_default, create_roles, read_roles, edit_roles, delete_roles)
			VALUES ($1, $2, $3, $4, $5, 'active', $6, FALSE, $7::jsonb, $8::jsonb, $9::jsonb, $10::jsonb)
		`, library.Slug, library.Name, library.Description, library.Visibility, userID, library.AppName, createRolesJSON, readRolesJSON, editRolesJSON, deleteRolesJSON)
		if err != nil {
			return "", err
		}
	default:
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return library.Slug, nil
}

func saveDocsArticle(ctx context.Context, userID, librarySlug string, article docsArticle, originalSlug string) (string, error) {
	librarySlug = strings.TrimSpace(strings.ToLower(librarySlug))
	article.Slug = strings.TrimSpace(strings.ToLower(article.Slug))
	originalSlug = strings.TrimSpace(strings.ToLower(originalSlug))
	article.Title = strings.TrimSpace(article.Title)
	article.MarkdownBody = strings.TrimSpace(article.MarkdownBody)
	article.Status = normalizeDocsArticleStatus(article.Status)
	article.Tags = strings.TrimSpace(article.Tags)
	article.ReadRoles = normalizeDocsRoleList(article.ReadRoles)
	article.EditRoles = normalizeDocsRoleList(article.EditRoles)
	article.DeleteRoles = normalizeDocsRoleList(article.DeleteRoles)
	readRolesJSON := marshalDocsRolesJSON(article.ReadRoles)
	editRolesJSON := marshalDocsRolesJSON(article.EditRoles)
	deleteRolesJSON := marshalDocsRolesJSON(article.DeleteRoles)

	if !docsSlugPattern.MatchString(librarySlug) {
		return "", fmt.Errorf("invalid library slug")
	}
	if !docsSlugPattern.MatchString(article.Slug) {
		return "", fmt.Errorf("invalid article slug")
	}
	if article.Title == "" {
		return "", fmt.Errorf("article title is required")
	}

	apps, err := db.ListActiveApps(ctx)
	if err != nil {
		return "", err
	}
	access, err := newDocsAccessContext(ctx, userID, apps)
	if err != nil {
		return "", err
	}
	libraries, err := listDocsLibraries(ctx)
	if err != nil {
		return "", err
	}
	var library docsLibrary
	for _, candidate := range libraries {
		if candidate.Slug == librarySlug {
			library = candidate
			break
		}
	}
	if strings.TrimSpace(library.ID) == "" {
		return "", fmt.Errorf("library not found")
	}

	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rendered := string(renderMarkdownToSafeHTML(article.MarkdownBody))
	var existing docsArticle
	existing, err = scanDocsArticle(tx.QueryRow(ctx, `
		SELECT `+docsArticleSelectColumns+`
		FROM _docs_article a
		JOIN _docs_library l ON l._id = a.docs_library_id
		WHERE l.slug = $1
		  AND a.slug = $2
		LIMIT 1
	`, librarySlug, firstNonEmpty(originalSlug, article.Slug)))
	switch err {
	case nil:
		if !access.canEditArticle(library, existing) {
			return "", fmt.Errorf("you do not have permission to edit this article")
		}
		var nextVersion int
		if err := tx.QueryRow(ctx, `
			UPDATE _docs_article
			SET slug = $2,
			    title = $3,
			    markdown_body = $4,
			    rendered_html = $5,
			    status = $6,
			    tags = $7,
			    read_roles = $8::jsonb,
			    edit_roles = $9::jsonb,
			    delete_roles = $10::jsonb,
			    version_num = version_num + 1,
			    published_at = CASE WHEN $6 = 'published' THEN COALESCE(published_at, NOW()) ELSE published_at END,
			    _updated_at = NOW()
			WHERE _id::text = $1
			RETURNING version_num
		`, existing.ID, article.Slug, article.Title, article.MarkdownBody, rendered, article.Status, article.Tags, readRolesJSON, editRolesJSON, deleteRolesJSON).Scan(&nextVersion); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO _docs_article_version (docs_article_id, version_num, markdown_body, rendered_html, status, created_by)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (docs_article_id, version_num) DO NOTHING
		`, existing.ID, nextVersion, article.MarkdownBody, rendered, article.Status, userID); err != nil {
			return "", err
		}
	case sql.ErrNoRows:
		if !access.canCreateInLibrary(library) {
			return "", fmt.Errorf("you do not have permission to create articles in this library")
		}
		var articleID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO _docs_article (docs_library_id, slug, title, markdown_body, rendered_html, status, tags, version_num, published_at, owner_user_id, read_roles, edit_roles, delete_roles)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 1, CASE WHEN $6 = 'published' THEN NOW() ELSE NULL END, $8, $9::jsonb, $10::jsonb, $11::jsonb)
			RETURNING _id::text
		`, library.ID, article.Slug, article.Title, article.MarkdownBody, rendered, article.Status, article.Tags, userID, readRolesJSON, editRolesJSON, deleteRolesJSON).Scan(&articleID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO _docs_article_version (docs_article_id, version_num, markdown_body, rendered_html, status, created_by)
			VALUES ($1, 1, $2, $3, $4, $5)
			ON CONFLICT (docs_article_id, version_num) DO NOTHING
		`, articleID, article.MarkdownBody, rendered, article.Status, userID); err != nil {
			return "", err
		}
	default:
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return article.Slug, nil
}

func archiveDocsLibrary(ctx context.Context, userID, librarySlug string) error {
	apps, err := db.ListActiveApps(ctx)
	if err != nil {
		return err
	}
	access, err := newDocsAccessContext(ctx, userID, apps)
	if err != nil {
		return err
	}
	libraries, err := listDocsLibraries(ctx)
	if err != nil {
		return err
	}
	var library docsLibrary
	for _, candidate := range libraries {
		if candidate.Slug == librarySlug {
			library = candidate
			break
		}
	}
	if strings.TrimSpace(library.ID) == "" {
		return fmt.Errorf("library not found")
	}
	if !access.canDeleteLibrary(library) {
		return fmt.Errorf("you do not have permission to archive this library")
	}
	_, err = db.Pool.Exec(ctx, `
		UPDATE _docs_library
		SET status = 'archived', _updated_at = NOW()
		WHERE _id::text = $1
	`, library.ID)
	return err
}

func archiveDocsArticle(ctx context.Context, userID, librarySlug, articleSlug string) error {
	apps, err := db.ListActiveApps(ctx)
	if err != nil {
		return err
	}
	access, err := newDocsAccessContext(ctx, userID, apps)
	if err != nil {
		return err
	}
	libraries, err := listDocsLibraries(ctx)
	if err != nil {
		return err
	}
	var library docsLibrary
	for _, candidate := range libraries {
		if candidate.Slug == librarySlug {
			library = candidate
			break
		}
	}
	if strings.TrimSpace(library.ID) == "" {
		return fmt.Errorf("library not found")
	}
	article, err := getDocsArticle(ctx, librarySlug, articleSlug)
	if err != nil {
		return fmt.Errorf("article not found")
	}
	if !access.canDeleteArticle(library, article) {
		return fmt.Errorf("you do not have permission to archive this article")
	}
	_, err = db.Pool.Exec(ctx, `
		UPDATE _docs_article
		SET status = 'archived', _updated_at = NOW()
		WHERE _id::text = $1
	`, article.ID)
	return err
}

func effectiveDocsRoles(articleRoles, libraryRoles []string) []string {
	if len(articleRoles) > 0 {
		return articleRoles
	}
	return libraryRoles
}

func docsRoleSet(roleNames []string) map[string]bool {
	set := make(map[string]bool, len(roleNames))
	for _, roleName := range roleNames {
		roleName = strings.TrimSpace(strings.ToLower(roleName))
		if roleName != "" {
			set[roleName] = true
		}
	}
	return set
}

func normalizeDocsRoleInput(raw string) []string {
	return normalizeDocsRoleList(strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ';'
	}))
}

func parseDocsRolesJSON(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var roles []string
	if err := json.Unmarshal([]byte(raw), &roles); err != nil {
		return nil
	}
	return normalizeDocsRoleList(roles)
}

func marshalDocsRolesJSON(roles []string) string {
	payload, err := json.Marshal(normalizeDocsRoleList(roles))
	if err != nil {
		return "[]"
	}
	return string(payload)
}

func normalizeDocsRoleList(raw []string) []string {
	normalized := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		for _, part := range strings.Fields(item) {
			role := strings.TrimSpace(strings.ToLower(part))
			if role == "" || seen[role] {
				continue
			}
			seen[role] = true
			normalized = append(normalized, role)
		}
	}
	sort.Strings(normalized)
	return normalized
}

func normalizeDocsLibraryVisibility(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "private", "public":
		return strings.TrimSpace(strings.ToLower(raw))
	default:
		return "app"
	}
}

func normalizeDocsArticleStatus(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "published", "archived":
		return strings.TrimSpace(strings.ToLower(raw))
	default:
		return "draft"
	}
}

func buildDocsVersionHref(librarySlug, articleSlug string, versionNum int) string {
	values := url.Values{}
	values.Set("library", librarySlug)
	values.Set("article", articleSlug)
	if versionNum > 0 {
		values.Set("version", fmt.Sprintf("%d", versionNum))
	}
	return "/docs/manage?" + values.Encode()
}
