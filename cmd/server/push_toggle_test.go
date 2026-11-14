package main

import (
	"regexp"
	"strings"
	"testing"
)

// Issue #332: the notification menu shipped the push toggle with a `hidden`
// attribute and nothing ever removed it. With VAPID keys set the button still
// never appeared, so no device could subscribe and no push could ever be sent.
//
// This holds the pair together: a toggle that ships hidden must be revealed by
// the client script from the server's own config, and the client must not guess
// — it asks /api/push/config so an instance without keys never shows a control
// that cannot work.
func TestPushToggleIsRevealedFromServerConfig(t *testing.T) {
	t.Chdir("../..")
	menu := readRepoFile(t, "web", "templates", "components", "notification-menu.html")
	client := readRepoFile(t, "web", "static", "js", "push-client.js")
	handler := readRepoFile(t, "cmd", "server", "handlePush.go")

	toggle := regexp.MustCompile(`id="pushToggle"[^>]*>`)
	match := toggle.FindString(menu)
	if match == "" {
		t.Fatal("notification-menu.html has no #pushToggle button")
	}
	if !strings.Contains(match, "hidden") {
		t.Fatalf("#pushToggle ships visible; it should start hidden and be revealed only when push is configured: %q", match)
	}

	if !strings.Contains(client, "btn.hidden = false") {
		t.Fatal("push-client.js never reveals #pushToggle, so a configured instance still shows nothing to press")
	}

	// The reveal must be gated on the server's answer, not on the presence of a
	// browser API: PushManager exists in every modern browser whether or not
	// this instance can send anything.
	reveal := regexp.MustCompile(`(?s)async function revealIfAvailable\(\).*?\n  }`)
	body := reveal.FindString(client)
	if body == "" {
		t.Fatal("push-client.js has no revealIfAvailable()")
	}
	if !strings.Contains(body, "/api/push/config") {
		t.Fatalf("revealIfAvailable() does not consult /api/push/config:\n%s", body)
	}
	if !strings.Contains(body, "config.enabled") {
		t.Fatalf("revealIfAvailable() reveals the toggle without checking config.enabled:\n%s", body)
	}

	// And the endpoint it consults must report the gate the server actually
	// uses, or the client would show a control for an unconfigured instance.
	if !strings.Contains(handler, "push.Enabled()") {
		t.Fatal("handlePushConfig does not report push.Enabled(), so the client cannot know whether push works")
	}
	if !regexp.MustCompile(`"publicKey"`).MatchString(handler) {
		t.Fatal("handlePushConfig does not expose publicKey, so the client cannot subscribe")
	}
}
