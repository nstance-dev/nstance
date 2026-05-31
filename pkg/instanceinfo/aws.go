// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package instanceinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nstance-dev/nstance/pkg/health"
)

type awsProvider struct {
	client *http.Client
}

func newAWSProvider() *awsProvider {
	return &awsProvider{
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (p *awsProvider) Kind() string {
	return "aws"
}

func (p *awsProvider) GetInstanceID(ctx context.Context) (string, error) {
	// Try cloud-init cache first (fast path)
	if id, _ := GetCloudInitInstanceID("aws"); id != "" {
		return id, nil
	}

	// Fall back to IMDS
	req, err := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/latest/meta-data/instance-id", nil)
	if err != nil {
		return "", err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to query instance-id: %w", err)
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

func (p *awsProvider) IsSpot(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/latest/meta-data/instance-life-cycle", nil)
	if err != nil {
		return false, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to query instance life cycle: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return false, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response: %w", err)
	}

	return string(body) == "spot", nil
}

func (p *awsProvider) GetTerminationNotice(ctx context.Context) (*health.TerminationNotice, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://169.254.169.254/latest/meta-data/spot/instance-action", nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == 404 {
		return nil, nil
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read termination notice: %w", err)
	}

	var notice struct {
		Action string `json:"action"`
		Time   string `json:"time"`
	}

	if err := json.Unmarshal(body, &notice); err != nil {
		return nil, fmt.Errorf("failed to parse termination notice JSON: %w", err)
	}

	deadline, err := time.Parse(time.RFC3339, notice.Time)
	if err != nil {
		deadline = time.Time{}
	} else {
		deadline = deadline.UTC()
	}

	return &health.TerminationNotice{
		Action:   notice.Action,
		Deadline: deadline,
	}, nil
}
