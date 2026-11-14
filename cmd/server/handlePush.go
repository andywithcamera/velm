package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"

	"velm/internal/auth"
	"velm/internal/db"
	"velm/internal/push"
)

// GET /api/push/config — public VAPID key so the client can subscribe.
func handlePushConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"enabled":   push.Enabled(),
		"publicKey": push.PublicKey(),
	}); err != nil {
		http.Error(w, "Encode failed", http.StatusInternalServerError)
	}
}

// POST /api/push/subscribe — store the browser's push subscription.
func handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Endpoint) == "" ||
		strings.TrimSpace(in.Keys.P256dh) == "" || strings.TrimSpace(in.Keys.Auth) == "" {
		http.Error(w, "Invalid subscription", http.StatusBadRequest)
		return
	}
	if err := db.UpsertPushSubscription(r.Context(), db.PushSubscriptionUpsert{
		UserID:    userID,
		Endpoint:  strings.TrimSpace(in.Endpoint),
		P256DH:    strings.TrimSpace(in.Keys.P256dh),
		Auth:      strings.TrimSpace(in.Keys.Auth),
		UserAgent: r.UserAgent(),
	}); err != nil {
		http.Error(w, "Failed to store subscription", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handlePushStatus — the current user's push subscription state.
//
// The client could previously only guess whether it was subscribed, from
// localStorage and the browser's own subscription, so a subscription that was
// never stored (or was pruned as stale) still rendered "Disable push" and the
// user had no way to tell opt-in had not taken effect (#344).
//
// It also reports the outcome of the last send to each endpoint: before that,
// "subscribed: true" with no phone notification was unanswerable from
// outside, because a rejected send looked identical to a delivered one.
func handlePushStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	states, err := db.ListPushSubscriptionStates(r.Context(), userID)
	if err != nil {
		http.Error(w, "Failed to read subscriptions", http.StatusInternalServerError)
		return
	}
	subs := make([]map[string]any, 0, len(states))
	for _, s := range states {
		entry := map[string]any{
			// Only the host: the full endpoint is a capability URL that
			// lets anyone holding it push to this device.
			"host":         endpointHost(s.Endpoint),
			"lastSuccess":  s.LastSuccess,
			"lastError":    s.LastError,
			"failureCount": s.FailureCount,
		}
		if s.LastStatus != nil {
			entry["lastStatus"] = *s.LastStatus
		}
		if s.LastFailure != nil {
			entry["lastFailure"] = *s.LastFailure
		}
		subs = append(subs, entry)
	}
	w.Header().Set("Content-Type", "application/json")
	body := map[string]any{
		"enabled":       push.Enabled(),
		"subscribed":    len(states) > 0,
		"count":         len(states),
		"subscriptions": subs,
		"subject":       push.Subject(),
	}
	// A keypair mismatch is otherwise undetectable from outside: the public
	// key is advertised and accepted, browsers subscribe against it, and
	// every send fails with a JWT error from the push service that points at
	// the token rather than the configuration. Report it here so a 403
	// BadJwtToken has a visible cause.
	if err := push.KeypairError(); err != nil {
		body["keypairOk"] = false
		body["keypairError"] = err.Error()
	} else {
		body["keypairOk"] = true
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, "Encode failed", http.StatusInternalServerError)
	}
}

// POST /api/push/test — send a test push to the caller's own endpoints and
// return the push service's answer synchronously.
//
// Diagnosing "subscribed but silent" otherwise needs a task assignment and
// then Railway logs; this puts the status code in the app.
func handlePushTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	subs, err := db.ListPushSubscriptions(r.Context(), userID)
	if err != nil {
		http.Error(w, "Failed to read subscriptions", http.StatusInternalServerError)
		return
	}
	if !push.Enabled() {
		http.Error(w, "Push is not configured on this instance", http.StatusServiceUnavailable)
		return
	}
	if len(subs) == 0 {
		http.Error(w, "No push subscription stored for this user", http.StatusBadRequest)
		return
	}
	res := push.Send(r.Context(), subs, "Velm push test",
		"If you can read this, push delivery works.", "/")
	if err := db.RecordPushDeliveries(r.Context(), res.Results); err != nil {
		log.Printf("push: recording test delivery failed: %v", err)
	}
	out := make([]map[string]any, 0, len(res.Results))
	for _, d := range res.Results {
		out = append(out, map[string]any{
			"host":   endpointHost(d.Endpoint),
			"status": d.Status,
			"error":  d.Err,
			"ok":     d.OK(),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"sent":    res.Sent,
		"failed":  res.Failed,
		"removed": res.Removed,
		"results": out,
	}); err != nil {
		http.Error(w, "Encode failed", http.StatusInternalServerError)
	}
}

// endpointHost reduces a push endpoint to its host, for display.
func endpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "(unparseable)"
	}
	return u.Host
}

// POST /api/push/unsubscribe — remove an endpoint for the current user.
func handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid method", http.StatusMethodNotAllowed)
		return
	}
	userID := strings.TrimSpace(auth.UserIDFromRequest(r))
	if userID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if err := db.DeletePushSubscription(r.Context(), userID, strings.TrimSpace(in.Endpoint)); err != nil {
		http.Error(w, "Failed to remove subscription", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
