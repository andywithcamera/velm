package main

// Task catalogue (2026-11-09): one page where every task definition can be
// found, searched, and started. The catalogue is the front door for spawning
// work; the board is where spawned work is tracked.
//
// Spelling: Andy asked for "catalogue" across the user-facing surface
// (2026-11-16). The URL, the titles and the copy say catalogue; Go identifiers,
// template names, CSS classes and the database keep "catalog", because
// renaming those would buy nothing a reader sees.

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

type taskCatalogEntry struct {
	Slug        string
	DisplayName string
	Description string
	Category    string
	IsContainer bool
	// StepCount is the number of blocks a container will open when it is
	// started. Zero for a leaf, which opens nothing but itself.
	StepCount   int
	InputFields []db.TaskInputField
}

// HasInputs reports whether starting this definition opens a form rather than
// spawning immediately.
func (e taskCatalogEntry) HasInputs() bool { return len(e.InputFields) > 0 }

// KindLabel names the entry as a chain or a single task, with the step count
// for a chain. This is the fix for the legibility bug: Andy ordered a leaf
// believing it was the container, got exactly one task, and nothing on the
// page had told him the difference.
func (e taskCatalogEntry) KindLabel() string {
	if !e.IsContainer {
		return "Single task"
	}
	return fmt.Sprintf("Chain · %d %s", e.StepCount, stepWord(e.StepCount))
}

// StartLabel is the button text: a chain says so before it is clicked, not
// after it has opened.
func (e taskCatalogEntry) StartLabel() string {
	if e.IsContainer {
		return "Start chain…"
	}
	return "Start…"
}

func stepWord(n int) string {
	if n == 1 {
		return "step"
	}
	return "steps"
}

type taskCatalogCategory struct {
	Name    string
	Entries []taskCatalogEntry
}

