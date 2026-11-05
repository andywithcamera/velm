package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const secretEncryptionKeyLength = 32

// secretEncryptionKeyEnv is the general-purpose name for the key. The legacy
// MFA_ENCRYPTION_KEY is still read so existing deployments need no change.
const (
	secretEncryptionKeyEnv    = "SECRET_ENCRYPTION_KEY"
	legacyMFAEncryptionKeyEnv = "MFA_ENCRYPTION_KEY"
)

// SecretCipher encrypts short secret strings (MFA seeds, AI provider keys,
// webhook signing secrets) with AES-256-GCM. Each Encrypt call uses a fresh
// random nonce, which is prepended to the ciphertext and base64 encoded.
type SecretCipher struct {
	aead cipher.AEAD
}

// LoadSecretEncryptionKeyFromEnv reads a base64-encoded 32-byte key.
// SECRET_ENCRYPTION_KEY wins; MFA_ENCRYPTION_KEY is the backwards-compatible
// fallback for deployments configured before this was generalised.
func LoadSecretEncryptionKeyFromEnv() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(secretEncryptionKeyEnv))
	name := secretEncryptionKeyEnv
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv(legacyMFAEncryptionKeyEnv))
		name = legacyMFAEncryptionKeyEnv
	}
	if raw == "" {
		return nil, fmt.Errorf("SECRET_ENCRYPTION_KEY (or legacy MFA_ENCRYPTION_KEY) is required")
	}

	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be base64: %w", name, err)
	}
	if len(key) != secretEncryptionKeyLength {
		return nil, fmt.Errorf("%s must decode to exactly %d bytes", name, secretEncryptionKeyLength)
	}
	return key, nil
}

func NewSecretCipher(key []byte) (*SecretCipher, error) {
	if len(key) != secretEncryptionKeyLength {
		return nil, fmt.Errorf("secret encryption key must be exactly %d bytes", secretEncryptionKeyLength)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM cipher: %w", err)
	}
	return &SecretCipher{aead: aead}, nil
}

func NewSecretCipherFromEnv() (*SecretCipher, error) {
	key, err := LoadSecretEncryptionKeyFromEnv()
	if err != nil {
		return nil, err
	}
	return NewSecretCipher(key)
}

// sharedSecretCipher caches the env-loaded cipher so every consumer in the
// process encrypts with one key and the environment is only parsed once.
// The error is cached too: a misconfigured key stays reported, not retried.
var sharedSecretCipher struct {
	once   sync.Once
	cipher *SecretCipher
	err    error
}

// SharedSecretCipher returns the process-wide cipher, loading it on first use.
func SharedSecretCipher() (*SecretCipher, error) {
	sharedSecretCipher.once.Do(func() {
		sharedSecretCipher.cipher, sharedSecretCipher.err = NewSecretCipherFromEnv()
	})
	return sharedSecretCipher.cipher, sharedSecretCipher.err
}

func (c *SecretCipher) Encrypt(plaintext string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("secret cipher is not initialized")
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, sealed...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

func (c *SecretCipher) Decrypt(encoded string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("secret cipher is not initialized")
	}

	payload, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", fmt.Errorf("decode secret payload: %w", err)
	}

	nonceSize := c.aead.NonceSize()
	if len(payload) < nonceSize {
		return "", fmt.Errorf("secret payload is invalid")
	}

	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt secret payload: %w", err)
	}
	return string(plaintext), nil
}
