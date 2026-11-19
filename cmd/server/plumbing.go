package main

// The plumbing check (Process Designer, Plan T6 / Requirements §4).
//
// Design Vision §4 lists seven invariants a definition must satisfy before it
// can run. The check is written to the same pattern as the v1 lossless check:
// it reads the submitted document, names the block and the field, and refuses
// before the database is touched. Messages never carry a line number or a file
// path (Requirements AC-85), because the author never sees a file.
//
// Invariant 6 (a wait's exits) has no reachable case in this release: `wait`
// is refused earlier by the not-available rule, so the check records it here
// with no exercised branch rather than pretending to test it.

import (
	"context"
	"fmt"
	"strings"

	"velm/internal/db"
)

type plumbingChild struct {
	Slug        string
	IsContainer bool
	Inputs      []db.TaskInputField
	Outputs     []string
}

// checkPlumbing validates the submitted document for the definition named by
// containerSlug. It returns a readable message, or "" when the document is
// sound. It reads child definitions, which is why it needs a context.
func checkPlumbing(ctx context.Context, containerSlug string, blocks []db.StructureBlock, admin ...bool) string {
	if message := checkScriptAdmin(blocks, len(admin) > 0 && admin[0]); message != "" {
		return message
	}
	if len(blocks) == 0 {
		return "A structure needs at least one block"
	}
	var containerSchema string
	if err := db.Pool.QueryRow(ctx,
		`SELECT COALESCE(input_schema_yaml, '') FROM base_task_definition
		 WHERE slug = $1 AND _deleted_at IS NULL`, containerSlug).Scan(&containerSchema); err != nil {
		return "The definition could not be read before saving"
	}
	containerInputs, _ := db.ParseTaskInputSchema(containerSchema)
	keyIndex := map[string]int{}
	childKey := map[string]db.StructureBlock{}
	for index, block := range blocks {
		key := strings.TrimSpace(block.Key)
		if key == "" {
			continue
		}
		keyIndex[key] = index
		childKey[key] = block
	}

	if message := checkTrigger(blocks); message != "" {
		return message
	}
	if message := checkScopes(blocks, keyIndex); message != "" {
		return message
	}
	if message := checkRunTaskTargets(ctx, blocks); message != "" {
		return message
	}
	if message := checkSelfCall(ctx, containerSlug, blocks); message != "" {
		return message
	}
	if message := checkRunTaskMapping(ctx, containerInputs, blocks); message != "" {
		return message
	}
	if message := checkPills(ctx, containerInputs, blocks, childKey); message != "" {
		return message
	}
	if message := checkCollections(ctx, containerInputs, blocks); message != "" {
		return message
	}
	if message := checkGates(blocks, keyIndex); message != "" {
		return message
	}
	return ""
}

// checkScriptAdmin is Design Vision D5's second layer: a tree containing a
// run_script block cannot be saved by a non-admin. The palette and the card
// are the other two layers; this one is the only one a hand-written POST
// cannot skip.
func checkScriptAdmin(blocks []db.StructureBlock, admin bool) string {
	if admin {
		return ""
	}
	for _, block := range blocks {
		if strings.TrimSpace(block.Kind) != "control" || strings.TrimSpace(block.Type) != "run_script" {
			continue
		}
		label := strings.TrimSpace(block.Label)
		if label == "" {
			label = strings.TrimSpace(block.Key)
		}
		return "Block " + strconvQuote(label) + ": run script requires the admin role"
	}
	return ""
}

func plumbingLabel(block db.StructureBlock) string {
	label := strings.TrimSpace(block.Label)
	if label == "" {
		label = strings.TrimSpace(block.Slug)
	}
	if label == "" {
		label = "block " + strings.TrimSpace(block.Key)
	}
	return label
}

