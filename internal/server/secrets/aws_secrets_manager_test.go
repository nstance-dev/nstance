// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// TestAWSSecretsManagerSet verifies create and update preserve text without binary encoding.
func TestAWSSecretsManagerSet(t *testing.T) {
	const value = "  café 世界\r\n-----BEGIN PRIVATE KEY-----\nYWJjZA==\n"
	for _, exists := range []bool{false, true} {
		name := "create"
		if exists {
			name = "update"
		}
		t.Run(name, func(t *testing.T) {
			stored := "old value"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/x-amz-json-1.1")
				var input struct {
					SecretString *string
					SecretBinary *string
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				switch r.Header.Get("X-Amz-Target") {
				case "secretsmanager.GetSecretValue":
					_ = json.NewEncoder(w).Encode(map[string]string{"SecretString": stored})
					return
				case "secretsmanager.UpdateSecret":
					if !exists {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"__type":"ResourceNotFoundException"}`))
						return
					}
				case "secretsmanager.CreateSecret":
					if exists {
						t.Error("attempted to create an existing secret")
					}
				default:
					t.Errorf("unexpected operation: %s", r.Header.Get("X-Amz-Target"))
				}
				if input.SecretString == nil || input.SecretBinary != nil {
					t.Error("write must use SecretString, not SecretBinary")
				} else {
					stored = *input.SecretString
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(server.Close)
			client := secretsmanager.New(secretsmanager.Options{
				Region: "us-east-1", BaseEndpoint: aws.String(server.URL),
				Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
					return aws.Credentials{AccessKeyID: "test", SecretAccessKey: "test"}, nil
				}),
			})
			store := NewAWSSecretsManagerStore(client, "/nstance/test/")
			if err := store.Set(context.Background(), "ca.key", []byte(value)); err != nil {
				t.Fatal(err)
			}
			got, err := store.Get(context.Background(), "ca.key")
			if err != nil || string(got) != value {
				t.Fatalf("Get() = %q, %v; want unchanged text", got, err)
			}
		})
	}
}

// TestAWSSecretsManagerSetRejectsBinary verifies malformed UTF-8 fails before accessing AWS.
func TestAWSSecretsManagerSetRejectsBinary(t *testing.T) {
	store := NewAWSSecretsManagerStore(nil, "/nstance/test/")
	if err := store.Set(context.Background(), "ca.key", []byte{0xff}); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
}
