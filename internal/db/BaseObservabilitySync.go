package db

import (
	"context"
	"fmt"
	"math"
	"strings"

	"gopkg.in/yaml.v3"
)

var baseObsObservableClassChoices = []ChoiceOption{
	{Value: "cpu", Label: "CPU"},
	{Value: "memory", Label: "Memory"},
	{Value: "disk", Label: "Disk"},
	{Value: "http", Label: "HTTP"},
	{Value: "custom", Label: "Custom"},
}

var baseObsGoldenSignalChoices = []ChoiceOption{
	{Value: "latency", Label: "Latency"},
	{Value: "traffic", Label: "Traffic"},
	{Value: "errors", Label: "Errors"},
	{Value: "saturation", Label: "Saturation"},
}

var baseObsStateChoices = []ChoiceOption{
	{Value: "ok", Label: "OK"},
	{Value: "warning", Label: "Warning"},
	{Value: "critical", Label: "Critical"},
	{Value: "unknown", Label: "Unknown"},
}

var baseObsActionTypeChoices = []ChoiceOption{
	{Value: "create_task", Label: "Create Task"},
	{Value: "resolve_task", Label: "Resolve Task"},
	{Value: "cancel_task", Label: "Cancel Task"},
	{Value: "notify", Label: "Notify"},
}

var baseObsTaskPriorityChoices = []ChoiceOption{
	{Value: "low", Label: "Low"},
	{Value: "medium", Label: "Medium"},
	{Value: "high", Label: "High"},
	{Value: "critical", Label: "Critical"},
}

const baseObservabilityEndpointPath = "/api/base/observability/ingest"