// Invariant 1: exactly one trigger, first in the document.
func checkTrigger(blocks []db.StructureBlock) string {
	first := -1
	count := 0
	for index, block := range blocks {
		if strings.TrimSpace(block.Parent) != "" {
			continue
		}
		if strings.TrimSpace(block.Kind) != "trigger" {
			continue
		}
		if first < 0 {
			first = index
		}
		count++
		if count == 2 {
			return fmt.Sprintf("Block %q: a definition has exactly one trigger, not two", plumbingLabel(block))
		}
	}
	if count == 0 {
		return "A definition needs exactly one trigger block"
	}
	if first != 0 {
		return fmt.Sprintf("Block %q: the trigger must be the first block in the document", plumbingLabel(blocks[first]))
	}
	return ""
}

// Invariant 2: every control scope has a body, and is closed by an end or by
// its last block. An `if` with no blocks in its scope cannot terminate.
//
// A retry block is a scope of its own but never a root one: it re-runs its
// own children, so it needs a parent scope and cannot contain another retry
// (Design Vision D17, AC-82/83).
func checkScopes(blocks []db.StructureBlock, keyIndex map[string]int) string {
	parentKeys := map[string]bool{}
	for _, block := range blocks {
		if parent := strings.TrimSpace(block.Parent); parent != "" {
			parentKeys[parent] = true
		}
	}
	byKey := map[string]db.StructureBlock{}
	for _, block := range blocks {
		if key := strings.TrimSpace(block.Key); key != "" {
			byKey[key] = block
		}
	}
	for _, block := range blocks {
		if strings.TrimSpace(block.Kind) != "control" {
			continue
		}
		if block.Type == "retry" {
			parentKey := strings.TrimSpace(block.Parent)
			if parentKey == "" {
				return fmt.Sprintf("Block %q: a retry block needs a parent scope", plumbingLabel(block))
			}
			if parent, found := byKey[parentKey]; found &&
				strings.TrimSpace(parent.Kind) == "control" && strings.TrimSpace(parent.Type) == "retry" {
				return fmt.Sprintf("Block %q cannot contain Block %q: a retry block cannot contain another retry",
					plumbingLabel(parent), plumbingLabel(block))
			}
			continue
		}
		if block.Type != "if" && block.Type != "for_each" {
			continue
		}
		key := strings.TrimSpace(block.Key)
		if key == "" || !parentKeys[key] {
			return fmt.Sprintf("Block %q: an %s with no blocks in its scope needs an end", plumbingLabel(block), block.Type)
		}
	}
	return ""
}

// checkRunTaskTargets refuses a run_task that names a definition which is gone
// (AC-102). A deleted definition is no longer a target: the save must not
// store a call that can never resolve at run time.
func checkRunTaskTargets(ctx context.Context, blocks []db.StructureBlock) string {
	for _, block := range blocks {
		if strings.TrimSpace(block.Kind) != "control" || strings.TrimSpace(block.Type) != "run_task" {
			continue
		}
		slug := strings.TrimSpace(block.Slug)
		if slug == "" {
			continue
		}
		var id string
		if err := db.Pool.QueryRow(ctx,
			`SELECT _id::text FROM base_task_definition WHERE slug = $1 AND _deleted_at IS NULL`,
			slug).Scan(&id); err != nil {
			return fmt.Sprintf("run task %s: that definition does not exist: %s", plumbingLabel(block), slug)
		}
	}
	return ""
}

// checkRunTaskMapping validates a run_task's input mapping against the target's
// declared inputs (AC-96, AC-98, AC-101): a required input must be mapped, and
// an `expr:` source must resolve against the enclosing scope.
func checkRunTaskMapping(ctx context.Context, containerInputs []db.TaskInputField, blocks []db.StructureBlock) string {
	cache := map[string]plumbingChild{}
	inputNames := map[string]bool{}
	for _, field := range containerInputs {
		inputNames[field.Name] = true
	}
	for _, block := range blocks {
		if strings.TrimSpace(block.Kind) != "control" || strings.TrimSpace(block.Type) != "run_task" {
			continue
		}
		slug := strings.TrimSpace(block.Slug)
		if slug == "" {
			continue
		}
		facts := childFacts(ctx, cache, slug)
		if len(facts.Inputs) == 0 {
			continue
		}
		for _, field := range facts.Inputs {
			raw, present := block.Mapping[field.Name]
			if !present || raw == nil || mappingText(raw) == "" {
				if !field.IsNullable && strings.TrimSpace(field.DefaultValue) == "" {
					return fmt.Sprintf("run task %s: input %s is required", slug, field.Name)
				}
				continue
			}
			if from, value := mappingParts(raw); from == "expr" {
				text := strings.TrimSpace(value)
				ref := strings.TrimSpace(strings.TrimPrefix(text, "expr:"))
				if !expressionResolves(ref, inputNames) {
					return fmt.Sprintf("run task %s: input %s cannot be resolved from %q", slug, field.Name, text)
				}
			}
		}
	}
	return ""
}

