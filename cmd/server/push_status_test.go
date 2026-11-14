package main

import (
	"regexp"
	"strings"
	"testing"
)

// Issue #344: a notification reached the in-app bell but never the phone, and
// nothing anywhere reported whether a push subscription existed. The client
// guessed its own state from localStorage plus the browser's own subscription,
// so a browser that had asked but whose POST never landed still rendered
// "Disable push notifications". The server is the only authority on this: it
// holds the rows the sender reads.
func TestPushStatusIsAuthoritative(t *testing.T) {
	t.Chdir("../..")
	routes := readRepoFile(t, "cmd", "server", "routes.go")
	handler := readRepoFile(t, "cmd", "server", "handlePush.go")
	client := readRepoFile(t, "web", "static", "js", "push-client.js")

	// Authenticated: the answer is per user, so it must not be a public route
	// like /api/push/config.
	statusRoute := regexp.MustCompile(`(?m)^\s*http\.Handle\("/api/push/status".*$`).FindString(routes)
	if statusRoute == "" {
		t.Fatal("/api/push/status is not registered")
	}
	if !strings.Contains(statusRoute, "withAuth") {
		t.Fatalf("/api/push/status must be authenticated: %q", statusRoute)
	}

	// It must report the state the sender actually uses, not config flags.
	body := regexp.MustCompile(`(?s)func handlePushStatus\(.*?\n\}`).FindString(handler)
	if body == "" {
		t.Fatal("handlePushStatus does not exist")
	}
	if !strings.Contains(body, "ListPushSubscription") {
		t.Fatalf("handlePushStatus must read the subscription rows, not infer state:\n%s", body)
	}
	if !strings.Contains(body, `"subscribed"`) {
		t.Fatalf("handlePushStatus does not report subscribed:\n%s", body)
	}

	// And the client must ask.
	if !strings.Contains(client, "/api/push/status") {
		t.Fatal("push-client.js never consults /api/push/status; local state can be wrong")
	}
}

// TestPushFailuresAreVisible pins the second half of #344. Every stage of
// opt-in could fail — permission, registration, subscribe, POST — and the
// errors were reported with console.warn, which nobody can see in an iOS
// home-screen app. The button is the only surface that reaches the user.
func TestPushFailuresAreVisible(t *testing.T) {
	t.Chdir("../..")
	client := readRepoFile(t, "web", "static", "js", "push-client.js")

	if regexp.MustCompile(`console\.(warn|error)\([^)]*push`).MatchString(client) {
		t.Fatal("push-client.js still reports failures only to the console")
	}

	enable := regexp.MustCompile(`(?s)async function enable\(\).*?\n  \}`).FindString(client)
	if enable == "" {
		t.Fatal("push-client.js has no enable()")
	}
	// Each failable stage must name itself, so the message locates the fault.
	for _, stage := range []string{
		"Notification permission failed",
		"Service worker registration failed",
		"Push subscribe failed",
		"Saving subscription failed",
	} {
		if !strings.Contains(enable, stage) {
			t.Fatalf("enable() does not report %q on the button:\n%s", stage, enable)
		}
	}

	// An error must survive: refreshing state afterwards would repaint the
	// plain label over the message, hiding the reason it failed.
	click := regexp.MustCompile(`(?s)document\.addEventListener\('click'.*?\n  \}\);`).FindString(client)
	if click == "" {
		t.Fatal("push-client.js has no delegated click handler")
	}
	if !regexp.MustCompile(`if \(ok\)\s*refreshState\(\)`).MatchString(click) {
		t.Fatalf("the click handler refreshes after a failure, erasing the error message:\n%s", click)
	}

	// enable() must distinguish success from failure rather than returning
	// undefined on both.
	if !regexp.MustCompile(`ok = await enable\(\)`).MatchString(click) {
		t.Fatalf("the click handler ignores enable()'s outcome:\n%s", click)
	}
	if !strings.Contains(enable, "return false") || !strings.Contains(enable, "return true") {
		t.Fatalf("enable() must return true only when a subscription is stored:\n%s", enable)
	}
}

// TestPushStatusReportsDeliveryOutcome pins the rest of #344. "Subscribed"
// and "delivers" are different facts, and a rejected send used to look
// identical to a delivered one — webpush-go returns the HTTP response
// unchanged and Go's client reports no error for a 403, so Send counted it as
// sent. The status route must therefore surface the push service's answer, and
// the user must be able to ask for a test send and see that answer.
func TestPushStatusReportsDeliveryOutcome(t *testing.T) {
	t.Chdir("../..")
	handler := readRepoFile(t, "cmd", "server", "handlePush.go")
	routes := readRepoFile(t, "cmd", "server", "routes.go")
	menu := readRepoFile(t, "web", "templates", "components", "notification-menu.html")
	client := readRepoFile(t, "web", "static", "js", "push-client.js")

	// The status answer must carry the last delivery outcome, not just a count.
	body := regexp.MustCompile(`(?s)func handlePushStatus\(.*?\n\}`).FindString(handler)
	for _, field := range []string{"lastStatus", "lastError", "failureCount"} {
		if !strings.Contains(body, field) {
			t.Fatalf("handlePushStatus does not report %q, so a silent device is undiagnosable:\n%s", field, body)
		}
	}

	// A test send must exist, be authenticated, and be CSRF-protected like the
	// other state-changing push routes.
	testRoute := regexp.MustCompile(`(?m)^\s*http\.Handle\("/api/push/test".*$`).FindString(routes)
	if testRoute == "" {
		t.Fatal("/api/push/test is not registered")
	}
	if !strings.Contains(testRoute, "withAuth") || !strings.Contains(testRoute, "RequireCSRF") {
		t.Fatalf("/api/push/test must be authenticated and CSRF-protected: %q", testRoute)
	}

	// The endpoint is a capability URL: only the host may leave the server.
	if regexp.MustCompile(`"endpoint":\s*s\.Endpoint`).MatchString(body) {
		t.Fatal("handlePushStatus leaks the full push endpoint, which can be used to push to the device")
	}

	if !strings.Contains(menu, `id="pushTest"`) {
		t.Fatal("the notification menu has no test-send control")
	}
	if !strings.Contains(client, "/api/push/test") {
		t.Fatal("push-client.js never calls /api/push/test")
	}
}

// APNs answered 403 BadJwtToken for every send while /api/push/config happily
// advertised the public key: the JWT's sub claim was "mailto:mailto:..."
// because webpush-go prepends "mailto:" to any contact that does not start
// "https:", and the configured contact was already written that way. The push
// sender now normalises the contact, and the status endpoint reports whether
// the keypair itself is sound, so a JWT rejection has a visible cause.
func TestPushStatusReportsKeypairValidity(t *testing.T) {
	t.Chdir("../..")
	handler := readRepoFile(t, "cmd", "server", "handlePush.go")
	body := regexp.MustCompile(`(?s)func handlePushStatus\(.*?\n\}`).FindString(handler)
	if body == "" {
		t.Fatal("handlePushStatus does not exist")
	}
	if !strings.Contains(body, "push.KeypairError()") {
		t.Fatalf("handlePushStatus must report keypair validity:\n%s", body)
	}
	if !strings.Contains(body, `"keypairOk"`) {
		t.Fatalf("handlePushStatus does not emit keypairOk:\n%s", body)
	}

	main := readRepoFile(t, "cmd", "server", "main.go")
	if !strings.Contains(main, "push.KeypairError()") {
		t.Fatalf("main must warn at boot when the keypair is unusable:\n%s", main)
	}
}
