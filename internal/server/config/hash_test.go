// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"strings"
	"testing"
)

func TestHashRuntimeConfig(t *testing.T) {
	t.Run("ConsistentHash", func(t *testing.T) {
		cfg := MergedConfig{
			Vars: map[string]string{
				"VAR1": "value1",
				"VAR2": "value2",
			},
			Files: map[string]FileConfig{
				"file1": {
					Kind:   "secret",
					Source: "secret1",
				},
			},
		}

		hash1 := HashRuntimeConfig(cfg)
		hash2 := HashRuntimeConfig(cfg)

		if hash1 != hash2 {
			t.Errorf("Expected consistent hashes, got %s and %s", hash1, hash2)
		}
	})

	t.Run("DifferentVarsProduceDifferentHash", func(t *testing.T) {
		cfg1 := MergedConfig{
			Vars: map[string]string{
				"VAR1": "value1",
			},
			Files: map[string]FileConfig{},
		}

		cfg2 := MergedConfig{
			Vars: map[string]string{
				"VAR1": "value2",
			},
			Files: map[string]FileConfig{},
		}

		hash1 := HashRuntimeConfig(cfg1)
		hash2 := HashRuntimeConfig(cfg2)

		if hash1 == hash2 {
			t.Errorf("Expected different hashes for different vars, both got %s", hash1)
		}
	})

	t.Run("DifferentFilesProduceDifferentHash", func(t *testing.T) {
		cfg1 := MergedConfig{
			Vars: map[string]string{},
			Files: map[string]FileConfig{
				"file1": {
					Kind:   "secret",
					Source: "secret1",
				},
			},
		}

		cfg2 := MergedConfig{
			Vars: map[string]string{},
			Files: map[string]FileConfig{
				"file1": {
					Kind:   "secret",
					Source: "secret2",
				},
			},
		}

		hash1 := HashRuntimeConfig(cfg1)
		hash2 := HashRuntimeConfig(cfg2)

		if hash1 == hash2 {
			t.Errorf("Expected different hashes for different files, both got %s", hash1)
		}
	})

	t.Run("HashFormatSHA256", func(t *testing.T) {
		cfg := MergedConfig{
			Vars:  map[string]string{},
			Files: map[string]FileConfig{},
		}

		hash := HashRuntimeConfig(cfg)

		if !strings.HasPrefix(hash, "sha256:") {
			t.Errorf("Expected hash to have sha256: prefix, got %s", hash)
		}
		if len(hash) != 71 { // "sha256:" (7) + 64 hex chars
			t.Errorf("Expected hash to be 71 characters (sha256: prefix + 64 hex), got %d", len(hash))
		}
	})

	t.Run("IgnoresOtherFields", func(t *testing.T) {
		cfg1 := MergedConfig{
			Vars:         map[string]string{"VAR": "value"},
			Files:        map[string]FileConfig{},
			InstanceType: "t3.micro",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind1",
			Arch:         "amd64",
			Args:         map[string]interface{}{"arg": "value"},
		}

		cfg2 := MergedConfig{
			Vars:         map[string]string{"VAR": "value"},
			Files:        map[string]FileConfig{},
			InstanceType: "t3.large",
			SubnetPool:   "subnet-2",
			Userdata:     &UserdataConfig{Content: "different userdata"},
			Kind:         "kind2",
			Arch:         "arm64",
			Args:         map[string]interface{}{"different": "arg"},
		}

		hash1 := HashRuntimeConfig(cfg1)
		hash2 := HashRuntimeConfig(cfg2)

		if hash1 != hash2 {
			t.Errorf("Expected same hash when only infra fields differ, got %s and %s", hash1, hash2)
		}
	})

	t.Run("EmptyConfig", func(t *testing.T) {
		cfg := MergedConfig{
			Vars:  make(map[string]string),
			Files: make(map[string]FileConfig),
		}

		hash := HashRuntimeConfig(cfg)

		if !strings.HasPrefix(hash, "sha256:") {
			t.Errorf("Expected valid sha256 hash format, got %s", hash)
		}
	})
}

