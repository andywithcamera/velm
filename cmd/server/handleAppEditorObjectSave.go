package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

func handleCreateAppEditorApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	namespace := strings.TrimSpace(strings.ToLower(r.FormValue("namespace")))
	label := strings.TrimSpace(r.FormValue("label"))
	description := strings.TrimSpace(r.FormValue("description"))
	userID := auth.UserIDFromRequest(r)

	if err := db.CreateApp(r.Context(), namespace, label, description, userID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = loadMenuFromDB()
	w.Header().Set("X-Velm-Form-Message", "App created")
	w.Header().Set("HX-Redirect", fmt.Sprintf("/admin/app-editor?app=%s&active=%s", url.QueryEscape(namespace), url.QueryEscape("application:"+namespace)))
	w.WriteHeader(http.StatusNoContent)
}

func handleAppEditorObjectSave(w http.ResponseWriter, r *http.Request) {
	handleAppEditorDefinitionWrite(w, r, false)
}

func handleAppEditorPublish(w http.ResponseWriter, r *http.Request) {
	handleAppEditorDefinitionWrite(w, r, true)
}

func handleAppEditorObjectDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	appName := strings.TrimSpace(strings.ToLower(r.FormValue("app_name")))
	objectID := strings.TrimSpace(r.FormValue("object_id"))
	if objectID == "" {
		http.Error(w, "Missing object id", http.StatusBadRequest)
		return
	}
	resolvedAppName := resolveAppEditorAppName(r.Context(), appName, objectID)
	if resolvedAppName == "" {
		http.Error(w, "App name is required", http.StatusBadRequest)
		return
	}

	if err := deleteAppEditorObject(r.Context(), resolvedAppName, objectID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_ = loadMenuFromDB()
	redirectTarget := "/admin/app-editor?app=" + url.QueryEscape(resolvedAppName)
	if activeObjectID := strings.TrimSpace(resolveDeletedObjectID(objectID)); activeObjectID != "" {
		redirectTarget += "&active=" + url.QueryEscape(activeObjectID)
	}
	w.Header().Set("X-Velm-Form-Message", "Draft object deleted")
	writeAppEditorRedirect(w, r, redirectTarget)
}

