// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package fakeserver

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/keys"
	"github.com/nstance-dev/nstance/internal/server/pki"
	"github.com/nstance-dev/nstance/pkg/nonce"
)

// persistedInstance is the fake server's durable state for one prepared instance.
type persistedInstance struct {
	TenantID     string `json:"tenant_id"`
	InstanceID   string `json:"instance_id"`
	InstanceKind string `json:"instance_kind"`
	Hostname     string `json:"hostname"`
	IPv4         string `json:"ipv4"`
	IPv6         string `json:"ipv6"`
	NonceJWT     string `json:"nonce_jwt"`
	Registered   bool   `json:"registered"`
}

// tenantKey returns the store key for a tenant runtime configuration.
func (s *Server) tenantKey(id string) string {
	return filepath.ToSlash(filepath.Join("fakeserver", "tenants", id, "runtime.json"))
}

// instanceKey returns the store key for persisted instance state.
func (s *Server) instanceKey(id string) string {
	return filepath.ToSlash(filepath.Join("fakeserver", "instances", id, "instance.json"))
}

// pendingKey returns the store key for files waiting to be delivered to an instance.
func (s *Server) pendingKey(id string) string {
	return filepath.ToSlash(filepath.Join("fakeserver", "instances", id, "pending-files.json"))
}

// publicKey returns the store key for a submitted instance public key.
func (s *Server) publicKey(id, name string) string {
	return filepath.ToSlash(filepath.Join("fakeserver", "instances", id, "keys", name+".pem"))
}

// loadOrCreateCA loads the fake server CA from the store or creates and persists one.
func (s *Server) loadOrCreateCA(ctx context.Context) ([]byte, []byte, error) {
	cert, e1 := s.cfg.Store.Get(ctx, "fakeserver/global/ca.crt")
	key, e2 := s.cfg.Store.Get(ctx, "fakeserver/global/ca.key")
	if e1 == nil && e2 == nil {
		return cert, key, nil
	}
	if e1 != nil && !errors.Is(e1, ErrNotFound) {
		return nil, nil, e1
	}
	if e2 != nil && !errors.Is(e2, ErrNotFound) {
		return nil, nil, e2
	}
	if e1 == nil || e2 == nil {
		return nil, nil, fmt.Errorf("incomplete fake server CA state")
	}
	cert, key, err := pki.GenerateTestCA()
	if err != nil {
		return nil, nil, err
	}
	if err := s.cfg.Store.Put(ctx, "fakeserver/global/ca.crt", cert); err != nil {
		return nil, nil, err
	}
	if err := s.cfg.Store.Put(ctx, "fakeserver/global/ca.key", key); err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// loadOrCreateNonceKey loads the registration nonce signing key or creates and persists one.
func (s *Server) loadOrCreateNonceKey(ctx context.Context) ([]byte, ed25519.PrivateKey, error) {
	pemBytes, err := s.cfg.Store.Get(ctx, "fakeserver/global/registration-nonce.key")
	if err == nil {
		key, err := keys.ParseEd25519PrivateKey(pemBytes)
		return pemBytes, key, err
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, nil, err
	}
	_, keyPEM, err := keys.GenerateTestEd25519KeyPair()
	if err != nil {
		return nil, nil, err
	}
	key, err := keys.ParseEd25519PrivateKey(keyPEM)
	if err != nil {
		return nil, nil, err
	}
	if err := s.cfg.Store.Put(ctx, "fakeserver/global/registration-nonce.key", keyPEM); err != nil {
		return nil, nil, err
	}
	return keyPEM, key, nil
}

// registrationJWT signs a one-use agent registration nonce for an instance request.
func (s *Server) registrationJWT(req InstanceRequest, tenant *TenantConfig) (string, error) {
	now := time.Now()
	claims := nonce.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   req.InstanceID,
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		Kind:       "agent",
		ConfigHash: tenantRuntimeConfigHash(tenant),
		ClusterID:  s.cfg.ClusterID,
		Shard:      s.cfg.ShardID,
		Group:      "local",
		Tenant:     req.TenantID,
	}
	return nonce.Sign(s.nonceKey, claims)
}

// tenantRuntimeConfigHash computes the runtime config hash for a tenant using
// the same hashing scheme as the real server (config.HashRuntimeConfig), so the
// hash agents persist locally is comparable to what production would issue for
// for an equivalent group config.
func tenantRuntimeConfigHash(tenant *TenantConfig) string {
	if tenant == nil {
		return ""
	}
	return config.HashRuntimeConfig(config.MergedConfig{
		Vars:  tenant.Vars,
		Files: tenant.Files,
	})
}

// tenant loads and decodes tenant configuration from the store.
func (s *Server) tenant(ctx context.Context, id string) (*TenantConfig, error) {
	b, err := s.cfg.Store.Get(ctx, s.tenantKey(id))
	if err != nil {
		return nil, err
	}
	var t TenantConfig
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// getInstance loads and decodes persisted instance state from the store.
func (s *Server) getInstance(ctx context.Context, id string) (*persistedInstance, error) {
	b, err := s.cfg.Store.Get(ctx, s.instanceKey(id))
	if err != nil {
		return nil, err
	}
	var inst persistedInstance
	if err := json.Unmarshal(b, &inst); err != nil {
		return nil, err
	}
	return &inst, nil
}

// putInstance encodes and stores persisted instance state.
func (s *Server) putInstance(ctx context.Context, inst *persistedInstance) error {
	b, err := json.Marshal(inst)
	if err != nil {
		return err
	}
	return s.cfg.Store.Put(ctx, s.instanceKey(inst.InstanceID), b)
}

// baseKeyName derives the public-key base name used for a configured file.
func baseKeyName(fc FileConfig, filename string) string {
	if fc.Key != nil && fc.Key.Name != "" {
		return strings.TrimSuffix(fc.Key.Name, ".pub")
	}
	return strings.TrimSuffix(filename, ".crt")
}
