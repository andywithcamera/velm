package security

import (
	"encoding/base64"
	"strings"
	"sync"
	"testing"
)

const testSecretKey = "12345678901234567890123456789012"

func TestSecretCipherEncryptDecryptRoundTrip(t *testing.T) {
	cipher, err := NewSecretCipher([]byte(testSecretKey))
	if err != nil {
		t.Fatalf("NewSecretCipher returned error: %v", err)
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

func TestNewSecretCipherRejectsWrongKeyLength(t *testing.T) {
	if _, err := NewSecretCipher([]byte("too-short")); err == nil {
		t.Fatal("expected a short key to be rejected")
	}
}

func TestLoadSecretEncryptionKeyFromEnvValidatesConfig(t *testing.T) {
	t.Setenv("MFA_ENCRYPTION_KEY", "")
	t.Setenv("SECRET_ENCRYPTION_KEY", "")
	if _, err := LoadSecretEncryptionKeyFromEnv(); err == nil {
		t.Fatal("expected a missing key to fail")
	}

	t.Setenv("SECRET_ENCRYPTION_KEY", "not-base64")
	if _, err := LoadSecretEncryptionKeyFromEnv(); err == nil {
		t.Fatal("expected invalid base64 to fail")
	}

	t.Setenv("SECRET_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	if _, err := LoadSecretEncryptionKeyFromEnv(); err == nil {
		t.Fatal("expected a short decoded key to fail")
	}

	t.Setenv("SECRET_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(testSecretKey)))
	key, err := LoadSecretEncryptionKeyFromEnv()
	if err != nil {
		t.Fatalf("LoadSecretEncryptionKeyFromEnv returned error: %v", err)
	}
	if len(key) != secretEncryptionKeyLength {
		t.Fatalf("key length = %d, want %d", len(key), secretEncryptionKeyLength)
	}
}

// Existing deployments set MFA_ENCRYPTION_KEY; the generalised loader still
// reads it so no environment has to change for this refactor.
func TestLoadSecretEncryptionKeyFallsBackToLegacyEnv(t *testing.T) {
	t.Setenv("SECRET_ENCRYPTION_KEY", "")
	t.Setenv("MFA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(testSecretKey)))

	key, err := LoadSecretEncryptionKeyFromEnv()
	if err != nil {
		t.Fatalf("LoadSecretEncryptionKeyFromEnv returned error: %v", err)
	}
	if string(key) != testSecretKey {
		t.Fatal("loader did not use the legacy MFA_ENCRYPTION_KEY")
	}
}

func TestLoadSecretEncryptionKeyPrefersGenericEnv(t *testing.T) {
	t.Setenv("SECRET_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(testSecretKey)))
	t.Setenv("MFA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("abcdefghijabcdefghijabcdefghij12")))

	key, err := LoadSecretEncryptionKeyFromEnv()
	if err != nil {
		t.Fatalf("LoadSecretEncryptionKeyFromEnv returned error: %v", err)
	}
	if string(key) != testSecretKey {
		t.Fatal("SECRET_ENCRYPTION_KEY should take precedence over the legacy variable")
	}
}

func TestSecretCipherRejectsInvalidPayload(t *testing.T) {
	cipher, err := NewSecretCipher([]byte(testSecretKey))
	if err != nil {
		t.Fatalf("NewSecretCipher returned error: %v", err)
	}

	_, err = cipher.Decrypt(base64.StdEncoding.EncodeToString([]byte("short")))
	if err == nil {
		t.Fatal("expected short payload to fail")
	}
	if !strings.Contains(err.Error(), "secret payload is invalid") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSharedSecretCipherLoadsOnce(t *testing.T) {
	resetSharedSecretCipherForTest()
	t.Cleanup(resetSharedSecretCipherForTest)

	legacy := base64.StdEncoding.EncodeToString([]byte(testSecretKey))
	t.Setenv("SECRET_ENCRYPTION_KEY", "")
	t.Setenv("MFA_ENCRYPTION_KEY", legacy)

	first, err := SharedSecretCipher()
	if err != nil {
		t.Fatalf("SharedSecretCipher returned error: %v", err)
	}

	// A later environment change must not produce a second, different cipher:
	// the point of the helper is one load per process.
	t.Setenv("SECRET_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte("abcdefghijabcdefghijabcdefghij12")))
	second, err := SharedSecretCipher()
	if err != nil {
		t.Fatalf("second SharedSecretCipher returned error: %v", err)
	}
	if first != second {
		t.Fatal("SharedSecretCipher built a new cipher on the second call")
	}
}

func TestSharedSecretCipherIsConcurrencySafe(t *testing.T) {
	resetSharedSecretCipherForTest()
	t.Cleanup(resetSharedSecretCipherForTest)
	t.Setenv("MFA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(testSecretKey)))

	var wg sync.WaitGroup
	results := make([]*SecretCipher, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = SharedSecretCipher()
		}(i)
	}
	wg.Wait()
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Fatal("concurrent callers received different ciphers")
		}
	}
}

// resetSharedSecretCipherForTest clears the once so tests can control the
// environment the cipher is loaded from.
func resetSharedSecretCipherForTest() {
	sharedSecretCipher = struct {
		once   sync.Once
		cipher *SecretCipher
		err    error
	}{}
}
