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
	"strings"

	"velm/internal/db"
)

type pillOptionView struct {
	Value string
	Label string
}

type canvasMappingView struct {
	Name    string
	Value   string
	Options []pillOptionView
}

type canvasBlockView struct {
	ID           string
	Label        string
	Slug         string
	Kind         string
	Type         string
	KindLabel    string
	Position     int
	Depth        int
	IndentClass  string
	ParentID     string
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
	Scopes       []canvasScopeView
	Document     string
	Error        string
	Notice       string
	Groups       []db.AuthzGroup
	Strategies   []string
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
// definition it calls so an unlabelled block is never "block 3".
func canvasBlockLabel(block db.DocumentBlock) string {
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
			ID:           block.ID,
			Label:        canvasBlockLabel(block),
			Slug:         block.Slug,
			Kind:         block.Kind,
			Type:         block.Type,
			KindLabel:    canvasKindLabel(block),
			Position:     block.Position,
			Depth:        d,
			IndentClass:  "ui-canvas-depth-" + itoa(d),
			ParentID:     block.ParentID,
			IsChain:      block.IsContainer,
			StepCount:    block.ChildStepCount,
			Condition:    configString(block.Config, "condition"),
			GroupID:      block.AssignmentGroupID,
			Strategy:     block.AssignmentStrategy,
			HasCondition: block.Kind == "control" && block.Type == "if",
		})
	}
	return view
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

// canvasPalette is the six block kinds of Design Vision §3, plus the control
// types. Kinds the engine cannot evaluate in this release are shown disabled
// with "not in this release" (Requirements AC-74).
func canvasPalette() []paletteKindView {
	return []paletteKindView{
		{Key: "trigger", Label: "Trigger", Kind: "trigger", Description: "What starts a run", Available: true},
		{Key: "task", Label: "Task", Kind: "task", Description: "Call a definition", Available: true},
		{Key: "control-if", Label: "If", Kind: "control", Type: "if", Description: "Run a scope when a condition holds", Available: true},
		{Key: "control-for-each", Label: "For each", Kind: "control", Type: "for_each", Description: "Run a scope once per item", Available: true},
		{Key: "control-end", Label: "End", Kind: "control", Type: "end", Description: "Close a scope", Available: true},
		{Key: "control-parallel", Label: "Parallel", Kind: "control", Type: "parallel", Description: "Run branches together", Available: false},
		{Key: "control-wait", Label: "Wait", Kind: "control", Type: "wait", Description: "Pause until a time or task", Available: false},
		{Key: "action", Label: "Action", Kind: "action", Description: "A non-human step", Available: false},
		{Key: "gate", Label: "Gate", Kind: "gate", Description: "A human approve/reject", Available: false},
		{Key: "stage", Label: "Stage", Kind: "stage", Description: "A human-facing lane", Available: false},
	}
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
func buildCanvasView(ctx context.Context, row db.TaskDefinitionRow, blocks []db.DocumentBlock, selectedID string) (canvasView, error) {
	view := canvasView{
		Palette:    canvasPalette(),
		Document:   encodeStructure(documentToStructure(blocks)),
		Strategies: db.AssignmentStrategies,
	}
	view.Blocks = buildCanvasDocument(blocks)
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
			view.Selected = view.Blocks[index]
			view.HasSelection = true
			pills, err := scopePillsFor(ctx, inputFields, blocks, block)
			if err != nil {
				return view, err
			}
			view.Selected.Pills = pills
			names, _ := declaredInputNames(block.InputSchemaYAML)
			for _, name := range names {
				view.Selected.Mapping = append(view.Selected.Mapping, canvasMappingView{
					Name:    name,
					Value:   mappingString(block.InputMapping[name]),
					Options: pills,
				})
			}
		}
		if block.Kind == "control" && (block.Type == "if" || block.Type == "for_each") {
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
