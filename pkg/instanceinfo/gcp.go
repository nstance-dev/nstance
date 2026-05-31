// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package instanceinfo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nstance-dev/nstance/pkg/health"
)

type gcpProvider struct {
	client *http.Client
}

func newGCPProvider() *gcpProvider {
	return &gcpProvider{
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (p *gcpProvider) Kind() string {
	return "gcp"
}

func (p *gcpProvider) GetInstanceID(ctx context.Context) (string, error) {
	// Try cloud-init cache first (fast path)
	if id, _ := GetCloudInitInstanceID("gcp"); id != "" {
		return id, nil
	}

	// Fall back to IMDS
	req, err := http.NewRequestWithContext(ctx, "GET", "http://metadata.google.internal/computeMetadata/v1/instance/name", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to query instance name: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(body), nil
}

func (p *gcpProvider) IsSpot(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://metadata.google.internal/computeMetadata/v1/instance/scheduling/preemptible", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to query GCP preemptible status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return false, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read GCP response: %w", err)
	}

	return string(body) == "TRUE", nil
}

func (p *gcpProvider) GetTerminationNotice(ctx context.Context) (*health.TerminationNotice, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://metadata.google.internal/computeMetadata/v1/instance/preempted", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read GCP preemption response: %w", err)
	}

	if string(body) == "TRUE" {
		return &health.TerminationNotice{
			Action: "terminate",
		}, nil
	}

	return nil, nil
}
