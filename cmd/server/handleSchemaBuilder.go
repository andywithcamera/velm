package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

func handleSchemaBuilderPage(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	apps, err := db.ListActiveApps(ctx)
	if err != nil {
		http.Error(w, "Failed to load applications", http.StatusInternalServerError)
		return
	}
	selectedApp := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("app")))
	if selectedApp == "" && len(apps) > 0 {
		selectedApp = apps[0].Name
	}

	tables, err := db.ListBuilderTables(ctx)
	if err != nil {
		http.Error(w, "Failed to load schema builder tables", http.StatusInternalServerError)
		return
	}
	filteredTables := filterBuilderTablesForApp(tables, apps, selectedApp)

	selectedTable := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("table")))
	if selectedTable != "" && !builderTableInList(filteredTables, selectedTable) {
		selectedTable = ""
	}
	if selectedTable == "" && len(filteredTables) > 0 {
		selectedTable = filteredTables[0].Name
	}

	columns := []db.BuilderColumnSummary{}
	selectedTableMeta := db.BuilderTableSummary{}
	if selectedTable != "" {
		columns, err = db.ListBuilderColumns(ctx, selectedTable)
		if err != nil {
			http.Error(w, "Failed to load table columns", http.StatusInternalServerError)
			return
		}
		selectedTableMeta, err = db.GetBuilderTable(ctx, selectedTable)
		if err != nil {
			http.Error(w, "Failed to load selected table", http.StatusInternalServerError)
			return
		}
	}

	data := newViewData(w, r, "/builder/schema", "Schema Builder", "Builder")
	data["View"] = "schema-builder"
	data["BuilderApps"] = apps
	data["BuilderSelectedApp"] = selectedApp
	data["BuilderTables"] = filteredTables
	data["BuilderColumns"] = columns
	data["BuilderSelectedTable"] = selectedTable
	data["BuilderSelectedTableMeta"] = selectedTableMeta
	data["BuilderTableCreated"] = r.URL.Query().Get("created_table")
	data["BuilderColumnCreated"] = r.URL.Query().Get("created_column")
	data["BuilderSchemaJobID"] = r.URL.Query().Get("job_id")
	data["BuilderSchemaApplied"] = r.URL.Query().Get("schema_applied")
	data["BuilderSchemaPlanned"] = r.URL.Query().Get("schema_planned")

	jobIDRaw := strings.TrimSpace(r.URL.Query().Get("job_id"))
	if jobIDRaw != "" {
		jobID, err := strconv.ParseInt(jobIDRaw, 10, 64)
		if err == nil && jobID > 0 {
			job, err := db.GetBuilderSchemaJob(ctx, jobID)
			if err == nil {
				steps, err := db.ListBuilderSchemaJobSteps(ctx, jobID)
				if err == nil {
					data["BuilderSchemaJob"] = job
					data["BuilderSchemaJobSteps"] = steps
				}
			}
		}
	}

	if err := templates.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, "Error rendering schema builder", http.StatusInternalServerError)
	}
}

func handleCreateBuilderTable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	name := r.FormValue("name")
	labelSingular := r.FormValue("label_singular")
	labelPlural := r.FormValue("label_plural")
	description := r.FormValue("description")
	appName := strings.TrimSpace(strings.ToLower(r.FormValue("app_name")))

	if err := db.CreateAppTable(context.Background(), appName, name, labelSingular, labelPlural, description, userID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = loadMenuFromDB()

	http.Redirect(w, r, schemaBuilderURL(appName, strings.ToLower(strings.TrimSpace(name)), "created_table=1"), http.StatusSeeOther)
}

func handleCreateBuilderColumn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	tableName := r.FormValue("table_name")
	name := r.FormValue("name")
	label := r.FormValue("label")
	dataType := r.FormValue("data_type")
	defaultValue := r.FormValue("default_value")
	prefix := r.FormValue("prefix")
	validationRegex := r.FormValue("validation_regex")
	validationExpr := r.FormValue("validation_expr")
	conditionExpr := r.FormValue("condition_expr")
	validationMessage := r.FormValue("validation_message")
	referenceTable := r.FormValue("reference_table")
	choicesRaw := strings.TrimSpace(r.FormValue("choices"))
	appName := strings.TrimSpace(strings.ToLower(r.FormValue("app_name")))
	isNullable := r.FormValue("is_nullable") == "on" || r.FormValue("is_nullable") == "true" || r.FormValue("is_nullable") == "1"
	choices := []db.ChoiceOption{}
	if choicesRaw != "" {
		if err := json.Unmarshal([]byte(choicesRaw), &choices); err != nil {
			http.Error(w, "choices must be a JSON array of {value,label} objects", http.StatusBadRequest)
			return
		}
	}

	if err := db.CreateAppColumn(context.Background(), tableName, name, label, dataType, defaultValue, prefix, validationRegex, validationExpr, conditionExpr, validationMessage, referenceTable, choices, userID, isNullable); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	redirectTable := strings.ToLower(strings.TrimSpace(tableName))
	http.Redirect(w, r, schemaBuilderURL(appName, redirectTable, "created_column=1"), http.StatusSeeOther)
}

func handleDeleteBuilderTable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	appName := strings.TrimSpace(strings.ToLower(r.FormValue("app_name")))
	tableName := r.FormValue("table_name")
	if err := db.DeleteAppTable(context.Background(), tableName); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = loadMenuFromDB()

	http.Redirect(w, r, schemaBuilderURL(appName, "", ""), http.StatusSeeOther)
}

