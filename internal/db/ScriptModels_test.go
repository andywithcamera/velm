package db

import (
	"fmt"
	"testing"

	"github.com/dop251/goja"
)

func TestBuildScriptModelRuntimeSourceSupportsAsyncQueryBuilder(t *testing.T) {
	scope := ScriptScope{
		CurrentApp: ScriptScopeApp{Name: "task", Namespace: "task", Label: "Task"},
		Objects: []ScriptScopeObject{
			{
				App:       ScriptScopeApp{Name: "task", Namespace: "task", Label: "Task"},
				TableName: "task_item",
				Alias:     "item",
				Path:      "item",
				Columns: []ScriptScopeColumn{
					{Name: "_id", Path: "item._id", DataType: "uuid", ReadOnly: true, System: true},
					{Name: "state", Path: "item.state", DataType: "text"},
					{Name: "due_date", Path: "item.due_date", DataType: "timestamptz"},
				},
			},
		},
	}

	descriptors, _, err := buildScriptModelDescriptors(scope)
	if err != nil {
		t.Fatalf("buildScriptModelDescriptors() error = %v", err)
	}

	source, err := buildScriptModelRuntimeSource(descriptors)
	if err != nil {
		t.Fatalf("buildScriptModelRuntimeSource() error = %v", err)
	}

	vm := goja.New()
	helper := vm.NewObject()
	var capturedQuery ScriptRecordQuery

	mustSetFn := func(name string, fn func(goja.FunctionCall) goja.Value) {
		if err := helper.DefineDataProperty(name, vm.ToValue(fn), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_FALSE); err != nil {
			t.Fatalf("DefineDataProperty(%s) error = %v", name, err)
		}
	}

	mustSetFn("get", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(map[string]any{
			"_id":      "task-1",
			"state":    "open",
			"due_date": "2026-03-20T00:00:00Z",
		})
	})
	mustSetFn("list", func(call goja.FunctionCall) goja.Value {
		query, err := scriptValueToQuery(call.Argument(1))
		if err != nil {
			panic(vm.NewGoError(err))
		}
		capturedQuery = query
		return vm.ToValue([]map[string]any{
			{
				"_id":      "task-1",
				"state":    "open",
				"due_date": "2026-03-20T00:00:00Z",
			},
		})
	})
	mustSetFn("listIDs", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue([]string{"task-1"})
	})
	mustSetFn("create", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	mustSetFn("update", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	mustSetFn("bulkPatch", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	mustSetFn("updateWhere", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	mustSetFn("deleteWhere", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })
	mustSetFn("deleteRecord", func(call goja.FunctionCall) goja.Value { return goja.Undefined() })

	_ = helper.Set("currentTableKey", "")
	_ = helper.Set("recordData", goja.Undefined())
	_ = helper.Set("previousRecordData", goja.Undefined())
	_ = vm.Set(scriptModelRuntimeHelperName, helper)

	if _, err := vm.RunString(source); err != nil {
		t.Fatalf("vm.RunString(source) error = %v", err)
	}

	if _, err := vm.RunString(`
async function run() {
  var single = await Item.get("task-1");
  var items = await Item.query()
    .whereAny([
      ["state", "in", ["open", "pending"]],
      ["assigned_to", "=", "user-123"]
    ])
    .where("due_date", "<", new Date("2026-03-21T00:00:00Z"))
    .orderBy("due_date", "asc")
    .limit(20)
    .fetch();

  return {
    single: single._id,
    count: items.length,
    firstState: items[0].state
  };
}`); err != nil {
		t.Fatalf("vm.RunString(run) error = %v", err)
	}

	runFn, ok := goja.AssertFunction(vm.Get("run"))
	if !ok {
		t.Fatal("run function is not callable")
	}

	value, err := runFn(goja.Undefined())
	if err != nil {
		t.Fatalf("runFn() error = %v", err)
	}

	promise, ok := value.Export().(*goja.Promise)
	if !ok {
		t.Fatalf("result type = %T, want *goja.Promise", value.Export())
	}
	if promise.State() != goja.PromiseStateFulfilled {
		t.Fatalf("promise state = %v, want fulfilled", promise.State())
	}

	result, ok := normalizeScriptExportValue(promise.Result().Export()).(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", normalizeScriptExportValue(promise.Result().Export()))
	}
	if got := result["single"]; got != "task-1" {
		t.Fatalf("result.single = %#v, want %q", got, "task-1")
	}
	if got := fmt.Sprint(result["count"]); got != "1" {
		t.Fatalf("result.count = %#v, want 1", got)
	}
	if got := result["firstState"]; got != "open" {
		t.Fatalf("result.firstState = %#v, want %q", got, "open")
	}

	if len(capturedQuery.Filters) != 1 {
		t.Fatalf("len(capturedQuery.Filters) = %d, want 1", len(capturedQuery.Filters))
	}
	if len(capturedQuery.Groups) != 1 || len(capturedQuery.Groups[0].Filters) != 2 {
		t.Fatalf("capturedQuery.Groups = %#v, want one any-group with two filters", capturedQuery.Groups)
	}
	if got := capturedQuery.Groups[0].Mode; got != "any" {
		t.Fatalf("capturedQuery.Groups[0].Mode = %q, want %q", got, "any")
	}
	if got := capturedQuery.Filters[0].Value; got != "2026-03-21T00:00:00.000Z" {
		t.Fatalf("capturedQuery.Filters[1].Value = %#v, want date string", got)
	}
	if len(capturedQuery.OrderBy) != 1 || capturedQuery.OrderBy[0].Column != "due_date" || capturedQuery.OrderBy[0].Direction != "asc" {
		t.Fatalf("capturedQuery.OrderBy = %#v, want due_date asc", capturedQuery.OrderBy)
	}
	if capturedQuery.Limit != 20 {
		t.Fatalf("capturedQuery.Limit = %d, want 20", capturedQuery.Limit)
	}
}