func handleAppEditorDefinitionWrite(w http.ResponseWriter, r *http.Request, publish bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	appName := strings.TrimSpace(strings.ToLower(r.FormValue("app_name")))
	objectID := strings.TrimSpace(r.FormValue("object_id"))
	if objectID == "" && !publish {
		http.Error(w, "Missing object id", http.StatusBadRequest)
		return
	}
	resolvedAppName := resolveAppEditorAppName(r.Context(), appName, objectID)
	if resolvedAppName == "" {
		http.Error(w, "App name is required", http.StatusBadRequest)
		return
	}

	activeObjectID := objectID
	if objectID != "" {
		if err := saveAppEditorObject(r.Context(), resolvedAppName, objectID, r.PostForm); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		activeObjectID = resolveSavedObjectID(objectID, r.PostForm)
	}
	if publish {
		if err := db.PublishAppDefinition(r.Context(), resolvedAppName); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	_ = loadMenuFromDB()
	if publish {
		w.Header().Set("X-Velm-Form-Message", "App definition published")
	} else {
		w.Header().Set("X-Velm-Form-Message", "Draft saved")
	}
	redirectTarget := buildAppEditorRedirectTarget(r.PostForm, resolvedAppName, activeObjectID)
	writeAppEditorRedirect(w, r, redirectTarget)
}

func saveAppEditorObject(ctx context.Context, appName, objectID string, form map[string][]string) error {
	appName = resolveAppEditorAppName(ctx, appName, objectID)
	if appName == "" {
		return fmt.Errorf("app name is required")
	}

	app, err := db.GetActiveAppByName(ctx, appName)
	if err != nil {
		return err
	}
	definition := db.CloneAppDefinition(app.DraftDefinition)
	if definition == nil {
		definition = db.CloneAppDefinition(app.Definition)
	}
	if definition == nil {
		definition = &db.AppDefinition{
			Name:        app.Name,
			Namespace:   app.Namespace,
			Label:       app.Label,
			Description: app.Description,
		}
	}

	switch {
	case strings.HasPrefix(objectID, "application:"):
		definition.Label = strings.TrimSpace(formValue(form, "label"))
		definition.Description = strings.TrimSpace(formValue(form, "description"))
		dependencies, err := parseStringArrayJSON(formValue(form, "dependencies"))
		if err != nil {
			return fmt.Errorf("dependencies: %w", err)
		}
		definition.Dependencies = dependencies
	case strings.HasPrefix(objectID, "yaml:"):
		raw := formValue(form, "definition_yaml")
		parsed, err := db.ParseAppDefinition(raw)
		if err != nil {
			return err
		}
		if parsed == nil {
			return fmt.Errorf("definition yaml is required")
		}
		if strings.TrimSpace(parsed.Name) == "" {
			parsed.Name = app.Name
		}
		if strings.TrimSpace(parsed.Namespace) == "" {
			parsed.Namespace = app.Namespace
		}
		return db.SaveAppDefinition(ctx, app.Name, parsed)
	case objectID == "new:table":
		name := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if name == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range definition.Tables {
			if strings.EqualFold(existing.Name, name) {
				return fmt.Errorf("table %q already exists", name)
			}
		}
		definition.Tables = append(definition.Tables, db.AppDefinitionTable{
			Name:          name,
			Extends:       strings.TrimSpace(strings.ToLower(formValue(form, "extends"))),
			Extensible:    parseBoolFormValue(formValue(form, "extensible")),
			LabelSingular: strings.TrimSpace(formValue(form, "label_singular")),
			LabelPlural:   strings.TrimSpace(formValue(form, "label_plural")),
			DisplayField:  strings.TrimSpace(strings.ToLower(formValue(form, "display_field"))),
			Description:   strings.TrimSpace(formValue(form, "description")),
			Columns:       []db.AppDefinitionColumn{},
			Forms:         []db.AppDefinitionForm{},
			Lists:         []db.AppDefinitionList{},
		})
	case strings.HasPrefix(objectID, "table:"):
		tableName := strings.TrimSpace(strings.TrimPrefix(objectID, "table:"))
		table, index, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		table.LabelSingular = strings.TrimSpace(formValue(form, "label_singular"))
		table.LabelPlural = strings.TrimSpace(formValue(form, "label_plural"))
		table.DisplayField = strings.TrimSpace(strings.ToLower(formValue(form, "display_field")))
		table.Description = strings.TrimSpace(formValue(form, "description"))
		table.Extensible = parseBoolFormValue(formValue(form, "extensible"))
		forms, err := parseDefinitionFormsJSON(formValue(form, "forms"))
		if err != nil {
			return fmt.Errorf("forms: %w", err)
		}
		table.Forms = forms
		lists, err := parseDefinitionListsJSON(formValue(form, "lists"))
		if err != nil {
			return fmt.Errorf("lists: %w", err)
		}
		table.Lists = lists
		definition.Tables[index] = *table
	case objectID == "new:column":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table_name")))
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		name := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if name == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range table.Columns {
			if strings.EqualFold(existing.Name, name) {
				return fmt.Errorf("column %q already exists on %q", name, tableName)
			}
		}
		choices, err := parseChoiceOptionsJSON(formValue(form, "choices"))
		if err != nil {
			return fmt.Errorf("choices: %w", err)
		}
		definition.Tables[tableIndex].Columns = append(definition.Tables[tableIndex].Columns, db.AppDefinitionColumn{
			Name:              name,
			Label:             strings.TrimSpace(formValue(form, "label")),
			DataType:          strings.TrimSpace(formValue(form, "data_type")),
			IsNullable:        parseBoolFormValue(formValue(form, "is_nullable")),
			DefaultValue:      strings.TrimSpace(formValue(form, "default_value")),
			Prefix:            strings.TrimSpace(formValue(form, "prefix")),
			ValidationRegex:   strings.TrimSpace(formValue(form, "validation_regex")),
			ConditionExpr:     strings.TrimSpace(formValue(form, "condition_expr")),
			ValidationMessage: strings.TrimSpace(formValue(form, "validation_message")),
			ReferenceTable:    strings.TrimSpace(formValue(form, "reference_table")),
			Choices:           choices,
		})
	case objectID == "new:form":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table")))
		if tableName == "" {
			return fmt.Errorf(`field "table" is required`)
		}
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		formName := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if formName == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range table.Forms {
			if strings.EqualFold(existing.Name, formName) {
				return fmt.Errorf("form %q already exists on %q", formName, tableName)
			}
		}
		table.Forms = append(table.Forms, db.AppDefinitionForm{
			Name:        formName,
			Label:       strings.TrimSpace(formValue(form, "label")),
			Description: strings.TrimSpace(formValue(form, "description")),
			Fields:      []string{},
			Actions:     nil,
			Security:    db.AppDefinitionSecurity{},
		})
		definition.Tables[tableIndex] = *table
	case objectID == "new:data-policy":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table_name")))
		if tableName == "" {
			return fmt.Errorf(`field "table_name" is required`)
		}
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		policyName := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if policyName == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range table.DataPolicies {
			if strings.EqualFold(existing.Name, policyName) {
				return fmt.Errorf("data policy %q already exists on %q", policyName, tableName)
			}
		}
		table.DataPolicies = append(table.DataPolicies, db.AppDefinitionDataPolicy{
			Name:        policyName,
			Label:       strings.TrimSpace(formValue(form, "label")),
			Description: strings.TrimSpace(formValue(form, "description")),
			Condition:   strings.TrimSpace(formValue(form, "condition")),
			Action:      strings.TrimSpace(formValue(form, "action")),
			Enabled:     parseBoolFormValue(formValue(form, "enabled")),
		})
		definition.Tables[tableIndex] = *table
	case objectID == "new:table-trigger":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table_name")))
		if tableName == "" {
			return fmt.Errorf(`field "table_name" is required`)
		}
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		triggerName := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if triggerName == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range table.Triggers {
			if strings.EqualFold(existing.Name, triggerName) {
				return fmt.Errorf("trigger %q already exists on %q", triggerName, tableName)
			}
		}
		table.Triggers = append(table.Triggers, db.AppDefinitionTrigger{
			Name:        triggerName,
			Label:       strings.TrimSpace(formValue(form, "label")),
			Description: strings.TrimSpace(formValue(form, "description")),
			Event:       strings.TrimSpace(formValue(form, "event")),
			Table:       tableName,
			Condition:   strings.TrimSpace(formValue(form, "condition")),
			Call:        strings.TrimSpace(formValue(form, "call")),
			Mode:        strings.TrimSpace(formValue(form, "mode")),
			Order:       parseIntFormValue(formValue(form, "order")),
			Enabled:     parseBoolFormValue(formValue(form, "enabled")),
		})
		definition.Tables[tableIndex] = *table
	case objectID == "new:related-list":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table_name")))
		if tableName == "" {
			return fmt.Errorf(`field "table_name" is required`)
		}
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		relatedListName := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if relatedListName == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range table.RelatedLists {
			if strings.EqualFold(existing.Name, relatedListName) {
				return fmt.Errorf("related list %q already exists on %q", relatedListName, tableName)
			}
		}
		columns, err := parseStringArrayJSON(formValue(form, "columns"))
		if err != nil {
			return fmt.Errorf("columns: %w", err)
		}
		table.RelatedLists = append(table.RelatedLists, db.AppDefinitionRelatedList{
			Name:           relatedListName,
			Label:          strings.TrimSpace(formValue(form, "label")),
			Table:          strings.TrimSpace(strings.ToLower(formValue(form, "table"))),
			ReferenceField: strings.TrimSpace(strings.ToLower(formValue(form, "reference_field"))),
			Columns:        columns,
		})
		definition.Tables[tableIndex] = *table
	case objectID == "new:security-rule":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table_name")))
		if tableName == "" {
			return fmt.Errorf(`field "table_name" is required`)
		}
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		ruleName := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if ruleName == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range table.Security.Rules {
			if strings.EqualFold(existing.Name, ruleName) {
				return fmt.Errorf("security rule %q already exists on %q", ruleName, tableName)
			}
		}
		table.Security.Rules = append(table.Security.Rules, db.AppDefinitionSecurityRule{
			Name:        ruleName,
			Description: strings.TrimSpace(formValue(form, "description")),
			Effect:      strings.TrimSpace(strings.ToLower(formValue(form, "effect"))),
			Operation:   strings.TrimSpace(strings.ToUpper(formValue(form, "operation"))),
			Table:       tableName,
			Field:       strings.TrimSpace(strings.ToLower(formValue(form, "field"))),
			Condition:   strings.TrimSpace(formValue(form, "condition")),
			Role:        strings.TrimSpace(strings.ToLower(formValue(form, "role"))),
			Order:       parseIntFormValue(formValue(form, "order")),
		})
		definition.Tables[tableIndex] = *table
	case objectID == "new:dependency":
		name := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if name == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range definition.Dependencies {
			if strings.EqualFold(existing, name) {
				return fmt.Errorf("dependency %q already exists", name)
			}
		}
		definition.Dependencies = append(definition.Dependencies, name)
	case strings.HasPrefix(objectID, "form:"):
		formAsset, err := requireDefinitionFormAsset(definition, objectID)
		if err != nil {
			return err
		}
		formAsset.Label = strings.TrimSpace(formValue(form, "label"))
		formAsset.Description = strings.TrimSpace(formValue(form, "description"))
	case strings.HasPrefix(objectID, "form-layout:"):
		formAsset, err := requireDefinitionFormAssetByChild(definition, objectID, "form-layout:")
		if err != nil {
			return err
		}
		layout, err := parseStringArrayJSON(formValue(form, "layout"))
		if err != nil {
			return fmt.Errorf("layout: %w", err)
		}
		formAsset.Layout = layout
		formAsset.Fields = append([]string(nil), layout...)
	case strings.HasPrefix(objectID, "form-actions:"):
		formAsset, err := requireDefinitionFormAssetByChild(definition, objectID, "form-actions:")
		if err != nil {
			return err
		}
		actions, err := parseDefinitionActionsJSON(formValue(form, "actions"))
		if err != nil {
			return fmt.Errorf("actions: %w", err)
		}
		formAsset.Actions = actions
	case strings.HasPrefix(objectID, "form-security:"):
		formAsset, err := requireDefinitionFormAssetByChild(definition, objectID, "form-security:")
		if err != nil {
			return err
		}
		security, err := parseDefinitionSecurityJSON(formValue(form, "security"))
		if err != nil {
			return fmt.Errorf("security: %w", err)
		}
		formAsset.Security = security
	case strings.HasPrefix(objectID, "tableform:"):
		legacyForm, err := requireDefinitionLegacyTableForm(definition, objectID)
		if err != nil {
			return err
		}
		legacyForm.Label = strings.TrimSpace(formValue(form, "label"))
		legacyForm.Description = strings.TrimSpace(formValue(form, "description"))
	case strings.HasPrefix(objectID, "tableform-layout:"):
		legacyForm, err := requireDefinitionLegacyTableFormByChild(definition, objectID, "tableform-layout:")
		if err != nil {
			return err
		}
		layout, err := parseStringArrayJSON(formValue(form, "layout"))
		if err != nil {
			return fmt.Errorf("layout: %w", err)
		}
		legacyForm.Fields = layout
	case strings.HasPrefix(objectID, "tableform-actions:"):
		legacyForm, err := requireDefinitionLegacyTableFormByChild(definition, objectID, "tableform-actions:")
		if err != nil {
			return err
		}
		actions, err := parseDefinitionActionsJSON(formValue(form, "actions"))
		if err != nil {
			return fmt.Errorf("actions: %w", err)
		}
		legacyForm.Actions = actions
	case strings.HasPrefix(objectID, "tableform-security:"):
		legacyForm, err := requireDefinitionLegacyTableFormByChild(definition, objectID, "tableform-security:")
		if err != nil {
			return err
		}
		security, err := parseDefinitionSecurityJSON(formValue(form, "security"))
		if err != nil {
			return fmt.Errorf("security: %w", err)
		}
		legacyForm.Security = security
	case strings.HasPrefix(objectID, "table-data-policy:"):
		policy, _, err := requireDefinitionTableDataPolicy(definition, objectID)
		if err != nil {
			return err
		}
		policy.Label = strings.TrimSpace(formValue(form, "label"))
		policy.Description = strings.TrimSpace(formValue(form, "description"))
		policy.Condition = strings.TrimSpace(formValue(form, "condition"))
		policy.Action = strings.TrimSpace(formValue(form, "action"))
		policy.Enabled = parseBoolFormValue(formValue(form, "enabled"))
	case strings.HasPrefix(objectID, "table-trigger:"):
		trigger, _, err := requireDefinitionTableTrigger(definition, objectID)
		if err != nil {
			return err
		}
		trigger.Label = strings.TrimSpace(formValue(form, "label"))
		trigger.Description = strings.TrimSpace(formValue(form, "description"))
		trigger.Event = strings.TrimSpace(formValue(form, "event"))
		trigger.Condition = strings.TrimSpace(formValue(form, "condition"))
		trigger.Call = strings.TrimSpace(formValue(form, "call"))
		trigger.Mode = strings.TrimSpace(formValue(form, "mode"))
		trigger.Order = parseIntFormValue(formValue(form, "order"))
		trigger.Enabled = parseBoolFormValue(formValue(form, "enabled"))
	case strings.HasPrefix(objectID, "table-related-list:"):
		relatedList, _, err := requireDefinitionTableRelatedList(definition, objectID)
		if err != nil {
			return err
		}
		columns, err := parseStringArrayJSON(formValue(form, "columns"))
		if err != nil {
			return fmt.Errorf("columns: %w", err)
		}
		relatedList.Label = strings.TrimSpace(formValue(form, "label"))
		relatedList.Table = strings.TrimSpace(strings.ToLower(formValue(form, "table")))
		relatedList.ReferenceField = strings.TrimSpace(strings.ToLower(formValue(form, "reference_field")))
		relatedList.Columns = columns
	case strings.HasPrefix(objectID, "table-security-rule:"):
		rule, _, err := requireDefinitionTableSecurityRule(definition, objectID)
		if err != nil {
			return err
		}
		tableName, _, ok := parseAppEditorTableScopedID(objectID, "table-security-rule:")
		if !ok {
			return fmt.Errorf("invalid security rule object id")
		}
		rule.Name = strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		rule.Description = strings.TrimSpace(formValue(form, "description"))
		rule.Effect = strings.TrimSpace(strings.ToLower(formValue(form, "effect")))
		rule.Operation = strings.TrimSpace(strings.ToUpper(formValue(form, "operation")))
		rule.Table = tableName
		rule.Field = strings.TrimSpace(strings.ToLower(formValue(form, "field")))
		rule.Condition = strings.TrimSpace(formValue(form, "condition"))
		rule.Role = strings.TrimSpace(strings.ToLower(formValue(form, "role")))
		rule.Order = parseIntFormValue(formValue(form, "order"))
	case strings.HasPrefix(objectID, "column:"):
		tableName, columnName, ok := parseAppEditorColumnID(objectID)
		if !ok {
			return fmt.Errorf("invalid column object id")
		}
		column, _, _, err := requireDefinitionColumn(definition, tableName, columnName)
		if err != nil {
			return err
		}
		column.Label = strings.TrimSpace(formValue(form, "label"))
		column.DataType = strings.TrimSpace(formValue(form, "data_type"))
		column.IsNullable = parseBoolFormValue(formValue(form, "is_nullable"))
		column.Prefix = strings.TrimSpace(formValue(form, "prefix"))
		column.ReferenceTable = strings.TrimSpace(formValue(form, "reference_table"))
		choices, err := parseChoiceOptionsJSON(formValue(form, "choices"))
		if err != nil {
			return fmt.Errorf("choices: %w", err)
		}
		column.Choices = choices
		column.DefaultValue = strings.TrimSpace(formValue(form, "default_value"))
		column.ValidationRegex = strings.TrimSpace(formValue(form, "validation_regex"))
		column.ConditionExpr = strings.TrimSpace(formValue(form, "condition_expr"))
		column.ValidationMessage = strings.TrimSpace(formValue(form, "validation_message"))
	case objectID == "new:role":
		role := db.AppDefinitionRole{
			Name:        strings.TrimSpace(formValue(form, "name")),
			Label:       strings.TrimSpace(formValue(form, "label")),
			Description: strings.TrimSpace(formValue(form, "description")),
		}
		if role.Name == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range definition.Roles {
			if strings.EqualFold(existing.Name, role.Name) {
				return fmt.Errorf("role %q already exists", role.Name)
			}
		}
		definition.Roles = append(definition.Roles, role)
	case strings.HasPrefix(objectID, "role:"):
		role, err := requireDefinitionRole(definition, objectID)
		if err != nil {
			return err
		}
		role.Label = strings.TrimSpace(formValue(form, "label"))
		role.Description = strings.TrimSpace(formValue(form, "description"))
	case objectID == "new:service":
		service := db.AppDefinitionService{
			Name:        strings.TrimSpace(formValue(form, "name")),
			Label:       strings.TrimSpace(formValue(form, "label")),
			Description: strings.TrimSpace(formValue(form, "description")),
		}
		if service.Name == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range definition.Services {
			if strings.EqualFold(existing.Name, service.Name) {
				return fmt.Errorf("service %q already exists", service.Name)
			}
		}
		definition.Services = append(definition.Services, service)
	case strings.HasPrefix(objectID, "service:"):
		service, err := requireDefinitionService(definition, objectID)
		if err != nil {
			return err
		}
		service.Label = strings.TrimSpace(formValue(form, "label"))
		service.Description = strings.TrimSpace(formValue(form, "description"))
	case objectID == "new:method":
		serviceName := strings.TrimSpace(strings.ToLower(formValue(form, "service_name")))
		service, err := requireDefinitionServiceByName(definition, serviceName)
		if err != nil {
			return err
		}
		roles, err := parseStringArrayJSON(formValue(form, "roles"))
		if err != nil {
			return fmt.Errorf("roles: %w", err)
		}
		method := db.AppDefinitionMethod{
			Name:        strings.TrimSpace(formValue(form, "name")),
			Label:       strings.TrimSpace(formValue(form, "label")),
			Description: strings.TrimSpace(formValue(form, "description")),
			Visibility:  strings.TrimSpace(formValue(form, "visibility")),
			Language:    strings.TrimSpace(formValue(form, "language")),
			Roles:       roles,
			Script:      formValue(form, "script"),
		}
		if method.Name == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range service.Methods {
			if strings.EqualFold(existing.Name, method.Name) {
				return fmt.Errorf("method %q already exists on %q", method.Name, service.Name)
			}
		}
		service.Methods = append(service.Methods, method)
	case strings.HasPrefix(objectID, "method:"):
		method, err := requireDefinitionMethod(definition, objectID)
		if err != nil {
			return err
		}
		roles, err := parseStringArrayJSON(formValue(form, "roles"))
		if err != nil {
			return fmt.Errorf("roles: %w", err)
		}
		method.Label = strings.TrimSpace(formValue(form, "label"))
		method.Description = strings.TrimSpace(formValue(form, "description"))
		method.Visibility = strings.TrimSpace(formValue(form, "visibility"))
		method.Language = strings.TrimSpace(formValue(form, "language"))
		method.Roles = roles
		method.Script = formValue(form, "script")
	case objectID == "new:endpoint":
		roles, err := parseStringArrayJSON(formValue(form, "roles"))
		if err != nil {
			return fmt.Errorf("roles: %w", err)
		}
		endpoint := db.AppDefinitionEndpoint{
			Name:        strings.TrimSpace(formValue(form, "name")),
			Label:       strings.TrimSpace(formValue(form, "label")),
			Description: strings.TrimSpace(formValue(form, "description")),
			Method:      strings.TrimSpace(formValue(form, "method")),
			Path:        strings.TrimSpace(formValue(form, "path")),
			Call:        strings.TrimSpace(formValue(form, "call")),
			Roles:       roles,
			Enabled:     parseBoolFormValue(formValue(form, "enabled")),
		}
		if endpoint.Name == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range definition.Endpoints {
			if strings.EqualFold(existing.Name, endpoint.Name) {
				return fmt.Errorf("endpoint %q already exists", endpoint.Name)
			}
		}
		definition.Endpoints = append(definition.Endpoints, endpoint)
	case strings.HasPrefix(objectID, "endpoint:"):
		endpoint, err := requireDefinitionEndpoint(definition, objectID)
		if err != nil {
			return err
		}
		roles, err := parseStringArrayJSON(formValue(form, "roles"))
		if err != nil {
			return fmt.Errorf("roles: %w", err)
		}
		endpoint.Label = strings.TrimSpace(formValue(form, "label"))
		endpoint.Description = strings.TrimSpace(formValue(form, "description"))
		endpoint.Method = strings.TrimSpace(formValue(form, "method"))
		endpoint.Path = strings.TrimSpace(formValue(form, "path"))
		endpoint.Call = strings.TrimSpace(formValue(form, "call"))
		endpoint.Roles = roles
		endpoint.Enabled = parseBoolFormValue(formValue(form, "enabled"))
	case objectID == "new:trigger":
		trigger := db.AppDefinitionTrigger{
			Name:        strings.TrimSpace(formValue(form, "name")),
			Label:       strings.TrimSpace(formValue(form, "label")),
			Description: strings.TrimSpace(formValue(form, "description")),
			Event:       strings.TrimSpace(formValue(form, "event")),
			Table:       strings.TrimSpace(formValue(form, "table")),
			Condition:   strings.TrimSpace(formValue(form, "condition")),
			Call:        strings.TrimSpace(formValue(form, "call")),
			Mode:        strings.TrimSpace(formValue(form, "mode")),
			Order:       parseIntFormValue(formValue(form, "order")),
			Enabled:     parseBoolFormValue(formValue(form, "enabled")),
		}
		if trigger.Name == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range definition.Triggers {
			if strings.EqualFold(existing.Name, trigger.Name) {
				return fmt.Errorf("trigger %q already exists", trigger.Name)
			}
		}
		definition.Triggers = append(definition.Triggers, trigger)
	case strings.HasPrefix(objectID, "trigger:"):
		trigger, err := requireDefinitionTrigger(definition, objectID)
		if err != nil {
			return err
		}
		trigger.Label = strings.TrimSpace(formValue(form, "label"))
		trigger.Description = strings.TrimSpace(formValue(form, "description"))
		trigger.Event = strings.TrimSpace(formValue(form, "event"))
		trigger.Table = strings.TrimSpace(formValue(form, "table"))
		trigger.Condition = strings.TrimSpace(formValue(form, "condition"))
		trigger.Call = strings.TrimSpace(formValue(form, "call"))
		trigger.Mode = strings.TrimSpace(formValue(form, "mode"))
		trigger.Order = parseIntFormValue(formValue(form, "order"))
		trigger.Enabled = parseBoolFormValue(formValue(form, "enabled"))
	case objectID == "new:schedule":
		schedule := db.AppDefinitionSchedule{
			Name:        strings.TrimSpace(formValue(form, "name")),
			Label:       strings.TrimSpace(formValue(form, "label")),
			Description: strings.TrimSpace(formValue(form, "description")),
			Cron:        strings.TrimSpace(formValue(form, "cron")),
			Call:        strings.TrimSpace(formValue(form, "call")),
			Enabled:     parseBoolFormValue(formValue(form, "enabled")),
		}
		if schedule.Name == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range definition.Schedules {
			if strings.EqualFold(existing.Name, schedule.Name) {
				return fmt.Errorf("schedule %q already exists", schedule.Name)
			}
		}
		definition.Schedules = append(definition.Schedules, schedule)
	case strings.HasPrefix(objectID, "schedule:"):
		schedule, err := requireDefinitionSchedule(definition, objectID)
		if err != nil {
			return err
		}
		schedule.Label = strings.TrimSpace(formValue(form, "label"))
		schedule.Description = strings.TrimSpace(formValue(form, "description"))
		schedule.Cron = strings.TrimSpace(formValue(form, "cron"))
		schedule.Call = strings.TrimSpace(formValue(form, "call"))
		schedule.Enabled = parseBoolFormValue(formValue(form, "enabled"))
	case strings.HasPrefix(objectID, "dependency:"):
		dependencyIndex, err := requireDefinitionDependency(definition, objectID)
		if err != nil {
			return err
		}
		definition.Dependencies[dependencyIndex] = strings.TrimSpace(formValue(form, "name"))
	case strings.HasPrefix(objectID, "page:"):
		page, err := requireDefinitionPage(definition, objectID)
		if err != nil {
			return err
		}
		title := strings.TrimSpace(formValue(form, "title"))
		if title == "" {
			return fmt.Errorf(`field "title" is required`)
		}
		page.Name = title
		page.Label = title
		page.SearchKeywords = strings.TrimSpace(formValue(form, "search_keywords"))
		page.Content = formValue(form, "content")
		if strings.TrimSpace(page.EditorMode) == "" {
			page.EditorMode = "wysiwyg"
		}
		if strings.TrimSpace(page.Status) == "" {
			page.Status = "published"
		}
	case objectID == "new:page":
		title := strings.TrimSpace(formValue(form, "title"))
		if title == "" {
			return fmt.Errorf(`field "title" is required`)
		}
		slug := pageSlugFromTitle(title)
		if slug == "" {
			return fmt.Errorf(`field "title" must produce a valid page slug`)
		}
		page := db.AppDefinitionPage{
			Name:           title,
			Slug:           slug,
			Label:          title,
			SearchKeywords: strings.TrimSpace(formValue(form, "search_keywords")),
			EditorMode:     "wysiwyg",
			Status:         "published",
			Content:        formValue(form, "content"),
		}
		for _, existing := range definition.Pages {
			if strings.EqualFold(existing.Slug, page.Slug) {
				return fmt.Errorf("page %q already exists", page.Slug)
			}
		}
		definition.Pages = append(definition.Pages, page)
	case strings.HasPrefix(objectID, "page-layout:"):
		page, err := requireDefinitionPageByChild(definition, objectID, "page-layout:")
		if err != nil {
			return err
		}
		page.Content = formValue(form, "content")
	case strings.HasPrefix(objectID, "page-actions:"):
		page, err := requireDefinitionPageByChild(definition, objectID, "page-actions:")
		if err != nil {
			return err
		}
		actions, err := parseDefinitionActionsJSON(formValue(form, "actions"))
		if err != nil {
			return fmt.Errorf("actions: %w", err)
		}
		page.Actions = actions
	case strings.HasPrefix(objectID, "page-security:"):
		page, err := requireDefinitionPageByChild(definition, objectID, "page-security:")
		if err != nil {
			return err
		}
		security, err := parseDefinitionSecurityJSON(formValue(form, "security"))
		if err != nil {
			return fmt.Errorf("security: %w", err)
		}
		page.Security = security
	case objectID == "new:client-script":
		script := db.AppDefinitionClientScript{
			Name:        strings.TrimSpace(strings.ToLower(formValue(form, "name"))),
			Label:       strings.TrimSpace(formValue(form, "label")),
			Description: strings.TrimSpace(formValue(form, "description")),
			Table:       strings.TrimSpace(strings.ToLower(formValue(form, "table"))),
			Event:       strings.TrimSpace(strings.ToLower(formValue(form, "event"))),
			Field:       strings.TrimSpace(strings.ToLower(formValue(form, "field"))),
			Language:    strings.TrimSpace(strings.ToLower(formValue(form, "language"))),
			Script:      firstNonEmpty(strings.TrimSpace(formValue(form, "script")), defaultJavaScriptBusinessScript),
			Enabled:     parseBoolFormValue(formValue(form, "enabled")),
		}
		if script.Name == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range definition.ClientScripts {
			if strings.EqualFold(existing.Name, script.Name) {
				return fmt.Errorf("client script %q already exists", script.Name)
			}
		}
		definition.ClientScripts = append(definition.ClientScripts, script)
	case strings.HasPrefix(objectID, "client-script:"):
		script, err := requireDefinitionClientScript(definition, objectID)
		if err != nil {
			return err
		}
		script.Label = strings.TrimSpace(formValue(form, "label"))
		script.Description = strings.TrimSpace(formValue(form, "description"))
		script.Table = strings.TrimSpace(strings.ToLower(formValue(form, "table")))
		script.Event = strings.TrimSpace(strings.ToLower(formValue(form, "event")))
		script.Field = strings.TrimSpace(strings.ToLower(formValue(form, "field")))
		script.Language = strings.TrimSpace(strings.ToLower(formValue(form, "language")))
		script.Enabled = parseBoolFormValue(formValue(form, "enabled"))
	case strings.HasPrefix(objectID, "client-script-code:"):
		script, err := requireDefinitionClientScriptByChild(definition, objectID, "client-script-code:")
		if err != nil {
			return err
		}
		script.Script = formValue(form, "script")
	case strings.HasPrefix(objectID, "table-data-policies:"):
		tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "table-data-policies:")))
		table, index, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		policies, err := parseDefinitionDataPoliciesJSON(formValue(form, "data_policies"))
		if err != nil {
			return fmt.Errorf("data_policies: %w", err)
		}
		table.DataPolicies = policies
		definition.Tables[index] = *table
	case strings.HasPrefix(objectID, "table-triggers:"):
		tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "table-triggers:")))
		table, index, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		triggers, err := parseDefinitionTableTriggersJSON(formValue(form, "table_triggers"))
		if err != nil {
			return fmt.Errorf("table_triggers: %w", err)
		}
		table.Triggers = triggers
		definition.Tables[index] = *table
	case strings.HasPrefix(objectID, "table-related-lists:"):
		tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "table-related-lists:")))
		table, index, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		relatedLists, err := parseDefinitionRelatedListsJSON(formValue(form, "related_lists"))
		if err != nil {
			return fmt.Errorf("related_lists: %w", err)
		}
		table.RelatedLists = relatedLists
		definition.Tables[index] = *table
	case strings.HasPrefix(objectID, "table-security:"):
		tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "table-security:")))
		table, index, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		security, err := parseDefinitionSecurityJSON(formValue(form, "security"))
		if err != nil {
			return fmt.Errorf("security: %w", err)
		}
		table.Security = security
		definition.Tables[index] = *table
	case strings.HasPrefix(objectID, "seed:"):
		seed, err := requireDefinitionSeed(definition, objectID)
		if err != nil {
			return err
		}
		rows, err := parseSeedRowsJSON(formValue(form, "rows"))
		if err != nil {
			return fmt.Errorf("rows: %w", err)
		}
		seed.Rows = rows
	case objectID == "new:documentation":
		article := db.AppDefinitionDocumentation{
			Name:        strings.TrimSpace(strings.ToLower(formValue(form, "name"))),
			Label:       strings.TrimSpace(formValue(form, "label")),
			Description: strings.TrimSpace(formValue(form, "description")),
		}
		if article.Name == "" {
			return fmt.Errorf(`field "name" is required`)
		}
		for _, existing := range definition.Documentation {
			if strings.EqualFold(existing.Name, article.Name) {
				return fmt.Errorf("documentation article %q already exists", article.Name)
			}
		}
		definition.Documentation = append(definition.Documentation, article)
	case strings.HasPrefix(objectID, "documentation:"):
		article, err := requireDefinitionDocumentation(definition, objectID)
		if err != nil {
			return err
		}
		article.Label = strings.TrimSpace(formValue(form, "label"))
		article.Description = strings.TrimSpace(formValue(form, "description"))
	case strings.HasPrefix(objectID, "documentation-content:"):
		article, err := requireDefinitionDocumentationByChild(definition, objectID, "documentation-content:")
		if err != nil {
			return err
		}
		article.Content = formValue(form, "content")
	case strings.HasPrefix(objectID, "documentation-category:"):
		article, err := requireDefinitionDocumentationByChild(definition, objectID, "documentation-category:")
		if err != nil {
			return err
		}
		article.Category = strings.TrimSpace(formValue(form, "category"))
	case strings.HasPrefix(objectID, "documentation-visibility:"):
		article, err := requireDefinitionDocumentationByChild(definition, objectID, "documentation-visibility:")
		if err != nil {
			return err
		}
		article.Visibility = strings.TrimSpace(formValue(form, "visibility"))
	case strings.HasPrefix(objectID, "documentation-related:"):
		article, err := requireDefinitionDocumentationByChild(definition, objectID, "documentation-related:")
		if err != nil {
			return err
		}
		related, err := parseStringArrayJSON(formValue(form, "related"))
		if err != nil {
			return fmt.Errorf("related: %w", err)
		}
		article.Related = related
	default:
		return fmt.Errorf("unsupported app editor object")
	}

	return db.SaveAppDefinition(ctx, app.Name, definition)
}