// normaliseRunTaskMappings keeps exactly the target's declared inputs (AC-96):
// a row for an input the target does not declare is dropped, and a target with
// no inputs stores an empty mapping.
func normaliseRunTaskMappings(ctx context.Context, blocks []db.StructureBlock) {
	cache := map[string]plumbingChild{}
	for index := range blocks {
		block := &blocks[index]
		if strings.TrimSpace(block.Kind) != "control" || strings.TrimSpace(block.Type) != "run_task" {
			continue
		}
		facts := childFacts(ctx, cache, strings.TrimSpace(block.Slug))
		kept := map[string]any{}
		for _, field := range facts.Inputs {
			if raw, present := block.Mapping[field.Name]; present {
				kept[field.Name] = raw
			}
		}
		block.Mapping = kept
	}
}

// mappingParts reads a mapping value's mode and text. A bare string keeps the
// engine's pre-slice-6 convention: `$` means an expression, anything else a
// literal. A `{from, value}` row states it explicitly.
func mappingParts(raw any) (string, string) {
	if row, ok := raw.(map[string]any); ok {
		from, _ := row["from"].(string)
		value, _ := row["value"].(string)
		return strings.TrimSpace(from), value
	}
	text, ok := raw.(string)
	if !ok {
		return "", ""
	}
	if strings.HasPrefix(strings.TrimSpace(text), "$") {
		return "expr", text
	}
	return "literal", text
}

func mappingText(raw any) string {
	_, value := mappingParts(raw)
	return strings.TrimSpace(value)
}

// expressionResolves reports whether an expr: source names a value the
// enclosing scope provides: one of the definition's own inputs, a block
// result, a loop variable or a global. Anything else is refused at save.
func expressionResolves(ref string, inputNames map[string]bool) bool {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "":
		return false
	case strings.HasPrefix(ref, "$inputs."):
		return inputNames[strings.TrimPrefix(ref, "$inputs.")]
	case strings.HasPrefix(ref, "$blocks."), strings.HasPrefix(ref, "$outputs."):
		return len(ref) > len("$blocks.")
	case ref == "$item", ref == "$index", strings.HasPrefix(ref, "$item."), strings.HasPrefix(ref, "$index."):
		return true
	case ref == "$now", ref == "$requester", ref == "$run.id",
		strings.HasPrefix(ref, "$now."), strings.HasPrefix(ref, "$run."), strings.HasPrefix(ref, "$requester."):
		return true
	default:
		return false
	}
}

// Invariant 3: a block may not be its own ancestor, and a definition may not
// transitively call itself. The handler already refuses a parent that is not
// earlier, which makes a document-internal cycle impossible; the transitive
// self-call is the case that needs the database.
func checkSelfCall(ctx context.Context, containerSlug string, blocks []db.StructureBlock) string {
	var containerID string
	if err := db.Pool.QueryRow(ctx,
		`SELECT _id::text FROM base_task_definition WHERE slug = $1 AND _deleted_at IS NULL`,
		containerSlug).Scan(&containerID); err != nil {
		return "The definition could not be read before saving"
	}
	for _, block := range blocks {
		if strings.TrimSpace(block.Kind) != "task" &&
			!(strings.TrimSpace(block.Kind) == "control" && strings.TrimSpace(block.Type) == "run_task") {
			continue
		}
		slug := strings.TrimSpace(block.Slug)
		if slug == "" {
			continue
		}
		var childID string
		if err := db.Pool.QueryRow(ctx,
			`SELECT _id::text FROM base_task_definition WHERE slug = $1 AND _deleted_at IS NULL`,
			slug).Scan(&childID); err != nil {
			continue
		}
		if childID == containerID || definitionContains(ctx, childID, containerID, map[string]bool{}) {
			return fmt.Sprintf("Block %q calls %s, which contains this definition: that cycle cannot run", plumbingLabel(block), slug)
		}
	}
	return ""
}