const baseObservabilityIngestScript = `
async function run(ctx) {
  var body = normalizeBody(payload, input);
  var event = normalizeEvent(body, payload, ctx);

  var entityResolution = await resolveEntity(event);
  var definitionResolution = await resolveDefinition(event);
  var observableResolution = await resolveObservable(event, definitionResolution.record, entityResolution.record);

  var eventRecord = await ObsEvent.create({
    observable_id: observableResolution.record._id,
    entity_id: entityResolution.record._id,
    source: event.source,
    node: event.node,
    resource: event.resource,
    metric_name: definitionResolution.record.metric_name,
    severity: event.severity,
    summary: event.summary,
    value: event.value_document,
    golden_signal: definitionResolution.record.golden_signal,
    payload: event.payload_document,
    occurred_at: event.occurred_at
  });

  var observable = await updateObservable(
    observableResolution.record,
    definitionResolution.record,
    event,
    eventRecord
  );

  var actionResults = await evaluateActions(observable, definitionResolution.record, eventRecord, event, ctx);

  return {
    ok: true,
    entity_id: entityResolution.record._id,
    definition_id: definitionResolution.record._id,
    observable_id: observable._id,
    event_id: eventRecord._id,
    current_state: observable.current_state,
    current_severity: observable.current_severity,
    entity_created: entityResolution.created,
    definition_created: definitionResolution.created,
    observable_created: observableResolution.created,
    actions: actionResults
  };
}

function normalizeBody(rawPayload, rawInput) {
  if (rawPayload && typeof rawPayload === "object") {
    return rawPayload;
  }
  if (rawInput && rawInput.body && typeof rawInput.body === "object") {
    return rawInput.body;
  }
  return {};
}

function normalizeEvent(body, rawPayload, ctx) {
  var observableClass = normalizeObservableClass(
    body.observable_class || body.observableClass || body.class || body.type
  );
  var metricName = requireText(body.metric_name || body.metricName || body.metric, "metric_name");
  var explicitState = normalizeOptionalText(body.state || body.current_state || body.currentState);
  var severity = normalizeSeverity(body.severity, explicitState);
  var state = explicitState ? normalizeState(explicitState) : stateForSeverity(severity);
  var node = normalizeOptionalText(body.node || body.host || body.hostname || body.target);
  var displayName = normalizeOptionalText(body.display_name || body.displayName || body.observable_name || body.observableName);

  if (!node) {
    node = normalizeOptionalText(body.entity_node || body.entityNode || displayName);
  }
  if (!node) {
    throw new Error("node is required");
  }

  var resource = normalizeOptionalText(body.resource || body.mount || body.path || body.component);
  var source = normalizeOptionalText(body.source || body.integration || body.system) || "observability";
  var definitionName = normalizeOptionalText(body.definition_name || body.definitionName || body.name);
  var goldenSignal = normalizeGoldenSignal(body.golden_signal || body.goldenSignal);
  var occurredAt = normalizeTimestamp(
    body.occurred_at || body.occurredAt || body.timestamp || body.observed_at || body.observedAt,
    ctx.now()
  );
  var valueDocument = normalizeJSONDocument(body.value, "value");
  var payloadDocument = normalizeJSONDocument(rawPayload, "raw");
  var summary = normalizeOptionalText(body.summary || body.message || body.description);
  if (!summary) {
    summary = buildDefaultSummary(displayName || humanizeIdentifier(metricName), state, severity, node, resource);
  }

  return {
    entity_id: normalizeOptionalText(body.entity_id || body.entityId),
    entity: isPlainObject(body.entity) ? body.entity : {},
    source: source,
    node: node.toLowerCase(),
    resource: resource ? resource.toLowerCase() : null,
    observable_class: observableClass,
    golden_signal: goldenSignal,
    metric_name: metricName,
    definition_name: buildDefinitionName(metricName, definitionName, observableClass),
    definition_description: normalizeOptionalText(body.definition_description || body.definitionDescription),
    value_schema: normalizeJSONDocument(body.value_schema, "schema"),
    default_thresholds: normalizeJSONDocument(body.default_thresholds, "thresholds"),
    default_check_interval: normalizePositiveInteger(
      body.default_check_interval || body.defaultCheckInterval || body.check_interval || body.checkInterval,
      300
    ),
    severity: severity,
    state: state,
    summary: summary,
    value_document: valueDocument,
    payload_document: payloadDocument,
    occurred_at: occurredAt,
    display_name: displayName,
    severity_config: normalizeJSONDocument(body.severity_config, "thresholds"),
    check_interval: normalizeOptionalPositiveInteger(body.check_interval || body.checkInterval),
    slo_context: normalizeJSONDocument(body.slo_context, "slo"),
    entity_source_system: normalizeOptionalText(
      body.entity_source_system || body.entitySourceSystem || (body.entity && body.entity.source_system)
    ),
    entity_source_record_id: normalizeOptionalText(
      body.entity_source_record_id || body.entitySourceRecordId || (body.entity && body.entity.source_record_id)
    )
  };
}

async function resolveEntity(event) {
  if (event.entity_id) {
    var existingEntity = await Entity.get(event.entity_id);
    if (!existingEntity) {
      throw new Error("entity_id " + event.entity_id + " not found");
    }
    return { record: existingEntity, created: false };
  }

  var entityPayload = isPlainObject(event.entity) ? event.entity : {};
  var sourceSystem = normalizeOptionalText(entityPayload.source_system || entityPayload.sourceSystem) || event.entity_source_system || event.source;
  var sourceRecordID = normalizeOptionalText(entityPayload.source_record_id || entityPayload.sourceRecordId) || event.entity_source_record_id || event.node;
  var entityName = normalizeOptionalText(entityPayload.name) || event.display_name || event.node;

  if (!entityName) {
    throw new Error("unable to derive entity name");
  }
  if (!sourceRecordID) {
    sourceRecordID = entityName;
  }

  var existing = await Entity.query()
    .where("source_system", sourceSystem)
    .where("source_record_id", sourceRecordID)
    .first();
  if (existing) {
    return { record: existing, created: false };
  }

  var values = {
    name: entityName,
    entity_type: normalizeOptionalText(entityPayload.entity_type || entityPayload.entityType) || "service",
    description: normalizeOptionalText(entityPayload.description),
    source_system: sourceSystem,
    source_record_id: sourceRecordID,
    external_ref: normalizeOptionalText(entityPayload.external_ref || entityPayload.externalRef),
    asset_tag: normalizeOptionalText(entityPayload.asset_tag || entityPayload.assetTag),
    serial_number: normalizeOptionalText(entityPayload.serial_number || entityPayload.serialNumber),
    owner_entity_id: normalizeOptionalText(entityPayload.owner_entity_id || entityPayload.ownerEntityId),
    responsible_group_id: normalizeOptionalText(entityPayload.responsible_group_id || entityPayload.responsibleGroupId),
    responsible_user_id: normalizeOptionalText(entityPayload.responsible_user_id || entityPayload.responsibleUserId)
  };

  try {
    var created = await Entity.create(values);
    return { record: created, created: true };
  } catch (error) {
    var retry = await Entity.query()
      .where("source_system", sourceSystem)
      .where("source_record_id", sourceRecordID)
      .first();
    if (retry) {
      return { record: retry, created: false };
    }
    throw error;
  }
}

async function resolveDefinition(event) {
  var existing = await ObsDefinition.query()
    .where("observable_class", event.observable_class)
    .where("metric_name", event.metric_name)
    .first();
  if (existing) {
    return { record: existing, created: false };
  }

  var values = {
    name: event.definition_name,
    description: event.definition_description,
    observable_class: event.observable_class,
    golden_signal: event.golden_signal,
    metric_name: event.metric_name,
    value_schema: event.value_schema,
    default_thresholds: event.default_thresholds,
    default_check_interval: event.default_check_interval
  };

  try {
    var created = await ObsDefinition.create(values);
    return { record: created, created: true };
  } catch (error) {
    var retry = await ObsDefinition.query()
      .where("observable_class", event.observable_class)
      .where("metric_name", event.metric_name)
      .first();
    if (retry) {
      return { record: retry, created: false };
    }
    throw error;
  }
}

async function resolveObservable(event, definition, entity) {
  var existing = await findObservable(definition._id, entity._id, event.node, event.resource);
  if (existing) {
    return { record: existing, created: false };
  }

  var displayName = event.display_name || buildObservableDisplayName(definition.name, entity.name, event.node, event.resource);
  var severityConfig = hasDocumentValues(event.severity_config) ? event.severity_config : definition.default_thresholds;
  var values = {
    definition_id: definition._id,
    entity_id: entity._id,
    node: event.node,
    resource: event.resource,
    display_name: displayName,
    severity_config: hasDocumentValues(severityConfig) ? severityConfig : null,
    check_interval: event.check_interval || definition.default_check_interval,
    current_severity: 0,
    current_state: "unknown",
    flap_count: 0,
    enabled: true
  };

  try {
    var created = await ObsObservable.create(values);
    await provisionObservableActions(created, event.observable_class);
    return { record: created, created: true };
  } catch (error) {
    var retry = await findObservable(definition._id, entity._id, event.node, event.resource);
    if (retry) {
      return { record: retry, created: false };
    }
    throw error;
  }
}

async function findObservable(definitionID, entityID, node, resource) {
  var query = ObsObservable.query()
    .where("definition_id", definitionID)
    .where("entity_id", entityID)
    .where("node", node);
  if (resource == null) {
    query = query.where("resource", null);
  } else {
    query = query.where("resource", resource);
  }
  return await query.first();
}

async function provisionObservableActions(observable, observableClass) {
  var defaults = await ObsDefaultAction.query()
    .where("enabled", true)
    .whereAny([
      ["observable_class", "=", observableClass],
      ["observable_class", "=", null]
    ])
    .fetch();

  for (var i = 0; i < defaults.length; i++) {
    var item = defaults[i];
    await ObsAction.create({
      observable_id: observable._id,
      name: item.name,
      trigger_state: item.trigger_state,
      trigger_severity: item.trigger_severity,
      flap_guard_count: item.flap_guard_count,
      flap_guard_window: item.flap_guard_window,
      action_type: item.action_type,
      task_type: item.task_type,
      task_priority: item.task_priority,
      task_title: item.task_title,
      task_description: item.task_description,
      enabled: item.enabled
    });
  }
}

async function updateObservable(observable, definition, event, eventRecord) {
  var previousState = normalizeState(observable.current_state || "unknown");
  var nextState = event.state;
  var changed = previousState !== nextState;
  var previousFlapCount = normalizeInteger(observable.flap_count, 0);

  observable.display_name = event.display_name || observable.display_name || buildObservableDisplayName(definition.name, "", event.node, event.resource);
  if (hasDocumentValues(event.severity_config)) {
    observable.severity_config = event.severity_config;
  }
  if (event.check_interval) {
    observable.check_interval = event.check_interval;
  }
  if (hasDocumentValues(event.slo_context)) {
    observable.slo_context = event.slo_context;
  }
  observable.current_severity = event.severity;
  observable.current_state = nextState;
  observable.last_value = event.value_document;
  observable.last_event_id = eventRecord._id;
  observable.last_observed_at = event.occurred_at;
  observable.flap_count = computeFlapCount(previousState, nextState, previousFlapCount);
  if (changed || !observable.state_changed_at) {
    observable.state_changed_at = new Date().toISOString();
  }

  return await observable.save();
}

async function evaluateActions(observable, definition, eventRecord, event, ctx) {
  var actions = await ObsAction.query()
    .where("observable_id", observable._id)
    .where("enabled", true)
    .fetch();

  var results = [];
  for (var i = 0; i < actions.length; i++) {
    results.push(await evaluateAction(actions[i], observable, definition, eventRecord, event, ctx));
  }
  return results;
}

async function evaluateAction(action, observable, definition, eventRecord, event, ctx) {
  var actionName = action.name || action._id;
  if (normalizeState(action.trigger_state) !== normalizeState(observable.current_state)) {
    return { action: actionName, action_id: action._id, status: "skipped", reason: "state_mismatch" };
  }

  var minSeverity = normalizeOptionalInteger(action.trigger_severity);
  if (minSeverity != null && normalizeInteger(observable.current_severity, 0) < minSeverity) {
    return { action: actionName, action_id: action._id, status: "skipped", reason: "severity_mismatch" };
  }

  var guardSatisfied = await flapGuardSatisfied(action, observable);
  if (!guardSatisfied) {
    return { action: actionName, action_id: action._id, status: "skipped", reason: "flap_guard" };
  }

  var actionType = normalizeActionType(action.action_type);
  if (actionType === "create_task") {
    return await createTaskForAction(action, observable, definition, eventRecord);
  }
  if (actionType === "resolve_task") {
    return await closeOpenTasksForAction(action, observable, "completed");
  }
  if (actionType === "cancel_task") {
    return await closeOpenTasksForAction(action, observable, "cancelled");
  }

  ctx.log("obs_notify_not_implemented", { action_id: action._id, observable_id: observable._id });
  return { action: actionName, action_id: action._id, status: "skipped", reason: "notify_not_implemented" };
}

async function flapGuardSatisfied(action, observable) {
  var requiredCount = normalizeOptionalInteger(action.flap_guard_count);
  if (requiredCount == null || requiredCount <= 1) {
    return true;
  }

  var windowSeconds = normalizeOptionalInteger(action.flap_guard_window);
  if (windowSeconds == null || windowSeconds <= 0) {
    return normalizeInteger(observable.flap_count, 0) >= requiredCount;
  }

  var windowStart = new Date(Date.now() - (windowSeconds * 1000)).toISOString();
  var recentEvents = await ObsEvent.query()
    .where("observable_id", observable._id)
    .where("occurred_at", ">=", windowStart)
    .orderBy("occurred_at", "desc")
    .limit(Math.max(requiredCount * 4, 50))
    .fetch();

  var matches = 0;
  for (var i = 0; i < recentEvents.length; i++) {
    if (stateForSeverity(normalizeSeverity(recentEvents[i].severity, null)) === normalizeState(action.trigger_state)) {
      matches++;
    }
  }
  return matches >= requiredCount;
}

async function createTaskForAction(action, observable, definition, eventRecord) {
  var openLink = await ObsTask.query()
    .where("observable_id", observable._id)
    .where("action_id", action._id)
    .where("closed_at", null)
    .first();
  if (openLink) {
    return {
      action: action.name || action._id,
      action_id: action._id,
      status: "already_open",
      task_id: openLink.task_id
    };
  }

  var tokens = buildTemplateTokens(observable, definition, eventRecord);
  var title = interpolateTemplate(action.task_title, tokens);
  if (!title) {
    title = buildDefaultTaskTitle(action, observable);
  }

  var description = interpolateTemplate(action.task_description, tokens);
  description = buildTaskDescription(description, action, observable, definition, eventRecord);

  var task = await Task.create({
    title: title,
    description: description,
    priority: mapTaskPriority(action.task_priority),
    state: "new"
  });

  await TaskEntity.create({
    task_id: task._id,
    entity_id: observable.entity_id
  });

  var link = await ObsTask.create({
    observable_id: observable._id,
    action_id: action._id,
    task_id: task._id
  });

  return {
    action: action.name || action._id,
    action_id: action._id,
    status: "created",
    task_id: task._id,
    link_id: link._id
  };
}

async function closeOpenTasksForAction(action, observable, closureReason) {
  var openLinks = await ObsTask.query()
    .where("observable_id", observable._id)
    .where("closed_at", null)
    .fetch();
  if (!openLinks.length) {
    return { action: action.name || action._id, action_id: action._id, status: "no_open_task" };
  }

  var matchedTaskIDs = [];
  var targetTaskType = normalizeOptionalText(action.task_type);
  for (var i = 0; i < openLinks.length; i++) {
    var link = openLinks[i];
    var createAction = await ObsAction.get(link.action_id);
    if (!createAction || normalizeActionType(createAction.action_type) !== "create_task") {
      continue;
    }

    var linkedTaskType = normalizeOptionalText(createAction.task_type);
    if (targetTaskType && linkedTaskType && linkedTaskType !== targetTaskType) {
      continue;
    }
    if (targetTaskType && !linkedTaskType) {
      continue;
    }

    var task = await Task.get(link.task_id);
    if (task) {
      task.state = "closed";
      task.closure_reason = closureReason;
      await task.save();
      matchedTaskIDs.push(task._id);
    }

    link.closed_at = new Date().toISOString();
    await link.save();
  }

  if (!matchedTaskIDs.length) {
    return { action: action.name || action._id, action_id: action._id, status: "no_open_task" };
  }

  return {
    action: action.name || action._id,
    action_id: action._id,
    status: closureReason === "cancelled" ? "cancelled" : "resolved",
    affected: matchedTaskIDs.length,
    task_ids: matchedTaskIDs
  };
}

function buildTemplateTokens(observable, definition, eventRecord) {
  return {
    display_name: observable.display_name || "",
    node: observable.node || "",
    resource: observable.resource || "",
    metric_name: definition.metric_name || "",
    current_state: observable.current_state || "",
    current_severity: String(normalizeInteger(observable.current_severity, 0)),
    last_value: safeJSONStringify(observable.last_value),
    entity_id: observable.entity_id || "",
    summary: eventRecord.summary || "",
    source: eventRecord.source || ""
  };
}

function interpolateTemplate(template, tokens) {
  var raw = typeof template === "string" ? template : "";
  if (!raw) {
    return "";
  }
  return raw.replace(/\{\{([a-zA-Z0-9_]+)\}\}/g, function(_, key) {
    return tokens[key] != null ? String(tokens[key]) : "";
  }).trim();
}

function buildDefaultTaskTitle(action, observable) {
  var typeLabel = humanizeIdentifier(normalizeOptionalText(action.task_type) || "task");
  return typeLabel + ": " + (observable.display_name || observable.node || "Observable") + " is " + humanizeIdentifier(observable.current_state || "critical");
}

function buildTaskDescription(description, action, observable, definition, eventRecord) {
  var parts = [];
  if (description) {
    parts.push(description);
  } else if (eventRecord.summary) {
    parts.push(eventRecord.summary);
  }

  var metadata = [];
  if (action.task_type) {
    metadata.push("Requested task type: " + action.task_type);
  }
  metadata.push("Observable: " + (observable.display_name || observable.node || observable._id));
  metadata.push("Metric: " + (definition.metric_name || ""));
  metadata.push("State: " + (observable.current_state || ""));
  metadata.push("Severity: " + String(normalizeInteger(observable.current_severity, 0)));
  if (observable.resource) {
    metadata.push("Resource: " + observable.resource);
  }
  if (hasDocumentValues(observable.last_value)) {
    metadata.push("Last value: " + safeJSONStringify(observable.last_value));
  }

  parts.push(metadata.join("\n"));
  return parts.join("\n\n").trim();
}

function computeFlapCount(previousState, nextState, previousCount) {
  if (previousState === nextState) {
    return previousCount + 1;
  }
  if (nextState === "ok") {
    return 0;
  }
  return 1;
}

function buildObservableDisplayName(definitionName, entityName, node, resource) {
  var base = normalizeOptionalText(definitionName) || normalizeOptionalText(entityName) || node;
  var out = base || "Observable";
  if (node) {
    out += " on " + node;
  }
  if (resource) {
    out += " " + resource;
  }
  return out.trim();
}

function buildDefaultSummary(displayName, state, severity, node, resource) {
  var parts = [displayName + " is " + state];
  parts.push("(severity " + String(severity) + ")");
  if (node) {
    parts.push("on " + node);
  }
  if (resource) {
    parts.push(resource);
  }
  return parts.join(" ");
}

function buildDefinitionName(metricName, explicitName, observableClass) {
  var provided = normalizeOptionalText(explicitName);
  if (provided) {
    return provided;
  }
  var metricLabel = humanizeIdentifier(metricName);
  var classLabel = humanizeIdentifier(observableClass);
  if (!metricLabel) {
    return classLabel + " Metric";
  }
  if (!classLabel || observableClass === "custom") {
    return metricLabel;
  }
  if (metricLabel.toLowerCase().indexOf(classLabel.toLowerCase()) === 0) {
    return metricLabel;
  }
  return classLabel + " " + metricLabel;
}

function humanizeIdentifier(value) {
  var text = normalizeOptionalText(value);
  if (!text) {
    return "";
  }
  var parts = text.replace(/[_-]+/g, " ").split(/\s+/);
  var out = [];
  for (var i = 0; i < parts.length; i++) {
    var item = parts[i];
    if (!item) {
      continue;
    }
    out.push(item.charAt(0).toUpperCase() + item.slice(1).toLowerCase());
  }
  return out.join(" ");
}

function mapTaskPriority(priority) {
  switch (normalizeOptionalText(priority)) {
    case "critical":
      return "very_high";
    case "high":
      return "high";
    case "medium":
      return "medium";
    case "low":
      return "low";
    default:
      return "low";
  }
}

function normalizeObservableClass(value) {
  switch (normalizeOptionalText(value)) {
    case "cpu":
    case "memory":
    case "disk":
    case "http":
      return normalizeOptionalText(value);
    default:
      return "custom";
  }
}

function normalizeGoldenSignal(value) {
  switch (normalizeOptionalText(value)) {
    case "latency":
    case "traffic":
    case "errors":
    case "saturation":
      return normalizeOptionalText(value);
    default:
      return null;
  }
}

function normalizeActionType(value) {
  switch (normalizeOptionalText(value)) {
    case "create_task":
    case "resolve_task":
    case "cancel_task":
    case "notify":
      return normalizeOptionalText(value);
    default:
      return "notify";
  }
}

function normalizeState(value) {
  switch (normalizeOptionalText(value)) {
    case "ok":
    case "warning":
    case "critical":
      return normalizeOptionalText(value);
    default:
      return "unknown";
  }
}

function stateForSeverity(severity) {
  var value = normalizeInteger(severity, 0);
  if (value >= 4) {
    return "critical";
  }
  if (value >= 2) {
    return "warning";
  }
  return "ok";
}

function normalizeSeverity(rawSeverity, rawState) {
  var parsed = normalizeOptionalInteger(rawSeverity);
  if (parsed == null) {
    switch (normalizeState(rawState)) {
      case "critical":
        parsed = 4;
        break;
      case "warning":
        parsed = 2;
        break;
      case "ok":
        parsed = 0;
        break;
      default:
        parsed = 0;
        break;
    }
  }
  if (parsed < 0) {
    return 0;
  }
  if (parsed > 5) {
    return 5;
  }
  return parsed;
}

function normalizeOptionalPositiveInteger(value) {
  var parsed = normalizeOptionalInteger(value);
  if (parsed == null || parsed <= 0) {
    return null;
  }
  return parsed;
}

function normalizePositiveInteger(value, fallback) {
  var parsed = normalizeOptionalPositiveInteger(value);
  if (parsed == null) {
    return fallback;
  }
  return parsed;
}

function normalizeOptionalInteger(value) {
  if (value == null || value === "") {
    return null;
  }
  var parsed = parseInt(value, 10);
  return isNaN(parsed) ? null : parsed;
}

function normalizeInteger(value, fallback) {
  var parsed = normalizeOptionalInteger(value);
  return parsed == null ? fallback : parsed;
}

function normalizeTimestamp(value, fallback) {
  var raw = normalizeOptionalText(value);
  if (!raw) {
    return fallback;
  }
  var parsed = new Date(raw);
  if (isNaN(parsed.getTime())) {
    return fallback;
  }
  return parsed.toISOString();
}

function normalizeOptionalText(value) {
  if (value == null) {
    return null;
  }
  var text = String(value).trim();
  return text === "" ? null : text;
}

function requireText(value, label) {
  var text = normalizeOptionalText(value);
  if (!text) {
    throw new Error(label + " is required");
  }
  return text;
}

function normalizeJSONDocument(value, fallbackKey) {
  if (value == null || value === "") {
    return {};
  }
  if (Array.isArray(value)) {
    return value;
  }
  if (typeof value === "object") {
    return value;
  }
  var wrapped = {};
  wrapped[fallbackKey || "value"] = value;
  return wrapped;
}

function hasDocumentValues(value) {
  if (value == null) {
    return false;
  }
  if (Array.isArray(value)) {
    return value.length > 0;
  }
  if (typeof value === "object") {
    return Object.keys(value).length > 0;
  }
  return true;
}

function isPlainObject(value) {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function safeJSONStringify(value) {
  try {
    return JSON.stringify(value == null ? {} : value);
  } catch (error) {
    return "{}";
  }
}
`

