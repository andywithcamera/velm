package main

// The process canvas (Process Designer, Plan T6 / Requirements §4).
//
// The canvas is the third view of one document. It reads the stored block tree
// through db.LoadDefinitionDocument, renders it as an indented vertical list,
// and writes the whole document back to POST /task-definitions/{slug}/structure
// — the same endpoint the raw view posts to. The rows view (the v1 stepper) is
// kept unchanged, as Requirements AC-89 requires.
//
// Nesting is shown by indentation, not wires (Design Vision §8). A pill is
// picked from a select of things that are actually in scope, so an
// unresolvable value is hard to author and is refused at save (AC-78, AC-82).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
)

type pillOptionView struct {
	Value string
	Label string
}

type canvasMappingView struct {
	Name    string
	Label   string
	Type    string
	Value   string
	Mode    string
	Options []pillOptionView
}

type canvasBlockView struct {
	ID          string
	Label       string
	Slug        string
	Kind        string
	Type        string
	KindLabel   string
	Position    int
	Depth       int
	IndentClass string
	ParentID    string
	// Branch is "then"/"else" under an if; empty elsewhere. It is carried in
	// the canvas document so a save through the canvas does not erase it.
	Branch       string
	IsChain      bool
	StepCount    int
	Selected     bool
	Condition    string
	GroupID      string
	Strategy     string
	Mapping      []canvasMappingView
	Pills        []pillOptionView
	BlockJSON    string
	HasCondition bool
	// Inline configuration (Design Vision D13): the selected block is the one
	// expanded block, and its config renders inside its own card.
	Expanded bool
	// Keyboard move affordances (Requirements AC-43 to AC-45). Bounds are
	// resolved on the server from the stored tree so a disabled control is
	// never a lie about what the engine will accept.
	CanMoveUp   bool
	CanMoveDown bool
	CanIndent   bool
	CanOutdent  bool
	// run_script (Design Vision D4/D5). HasScript marks the block type;
	// ScriptRefused renders the refusal instead of the body for a non-admin.
	HasScript       bool
	ScriptRefused   bool
	ScriptBody      string
	ScriptTimeout   string
	ScriptOutputVar string
	// retry (Design Vision D17). HasRetry marks the block type; the rest are
	// its policy fields, rendered in the block's own card.
	HasRetry         bool
	RetryAttempts    string
	RetryDelay       string
	RetryBackoff     string
	RetryOnExhausted string
	// run_task (Design Vision D4/D9). HasRunTask marks the block type; the
	// target is the definition whose inputs the rows map.
	HasRunTask    bool
	RunTaskTarget string
}

type paletteKindView struct {
	Key         string
	Label       string
	Kind        string
	Type        string
	Description string
	Available   bool
}

type canvasScopeView struct {
	ID    string
	Label string
	Depth int
}

type canvasView struct {
	Blocks       []canvasBlockView
	Selected     canvasBlockView
	HasSelection bool
	Palette      []paletteKindView
	Candidates   []db.TaskDefinitionRow
	Targets      []db.TaskDefinitionRow
	Scopes       []canvasScopeView
	Document     string
	Error        string
	Notice       string
	Groups       []db.AuthzGroup
	Strategies   []string
	IsAdmin      bool
}

// canvasKindLabel is the word the canvas prints beside a block.
func canvasKindLabel(block db.DocumentBlock) string {
	switch block.Kind {
	case "trigger":
		return "Trigger"
	case "control":
		switch block.Type {
		case "if":
			return "If"
		case "for_each":
			return "For each"
		case "end":
			return "End"
		case "retry":
			return "Retry"
		case "run_task":
			return "Run task"
		default:
			return "Control"
		}
	case "task":
		return "Task"
	default:
		return titleizeBreadcrumbLabel(block.Kind)
	}
}

// canvasBlockLabel is what the author called the block, falling back to the
// definition it calls so an unlabelled block is never "block 3". The trigger
// is structural, so it always reads Start (Design Vision D6).
func canvasBlockLabel(block db.DocumentBlock) string {
	if block.Kind == "trigger" {
		return "Start"
	}
	if label := strings.TrimSpace(block.Label); label != "" {
		return label
	}
	if block.DisplayName != "" {
		return block.DisplayName
	}
	if block.Slug != "" {
		return block.Slug
	}
	return canvasKindLabel(block) + " block"
}

