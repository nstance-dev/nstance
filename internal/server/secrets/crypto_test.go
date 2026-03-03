// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/nstance-dev/nstance/internal/server/keys"
)

func TestEncryptDecrypt(t *testing.T) {
	// Generate test key
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	testData := []byte("Hello, World! This is test data for encryption.")

	// Test encryption
	encryptedData, err := EncryptData(testData, key)
	if err != nil {
		t.Fatalf("Failed to encrypt data: %v", err)
	}

	// Verify encrypted data is different and longer (due to nonce)
	if bytes.Equal(testData, encryptedData) {
		t.Error("Encrypted data should be different from plaintext")
	}

	if len(encryptedData) <= len(testData) {
		t.Error("Encrypted data should be longer than plaintext (includes nonce)")
	}

	// Test decryption
	decryptedData, err := DecryptData(encryptedData, key)
	if err != nil {
		t.Fatalf("Failed to decrypt data: %v", err)
	}

	// Verify decrypted data matches original
	if !bytes.Equal(testData, decryptedData) {
		t.Error("Decrypted data doesn't match original")
	}
}

func TestDecryptWithWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)

	_, _ = rand.Read(key1)
	_, _ = rand.Read(key2)

	testData := []byte("Test data")

	// Encrypt with key1
	encryptedData, err := EncryptData(testData, key1)
	if err != nil {
		t.Fatalf("Failed to encrypt data: %v", err)
	}

	// Try to decrypt with key2 (should fail)
	_, err = DecryptData(encryptedData, key2)
	if err == nil {
		t.Error("Expected decryption to fail with wrong key")
	}
}

func TestDecryptDataWithMultipleKeys(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	key3 := make([]byte, 32)

	_, _ = rand.Read(key1)
	_, _ = rand.Read(key2)
	_, _ = rand.Read(key3)

	testData := []byte("Multi-key test data")

	// Encrypt with key2
	encryptedData, err := EncryptData(testData, key2)
	if err != nil {
		t.Fatalf("Failed to encrypt data: %v", err)
	}

	// Try to decrypt with multiple keys (key2 is in the middle)
	keys := [][]byte{key1, key2, key3}
	decryptedData, err := DecryptDataWithMultipleKeys(encryptedData, keys)
	if err != nil {
		t.Fatalf("Failed to decrypt with multiple keys: %v", err)
	}

	if !bytes.Equal(testData, decryptedData) {
		t.Error("Decrypted data doesn't match original")
	}

	// Try with keys that don't include the correct one
	wrongKeys := [][]byte{key1, key3}
	_, err = DecryptDataWithMultipleKeys(encryptedData, wrongKeys)
	if err == nil {
		t.Error("Expected decryption to fail when correct key is not included")
	}
}

func TestGenerateEd25519KeyPair(t *testing.T) {
	// Test Ed25519 key generation
	key, err := keys.GenerateEd25519Key()
	if err != nil {
		t.Fatalf("Failed to generate Ed25519 key: %v", err)
	}

	if key == nil {
		t.Fatal("Generated key is nil")
	}

	// Ed25519 private keys are always 64 bytes
	if len(key) != 64 {
		t.Errorf("Expected Ed25519 key size 64 bytes, got %d", len(key))
	}
}

func TestCalculateChecksum(t *testing.T) {
	testData := []byte("Test data for checksum")

	checksum1 := CalculateChecksum(testData)
	checksum2 := CalculateChecksum(testData)

	// Same data should produce same checksum
	if checksum1 != checksum2 {
		t.Error("Same data should produce same checksum")
	}

	// Different data should produce different checksum
	differentData := []byte("Different test data")
	checksum3 := CalculateChecksum(differentData)

	if checksum1 == checksum3 {
		t.Error("Different data should produce different checksum")
	}

	// Checksum should be 64 characters (hex of 32-byte hash)
	if len(checksum1) != 64 {
		t.Errorf("Expected checksum length 64, got %d", len(checksum1))
	}
}

func TestGenerateKeyID(t *testing.T) {
	id1, err := GenerateKeyID()
	if err != nil {
		t.Fatalf("Failed to generate key ID: %v", err)
	}

	id2, err := GenerateKeyID()
	if err != nil {
		t.Fatalf("Failed to generate second key ID: %v", err)
	}

	// IDs should be different
	if id1 == id2 {
		t.Error("Generated key IDs should be different")
	}

	// IDs should be 32 characters (hex of 16 bytes)
	if len(id1) != 32 {
		t.Errorf("Expected key ID length 32, got %d", len(id1))
	}

	if len(id2) != 32 {
		t.Errorf("Expected key ID length 32, got %d", len(id2))
	}
}

func TestEncryptDecryptWithAlgorithm(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	testData := []byte("Algorithm test data")

	// Test with explicit algorithm
	encrypted, err := EncryptDataWithAlgorithm(testData, key, "AES-256-GCM")
	if err != nil {
		t.Fatalf("Failed to encrypt with algorithm: %v", err)
	}

	decrypted, err := DecryptDataWithAlgorithm(encrypted, key, "AES-256-GCM")
	if err != nil {
		t.Fatalf("Failed to decrypt with algorithm: %v", err)
	}

	if !bytes.Equal(testData, decrypted) {
		t.Error("Data doesn't match after algorithm encrypt/decrypt")
	}

	// Test with empty algorithm (should default to AES-256-GCM)
	encrypted2, err := EncryptDataWithAlgorithm(testData, key, "")
	if err != nil {
		t.Fatalf("Failed to encrypt with empty algorithm: %v", err)
	}

	decrypted2, err := DecryptDataWithAlgorithm(encrypted2, key, "")
	if err != nil {
		t.Fatalf("Failed to decrypt with empty algorithm: %v", err)
	}

	if !bytes.Equal(testData, decrypted2) {
		t.Error("Data doesn't match after default algorithm encrypt/decrypt")
	}

	// Test with unsupported algorithm
	_, err = EncryptDataWithAlgorithm(testData, key, "UNSUPPORTED")
	if err == nil {
		t.Error("Expected error for unsupported encryption algorithm")
	}

	_, err = DecryptDataWithAlgorithm(encrypted, key, "UNSUPPORTED")
	if err == nil {
		t.Error("Expected error for unsupported decryption algorithm")
	}
}

func TestEncryptDecryptEdgeCases(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)

	// Test empty data
	emptyData := []byte{}
	encrypted, err := EncryptData(emptyData, key)
	if err != nil {
		t.Fatalf("Failed to encrypt empty data: %v", err)
	}

	decrypted, err := DecryptData(encrypted, key)
	if err != nil {
		t.Fatalf("Failed to decrypt empty data: %v", err)
	}

	if !bytes.Equal(emptyData, decrypted) {
		t.Error("Empty data doesn't match after encrypt/decrypt")
	}

	// Test data that's too short to decrypt
	shortData := []byte("short")
	_, err = DecryptData(shortData, key)
	if err == nil {
		t.Error("Expected error when decrypting data that's too short")
	}

	// Test with nil key
	_, err = EncryptData([]byte("test"), nil)
	if err == nil {
		t.Error("Expected error with nil key")
	}

	_, err = DecryptData(encrypted, nil)
	if err == nil {
		t.Error("Expected error with nil key for decryption")
	}
}