func SyncBaseObservabilityDefinition(ctx context.Context) error {
	app, err := GetActiveAppByName(ctx, "base")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "app not found") {
			return nil
		}
		return err
	}

	draft := cloneAppDefinition(app.DraftDefinition)
	if draft == nil {
		draft = cloneAppDefinition(app.Definition)
	}
	published := cloneAppDefinition(app.Definition)
	if published == nil {
		published = cloneAppDefinition(app.DraftDefinition)
	}
	if draft == nil && published == nil {
		return nil
	}

	draftChanged := upsertBaseObservabilityDefinition(draft)
	publishedChanged := upsertBaseObservabilityDefinition(published)
	if !draftChanged && !publishedChanged {
		return nil
	}

	if draft != nil {
		if err := prepareDefinitionForApp(app, draft); err != nil {
			return err
		}
		if err := validateAppDefinitionForApp(ctx, app, draft); err != nil {
			return err
		}
	}
	if published != nil {
		if err := prepareDefinitionForApp(app, published); err != nil {
			return err
		}
		if err := validateAppDefinitionForApp(ctx, app, published); err != nil {
			return err
		}
	}

	draftContent := strings.TrimSpace(app.DefinitionYAML)
	if draft != nil {
		content, err := yaml.Marshal(draft)
		if err != nil {
			return fmt.Errorf("marshal base observability draft definition: %w", err)
		}
		draftContent = string(content)
	}

	publishedContent := strings.TrimSpace(app.PublishedDefinitionYAML)
	if published != nil {
		content, err := yaml.Marshal(published)
		if err != nil {
			return fmt.Errorf("marshal base observability published definition: %w", err)
		}
		publishedContent = string(content)
	}

	_, err = Pool.Exec(ctx, `
		UPDATE _app
		SET definition_yaml = $2,
			published_definition_yaml = $3,
			definition_version = CASE
				WHEN COALESCE(definition_yaml, '') = $2 THEN definition_version
				ELSE GREATEST(definition_version, published_version) + 1
			END,
			published_version = CASE
				WHEN COALESCE(published_definition_yaml, '') = $3 THEN published_version
				ELSE GREATEST(definition_version, published_version) + 1
			END,
			_updated_at = NOW()
		WHERE name = $1 OR namespace = $1
	`, app.Name, draftContent, publishedContent)
	if err != nil {
		return fmt.Errorf("sync base observability definition: %w", err)
	}

	return nil
}

