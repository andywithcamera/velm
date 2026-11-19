package main

// Integration tests for slice 3 of the task definition editor: the step stepper
// and its mutations. Real Postgres, gated on TEST_DATABASE_URL (a skip here is a
// broken run, not a pass).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"velm/internal/db"
)

type stepFixture struct {
	ContainerSlug string
	ContainerID   string
	ChildSlugs    []string
	ChildIDs      []string
}

// seedStepFixture builds a container and n leaf children. Children use the
// two-input schema so a mapping has declared names to check against.
func seedStepFixture(t *testing.T, ctx context.Context, name string, children int) stepFixture {
	t.Helper()
	fixture := stepFixture{ContainerSlug: tdeSlug(t, name)}
	fixture.ContainerID = insertDefinition(t, ctx, fixture.ContainerSlug, "Upgrade OS "+name, "", "", true, true)
	for index := 0; index < children; index++ {
		slug := tdeSlug(t, name+"-c"+string(rune('a'+index)))
		id := insertDefinition(t, ctx, slug, "Child "+strings.ToUpper(string(rune('A'+index)))+" "+name, "", tdeTwoInputs, false, true)
		fixture.ChildSlugs = append(fixture.ChildSlugs, slug)
		fixture.ChildIDs = append(fixture.ChildIDs, id)
	}
	return fixture
}

