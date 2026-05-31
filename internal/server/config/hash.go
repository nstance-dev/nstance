// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// runtimeConfigHash represents the fields included in runtime config hash computation
type runtimeConfigHash struct {
	Vars  map[string]string     `json:"vars"`
	Files map[string]FileConfig `json:"files"`
}

// infraConfigHash represents the fields included in infra config hash computation
type infraConfigHash struct {
	Args         map[string]interface{} `json:"args"`
	InstanceType string                 `json:"instance_type"`
	SubnetPool   string                 `json:"subnet_pool"`
	Userdata     *UserdataConfig        `json:"userdata,omitempty"`
	Kind         string                 `json:"kind"`
	Arch         string                 `json:"arch"`
}

// HashRuntimeConfig computes the runtime config hash from merged config
// This covers vars and files (anything that can be pushed to existing instances)
func HashRuntimeConfig(cfg MergedConfig) string {
	h := runtimeConfigHash{
		Vars:  cfg.Vars,
		Files: cfg.Files,
	}
	b, _ := json.Marshal(h)
	hash := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", hash)
}

// HashInfraConfig computes the infra config hash from merged config
// This covers immutable fields requiring VM replacement
func HashInfraConfig(cfg MergedConfig) string {
	h := infraConfigHash{
		Args:         cfg.Args,
		InstanceType: cfg.InstanceType,
		SubnetPool:   cfg.SubnetPool,
		Userdata:     cfg.Userdata,
		Kind:         cfg.Kind,
		Arch:         cfg.Arch,
	}
	b, _ := json.Marshal(h)
	hash := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", hash)
}