func upsertBaseObservabilityDefinition(definition *AppDefinition) bool {
	if definition == nil {
		return false
	}

	before, _ := yaml.Marshal(definition)

	for _, table := range baseObservabilityTables() {
		definition.Tables = upsertAppDefinitionTable(definition.Tables, table)
	}
	definition.Tables = upsertBaseObservabilityRelatedLists(definition.Tables)
	definition.Services = upsertAppDefinitionService(definition.Services, baseObservabilityServiceDefinition())
	definition.Endpoints = upsertAppDefinitionEndpoint(definition.Endpoints, baseObservabilityEndpointDefinition())

	after, _ := yaml.Marshal(definition)
	return string(before) != string(after)
}

func baseObservabilityTables() []AppDefinitionTable {
	return []AppDefinitionTable{
		baseObsDefinitionTable(),
		baseObsDefaultActionTable(),
		baseObsObservableTable(),
		baseObsActionTable(),
		baseObsEventTable(),
		baseObsTaskTable(),
	}
}

func baseObsDefinitionTable() AppDefinitionTable {
	return AppDefinitionTable{
		Name:          "base_obs_definition",
		LabelSingular: "Observation Definition",
		LabelPlural:   "Observation Definitions",
		Description:   "Reusable measurement templates for observability signals.",
		DisplayField:  "name",
		Columns: []AppDefinitionColumn{
			{Name: "name", Label: "Name", DataType: "varchar(255)", IsNullable: false},
			{Name: "description", Label: "Description", DataType: "text", IsNullable: true},
			{Name: "observable_class", Label: "Observable Class", DataType: "choice", IsNullable: false, DefaultValue: "custom", Choices: append([]ChoiceOption(nil), baseObsObservableClassChoices...)},
			{Name: "golden_signal", Label: "Golden Signal", DataType: "choice", IsNullable: true, Choices: append([]ChoiceOption(nil), baseObsGoldenSignalChoices...)},
			{Name: "metric_name", Label: "Metric Name", DataType: "varchar(255)", IsNullable: false},
			{Name: "value_schema", Label: "Value Schema", DataType: "jsonb", IsNullable: true},
			{Name: "default_thresholds", Label: "Default Thresholds", DataType: "jsonb", IsNullable: true},
			{Name: "default_check_interval", Label: "Default Check Interval", DataType: "integer", IsNullable: false, DefaultValue: "300"},
		},
		Forms: []AppDefinitionForm{{
			Name:   "default",
			Label:  "Default",
			Fields: []string{"name", "description", "observable_class", "golden_signal", "metric_name", "value_schema", "default_thresholds", "default_check_interval"},
		}},
		Lists: []AppDefinitionList{{
			Name:    "default",
			Label:   "Default",
			Columns: []string{"name", "observable_class", "golden_signal", "metric_name", "default_check_interval", "_updated_at"},
		}},
		RelatedLists: []AppDefinitionRelatedList{{
			Name:           "observables",
			Label:          "Observables",
			Table:          "base_obs_observable",
			ReferenceField: "definition_id",
			Columns:        []string{"display_name", "current_state", "current_severity", "last_observed_at"},
		}},
	}
}

