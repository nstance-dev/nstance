// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import "testing"

// TestGoogleSecretManagerSecretID verifies logical secret names become valid Google IDs.
func TestGoogleSecretManagerSecretID(t *testing.T) {
	store := NewGoogleSecretManagerStore(nil, "test-project", "example-cluster-")

	for name, want := range map[string]string{
		"ca.key":                 "example-cluster-ca-key",
		"registration-nonce.key": "example-cluster-registration-nonce-key",
	} {
		t.Run(name, func(t *testing.T) {
			if got := store.secretID(name); got != want {
				t.Fatalf("secretID(%q) = %q, want %q", name, got, want)
			}
		})
	}
}

// TestGoogleSecretManagerSecretVersionName verifies reads use the converted secret ID.
func TestGoogleSecretManagerSecretVersionName(t *testing.T) {
	store := NewGoogleSecretManagerStore(nil, "test-project", "example-cluster-")
	want := "projects/test-project/secrets/example-cluster-ca-key/versions/latest"

	if got := store.secretVersionName("ca.key"); got != want {
		t.Fatalf("secretVersionName() = %q, want %q", got, want)
	}
}
