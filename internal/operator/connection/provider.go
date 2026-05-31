// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package connection

import (
	"sync"

	"google.golang.org/grpc"
)

// Provider provides access to shard connections. It can be created before
// connections are established and populated later, allowing controllers
// to be set up before the manager starts while still receiving connections
// after registration completes.
type Provider struct {
	mu    sync.RWMutex
	conns map[string]*grpc.ClientConn
}

// NewProvider creates a new connection provider
func NewProvider() *Provider {
	return &Provider{}
}

// Set sets the shard connections. This should be called after
// registration completes and connections are established.
func (p *Provider) Set(conns map[string]*grpc.ClientConn) {
	p.mu.Lock()
	p.conns = conns
	p.mu.Unlock()
}

// Get returns all shard connections, or nil if not yet ready
func (p *Provider) Get() map[string]*grpc.ClientConn {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.conns
}