func baseObsDefaultActionTable() AppDefinitionTable {
	return AppDefinitionTable{
		Name:          "base_obs_default_action",
		LabelSingular: "Default Observation Action",
		LabelPlural:   "Default Observation Actions",
		Description:   "Action templates copied onto newly provisioned observables.",
		DisplayField:  "name",
		Columns: []AppDefinitionColumn{
			{Name: "observable_class", Label: "Observable Class", DataType: "choice", IsNullable: true, Choices: append([]ChoiceOption(nil), baseObsObservableClassChoices...)},
			{Name: "name", Label: "Name", DataType: "varchar(255)", IsNullable: false},
			{Name: "trigger_state", Label: "Trigger State", DataType: "choice", IsNullable: false, Choices: append([]ChoiceOption(nil), baseObsStateChoices...)},
			{Name: "trigger_severity", Label: "Trigger Severity", DataType: "integer", IsNullable: true},
			{Name: "flap_guard_count", Label: "Flap Guard Count", DataType: "integer", IsNullable: true},
			{Name: "flap_guard_window", Label: "Flap Guard Window", DataType: "integer", IsNullable: true},
			{Name: "action_type", Label: "Action Type", DataType: "choice", IsNullable: false, Choices: append([]ChoiceOption(nil), baseObsActionTypeChoices...)},
			{Name: "task_type", Label: "Task Type", DataType: "varchar(255)", IsNullable: true},
			{Name: "task_priority", Label: "Task Priority", DataType: "choice", IsNullable: true, Choices: append([]ChoiceOption(nil), baseObsTaskPriorityChoices...)},
			{Name: "task_title", Label: "Task Title", DataType: "text", IsNullable: true},
			{Name: "task_description", Label: "Task Description", DataType: "text", IsNullable: true},
			{Name: "enabled", Label: "Enabled", DataType: "bool", IsNullable: false, DefaultValue: "true"},
		},
		Forms: []AppDefinitionForm{{
			Name:   "default",
			Label:  "Default",
			Fields: []string{"observable_class", "name", "trigger_state", "trigger_severity", "flap_guard_count", "flap_guard_window", "action_type", "task_type", "task_priority", "task_title", "task_description", "enabled"},
		}},
		Lists: []AppDefinitionList{{
			Name:    "default",
			Label:   "Default",
			Columns: []string{"name", "observable_class", "trigger_state", "trigger_severity", "action_type", "task_type", "task_priority", "enabled", "_updated_at"},
		}},
	}
}

