package db

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/dop251/goja"
)

const scriptModelRuntimeHelperName = "__velmModels"

type scriptModelColumnDescriptor struct {
	Name           string `json:"name"`
	Label          string `json:"label"`
	DataType       string `json:"dataType,omitempty"`
	IsNullable     bool   `json:"isNullable"`
	ReferenceTable string `json:"referenceTable,omitempty"`
	ReadOnly       bool   `json:"readOnly,omitempty"`
	System         bool   `json:"system,omitempty"`
}

type scriptModelDescriptor struct {
	Key       string                        `json:"key"`
	App       ScriptScopeApp                `json:"app"`
	TableName string                        `json:"tableName"`
	Alias     string                        `json:"alias"`
	ClassName string                        `json:"className"`
	Namespace string                        `json:"namespace,omitempty"`
	ScopePath string                        `json:"scopePath"`
	Columns   []scriptModelColumnDescriptor `json:"columns"`
}

func buildScriptModelDescriptors(scope ScriptScope) ([]scriptModelDescriptor, map[string]scriptModelDescriptor, error) {
	descriptors := make([]scriptModelDescriptor, 0, len(scope.Objects))
	byKey := make(map[string]scriptModelDescriptor, len(scope.Objects))
	rootNames := map[string]string{
		"app":                        "reserved global",
		"console":                    "reserved global",
		"ctx":                        "reserved global",
		"input":                      "reserved global",
		"payload":                    "reserved global",
		"previousRecord":             "reserved global",
		"record":                     "reserved global",
		"run":                        "reserved global",
		"trigger":                    "reserved global",
		"user":                       "reserved global",
		scriptModelRuntimeHelperName: "reserved global",
	}
	namespaces := map[string]string{}
	namespaceClasses := map[string]map[string]string{}

	for _, object := range scope.Objects {
		descriptor := buildScriptModelDescriptor(scope, object)
		if descriptor.Key == "" {
			continue
		}

		if descriptor.Namespace == "" {
			if existing, ok := rootNames[descriptor.ClassName]; ok {
				return nil, nil, fmt.Errorf("script model %q for table %q conflicts with %s", descriptor.ClassName, descriptor.TableName, existing)
			}
			rootNames[descriptor.ClassName] = descriptor.TableName
		} else {
			if existing, ok := namespaces[descriptor.Namespace]; ok {
				if existing != descriptor.App.Name {
					return nil, nil, fmt.Errorf("script model namespace %q is shared by apps %q and %q", descriptor.Namespace, existing, descriptor.App.Name)
				}
			} else {
				if existing, ok := rootNames[descriptor.Namespace]; ok {
					return nil, nil, fmt.Errorf("script model namespace %q for app %q conflicts with %s", descriptor.Namespace, descriptor.App.Name, existing)
				}
				rootNames[descriptor.Namespace] = "model namespace"
				namespaces[descriptor.Namespace] = descriptor.App.Name
			}

			bucket := namespaceClasses[descriptor.Namespace]
			if bucket == nil {
				bucket = map[string]string{}
				namespaceClasses[descriptor.Namespace] = bucket
			}
			if existing, ok := bucket[descriptor.ClassName]; ok && existing != descriptor.TableName {
				return nil, nil, fmt.Errorf("script model %q conflicts for tables %q and %q inside namespace %q", descriptor.ClassName, existing, descriptor.TableName, descriptor.Namespace)
			}
			bucket[descriptor.ClassName] = descriptor.TableName
		}

		descriptors = append(descriptors, descriptor)
		byKey[descriptor.Key] = descriptor
	}

	sort.Slice(descriptors, func(i, j int) bool {
		if descriptors[i].ScopePath == descriptors[j].ScopePath {
			return descriptors[i].TableName < descriptors[j].TableName
		}
		return descriptors[i].ScopePath < descriptors[j].ScopePath
	})

	return descriptors, byKey, nil
}

