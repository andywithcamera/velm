package main

import (
	"regexp"
	"strings"
	"testing"
)

// Issue #351: the "Send test notification" control was in the template with a
// `hidden` attribute and revealed only by push-client.js. On an installed iOS
// PWA the notification bell showed "Disable push notifications" — so the
// client was running and had resolved the subscribed state — while the test
// button never appeared. The reveal path depends on JavaScript that arrives
// through the very cache being diagnosed, and a stale copy is indistinguishable
// from a working one.
//
// The control must therefore be decided by the server, not revealed by the
// client: the panel knows the account's subscription rows, so it can emit the
// control directly and it must work with no JavaScript at all.
func TestPushTestControlIsServerRendered(t *testing.T) {
	t.Chdir("../..")
	menu := readRepoFile(t, "web", "templates", "components", "notification-menu.html")
	handler := readRepoFile(t, "cmd", "server", "handleNotifications.go")
	view := readRepoFile(t, "cmd", "server", "handlePush.go")
	client := readRepoFile(t, "web", "static", "js", "push-client.js")

	// The panel must consult the subscription rows it already has available.
	if !strings.Contains(handler, "ListPushSubscriptionStates") {
		t.Fatal("the notification panel does not read push subscription state, so it cannot render the control")
	}
	if !strings.Contains(handler, "PushSubscribed") {
		t.Fatal("the panel view data has no PushSubscribed field")
	}

	// The template must emit the control unconditionally when subscribed, not
	// leave it to the client to reveal.
	// Only the subscribed branch: the unsubscribed branch is meant to ship
	// hidden, since there is nothing to send to yet.
	span := regexp.MustCompile(`(?s)\{\{if \.PushSubscribed\}\}(.*?)\{\{else\}\}`).FindStringSubmatch(menu)
	if span == nil {
		t.Fatal("notification-menu.html has no {{if .PushSubscribed}}...{{else}} block; the control still depends on JavaScript to appear")
	}
	block := span[1]
	control := regexp.MustCompile(`<button[^>]*id="pushTest"[^>]*>`).FindString(block)
	if control == "" {
		t.Fatalf("the subscribed branch has no #pushTest button:\n%s", block)
	}
	if regexp.MustCompile(`(^|\s)hidden(\s|>|$)`).MatchString(control) {
		t.Fatalf("the server-rendered test control ships hidden, so it still needs JavaScript: %q", control)
	}
	// A plain form submit is what survives a stale service worker.
	if !strings.Contains(block, `action="/api/push/test"`) || !strings.Contains(block, `method="post"`) {
		t.Fatalf("the test control is not a plain form POST:\n%s", block)
	}
	if !strings.Contains(block, `name="csrf_token"`) {
		t.Fatalf("the test form does not carry a CSRF token:\n%s", block)
	}

	// A form POST must land back in the app rather than dump JSON.
	if !strings.Contains(view, "wantsFormResponse") {
		t.Fatal("handlePushTest does not distinguish a browser form from the JSON client")
	}

	// The client may reveal the control but must never hide a server-rendered
	// one: a client that disagreed with the server about subscription state
	// would remove the only control that explains the silence.
	show := regexp.MustCompile(`(?s)function showTestButton\(on\) \{.*?\n  \}`).FindString(client)
	if show == "" {
		t.Fatal("showTestButton is missing from push-client.js")
	}
	if regexp.MustCompile(`hidden\s*=\s*!on`).MatchString(show) {
		t.Fatalf("showTestButton hides the control when it disagrees with the server:\n%s", show)
	}
}
