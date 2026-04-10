package db

import "testing"

func TestUpsertBaseObservabilityDefinition(t *testing.T) {
	definition, err := ParseAppDefinition(`
name: base
tables:
  - name: base_task
    columns:
      - name: title
        data_type: varchar(255)
        is_nullable: false
  - name: base_entity
    columns:
      - name: name
        data_type: varchar(255)
        is_nullable: false
`)
	if err != nil {
		t.Fatalf("ParseAppDefinition() error = %v", err)
	}

	if !upsertBaseObservabilityDefinition(definition) {
		t.Fatal("expected observability definition sync to change the app definition")
	}
	if upsertBaseObservabilityDefinition(definition) {
		t.Fatal("expected observability definition sync to be idempotent")
	}

	obsDefinition := requireDefinitionTable(t, definition, "base_obs_definition")
	if got := obsDefinition.DisplayField; got != "name" {
		t.Fatalf("expected base_obs_definition display field name, got %q", got)
	}
	if got := requireDefinitionColumn(t, obsDefinition, "observable_class").DataType; got != "choice" {
		t.Fatalf("expected observable_class to be choice, got %q", got)
	}
	if got := len(requireDefinitionColumn(t, obsDefinition, "observable_class").Choices); got != 5 {
		t.Fatalf("expected five observable class choices, got %d", got)
	}

	obsObservable := requireDefinitionTable(t, definition, "base_obs_observable")
	if got := requireDefinitionColumn(t, obsObservable, "definition_id").ReferenceTable; got != "base_obs_definition" {
		t.Fatalf("expected observable.definition_id to reference base_obs_definition, got %q", got)
	}
	if got := requireDefinitionColumn(t, obsObservable, "entity_id").ReferenceTable; got != "base_entity" {
		t.Fatalf("expected observable.entity_id to reference base_entity, got %q", got)
	}
	if got := requireDefinitionColumn(t, obsObservable, "current_state").DefaultValue; got != "unknown" {
		t.Fatalf("expected observable.current_state default unknown, got %q", got)
	}
	events := requireRelatedList(t, obsObservable, "events")
	if events.Table != "base_obs_event" {
		t.Fatalf("expected observable events related list table base_obs_event, got %q", events.Table)
	}

	obsAction := requireDefinitionTable(t, definition, "base_obs_action")
	if got := requireDefinitionColumn(t, obsAction, "action_type").DataType; got != "choice" {
		t.Fatalf("expected action_type to be choice, got %q", got)
	}
	if got := len(requireDefinitionColumn(t, obsAction, "action_type").Choices); got != 4 {
		t.Fatalf("expected four action type choices, got %d", got)
	}

	obsEvent := requireDefinitionTable(t, definition, "base_obs_event")
	if got := requireDefinitionColumn(t, obsEvent, "payload").DataType; got != "jsonb" {
		t.Fatalf("expected event.payload to be jsonb, got %q", got)
	}

	obsTask := requireDefinitionTable(t, definition, "base_obs_task")
	if got := requireDefinitionColumn(t, obsTask, "task_id").ReferenceTable; got != "base_task" {
		t.Fatalf("expected obs task link task_id to reference base_task, got %q", got)
	}

	entity := requireDefinitionTable(t, definition, "base_entity")
	observables := requireRelatedList(t, entity, "observables")
	if observables.Table != "base_obs_observable" {
		t.Fatalf("expected base_entity observables related list table base_obs_observable, got %q", observables.Table)
	}

	task := requireDefinitionTable(t, definition, "base_task")
	observationLinks := requireRelatedList(t, task, "observation_links")
	if observationLinks.Table != "base_obs_task" {
		t.Fatalf("expected base_task observation_links table base_obs_task, got %q", observationLinks.Table)
	}

	if got := len(definition.Services); got != 1 {
		t.Fatalf("expected one service after sync, got %d", got)
	}
	if definition.Services[0].Name != "observability" {
		t.Fatalf("expected observability service, got %q", definition.Services[0].Name)
	}
	if got := definition.Services[0].Methods[0].Name; got != "ingest_event" {
		t.Fatalf("expected ingest_event method, got %q", got)
	}
	if got := definition.Services[0].Methods[0].Visibility; got != "public" {
		t.Fatalf("expected ingest_event visibility public, got %q", got)
	}

	if got := len(definition.Endpoints); got != 1 {
		t.Fatalf("expected one endpoint after sync, got %d", got)
	}
	if got := definition.Endpoints[0].Path; got != baseObservabilityEndpointPath {
		t.Fatalf("expected observability ingest path %q, got %q", baseObservabilityEndpointPath, got)
	}
	if got := definition.Endpoints[0].Call; got != "observability.ingest_event" {
		t.Fatalf("expected observability ingest endpoint call, got %q", got)
	}
}
