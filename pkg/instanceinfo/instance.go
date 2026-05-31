// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package instanceinfo

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/nstance-dev/nstance/pkg/health"
)

// Provider defines the interface for querying cloud instance metadata.
type Provider interface {
	// Kind returns the provider kind (aws, azure, gcp).
	Kind() string

	// GetInstanceID returns the provider-specific instance ID.
	// For AWS: instance-id (e.g., i-abc123)
	// For Azure: VM name
	// For GCP: instance name
	GetInstanceID(ctx context.Context) (string, error)

	// IsSpot returns true if running on a spot/preemptible instance.
	IsSpot(ctx context.Context) (bool, error)

	// GetTerminationNotice checks for spot termination notices.
	// Returns nil if no termination notice is present.
	GetTerminationNotice(ctx context.Context) (*health.TerminationNotice, error)
}

// Client provides access to cloud instance metadata.
type Client struct {
	provider Provider
}

// New creates a new instance metadata client.
// It auto-detects the cloud provider.
func New() (*Client, error) {
	provider, err := detectProvider()
	if err != nil {
		return nil, err
	}

	return &Client{provider: provider}, nil
}

// NewWithProvider creates a client with an explicit provider.
func NewWithProvider(provider Provider) *Client {
	return &Client{provider: provider}
}

// Provider returns the detected provider kind.
func (c *Client) Provider() string {
	return c.provider.Kind()
}

// GetInstanceID returns the provider-specific instance ID.
func (c *Client) GetInstanceID(ctx context.Context) (string, error) {
	return c.provider.GetInstanceID(ctx)
}

// IsSpot returns true if running on a spot/preemptible instance.
func (c *Client) IsSpot(ctx context.Context) (bool, error) {
	return c.provider.IsSpot(ctx)
}

// GetTerminationNotice checks for spot termination notices.
func (c *Client) GetTerminationNotice(ctx context.Context) (*health.TerminationNotice, error) {
	return c.provider.GetTerminationNotice(ctx)
}

// detectProvider attempts to detect the cloud provider.
func detectProvider() (Provider, error) {
	if provider := os.Getenv("NSTANCE_PROVIDER"); provider != "" {
		return newProviderByName(provider)
	}

	if isAWS() {
		return newAWSProvider(), nil
	}
	if isGCP() {
		return newGCPProvider(), nil
	}
	if isAzure() {
		return newAzureProvider(), nil
	}

	return nil, fmt.Errorf("unable to detect cloud provider")
}

func newProviderByName(name string) (Provider, error) {
	switch name {
	case "aws":
		return newAWSProvider(), nil
	case "gcp":
		return newGCPProvider(), nil
	case "azure":
		return newAzureProvider(), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
}

func isAWS() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://169.254.169.254/latest/meta-data/instance-id")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == 200
}

func isGCP() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/instance/id", nil)
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == 200
}

func isAzure() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest("GET", "http://169.254.169.254/metadata/instance?api-version=2021-02-01&format=json", nil)
	req.Header.Set("Metadata", "true")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == 200
}