func buildScriptModelDescriptor(scope ScriptScope, object ScriptScopeObject) scriptModelDescriptor {
	className := scriptModelClassName(object.Alias)
	namespace := ""
	scopePath := className
	if object.App.Name != scope.CurrentApp.Name {
		namespace = scriptScopeAppPathName(object.App)
		if namespace != "" {
			scopePath = namespace + "." + className
		}
	}

	columns := make([]scriptModelColumnDescriptor, 0, len(object.Columns))
	for _, column := range object.Columns {
		columns = append(columns, scriptModelColumnDescriptor{
			Name:           strings.TrimSpace(column.Name),
			Label:          strings.TrimSpace(column.Label),
			DataType:       strings.TrimSpace(column.DataType),
			IsNullable:     column.IsNullable,
			ReferenceTable: strings.TrimSpace(column.ReferenceTable),
			ReadOnly:       column.ReadOnly,
			System:         column.System,
		})
	}

	return scriptModelDescriptor{
		Key:       scriptModelKey(object.App.Name, object.TableName),
		App:       object.App,
		TableName: strings.TrimSpace(strings.ToLower(object.TableName)),
		Alias:     strings.TrimSpace(strings.ToLower(object.Alias)),
		ClassName: className,
		Namespace: namespace,
		ScopePath: scopePath,
		Columns:   columns,
	}
}

func scriptModelKey(appName, tableName string) string {
	appName = strings.TrimSpace(strings.ToLower(appName))
	tableName = strings.TrimSpace(strings.ToLower(tableName))
	if appName == "" || tableName == "" {
		return ""
	}
	return appName + ":" + tableName
}

func scriptModelClassName(alias string) string {
	alias = strings.TrimSpace(strings.ToLower(alias))
	if alias == "" {
		return "Record"
	}

	parts := strings.FieldsFunc(alias, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r))
	})
	if len(parts) == 0 {
		return "Record"
	}

	var builder strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		builder.WriteRune(unicode.ToUpper(runes[0]))
		for _, r := range runes[1:] {
			builder.WriteRune(r)
		}
	}

	name := builder.String()
	if name == "" {
		name = "Record"
	}
	if first := rune(name[0]); unicode.IsDigit(first) {
		name = "T" + name
	}
	return name
}

func (runtimeCtx *scriptRuntimeContext) installScopeModels(recordData, previousRecordData map[string]any) error {
	helper := runtimeCtx.vm.NewObject()
	if err := runtimeCtx.defineFunction(helper, "get", runtimeCtx.modelHelperGet); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(helper, "list", runtimeCtx.modelHelperList); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(helper, "listIDs", runtimeCtx.modelHelperListIDs); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(helper, "create", runtimeCtx.modelHelperCreate); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(helper, "update", runtimeCtx.modelHelperUpdate); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(helper, "bulkPatch", runtimeCtx.modelHelperBulkPatch); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(helper, "updateWhere", runtimeCtx.modelHelperUpdateWhere); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(helper, "deleteWhere", runtimeCtx.modelHelperDeleteWhere); err != nil {
		return err
	}
	if err := runtimeCtx.defineFunction(helper, "deleteRecord", runtimeCtx.modelHelperDeleteRecord); err != nil {
		return err
	}

	descriptors, byKey, err := buildScriptModelDescriptors(runtimeCtx.scope)
	if err != nil {
		return err
	}
	runtimeCtx.modelDescriptors = byKey

	_ = helper.Set("currentTableKey", runtimeCtx.currentTableModelKey())
	_ = helper.Set("recordData", runtimeCtx.toValue(recordData))
	_ = helper.Set("previousRecordData", runtimeCtx.toValue(previousRecordData))
	_ = runtimeCtx.vm.Set(scriptModelRuntimeHelperName, helper)

	if len(descriptors) > 0 {
		source, err := buildScriptModelRuntimeSource(descriptors)
		if err != nil {
			return err
		}
		if _, err := runtimeCtx.vm.RunString(source); err != nil {
			return err
		}
	}

	if runtimeCtx.vm.Get("record") == nil || goja.IsUndefined(runtimeCtx.vm.Get("record")) {
		if object := runtimeCtx.newDataObject(recordData); object != nil {
			_ = runtimeCtx.vm.Set("record", object)
		}
	}
	if runtimeCtx.vm.Get("previousRecord") == nil || goja.IsUndefined(runtimeCtx.vm.Get("previousRecord")) {
		if object := runtimeCtx.newDataObject(previousRecordData); object != nil {
			_ = runtimeCtx.vm.Set("previousRecord", object)
		}
	}

	return nil
}

