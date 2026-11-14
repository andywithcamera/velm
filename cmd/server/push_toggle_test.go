package main

import (
	"regexp"
	"strings"
	"testing"
)

// Issue #332/#337: the push toggle shipped with a `hidden` attribute and
// nothing removed it. The first fix added `btn.hidden = false` to
// revealIfAvailable(), which passed a test that only looked for that line —
// but the toggle lives in the notification menu, which htmx fetches when the
// bell is clicked. #pushToggle is not in the DOM when the script runs, so
// `const btn = document.getElementById('pushToggle'); if (!btn) return;`
// exited before any reveal. The line existed and never executed.
//
// These assertions are about reachability rather than spelling: the client
// must not bind the button at load, must not bail when it is absent, and must
// re-reveal after the panel is swapped in.
func TestPushToggleReachableAfterHtmxSwap(t *testing.T) {
	t.Chdir("../..")
	menu := readRepoFile(t, "web", "templates", "components", "notification-menu.html")
	client := readRepoFile(t, "web", "static", "js", "push-client.js")
	navbar := readRepoFile(t, "web", "templates", "components", "navbar.html")
	handler := readRepoFile(t, "cmd", "server", "handlePush.go")

	// The panel really is fetched after load; if that ever changes, this whole
	// test is guarding the wrong shape and should be revisited.
	if !strings.Contains(navbar, `hx-get="/api/notifications/panel`) {
		t.Fatal("navbar no longer htmx-loads the notification panel; re-check this test's premise")
	}

	toggle := regexp.MustCompile(`id="pushToggle"[^>]*>`)
	match := toggle.FindString(menu)
	if match == "" {
		t.Fatal("notification-menu.html has no #pushToggle button")
	}
	if !strings.Contains(match, "hidden") {
		t.Fatalf("#pushToggle ships visible; it should start hidden and be revealed only when push is configured: %q", match)
	}

	// Must not bind at load: that is what made the reveal dead code. Only the
	// load-time head is inspected — an `if (!btn) return;` inside a function is
	// correct and necessary.
	head := client
	if i := strings.Index(client, "function "); i >= 0 {
		head = client[:i]
	}
	if strings.Contains(head, "getElementById('pushToggle')") {
		t.Fatalf("push-client.js binds #pushToggle at load; it is not in the DOM until htmx swaps the panel in:\n%s", head)
	}
	// A load-time bail on a missing element is the same failure in another
	// spelling: nothing below it ever runs.
	if regexp.MustCompile(`(?m)^\s*if \(![a-zA-Z]+\) return;`).MatchString(head) {
		t.Fatalf("push-client.js bails out at load when an element is absent, so nothing below ever runs:\n%s", head)
	}

	// And it must re-run once the panel arrives.
	if !strings.Contains(client, "htmx:afterSettle") && !strings.Contains(client, "htmx:afterSwap") && !strings.Contains(client, "htmx:load") {
		t.Fatal("push-client.js never re-reveals after an htmx swap, so the toggle stays hidden until a full page load")
	}
	if !strings.Contains(client, "revealIfAvailable") {
		t.Fatal("push-client.js has no revealIfAvailable()")
	}
	// Delegation, for the same reason: a direct listener cannot attach to a
	// button that does not exist yet.
	if regexp.MustCompile(`btn\.addEventListener\('click'`).MatchString(client) {
		t.Fatal("push-client.js attaches the click handler directly to the button, which is absent at load")
	}

	// The reveal must be gated on the server's answer, not on a browser API:
	// PushManager exists whether or not this instance can send anything.
	reveal := regexp.MustCompile(`(?s)async function revealIfAvailable\(\).*?\n  \}`).FindString(client)
	if reveal == "" {
		t.Fatal("push-client.js has no revealIfAvailable()")
	}
	if !strings.Contains(reveal, "/api/push/config") || !strings.Contains(reveal, "config.enabled") {
		t.Fatalf("revealIfAvailable() must consult /api/push/config and check config.enabled:\n%s", reveal)
	}
	if !strings.Contains(handler, "push.Enabled()") {
		t.Fatal("handlePushConfig does not report push.Enabled(), so the client cannot know whether push works")
	}
	if !regexp.MustCompile(`"publicKey"`).MatchString(handler) {
		t.Fatal("handlePushConfig does not expose publicKey, so the client cannot subscribe")
	}
}