func deleteAppEditorObject(ctx context.Context, appName, objectID string) error {
	appName = resolveAppEditorAppName(ctx, appName, objectID)
	if appName == "" {
		return fmt.Errorf("app name is required")
	}

	app, err := db.GetActiveAppByName(ctx, appName)
	if err != nil {
		return err
	}
	definition := db.CloneAppDefinition(app.DraftDefinition)
	if definition == nil {
		definition = db.CloneAppDefinition(app.Definition)
	}
	if definition == nil {
		return fmt.Errorf("definition is required")
	}

	switch {
	case strings.HasPrefix(objectID, "table:"):
		tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "table:")))
		for i := range definition.Tables {
			if definition.Tables[i].Name != tableName {
				continue
			}
			definition.Tables = append(definition.Tables[:i], definition.Tables[i+1:]...)
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("table %q not found", tableName)
	case strings.HasPrefix(objectID, "column:"):
		tableName, columnName, ok := parseAppEditorColumnID(objectID)
		if !ok {
			return fmt.Errorf("invalid column object id")
		}
		if db.IsSystemColumnName(columnName) {
			return fmt.Errorf("system columns cannot be removed")
		}
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		for i := range table.Columns {
			if table.Columns[i].Name != columnName {
				continue
			}
			table.Columns = append(table.Columns[:i], table.Columns[i+1:]...)
			definition.Tables[tableIndex] = *table
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("column %q not found on %q", columnName, tableName)
	case strings.HasPrefix(objectID, "form:"):
		name := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "form:")))
		for i := range definition.Forms {
			if definition.Forms[i].Name != name {
				continue
			}
			definition.Forms = append(definition.Forms[:i], definition.Forms[i+1:]...)
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("form %q not found", name)
	case strings.HasPrefix(objectID, "tableform:"):
		tableName, formName, ok := parseLegacyTableFormID(strings.TrimPrefix(objectID, "tableform:"))
		if !ok {
			return fmt.Errorf("invalid table form object id")
		}
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		for i := range table.Forms {
			name := strings.TrimSpace(strings.ToLower(table.Forms[i].Name))
			if name == "" {
				name = "default"
			}
			if name != formName {
				continue
			}
			table.Forms = append(table.Forms[:i], table.Forms[i+1:]...)
			definition.Tables[tableIndex] = *table
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("form %q not found on %q", formName, tableName)
	case strings.HasPrefix(objectID, "table-data-policy:"):
		tableName, policyName, ok := parseAppEditorTableScopedID(objectID, "table-data-policy:")
		if !ok {
			return fmt.Errorf("invalid data policy object id")
		}
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		for i := range table.DataPolicies {
			if table.DataPolicies[i].Name != policyName {
				continue
			}
			table.DataPolicies = append(table.DataPolicies[:i], table.DataPolicies[i+1:]...)
			definition.Tables[tableIndex] = *table
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("data policy %q not found on %q", policyName, tableName)
	case strings.HasPrefix(objectID, "table-trigger:"):
		tableName, triggerName, ok := parseAppEditorTableScopedID(objectID, "table-trigger:")
		if !ok {
			return fmt.Errorf("invalid table trigger object id")
		}
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		for i := range table.Triggers {
			if table.Triggers[i].Name != triggerName {
				continue
			}
			table.Triggers = append(table.Triggers[:i], table.Triggers[i+1:]...)
			definition.Tables[tableIndex] = *table
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("trigger %q not found on %q", triggerName, tableName)
	case strings.HasPrefix(objectID, "table-related-list:"):
		tableName, relatedListName, ok := parseAppEditorTableScopedID(objectID, "table-related-list:")
		if !ok {
			return fmt.Errorf("invalid related list object id")
		}
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		for i := range table.RelatedLists {
			if table.RelatedLists[i].Name != relatedListName {
				continue
			}
			table.RelatedLists = append(table.RelatedLists[:i], table.RelatedLists[i+1:]...)
			definition.Tables[tableIndex] = *table
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("related list %q not found on %q", relatedListName, tableName)
	case strings.HasPrefix(objectID, "table-security-rule:"):
		tableName, ruleName, ok := parseAppEditorTableScopedID(objectID, "table-security-rule:")
		if !ok {
			return fmt.Errorf("invalid security rule object id")
		}
		table, tableIndex, err := requireDefinitionTable(definition, tableName)
		if err != nil {
			return err
		}
		for i := range table.Security.Rules {
			if table.Security.Rules[i].Name != ruleName {
				continue
			}
			table.Security.Rules = append(table.Security.Rules[:i], table.Security.Rules[i+1:]...)
			definition.Tables[tableIndex] = *table
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("security rule %q not found on %q", ruleName, tableName)
	case strings.HasPrefix(objectID, "role:"):
		name, ok := parseAppEditorScopedName(objectID, "role:")
		if !ok {
			return fmt.Errorf("invalid role object id")
		}
		for i := range definition.Roles {
			if definition.Roles[i].Name != name {
				continue
			}
			definition.Roles = append(definition.Roles[:i], definition.Roles[i+1:]...)
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("role %q not found", name)
	case strings.HasPrefix(objectID, "service:"):
		name, ok := parseAppEditorScopedName(objectID, "service:")
		if !ok {
			return fmt.Errorf("invalid service object id")
		}
		for i := range definition.Services {
			if definition.Services[i].Name != name {
				continue
			}
			definition.Services = append(definition.Services[:i], definition.Services[i+1:]...)
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("service %q not found", name)
	case strings.HasPrefix(objectID, "method:"):
		_, serviceName, methodName, ok := parseAppEditorMethodID(objectID)
		if !ok {
			return fmt.Errorf("invalid method object id")
		}
		service, err := requireDefinitionServiceByName(definition, serviceName)
		if err != nil {
			return err
		}
		for i := range service.Methods {
			if service.Methods[i].Name != methodName {
				continue
			}
			service.Methods = append(service.Methods[:i], service.Methods[i+1:]...)
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("method %q not found on service %q", methodName, serviceName)
	case strings.HasPrefix(objectID, "trigger:"):
		name, ok := parseAppEditorScopedName(objectID, "trigger:")
		if !ok {
			return fmt.Errorf("invalid trigger object id")
		}
		for i := range definition.Triggers {
			if definition.Triggers[i].Name != name {
				continue
			}
			definition.Triggers = append(definition.Triggers[:i], definition.Triggers[i+1:]...)
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("trigger %q not found", name)
	case strings.HasPrefix(objectID, "schedule:"):
		name, ok := parseAppEditorScopedName(objectID, "schedule:")
		if !ok {
			return fmt.Errorf("invalid schedule object id")
		}
		for i := range definition.Schedules {
			if definition.Schedules[i].Name != name {
				continue
			}
			definition.Schedules = append(definition.Schedules[:i], definition.Schedules[i+1:]...)
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("schedule %q not found", name)
	case strings.HasPrefix(objectID, "dependency:"):
		index, err := requireDefinitionDependency(definition, objectID)
		if err != nil {
			return err
		}
		definition.Dependencies = append(definition.Dependencies[:index], definition.Dependencies[index+1:]...)
		return db.SaveAppDefinition(ctx, app.Name, definition)
	case strings.HasPrefix(objectID, "page:"):
		name, ok := parseAppEditorScopedName(objectID, "page:")
		if !ok {
			return fmt.Errorf("invalid page object id")
		}
		for i := range definition.Pages {
			if definition.Pages[i].Slug != name {
				continue
			}
			definition.Pages = append(definition.Pages[:i], definition.Pages[i+1:]...)
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("page %q not found", name)
	case strings.HasPrefix(objectID, "endpoint:"):
		name, ok := parseAppEditorScopedName(objectID, "endpoint:")
		if !ok {
			return fmt.Errorf("invalid endpoint object id")
		}
		for i := range definition.Endpoints {
			if definition.Endpoints[i].Name != name {
				continue
			}
			definition.Endpoints = append(definition.Endpoints[:i], definition.Endpoints[i+1:]...)
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("endpoint %q not found", name)
	case strings.HasPrefix(objectID, "client-script:"):
		name, ok := parseAppEditorScopedName(objectID, "client-script:")
		if !ok {
			return fmt.Errorf("invalid client script object id")
		}
		for i := range definition.ClientScripts {
			if definition.ClientScripts[i].Name != name {
				continue
			}
			definition.ClientScripts = append(definition.ClientScripts[:i], definition.ClientScripts[i+1:]...)
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("client script %q not found", name)
	case strings.HasPrefix(objectID, "seed:"):
		_, tableName, seedIndex, ok := parseAppEditorSeedID(objectID)
		if !ok {
			return fmt.Errorf("invalid seed object id")
		}
		if seedIndex < 0 || seedIndex >= len(definition.Seeds) {
			return fmt.Errorf("seed %q not found", tableName)
		}
		if definition.Seeds[seedIndex].Table != tableName {
			return fmt.Errorf("seed %q not found", tableName)
		}
		definition.Seeds = append(definition.Seeds[:seedIndex], definition.Seeds[seedIndex+1:]...)
		return db.SaveAppDefinition(ctx, app.Name, definition)
	case strings.HasPrefix(objectID, "documentation:"):
		article, err := requireDefinitionDocumentation(definition, objectID)
		if err != nil {
			return err
		}
		for i := range definition.Documentation {
			if definition.Documentation[i].Name != article.Name {
				continue
			}
			definition.Documentation = append(definition.Documentation[:i], definition.Documentation[i+1:]...)
			return db.SaveAppDefinition(ctx, app.Name, definition)
		}
		return fmt.Errorf("documentation article %q not found", article.Name)
	default:
		return fmt.Errorf("unsupported app editor object")
	}
}

func resolveAppEditorAppName(ctx context.Context, appName, objectID string) string {
	appName = strings.TrimSpace(strings.ToLower(appName))
	if appName != "" {
		return appName
	}
	switch {
	case strings.HasPrefix(objectID, "application:"):
		return strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "application:")))
	case strings.HasPrefix(objectID, "yaml:"):
		return strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "yaml:")))
	case strings.HasPrefix(objectID, "role:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "role:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "service:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "service:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "method:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "method:"), ":", 3)
		if len(parts) == 3 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "endpoint:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "endpoint:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "client-script:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "client-script:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "client-script-code:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "client-script-code:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "trigger:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "trigger:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "schedule:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "schedule:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "dependency:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "dependency:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "page:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "page:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "form:"):
		return strings.TrimSpace(strings.ToLower(appName))
	case strings.HasPrefix(objectID, "form-layout:"):
		return strings.TrimSpace(strings.ToLower(appName))
	case strings.HasPrefix(objectID, "form-actions:"):
		return strings.TrimSpace(strings.ToLower(appName))
	case strings.HasPrefix(objectID, "form-security:"):
		return strings.TrimSpace(strings.ToLower(appName))
	case strings.HasPrefix(objectID, "tableform:"):
		return strings.TrimSpace(strings.ToLower(appName))
	case strings.HasPrefix(objectID, "tableform-layout:"):
		return strings.TrimSpace(strings.ToLower(appName))
	case strings.HasPrefix(objectID, "tableform-actions:"):
		return strings.TrimSpace(strings.ToLower(appName))
	case strings.HasPrefix(objectID, "tableform-security:"):
		return strings.TrimSpace(strings.ToLower(appName))
	case strings.HasPrefix(objectID, "table-data-policies:"):
		if tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "table-data-policies:"))); tableName != "" {
			if app, _, ok, err := db.FindYAMLTableByName(ctx, tableName); err == nil && ok {
				return app.Name
			}
		}
	case strings.HasPrefix(objectID, "table-triggers:"):
		if tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "table-triggers:"))); tableName != "" {
			if app, _, ok, err := db.FindYAMLTableByName(ctx, tableName); err == nil && ok {
				return app.Name
			}
		}
	case strings.HasPrefix(objectID, "table-related-lists:"):
		if tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "table-related-lists:"))); tableName != "" {
			if app, _, ok, err := db.FindYAMLTableByName(ctx, tableName); err == nil && ok {
				return app.Name
			}
		}
	case strings.HasPrefix(objectID, "table-security:"):
		if tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "table-security:"))); tableName != "" {
			if app, _, ok, err := db.FindYAMLTableByName(ctx, tableName); err == nil && ok {
				return app.Name
			}
		}
	case strings.HasPrefix(objectID, "table-security-rule:"):
		if tableName, _, ok := parseAppEditorTableScopedID(objectID, "table-security-rule:"); ok {
			if app, _, ok, err := db.FindYAMLTableByName(ctx, tableName); err == nil && ok {
				return app.Name
			}
		}
	case strings.HasPrefix(objectID, "table-data-policy:"):
		if tableName, _, ok := parseAppEditorTableScopedID(objectID, "table-data-policy:"); ok {
			if app, _, ok, err := db.FindYAMLTableByName(ctx, tableName); err == nil && ok {
				return app.Name
			}
		}
	case strings.HasPrefix(objectID, "table-trigger:"):
		if tableName, _, ok := parseAppEditorTableScopedID(objectID, "table-trigger:"); ok {
			if app, _, ok, err := db.FindYAMLTableByName(ctx, tableName); err == nil && ok {
				return app.Name
			}
		}
	case strings.HasPrefix(objectID, "table-related-list:"):
		if tableName, _, ok := parseAppEditorTableScopedID(objectID, "table-related-list:"); ok {
			if app, _, ok, err := db.FindYAMLTableByName(ctx, tableName); err == nil && ok {
				return app.Name
			}
		}
	case strings.HasPrefix(objectID, "documentation:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "documentation:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "documentation-content:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "documentation-content:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "documentation-category:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "documentation-category:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "documentation-visibility:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "documentation-visibility:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "documentation-related:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "documentation-related:"), ":", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "seed:"):
		parts := strings.SplitN(strings.TrimPrefix(objectID, "seed:"), ":", 3)
		if len(parts) >= 2 {
			return strings.TrimSpace(strings.ToLower(parts[0]))
		}
	case strings.HasPrefix(objectID, "table:"):
		tableName := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "table:")))
		if app, _, ok, err := db.FindYAMLTableByName(ctx, tableName); err == nil && ok {
			return app.Name
		}
	case strings.HasPrefix(objectID, "column:"):
		tableName, _, ok := parseAppEditorColumnID(objectID)
		if ok {
			if app, _, ok, err := db.FindYAMLTableByName(ctx, tableName); err == nil && ok {
				return app.Name
			}
		}
	}
	return ""
}

