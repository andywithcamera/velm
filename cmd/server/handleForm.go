package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"velm/internal/auth"
	"velm/internal/db"
)

type formReferenceOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type formFieldConfig struct {
	Kind                string
	InputType           string
	ReadOnly            bool
	Step                string
	Placeholder         string
	ReferenceTo         string
	ReferenceTableLabel string
	ReferenceRows       []formReferenceOption
	ConditionExpr       string
}

type formVariantOption struct {
	Name     string
	Label    string
	Href     string
	Selected bool
}

type formSecurityPreviewResponse struct {
	SaveAllowed   bool     `json:"save_allowed"`
	Message       string   `json:"message,omitempty"`
	BlockedFields []string `json:"blocked_fields,omitempty"`
}

const defaultJavaScriptBusinessScript = `async function run(ctx) {
  console.log("Hello, world");
}`

func handleForm(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/f/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		http.Error(w, "Invalid form path", http.StatusBadRequest)
		return
	}

	tableName, id := parts[0], parts[1]
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if !requireAdminTableAccess(w, r, tableName) {
		return
	}
	if !db.IsValidRecordID(tableName, id) {
		http.Error(w, "Invalid record ID", http.StatusBadRequest)
		return
	}
	if db.GetTableContext(r.Context(), tableName).ID == "" {
		http.Error(w, "Table not found", http.StatusNotFound)
		return
	}

	formName := normalizeRuntimeFormName(r.URL.Query().Get("form"))
	viewData, err := buildFormViewData(w, r, r.URL.Path, "Form: "+tableName, "Builder", tableName, id, formName)
	if err != nil {
		status := http.StatusBadRequest
		switch strings.TrimSpace(strings.ToLower(err.Error())) {
		case "permission denied":
			status = http.StatusForbidden
		case "form not found":
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}

	// Render form
	err = templates.ExecuteTemplate(w, "layout.html", viewData)
	if err != nil {
		http.Error(w, "Failed to render form", http.StatusInternalServerError)
	}
}

func handleFormSecurityPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	tableName := strings.TrimSpace(strings.ToLower(r.FormValue("table_name")))
	recordID := strings.TrimSpace(r.FormValue("record_id"))
	formName := normalizeRuntimeFormName(r.FormValue("form_name"))
	if recordID == "" {
		recordID = "new"
	}
	if !db.IsSafeIdentifier(tableName) {
		http.Error(w, "Invalid table name", http.StatusBadRequest)
		return
	}
	if !db.IsValidRecordID(tableName, recordID) {
		http.Error(w, "Invalid record ID", http.StatusBadRequest)
		return
	}
	if !requireAdminTableAccess(w, r, tableName) {
		return
	}

	xData := db.GetRecordContext(r.Context(), tableName, recordID)
	if xData.View == nil {
		http.Error(w, "Record not found", http.StatusNotFound)
		return
	}

	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	tableSecurityEvaluator, err := db.LoadTableSecurityEvaluator(r.Context(), tableName, userID)
	if err != nil {
		http.Error(w, "Failed to evaluate security rules", http.StatusInternalServerError)
		return
	}
	if _, _, _, found, err := resolveRuntimeFormSelection(r.Context(), tableName, formName); err != nil {
		http.Error(w, "Failed to evaluate security rules", http.StatusInternalServerError)
		return
	} else if formName != "" && !found {
		http.Error(w, "Form not found", http.StatusNotFound)
		return
	}
	formSecurityEvaluator, err := db.LoadFormSecurityEvaluator(r.Context(), tableName, formName, userID)
	if err != nil {
		http.Error(w, "Failed to evaluate security rules", http.StatusInternalServerError)
		return
	}

	isCreate := recordID == "" || recordID == "new"
	formData, nullColumns, err := parseFormSecurityPreviewValues(r.Context(), xData.View.Columns, tableName, userID, isCreate, r.PostForm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	oldSnapshot := map[string]any{}
	if !isCreate {
		if recordValues, ok := xData.Data.(map[string]any); ok {
			oldSnapshot = recordValues
		}
	}
	preview := db.MergeSecuritySavePreviews(
		db.BuildTableSecuritySavePreview(xData.View.Columns, tableSecurityEvaluator, isCreate, oldSnapshot, formData, nullColumns),
		db.BuildTableSecuritySavePreview(xData.View.Columns, formSecurityEvaluator, isCreate, oldSnapshot, formData, nullColumns),
	)
	response := buildFormSecurityPreviewResponse(preview, isCreate)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func buildFormViewData(w http.ResponseWriter, r *http.Request, uri, title, section, tableName, id, requestedFormName string) (map[string]any, error) {
	ctx := r.Context()
	if !db.IsSafeIdentifier(tableName) {
		return nil, fmt.Errorf("invalid table name")
	}
	if !db.IsValidRecordID(tableName, id) {
		return nil, fmt.Errorf("invalid record id")
	}
	if db.GetTableContext(ctx, tableName).ID == "" {
		return nil, fmt.Errorf("table not found")
	}

	xData := db.GetRecordContext(ctx, tableName, id)
	if xData.View == nil {
		return nil, fmt.Errorf("record not found")
	}
	requestedFormName = normalizeRuntimeFormName(requestedFormName)
	runtimeFormApp, selectedForm, availableForms, selectedFormFound, err := resolveRuntimeFormSelection(ctx, tableName, requestedFormName)
	if err != nil {
		return nil, err
	}
	if requestedFormName != "" && !selectedFormFound {
		return nil, fmt.Errorf("form not found")
	}
	formReadOnly := db.IsImmutableTableName(tableName)
	formColumns := orderedFormColumns(selectedForm, xData.View.Columns)
	formFieldConfigs := buildFormFieldConfigs(ctx, tableName, formColumns)
	formFieldValues := buildFormFieldValues(xData.Data, formFieldConfigs)
	formSecurityPreviewURL := ""
	hiddenFields := []formHiddenField{}
	if selectedFormFound {
		viewFormName := normalizeRuntimeFormName(selectedForm.Name)
		if viewFormName != "" && viewFormName != "default" {
			hiddenFields = append(hiddenFields, formHiddenField{Name: "form_name", Value: viewFormName})
		}
	}
	ensureReferenceFieldSelections(ctx, formFieldConfigs, formFieldValues)
	if id == "new" {
		applyFormPrefillValues(r, formFieldValues, formFieldConfigs)
		applySpecialFormDefaults(tableName, formFieldValues)
	}
	if userID := strings.TrimSpace(auth.UserIDFromRequest(r)); userID != "" {
		tableSecurityEvaluator, err := db.LoadTableSecurityEvaluator(ctx, tableName, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate security rules")
		}
		formSecurityEvaluator, err := db.LoadFormSecurityEvaluator(ctx, tableName, requestedFormName, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate security rules")
		}
		if tableSecurityEvaluator != nil || formSecurityEvaluator != nil {
			if id == "new" {
				createRecord := db.StringMapToAny(formFieldValues)
				if !formAllowsRecord("C", createRecord, tableSecurityEvaluator, formSecurityEvaluator) {
					return nil, fmt.Errorf("permission denied")
				}
				for _, column := range formColumns {
					if formAllowsField("C", column.NAME, createRecord, tableSecurityEvaluator, formSecurityEvaluator) {
						continue
					}
					cfg := formFieldConfigs[column.NAME]
					cfg.ReadOnly = true
					formFieldConfigs[column.NAME] = cfg
				}
			} else if recordValues, ok := xData.Data.(map[string]any); ok {
				if !formAllowsRecord("R", recordValues, tableSecurityEvaluator, formSecurityEvaluator) {
					return nil, fmt.Errorf("permission denied")
				}
				filteredColumns := make([]db.Column, 0, len(formColumns))
				for _, column := range formColumns {
					if !formAllowsField("R", column.NAME, recordValues, tableSecurityEvaluator, formSecurityEvaluator) {
						delete(formFieldConfigs, column.NAME)
						delete(formFieldValues, column.NAME)
						continue
					}
					filteredColumns = append(filteredColumns, column)
					if formAllowsField("U", column.NAME, recordValues, tableSecurityEvaluator, formSecurityEvaluator) {
						continue
					}
					cfg := formFieldConfigs[column.NAME]
					cfg.ReadOnly = true
					formFieldConfigs[column.NAME] = cfg
				}
				formColumns = filteredColumns
				if !formAllowsRecord("U", recordValues, tableSecurityEvaluator, formSecurityEvaluator) {
					formReadOnly = true
				}
			}
		}
		previewOperation := "U"
		if id == "new" {
			previewOperation = "C"
		}
		if !formReadOnly && formHasOperationRules(previewOperation, tableSecurityEvaluator, formSecurityEvaluator) {
			formSecurityPreviewURL = "/api/form/security-preview"
		}
	}

	viewData := newViewData(w, r, uri, title, section)
	viewData["FormTable"] = tableName
	viewData["FormID"] = id
	viewData["XData"] = xData
	viewData["FormTableLabel"] = formTableLabel(xData.View, tableName)
	viewData["FormTableDescription"] = formTableDescription(xData.View, tableName)
	viewData["FormColumns"] = formColumns
	viewData["FormFieldConfigs"] = formFieldConfigs
	viewData["FormFieldValues"] = formFieldValues
	viewData["FormHiddenFields"] = hiddenFields
	viewData["FormSecurityPreviewURL"] = formSecurityPreviewURL
	viewData["FormExpectedVersion"] = buildExpectedRecordVersionValue(xData.Data)
	if version := strings.TrimSpace(fmt.Sprint(viewData["FormExpectedVersion"])); version != "" {
		w.Header().Set("X-Velm-Record-Version", version)
	}
	viewData["FormReadOnly"] = formReadOnly
	viewData["FormIsDeleted"] = formFieldValues["_deleted_at"] != ""
	viewData["FormTimelineCanComment"] = id != "" && id != "new" && !formReadOnly
	viewData["FormTimelineItems"] = []formTimelineItem{}
	viewData["FormRelatedSections"] = []formRelatedSection{}
	viewData["FormRuntimeApp"] = ""
	viewData["FormClientScriptsJSON"] = "[]"
	if runtimeFormApp.Name != "" {
		viewData["FormRuntimeApp"] = runtimeFormApp.Name
	}
	if app, ok, err := db.ResolveRuntimeAppByTable(ctx, tableName); err == nil && ok {
		viewData["FormRuntimeApp"] = app.Name
		if scripts, err := db.ListRuntimeClientScriptsForTable(ctx, tableName); err == nil && len(scripts) > 0 {
			if raw, err := json.Marshal(scripts); err == nil {
				viewData["FormClientScriptsJSON"] = string(raw)
			}
		}
	}
	if len(availableForms) > 1 {
		viewData["FormVariantOptions"] = buildFormVariantOptions(tableName, id, selectedForm, availableForms)
	}
	if id != "" && id != "new" {
		var (
			timelineItems   []formTimelineItem
			relatedSections []formRelatedSection
			wg              sync.WaitGroup
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			timelineItems = loadFormTimeline(ctx, tableName, id, formColumns)
		}()
		go func() {
			defer wg.Done()
			relatedSections = loadFormRelatedSections(ctx, r, tableName, id)
		}()
		wg.Wait()
		viewData["FormTimelineItems"] = timelineItems
		viewData["FormRelatedSections"] = relatedSections
	}
	return viewData, nil
}

func parseFormSecurityPreviewValues(ctx context.Context, columns []db.Column, tableName, userID string, isCreate bool, values url.Values) (map[string]string, map[string]bool, error) {
	allowedColumns := map[string]bool{}
	editableColumns := map[string]bool{}
	columnMeta := map[string]db.Column{}
	for _, col := range columns {
		allowedColumns[col.NAME] = true
		columnMeta[col.NAME] = col
		if !strings.HasPrefix(col.NAME, "_") {
			editableColumns[col.NAME] = true
		}
	}

	formData := map[string]string{}
	nullColumns := map[string]bool{}
	for key, items := range values {
		if len(items) == 0 {
			continue
		}
		if key == "_id" || key == "table_name" || key == "record_id" || isFormSecurityPreviewTransportField(key) || strings.HasPrefix(key, "_") {
			continue
		}
		if !db.IsSafeIdentifier(key) || !editableColumns[key] {
			return nil, nil, fmt.Errorf("invalid field in request")
		}
		value := strings.TrimSpace(items[len(items)-1])
		normalized, isNull, err := db.NormalizeSubmittedColumnValue(columnMeta[key], value)
		if err != nil {
			return nil, nil, err
		}
		formData[key] = normalized
		if isNull {
			nullColumns[key] = true
		}
	}

	if taskTypeValue := db.TaskTypeValueForTable(ctx, tableName); taskTypeValue != "" && allowedColumns["work_type"] {
		formData["work_type"] = taskTypeValue
		delete(nullColumns, "work_type")
	}
	if userID != "" {
		if allowedColumns["_updated_by"] {
			formData["_updated_by"] = userID
			delete(nullColumns, "_updated_by")
		}
		if allowedColumns["_updated_at"] {
			formData["_updated_at"] = time.Now().Format(time.RFC3339Nano)
			delete(nullColumns, "_updated_at")
		}
		if isCreate {
			if allowedColumns["_created_by"] {
				formData["_created_by"] = userID
				delete(nullColumns, "_created_by")
			}
			if allowedColumns["_created_at"] {
				formData["_created_at"] = time.Now().Format(time.RFC3339Nano)
				delete(nullColumns, "_created_at")
			}
		}
	}

	return formData, nullColumns, nil
}

func isFormSecurityPreviewTransportField(name string) bool {
	switch strings.TrimSpace(name) {
	case "expected_version", "expected_updated_at", "csrf_token", "realtime_client_id", "form_name":
		return true
	default:
		return false
	}
}

func buildFormSecurityPreviewResponse(preview db.TableSecuritySavePreview, isCreate bool) formSecurityPreviewResponse {
	response := formSecurityPreviewResponse{
		SaveAllowed:   preview.SaveAllowed,
		BlockedFields: append([]string(nil), preview.BlockedFields...),
	}
	if preview.SaveAllowed {
		return response
	}
	if !preview.RecordAllowed {
		if isCreate {
			response.Message = "You can no longer create this record with the current values."
		} else {
			response.Message = "You can no longer save this record with the current values."
		}
		return response
	}
	if len(preview.BlockedFields) == 1 {
		response.Message = fmt.Sprintf("You can no longer save changes to %s.", preview.BlockedFields[0])
		return response
	}
	if len(preview.BlockedFields) > 1 {
		response.Message = "You can no longer save changes to some fields under the current security rules."
	}
	return response
}

func formAllowsRecord(operation string, record map[string]any, evaluators ...*db.TableSecurityEvaluator) bool {
	for _, evaluator := range evaluators {
		if evaluator == nil {
			continue
		}
		if !evaluator.AllowsRecord(operation, record) {
			return false
		}
	}
	return true
}

func formAllowsField(operation, field string, record map[string]any, evaluators ...*db.TableSecurityEvaluator) bool {
	for _, evaluator := range evaluators {
		if evaluator == nil {
			continue
		}
		if !evaluator.AllowsField(operation, field, record) {
			return false
		}
	}
	return true
}

func formHasOperationRules(operation string, evaluators ...*db.TableSecurityEvaluator) bool {
	for _, evaluator := range evaluators {
		if evaluator != nil && evaluator.HasOperationRules(operation) {
			return true
		}
	}
	return false
}

func normalizeRuntimeFormName(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func resolveRuntimeFormSelection(ctx context.Context, tableName, formName string) (db.RegisteredApp, db.AppDefinitionForm, []db.AppDefinitionForm, bool, error) {
	formName = normalizeRuntimeFormName(formName)
	app, table, ok, err := db.FindYAMLTableByName(ctx, tableName)
	if err != nil {
		return db.RegisteredApp{}, db.AppDefinitionForm{}, nil, false, err
	}
	if !ok {
		return db.RegisteredApp{}, db.AppDefinitionForm{}, nil, false, nil
	}
	forms := db.ResolveTableForms(table)
	form, found := db.ResolveTableForm(table, formName)
	return app, form, forms, found, nil
}

func recordFormHref(tableName, recordID, formName string) string {
	target := "/f/" + tableName + "/" + recordID
	formName = normalizeRuntimeFormName(formName)
	if formName == "" || formName == "default" {
		return target
	}
	return target + "?form=" + url.QueryEscape(formName)
}

func buildFormVariantOptions(tableName, recordID string, selectedForm db.AppDefinitionForm, forms []db.AppDefinitionForm) []formVariantOption {
	selectedName := normalizeRuntimeFormName(selectedForm.Name)
	options := make([]formVariantOption, 0, len(forms))
	for _, form := range forms {
		name := normalizeRuntimeFormName(form.Name)
		if name == "" {
			continue
		}
		label := strings.TrimSpace(form.Label)
		if label == "" {
			label = humanizeTimelineIdentifier(name)
		}
		options = append(options, formVariantOption{
			Name:     name,
			Label:    label,
			Href:     recordFormHref(tableName, recordID, name),
			Selected: name == selectedName || (selectedName == "" && name == "default"),
		})
	}
	return options
}

func formTableLabel(view *db.View, tableName string) string {
	if view != nil && view.Table != nil {
		label := strings.TrimSpace(view.Table.LABEL_SINGULAR)
		if label != "" {
			return label
		}
	}
	return humanizeTimelineIdentifier(tableName)
}

func formTableDescription(view *db.View, tableName string) string {
	if view != nil && view.Table != nil {
		description := strings.TrimSpace(view.Table.DESCRIPTION)
		if description != "" {
			return description
		}
	}
	return tableName
}

func orderedFormColumns(form db.AppDefinitionForm, columns []db.Column) []db.Column {
	if len(form.Fields) == 0 {
		return columns
	}

	columnByName := make(map[string]db.Column, len(columns))
	hiddenColumns := make([]db.Column, 0, len(columns))
	for _, column := range columns {
		columnByName[column.NAME] = column
		if strings.HasPrefix(column.NAME, "_") {
			hiddenColumns = append(hiddenColumns, column)
		}
	}

	ordered := make([]db.Column, 0, len(columns))
	seen := map[string]bool{}
	for _, field := range form.Fields {
		if column, ok := columnByName[field]; ok {
			ordered = append(ordered, column)
			seen[field] = true
		}
	}

	for _, column := range columns {
		if strings.HasPrefix(column.NAME, "_") {
			continue
		}
		if seen[column.NAME] {
			continue
		}
		ordered = append(ordered, column)
	}
	ordered = append(ordered, hiddenColumns...)
	return ordered
}

func buildFormFieldConfigs(ctx context.Context, currentTable string, columns []db.Column) map[string]formFieldConfig {
	configs := make(map[string]formFieldConfig, len(columns))
	tableExistsCache := make(map[string]bool, len(columns))
	referenceOptionsCache := make(map[string][]formReferenceOption, len(columns))
	referenceTableLabelCache := make(map[string]string, len(columns))
	taskTypeValue := db.TaskTypeValueForTable(ctx, currentTable)
	for _, col := range columns {
		cfg := formFieldConfig{
			Kind:        "text",
			InputType:   "text",
			Placeholder: col.LABEL,
		}

		dataType := strings.ToLower(strings.TrimSpace(col.DATA_TYPE))
		switch dataType {
		case "int", "integer", "float", "double", "decimal", "numeric":
			cfg.Kind = "number"
			cfg.InputType = "number"
			cfg.Step = numberStep(dataType)
		case "bool", "boolean":
			cfg.Kind = "bool"
			cfg.InputType = "checkbox"
		case "choice":
			cfg.Kind = "choice"
			cfg.InputType = "select"
			cfg.ReferenceRows = buildChoiceOptions(col)
		case "date":
			cfg.Kind = "date"
			cfg.InputType = "date"
		case "timestamp", "timestamptz", "datetime":
			cfg.Kind = "date"
			cfg.InputType = "datetime-local"
		case "long_text":
			cfg.Kind = "textarea"
			cfg.InputType = "textarea"
		case "reference":
			cfg.Kind = "reference"
			if col.REFERENCE_TABLE.Valid {
				cfg.ReferenceTo = strings.TrimSpace(col.REFERENCE_TABLE.String)
			}
		case "json", "jsonb":
			cfg.Kind = "json"
			cfg.InputType = "textarea"
		case "markdown":
			cfg.Kind = "markdown"
			cfg.InputType = "textarea"
		case "email":
			cfg.InputType = "email"
		case "url":
			cfg.InputType = "url"
		case "phone":
			cfg.InputType = "tel"
		case "autnumber":
			cfg.ReadOnly = true
		case "uuid":
			cfg.ReadOnly = true
		default:
			if strings.HasPrefix(dataType, "enum:") {
				cfg.Kind = "choice"
				cfg.InputType = "select"
				cfg.ReferenceRows = buildEnumOptions(strings.TrimPrefix(dataType, "enum:"))
			}
		}

		if col.NAME != "_id" && strings.HasSuffix(col.NAME, "_id") {
			refTable := inferReferenceTableCached(ctx, currentTable, col.NAME, tableExistsCache)
			if refTable != "" {
				cfg.Kind = "reference"
				cfg.InputType = "select"
				cfg.ReferenceTo = refTable
				cfg.ReadOnly = false
			}
		}
		if cfg.Kind == "reference" && cfg.ReferenceTo == "" {
			cfg.ReferenceTo = inferReferenceTableCached(ctx, currentTable, col.NAME, tableExistsCache)
		}
		if cfg.Kind == "reference" && cfg.ReferenceTo != "" {
			cfg.ReferenceTableLabel = referenceTableLabelCached(ctx, cfg.ReferenceTo, referenceTableLabelCache)
			cfg.ReferenceRows = fetchReferenceOptionsCached(ctx, cfg.ReferenceTo, referenceOptionsCache)
		}
		if currentTable == "base_task" && (col.NAME == "state_changed_at" || col.NAME == "started_at" || col.NAME == "closed_at") {
			cfg.ReadOnly = true
		}
		if col.NAME == "work_type" && taskTypeValue != "" {
			cfg.ReadOnly = true
		}
		if col.NAME == "_id" {
			cfg.ReadOnly = true
		}
		if col.CONDITION_EXPR.Valid {
			cfg.ConditionExpr = strings.TrimSpace(col.CONDITION_EXPR.String)
		}
		if db.IsImmutableTableName(currentTable) {
			cfg.ReadOnly = true
		}

		configs[col.NAME] = cfg
	}
	return configs
}

func numberStep(dataType string) string {
	switch dataType {
	case "int", "integer":
		return "1"
	default:
		return "any"
	}
}

func inferReferenceTable(currentTable, columnName string) string {
	return inferReferenceTableCached(context.Background(), currentTable, columnName, map[string]bool{})
}

func inferReferenceTableCached(ctx context.Context, currentTable, columnName string, tableExistsCache map[string]bool) string {
	columnName = strings.TrimSpace(strings.ToLower(columnName))
	if !strings.HasSuffix(columnName, "_id") {
		return ""
	}
	base := strings.TrimSuffix(columnName, "_id")
	switch base {
	case "user":
		return "_user"
	case "group":
		return "_group"
	case "role":
		return "_role"
	case "permission":
		return "_permission"
	case "entity":
		return "base_entity"
	case "task":
		return "base_task"
	}

	for _, prefix := range []string{"source_", "target_", "owner_", "parent_", "child_", "primary_", "secondary_", "responsible_", "requested_", "affected_", "related_"} {
		if !strings.HasPrefix(base, prefix) {
			continue
		}
		if inferred := inferReferenceTableCached(ctx, currentTable, strings.TrimPrefix(base, prefix)+"_id", tableExistsCache); inferred != "" {
			return inferred
		}
	}

	candidates := []string{base, "_" + base}
	if strings.HasSuffix(base, "y") {
		candidates = append(candidates, strings.TrimSuffix(base, "y")+"ies")
	}
	candidates = append(candidates, base+"s", "_"+base+"s")

	for _, candidate := range candidates {
		if candidate == "" || candidate == currentTable {
			continue
		}
		if tableExistsCached(ctx, candidate, tableExistsCache) {
			return candidate
		}
	}
	return ""
}

func tableExistsCached(ctx context.Context, tableName string, cache map[string]bool) bool {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" {
		return false
	}
	if exists, ok := cache[tableName]; ok {
		return exists
	}
	exists := db.TableExists(ctx, tableName)
	cache[tableName] = exists
	return exists
}

func fetchReferenceOptions(ctx context.Context, tableName string) []formReferenceOption {
	return fetchReferenceOptionsFiltered(ctx, tableName, "", 200, "")
}

func fetchReferenceOptionsCached(ctx context.Context, tableName string, cache map[string][]formReferenceOption) []formReferenceOption {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" {
		return nil
	}
	if options, ok := cache[tableName]; ok {
		return append([]formReferenceOption(nil), options...)
	}
	options := fetchReferenceOptions(ctx, tableName)
	cache[tableName] = append([]formReferenceOption(nil), options...)
	return append([]formReferenceOption(nil), options...)
}

