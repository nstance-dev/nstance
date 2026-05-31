// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	proxmox "github.com/luthermonson/go-proxmox"

	"github.com/nstance-dev/nstance/internal/server/infra/provider"
)

// vmidFloor is the minimum VMID allocated for managed VMs. VMIDs below this
// are reserved for templates and manually created VMs.
const vmidFloor = 10000

// vmidMax is the maximum VMID supported by Proxmox VE (per API schema).
const vmidMax = 999999999

// Provider implements the provider.Provider interface for Proxmox VE
type Provider struct {
	client            *proxmox.Client
	config            provider.ProviderConfig
	options           ProviderOptions
	logger            *slog.Logger
	templateCache     templateCache
	vmidCounter       atomic.Int64                             // monotonic VMID counter; 0 = not yet seeded
	vmidHighWaterMark func(ctx context.Context) (int64, error) // optional DB-backed high water mark
}

// templateCache caches template VMID lookups per node when using template_name
type templateCache struct {
	mu     sync.RWMutex
	byNode map[string]int // nodeName -> vmid
}

// ProviderOptions contains Proxmox-specific configuration options
type ProviderOptions struct {
	InsecureTLS         bool   `json:"insecure_tls"`
	CloudInitISOStorage string `json:"cloud_init_iso_storage"` // Storage for cloud-init ISOs (default: "local")
}

// Options contains options for creating a Proxmox provider
type Options struct {
	Config            provider.ProviderConfig
	Logger            *slog.Logger
	APIURL            string                                   // Proxmox API URL (resolved from env at the factory level)
	TokenID           string                                   // Proxmox API token ID (resolved from env at the factory level)
	TokenSecret       string                                   // Proxmox API token secret (resolved from env at the factory level)
	VMIDHighWaterMark func(ctx context.Context) (int64, error) // Optional callback to get the highest known VMID from the DB
}

// NewProvider creates a new Proxmox provider
func NewProvider(opts Options) (*Provider, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	var providerOpts ProviderOptions
	if opts.Config.Options != nil {
		optBytes, err := json.Marshal(opts.Config.Options)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Proxmox options: %w", err)
		}
		if err := json.Unmarshal(optBytes, &providerOpts); err != nil {
			return nil, fmt.Errorf("invalid Proxmox options: %w", err)
		}
	}

	if opts.APIURL == "" {
		return nil, fmt.Errorf("API URL is required")
	}

	if opts.TokenID == "" {
		return nil, fmt.Errorf("token ID is required")
	}
	if opts.TokenSecret == "" {
		return nil, fmt.Errorf("token secret is required")
	}

	if providerOpts.CloudInitISOStorage == "" {
		providerOpts.CloudInitISOStorage = "local"
	}

	clientOpts := []proxmox.Option{
		proxmox.WithAPIToken(opts.TokenID, opts.TokenSecret),
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	if providerOpts.InsecureTLS {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}
	clientOpts = append(clientOpts, proxmox.WithHTTPClient(httpClient))

	client := proxmox.NewClient(opts.APIURL, clientOpts...)

	version, err := client.Version(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Proxmox API: %w", err)
	}

	opts.Logger.Info("connected to Proxmox VE",
		"version", version.Version,
		"release", version.Release,
	)

	// ensure client has sufficient permissions for VM lifecycle management
	perms, err := client.Permissions(context.Background(), &proxmox.PermissionsOptions{Path: "/"})
	if err != nil {
		return nil, fmt.Errorf("failed to check API token permissions: %w", err)
	}
	rootPerms, ok := perms["/"]
	if !ok {
		return nil, fmt.Errorf("API token has no permissions on /: assign PVEVMAdmin + PVEDatastoreAdmin + PVEAuditor + PVESDNUser roles to the API token user on /")
	}
	var missing []string
	for _, perm := range requiredPermissions {
		if !bool(rootPerms[perm]) {
			missing = append(missing, perm)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("API token missing required permissions on /: %v. Assign PVEVMAdmin + PVEDatastoreAdmin + PVEAuditor + PVESDNUser roles to the API token user", missing)
	}

	return &Provider{
		client:            client,
		config:            opts.Config,
		options:           providerOpts,
		logger:            opts.Logger,
		vmidHighWaterMark: opts.VMIDHighWaterMark,
		templateCache: templateCache{
			byNode: make(map[string]int),
		},
	}, nil
}

func (p *Provider) Kind() string {
	return "proxmox"
}
