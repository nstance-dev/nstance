// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/nstance-dev/nstance/pkg/proxy"
)

// Load reads a strict static proxy configuration file.
func Load(path string) (proxy.Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return proxy.Config{}, fmt.Errorf("open proxy config: %w", err)
	}
	defer func() { _ = file.Close() }()
	var config proxy.Config
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return proxy.Config{}, fmt.Errorf("decode proxy config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return proxy.Config{}, fmt.Errorf("decode proxy config: unexpected trailing data")
	}
	if config.Listeners == nil {
		config.Listeners = make(map[string]proxy.Listener)
	}
	return config, nil
}
