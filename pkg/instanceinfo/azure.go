// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package instanceinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nstance-dev/nstance/pkg/health"
)

type azureProvider struct {
	client *http.Client
}

func newAzureProvider() *azureProvider {
	return &azureProvider{
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (p *azureProvider) Kind() string {
	return "azure"
}

func (p *azureProvider) GetInstanceID(ctx context.Context) (string, error) {
	// Try cloud-init cache first (fast path)
	if id, _ := GetCloudInitInstanceID("azure"); id != "" {
		return id, nil
	}

	// Fall back to IMDS
	req, err := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/metadata/instance/compute?api-version=2021-02-01", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata", "true")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to query Azure instance metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var metadata struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return "", fmt.Errorf("failed to parse Azure metadata: %w", err)
	}

	return metadata.Name, nil
}

func (p *azureProvider) IsSpot(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/metadata/instance/compute?api-version=2021-02-01", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Metadata", "true")

	resp, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to query Azure instance metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return false, nil
	}

	var metadata struct {
		Priority string `json:"priority"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return false, fmt.Errorf("failed to parse Azure metadata: %w", err)
	}

	return metadata.Priority == "Spot", nil
}

func (p *azureProvider) GetTerminationNotice(ctx context.Context) (*health.TerminationNotice, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/metadata/scheduledevents?api-version=2020-07-01", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Metadata", "true")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, nil
	}

	var events struct {
		Events []struct {
			EventType string `json:"EventType"`
			StartTime string `json:"StartTime"`
		} `json:"Events"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, fmt.Errorf("failed to parse Azure scheduled events: %w", err)
	}

	for _, event := range events.Events {
		if event.EventType == "Preempt" {
			deadline, err := time.Parse(time.RFC3339, event.StartTime)
			if err != nil {
				deadline = time.Time{}
			} else {
				deadline = deadline.UTC()
			}
			return &health.TerminationNotice{
				Action:   "terminate",
				Deadline: deadline,
			}, nil
		}
	}

	return nil, nil
}
