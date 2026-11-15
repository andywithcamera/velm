package main

import (
	"regexp"
	"strings"
	"testing"
)

// The board is the only page whose height follows the content rather than the
// window: four lanes holding three cards stopped a third of the way down and
// left the rest of the screen blank, which reads as a broken render rather than
// as a short list. These pin the three pieces that make it fill.

// boardCSS returns the refinements sheet with comments stripped, so a rule body
// can be matched on declarations alone.
func boardCSS(t *testing.T) string {
	t.Helper()
	t.Chdir("../..")
	css := readRepoFile(t, "web", "static", "css", "ui", "55-ui-refinements.css")
	return regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
}

// TestLanesClearTheRuleAboveThem covers the spacing complaint: the lane tops sat
// 2.4px under the action bar's rule, close enough to read as a collision. The
// value is asserted against the old one so a later tidy-up cannot quietly
// restore it.
func TestLanesClearTheRuleAboveThem(t *testing.T) {
	css := boardCSS(t)
	for _, body := range ruleBodies(t, css, ".ui-task-board-scroll") {
		if !strings.Contains(body, "padding:") {
			continue
		}
		if strings.Contains(body, "0.15rem") {
			t.Fatal("the strip is back to a 2.4px top padding, which puts the lanes against the rule above")
		}
		if !strings.Contains(body, "0.5rem 0.85rem 0.85rem") {
			t.Fatalf("unexpected strip padding %q", strings.TrimSpace(body))
		}
		return
	}
	t.Fatal("no padding rule for .ui-task-board-scroll")
}

// TestBoardGrowsToFillTheWindow checks the chain that lets the lanes stretch:
// a flex column from the shell down to the grid, and one implicit row told to
// take the grid's height. Any single link missing leaves the board its content
// height, silently — the page still renders, it is just short.
func TestBoardGrowsToFillTheWindow(t *testing.T) {
	css := boardCSS(t)

	for _, want := range []string{
		`.ui-main-shell:has(#taskBoard)`,
		`grid-auto-rows: 1fr`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("board layout is missing %q", want)
		}
	}

	// Shrink must stay off on the strip. With `flex: 1 1 auto` a full board is
	// absorbed into the strip's own vertical scrollbar, and `overflow-x: auto`
	// has already forced one on it, so the page stops growing and the overflow
	// hides inside a nested scroller.
	for _, sel := range []string{".ui-task-board-scroll", ".ui-task-board-grid"} {
		for _, body := range ruleBodies(t, css, sel) {
			if strings.Contains(body, "flex: 1 1 auto") {
				t.Errorf("%s uses `flex: 1 1 auto`; a tall board is then clipped into a nested scrollbar", sel)
			}
		}
	}

	// The side-by-side layout only. Under the lane-switcher breakpoint the lanes
	// stack, where stretching would give a single lane a screenful of empty box.
	media := regexp.MustCompile(`(?s)@media \(min-width: 721px\) \{(.*?)\n\}`).FindStringSubmatch(css)
	if media == nil {
		t.Fatal("the fill rules are not scoped to a min-width: 721px block")
	}
	if !strings.Contains(media[1], "grid-auto-rows: 1fr") {
		t.Error("grid-auto-rows is not inside the desktop-only block")
	}
}
