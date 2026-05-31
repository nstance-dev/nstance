// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleServiceAccountToken handles POST requests to create a token for a
// ServiceAccount. This returns a fake token response sufficient for the
// operator's CAPI kubeconfig secret management in dev mode.
func (s *Server) handleServiceAccountToken(w http.ResponseWriter, r *http.Request, namespace, name string) {
	// Parse the incoming TokenRequest to get expirationSeconds
	var body struct {
		Spec struct {
			ExpirationSeconds *int64 `json:"expirationSeconds"`
		} `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	expSeconds := int64(3600) // default 1 hour
	if body.Spec.ExpirationSeconds != nil {
		expSeconds = *body.Spec.ExpirationSeconds
	}

	now := time.Now().UTC()
	expiry := now.Add(time.Duration(expSeconds) * time.Second)

	// Generate a fake token
	tokenBytes := make([]byte, 32)
	_, _ = rand.Read(tokenBytes)
	token := fmt.Sprintf("dev-token-%s", hex.EncodeToString(tokenBytes))

	response := map[string]interface{}{
		"apiVersion": "authentication.k8s.io/v1",
		"kind":       "TokenRequest",
		"metadata": map[string]interface{}{
			"creationTimestamp": now.Format(time.RFC3339),
		},
		"spec": map[string]interface{}{
			"expirationSeconds": expSeconds,
		},
		"status": map[string]interface{}{
			"token":               token,
			"expirationTimestamp": expiry.Format(time.RFC3339),
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(response)
}
