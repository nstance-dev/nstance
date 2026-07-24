// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package instanceinfo

import (
	"encoding/json"
	"fmt"
	"os"
)

const cloudInitDataPath = "/run/cloud-init/instance-data.json"

// CloudInitData represents the cloud-init instance data structure.
type CloudInitData struct {
	V1       CloudInitV1    `json:"v1"`
	DS       CloudInitDS    `json:"ds"`
	Metadata map[string]any `json:"meta_data"`
}

// CloudInitV1 contains standardized instance data.
type CloudInitV1 struct {
	InstanceID string `json:"instance_id"`
	Region     string `json:"region"`
	Zone       string `json:"availability_zone"`
}

// CloudInitDS contains provider-specific data source metadata.
type CloudInitDS struct {
	MetaData CloudInitProviderMeta `json:"meta_data"`
}

// CloudInitProviderMeta contains provider-specific metadata fields.
type CloudInitProviderMeta struct {
	// AWS fields
	InstanceID string `json:"instance-id"`

	// Azure fields
	Compute *struct {
		Name              string `json:"name"`
		VMId              string `json:"vmId"`
		ResourceGroupName string `json:"resourceGroupName"`
		SubscriptionId    string `json:"subscriptionId"`
	} `json:"compute"`

	// Google Cloud fields
	Name      string `json:"name"`
	Zone      string `json:"zone"`
	ProjectID string `json:"project-id"`
}

// ReadCloudInit reads and parses the cloud-init instance data file.
// Returns nil if the file doesn't exist (not running with cloud-init).
func ReadCloudInit() (*CloudInitData, error) {
	data, err := os.ReadFile(cloudInitDataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read cloud-init data: %w", err)
	}

	var cloudInit CloudInitData
	if err := json.Unmarshal(data, &cloudInit); err != nil {
		return nil, fmt.Errorf("failed to parse cloud-init data: %w", err)
	}

	return &cloudInit, nil
}

// GetCloudInitInstanceID gets the instance ID from cloud-init cache for the given provider.
func GetCloudInitInstanceID(providerName string) (string, error) {
	data, err := ReadCloudInit()
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", nil
	}

	switch providerName {
	case "aws":
		if data.V1.InstanceID != "" {
			return data.V1.InstanceID, nil
		}
		return data.DS.MetaData.InstanceID, nil
	case "azure":
		if data.DS.MetaData.Compute != nil {
			return data.DS.MetaData.Compute.Name, nil
		}
		return "", nil
	case "google":
		return data.DS.MetaData.Name, nil
	default:
		return "", nil
	}
}