func requireDefinitionTable(definition *db.AppDefinition, tableName string) (*db.AppDefinitionTable, int, error) {
	for i := range definition.Tables {
		if definition.Tables[i].Name == tableName {
			return &definition.Tables[i], i, nil
		}
	}
	return nil, -1, fmt.Errorf("table %q not found", tableName)
}

func requireDefinitionColumn(definition *db.AppDefinition, tableName, columnName string) (*db.AppDefinitionColumn, int, int, error) {
	table, tableIndex, err := requireDefinitionTable(definition, tableName)
	if err != nil {
		return nil, -1, -1, err
	}
	for i := range table.Columns {
		if table.Columns[i].Name == columnName {
			return &table.Columns[i], tableIndex, i, nil
		}
	}
	return nil, -1, -1, fmt.Errorf("column %q not found on %q", columnName, tableName)
}

func requireDefinitionFormAsset(definition *db.AppDefinition, objectID string) (*db.AppDefinitionAssetForm, error) {
	name := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, "form:")))
	for i := range definition.Forms {
		if definition.Forms[i].Name == name {
			return &definition.Forms[i], nil
		}
	}
	return nil, fmt.Errorf("form %q not found", name)
}

func requireDefinitionFormAssetByChild(definition *db.AppDefinition, objectID, prefix string) (*db.AppDefinitionAssetForm, error) {
	name := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(objectID, prefix)))
	return requireDefinitionFormAsset(definition, "form:"+name)
}

