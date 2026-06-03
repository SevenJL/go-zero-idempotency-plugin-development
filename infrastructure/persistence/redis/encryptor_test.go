package redis

import (
	"crypto/rand"
	"testing"
)

func TestAESEncryptorRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	enc, err := NewAESEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		plaintext []byte
	}{
		{"empty", nil},
		{"empty bytes", []byte{}},
		{"short string", []byte("hello")},
		{"json body", []byte(`{"status":"ok","count":42}`)},
		{"large body", make([]byte, 1024*1024)}, // 1MB
		{"binary data", []byte{0x00, 0xFF, 0x80, 0x7F}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ciphertext, err := enc.Encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}

			plaintext, err := enc.Decrypt(ciphertext)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}

			if string(plaintext) != string(tt.plaintext) {
				t.Fatalf("roundtrip mismatch: got %q, want %q", plaintext, tt.plaintext)
			}
		})
	}
}

func TestAESEncryptorDeterministicOutput(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	enc, err := NewAESEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("test message")

	// Same plaintext encrypted twice should produce different ciphertexts
	// due to random nonce.
	c1, _ := enc.Encrypt(plaintext)
	c2, _ := enc.Encrypt(plaintext)

	if c1 == c2 {
		t.Fatal("encrypted outputs should differ due to random nonce")
	}
}

func TestAESEncryptorInvalidKey(t *testing.T) {
	// Key too short.
	_, err := NewAESEncryptor(make([]byte, 16))
	if err == nil {
		t.Fatal("expected error for 16-byte key (AES-128)")
	}

	// Key too long.
	_, err = NewAESEncryptor(make([]byte, 64))
	if err == nil {
		t.Fatal("expected error for 64-byte key")
	}

	// Exactly 32 bytes should work.
	_, err = NewAESEncryptor(make([]byte, 32))
	if err != nil {
		t.Fatalf("32-byte key should be valid: %v", err)
	}
}

func TestAESEncryptorDecryptCorruptData(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	enc, _ := NewAESEncryptor(key)

	// Corrupt base64.
	_, err := enc.Decrypt("!!!not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}

	// Valid base64 but too short for nonce.
	_, err = enc.Decrypt("YWJj") // "abc" in base64
	if err == nil {
		t.Fatal("expected error for short ciphertext")
	}
}

func TestMustAESEncryptor(t *testing.T) {
	key := make([]byte, 32)

	// Must not panic with valid key.
	enc := MustAESEncryptor(key)
	if enc == nil {
		t.Fatal("MustAESEncryptor returned nil")
	}

	// Must panic with invalid key.
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustAESEncryptor did not panic with invalid key")
		}
	}()
	MustAESEncryptor(make([]byte, 16))
}