func baseObsObservableTable() AppDefinitionTable {
	return AppDefinitionTable{
		Name:          "base_obs_observable",
		LabelSingular: "Observable",
		LabelPlural:   "Observables",
		Description:   "Current rolled-up state for an observation definition on a specific entity.",
		DisplayField:  "display_name",
		Columns: []AppDefinitionColumn{
			{Name: "definition_id", Label: "Definition", DataType: "reference", IsNullable: false, ReferenceTable: "base_obs_definition"},
			{Name: "entity_id", Label: "Entity", DataType: "reference", IsNullable: false, ReferenceTable: "base_entity"},
			{Name: "node", Label: "Node", DataType: "varchar(255)", IsNullable: false},
			{Name: "resource", Label: "Resource", DataType: "varchar(255)", IsNullable: true},
			{Name: "display_name", Label: "Display Name", DataType: "varchar(255)", IsNullable: false},
			{Name: "severity_config", Label: "Severity Config", DataType: "jsonb", IsNullable: true},
			{Name: "check_interval", Label: "Check Interval", DataType: "integer", IsNullable: true},
			{Name: "current_severity", Label: "Current Severity", DataType: "integer", IsNullable: false, DefaultValue: "0"},
			{Name: "current_state", Label: "Current State", DataType: "choice", IsNullable: false, DefaultValue: "unknown", Choices: append([]ChoiceOption(nil), baseObsStateChoices...)},
			{Name: "last_value", Label: "Last Value", DataType: "jsonb", IsNullable: true},
			{Name: "last_event_id", Label: "Last Event", DataType: "reference", IsNullable: true, ReferenceTable: "base_obs_event"},
			{Name: "last_observed_at", Label: "Last Observed At", DataType: "timestamptz", IsNullable: true},
			{Name: "state_changed_at", Label: "State Changed At", DataType: "timestamptz", IsNullable: true},
			{Name: "flap_count", Label: "Flap Count", DataType: "integer", IsNullable: false, DefaultValue: "0"},
			{Name: "slo_context", Label: "SLO Context", DataType: "jsonb", IsNullable: true},
			{Name: "message_key", Label: "Message Key", DataType: "varchar(255)", IsNullable: false},
			{Name: "enabled", Label: "Enabled", DataType: "bool", IsNullable: false, DefaultValue: "true"},
		},
		Forms: []AppDefinitionForm{{
			Name:   "default",
			Label:  "Default",
			Fields: []string{"definition_id", "entity_id", "display_name", "node", "resource", "current_state", "current_severity", "check_interval", "last_observed_at", "state_changed_at", "flap_count", "severity_config", "slo_context", "message_key", "enabled"},
		}},
		Lists: []AppDefinitionList{{
			Name:    "default",
			Label:   "Default",
			Columns: []string{"display_name", "entity_id", "current_state", "current_severity", "node", "resource", "last_observed_at", "enabled", "_updated_at"},
		}},
		RelatedLists: []AppDefinitionRelatedList{
			{Name: "actions", Label: "Actions", Table: "base_obs_action", ReferenceField: "observable_id", Columns: []string{"name", "trigger_state", "action_type", "enabled"}},
			{Name: "events", Label: "Events", Table: "base_obs_event", ReferenceField: "observable_id", Columns: []string{"occurred_at", "severity", "summary", "source"}},
			{Name: "tasks", Label: "Spawned Tasks", Table: "base_obs_task", ReferenceField: "observable_id", Columns: []string{"task_id", "action_id", "spawned_at", "closed_at"}},
		},
	}
}