func (runtimeCtx *scriptRuntimeContext) currentTableModelKey() string {
	if runtimeCtx.currentTableName == "" || len(runtimeCtx.modelDescriptors) == 0 {
		return ""
	}
	for key, descriptor := range runtimeCtx.modelDescriptors {
		if descriptor.TableName == runtimeCtx.currentTableName {
			return key
		}
	}
	return ""
}

func (runtimeCtx *scriptRuntimeContext) mustModelDescriptor(value goja.Value) scriptModelDescriptor {
	key := runtimeCtx.mustStringArgument(value, "model key")
	descriptor, ok := runtimeCtx.modelDescriptors[strings.TrimSpace(strings.ToLower(key))]
	if !ok {
		panic(runtimeCtx.vm.NewGoError(fmt.Errorf("script model %q is not available", key)))
	}
	return descriptor
}

func (runtimeCtx *scriptRuntimeContext) modelHelperGet(call goja.FunctionCall) goja.Value {
	descriptor := runtimeCtx.mustModelDescriptor(call.Argument(0))
	id := runtimeCtx.mustStringArgument(call.Argument(1), "record id")
	record, err := getScriptRecordWithQuerier(runtimeCtx.ctx, runtimeCtx.db, descriptor.TableName, id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return goja.Null()
		}
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(record)
}

func (runtimeCtx *scriptRuntimeContext) modelHelperList(call goja.FunctionCall) goja.Value {
	descriptor := runtimeCtx.mustModelDescriptor(call.Argument(0))
	query := runtimeCtx.mustQueryArgument(call.Argument(1))
	items, err := listScriptRecordsWithQuerier(runtimeCtx.ctx, runtimeCtx.db, descriptor.TableName, query)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(items)
}

func (runtimeCtx *scriptRuntimeContext) modelHelperListIDs(call goja.FunctionCall) goja.Value {
	descriptor := runtimeCtx.mustModelDescriptor(call.Argument(0))
	query := runtimeCtx.mustQueryArgument(call.Argument(1))
	ids, err := listScriptRecordIDsWithQuerier(runtimeCtx.ctx, runtimeCtx.db, descriptor.TableName, query)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(ids)
}

