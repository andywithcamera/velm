package db

import (
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func TestExecuteJavaScriptCapturesLogsAndResult(t *testing.T) {
	result, err := ExecuteJavaScript(t.Context(), ScriptExecutionOptions{
		Code: `
async function run(ctx) {
  console.log("hello");
  ctx.log("logged", { x: 1 });
  return { ok: true };
}`,
		Language: "javascript",
	})
	if err != nil {
		t.Fatalf("ExecuteJavaScript() error = %v", err)
	}

	if len(result.Logs) != 2 {
		t.Fatalf("expected 2 logs, got %d", len(result.Logs))
	}
	if got := result.Logs[0].Message; got != "hello" {
		t.Fatalf("first log = %q, want %q", got, "hello")
	}
	if got := result.Logs[1].Message; got != "logged" {
		t.Fatalf("second log = %q, want %q", got, "logged")
	}

	gotResult, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result.Result)
	}
	if got := gotResult["ok"]; got != true {
		t.Fatalf("result ok = %#v, want true", got)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Fatalf("output = %q, expected hello", result.Output)
	}
	if !strings.Contains(result.Output, `"ok":true`) {
		t.Fatalf("output = %q, expected JSON result", result.Output)
	}
}

func TestExecuteJavaScriptExposesNotificationsHelper(t *testing.T) {
	result, err := ExecuteJavaScript(t.Context(), ScriptExecutionOptions{
		Code: `
function run(ctx) {
  return typeof ctx.notifications.send;
}`,
		Language: "javascript",
	})
	if err != nil {
		t.Fatalf("ExecuteJavaScript() error = %v", err)
	}
	if got := result.Result; got != "function" {
		t.Fatalf("result = %#v, want %q", got, "function")
	}
}

func TestExecuteJavaScriptDoesNotExposeLegacyCtxRecords(t *testing.T) {
	result, err := ExecuteJavaScript(t.Context(), ScriptExecutionOptions{
		Code: `
function run(ctx) {
  return typeof ctx.records;
}`,
		Language: "javascript",
	})
	if err != nil {
		t.Fatalf("ExecuteJavaScript() error = %v", err)
	}
	if got := result.Result; got != "undefined" {
		t.Fatalf("result = %#v, want %q", got, "undefined")
	}
}

func TestExecuteJavaScriptBindsCurrentTableClassAndRecordInstance(t *testing.T) {
	scope := ScriptScope{
		CurrentApp: ScriptScopeApp{Name: "itsm", Namespace: "itsm", Label: "ITSM"},
		Objects: []ScriptScopeObject{
			{
				App:       ScriptScopeApp{Name: "itsm", Namespace: "itsm", Label: "ITSM"},
				TableName: "itsm_incident",
				Alias:     "incident",
				Path:      "incident",
				Columns: []ScriptScopeColumn{
					{Name: "_id", Path: "incident._id", DataType: "uuid", ReadOnly: true, System: true},
					{Name: "resolution", Path: "incident.resolution"},
				},
			},
		},
	}

	result, err := ExecuteJavaScript(t.Context(), ScriptExecutionOptions{
		Code: `
async function run(ctx) {
  record.resolution = "abc";
  return {
    instance: record instanceof Incident,
    getter: typeof Incident.get,
    changed: record.changedFields(),
    json: record.toJSON()
  };
}`,
		Language:  "javascript",
		AppScope:  "itsm",
		TableName: "itsm_incident",
		Scope:     scope,
		Record: map[string]any{
			"resolution": "old",
		},
	})
	if err != nil {
		t.Fatalf("ExecuteJavaScript() error = %v", err)
	}

	payload, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result.Result)
	}
	if got := payload["instance"]; got != true {
		t.Fatalf("instance = %#v, want true", got)
	}
	if got := payload["getter"]; got != "function" {
		t.Fatalf("getter = %#v, want %q", got, "function")
	}
	changed, ok := payload["changed"].([]any)
	if !ok || len(changed) != 1 || changed[0] != "resolution" {
		t.Fatalf("changed = %#v, want [resolution]", payload["changed"])
	}
	if got := result.CurrentRecord["resolution"]; got != "abc" {
		t.Fatalf("current record resolution = %#v, want %q", got, "abc")
	}
}

