package main

import (
	"net/http/httptest"
	"testing"
)

// The closed lane is uncapped but paged: the first page is one size, and any
// closed_limit a caller asks for snaps to the same grid so the sentinel's next
// request is always current+pageSize.
func TestParseTaskBoardClosedLimit(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", taskBoardClosedPageSize},
		{"0", taskBoardClosedPageSize},
		{"-100", taskBoardClosedPageSize},
		{"not-a-number", taskBoardClosedPageSize},
		{"50", taskBoardClosedPageSize},
		{"100", 100},
		{"250", 200},
		{"1000", 1000},
		{"999999", taskBoardClosedPageMax},
	}
	for _, c := range cases {
		if got := parseTaskBoardClosedLimit(c.raw); got != c.want {
			t.Errorf("parseTaskBoardClosedLimit(%q) = %d, want %d", c.raw, got, c.want)
		}
	}
}

// Paging must not widen the query: every active filter travels with the next
// page, and only closed_limit changes.
func TestTaskBoardClosedNextURLKeepsFilters(t *testing.T) {
	req := httptest.NewRequest("GET", "/task?scope=mine&Priority=high&q=invoice+overdue&closed_limit=200", nil)
	got := taskBoardClosedNextURL(req, 200)
	want := taskBoardURL() + "?Priority=high&closed_limit=300&q=invoice+overdue&scope=mine"
	if got != want {
		t.Errorf("taskBoardClosedNextURL = %q, want %q", got, want)
	}
}

func TestTaskBoardClosedNextURLStopsAtMax(t *testing.T) {
	req := httptest.NewRequest("GET", "/task", nil)
	if got := taskBoardClosedNextURL(req, taskBoardClosedPageMax); got != taskBoardURL()+"?closed_limit=5000" {
		t.Errorf("at the cap the next URL = %q, want closed_limit=5000", got)
	}
}

// The closed lane loads a page but reports the real lane size, otherwise the
// header badge would say "100" on a board with 900 closed tasks.
func TestBuildTaskBoardLanesReportsFullClosedTotal(t *testing.T) {
	choices := []taskBoardChoice{{Value: "closed", Label: "Closed"}}
	items := []taskBoardItem{
		{State: "closed", Card: taskBoardCard{ID: "a"}, StateGroupTotal: 900},
		{State: "closed", Card: taskBoardCard{ID: "b"}, StateGroupTotal: 900},
	}
	lanes := buildTaskBoardLanes(choices, items)
	closed := taskBoardLaneByValue(lanes, "closed")
	if closed == nil {
		t.Fatal("no closed lane")
	}
	if closed.Count != 900 {
		t.Errorf("closed lane count = %d, want 900", closed.Count)
	}
	if len(closed.Cards) != 2 {
		t.Errorf("closed lane cards = %d, want 2", len(closed.Cards))
	}
	if taskBoardLaneLoadedCount(lanes, "closed") != 2 || taskBoardLaneTotalCount(lanes, "closed") != 900 {
		t.Errorf("loaded/total = %d/%d, want 2/900",
			taskBoardLaneLoadedCount(lanes, "closed"), taskBoardLaneTotalCount(lanes, "closed"))
	}
}

func taskBoardLaneByValue(lanes []taskBoardLane, value string) *taskBoardLane {
	for i := range lanes {
		if lanes[i].Value == value {
			return &lanes[i]
		}
	}
	return nil
}

// state_group_total is authoritative for every lane, so an uncapped lane's
// count comes out the same either way: the rows returned are the whole lane.
func TestBuildTaskBoardLanesUncappedLaneCountUnchanged(t *testing.T) {
	choices := []taskBoardChoice{{Value: "ready", Label: "Ready"}}
	items := []taskBoardItem{{State: "ready", Card: taskBoardCard{ID: "a"}, StateGroupTotal: 3}}
	lanes := buildTaskBoardLanes(choices, items)
	ready := taskBoardLaneByValue(lanes, "ready")
	if ready == nil {
		t.Fatal("no ready lane")
	}
	if ready.Count != 3 {
		t.Errorf("ready lane count = %d, want 3 (state_group_total is the lane size)", ready.Count)
	}
}