func requireDefinitionLegacyTableForm(definition *db.AppDefinition, objectID string) (*db.AppDefinitionForm, error) {
	tableName, formName, ok := parseLegacyTableFormID(strings.TrimPrefix(objectID, "tableform:"))
	if !ok {
		return nil, fmt.Errorf("invalid table form object id")
	}
	table, _, err := requireDefinitionTable(definition, tableName)
	if err != nil {
		return nil, err
	}
	for i := range table.Forms {
		name := strings.TrimSpace(strings.ToLower(table.Forms[i].Name))
		if name == "" {
			name = "default"
		}
		if name == formName {
			return &table.Forms[i], nil
		}
	}
	return nil, fmt.Errorf("form %q not found on %q", formName, tableName)
}

func requireDefinitionLegacyTableFormByChild(definition *db.AppDefinition, objectID, prefix string) (*db.AppDefinitionForm, error) {
	return requireDefinitionLegacyTableForm(definition, "tableform:"+strings.TrimPrefix(objectID, prefix))
}

func requireDefinitionTableDataPolicy(definition *db.AppDefinition, objectID string) (*db.AppDefinitionDataPolicy, int, error) {
	tableName, policyName, ok := parseAppEditorTableScopedID(objectID, "table-data-policy:")
	if !ok {
		return nil, -1, fmt.Errorf("invalid data policy object id")
	}
	table, tableIndex, err := requireDefinitionTable(definition, tableName)
	if err != nil {
		return nil, -1, err
	}
	for i := range table.DataPolicies {
		if table.DataPolicies[i].Name == policyName {
			return &table.DataPolicies[i], tableIndex, nil
		}
	}
	return nil, -1, fmt.Errorf("data policy %q not found on %q", policyName, tableName)
}