func handleDeleteBuilderColumn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	appName := strings.TrimSpace(strings.ToLower(r.FormValue("app_name")))
	tableName := r.FormValue("table_name")
	columnName := r.FormValue("column_name")
	if err := db.DeleteAppColumn(context.Background(), tableName, columnName); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	redirectTable := strings.ToLower(strings.TrimSpace(tableName))
	http.Redirect(w, r, schemaBuilderURL(appName, redirectTable, ""), http.StatusSeeOther)
}

func handleUpdateBuilderTable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	tableName := r.FormValue("table_name")
	labelSingular := r.FormValue("label_singular")
	labelPlural := r.FormValue("label_plural")
	description := r.FormValue("description")
	appName := strings.TrimSpace(strings.ToLower(r.FormValue("app_name")))

	if err := db.UpdateAppTable(context.Background(), tableName, labelSingular, labelPlural, description, userID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_ = loadMenuFromDB()

	redirectTable := strings.ToLower(strings.TrimSpace(tableName))
	http.Redirect(w, r, schemaBuilderURL(appName, redirectTable, ""), http.StatusSeeOther)
}

func handleUpdateBuilderColumn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	tableName := r.FormValue("table_name")
	columnName := r.FormValue("column_name")
	label := r.FormValue("label")
	defaultValue := r.FormValue("default_value")
	prefix := r.FormValue("prefix")
	validationRegex := r.FormValue("validation_regex")
	validationExpr := r.FormValue("validation_expr")
	conditionExpr := r.FormValue("condition_expr")
	validationMessage := r.FormValue("validation_message")
	referenceTable := r.FormValue("reference_table")
	choicesRaw := strings.TrimSpace(r.FormValue("choices"))
	appName := strings.TrimSpace(strings.ToLower(r.FormValue("app_name")))
	isNullable := r.FormValue("is_nullable") == "on" || r.FormValue("is_nullable") == "true" || r.FormValue("is_nullable") == "1"
	choices := []db.ChoiceOption{}
	if choicesRaw != "" {
		if err := json.Unmarshal([]byte(choicesRaw), &choices); err != nil {
			http.Error(w, "choices must be a JSON array of {value,label} objects", http.StatusBadRequest)
			return
		}
	}

	if err := db.UpdateAppColumn(context.Background(), tableName, columnName, label, defaultValue, prefix, validationRegex, validationExpr, conditionExpr, validationMessage, referenceTable, choices, userID, isNullable); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	redirectTable := strings.ToLower(strings.TrimSpace(tableName))
	http.Redirect(w, r, schemaBuilderURL(appName, redirectTable, ""), http.StatusSeeOther)
}

func handleApplyBuilderSchema(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	userID := auth.UserIDFromRequest(r)
	if userID == "" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	tableName := strings.ToLower(strings.TrimSpace(r.FormValue("table_name")))
	appName := strings.TrimSpace(strings.ToLower(r.FormValue("app_name")))
	mode := strings.ToLower(strings.TrimSpace(r.FormValue("mode")))
	dryRun := mode != "apply"

	jobID, err := db.PlanAndApplyBuilderSchema(context.Background(), tableName, dryRun, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	flag := "schema_planned=1"
	if !dryRun {
		flag = "schema_applied=1"
	}
	http.Redirect(w, r, schemaBuilderURL(appName, tableName, "job_id="+strconv.FormatInt(jobID, 10)+"&"+flag), http.StatusSeeOther)
}

func filterBuilderTablesForApp(tables []db.BuilderTableSummary, apps []db.RegisteredApp, selectedApp string) []db.BuilderTableSummary {
	if selectedApp == "" {
		return tables
	}
	filtered := make([]db.BuilderTableSummary, 0, len(tables))
	for _, table := range tables {
		if builderTableBelongsToApp(table.Name, apps, selectedApp) {
			filtered = append(filtered, table)
		}
	}
	return filtered
}

func builderTableBelongsToApp(tableName string, apps []db.RegisteredApp, selectedApp string) bool {
	selectedApp = strings.TrimSpace(strings.ToLower(selectedApp))
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if selectedApp == "" || tableName == "" {
		return false
	}
	for _, app := range apps {
		if app.Name != selectedApp && app.Namespace != selectedApp {
			continue
		}
		if app.Definition != nil {
			for _, table := range app.Definition.Tables {
				if table.Name == tableName {
					return true
				}
			}
			return false
		}
		prefix := strings.TrimSpace(strings.ToLower(app.Namespace)) + "_"
		return prefix != "_" && strings.HasPrefix(tableName, prefix)
	}
	return false
}

func builderTableInList(tables []db.BuilderTableSummary, tableName string) bool {
	for _, table := range tables {
		if table.Name == tableName {
			return true
		}
	}
	return false
}

func schemaBuilderURL(appName, tableName, extraQuery string) string {
	path := "/builder/schema"
	params := make([]string, 0, 3)
	if appName = strings.TrimSpace(strings.ToLower(appName)); appName != "" {
		params = append(params, "app="+appName)
	}
	if tableName = strings.TrimSpace(strings.ToLower(tableName)); tableName != "" {
		params = append(params, "table="+tableName)
	}
	if extraQuery = strings.TrimSpace(extraQuery); extraQuery != "" {
		params = append(params, extraQuery)
	}
	if len(params) == 0 {
		return path
	}
	return fmt.Sprintf("%s?%s", path, strings.Join(params, "&"))
}