func baseObsActionTable() AppDefinitionTable {
	return AppDefinitionTable{
		Name:          "base_obs_action",
		LabelSingular: "Observation Action",
		LabelPlural:   "Observation Actions",
		Description:   "Task and notification responses owned by a specific observable.",
		DisplayField:  "name",
		Columns: []AppDefinitionColumn{
			{Name: "observable_id", Label: "Observable", DataType: "reference", IsNullable: false, ReferenceTable: "base_obs_observable"},
			{Name: "name", Label: "Name", DataType: "varchar(255)", IsNullable: false},
			{Name: "trigger_state", Label: "Trigger State", DataType: "choice", IsNullable: false, Choices: append([]ChoiceOption(nil), baseObsStateChoices...)},
			{Name: "trigger_severity", Label: "Trigger Severity", DataType: "integer", IsNullable: true},
			{Name: "flap_guard_count", Label: "Flap Guard Count", DataType: "integer", IsNullable: true},
			{Name: "flap_guard_window", Label: "Flap Guard Window", DataType: "integer", IsNullable: true},
			{Name: "action_type", Label: "Action Type", DataType: "choice", IsNullable: false, Choices: append([]ChoiceOption(nil), baseObsActionTypeChoices...)},
			{Name: "task_type", Label: "Task Type", DataType: "varchar(255)", IsNullable: true},
			{Name: "task_priority", Label: "Task Priority", DataType: "choice", IsNullable: true, Choices: append([]ChoiceOption(nil), baseObsTaskPriorityChoices...)},
			{Name: "task_title", Label: "Task Title", DataType: "text", IsNullable: true},
			{Name: "task_description", Label: "Task Description", DataType: "text", IsNullable: true},
			{Name: "enabled", Label: "Enabled", DataType: "bool", IsNullable: false, DefaultValue: "true"},
		},
		Forms: []AppDefinitionForm{{
			Name:   "default",
			Label:  "Default",
			Fields: []string{"observable_id", "name", "trigger_state", "trigger_severity", "flap_guard_count", "flap_guard_window", "action_type", "task_type", "task_priority", "task_title", "task_description", "enabled"},
		}},
		Lists: []AppDefinitionList{{
			Name:    "default",
			Label:   "Default",
			Columns: []string{"observable_id", "name", "trigger_state", "trigger_severity", "action_type", "task_type", "enabled", "_updated_at"},
		}},
		RelatedLists: []AppDefinitionRelatedList{{
			Name:           "tasks",
			Label:          "Spawned Tasks",
			Table:          "base_obs_task",
			ReferenceField: "action_id",
			Columns:        []string{"task_id", "observable_id", "spawned_at", "closed_at"},
		}},
	}
}

func baseObsEventTable() AppDefinitionTable {
	return AppDefinitionTable{
		Name:          "base_obs_event",
		LabelSingular: "Observation Event",
		LabelPlural:   "Observation Events",
		Description:   "Immutable append-only audit log of normalized observation signals.",
		DisplayField:  "summary",
		Columns: []AppDefinitionColumn{
			{Name: "observable_id", Label: "Observable", DataType: "reference", IsNullable: false, ReferenceTable: "base_obs_observable"},
			{Name: "entity_id", Label: "Entity", DataType: "reference", IsNullable: false, ReferenceTable: "base_entity"},
			{Name: "source", Label: "Source", DataType: "varchar(255)", IsNullable: false},
			{Name: "node", Label: "Node", DataType: "varchar(255)", IsNullable: false},
			{Name: "resource", Label: "Resource", DataType: "varchar(255)", IsNullable: true},
			{Name: "metric_name", Label: "Metric Name", DataType: "varchar(255)", IsNullable: false},
			{Name: "severity", Label: "Severity", DataType: "integer", IsNullable: false},
			{Name: "summary", Label: "Summary", DataType: "text", IsNullable: true},
			{Name: "value", Label: "Value", DataType: "jsonb", IsNullable: true},
			{Name: "golden_signal", Label: "Golden Signal", DataType: "choice", IsNullable: true, Choices: append([]ChoiceOption(nil), baseObsGoldenSignalChoices...)},
			{Name: "payload", Label: "Payload", DataType: "jsonb", IsNullable: true},
			{Name: "occurred_at", Label: "Occurred At", DataType: "timestamptz", IsNullable: false},
			{Name: "ingested_at", Label: "Ingested At", DataType: "timestamptz", IsNullable: false},
		},
		Forms: []AppDefinitionForm{{
			Name:   "default",
			Label:  "Default",
			Fields: []string{"observable_id", "entity_id", "source", "node", "resource", "metric_name", "severity", "summary", "value", "golden_signal", "payload", "occurred_at", "ingested_at"},
		}},
		Lists: []AppDefinitionList{{
			Name:    "default",
			Label:   "Default",
			Columns: []string{"occurred_at", "observable_id", "severity", "summary", "source", "metric_name", "ingested_at"},
		}},
	}
}