func seedStep(t *testing.T, ctx context.Context, containerID, childID string, sortOrder int) {
	t.Helper()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO base_task_definition_child (container_definition_id, child_definition_id, sort_order)
		VALUES ($1, $2, $3)`, containerID, childID, sortOrder); err != nil {
		t.Fatalf("seed step: %v", err)
	}
}

func postStepAction(t *testing.T, slug, childSlug, action string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	path := "/task-definitions/" + slug + "/steps"
	if action != "" {
		path += "/" + childSlug + "/" + action
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// These tests exercise the HTMX path: the stepper is swapped into
	// #step-errors. The non-HTMX path has its own test (AC-16).
	req.Header.Set("HX-Request", "true")
	req.SetPathValue("slug", slug)
	if childSlug != "" {
		req.SetPathValue("child_slug", childSlug)
	}
	switch action {
	case "":
		handleTaskDefinitionStepAdd(rec, req)
	case "remove":
		handleTaskDefinitionStepRemove(rec, req)
	case "move":
		handleTaskDefinitionStepMove(rec, req)
	case "depends":
		handleTaskDefinitionStepDepends(rec, req)
	case "mapping":
		handleTaskDefinitionStepMapping(rec, req)
	default:
		t.Fatalf("unknown action %q", action)
	}
	return rec
}

func postBasics(t *testing.T, slug string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/task-definitions/"+slug+"/basics", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("slug", slug)
	handleTaskDefinitionBasicsSave(rec, req)
	return rec
}

func getStepAction(t *testing.T, slug, childSlug, action string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/task-definitions/" + slug + "/steps"
	if action != "" {
		path += "/" + childSlug + "/" + action
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetPathValue("slug", slug)
	req.SetPathValue("child_slug", childSlug)
	switch action {
	case "":
		handleTaskDefinitionStepAdd(rec, req)
	case "remove":
		handleTaskDefinitionStepRemove(rec, req)
	case "move":
		handleTaskDefinitionStepMove(rec, req)
	case "depends":
		handleTaskDefinitionStepDepends(rec, req)
	case "mapping":
		handleTaskDefinitionStepMapping(rec, req)
	}
	return rec
}

// stepListNode parses a rendered page and returns the element carrying the
// stepper's id, so assertions can be made on the stepper rather than on the
// whole page (a removed definition legitimately reappears in the add-step
// select).
func stepListNode(t *testing.T, body string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse editor page: %v", err)
	}
	var found *html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found != nil {
			return
		}
		if node.Type == html.ElementNode {
			for _, attribute := range node.Attr {
				if attribute.Key == "id" && attribute.Val == "step-list" {
					found = node
					return
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if found == nil {
		t.Fatalf("no element with id step-list rendered")
	}
	return found
}

// stepListChildren returns the element children of the step <ol>.
func stepListChildren(t *testing.T, body string) []*html.Node {
	t.Helper()
	var children []*html.Node
	for child := stepListNode(t, body).FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode {
			children = append(children, child)
		}
	}
	return children
}

// stepListHTML renders the contents of the step <ol>, in document order, as the
// substring assertions use.
func stepListHTML(t *testing.T, body string) string {
	t.Helper()
	var rendered strings.Builder
	for _, child := range stepListChildren(t, body) {
		if err := html.Render(&rendered, child); err != nil {
			t.Fatalf("render step list child: %v", err)
		}
	}
	return rendered.String()
}

func loadSteps(t *testing.T, ctx context.Context, containerID string) []db.TaskDefinitionStep {
	t.Helper()
	steps, err := db.ListDefinitionSteps(ctx, containerID)
	if err != nil {
		t.Fatalf("load steps: %v", err)
	}
	return steps
}

func stepSlugs(steps []db.TaskDefinitionStep) []string {
	slugs := make([]string, 0, len(steps))
	for _, step := range steps {
		slugs = append(slugs, step.ChildSlug)
	}
	return slugs
}

func assertStepRefusal(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("HX-Retarget"); got != stepErrorRegion {
		t.Errorf("HX-Retarget = %q, want %q", got, stepErrorRegion)
	}
	if got := rec.Header().Get("HX-Reswap"); got != "innerHTML" {
		t.Errorf("HX-Reswap = %q, want innerHTML", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, want) {
		t.Errorf("refusal does not contain %q; body: %s", want, body)
	}
	for _, leak := range []string{"SQLSTATE", "pgx", "23505", "P0001", "constraint", "uq_base_task"} {
		if strings.Contains(body, leak) {
			t.Errorf("refusal leaks %q: %s", leak, body)
		}
	}
}

// AC-41, AC-47: adding a step appends it at max+10 and the rendered stepper
// already contains it; a reload agrees.
func TestStepAddAppendsAndRenders(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	fixture := seedStepFixture(t, ctx, "stepadd", 2)

	rec := postStepAction(t, fixture.ContainerSlug, "", "", url.Values{"child_slug": {fixture.ChildSlugs[0]}})
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fixture.ChildSlugs[0]) {
		t.Error("the re-rendered stepper does not contain the added step")
	}
	steps := loadSteps(t, ctx, fixture.ContainerID)
	if len(steps) != 1 || steps[0].SortOrder != 10 {
		t.Fatalf("after one add: %+v, want one step at sort_order 10", steps)
	}

	rec = postStepAction(t, fixture.ContainerSlug, "", "", url.Values{"child_slug": {fixture.ChildSlugs[1]}})
	if rec.Code != http.StatusOK {
		t.Fatalf("second add status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	steps = loadSteps(t, ctx, fixture.ContainerID)
	if got := stepSlugs(steps); len(got) != 2 || got[0] != fixture.ChildSlugs[0] || got[1] != fixture.ChildSlugs[1] {
		t.Fatalf("step order = %v, want the add order", got)
	}
	if steps[1].SortOrder != 20 {
		t.Errorf("second step sort_order = %d, want 20", steps[1].SortOrder)
	}

	page := getRowsEditor(t, fixture.ContainerSlug)
	if !strings.Contains(page.Body.String(), fixture.ChildSlugs[1]) {
		t.Error("editor reload does not show the committed step")
	}
}

// AC-50, AC-51, AC-55, AC-56, AC-57: every add-step refusal is a 400 naming the
// thing in words, writes nothing and leaks no database internals.
func TestStepAddRefusals(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	fixture := seedStepFixture(t, ctx, "stepbad", 2)
	displayName := "Upgrade OS stepbad"
	_ = displayName

	rec := postStepAction(t, fixture.ContainerSlug, "", "", url.Values{"child_slug": {fixture.ContainerSlug}})
	assertStepRefusal(t, rec, stepSelfMessage)
	if !strings.Contains(rec.Body.String(), "Upgrade OS stepbad") {
		t.Error("self-containment refusal does not name the container")
	}

	rec = postStepAction(t, fixture.ContainerSlug, "", "", url.Values{"child_slug": {"no-such-definition-zz"}})
	assertStepRefusal(t, rec, "That definition does not exist: no-such-definition-zz")
	if !strings.Contains(rec.Body.String(), "Upgrade OS stepbad") {
		t.Error("absent-child refusal does not name the container")
	}

	deletedSlug := tdeSlug(t, "stepdeleted")
	insertDefinition(t, ctx, deletedSlug, "Deleted Child", "", tdeTwoInputs, false, true)
	if _, err := db.Pool.Exec(ctx, `UPDATE base_task_definition SET _deleted_at = NOW() WHERE slug = $1`, deletedSlug); err != nil {
		t.Fatalf("soft delete child: %v", err)
	}
	rec = postStepAction(t, fixture.ContainerSlug, "", "", url.Values{"child_slug": {deletedSlug}})
	assertStepRefusal(t, rec, "That definition does not exist: "+deletedSlug)

	if steps := loadSteps(t, ctx, fixture.ContainerID); len(steps) != 0 {
		t.Fatalf("refusals wrote %d steps, want 0", len(steps))
	}

	if rec := postStepAction(t, fixture.ContainerSlug, "", "", url.Values{"child_slug": {fixture.ChildSlugs[0]}}); rec.Code != http.StatusOK {
		t.Fatalf("first add failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = postStepAction(t, fixture.ContainerSlug, "", "", url.Values{"child_slug": {fixture.ChildSlugs[0]}})
	assertStepRefusal(t, rec, stepDuplicateMessage)
	if steps := loadSteps(t, ctx, fixture.ContainerID); len(steps) != 1 {
		t.Fatalf("duplicate refusal left %d steps, want 1", len(steps))
	}
}

// AC-42, AC-61: removal drops the row; a step another step waits on is refused
// and names every dependant.
func TestStepRemoveAndDependedOnRefusal(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	fixture := seedStepFixture(t, ctx, "stepremove", 3)
	for index, id := range fixture.ChildIDs {
		seedStep(t, ctx, fixture.ContainerID, id, (index+1)*10)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE base_task_definition_child SET depends_on = ARRAY[$3]::uuid[]
		WHERE container_definition_id = $1 AND child_definition_id = $2`,
		fixture.ContainerID, fixture.ChildIDs[1], fixture.ChildIDs[0]); err != nil {
		t.Fatalf("seed dependency: %v", err)
	}

	rec := postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[0], "remove", nil)
	assertStepRefusal(t, rec, "Step "+fixture.ChildSlugs[0]+" is depended on by "+fixture.ChildSlugs[1])
	if steps := loadSteps(t, ctx, fixture.ContainerID); len(steps) != 3 {
		t.Fatalf("refused removal left %d steps, want 3", len(steps))
	}

	rec = postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[2], "remove", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if steps := loadSteps(t, ctx, fixture.ContainerID); len(steps) != 2 {
		t.Fatalf("after removal %d steps, want 2", len(steps))
	}
	reloaded := getRowsEditor(t, fixture.ContainerSlug).Body.String()
	if list := stepListHTML(t, reloaded); strings.Contains(list, fixture.ChildSlugs[2]) {
		t.Error("reload still shows the removed step")
	}

	rec = postStepAction(t, fixture.ContainerSlug, "not-a-step-zz", "remove", nil)
	assertStepRefusal(t, rec, "Step not-a-step-zz is not in this container")
}

