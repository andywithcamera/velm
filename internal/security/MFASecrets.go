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
)

const mfaEncryptionKeyLength = 32

type MFASecretCipher struct {
	aead cipher.AEAD
}

func LoadMFAEncryptionKeyFromEnv() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("MFA_ENCRYPTION_KEY"))
	if raw == "" {
		return nil, fmt.Errorf("MFA_ENCRYPTION_KEY is required")
	}

	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("MFA_ENCRYPTION_KEY must be base64: %w", err)
	}
	if len(key) != mfaEncryptionKeyLength {
		return nil, fmt.Errorf("MFA_ENCRYPTION_KEY must decode to exactly %d bytes", mfaEncryptionKeyLength)
	}
	return key, nil
}

func NewMFASecretCipher(key []byte) (*MFASecretCipher, error) {
	if len(key) != mfaEncryptionKeyLength {
		return nil, fmt.Errorf("MFA encryption key must be exactly %d bytes", mfaEncryptionKeyLength)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM cipher: %w", err)
	}
	return &MFASecretCipher{aead: aead}, nil
}

func NewMFASecretCipherFromEnv() (*MFASecretCipher, error) {
	key, err := LoadMFAEncryptionKeyFromEnv()
	if err != nil {
		return nil, err
	}
	return NewMFASecretCipher(key)
}

func (c *MFASecretCipher) Encrypt(plaintext string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("MFA secret cipher is not initialized")
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	payload := append(nonce, sealed...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

func (c *MFASecretCipher) Decrypt(encoded string) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("MFA secret cipher is not initialized")
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