func baseObsTaskTable() AppDefinitionTable {
	return AppDefinitionTable{
		Name:          "base_obs_task",
		LabelSingular: "Observation Task Link",
		LabelPlural:   "Observation Task Links",
		Description:   "Historical and active task links created by observation actions.",
		DisplayField:  "task_id",
		Columns: []AppDefinitionColumn{
			{Name: "observable_id", Label: "Observable", DataType: "reference", IsNullable: false, ReferenceTable: "base_obs_observable"},
			{Name: "action_id", Label: "Action", DataType: "reference", IsNullable: false, ReferenceTable: "base_obs_action"},
			{Name: "task_id", Label: "Task", DataType: "reference", IsNullable: false, ReferenceTable: "base_task"},
			{Name: "spawned_at", Label: "Spawned At", DataType: "timestamptz", IsNullable: false},
			{Name: "closed_at", Label: "Closed At", DataType: "timestamptz", IsNullable: true},
		},
		Forms: []AppDefinitionForm{{
			Name:   "default",
			Label:  "Default",
			Fields: []string{"observable_id", "action_id", "task_id", "spawned_at", "closed_at"},
		}},
		Lists: []AppDefinitionList{{
			Name:    "default",
			Label:   "Default",
			Columns: []string{"observable_id", "action_id", "task_id", "spawned_at", "closed_at"},
		}},
	}
}

func baseObservabilityServiceDefinition() AppDefinitionService {
	return AppDefinitionService{
		Name:        "observability",
		Label:       "Observability Service",
		Description: "Ingests observation events, provisions observables, and drives task responses.",
		Methods: []AppDefinitionMethod{{
			Name:        "ingest_event",
			Label:       "Ingest Event",
			Description: "Normalize and ingest an observation event inside one transactional service execution.",
			Visibility:  "public",
			Language:    "javascript",
			Script:      baseObservabilityIngestScript,
		}},
	}
}

func baseObservabilityEndpointDefinition() AppDefinitionEndpoint {
	return AppDefinitionEndpoint{
		Name:        "obs_ingest",
		Label:       "Observation Ingest",
		Description: "Accept normalized observation events and update current observable state.",
		Method:      "POST",
		Path:        baseObservabilityEndpointPath,
		Call:        "observability.ingest_event",
		Enabled:     true,
	}
}

func upsertBaseObservabilityRelatedLists(tables []AppDefinitionTable) []AppDefinitionTable {
	items := make([]AppDefinitionTable, len(tables))
	copy(items, tables)
	for i := range items {
		switch strings.TrimSpace(strings.ToLower(items[i].Name)) {
		case "base_entity":
			items[i].RelatedLists = upsertRelatedList(items[i].RelatedLists, AppDefinitionRelatedList{
				Name:           "observables",
				Label:          "Observables",
				Table:          "base_obs_observable",
				ReferenceField: "entity_id",
				Columns:        []string{"display_name", "current_state", "current_severity", "last_observed_at"},
			})
		case "base_task":
			items[i].RelatedLists = upsertRelatedList(items[i].RelatedLists, AppDefinitionRelatedList{
				Name:           "observation_links",
				Label:          "Observation Links",
				Table:          "base_obs_task",
				ReferenceField: "task_id",
				Columns:        []string{"observable_id", "action_id", "spawned_at", "closed_at"},
			})
		}
	}
	return items
}

func upsertAppDefinitionTable(tables []AppDefinitionTable, item AppDefinitionTable) []AppDefinitionTable {
	name := strings.TrimSpace(strings.ToLower(item.Name))
	if name == "" {
		return tables
	}

	var items []AppDefinitionTable
	if len(tables) < math.MaxInt {
		items = make([]AppDefinitionTable, 0, len(tables)+1)
	} else {
		items = append([]AppDefinitionTable(nil), tables...)
	}
	replaced := false
	for _, existing := range tables {
		if strings.TrimSpace(strings.ToLower(existing.Name)) == name {
			items = append(items, item)
			replaced = true
			continue
		}
		items = append(items, existing)
	}
	if !replaced {
		items = append(items, item)
	}
	return items
}

func upsertAppDefinitionService(services []AppDefinitionService, item AppDefinitionService) []AppDefinitionService {
	name := strings.TrimSpace(strings.ToLower(item.Name))
	if name == "" {
		return services
	}

	var items []AppDefinitionService
	if len(services) < math.MaxInt {
		items = make([]AppDefinitionService, 0, len(services)+1)
	} else {
		items = append([]AppDefinitionService(nil), services...)
	}
	replaced := false
	for _, existing := range services {
		if strings.TrimSpace(strings.ToLower(existing.Name)) == name {
			items = append(items, item)
			replaced = true
			continue
		}
		items = append(items, existing)
	}
	if !replaced {
		items = append(items, item)
	}
	return items
}

func upsertAppDefinitionEndpoint(endpoints []AppDefinitionEndpoint, item AppDefinitionEndpoint) []AppDefinitionEndpoint {
	name := strings.TrimSpace(strings.ToLower(item.Name))
	if name == "" {
		return endpoints
	}

	var items []AppDefinitionEndpoint
	if len(endpoints) < math.MaxInt {
		items = make([]AppDefinitionEndpoint, 0, len(endpoints)+1)
	} else {
		items = append([]AppDefinitionEndpoint(nil), endpoints...)
	}
	replaced := false
	for _, existing := range endpoints {
		if strings.TrimSpace(strings.ToLower(existing.Name)) == name {
			items = append(items, item)
			replaced = true
			continue
		}
		items = append(items, existing)
	}
	if !replaced {
		items = append(items, item)
	}
	return items
}
