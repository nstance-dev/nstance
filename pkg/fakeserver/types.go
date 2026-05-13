// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package fakeserver

import (
	"log/slog"

	"github.com/nstance-dev/nstance/internal/server/config"
)

// Config controls a fake Nstance server.
type Config struct {
	Store                  Store
	ClusterID              string
	ShardID                string
	ListenAddr             string
	RegistrationListenAddr string
	AgentListenAddr        string
	AdvertiseHost          string
	Logger                 *slog.Logger
}

// TenantConfig describes tenant-scoped runtime file and certificate settings.
type TenantConfig struct {
	TenantID     string
	Kind         string
	Arch         string
	InstanceType string
	Vars         map[string]string
	Files        map[string]config.FileConfig
	Certificates map[string]config.CertConfig
}

// FileConfig is the Nstance server file config shape used by runtime templates.
type FileConfig = config.FileConfig

// KeyConfig describes an agent-generated key dependency for a certificate file.
type KeyConfig = config.KeyConfig

// CertificateConfig is the Nstance server certificate template shape.
type CertificateConfig = config.CertConfig

// InstanceRequest creates or updates fake server state for one instance.
type InstanceRequest struct {
	TenantID     string
	InstanceID   string
	InstanceKind string
	Hostname     string
	IPv4         string
	IPv6         string
}

// InstanceEnv contains environment variables needed by nstance-agent.
type InstanceEnv struct {
	Vars map[string]string
}

// ServerAddrs contains the Nstance server addresses used by an nstance-agent.
type ServerAddrs struct {
	RegistrationAddr string
	AgentAddr        string
}