func ensureReferenceFieldSelections(ctx context.Context, configs map[string]formFieldConfig, values map[string]string) {
	for fieldName, cfg := range configs {
		if cfg.Kind != "reference" || strings.TrimSpace(cfg.ReferenceTo) == "" {
			continue
		}

		value := strings.TrimSpace(values[fieldName])
		if value == "" || referenceOptionExists(cfg.ReferenceRows, value) {
			continue
		}

		option, ok := fetchReferenceOptionByID(ctx, cfg.ReferenceTo, value)
		if !ok {
			continue
		}

		cfg.ReferenceRows = append([]formReferenceOption{option}, cfg.ReferenceRows...)
		configs[fieldName] = cfg
	}
}

func referenceOptionExists(options []formReferenceOption, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, option := range options {
		if strings.TrimSpace(option.Value) == value {
			return true
		}
	}
	return false
}

func fetchReferenceOptionByID(ctx context.Context, tableName, id string) (formReferenceOption, bool) {
	id = strings.TrimSpace(id)
	if !db.IsSafeIdentifier(tableName) || id == "" {
		return formReferenceOption{}, false
	}

	quotedTable, err := db.QuoteIdentifier(tableName)
	if err != nil {
		return formReferenceOption{}, false
	}

	labelColumn := referenceLabelColumnWithContext(ctx, tableName)
	quotedLabel, err := db.QuoteIdentifier(labelColumn)
	if err != nil {
		return formReferenceOption{}, false
	}

	query := fmt.Sprintf(
		`SELECT _id::text, COALESCE(CAST(%s AS text), _id::text)
		 FROM %s
		 WHERE _id::text = $1
		 LIMIT 1`,
		quotedLabel,
		quotedTable,
	)

	var option formReferenceOption
	if err := db.Pool.QueryRow(ctx, query, id).Scan(&option.Value, &option.Label); err != nil {
		return formReferenceOption{}, false
	}
	return option, true
}

