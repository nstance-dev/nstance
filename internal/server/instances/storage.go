// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package instances

import (
	"fmt"

	"github.com/refreshjs/puidv7"
)

// StorageKey converts a puidv7 instance ID to S3 key format: "uuid-prefix"
// e.g., "knc06bgm7733st2576nx5jht4ecjw" -> "01970a1c-e31e-7422-9cd5-e9651d11cc97-knc"
//
// This format preserves UUIDv7's time-ordering property when used as S3 object keys,
// while keeping the 3-character type prefix visible for debugging.
func StorageKey(instanceID string) (string, error) {
	if len(instanceID) < 3 {
		return "", fmt.Errorf("invalid instance ID %q: too short", instanceID)
	}

	prefix := instanceID[:3]
	uuid, err := puidv7.Decode(instanceID, prefix)
	if err != nil {
		return "", fmt.Errorf("invalid instance ID %q: %w", instanceID, err)
	}
	return fmt.Sprintf("%s-%s", uuid, prefix), nil
}

// ParseStorageKey extracts the original puidv7 instance ID from a storage key.
// e.g., "01970a1c-e31e-7422-9cd5-e9651d11cc97-knc" -> "knc06bgm7733st2576nx5jht4ecjw"
func ParseStorageKey(storageKey string) (string, error) {
	if len(storageKey) < 40 {
		return "", fmt.Errorf("invalid storage key %q: too short", storageKey)
	}

	uuidPart := storageKey[:36]
	if storageKey[36] != '-' {
		return "", fmt.Errorf("invalid storage key %q: missing separator", storageKey)
	}
	prefix := storageKey[37:]

	if len(prefix) < 1 || len(prefix) > 4 {
		return "", fmt.Errorf("invalid storage key %q: invalid prefix length", storageKey)
	}

	instanceID, err := puidv7.Encode(uuidPart, prefix)
	if err != nil {
		return "", fmt.Errorf("invalid storage key %q: %w", storageKey, err)
	}

	return instanceID, nil
}