func definitionContains(ctx context.Context, fromID, targetID string, seen map[string]bool) bool {
	if seen[fromID] {
		return false
	}
	seen[fromID] = true
	rows, err := db.Pool.Query(ctx,
		`SELECT COALESCE(child_definition_id::text, '') FROM base_task_definition_child
		 WHERE container_definition_id = $1`, fromID)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var child string
		if err := rows.Scan(&child); err != nil {
			return false
		}
		if child == targetID || (child != "" && definitionContains(ctx, child, targetID, seen)) {
			return true
		}
	}
	return false
}

func childFacts(ctx context.Context, cache map[string]plumbingChild, slug string) plumbingChild {
	if facts, ok := cache[slug]; ok {
		return facts
	}
	facts := plumbingChild{Slug: slug}
	var schema, variables string
	_ = db.Pool.QueryRow(ctx,
		`SELECT COALESCE(is_container, false), COALESCE(input_schema_yaml, ''), COALESCE(variables_yaml, '')
		 FROM base_task_definition WHERE slug = $1 AND _deleted_at IS NULL`, slug).
		Scan(&facts.IsContainer, &schema, &variables)
	facts.Inputs, _ = db.ParseTaskInputSchema(schema)
	facts.Outputs = buildOutputsView(variables).Outputs
	cache[slug] = facts
	return facts
}

// Invariant 4: every mapped source resolves. A mapping value is a literal, an
// expression behind `expr:`, or a pill naming something in scope.
func checkPills(ctx context.Context, containerInputs []db.TaskInputField, blocks []db.StructureBlock, childKey map[string]db.StructureBlock) string {
	cache := map[string]plumbingChild{}
	containerNames := map[string]bool{}
	for _, field := range containerInputs {
		containerNames[field.Name] = true
	}
	for _, block := range blocks {
		if len(block.Mapping) == 0 {
			continue
		}
		ancestorInputs := map[string]bool{}
		for name := range containerNames {
			ancestorInputs[name] = true
		}
		for id := strings.TrimSpace(block.Parent); id != ""; {
			parent, found := childKey[id]
			if !found {
				break
			}
			if parent.Kind == "task" && parent.Slug != "" {
				for _, field := range childFacts(ctx, cache, parent.Slug).Inputs {
					ancestorInputs[field.Name] = true
				}
			}
			id = strings.TrimSpace(parent.Parent)
		}
		inLoop := false
		for id := strings.TrimSpace(block.Parent); id != ""; {
			parent, found := childKey[id]
			if !found {
				break
			}
			if parent.Kind == "control" && parent.Type == "for_each" {
				inLoop = true
			}
			id = strings.TrimSpace(parent.Parent)
		}
		earlierOutputs := map[string]bool{}
		for _, other := range blocks {
			if other.Key == block.Key {
				break
			}
			if strings.TrimSpace(other.Parent) != strings.TrimSpace(block.Parent) {
				continue
			}
			if other.Kind != "task" || other.Slug == "" {
				continue
			}
			name := strings.TrimSpace(other.Slug)
			if name == "" {
				name = plumbingLabel(other)
			}
			for _, output := range childFacts(ctx, cache, other.Slug).Outputs {
				earlierOutputs[name+"."+output] = true
			}
		}
		for _, value := range block.Mapping {
			text, ok := value.(string)
			if !ok {
				continue
			}
			text = strings.TrimSpace(text)
			if text == "" || strings.HasPrefix(text, "expr:") || !strings.HasPrefix(text, "$") {
				continue
			}
			if message := resolvePill(text, ancestorInputs, earlierOutputs, inLoop); message != "" {
				return fmt.Sprintf("Block %q: %s %s", plumbingLabel(block), text, message)
			}
		}
	}
	return ""
}

