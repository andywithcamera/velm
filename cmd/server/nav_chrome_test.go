package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Issue #331: search is an icon beside the bell, and both of the panels the
// action cluster opens are anchored to the viewport. The bell's menu was
// right-aligned to a 36px button 108px from the right edge, so on a 390px phone
// its left edge landed at x=-70 and the panel was clipped off-screen.

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("read %v: %v", parts, err)
	}
	return string(source)
}

// ruleBodies returns every block whose selector list contains selector, in
// document order. A selector can appear both in a shared group and in its own
// rule, so callers get all of them.
func ruleBodies(t *testing.T, css, selector string) []string {
	t.Helper()
	// Comments sit between rules and would otherwise be captured as part of the
	// selector, so strip them before splitting.
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	pattern := regexp.MustCompile(`(?s)([^{}]+)\{([^}]*)\}`)
	var bodies []string
	for _, m := range pattern.FindAllStringSubmatch(css, -1) {
		if strings.HasPrefix(strings.TrimSpace(m[1]), "@") {
			continue
		}
		for _, part := range strings.Split(m[1], ",") {
			if strings.TrimSpace(part) == selector {
				bodies = append(bodies, m[2])
				break
			}
		}
	}
	if len(bodies) == 0 {
		t.Fatalf("no rule for %q", selector)
	}
	return bodies
}

func TestSearchIsAnIconBesideTheBell(t *testing.T) {
	t.Chdir("../..")
	navbar := readRepoFile(t, "web", "templates", "components", "navbar.html")
	shell := readRepoFile(t, "web", "static", "css", "ui", "20-shell-and-workspaces.css")

	// The field lives in a popover behind an icon button, not in the bar.
	if !strings.Contains(navbar, "ui-topnav-search-btn") {
		t.Fatal("the bar has no search icon button")
	}
	buttonTag := navbar[strings.Index(navbar, "ui-topnav-search-btn")-400:]
	if !strings.Contains(buttonTag[:600], "ui-topnav-icon") {
		t.Error("the search button does not carry .ui-topnav-icon, so it is not the bell's size")
	}
	if strings.Contains(navbar, "ui-topnav-center") {
		t.Error(".ui-topnav-center is still in the markup; the bar should not hold a full-width field")
	}
	if strings.Contains(navbar, "mobileSearchPanel") {
		t.Error("the separate mobile search panel survives; one icon should serve every width")
	}

	// The popover and the bell's menu are viewport-anchored, which is what keeps
	// them on screen, and both are width-clamped to the viewport.
	for _, sel := range []string{".ui-topnav-search-popover", ".ui-notification-menu"} {
		anchored, clamped := false, false
		for _, body := range ruleBodies(t, shell, sel) {
			if strings.Contains(body, "position: fixed") {
				anchored = true
			}
			if strings.Contains(body, "calc(100vw -") {
				clamped = true
			}
		}
		if !anchored {
			t.Errorf("%s is not position: fixed; anchoring it to the button is what pushed it off-screen", sel)
		}
		if !clamped {
			t.Errorf("%s has no viewport width clamp", sel)
		}
	}
}

// TestSearchPopoverKeepsTheSearchIds guards the htmx wiring: the popover moves
// the field, and the results target and the js binding are looked up by id.
func TestSearchPopoverKeepsTheSearchIds(t *testing.T) {
	t.Chdir("../..")
	navbar := readRepoFile(t, "web", "templates", "components", "navbar.html")
	for _, id := range []string{`id="searchInput"`, `id="searchResults"`, `id="searchPopover"`} {
		if !strings.Contains(navbar, id) {
			t.Errorf("navbar is missing %s", id)
		}
	}
	if strings.Contains(navbar, "searchResultsMobile") || strings.Contains(navbar, "mobileSearchInput") {
		t.Error("navbar still references the removed mobile search ids")
	}
}