func requireDefinitionTableTrigger(definition *db.AppDefinition, objectID string) (*db.AppDefinitionTrigger, int, error) {
	tableName, triggerName, ok := parseAppEditorTableScopedID(objectID, "table-trigger:")
	if !ok {
		return nil, -1, fmt.Errorf("invalid table trigger object id")
	}
	table, tableIndex, err := requireDefinitionTable(definition, tableName)
	if err != nil {
		return nil, -1, err
	}
	for i := range table.Triggers {
		if table.Triggers[i].Name == triggerName {
			return &table.Triggers[i], tableIndex, nil
		}
	}
	return nil, -1, fmt.Errorf("trigger %q not found on %q", triggerName, tableName)
}

func requireDefinitionTableRelatedList(definition *db.AppDefinition, objectID string) (*db.AppDefinitionRelatedList, int, error) {
	tableName, relatedListName, ok := parseAppEditorTableScopedID(objectID, "table-related-list:")
	if !ok {
		return nil, -1, fmt.Errorf("invalid related list object id")
	}
	table, tableIndex, err := requireDefinitionTable(definition, tableName)
	if err != nil {
		return nil, -1, err
	}
	for i := range table.RelatedLists {
		if table.RelatedLists[i].Name == relatedListName {
			return &table.RelatedLists[i], tableIndex, nil
		}
	}
	return nil, -1, fmt.Errorf("related list %q not found on %q", relatedListName, tableName)
}

