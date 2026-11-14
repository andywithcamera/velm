package push

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captureVapidClaims sends one notification and returns the JWT claims from the
// Authorization header the server would have received.
func captureVapidClaims(t *testing.T, contact string) map[string]any {
	t.Helper()
	resetKeysForTest(t)
	pub, priv, err := GenerateVapidKeys()
	if err != nil {
		t.Fatalf("GenerateVapidKeys: %v", err)
	}
	t.Setenv("VAPID_PUBLIC_KEY", pub)
	t.Setenv("VAPID_PRIVATE_KEY", priv)
	t.Setenv("VAPID_CONTACT", contact)
	resetKeysForTest(t)

	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	res := Send(context.Background(), []Subscription{{
		Endpoint: srv.URL + "/push/abc",
		P256DH:   testP256DH(t),
		Auth:     testAuth(t),
	}}, "t", "b", "/")
	if res.Sent != 1 {
		t.Fatalf("Sent = %d, want 1 (Results = %+v)", res.Sent, res.Results)
	}
	if !strings.HasPrefix(auth, "vapid t=") {
		t.Fatalf("Authorization = %q, want a vapid header", auth)
	}
	jwt := strings.SplitN(strings.TrimPrefix(auth, "vapid t="), ",", 2)[0]
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT has %d segments, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	claims := map[string]any{}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return claims
}

// APNs rejects a JWT whose sub is "mailto:mailto:user@example.com" with
// 403 BadJwtToken. webpush-go prepends "mailto:" to anything not starting
// "https:", so a correctly-written contact must not be prefixed again.
func TestVapidSubjectIsNotDoublePrefixed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		contact string
		want    string
	}{
		{"mailto prefix", "mailto:andy@example.com", "mailto:andy@example.com"},
		{"mailto prefix with spaces", "  mailto:andy@example.com  ", "mailto:andy@example.com"},
		{"bare email", "andy@example.com", "mailto:andy@example.com"},
		{"https url", "https://example.com/contact", "https://example.com/contact"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := captureVapidClaims(t, tc.contact)
			if got := claims["sub"]; got != tc.want {
				t.Fatalf("sub = %q, want %q", got, tc.want)
			}
			if aud, _ := claims["aud"].(string); aud == "" {
				t.Fatalf("aud is empty: %+v", claims)
			}
		})
	}
}

func TestKeypairErrorDetectsMismatch(t *testing.T) {
	resetKeysForTest(t)
	pub, priv, err := GenerateVapidKeys()
	if err != nil {
		t.Fatalf("GenerateVapidKeys: %v", err)
	}
	otherPub, _, err := GenerateVapidKeys()
	if err != nil {
		t.Fatalf("GenerateVapidKeys: %v", err)
	}

	t.Run("matching", func(t *testing.T) {
		t.Setenv("VAPID_PUBLIC_KEY", pub)
		t.Setenv("VAPID_PRIVATE_KEY", priv)
		resetKeysForTest(t)
		if err := KeypairError(); err != nil {
			t.Fatalf("KeypairError = %v, want nil", err)
		}
	})

	t.Run("mismatched", func(t *testing.T) {
		t.Setenv("VAPID_PUBLIC_KEY", otherPub)
		t.Setenv("VAPID_PRIVATE_KEY", priv)
		resetKeysForTest(t)
		if err := KeypairError(); err == nil {
			t.Fatal("KeypairError = nil, want a mismatch error")
		}
	})
}

// Subject must report exactly what goes on the wire, so the status endpoint
// shows the same string the push service validates.
func TestSubjectMatchesTransmittedClaim(t *testing.T) {
	resetKeysForTest(t)
	t.Setenv("VAPID_PUBLIC_KEY", "")
	t.Setenv("VAPID_PRIVATE_KEY", "")
	t.Setenv("VAPID_CONTACT", "mailto:andy@example.com")
	resetKeysForTest(t)
	if got := Subject(); got != "mailto:andy@example.com" {
		t.Fatalf("Subject() = %q, want %q", got, "mailto:andy@example.com")
	}
	resetKeysForTest(t)
	t.Setenv("VAPID_PUBLIC_KEY", "")
	t.Setenv("VAPID_PRIVATE_KEY", "")
	t.Setenv("VAPID_CONTACT", "andy@example.com")
	resetKeysForTest(t)
	if got := Subject(); got != "mailto:andy@example.com" {
		t.Fatalf("Subject() = %q, want %q", got, "mailto:andy@example.com")
	}
}
