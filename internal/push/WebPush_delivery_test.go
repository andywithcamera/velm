package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// A non-2xx response from a push service used to be counted as a successful
// send. webpush-go returns the *http.Response unchanged and Go's HTTP client
// reports no error for an HTTP error status, so Send only ever looked at err.
// That made "subscribed but silent" invisible: the subscription row existed,
// the inbox filled in-app, the device got nothing, and nothing was logged
// (#344).
func TestClassifyTreatsHTTPStatusAsOutcome(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantOK  bool
		wantErr string
	}{
		{"accepted", http.StatusCreated, "", true, ""},
		{"no content", http.StatusNoContent, "", true, ""},
		{"vapid key mismatch", http.StatusForbidden, "VapidPkHashMismatch", false, "VapidPkHashMismatch"},
		{"bad request", http.StatusBadRequest, "invalid key", false, "invalid key"},
		{"gone", http.StatusGone, "Gone", false, "Gone"},
		{"server error", http.StatusInternalServerError, "boom", false, "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tc.status,
				Body:       http.NoBody,
			}
			if tc.body != "" {
				resp.Body = readCloser(tc.body)
			}
			d := classify("https://push.example/abc", resp, nil)
			if d.Status != tc.status {
				t.Fatalf("Status = %d, want %d", d.Status, tc.status)
			}
			if d.OK() != tc.wantOK {
				t.Fatalf("OK() = %v, want %v (status %d)", d.OK(), tc.wantOK, tc.status)
			}
			if tc.wantErr != "" && d.Err != tc.wantErr {
				t.Fatalf("Err = %q, want %q", d.Err, tc.wantErr)
			}
		})
	}
}

func TestClassifyWithoutResponseCarriesTransportError(t *testing.T) {
	d := classify("https://push.example/abc", nil, context.DeadlineExceeded)
	if d.OK() {
		t.Fatal("a request with no response must not count as delivered")
	}
	if d.Status != 0 {
		t.Fatalf("Status = %d, want 0 for no response", d.Status)
	}
	if d.Err == "" {
		t.Fatal("expected the transport error to be kept")
	}
}

// Send must report a rejected delivery as failed, not sent, and must flag a
// 410 endpoint as stale so it is cleaned up.
func TestSendCountsRejectedDeliveryAsFailure(t *testing.T) {
	resetKeysForTest(t)
	pub, priv, err := GenerateVapidKeys()
	if err != nil {
		t.Fatalf("GenerateVapidKeys: %v", err)
	}
	t.Setenv("VAPID_PUBLIC_KEY", pub)
	t.Setenv("VAPID_PRIVATE_KEY", priv)
	t.Setenv("VAPID_CONTACT", "mailto:test@example.com")
	resetKeysForTest(t)

	for _, tc := range []struct {
		name       string
		status     int
		wantSent   int
		wantFailed int
		wantGone   int
	}{
		{"rejected", http.StatusForbidden, 0, 1, 0},
		{"gone", http.StatusGone, 0, 0, 1},
		{"accepted", http.StatusCreated, 1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			sub := Subscription{
				Endpoint: srv.URL + "/push/abc",
				P256DH:   testP256DH(t),
				Auth:     testAuth(t),
			}
			res := Send(context.Background(), []Subscription{sub}, "t", "b", "/")

			if res.Sent != tc.wantSent {
				t.Fatalf("Sent = %d, want %d", res.Sent, tc.wantSent)
			}
			if res.Failed != tc.wantFailed {
				t.Fatalf("Failed = %d, want %d", res.Failed, tc.wantFailed)
			}
			if len(res.Stale) != tc.wantGone {
				t.Fatalf("Stale = %v, want %d entries", res.Stale, tc.wantGone)
			}
			if len(res.Results) != 1 || res.Results[0].Status != tc.status {
				t.Fatalf("Results = %+v, want one entry with status %d", res.Results, tc.status)
			}
		})
	}
}

func resetKeysForTest(t *testing.T) {
	t.Helper()
	keysOnce = sync.Once{}
	t.Cleanup(func() { keysOnce = sync.Once{} })
}

func testP256DH(t *testing.T) string {
	t.Helper()
	curve := ecdh.P256()
	priv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
}

func testAuth(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("random: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func readCloser(s string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(s))
}