func requireDefinitionTableSecurityRule(definition *db.AppDefinition, objectID string) (*db.AppDefinitionSecurityRule, int, error) {
	tableName, ruleName, ok := parseAppEditorTableScopedID(objectID, "table-security-rule:")
	if !ok {
		return nil, -1, fmt.Errorf("invalid security rule object id")
	}
	table, tableIndex, err := requireDefinitionTable(definition, tableName)
	if err != nil {
		return nil, -1, err
	}
	for i := range table.Security.Rules {
		if table.Security.Rules[i].Name == ruleName {
			return &table.Security.Rules[i], tableIndex, nil
		}
	}
	return nil, -1, fmt.Errorf("security rule %q not found on %q", ruleName, tableName)
}

func requireDefinitionRole(definition *db.AppDefinition, objectID string) (*db.AppDefinitionRole, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "role:"), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid role object id")
	}
	name := strings.TrimSpace(strings.ToLower(parts[1]))
	for i := range definition.Roles {
		if definition.Roles[i].Name == name {
			return &definition.Roles[i], nil
		}
	}
	return nil, fmt.Errorf("role %q not found", name)
}

func requireDefinitionService(definition *db.AppDefinition, objectID string) (*db.AppDefinitionService, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "service:"), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid service object id")
	}
	return requireDefinitionServiceByName(definition, strings.TrimSpace(strings.ToLower(parts[1])))
}

func requireDefinitionServiceByName(definition *db.AppDefinition, serviceName string) (*db.AppDefinitionService, error) {
	for i := range definition.Services {
		if definition.Services[i].Name == serviceName {
			return &definition.Services[i], nil
		}
	}
	return nil, fmt.Errorf("service %q not found", serviceName)
}

func requireDefinitionMethod(definition *db.AppDefinition, objectID string) (*db.AppDefinitionMethod, error) {
	appName, serviceName, methodName, ok := parseAppEditorMethodID(objectID)
	_ = appName
	if !ok {
		return nil, fmt.Errorf("invalid method object id")
	}
	service, err := requireDefinitionServiceByName(definition, serviceName)
	if err != nil {
		return nil, err
	}
	for i := range service.Methods {
		if service.Methods[i].Name == methodName {
			return &service.Methods[i], nil
		}
	}
	return nil, fmt.Errorf("method %q not found on service %q", methodName, serviceName)
}

func requireDefinitionTrigger(definition *db.AppDefinition, objectID string) (*db.AppDefinitionTrigger, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "trigger:"), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid trigger object id")
	}
	name := strings.TrimSpace(strings.ToLower(parts[1]))
	for i := range definition.Triggers {
		if definition.Triggers[i].Name == name {
			return &definition.Triggers[i], nil
		}
	}
	return nil, fmt.Errorf("trigger %q not found", name)
}

func requireDefinitionSchedule(definition *db.AppDefinition, objectID string) (*db.AppDefinitionSchedule, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "schedule:"), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid schedule object id")
	}
	name := strings.TrimSpace(strings.ToLower(parts[1]))
	for i := range definition.Schedules {
		if definition.Schedules[i].Name == name {
			return &definition.Schedules[i], nil
		}
	}
	return nil, fmt.Errorf("schedule %q not found", name)
}

func requireDefinitionDependency(definition *db.AppDefinition, objectID string) (int, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "dependency:"), ":", 2)
	if len(parts) != 2 {
		return -1, fmt.Errorf("invalid dependency object id")
	}
	target := strings.TrimSpace(strings.ToLower(parts[1]))
	for i := range definition.Dependencies {
		if definition.Dependencies[i] == target {
			return i, nil
		}
	}
	return -1, fmt.Errorf("dependency %q not found", target)
}

func requireDefinitionPage(definition *db.AppDefinition, objectID string) (*db.AppDefinitionPage, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "page:"), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid page object id")
	}
	slug := strings.TrimSpace(strings.ToLower(parts[1]))
	for i := range definition.Pages {
		if definition.Pages[i].Slug == slug {
			return &definition.Pages[i], nil
		}
	}
	return nil, fmt.Errorf("page %q not found", slug)
}

func requireDefinitionPageByChild(definition *db.AppDefinition, objectID, prefix string) (*db.AppDefinitionPage, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, prefix), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid page object id")
	}
	return requireDefinitionPage(definition, "page:"+parts[0]+":"+parts[1])
}

func requireDefinitionEndpoint(definition *db.AppDefinition, objectID string) (*db.AppDefinitionEndpoint, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "endpoint:"), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid endpoint object id")
	}
	name := strings.TrimSpace(strings.ToLower(parts[1]))
	for i := range definition.Endpoints {
		if definition.Endpoints[i].Name == name {
			return &definition.Endpoints[i], nil
		}
	}
	return nil, fmt.Errorf("endpoint %q not found", name)
}

func requireDefinitionClientScript(definition *db.AppDefinition, objectID string) (*db.AppDefinitionClientScript, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "client-script:"), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid client script object id")
	}
	name := strings.TrimSpace(strings.ToLower(parts[1]))
	for i := range definition.ClientScripts {
		if definition.ClientScripts[i].Name == name {
			return &definition.ClientScripts[i], nil
		}
	}
	return nil, fmt.Errorf("client script %q not found", name)
}

func requireDefinitionClientScriptByChild(definition *db.AppDefinition, objectID, prefix string) (*db.AppDefinitionClientScript, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, prefix), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid client script object id")
	}
	return requireDefinitionClientScript(definition, "client-script:"+parts[0]+":"+parts[1])
}

func requireDefinitionSeed(definition *db.AppDefinition, objectID string) (*db.AppDefinitionSeed, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "seed:"), ":", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid seed object id")
	}
	tableName := strings.TrimSpace(strings.ToLower(parts[1]))
	for i := range definition.Seeds {
		if definition.Seeds[i].Table == tableName {
			return &definition.Seeds[i], nil
		}
	}
	return nil, fmt.Errorf("seed %q not found", tableName)
}

func requireDefinitionDocumentation(definition *db.AppDefinition, objectID string) (*db.AppDefinitionDocumentation, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "documentation:"), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid documentation object id")
	}
	name := strings.TrimSpace(strings.ToLower(parts[1]))
	for i := range definition.Documentation {
		if definition.Documentation[i].Name == name {
			return &definition.Documentation[i], nil
		}
	}
	return nil, fmt.Errorf("documentation article %q not found", name)
}

func requireDefinitionDocumentationByChild(definition *db.AppDefinition, objectID, prefix string) (*db.AppDefinitionDocumentation, error) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, prefix), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid documentation object id")
	}
	return requireDefinitionDocumentation(definition, "documentation:"+parts[0]+":"+parts[1])
}

func parseLegacyTableFormID(value string) (string, string, bool) {
	tableName, formName, ok := strings.Cut(strings.TrimSpace(strings.ToLower(value)), ":")
	if !ok || tableName == "" || formName == "" {
		return "", "", false
	}
	return tableName, formName, true
}

func parseAppEditorColumnID(objectID string) (string, string, bool) {
	rest := strings.TrimPrefix(objectID, "column:")
	tableName, columnName, ok := strings.Cut(rest, ":")
	if !ok || tableName == "" || columnName == "" {
		return "", "", false
	}
	return strings.TrimSpace(strings.ToLower(tableName)), strings.TrimSpace(strings.ToLower(columnName)), true
}

func parseAppEditorTableScopedID(objectID, prefix string) (string, string, bool) {
	rest := strings.TrimPrefix(objectID, prefix)
	tableName, objectName, ok := strings.Cut(rest, ":")
	if !ok || tableName == "" || objectName == "" {
		return "", "", false
	}
	return strings.TrimSpace(strings.ToLower(tableName)), strings.TrimSpace(strings.ToLower(objectName)), true
}

func parseAppEditorMethodID(objectID string) (string, string, string, bool) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "method:"), ":", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return strings.TrimSpace(strings.ToLower(parts[0])),
		strings.TrimSpace(strings.ToLower(parts[1])),
		strings.TrimSpace(strings.ToLower(parts[2])),
		true
}

func parseAppEditorScopedName(objectID, prefix string) (string, bool) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, prefix), ":", 2)
	if len(parts) != 2 {
		return "", false
	}
	return strings.TrimSpace(strings.ToLower(parts[1])), true
}

func parseAppEditorSeedID(objectID string) (string, string, int, bool) {
	parts := strings.SplitN(strings.TrimPrefix(objectID, "seed:"), ":", 3)
	if len(parts) != 3 {
		return "", "", 0, false
	}
	index, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return "", "", 0, false
	}
	return strings.TrimSpace(strings.ToLower(parts[0])),
		strings.TrimSpace(strings.ToLower(parts[1])),
		index,
		true
}

