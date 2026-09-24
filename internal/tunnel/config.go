// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package tunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"time"
)

// Config is the complete static local configuration for nstance-tunnel.
type Config struct {
	Socket            string                   `json:"socket"`
	Tunnels           map[string]ProcessConfig `json:"tunnels"`
	MinRestartBackoff time.Duration            `json:"-"`
	MaxRestartBackoff time.Duration            `json:"-"`
}

// ProcessConfig defines one fixed local process and readiness endpoint.
type ProcessConfig struct {
	Command          string        `json:"command"`
	Args             []string      `json:"args,omitempty"`
	ReadinessURL     string        `json:"readiness_url"`
	ReadinessTimeout time.Duration `json:"-"`
}

// rawConfig is the JSON representation with durations encoded as strings.
type rawConfig struct {
	Socket  string `json:"socket"`
	Tunnels map[string]struct {
		Command          string   `json:"command"`
		Args             []string `json:"args,omitempty"`
		ReadinessURL     string   `json:"readiness_url"`
		ReadinessTimeout string   `json:"readiness_timeout,omitempty"`
	} `json:"tunnels"`
	MinRestartBackoff string `json:"min_restart_backoff,omitempty"`
	MaxRestartBackoff string `json:"max_restart_backoff,omitempty"`
}

// LoadConfig strictly loads a JSON configuration file.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read tunnel config: %w", err)
	}
	var raw rawConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("decode tunnel config: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("decode tunnel config: trailing content")
	}
	cfg := Config{Socket: raw.Socket, Tunnels: make(map[string]ProcessConfig), MinRestartBackoff: time.Second, MaxRestartBackoff: 30 * time.Second}
	if raw.MinRestartBackoff != "" {
		cfg.MinRestartBackoff, err = time.ParseDuration(raw.MinRestartBackoff)
		if err != nil {
			return Config{}, fmt.Errorf("parse min restart backoff: %w", err)
		}
	}
	if raw.MaxRestartBackoff != "" {
		cfg.MaxRestartBackoff, err = time.ParseDuration(raw.MaxRestartBackoff)
		if err != nil {
			return Config{}, fmt.Errorf("parse max restart backoff: %w", err)
		}
	}
	if cfg.Socket == "" || len(raw.Tunnels) == 0 {
		return Config{}, fmt.Errorf("socket and at least one local tunnel definition are required")
	}
	for tunnelName, item := range raw.Tunnels {
		timeout := 30 * time.Second
		if item.ReadinessTimeout != "" {
			timeout, err = time.ParseDuration(item.ReadinessTimeout)
			if err != nil {
				return Config{}, fmt.Errorf("parse readiness timeout for %q: %w", tunnelName, err)
			}
		}
		readinessURL, parseErr := url.Parse(item.ReadinessURL)
		if parseErr != nil {
			return Config{}, fmt.Errorf("tunnel %q requires a loopback HTTP readiness URL", tunnelName)
		}
		host := readinessURL.Hostname()
		ip := net.ParseIP(host)
		if readinessURL.Scheme != "http" || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return Config{}, fmt.Errorf("tunnel %q requires a loopback HTTP readiness URL", tunnelName)
		}
		if tunnelName == "" || item.Command == "" || item.Command[0] != '/' {
			return Config{}, fmt.Errorf("tunnel %q requires an absolute command and readiness URL", tunnelName)
		}
		cfg.Tunnels[tunnelName] = ProcessConfig{Command: item.Command, Args: item.Args, ReadinessURL: item.ReadinessURL, ReadinessTimeout: timeout}
	}
	return cfg, nil
}