func TestExecuteJavaScriptExposesDependencyModelsByNamespace(t *testing.T) {
	scope := ScriptScope{
		CurrentApp: ScriptScopeApp{Name: "itsm", Namespace: "itsm", Label: "ITSM"},
		DependencyApps: []ScriptScopeApp{
			{Name: "customer_mgmt", Namespace: "crm", Label: "CRM"},
		},
		Objects: []ScriptScopeObject{
			{
				App:       ScriptScopeApp{Name: "customer_mgmt", Namespace: "crm", Label: "CRM"},
				TableName: "crm_account",
				Alias:     "account",
				Path:      "crm.account",
				Columns: []ScriptScopeColumn{
					{Name: "_id", Path: "crm.account._id", DataType: "uuid", ReadOnly: true, System: true},
					{Name: "name", Path: "crm.account.name"},
				},
			},
		},
	}

	result, err := ExecuteJavaScript(t.Context(), ScriptExecutionOptions{
		Code: `
function run(ctx) {
  return {
    dependencyModel: typeof crm.Account.get,
    legacyAppName: typeof customer_mgmt
  };
}`,
		Language: "javascript",
		AppScope: "itsm",
		Scope:    scope,
	})
	if err != nil {
		t.Fatalf("ExecuteJavaScript() error = %v", err)
	}

	payload, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result.Result)
	}
	if got := payload["dependencyModel"]; got != "function" {
		t.Fatalf("dependencyModel = %#v, want %q", got, "function")
	}
	if got := payload["legacyAppName"]; got != "undefined" {
		t.Fatalf("legacyAppName = %#v, want %q", got, "undefined")
	}
}

func TestExecuteJavaScriptRejectsUnknownModelFields(t *testing.T) {
	scope := ScriptScope{
		CurrentApp: ScriptScopeApp{Name: "itsm", Namespace: "itsm", Label: "ITSM"},
		Objects: []ScriptScopeObject{
			{
				App:       ScriptScopeApp{Name: "itsm", Namespace: "itsm", Label: "ITSM"},
				TableName: "itsm_incident",
				Alias:     "incident",
				Path:      "incident",
				Columns: []ScriptScopeColumn{
					{Name: "resolution", Path: "incident.resolution"},
				},
			},
		},
	}

	_, err := ExecuteJavaScript(t.Context(), ScriptExecutionOptions{
		Code: `
function run(ctx) {
  return new Incident({ missing_field: true });
}`,
		Language: "javascript",
		AppScope: "itsm",
		Scope:    scope,
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "has no column") {
		t.Fatalf("error = %v, want unknown column message", err)
	}
}

func TestScriptValueToQueryParsesFiltersAndOrderBy(t *testing.T) {
	vm := goja.New()
	value, err := vm.RunString(`({
  filters: [
    { column: "state", operator: "in", value: ["open", "pending"] },
    { column: "due_date", operator: "<", value: new Date("2026-03-21T00:00:00Z") }
  ],
  groups: [
    {
      mode: "any",
      filters: [
        { column: "state", operator: "=", value: "open" },
        { column: "assigned_to", operator: "=", value: "550e8400-e29b-41d4-a716-446655440001" }
      ]
    }
  ],
  orderBy: [
    { column: "due_date", direction: "asc" }
  ],
  limit: 20,
  includeDeleted: true
})`)
	if err != nil {
		t.Fatalf("vm.RunString() error = %v", err)
	}

	query, err := scriptValueToQuery(value)
	if err != nil {
		t.Fatalf("scriptValueToQuery() error = %v", err)
	}

	if len(query.Filters) != 2 {
		t.Fatalf("len(query.Filters) = %d, want 2", len(query.Filters))
	}
	if got := query.Filters[0].Operator; got != "in" {
		t.Fatalf("query.Filters[0].Operator = %q, want %q", got, "in")
	}
	items, ok := query.Filters[0].Value.([]any)
	if !ok || len(items) != 2 || items[0] != "open" || items[1] != "pending" {
		t.Fatalf("query.Filters[0].Value = %#v, want [open pending]", query.Filters[0].Value)
	}
	if got := query.Filters[1].Value; got != "2026-03-21T00:00:00Z" {
		t.Fatalf("query.Filters[1].Value = %#v, want RFC3339 date string", got)
	}
	if len(query.OrderBy) != 1 || query.OrderBy[0].Column != "due_date" || query.OrderBy[0].Direction != "asc" {
		t.Fatalf("query.OrderBy = %#v, want due_date asc", query.OrderBy)
	}
	if len(query.Groups) != 1 || len(query.Groups[0].Filters) != 2 || query.Groups[0].Mode != "any" {
		t.Fatalf("query.Groups = %#v, want one any-group with two filters", query.Groups)
	}
	if query.Limit != 20 {
		t.Fatalf("query.Limit = %d, want 20", query.Limit)
	}
	if !query.IncludeDeleted {
		t.Fatal("query.IncludeDeleted = false, want true")
	}
}

func TestExecuteJavaScriptTimesOut(t *testing.T) {
	_, err := ExecuteJavaScript(t.Context(), ScriptExecutionOptions{
		Code: `
function run(ctx) {
  while (true) {}
}`,
		Language: "javascript",
		Timeout:  50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout error = %v", err)
	}
}