func parseBoolFormValue(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

func parseIntFormValue(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return n
}

func parseStringArrayJSON(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("must be a JSON array of strings")
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out, nil
}

func parseDefinitionFormsJSON(raw string) ([]db.AppDefinitionForm, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var forms []db.AppDefinitionForm
	if err := json.Unmarshal([]byte(raw), &forms); err != nil {
		return nil, fmt.Errorf("must be valid JSON")
	}
	return forms, nil
}

func parseDefinitionActionsJSON(raw string) ([]db.AppDefinitionAction, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var actions []db.AppDefinitionAction
	if err := json.Unmarshal([]byte(raw), &actions); err != nil {
		return nil, fmt.Errorf("must be valid JSON")
	}
	return actions, nil
}

func parseDefinitionSecurityJSON(raw string) (db.AppDefinitionSecurity, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return db.AppDefinitionSecurity{}, nil
	}
	var security db.AppDefinitionSecurity
	if err := json.Unmarshal([]byte(raw), &security); err != nil {
		return db.AppDefinitionSecurity{}, fmt.Errorf("must be valid JSON")
	}
	return security, nil
}

func parseDefinitionDataPoliciesJSON(raw string) ([]db.AppDefinitionDataPolicy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var policies []db.AppDefinitionDataPolicy
	if err := json.Unmarshal([]byte(raw), &policies); err != nil {
		return nil, fmt.Errorf("must be valid JSON")
	}
	return policies, nil
}

func parseDefinitionTableTriggersJSON(raw string) ([]db.AppDefinitionTrigger, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var triggers []db.AppDefinitionTrigger
	if err := json.Unmarshal([]byte(raw), &triggers); err != nil {
		return nil, fmt.Errorf("must be valid JSON")
	}
	return triggers, nil
}

func parseDefinitionRelatedListsJSON(raw string) ([]db.AppDefinitionRelatedList, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var items []db.AppDefinitionRelatedList
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, fmt.Errorf("must be valid JSON")
	}
	return items, nil
}

func parseDefinitionListsJSON(raw string) ([]db.AppDefinitionList, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var lists []db.AppDefinitionList
	if err := json.Unmarshal([]byte(raw), &lists); err != nil {
		return nil, fmt.Errorf("must be valid JSON")
	}
	return lists, nil
}

func parseSeedRowsJSON(raw string) ([]map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, fmt.Errorf("must be valid JSON")
	}
	return rows, nil
}

func parseChoiceOptionsJSON(raw string) ([]db.ChoiceOption, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var options []db.ChoiceOption
	if err := json.Unmarshal([]byte(raw), &options); err == nil {
		return options, nil
	}

	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("must be a JSON array of choice objects or strings")
	}

	options = make([]db.ChoiceOption, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		options = append(options, db.ChoiceOption{Value: value})
	}
	return options, nil
}

func formValue(values map[string][]string, key string) string {
	items := values[key]
	if len(items) == 0 {
		return ""
	}
	return items[0]
}

func pageSlugFromTitle(title string) string {
	title = strings.TrimSpace(strings.ToLower(title))
	if title == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(title))
	lastDash := false
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		default:
			if builder.Len() == 0 || lastDash {
				continue
			}
			builder.WriteByte('-')
			lastDash = true
		}
	}

	slug := strings.Trim(builder.String(), "-")
	if len(slug) > 64 {
		slug = strings.Trim(slug[:64], "-")
	}
	if !pageSlugPattern.MatchString(slug) {
		return ""
	}
	return slug
}

func resolveSavedObjectID(objectID string, form map[string][]string) string {
	switch objectID {
	case "new:table":
		name := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if name != "" {
			return "table:" + name
		}
	case "new:column":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table_name")))
		columnName := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if tableName != "" && columnName != "" {
			return "column:" + tableName + ":" + columnName
		}
	case "new:role":
		if name := strings.TrimSpace(strings.ToLower(formValue(form, "name"))); name != "" {
			return "role:" + strings.TrimSpace(strings.ToLower(formValue(form, "app_name"))) + ":" + name
		}
	case "new:form":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table")))
		name := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if tableName != "" && name != "" {
			return "tableform:" + tableName + ":" + name
		}
	case "new:data-policy":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table_name")))
		name := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if tableName != "" && name != "" {
			return "table-data-policy:" + tableName + ":" + name
		}
	case "new:table-trigger":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table_name")))
		name := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if tableName != "" && name != "" {
			return "table-trigger:" + tableName + ":" + name
		}
	case "new:related-list":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table_name")))
		name := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if tableName != "" && name != "" {
			return "table-related-list:" + tableName + ":" + name
		}
	case "new:security-rule":
		tableName := strings.TrimSpace(strings.ToLower(formValue(form, "table_name")))
		name := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		if tableName != "" && name != "" {
			return "table-security-rule:" + tableName + ":" + name
		}
	case "new:dependency":
		if name := strings.TrimSpace(strings.ToLower(formValue(form, "name"))); name != "" {
			return "dependency:" + strings.TrimSpace(strings.ToLower(formValue(form, "app_name"))) + ":" + name
		}
	case "new:page":
		if slug := pageSlugFromTitle(formValue(form, "title")); slug != "" {
			return "page:" + strings.TrimSpace(strings.ToLower(formValue(form, "app_name"))) + ":" + slug
		}
	case "new:client-script":
		name := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		appName := strings.TrimSpace(strings.ToLower(formValue(form, "app_name")))
		if appName != "" && name != "" {
			return "client-script:" + appName + ":" + name
		}
	case "new:documentation":
		if name := strings.TrimSpace(strings.ToLower(formValue(form, "name"))); name != "" {
			return "documentation:" + strings.TrimSpace(strings.ToLower(formValue(form, "app_name"))) + ":" + name
		}
	case "new:service":
		if name := strings.TrimSpace(strings.ToLower(formValue(form, "name"))); name != "" {
			return "service:" + strings.TrimSpace(strings.ToLower(formValue(form, "app_name"))) + ":" + name
		}
	case "new:method":
		serviceName := strings.TrimSpace(strings.ToLower(formValue(form, "service_name")))
		methodName := strings.TrimSpace(strings.ToLower(formValue(form, "name")))
		appName := strings.TrimSpace(strings.ToLower(formValue(form, "app_name")))
		if appName != "" && serviceName != "" && methodName != "" {
			return "method:" + appName + ":" + serviceName + ":" + methodName
		}
	case "new:endpoint":
		if name := strings.TrimSpace(strings.ToLower(formValue(form, "name"))); name != "" {
			return "endpoint:" + strings.TrimSpace(strings.ToLower(formValue(form, "app_name"))) + ":" + name
		}
	case "new:trigger":
		if name := strings.TrimSpace(strings.ToLower(formValue(form, "name"))); name != "" {
			return "trigger:" + strings.TrimSpace(strings.ToLower(formValue(form, "app_name"))) + ":" + name
		}
	case "new:schedule":
		if name := strings.TrimSpace(strings.ToLower(formValue(form, "name"))); name != "" {
			return "schedule:" + strings.TrimSpace(strings.ToLower(formValue(form, "app_name"))) + ":" + name
		}
	}
	return objectID
}

func resolveDeletedObjectID(objectID string) string {
	switch {
	case strings.HasPrefix(objectID, "column:"):
		tableName, _, ok := parseAppEditorColumnID(objectID)
		if ok {
			return "table:" + tableName
		}
	case strings.HasPrefix(objectID, "method:"):
		appName, serviceName, _, ok := parseAppEditorMethodID(objectID)
		if ok {
			return "service:" + appName + ":" + serviceName
		}
	case strings.HasPrefix(objectID, "tableform:"):
		if tableName, _, ok := parseLegacyTableFormID(strings.TrimPrefix(objectID, "tableform:")); ok {
			return "table:" + tableName
		}
	case strings.HasPrefix(objectID, "table-data-policy:"):
		if tableName, _, ok := parseAppEditorTableScopedID(objectID, "table-data-policy:"); ok {
			return "table:" + tableName
		}
	case strings.HasPrefix(objectID, "table-trigger:"):
		if tableName, _, ok := parseAppEditorTableScopedID(objectID, "table-trigger:"); ok {
			return "table:" + tableName
		}
	case strings.HasPrefix(objectID, "table-related-list:"):
		if tableName, _, ok := parseAppEditorTableScopedID(objectID, "table-related-list:"); ok {
			return "table:" + tableName
		}
	case strings.HasPrefix(objectID, "table-security-rule:"):
		if tableName, _, ok := parseAppEditorTableScopedID(objectID, "table-security-rule:"); ok {
			return "table:" + tableName
		}
	}
	return ""
}

func buildAppEditorRedirectTarget(form map[string][]string, appName, activeObjectID string) string {
	returnTo := strings.TrimSpace(formValue(form, "return_to"))
	if strings.HasPrefix(returnTo, "/admin/app-editor") {
		if activeObjectID == "" {
			return returnTo
		}
		parsed, err := url.Parse(returnTo)
		if err == nil {
			query := parsed.Query()
			if strings.TrimSpace(query.Get("app")) == "" {
				query.Set("app", appName)
			}
			query.Set("active", activeObjectID)
			parsed.RawQuery = query.Encode()
			return parsed.String()
		}
	}

	target := "/admin/app-editor?app=" + url.QueryEscape(appName)
	if strings.TrimSpace(activeObjectID) != "" {
		target += "&active=" + url.QueryEscape(activeObjectID)
	}
	return target
}

func writeAppEditorRedirect(w http.ResponseWriter, r *http.Request, target string) {
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("HX-Request")), "true") {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
