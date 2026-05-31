// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"fmt"

	"github.com/nstance-dev/nstance/internal/server/storage"
)

// ObjectStorageStore implements Store using object storage (S3, GCS, etc.) with optional encryption
type ObjectStorageStore struct {
	storage        storage.Storage
	prefix         string
	encryptionKeys [][]byte // Optional keys for encryption (first used for encryption, all tried for decryption)
}

// NewObjectStorageStore creates a new object storage-based store with optional encryption keys
func NewObjectStorageStore(storage storage.Storage, prefix string, encryptionKeys [][]byte) *ObjectStorageStore {
	if prefix == "" {
		prefix = "secret/"
	}
	return &ObjectStorageStore{
		storage:        storage,
		prefix:         prefix,
		encryptionKeys: encryptionKeys,
	}
}

// Get retrieves a secret from object storage
func (s *ObjectStorageStore) Get(ctx context.Context, name string) ([]byte, error) {
	key := s.prefix + name
	data, _, err := s.storage.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s: %w", name, err)
	}

	// Decrypt if encryption keys are provided
	if len(s.encryptionKeys) > 0 {
		return DecryptDataWithMultipleKeys(data, s.encryptionKeys)
	}

	return data, nil
}

// Set stores a secret in object storage
func (s *ObjectStorageStore) Set(ctx context.Context, name string, data []byte) error {
	key := s.prefix + name

	// Encrypt if encryption keys are provided (use first key for encryption)
	if len(s.encryptionKeys) > 0 {
		encrypted, err := EncryptData(data, s.encryptionKeys[0])
		if err != nil {
			return fmt.Errorf("failed to encrypt secret %s: %w", name, err)
		}
		data = encrypted
	}

	if err := s.storage.Put(ctx, key, data); err != nil {
		return fmt.Errorf("failed to store secret %s: %w", name, err)
	}

	return nil
}

// Delete removes a secret from object storage
func (s *ObjectStorageStore) Delete(ctx context.Context, name string) error {
	key := s.prefix + name
	if err := s.storage.Delete(ctx, key); err != nil {
		return fmt.Errorf("failed to delete secret %s: %w", name, err)
	}
	return nil
}