// AC-43: move swaps with the adjacent row in engine order; first-up and
// last-down are 200 no-ops.
func TestStepMoveSwapsAndNoOps(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	fixture := seedStepFixture(t, ctx, "stepmove", 3)
	// Deliberately out of insertion order: engine order is C, B, A.
	seedStep(t, ctx, fixture.ContainerID, fixture.ChildIDs[0], 20)
	seedStep(t, ctx, fixture.ContainerID, fixture.ChildIDs[1], 10)
	seedStep(t, ctx, fixture.ContainerID, fixture.ChildIDs[2], 0)

	want := func(order ...int) {
		t.Helper()
		steps := loadSteps(t, ctx, fixture.ContainerID)
		for index, child := range order {
			if steps[index].ChildSlug != fixture.ChildSlugs[child] {
				t.Fatalf("order = %v, want %v", stepSlugs(steps), order)
			}
		}
	}
	want(2, 1, 0)

	rec := postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[0], "move", url.Values{"direction": {"up"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("move up status = %d: %s", rec.Code, rec.Body.String())
	}
	want(2, 0, 1)

	rec = postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[0], "move", url.Values{"direction": {"down"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("move down status = %d: %s", rec.Code, rec.Body.String())
	}
	want(2, 1, 0)

	// First up and last down do nothing, and say so with a 200.
	rec = postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[2], "move", url.Values{"direction": {"up"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("first-up status = %d, want 200", rec.Code)
	}
	want(2, 1, 0)
	rec = postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[0], "move", url.Values{"direction": {"down"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("last-down status = %d, want 200", rec.Code)
	}
	want(2, 1, 0)

	rec = postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[0], "move", url.Values{"direction": {"sideways"}})
	assertStepRefusal(t, rec, "Direction must be up or down")
}

// AC-44, AC-52, AC-53, AC-54, AC-58: dependency edges are stored by id, the
// reason is validated against the closure vocabulary, and a cycle is refused by
// the database and translated.
func TestStepDependsStoresAndRefuses(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	fixture := seedStepFixture(t, ctx, "stepdep", 3)
	outsider := insertDefinition(t, ctx, tdeSlug(t, "stepdep-out"), "Outsider", "", tdeTwoInputs, false, true)
	_ = outsider
	outsiderSlug := tdeSlug(t, "stepdep-out2")
	insertDefinition(t, ctx, outsiderSlug, "Outsider Two", "", tdeTwoInputs, false, true)
	for index, id := range fixture.ChildIDs {
		seedStep(t, ctx, fixture.ContainerID, id, (index+1)*10)
	}

	rec := postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[1], "depends", url.Values{
		"depends_on":        {fixture.ChildSlugs[0]},
		"depends_on_reason": {"rejected"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("depends status = %d: %s", rec.Code, rec.Body.String())
	}
	steps := loadSteps(t, ctx, fixture.ContainerID)
	if len(steps[1].DependsOn) != 1 || steps[1].DependsOn[0] != fixture.ChildIDs[0] {
		t.Fatalf("depends_on = %v, want [%s]", steps[1].DependsOn, fixture.ChildIDs[0])
	}
	if steps[1].DependsOnReason != "rejected" {
		t.Errorf("reason = %q, want rejected", steps[1].DependsOnReason)
	}

	if rec := postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[1], "depends", url.Values{
		"depends_on": {fixture.ChildSlugs[0]},
	}); rec.Code != http.StatusOK {
		t.Fatalf("depends without reason status = %d: %s", rec.Code, rec.Body.String())
	}
	if steps := loadSteps(t, ctx, fixture.ContainerID); steps[1].DependsOnReason != "" {
		t.Errorf("absent reason stored %q, want ''", steps[1].DependsOnReason)
	}

	rec = postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[1], "depends", url.Values{
		"depends_on": {outsiderSlug},
	})
	assertStepRefusal(t, rec, "Depends on must be another step in this container: "+outsiderSlug)

	rec = postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[1], "depends", url.Values{
		"depends_on": {fixture.ChildSlugs[1]},
	})
	assertStepRefusal(t, rec, "A step cannot depend on itself")

	rec = postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[1], "depends", url.Values{
		"depends_on_reason": {"whenever"},
	})
	assertStepRefusal(t, rec, stepReasonMessage)

	// A depends on B, then B on A closes the loop; 084 refuses it. The earlier
	// blocks already stored B depends A, so clear that edge first or the very
	// first assignment is the cycle.
	if rec := postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[1], "depends", url.Values{
		"depends_on": {},
	}); rec.Code != http.StatusOK {
		t.Fatalf("clear B depends A status = %d: %s", rec.Code, rec.Body.String())
	}
	if steps := loadSteps(t, ctx, fixture.ContainerID); len(steps[1].DependsOn) != 0 {
		t.Fatalf("clear left depends_on = %v, want empty", steps[1].DependsOn)
	}
	if rec := postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[0], "depends", url.Values{
		"depends_on": {fixture.ChildSlugs[1]},
	}); rec.Code != http.StatusOK {
		t.Fatalf("set A depends B status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[1], "depends", url.Values{
		"depends_on": {fixture.ChildSlugs[0]},
	})
	assertStepRefusal(t, rec, stepCycleMessage)
	steps = loadSteps(t, ctx, fixture.ContainerID)
	if len(steps[1].DependsOn) != 0 {
		t.Errorf("cycle refusal changed the row: %v, want the cleared edge left alone", steps[1].DependsOn)
	}
	if got := steps[0].DependsOn; len(got) != 1 || got[0] != fixture.ChildIDs[1] {
		t.Errorf("cycle refusal disturbed the edge that was stored: %v", got)
	}
}