func buildChoiceOptions(column db.Column) []formReferenceOption {
	if len(column.CHOICES) > 0 {
		options := make([]formReferenceOption, 0, len(column.CHOICES))
		for _, choice := range column.CHOICES {
			value := strings.TrimSpace(choice.Value)
			if value == "" {
				continue
			}
			label := strings.TrimSpace(choice.Label)
			if label == "" {
				label = humanizeTimelineIdentifier(value)
			}
			options = append(options, formReferenceOption{
				Value: value,
				Label: label,
			})
		}
		return options
	}
	return buildEnumOptions(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(column.DATA_TYPE)), "enum:"))
}

func buildEnumOptions(enumValues string) []formReferenceOption {
	parts := strings.Split(enumValues, "|")
	options := make([]formReferenceOption, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		options = append(options, formReferenceOption{
			Value: value,
			Label: humanizeTimelineIdentifier(value),
		})
	}
	return options
}

func fetchReferenceOptionsFiltered(ctx context.Context, tableName, query string, limit int, selected string) []formReferenceOption {
	if !db.IsSafeIdentifier(tableName) {
		return nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	quotedTable, err := db.QuoteIdentifier(tableName)
	if err != nil {
		return nil
	}

	labelColumn := referenceLabelColumnWithContext(ctx, tableName)
	quotedLabel, err := db.QuoteIdentifier(labelColumn)
	if err != nil {
		return nil
	}

	sqlQuery := fmt.Sprintf(
		`SELECT _id::text, COALESCE(CAST(%s AS text), _id::text) AS label
		 FROM %s
		 WHERE ($1 = '' OR CAST(%s AS text) ILIKE $2 OR _id::text ILIKE $2 OR $3 = _id::text)
		 ORDER BY label
		 LIMIT $4`,
		quotedLabel,
		quotedTable,
		quotedLabel,
	)
	search := strings.TrimSpace(query)
	selected = strings.TrimSpace(selected)
	rows, err := db.Pool.Query(ctx, sqlQuery, search, "%"+search+"%", selected, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()

	options := make([]formReferenceOption, 0, limit)
	for rows.Next() {
		var option formReferenceOption
		if err := rows.Scan(&option.Value, &option.Label); err != nil {
			continue
		}
		options = append(options, option)
	}
	sort.Slice(options, func(i, j int) bool {
		return options[i].Label < options[j].Label
	})
	return options
}

func referenceLabelColumn(tableName string) string {
	view := db.GetViewContext(context.Background(), tableName)
	return preferredDisplayColumn(view)
}

func referenceLabelColumnWithContext(ctx context.Context, tableName string) string {
	view := db.GetViewContext(ctx, tableName)
	return preferredDisplayColumn(view)
}

func referenceTableLabel(tableName string) string {
	return referenceTableLabelWithContext(context.Background(), tableName)
}

func referenceTableLabelWithContext(ctx context.Context, tableName string) string {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" {
		return "Record"
	}

	if table := db.GetTableContext(ctx, tableName); table.ID != "" {
		if label := strings.TrimSpace(table.LABEL_SINGULAR); label != "" {
			return label
		}
	}

	return humanizeTimelineIdentifier(strings.TrimPrefix(tableName, "_"))
}

func referenceTableLabelCached(ctx context.Context, tableName string, cache map[string]string) string {
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if tableName == "" {
		return "Record"
	}
	if label, ok := cache[tableName]; ok {
		return label
	}
	label := referenceTableLabelWithContext(ctx, tableName)
	cache[tableName] = label
	return label
}

func preferredDisplayColumn(view db.View) string {
	if view.Table == nil {
		return "_id"
	}

	if field := strings.TrimSpace(view.Table.DISPLAY_FIELD); field != "" {
		for _, col := range view.Columns {
			if strings.EqualFold(strings.TrimSpace(col.NAME), field) {
				return col.NAME
			}
		}
	}

	names := make([]string, 0, len(view.Columns))
	for _, col := range view.Columns {
		names = append(names, col.NAME)
	}
	if inferred := db.InferDisplayFieldName(names); inferred != "" {
		for _, col := range view.Columns {
			if strings.EqualFold(col.NAME, inferred) {
				return col.NAME
			}
		}
	}
	for _, col := range view.Columns {
		if strings.EqualFold(col.NAME, "_id") {
			return col.NAME
		}
	}
	if len(view.Columns) == 0 {
		return "_id"
	}
	return view.Columns[0].NAME
}

func buildFormFieldValues(raw any, configs map[string]formFieldConfig) map[string]string {
	values := map[string]string{}
	row, ok := raw.(map[string]any)
	if !ok {
		return values
	}
	for key, v := range row {
		cfg := configs[key]
		values[key] = formatFieldValue(v, cfg)
	}
	return values
}

func buildExpectedRecordVersionValue(raw any) string {
	row, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if version, ok := row["_update_count"]; ok && version != nil {
		return strings.TrimSpace(fmt.Sprint(version))
	}
	return formatExactTimestampValue(row["_updated_at"])
}

func applyFormPrefillValues(r *http.Request, values map[string]string, configs map[string]formFieldConfig) {
	query := r.URL.Query()
	for key := range configs {
		if strings.HasPrefix(key, "_") {
			continue
		}
		if _, exists := values[key]; exists && strings.TrimSpace(values[key]) != "" {
			continue
		}
		prefill := strings.TrimSpace(query.Get(key))
		if prefill == "" {
			continue
		}
		values[key] = prefill
	}
}

func applySpecialFormDefaults(tableName string, values map[string]string) {
	if tableName == "base_task" {
		if strings.TrimSpace(values["state_changed_at"]) == "" {
			values["state_changed_at"] = time.Now().Format("2006-01-02T15:04")
		}
	}
	if taskTypeValue := db.TaskTypeValueForTable(context.Background(), tableName); taskTypeValue != "" && strings.TrimSpace(values["work_type"]) == "" {
		values["work_type"] = taskTypeValue
	}
}

func formatFieldValue(value any, cfg formFieldConfig) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return formatStringValue(v, cfg)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case time.Time:
		switch cfg.InputType {
		case "date":
			return v.Format("2006-01-02")
		case "datetime-local":
			return v.Format("2006-01-02T15:04")
		default:
			return v.Format(time.RFC3339)
		}
	default:
		return fmt.Sprint(v)
	}
}

func formatStringValue(v string, cfg formFieldConfig) string {
	if cfg.InputType == "date" && len(v) >= 10 {
		return v[:10]
	}
	if cfg.InputType == "datetime-local" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t.Format("2006-01-02T15:04")
		}
		if len(v) >= 16 {
			return v[:16]
		}
	}
	return v
}

func formatExactTimestampValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return ""
		}
		if t, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return t.Format(time.RFC3339Nano)
		}
		if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
			return t.Format(time.RFC3339Nano)
		}
		return trimmed
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
