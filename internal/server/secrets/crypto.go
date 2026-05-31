// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

const (
	// AESKeySize is the size of AES keys we use (256-bit)
	AESKeySize = 32

	// NonceSize is the size of the nonce/IV for AES-GCM
	NonceSize = 12
)

// EncryptData encrypts data using AES-GCM with the given encryption key
func EncryptData(plaintext, key []byte) ([]byte, error) {
	if len(key) != AESKeySize {
		return nil, fmt.Errorf("encryption key must be exactly %d bytes (got %d)", AESKeySize, len(key))
	}

	// Create AES-GCM cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt the data
	ciphertext := aesGCM.Seal(nil, nonce, plaintext, nil)

	// Return nonce + ciphertext
	result := make([]byte, NonceSize+len(ciphertext))
	copy(result[:NonceSize], nonce)
	copy(result[NonceSize:], ciphertext)

	return result, nil
}

// DecryptData decrypts data using AES-GCM with the given encryption key
func DecryptData(encryptedData, key []byte) ([]byte, error) {
	if len(key) != AESKeySize {
		return nil, fmt.Errorf("encryption key must be exactly %d bytes (got %d)", AESKeySize, len(key))
	}

	if len(encryptedData) < NonceSize {
		return nil, fmt.Errorf("encrypted data too short")
	}

	// Create AES-GCM cipher
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Extract nonce and ciphertext
	nonce := encryptedData[:NonceSize]
	ciphertext := encryptedData[NonceSize:]

	// Decrypt the data
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}

	return plaintext, nil
}

// DecryptDataWithMultipleKeys tries to decrypt data with multiple encryption keys
func DecryptDataWithMultipleKeys(encryptedData []byte, keys [][]byte) ([]byte, error) {
	var lastErr error

	for _, key := range keys {
		plaintext, err := DecryptData(encryptedData, key)
		if err == nil {
			return plaintext, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("failed to decrypt with any encryption key, last error: %w", lastErr)
}

// CalculateChecksum calculates SHA256 checksum of data
func CalculateChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash)
}

// GenerateKeyID generates a unique identifier for a key
func GenerateKeyID() (string, error) {
	// Generate 16 random bytes and encode as hex
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate key ID: %w", err)
	}
	return fmt.Sprintf("%x", bytes), nil
}

// EncryptDataWithAlgorithm encrypts data with specific algorithm info
func EncryptDataWithAlgorithm(plaintext, key []byte, algorithm string) ([]byte, error) {
	switch algorithm {
	case "AES-256-GCM", "":
		return EncryptData(plaintext, key)
	default:
		return nil, fmt.Errorf("unsupported encryption algorithm: %s", algorithm)
	}
}

// DecryptDataWithAlgorithm decrypts data with specific algorithm info
func DecryptDataWithAlgorithm(encryptedData, key []byte, algorithm string) ([]byte, error) {
	switch algorithm {
	case "AES-256-GCM", "":
		return DecryptData(encryptedData, key)
	default:
		return nil, fmt.Errorf("unsupported encryption algorithm: %s", algorithm)
	}
}
