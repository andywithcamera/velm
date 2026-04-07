package security

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestMFASecretCipherEncryptDecryptRoundTrip(t *testing.T) {
	key := []byte("12345678901234567890123456789012")
	cipher, err := NewMFASecretCipher(key)
	if err != nil {
		t.Fatalf("NewMFASecretCipher returned error: %v", err)
	}

	encrypted, err := cipher.Encrypt("totp-secret-value")
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	if encrypted == "" {
		t.Fatal("Encrypt returned an empty payload")
	}

	decrypted, err := cipher.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}
	if decrypted != "totp-secret-value" {
		t.Fatalf("Decrypt = %q, want %q", decrypted, "totp-secret-value")
	}
}

func TestLoadMFAEncryptionKeyFromEnvValidatesConfig(t *testing.T) {
	t.Setenv("MFA_ENCRYPTION_KEY", "")
	if _, err := LoadMFAEncryptionKeyFromEnv(); err == nil {
		t.Fatal("expected missing MFA_ENCRYPTION_KEY to fail")
	}

	t.Setenv("MFA_ENCRYPTION_KEY", "not-base64")
	if _, err := LoadMFAEncryptionKeyFromEnv(); err == nil {
		t.Fatal("expected invalid base64 MFA_ENCRYPTION_KEY to fail")
	}

	t.Setenv("MFA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012")))
	key, err := LoadMFAEncryptionKeyFromEnv()
	if err != nil {
		t.Fatalf("LoadMFAEncryptionKeyFromEnv returned error: %v", err)
	}
	if len(key) != mfaEncryptionKeyLength {
		t.Fatalf("key length = %d, want %d", len(key), mfaEncryptionKeyLength)
	}
}

func TestMFASecretCipherRejectsInvalidPayload(t *testing.T) {
	key := []byte("12345678901234567890123456789012")
	cipher, err := NewMFASecretCipher(key)
	if err != nil {
		t.Fatalf("NewMFASecretCipher returned error: %v", err)
	}

	_, err = cipher.Decrypt(base64.StdEncoding.EncodeToString([]byte("short")))
	if err == nil {
		t.Fatal("expected short payload to fail")
	}
	if !strings.Contains(err.Error(), "secret payload is invalid") {
		t.Fatalf("unexpected error: %v", err)
	}
}
