package push

import "testing"

func TestGenerateVapidKeysRoundTrip(t *testing.T) {
	pub, priv, err := GenerateVapidKeys()
	if err != nil {
		t.Fatalf("GenerateVapidKeys() error = %v", err)
	}
	if len(pub) != 87 || len(priv) != 43 {
		t.Fatalf("expected base64url keys (87 pub uncompressed, 43 priv), got pub=%d priv=%d", len(pub), len(priv))
	}
}

func TestEnabledAndPublicKeyWithoutConfig(t *testing.T) {
	t.Setenv("VAPID_PUBLIC_KEY", "")
	t.Setenv("VAPID_PRIVATE_KEY", "")
	if Enabled() {
		t.Fatal("expected Enabled() to be false without keys")
	}
	if PublicKey() != "" {
		t.Fatal("expected empty public key without config")
	}
}