func loadTaskCatalog(ctx context.Context, query string) ([]taskCatalogCategory, error) {
	rows, err := db.Pool.Query(ctx,
		`SELECT d.slug, d.display_name, d.description, d.category, d.is_container,
		        COALESCE(d.input_schema_yaml, ''),
		        (SELECT count(*) FROM base_task_definition_child c
		          WHERE c.container_definition_id = d._id)
		 FROM base_task_definition d
		 WHERE d._deleted_at IS NULL
		 ORDER BY d.display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	needle := strings.ToLower(strings.TrimSpace(query))
	byCategory := map[string][]taskCatalogEntry{}
	categories := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var e taskCatalogEntry
		var category, inputSchema string
		if err := rows.Scan(&e.Slug, &e.DisplayName, &e.Description, &category, &e.IsContainer, &inputSchema, &e.StepCount); err != nil {
			return nil, err
		}
		// A malformed schema must not hide the definition: the card still
		// renders and the error surfaces when someone tries to start it.
		if fields, err := db.ParseTaskInputSchema(inputSchema); err == nil {
			e.InputFields = fields
		} else {
			log.Printf("task definition %q has an unreadable input schema: %v", e.Slug, err)
		}
		if category == "" {
			category = "general"
		}
		if needle != "" &&
			!strings.Contains(strings.ToLower(e.DisplayName), needle) &&
			!strings.Contains(strings.ToLower(e.Description), needle) &&
			!strings.Contains(strings.ToLower(e.Slug), needle) {
			continue
		}
		if !seen[category] {
			seen[category] = true
			categories = append(categories, category)
		}
		byCategory[category] = append(byCategory[category], e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(categories)

	out := make([]taskCatalogCategory, 0, len(categories))
	for _, name := range categories {
		out = append(out, taskCatalogCategory{Name: name, Entries: byCategory[name]})
	}
	return out, nil
}

func handleTaskCatalogPage(w http.ResponseWriter, r *http.Request) {
	if db.Pool == nil {
		http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
		return
	}
	viewData := newViewData(w, r, "/task-catalogue", "Task Catalogue", "Operations")
	catalog, err := loadTaskCatalog(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		http.Error(w, "Failed to load task catalogue", http.StatusInternalServerError)
		return
	}
	total := 0
	for _, c := range catalog {
		total += len(c.Entries)
	}
	viewData["View"] = "task-catalog"
	viewData["TaskCatalogCategories"] = catalog
	viewData["TaskCatalogQuery"] = strings.TrimSpace(r.URL.Query().Get("q"))
	viewData["TaskCatalogTotal"] = total
	viewData["TaskCatalogOrderURL"] = "/task-catalogue/order"
	if r.Header.Get("HX-Request") == "true" {
		// HTMX search swaps only the results region, not the whole page.
		if err := templates.ExecuteTemplate(w, "task-catalog-results", viewData); err != nil {
			http.Error(w, "Failed to render task catalogue", http.StatusInternalServerError)
		}
		return
	}
	if err := templates.ExecuteTemplate(w, "layout.html", viewData); err != nil {
		http.Error(w, "Failed to render task catalogue", http.StatusInternalServerError)
	}
}

// handleTaskCatalogueLegacyRedirect keeps the pre-spelling URL working: the
// page lived at /task-catalogue's old path /task-catalog from 2026-11-09 until
// the spelling change, so bookmarks and links in older docs must not 404.
// A 301 is permanent on purpose — the old path is not coming back.
func handleTaskCatalogueLegacyRedirect(w http.ResponseWriter, r *http.Request) {
	target := "/task-catalogue"
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

// handleTaskCatalogOrder spawns a definition instance from the catalogue's
// "Start" form. Same engine path as POST /api/task-instances; the catalogue
// just wraps it in a form so the experience needs no client scripting.
func handleTaskCatalogOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	slug := strings.TrimSpace(r.PostFormValue("definition_slug"))
	if slug == "" {
		http.Error(w, "definition_slug is required", http.StatusBadRequest)
		return
	}
	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Unauthenticated", http.StatusUnauthorized)
		return
	}
	inputs, ierr := catalogOrderInputs(r.Context(), slug, r.PostForm)
	if ierr != nil {
		log.Printf("catalog order %q by %s rejected: %v", slug, userID, ierr)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = catalogOrderInvalidTmpl.Execute(w, map[string]string{"Slug": slug, "Reason": ierr.Error()})
		return
	}
	rootID, err := runtimeSpawnTaskBySlug(r.Context(), db.Pool, slug, "", userID, inputs)
	if err != nil {
		// Log the underlying error; the client sees a static message plus the
		// (escaped) slug. Rendered through html/template so no user-controlled
		// string is ever concatenated into markup (CodeQL go/reflected-xss).
		log.Printf("catalog order %q by %s failed: %v", slug, userID, err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_ = catalogOrderErrorTmpl.Execute(w, map[string]string{"Slug": slug})
		return
	}
	// The confirmation has to say what came into existence: a chain that opened
	// steps is a different outcome from a single task, and Andy ordered the
	// leaf believing it was the chain precisely because nothing said so.
	number := ""
	steps := 0
	if root, children, derr := runtimeDescribeTaskInstace(r.Context(), db.Pool, rootID); derr == nil {
		number = root.Number
		steps = len(children)
	}
	isContainer := catalogDefinitionIsContainer(r.Context(), slug) || steps > 0
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = catalogOrderDoneTmpl.Execute(w, map[string]string{
		"TaskID":  rootID,
		"Number":  number,
		"Message": catalogOrderConfirmation(number, isContainer, steps),
	})
}

// catalogDefinitionIsContainer reads the flag that decides how the order
// confirmation describes the run. A read failure falls back to "not a
// container"; the step count then decides, and the spawn has already
// succeeded, so this must never turn a success into an error.
func catalogDefinitionIsContainer(ctx context.Context, slug string) bool {
	var isContainer bool
	err := db.Pool.QueryRow(ctx,
		`SELECT is_container FROM base_task_definition
		 WHERE slug = $1 AND _deleted_at IS NULL`, slug).Scan(&isContainer)
	return err == nil && isContainer
}

// catalogOrderConfirmation is the sentence the order fragment shows. Pure, so
// the wording is testable without a database or a spawn: "Started TASK-152 as
// a chain: 1 step opened" must stay true for 0, 1 and many steps.
func catalogOrderConfirmation(number string, isContainer bool, steps int) string {
	if !isContainer {
		return fmt.Sprintf("Started %s as a single task: no steps opened.", number)
	}
	if steps == 0 {
		return fmt.Sprintf("Started %s as a chain: no steps to open yet.", number)
	}
	return fmt.Sprintf("Started %s as a chain: %d %s opened.", number, steps, stepWord(steps))
}

// catalogOrderInputs resolves the requester's form against the definition's
// declared inputs. A definition that declares nothing collects nothing, which
// keeps every existing "Start" button working unchanged.
func catalogOrderInputs(ctx context.Context, slug string, form url.Values) (map[string]any, error) {
	var raw string
	err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(input_schema_yaml, '') FROM base_task_definition
		 WHERE slug = $1 AND _deleted_at IS NULL`, slug).Scan(&raw)
	if err != nil {
		return nil, nil // spawn reports an unknown definition with its own message
	}
	return resolveCatalogInputs(raw, form)
}

// resolveCatalogInputs is the part that does not touch the database, so the
// validation rules are testable without a Postgres harness.
func resolveCatalogInputs(schemaYAML string, form url.Values) (map[string]any, error) {
	fields, err := db.ParseTaskInputSchema(schemaYAML)
	if err != nil {
		return nil, fmt.Errorf("this definition's input form is misconfigured: %w", err)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	submitted := make(map[string]string, len(fields))
	for _, field := range fields {
		submitted[field.Name] = form.Get(field.Name)
	}
	return db.TaskInputValues(fields, submitted)
}

// catalogOrderErrorTmpl / catalogOrderDoneTmpl render the HTMX swap fragments
// for the order endpoint. html/template context-encodes every interpolation,
// which is both the XSS defence and what the code scanner expects.
var catalogOrderErrorTmpl = template.Must(template.New("catalogOrderError").Parse(
	`<div class="ui-catalog-order-error">Could not start {{.Slug}}: the definition could not be spawned. See the server log for details.</div>`))

var catalogOrderInvalidTmpl = template.Must(template.New("catalogOrderInvalid").Parse(
	`<div class="ui-catalog-order-error">Could not start {{.Slug}}: {{.Reason}}</div>`))

var catalogOrderDoneTmpl = template.Must(template.New("catalogOrderDone").Parse(
	`<div class="ui-catalog-order-done">{{.Message}} <a href="/f/base_task/{{.TaskID}}">Open the task</a> · track it on the <a href="/task">board</a>.</div>`))
