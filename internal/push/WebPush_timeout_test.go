package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A push service that never answers must not hold the caller. webpush-go uses
// http.DefaultClient (no timeout) when given a bare call, so a stalled endpoint
// hung the request that sent it — including the synchronous "Send test
// notification" form submission, which then appeared to do nothing at all.
func TestSendIsBoundedByTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never respond
	}))
	defer func() { close(block); srv.Close() }()

	// A real subscription key so the send reaches the network instead of
	// failing during payload encryption.
	curve := ecdh.P256()
	clientKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating client key: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("generating auth secret: %v", err)
	}

	resetKeysForTest(t)
	pub, priv, err := GenerateVapidKeys()
	if err != nil {
		t.Fatalf("GenerateVapidKeys: %v", err)
	}
	t.Setenv("VAPID_PUBLIC_KEY", pub)
	t.Setenv("VAPID_PRIVATE_KEY", priv)
	t.Setenv("VAPID_CONTACT", "test@example.com")
	resetKeysForTest(t)

	old := sendTimeout
	sendTimeout = 150 * time.Millisecond
	defer func() { sendTimeout = old }()

	start := time.Now()
	res := Send(context.Background(), []Subscription{{
		Endpoint: srv.URL + "/push",
		P256DH:   base64.RawURLEncoding.EncodeToString(clientKey.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}}, "title", "body", "/")
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("Send blocked for %v against an unresponsive push service", elapsed)
	}
	if len(res.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(res.Results))
	}
	if res.Results[0].OK() {
		t.Fatal("a timed-out send was recorded as delivered")
	}
}