// buildCanvasDocument flattens the stored tree into document order with a
// depth per block. Order is the database's position order; depth comes from
// walking the parent chain, so a nested block's indent class differs from its
// parent's (Requirements AC-71, AC-72).
func buildCanvasDocument(blocks []db.DocumentBlock) []canvasBlockView {
	byID := make(map[string]db.DocumentBlock, len(blocks))
	for _, block := range blocks {
		byID[block.ID] = block
	}
	depthOf := map[string]int{}
	var depth func(id string) int
	depth = func(id string) int {
		if d, ok := depthOf[id]; ok {
			return d
		}
		block, found := byID[id]
		if !found || block.ParentID == "" {
			depthOf[id] = 0
			return 0
		}
		d := depth(block.ParentID) + 1
		depthOf[id] = d
		return d
	}
	view := make([]canvasBlockView, 0, len(blocks))
	for _, block := range blocks {
		d := depth(block.ID)
		view = append(view, canvasBlockView{
			ID:               block.ID,
			Label:            canvasBlockLabel(block),
			Slug:             block.Slug,
			Kind:             block.Kind,
			Type:             block.Type,
			KindLabel:        canvasKindLabel(block),
			Position:         block.Position,
			Depth:            d,
			IndentClass:      canvasIndentClass(d),
			ParentID:         block.ParentID,
			Branch:           block.Branch,
			IsChain:          block.IsContainer,
			StepCount:        block.ChildStepCount,
			Condition:        configString(block.Config, "condition"),
			GroupID:          block.AssignmentGroupID,
			Strategy:         block.AssignmentStrategy,
			HasCondition:     block.Kind == "control" && block.Type == "if",
			HasScript:        block.Kind == "control" && block.Type == "run_script",
			ScriptBody:       configString(block.Config, "script"),
			ScriptTimeout:    configString(block.Config, "timeout_ms"),
			ScriptOutputVar:  configString(block.Config, "output_var"),
			HasRunTask:       block.Kind == "control" && block.Type == "run_task",
			RunTaskTarget:    block.Slug,
			HasRetry:         block.Kind == "control" && block.Type == "retry",
			RetryAttempts:    configString(block.Config, "attempts"),
			RetryDelay:       configString(block.Config, "delay_seconds"),
			RetryBackoff:     configString(block.Config, "backoff"),
			RetryOnExhausted: configString(block.Config, "on_exhausted"),
		})
	}
	return view
}