// AC-45, AC-46, AC-59, AC-60: mapping values are stored exactly, an empty value
// deletes the key, an undeclared key is refused, and an unparseable or empty
// child schema saves with a warning.
func TestStepMappingExactAndEmptyRemoves(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	fixture := seedStepFixture(t, ctx, "stepmap", 2)
	blankSlug := tdeSlug(t, "stepmap-blank")
	blankID := insertDefinition(t, ctx, blankSlug, "No Inputs", "", "", false, true)
	seedStep(t, ctx, fixture.ContainerID, fixture.ChildIDs[0], 10)
	seedStep(t, ctx, fixture.ContainerID, blankID, 20)

	rec := postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[0], "mapping", url.Values{
		"mapping__server": {"$inputs.server"},
		"mapping__reason": {"two  spaces $5.00"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("mapping status = %d: %s", rec.Code, rec.Body.String())
	}
	steps := loadSteps(t, ctx, fixture.ContainerID)
	if got := steps[0].InputMapping["server"]; got != "$inputs.server" {
		t.Errorf("server mapping = %q, want the submission verbatim", got)
	}
	if got := steps[0].InputMapping["reason"]; got != "two  spaces $5.00" {
		t.Errorf("reason mapping = %q, want spacing preserved", got)
	}
	page := getRowsEditor(t, fixture.ContainerSlug).Body.String()
	if !strings.Contains(page, "two  spaces $5.00") {
		t.Error("reload does not show the stored mapping")
	}

	rec = postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[0], "mapping", url.Values{
		"mapping__server": {"x"},
		"mapping__nosuch": {"y"},
	})
	assertStepRefusal(t, rec, "That definition has no input named nosuch")
	if steps := loadSteps(t, ctx, fixture.ContainerID); steps[0].InputMapping["server"] != "$inputs.server" {
		t.Error("refused mapping still changed the row")
	}

	rec = postStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[0], "mapping", url.Values{
		"mapping__server": {""},
		"mapping__reason": {"two  spaces $5.00"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("mapping removal status = %d: %s", rec.Code, rec.Body.String())
	}
	steps = loadSteps(t, ctx, fixture.ContainerID)
	if _, present := steps[0].InputMapping["server"]; present {
		t.Error("empty source did not remove the key")
	}
	if steps[0].InputMapping["reason"] != "two  spaces $5.00" {
		t.Error("removing one key disturbed another")
	}

	rec = postStepAction(t, fixture.ContainerSlug, blankSlug, "mapping", url.Values{
		"mapping__anything": {"$inputs.anything"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("unchecked mapping status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), stepNoInputsWarning) {
		t.Errorf("save with no declared inputs does not carry the warning: %s", rec.Body.String())
	}
	steps = loadSteps(t, ctx, fixture.ContainerID)
	if steps[1].InputMapping["anything"] != "$inputs.anything" {
		t.Error("unchecked mapping was not saved")
	}
}

// AC-14, AC-15: the stepper is one <ol> of one <li> per step, in engine order,
// whatever order the sort_order values were set in, with dependencies as
// sibling positions and every mapping pair.
func TestStepperRendersInEngineOrder(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	fixture := seedStepFixture(t, ctx, "steprender", 3)
	seedStep(t, ctx, fixture.ContainerID, fixture.ChildIDs[0], 20)
	seedStep(t, ctx, fixture.ContainerID, fixture.ChildIDs[1], 10)
	seedStep(t, ctx, fixture.ContainerID, fixture.ChildIDs[2], 0)
	if _, err := db.Pool.Exec(ctx, `
		UPDATE base_task_definition_child
		SET depends_on = ARRAY[$3]::uuid[], depends_on_reason = 'rejected',
		    input_mapping = '{"server": "$inputs.server"}'::jsonb
		WHERE container_definition_id = $1 AND child_definition_id = $2`,
		fixture.ContainerID, fixture.ChildIDs[1], fixture.ChildIDs[2]); err != nil {
		t.Fatalf("seed step metadata: %v", err)
	}

	body := getRowsEditor(t, fixture.ContainerSlug).Body.String()
	list := stepListHTML(t, body)
	if list == "" {
		t.Fatalf("no step list rendered")
	}
	// AC-14: exactly one <li> per step, and nothing else, as a *direct* child of
	// the <ol>. Nested lists and forms inside a step's <li> are expected.
	direct := stepListChildren(t, body)
	if len(direct) != 3 {
		t.Fatalf("the step <ol> has %d element children, want 3 <li>", len(direct))
	}
	for _, child := range direct {
		if child.Data != "li" {
			t.Errorf("the step <ol> has a non-<li> child (<%s>)", child.Data)
		}
	}
	order := []string{fixture.ChildSlugs[2], fixture.ChildSlugs[1], fixture.ChildSlugs[0]}
	last := -1
	for _, slug := range order {
		at := strings.Index(list, slug)
		if at < 0 {
			t.Fatalf("step %q missing from the stepper", slug)
		}
		if at < last {
			t.Errorf("step %q is out of engine order", slug)
		}
		last = at
	}
	for _, want := range []string{"#1 " + fixture.ChildSlugs[2], "rejected", "server → $inputs.server", "sort_order 10"} {
		if !strings.Contains(list, want) {
			t.Errorf("stepper does not show %q", want)
		}
	}
}

// AC-13: a leaf renders no stepper, says why and offers no add control.
func TestLeafRendersNoStepper(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "stepleaf")
	insertDefinition(t, ctx, slug, "Leaf Definition", "", tdeTwoInputs, false, true)

	body := getRowsEditor(t, slug).Body.String()
	if !strings.Contains(body, "This definition has no steps; it does no work itself.") {
		t.Error("leaf does not carry the no-steps message")
	}
	if strings.Contains(body, `id="step-stepper"`) || strings.Contains(body, "Add step") {
		t.Error("leaf renders a stepper or an add control")
	}
}

// AC-49: every mutation control is a button inside a POST form; no anchor points
// at a mutation path.
func TestStepControlsAreFormsNotLinks(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	fixture := seedStepFixture(t, ctx, "stepforms", 1)
	seedStep(t, ctx, fixture.ContainerID, fixture.ChildIDs[0], 10)
	body := getRowsEditor(t, fixture.ContainerSlug).Body.String()

	for _, action := range []string{"/steps", "/move", "/remove", "/depends", "/mapping"} {
		if strings.Contains(body, `<a href="/task-definitions/`+fixture.ContainerSlug+action) {
			t.Errorf("an anchor targets the mutation path %s", action)
		}
	}
	if !strings.Contains(body, `action="/task-definitions/`+fixture.ContainerSlug+`/steps"`) {
		t.Error("the add-step form is not a POST form")
	}
	moveForm := strings.Index(body, `action="/task-definitions/`+fixture.ContainerSlug+`/steps/`+fixture.ChildSlugs[0]+`/move"`)
	if moveForm < 0 {
		t.Fatal("the move control is not a form posting to the move route")
	}
}

// AC-48: a GET on any mutation path is 405 and writes nothing.
func TestStepMutationGetIsMethodNotAllowed(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	fixture := seedStepFixture(t, ctx, "stepget", 2)
	seedStep(t, ctx, fixture.ContainerID, fixture.ChildIDs[0], 10)
	if _, err := db.Pool.Exec(ctx, `
		UPDATE base_task_definition_child SET input_mapping = '{"server": "$inputs.server"}'::jsonb
		WHERE container_definition_id = $1 AND child_definition_id = $2`,
		fixture.ContainerID, fixture.ChildIDs[0]); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
	before := loadSteps(t, ctx, fixture.ContainerID)

	for _, action := range []string{"", "remove", "move", "depends", "mapping"} {
		rec := getStepAction(t, fixture.ContainerSlug, fixture.ChildSlugs[0], action)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %q = %d, want 405", action, rec.Code)
		}
	}
	after := loadSteps(t, ctx, fixture.ContainerID)
	if len(after) != len(before) {
		t.Fatalf("a GET changed the step count: %d -> %d", len(before), len(after))
	}
	if after[0].SortOrder != before[0].SortOrder || after[0].InputMapping["server"] != before[0].InputMapping["server"] {
		t.Error("a GET changed a step row")
	}
}

// AC-17: the outputs card lists outputs: as text and shows inputs:/locals:
// read-only under a note saying the engine reads neither.
func TestOutputsCardListsDeclaredNames(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	slug := tdeSlug(t, "stepoutputs")
	id := insertDefinition(t, ctx, slug, "Outputs Definition", "", "", true, true)
	if _, err := db.Pool.Exec(ctx, `UPDATE base_task_definition SET variables_yaml = $2 WHERE _id = $1`, id, `
outputs:
  - server_uptime
  - reboot_time
inputs:
  - not_an_output
locals:
  - scratch
`); err != nil {
		t.Fatalf("seed variables: %v", err)
	}

	body := getRowsEditor(t, slug).Body.String()
	for _, want := range []string{"server_uptime", "reboot_time", "not_an_output", "scratch", "read by nothing today"} {
		if !strings.Contains(body, want) {
			t.Errorf("outputs card does not show %q", want)
		}
	}
}

// AC-62: flipping is_container off while steps exist is refused and the rows
// stay. Slice 1 added the rule; this proves it still holds beside the stepper.
func TestBasicsRefusesLeafWhileStepsExist(t *testing.T) {
	ctx := taskDefinitionsTestDB(t)
	fixture := seedStepFixture(t, ctx, "stepbasic", 1)
	seedStep(t, ctx, fixture.ContainerID, fixture.ChildIDs[0], 10)

	rec := postBasics(t, fixture.ContainerSlug, url.Values{
		"display_name": {"Upgrade OS stepbasic"},
		"is_active":    {"yes"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if steps := loadSteps(t, ctx, fixture.ContainerID); len(steps) != 1 {
		t.Fatalf("refusal removed the step: %d steps", len(steps))
	}
}

// getRowsEditor renders the retained rows (stepper) surface by URL. Design
// Vision D1 made the canvas the only documented authoring view, so these ACs
// about the stepper are asserted at ?view=rows, which stays reachable and
// supported even though nothing links to it.
func getRowsEditor(t *testing.T, slug string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/task-definitions/"+slug+"?view=rows", nil)
	req.URL.RawQuery = "view=rows"
	req.SetPathValue("slug", slug)
	handleTaskDefinitionEditor(rec, req)
	return rec
}