func resolvePill(text string, inputs, outputs map[string]bool, inLoop bool) string {
	switch {
	case strings.HasPrefix(text, "$inputs."):
		name := strings.TrimPrefix(text, "$inputs.")
		if inputs[name] {
			return ""
		}
		return "is not an input of this block's ancestors"
	case strings.HasPrefix(text, "$outputs."):
		name := strings.TrimPrefix(text, "$outputs.")
		if outputs[name] {
			return ""
		}
		return "is not an output of a block earlier in this scope"
	case text == "$item" || text == "$index" || strings.HasPrefix(text, "$item.") || strings.HasPrefix(text, "$index."):
		if inLoop {
			return ""
		}
		return "is only in scope inside a for_each"
	case text == "$now" || text == "$requester" || text == "$run.id":
		return ""
	case strings.HasPrefix(text, "$now.") || strings.HasPrefix(text, "$run.") || strings.HasPrefix(text, "$requester."):
		return ""
	default:
		return "does not name a value in scope"
	}
}

// Invariant 5: a for_each's collection resolves to an array. A literal array
// in config does; an input declared as text does not. An unresolvable
// reference is refused rather than deferred to runtime.
func checkCollections(ctx context.Context, containerInputs []db.TaskInputField, blocks []db.StructureBlock) string {
	arrayInputs := map[string]bool{}
	for _, field := range containerInputs {
		switch strings.ToLower(strings.TrimSpace(field.DataType)) {
		case "array", "list", "multiselect", "multi_select":
			arrayInputs[field.Name] = true
		}
	}
	for _, block := range blocks {
		if strings.TrimSpace(block.Kind) != "control" || block.Type != "for_each" {
			continue
		}
		if block.Config == nil {
			return fmt.Sprintf("Block %q: a for_each needs a collection that resolves to an array", plumbingLabel(block))
		}
		collection := block.Config["collection"]
		switch typed := collection.(type) {
		case []any:
			continue
		case []string:
			continue
		case string:
			text := strings.TrimSpace(typed)
			if strings.HasPrefix(text, "$inputs.") && arrayInputs[strings.TrimPrefix(text, "$inputs.")] {
				continue
			}
		}
		_ = ctx
		return fmt.Sprintf("Block %q: its collection does not resolve to an array", plumbingLabel(block))
	}
	return ""
}

// Invariant 7: a gate has both a completed and a rejected successor. A gate
// names its exits in config; the keys must name blocks in this document.
func checkGates(blocks []db.StructureBlock, keyIndex map[string]int) string {
	for _, block := range blocks {
		if strings.TrimSpace(block.Kind) != "gate" {
			continue
		}
		completed := configString(block.Config, "completed")
		rejected := configString(block.Config, "rejected")
		if completed == "" {
			return fmt.Sprintf("Block %q: a gate needs a completed successor", plumbingLabel(block))
		}
		if _, ok := keyIndex[completed]; !ok {
			return fmt.Sprintf("Block %q: its completed successor does not name a block in this document", plumbingLabel(block))
		}
		if rejected == "" {
			return fmt.Sprintf("Block %q: a gate needs a rejected successor", plumbingLabel(block))
		}
		if _, ok := keyIndex[rejected]; !ok {
			return fmt.Sprintf("Block %q: its rejected successor does not name a block in this document", plumbingLabel(block))
		}
	}
	return ""
}

// checkStoredPlumbing runs the plumbing check against a definition's stored
// document. The catalogue's order path uses it so a definition that could not
// be saved cannot be started either (Requirements AC-86).
func checkStoredPlumbing(ctx context.Context, slug string, admin ...bool) (string, error) {
	var id string
	var isContainer bool
	if err := db.Pool.QueryRow(ctx,
		`SELECT _id::text, COALESCE(is_container, false) FROM base_task_definition
		 WHERE slug = $1 AND _deleted_at IS NULL`, slug).Scan(&id, &isContainer); err != nil {
		return "", err
	}
	if !isContainer {
		return "", nil
	}
	blocks, err := db.StoredStructureDocument(ctx, id)
	if err != nil {
		return "", err
	}
	if len(blocks) == 0 {
		return "", nil
	}
	return checkPlumbing(ctx, slug, blocks, admin...), nil
}