// canvasIndentClass caps the indent ladder at depth 4 (Requirements AC-49):
// nesting is unconstrained at save, so a seven-level chain must render rather
// than silently lose its indentation class.
func canvasIndentClass(depth int) string {
	if depth > 4 {
		depth = 4
	}
	return "ui-canvas-depth-" + itoa(depth)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func configString(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

// canvasPalette is the block kinds an author can add. The trigger is absent
// on purpose: it is rendered as a fixed Start node and never inserted from the
// palette (Design Vision D6, Requirements AC-11). Kinds the engine cannot
// evaluate in this release are shown disabled with "not in this release"
// (Requirements AC-74).
func canvasPalette(admin ...bool) []paletteKindView {
	isAdmin := len(admin) > 0 && admin[0]
	palette := []paletteKindView{
		{Key: "task", Label: "Task", Kind: "task", Description: "Call a definition", Available: true},
		{Key: "control-if", Label: "If", Kind: "control", Type: "if", Description: "Run a scope when a condition holds", Available: true},
		{Key: "control-for-each", Label: "For each", Kind: "control", Type: "for_each", Description: "Run a scope once per item", Available: true},
		{Key: "control-end", Label: "End", Kind: "control", Type: "end", Description: "Close a scope", Available: true},
		{Key: "control-retry", Label: "Retry", Kind: "control", Type: "retry", Description: "Run a scope again when it fails", Available: true},
		{Key: "control-run-task", Label: "Run task", Kind: "control", Type: "run_task", Description: "Call a definition with mapped inputs", Available: true},
		{Key: "control-parallel", Label: "Parallel", Kind: "control", Type: "parallel", Description: "Run branches together", Available: false},
		{Key: "control-wait", Label: "Wait", Kind: "control", Type: "wait", Description: "Pause until a time or task", Available: false},
		{Key: "action", Label: "Action", Kind: "action", Description: "A non-human step", Available: false},
		{Key: "gate", Label: "Gate", Kind: "gate", Description: "A human approve/reject", Available: false},
		{Key: "stage", Label: "Stage", Kind: "stage", Description: "A human-facing lane", Available: false},
	}
	// AC-57: run_script is hidden, not disabled, for a non-admin.
	if isAdmin {
		palette = append(palette, paletteKindView{Key: "control-run-script", Label: "Run script", Kind: "control", Type: "run_script", Description: "Run a JavaScript body (admin only)", Available: true})
	}
	return palette
}

// documentToStructure turns the stored tree into the payload the structure
// endpoint accepts. The canvas submits this same document, so both paths store
// the same thing (Requirements AC-70, AC-88).
func documentToStructure(blocks []db.DocumentBlock) []db.StructureBlock {
	out := make([]db.StructureBlock, 0, len(blocks))
	for _, block := range blocks {
		mapping := map[string]any{}
		for key, value := range block.InputMapping {
			mapping[key] = value
		}
		out = append(out, db.StructureBlock{
			Key:                block.ID,
			Parent:             block.ParentID,
			Branch:             block.Branch,
			Kind:               block.Kind,
			Type:               block.Type,
			Slug:               block.Slug,
			Label:              canvasBlockLabel(block),
			Position:           block.Position,
			Config:             block.Config,
			Mapping:            mapping,
			DependsOn:          block.DependsOnBlockIDs,
			AssignmentGroupID:  block.AssignmentGroupID,
			AssignmentStrategy: block.AssignmentStrategy,
		})
	}
	return out
}

func encodeStructure(blocks []db.StructureBlock) string {
	payload, err := json.Marshal(map[string]any{"blocks": blocks})
	if err != nil {
		return `{"blocks":[]}`
	}
	return string(payload)
}

// scopePillsFor collects the values a block may pick: the definition's own
// inputs (an ancestor's inputs), outputs of blocks earlier in this scope, the
// loop variables inside a for_each, and the live globals. Requirements AC-78.
func scopePillsFor(ctx context.Context, containerInputs []db.TaskInputField, blocks []db.DocumentBlock, target db.DocumentBlock) ([]pillOptionView, error) {
	pills := []pillOptionView{
		{Value: "$now", Label: "$now"},
		{Value: "$run.id", Label: "$run.id"},
		{Value: "$requester", Label: "$requester"},
	}
	for _, field := range containerInputs {
		pills = append(pills, pillOptionView{Value: "$inputs." + field.Name, Label: "$inputs." + field.Name})
	}
	// $item/$index are only in scope beneath a for_each.
	inLoop := false
	byID := map[string]db.DocumentBlock{}
	for _, block := range blocks {
		byID[block.ID] = block
	}
	for id := target.ParentID; id != ""; {
		parent, found := byID[id]
		if !found {
			break
		}
		if parent.Kind == "control" && parent.Type == "for_each" {
			inLoop = true
		}
		id = parent.ParentID
	}
	if inLoop {
		pills = append(pills, pillOptionView{Value: "$item", Label: "$item"}, pillOptionView{Value: "$index", Label: "$index"})
	}
	// Outputs of earlier blocks at the same scope.
	seenTarget := false
	for _, block := range blocks {
		if block.ID == target.ID {
			seenTarget = true
			continue
		}
		if !seenTarget || block.ParentID != target.ParentID {
			continue
		}
		name := strings.TrimSpace(block.Slug)
		if name == "" {
			name = canvasBlockLabel(block)
		}
		for _, output := range buildOutputsView(block.VariablesYAML).Outputs {
			pills = append(pills, pillOptionView{Value: "$outputs." + name + "." + output, Label: "$outputs." + name + "." + output})
		}
	}
	_ = ctx
	return pills, nil
}

// buildCanvasView assembles the canvas, its palette, the candidate list and
// the config rail. The rail's selection comes from ?block=<id>.
func buildCanvasView(ctx context.Context, row db.TaskDefinitionRow, blocks []db.DocumentBlock, selectedID string, admin ...bool) (canvasView, error) {
	isAdmin := len(admin) > 0 && admin[0]
	view := canvasView{
		IsAdmin:    isAdmin,
		Palette:    canvasPalette(isAdmin),
		Document:   encodeStructure(documentToStructure(blocks)),
		Strategies: db.AssignmentStrategies,
	}
	view.Blocks = buildCanvasDocument(blocks)
	applyMoveCapabilities(blocks, view.Blocks)
	if selectedID == "" {
		for _, block := range blocks {
			if block.Kind != "trigger" {
				selectedID = block.ID
				break
			}
		}
		if selectedID == "" && len(blocks) > 0 {
			selectedID = blocks[0].ID
		}
	}
	inputFields, _ := db.ParseTaskInputSchema(row.InputSchemaYAML)
	for index := range view.Blocks {
		block := blocks[index]
		if block.ID == selectedID {
			view.Blocks[index].Selected = true
			view.Blocks[index].Expanded = true
			pills, err := scopePillsFor(ctx, inputFields, blocks, block)
			if err != nil {
				return view, err
			}
			// The card template ranges over Blocks, so the pills and mapping
			// rows belong on the block itself; Selected is a copy for the
			// sections that read it directly.
			view.Blocks[index].Pills = pills
			// AC-97: one row per input of the block's target, labelled with
			// the input's own label where it has one. For a run_task the
			// block's InputSchemaYAML is the target's schema, joined on the
			// definition the block stores.
			fields, _ := db.ParseTaskInputSchema(block.InputSchemaYAML)
			for _, field := range fields {
				value, mode := mappingRow(block.InputMapping[field.Name])
				view.Blocks[index].Mapping = append(view.Blocks[index].Mapping, canvasMappingView{
					Name:    field.Name,
					Label:   field.LabelOrName(),
					Type:    mappingTypeLabel(field.DataType),
					Value:   value,
					Mode:    mode,
					Options: pills,
				})
			}
			view.Selected = view.Blocks[index]
			view.HasSelection = true
		}
		if block.Kind == "control" && block.Type == "run_script" && !isAdmin {
			view.Blocks[index].ScriptRefused = true
		}
		if block.Kind == "control" && (block.Type == "if" || block.Type == "for_each" || block.Type == "retry") {
			view.Scopes = append(view.Scopes, canvasScopeView{ID: block.ID, Label: canvasBlockLabel(block), Depth: view.Blocks[index].Depth})
		}
	}

	// The palette's definition list excludes the edited definition and the
	// definitions already present as blocks, exactly as the stepper's
	// candidate filter does (Requirements AC-75).
	present := map[string]bool{row.Slug: true}
	for _, block := range blocks {
		if block.Slug != "" {
			present[block.Slug] = true
		}
	}
	candidates, err := db.ListTaskDefinitionRows(ctx, "")
	if err != nil {
		return view, err
	}
	for _, candidate := range candidates {
		if present[candidate.Slug] {
			continue
		}
		view.Candidates = append(view.Candidates, candidate)
	}
	// A run_task's target list is every live definition except this one
	// (AC-95). A target already used elsewhere in the tree is still a legal
	// target, so it is not filtered out the way a palette candidate is.
	for _, candidate := range candidates {
		if candidate.Slug == row.Slug {
			continue
		}
		view.Targets = append(view.Targets, candidate)
	}
	return view, nil
}

func mappingString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// mappingRow splits a stored mapping value into the text the row shows and
// its mode. A bare string is the pre-slice-6 form where a `$` prefix means an
// expression; a run_task row stores {from, value} (AC-96, AC-99).
func mappingRow(value any) (string, string) {
	if row, ok := value.(map[string]any); ok {
		from, _ := row["from"].(string)
		text, _ := row["value"].(string)
		if from == "literal" || from == "expr" {
			return text, from
		}
		return text, ""
	}
	text := mappingString(value)
	if strings.HasPrefix(strings.TrimSpace(text), "$") {
		return text, "expr"
	}
	if text != "" {
		return text, "literal"
	}
	return "", ""
}

// isAdminRequest is the existing role predicate (Design Vision D5): the same
// context role the rest of the server reads, not a new role.
func isAdminRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(auth.UserRoleFromRequest(r)), "admin")
}

// mappingTypeLabel is the word a run_task mapping row prints beside the input
// name. A reference input is an object, not a string, so the author can see
// that a literal cannot fill it; everything else prints its canonical type.
func mappingTypeLabel(dataType string) string {
	switch db.BaseDataType(dataType) {
	case "reference", "entity":
		return "object"
	case "":
		return "text"
	default:
		return db.BaseDataType(dataType)
	}
}