func (runtimeCtx *scriptRuntimeContext) modelHelperCreate(call goja.FunctionCall) goja.Value {
	descriptor := runtimeCtx.mustModelDescriptor(call.Argument(0))
	values := runtimeCtx.mustMapArgument(call.Argument(1), "values")
	record, err := createScriptRecordWithQuerier(runtimeCtx.ctx, runtimeCtx.db, descriptor.TableName, values, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(record)
}

func (runtimeCtx *scriptRuntimeContext) modelHelperUpdate(call goja.FunctionCall) goja.Value {
	descriptor := runtimeCtx.mustModelDescriptor(call.Argument(0))
	id := runtimeCtx.mustStringArgument(call.Argument(1), "record id")
	patch := runtimeCtx.mustMapArgument(call.Argument(2), "patch")
	record, err := updateScriptRecordWithQuerier(runtimeCtx.ctx, runtimeCtx.db, descriptor.TableName, id, patch, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(record)
}

func (runtimeCtx *scriptRuntimeContext) modelHelperBulkPatch(call goja.FunctionCall) goja.Value {
	descriptor := runtimeCtx.mustModelDescriptor(call.Argument(0))
	ids := runtimeCtx.mustStringSliceArgument(call.Argument(1), "ids")
	patch := runtimeCtx.mustMapArgument(call.Argument(2), "patch")
	result, err := updateScriptRecordsWhereWithQuerier(runtimeCtx.ctx, runtimeCtx.db, descriptor.TableName, ScriptRecordQuery{IDs: ids}, patch, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(map[string]any{
		"ids":      result.IDs,
		"affected": result.Affected,
	})
}

func (runtimeCtx *scriptRuntimeContext) modelHelperUpdateWhere(call goja.FunctionCall) goja.Value {
	descriptor := runtimeCtx.mustModelDescriptor(call.Argument(0))
	query := runtimeCtx.mustQueryArgument(call.Argument(1))
	patch := runtimeCtx.mustMapArgument(call.Argument(2), "patch")
	result, err := updateScriptRecordsWhereWithQuerier(runtimeCtx.ctx, runtimeCtx.db, descriptor.TableName, query, patch, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(map[string]any{
		"ids":      result.IDs,
		"affected": result.Affected,
	})
}

func (runtimeCtx *scriptRuntimeContext) modelHelperDeleteWhere(call goja.FunctionCall) goja.Value {
	descriptor := runtimeCtx.mustModelDescriptor(call.Argument(0))
	query := runtimeCtx.mustQueryArgument(call.Argument(1))
	result, err := deleteScriptRecordsWhereWithQuerier(runtimeCtx.ctx, runtimeCtx.db, descriptor.TableName, query, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(map[string]any{
		"ids":      result.IDs,
		"affected": result.Affected,
	})
}

func (runtimeCtx *scriptRuntimeContext) modelHelperDeleteRecord(call goja.FunctionCall) goja.Value {
	descriptor := runtimeCtx.mustModelDescriptor(call.Argument(0))
	id := runtimeCtx.mustStringArgument(call.Argument(1), "record id")
	result, err := deleteScriptRecordsWhereWithQuerier(runtimeCtx.ctx, runtimeCtx.db, descriptor.TableName, ScriptRecordQuery{
		IDs:   []string{id},
		Limit: 1,
	}, runtimeCtx.userID)
	if err != nil {
		panic(runtimeCtx.vm.NewGoError(err))
	}
	return runtimeCtx.vm.ToValue(map[string]any{
		"ids":      result.IDs,
		"affected": result.Affected,
	})
}

func buildScriptModelRuntimeSource(descriptors []scriptModelDescriptor) (string, error) {
	payload, err := json.Marshal(descriptors)
	if err != nil {
		return "", err
	}

	return `(function(root, runtime, descriptors) {
  var byKey = {};

  function defineHidden(target, name, value) {
    Object.defineProperty(target, name, {
      value: value,
      enumerable: false,
      configurable: true,
      writable: true
    });
  }

  function isDateValue(value) {
    return Object.prototype.toString.call(value) === "[object Date]";
  }

  function cloneValue(value) {
    if (value === undefined) return undefined;
    if (value === null || typeof value !== "object") return value;
    if (isDateValue(value)) return value.toISOString();
    return JSON.parse(JSON.stringify(value));
  }

  function isModelInstance(value) {
    return !!(value && typeof value === "object" && typeof value._id === "string" && value._id && value.__modelKey);
  }

  function normalizeValue(value) {
    if (value === undefined) return undefined;
    if (value === null) return null;
    if (isModelInstance(value)) return value._id;
    if (isDateValue(value)) return value.toISOString();
    if (Array.isArray(value)) return value.map(normalizeValue);
    if (typeof value !== "object") return value;

    var out = {};
    Object.keys(value).forEach(function(key) {
      if (key === "__modelKey" || key === "__original") return;
      var normalized = normalizeValue(value[key]);
      if (normalized !== undefined) {
        out[key] = normalized;
      }
    });
    return out;
  }

  function promiseResult(work) {
    return new Promise(function(resolve, reject) {
      try {
        resolve(work());
      } catch (error) {
        reject(error);
      }
    });
  }

  function normalizeInput(descriptor, values, strict) {
    if (values == null) return {};
    if (Array.isArray(values) || typeof values !== "object") {
      throw new Error(descriptor.className + " values must be an object");
    }

    var out = {};
    Object.keys(values).forEach(function(key) {
      if (!descriptor.columnMap[key]) {
        if (strict) {
          throw new Error(descriptor.className + " has no column \"" + key + "\"");
        }
        return;
      }
      var normalized = normalizeValue(values[key]);
      if (normalized !== undefined) {
        out[key] = normalized;
      }
    });
    return out;
  }

  function defaultQueryState() {
    return {
      ids: [],
      equals: {},
      filters: [],
      groups: [],
      orderBy: [],
      limit: 0,
      includeDeleted: false
    };
  }

  function cloneQueryState(value) {
    var state = defaultQueryState();
    if (!value || typeof value !== "object") {
      return state;
    }

    if (Array.isArray(value.ids)) {
      state.ids = value.ids.map(function(item) {
        return String(item || "").trim();
      }).filter(Boolean);
    }

    if (value.equals && typeof value.equals === "object" && !Array.isArray(value.equals)) {
      Object.keys(value.equals).forEach(function(key) {
        state.equals[key] = normalizeValue(value.equals[key]);
      });
    }

    if (Array.isArray(value.filters)) {
      state.filters = normalizeFilterList(value.filters, "query.filters");
    }

    if (Array.isArray(value.groups)) {
      state.groups = value.groups.map(function(group) {
        if (!group || typeof group !== "object" || Array.isArray(group)) {
          throw new Error("query.groups must contain objects");
        }
        return {
          mode: String(group.mode || "any").trim() || "any",
          filters: normalizeFilterList(group.filters || [], "query.groups[].filters")
        };
      });
    }

    var orderBy = [];
    if (Array.isArray(value.orderBy)) {
      orderBy = value.orderBy;
    } else if (Array.isArray(value.order_by)) {
      orderBy = value.order_by;
    }
    state.orderBy = orderBy.map(function(order) {
      if (!order || typeof order !== "object") {
        throw new Error("query.orderBy must contain objects");
      }
      var column = String(order.column || "").trim();
      if (!column) {
        throw new Error("query.orderBy[].column is required");
      }
      return {
        column: column,
        direction: String(order.direction || "asc").trim() || "asc"
      };
    });

    if (value.limit !== undefined && value.limit !== null && value.limit !== "") {
      var parsedLimit = Number(value.limit);
      if (!Number.isFinite(parsedLimit) || parsedLimit < 0) {
        throw new Error("query.limit must be a non-negative number");
      }
      state.limit = Math.trunc(parsedLimit);
    }

    if (value.includeDeleted !== undefined) {
      state.includeDeleted = !!value.includeDeleted;
    }

    return state;
  }

  function normalizeFilterSpec(filter, label) {
    if (Array.isArray(filter)) {
      if (filter.length < 2) {
        throw new Error(label + " must contain at least column and value");
      }
      return {
        column: String(filter[0] || "").trim(),
        operator: String((filter.length > 2 ? filter[1] : "=") || "=").trim() || "=",
        value: normalizeValue(filter.length > 2 ? filter[2] : filter[1])
      };
    }

    if (!filter || typeof filter !== "object") {
      throw new Error(label + " must contain objects or [column, operator, value] tuples");
    }

    var column = String(filter.column || "").trim();
    if (!column) {
      throw new Error(label + " requires a column");
    }

    return {
      column: column,
      operator: String(filter.operator || "=").trim() || "=",
      value: normalizeValue(filter.value)
    };
  }

  function normalizeFilterList(filters, label) {
    if (!Array.isArray(filters)) {
      throw new Error(label + " must be an array");
    }
    return filters.map(function(filter, index) {
      return normalizeFilterSpec(filter, label + "[" + index + "]");
    });
  }

  function editablePayload(descriptor, values, strict) {
    var normalized = normalizeInput(descriptor, values, strict);
    var out = {};
    Object.keys(normalized).forEach(function(key) {
      var column = descriptor.columnMap[key];
      if (!column) return;
      if (column.readOnly || column.system) {
        if (strict) {
          throw new Error(descriptor.className + "." + key + " is read only");
        }
        return;
      }
      out[key] = normalized[key];
    });
    return out;
  }

  function snapshot(descriptor, source) {
    var out = {};
    descriptor.columns.forEach(function(column) {
      if (!Object.prototype.hasOwnProperty.call(source, column.name)) return;
      if (source[column.name] === undefined) return;
      out[column.name] = cloneValue(source[column.name]);
    });
    return out;
  }

  function replaceState(target, descriptor, values, original, strict) {
    descriptor.columns.forEach(function(column) {
      delete target[column.name];
    });

    var normalized = normalizeInput(descriptor, values || {}, !!strict);
    Object.keys(normalized).forEach(function(key) {
      target[key] = normalized[key];
    });

    defineHidden(target, "__modelKey", descriptor.key);
    defineHidden(target, "__original", snapshot(descriptor, original || normalized));
    return target;
  }

  function hydrate(descriptor, values) {
    var instance = Object.create(descriptor.ctor.prototype);
    return replaceState(instance, descriptor, values || {}, values || {}, false);
  }

  function valuesEqual(left, right) {
    return JSON.stringify(normalizeValue(left)) === JSON.stringify(normalizeValue(right));
  }

  function changedFieldNames(instance, descriptor) {
    var current = editablePayload(descriptor, instance || {}, false);
    var original = editablePayload(descriptor, instance.__original || {}, false);
    var seen = {};
    Object.keys(current).forEach(function(key) { seen[key] = true; });
    Object.keys(original).forEach(function(key) { seen[key] = true; });

    return Object.keys(seen).sort().filter(function(key) {
      return !valuesEqual(current[key], original[key]);
    });
  }

  function QueryBuilder(descriptor, initialState) {
    if (!(this instanceof QueryBuilder)) return new QueryBuilder(descriptor, initialState);
    defineHidden(this, "__descriptor", descriptor);
    defineHidden(this, "__state", cloneQueryState(initialState));
  }

  QueryBuilder.prototype.clone = function() {
    return new QueryBuilder(this.__descriptor, this.toQuery());
  };

  QueryBuilder.prototype.ids = function(ids) {
    if (!Array.isArray(ids)) {
      throw new Error(this.__descriptor.className + ".query().ids expects an array");
    }
    this.__state.ids = ids.map(function(item) {
      return String(item || "").trim();
    }).filter(Boolean);
    return this;
  };

  QueryBuilder.prototype.where = function(column, operator, value) {
    if (arguments.length < 2) {
      throw new Error(this.__descriptor.className + ".query().where requires at least a column and value");
    }
    if (arguments.length === 2) {
      value = operator;
      operator = "=";
    }
    column = String(column || "").trim();
    operator = String(operator || "=").trim() || "=";
    if (!column) {
      throw new Error(this.__descriptor.className + ".query().where requires a column");
    }
    this.__state.filters.push({
      column: column,
      operator: operator,
      value: normalizeValue(value)
    });
    return this;
  };

  QueryBuilder.prototype.whereAny = function(filters) {
    this.__state.groups.push({
      mode: "any",
      filters: normalizeFilterList(filters, this.__descriptor.className + ".query().whereAny")
    });
    return this;
  };

  QueryBuilder.prototype.whereAll = function(filters) {
    this.__state.groups.push({
      mode: "all",
      filters: normalizeFilterList(filters, this.__descriptor.className + ".query().whereAll")
    });
    return this;
  };

  QueryBuilder.prototype.equals = function(values) {
    if (!values || typeof values !== "object" || Array.isArray(values)) {
      throw new Error(this.__descriptor.className + ".query().equals expects an object");
    }
    Object.keys(values).forEach(function(key) {
      this.__state.equals[key] = normalizeValue(values[key]);
    }, this);
    return this;
  };

  QueryBuilder.prototype.orderBy = function(column, direction) {
    column = String(column || "").trim();
    if (!column) {
      throw new Error(this.__descriptor.className + ".query().orderBy requires a column");
    }
    this.__state.orderBy.push({
      column: column,
      direction: String(direction || "asc").trim() || "asc"
    });
    return this;
  };

  QueryBuilder.prototype.limit = function(count) {
    if (count === undefined || count === null || count === "") {
      this.__state.limit = 0;
      return this;
    }
    var parsed = Number(count);
    if (!Number.isFinite(parsed) || parsed < 0) {
      throw new Error(this.__descriptor.className + ".query().limit requires a non-negative number");
    }
    this.__state.limit = Math.trunc(parsed);
    return this;
  };

  QueryBuilder.prototype.includeDeleted = function(enabled) {
    this.__state.includeDeleted = arguments.length === 0 ? true : !!enabled;
    return this;
  };

  QueryBuilder.prototype.toQuery = function() {
    return cloneQueryState(this.__state);
  };

  QueryBuilder.prototype.fetch = function() {
    var descriptor = this.__descriptor;
    var query = this.toQuery();
    return promiseResult(function() {
      var items = runtime.list(descriptor.key, query);
      return Array.isArray(items) ? items.map(function(item) { return hydrate(descriptor, item); }) : [];
    });
  };

  QueryBuilder.prototype.first = function() {
    var descriptor = this.__descriptor;
    var query = this.toQuery();
    query.limit = 1;
    return promiseResult(function() {
      var items = runtime.list(descriptor.key, query);
      if (!Array.isArray(items) || items.length === 0) {
        return null;
      }
      return hydrate(descriptor, items[0]);
    });
  };

  QueryBuilder.prototype.listIDs = function() {
    var descriptor = this.__descriptor;
    var query = this.toQuery();
    return promiseResult(function() {
      return runtime.listIDs(descriptor.key, query);
    });
  };

  QueryBuilder.prototype.update = function(patch) {
    var descriptor = this.__descriptor;
    var query = this.toQuery();
    var normalizedPatch = editablePayload(descriptor, patch || {}, true);
    return promiseResult(function() {
      return runtime.updateWhere(descriptor.key, query, normalizedPatch);
    });
  };

  QueryBuilder.prototype.delete = function() {
    var descriptor = this.__descriptor;
    var query = this.toQuery();
    return promiseResult(function() {
      return runtime.deleteWhere(descriptor.key, query);
    });
  };

  descriptors.forEach(function(descriptor) {
    descriptor.columnMap = {};
    descriptor.columns.forEach(function(column) {
      descriptor.columnMap[column.name] = column;
    });
    byKey[descriptor.key] = descriptor;

    function Model(values) {
      if (!(this instanceof Model)) return new Model(values);
      replaceState(this, descriptor, values || {}, {}, true);
    }

    Model.tableName = descriptor.tableName;
    Model.className = descriptor.className;
    Model.scopePath = descriptor.scopePath;
    Model.query = function(initialQuery) {
      return new QueryBuilder(descriptor, initialQuery || {});
    };
    Model.schema = function() {
      return cloneValue({
        app: descriptor.app,
        tableName: descriptor.tableName,
        alias: descriptor.alias,
        className: descriptor.className,
        namespace: descriptor.namespace,
        scopePath: descriptor.scopePath,
        columns: descriptor.columns
      });
    };
    Model.get = function(id) {
      return promiseResult(function() {
        var item = runtime.get(descriptor.key, id);
        return item == null ? null : hydrate(descriptor, item);
      });
    };
    Model.list = function(query) {
      return Model.query(query).fetch();
    };
    Model.listIDs = function(query) {
      return Model.query(query).listIDs();
    };
    Model.create = function(values) {
      var payload = editablePayload(descriptor, values || {}, true);
      return promiseResult(function() {
        var created = runtime.create(descriptor.key, payload);
        return hydrate(descriptor, created);
      });
    };
    Model.bulkPatch = function(ids, patch) {
      var normalizedPatch = editablePayload(descriptor, patch || {}, true);
      return promiseResult(function() {
        return runtime.bulkPatch(descriptor.key, ids, normalizedPatch);
      });
    };
    Model.updateWhere = function(query, patch) {
      var normalizedPatch = editablePayload(descriptor, patch || {}, true);
      return promiseResult(function() {
        return runtime.updateWhere(descriptor.key, cloneQueryState(query || {}), normalizedPatch);
      });
    };
    Model.deleteWhere = function(query) {
      return promiseResult(function() {
        return runtime.deleteWhere(descriptor.key, cloneQueryState(query || {}));
      });
    };

    Model.prototype.assign = function(values) {
      var normalized = normalizeInput(descriptor, values || {}, true);
      Object.keys(normalized).forEach(function(key) {
        this[key] = normalized[key];
      }, this);
      return this;
    };
    Model.prototype.isNew = function() {
      return !this._id;
    };
    Model.prototype.changedFields = function() {
      return changedFieldNames(this, descriptor);
    };
    Model.prototype.isDirty = function() {
      return this.changedFields().length > 0;
    };
    Model.prototype.toJSON = function() {
      return snapshot(descriptor, this);
    };
    Model.prototype.save = function() {
      var instance = this;
      return promiseResult(function() {
        if (!instance._id) {
          var created = runtime.create(descriptor.key, editablePayload(descriptor, instance, false));
          replaceState(instance, descriptor, created, created, false);
          return instance;
        }

        var changed = changedFieldNames(instance, descriptor);
        if (changed.length === 0) {
          return instance;
        }

        var patch = {};
        changed.forEach(function(key) {
          patch[key] = normalizeValue(instance[key]);
        });

        var updated = runtime.update(descriptor.key, instance._id, patch);
        replaceState(instance, descriptor, updated, updated, false);
        return instance;
      });
    };
    Model.prototype.reload = function() {
      var instance = this;
      return promiseResult(function() {
        if (!instance._id) return instance;
        var fresh = runtime.get(descriptor.key, instance._id);
        if (fresh == null) return null;
        replaceState(instance, descriptor, fresh, fresh, false);
        return instance;
      });
    };
    Model.prototype.delete = function() {
      var instance = this;
      return promiseResult(function() {
        if (!instance._id) {
          throw new Error(descriptor.className + ".delete requires _id");
        }
        var result = runtime.deleteRecord(descriptor.key, instance._id);
        return !!(result && result.affected > 0);
      });
    };

    descriptor.ctor = Model;

    if (descriptor.namespace) {
      if (!root[descriptor.namespace]) {
        root[descriptor.namespace] = {};
      }
      root[descriptor.namespace][descriptor.className] = Model;
    } else {
      root[descriptor.className] = Model;
    }
  });

  if (runtime.currentTableKey && runtime.recordData) {
    if (byKey[runtime.currentTableKey]) {
      root.record = hydrate(byKey[runtime.currentTableKey], runtime.recordData);
    } else {
      root.record = runtime.recordData;
    }
  } else if (runtime.recordData) {
    root.record = runtime.recordData;
  }

  if (runtime.currentTableKey && runtime.previousRecordData) {
    if (byKey[runtime.currentTableKey]) {
      root.previousRecord = hydrate(byKey[runtime.currentTableKey], runtime.previousRecordData);
    } else {
      root.previousRecord = runtime.previousRecordData;
    }
  } else if (runtime.previousRecordData) {
    root.previousRecord = runtime.previousRecordData;
  }
})(this, ` + scriptModelRuntimeHelperName + `, ` + string(payload) + `);`, nil
}