func TestHashInfraConfig(t *testing.T) {
	t.Run("ConsistentHash", func(t *testing.T) {
		cfg := MergedConfig{
			Args: map[string]interface{}{
				"arg1": "value1",
				"arg2": 42,
			},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata content"},
			Kind:         "test-kind",
			Arch:         "amd64",
		}

		hash1 := HashInfraConfig(cfg)
		hash2 := HashInfraConfig(cfg)

		if hash1 != hash2 {
			t.Errorf("Expected consistent hashes, got %s and %s", hash1, hash2)
		}
	})

	t.Run("DifferentArgsProduceDifferentHash", func(t *testing.T) {
		cfg1 := MergedConfig{
			Args:         map[string]interface{}{"arg": "value1"},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind",
			Arch:         "amd64",
		}

		cfg2 := MergedConfig{
			Args:         map[string]interface{}{"arg": "value2"},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind",
			Arch:         "amd64",
		}

		hash1 := HashInfraConfig(cfg1)
		hash2 := HashInfraConfig(cfg2)

		if hash1 == hash2 {
			t.Errorf("Expected different hashes for different args, both got %s", hash1)
		}
	})

	t.Run("DifferentInstanceTypeProduceDifferentHash", func(t *testing.T) {
		cfg1 := MergedConfig{
			Args:         map[string]interface{}{},
			InstanceType: "t3.micro",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind",
			Arch:         "amd64",
		}

		cfg2 := MergedConfig{
			Args:         map[string]interface{}{},
			InstanceType: "t3.large",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind",
			Arch:         "amd64",
		}

		hash1 := HashInfraConfig(cfg1)
		hash2 := HashInfraConfig(cfg2)

		if hash1 == hash2 {
			t.Errorf("Expected different hashes for different instance types, both got %s", hash1)
		}
	})

	t.Run("DifferentSubnetProducesDifferentHash", func(t *testing.T) {
		cfg1 := MergedConfig{
			Args:         map[string]interface{}{},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind",
			Arch:         "amd64",
		}

		cfg2 := MergedConfig{
			Args:         map[string]interface{}{},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-2",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind",
			Arch:         "amd64",
		}

		hash1 := HashInfraConfig(cfg1)
		hash2 := HashInfraConfig(cfg2)

		if hash1 == hash2 {
			t.Errorf("Expected different hashes for different subnet pools, both got %s", hash1)
		}
	})

	t.Run("DifferentUserdataProduceDifferentHash", func(t *testing.T) {
		cfg1 := MergedConfig{
			Args:         map[string]interface{}{},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata1"},
			Kind:         "kind",
			Arch:         "amd64",
		}

		cfg2 := MergedConfig{
			Args:         map[string]interface{}{},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata2"},
			Kind:         "kind",
			Arch:         "amd64",
		}

		hash1 := HashInfraConfig(cfg1)
		hash2 := HashInfraConfig(cfg2)

		if hash1 == hash2 {
			t.Errorf("Expected different hashes for different userdata, both got %s", hash1)
		}
	})

	t.Run("DifferentKindProduceDifferentHash", func(t *testing.T) {
		cfg1 := MergedConfig{
			Args:         map[string]interface{}{},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind1",
			Arch:         "amd64",
		}

		cfg2 := MergedConfig{
			Args:         map[string]interface{}{},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind2",
			Arch:         "amd64",
		}

		hash1 := HashInfraConfig(cfg1)
		hash2 := HashInfraConfig(cfg2)

		if hash1 == hash2 {
			t.Errorf("Expected different hashes for different kinds, both got %s", hash1)
		}
	})

	t.Run("DifferentArchProduceDifferentHash", func(t *testing.T) {
		cfg1 := MergedConfig{
			Args:         map[string]interface{}{},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind",
			Arch:         "amd64",
		}

		cfg2 := MergedConfig{
			Args:         map[string]interface{}{},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind",
			Arch:         "arm64",
		}

		hash1 := HashInfraConfig(cfg1)
		hash2 := HashInfraConfig(cfg2)

		if hash1 == hash2 {
			t.Errorf("Expected different hashes for different arch, both got %s", hash1)
		}
	})

	t.Run("HashFormatSHA256", func(t *testing.T) {
		cfg := MergedConfig{
			Args:         map[string]interface{}{},
			InstanceType: "t3.medium",
			SubnetPool:   "",
			Userdata:     nil,
			Kind:         "kind",
			Arch:         "amd64",
		}

		hash := HashInfraConfig(cfg)

		if !strings.HasPrefix(hash, "sha256:") {
			t.Errorf("Expected hash to have sha256: prefix, got %s", hash)
		}
		if len(hash) != 71 { // "sha256:" (7) + 64 hex chars
			t.Errorf("Expected hash to be 71 characters (sha256: prefix + 64 hex), got %d", len(hash))
		}
	})

	t.Run("IgnoresRuntimeFields", func(t *testing.T) {
		cfg1 := MergedConfig{
			Args:         map[string]interface{}{"arg": "value"},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind",
			Arch:         "amd64",
			Vars:         map[string]string{"VAR": "value"},
			Files:        map[string]FileConfig{},
		}

		cfg2 := MergedConfig{
			Args:         map[string]interface{}{"arg": "value"},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind",
			Arch:         "amd64",
			Vars:         map[string]string{"VAR": "different"},
			Files: map[string]FileConfig{
				"file": {Kind: "secret"},
			},
		}

		hash1 := HashInfraConfig(cfg1)
		hash2 := HashInfraConfig(cfg2)

		if hash1 != hash2 {
			t.Errorf("Expected same hash when only runtime fields differ, got %s and %s", hash1, hash2)
		}
	})

	t.Run("EmptyConfig", func(t *testing.T) {
		cfg := MergedConfig{
			Args:       make(map[string]interface{}),
			SubnetPool: "",
		}

		hash := HashInfraConfig(cfg)

		if !strings.HasPrefix(hash, "sha256:") {
			t.Errorf("Expected valid sha256 hash format, got %s", hash)
		}
	})
}

func TestHashDifference(t *testing.T) {
	t.Run("RuntimeAndInfraHashesDiffer", func(t *testing.T) {
		cfg := MergedConfig{
			Args: map[string]interface{}{"arg": "value"},
			Vars: map[string]string{"VAR": "value"},
			Files: map[string]FileConfig{
				"file": {Kind: "secret"},
			},
			InstanceType: "t3.medium",
			SubnetPool:   "subnet-1",
			Userdata:     &UserdataConfig{Content: "userdata"},
			Kind:         "kind",
			Arch:         "amd64",
		}

		runtimeHash := HashRuntimeConfig(cfg)
		infraHash := HashInfraConfig(cfg)

		if runtimeHash == infraHash {
			t.Errorf("Runtime and infra hashes should differ, both got %s", runtimeHash)
		}
	})
}
