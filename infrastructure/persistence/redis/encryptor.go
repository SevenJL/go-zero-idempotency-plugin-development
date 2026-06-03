// Package redis provides AES-GCM encryption for idempotency record bodies
// stored in Redis. Response bodies containing sensitive data (PII, financial
// information) can be transparently encrypted at rest.
//
// Usage:
//
//	key := make([]byte, 32) // 256-bit key from a secret manager
//	encryptor := redis.NewAESEncryptor(key)
//	repo := redis.NewIdempotencyRecordRepository(rds,
//	    redis.WithBodyEncryptor(encryptor),
//	)
//
// The encryptor is applied before base64 encoding during storage and after
// base64 decoding during retrieval. When no encryptor is configured, bodies
// are stored as plain base64 (the default).
package redis

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// BodyEncryptor encrypts and decrypts response bodies for storage in Redis.
// Implementations must be concurrency-safe.
type BodyEncryptor interface {
	// Encrypt encrypts plaintext and returns a base64-encoded ciphertext.
	Encrypt(plaintext []byte) (string, error)

	// Decrypt decodes the base64 ciphertext and returns the original plaintext.
	Decrypt(ciphertext string) ([]byte, error)
}

// AESEncryptor implements BodyEncryptor using AES-256-GCM with a random
// 12-byte nonce prepended to the ciphertext before base64 encoding.
// The nonce ensures that encrypting the same plaintext twice produces
// different ciphertexts.
type AESEncryptor struct {
	gcm cipher.AEAD
}

// NewAESEncryptor creates an AES-256-GCM encryptor. The key must be exactly
// 32 bytes (AES-256). Keys shorter or longer are rejected.
func NewAESEncryptor(key []byte) (*AESEncryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("aes encryptor: key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes encryptor: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("aes encryptor: %w", err)
	}

	return &AESEncryptor{gcm: gcm}, nil
}

// MustAESEncryptor is like NewAESEncryptor but panics on error.
// Suitable for init-time setup with a known-good key.
func MustAESEncryptor(key []byte) *AESEncryptor {
	e, err := NewAESEncryptor(key)
	if err != nil {
		panic(err)
	}
	return e
}

// Encrypt encrypts plaintext and returns a base64-encoded string.
// The format is: base64(nonce || ciphertext || tag).
// An empty plaintext returns an empty string.
func (e *AESEncryptor) Encrypt(plaintext []byte) (string, error) {
	if len(plaintext) == 0 {
		return "", nil
	}

	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("aes encryptor: generate nonce: %w", err)
	}

	// Seal appends the encrypted data to nonce: nonce || ciphertext || tag
	ciphertext := e.gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decodes the base64 ciphertext and returns the original plaintext.
// An empty string returns nil.
func (e *AESEncryptor) Decrypt(ciphertext string) ([]byte, error) {
	if ciphertext == "" {
		return nil, nil
	}

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("aes encryptor: decode: %w", err)
	}

	nonceSize := e.gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("aes encryptor: ciphertext too short")
	}

	nonce, cipherdata := data[:nonceSize], data[nonceSize:]
	plaintext, err := e.gcm.Open(nil, nonce, cipherdata, nil)
	if err != nil {
		return nil, fmt.Errorf("aes encryptor: decrypt: %w", err)
	}

	return plaintext, nil
}

var _ BodyEncryptor = (*AESEncryptor)(nil)
