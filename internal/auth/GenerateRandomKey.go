package auth

import (
	"crypto/rand"
	"log"
)

func GenerateRandomKey(length int) []byte {
	key := make([]byte, length)
	_, err := rand.Read(key)
	if err != nil {
		log.Fatalf("Failed to generate random key: %v", err)
	}
	return key
}
