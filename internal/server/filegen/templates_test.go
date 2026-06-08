// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package filegen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/pki"
)

func TestTemplateRenderer_ProcessEnvTemplate(t *testing.T) {
	renderer := NewTemplateRenderer(nil)

	// Create test template data
	templateData := pki.CertificateTemplateData{
		Instance: pki.InstanceData{
			ID:       "test-instance-123",
			Kind:     "knc",
			Arch:     "arm64",
			Type:     "t4g.medium",
			Hostname: "test-host",
		},
		Cluster:  pki.ClusterData{ID: "test-cluster-id", CACert: "test-ca-cert"},
		Server:   pki.ServerData{Shard: "us-west-2a-1", RegistrationAddr: "10.0.0.1:8992", AgentAddr: "10.0.0.1:8994", OperatorAddr: "10.0.0.1:8993"},
		Provider: pki.ProviderData{Kind: "aws", Region: "us-west-2", Zone: "us-west-2a"},
		Image:    map[string]string{"debian_13_arm64": "ami-test123"},
		Vars: map[string]string{
			"Environment":         "production",
			"KUBELET_NODE_LABELS": "controlplane",
			"ClusterFQDN":         "test-cluster.example.com",
		},
	}

	// Create file config for env template
	fileConfig := &config.FileConfig{
		Kind: "env",
		Template: map[string]interface{}{
			"INSTANCE_ID":     "{{ .Instance.ID }}",
			"ENVIRONMENT":     "{{ .Vars.Environment }}",
			"K8S_NODE_LABELS": "{{ .Vars.KUBELET_NODE_LABELS }}",
			"CLUSTER_FQDN":    "{{ .Vars.ClusterFQDN }}",
			"CLUSTER_ID":      "{{ .Cluster.ID }}",
			"PROVIDER_KIND":   "{{ .Provider.Kind }}",
			"SERVER_AGENT":    "{{ .Server.AgentAddr }}",
			"IMAGE_ID":        "{{ .Image.debian_13_arm64 }}",
			"INSTANCE_ARCH":   "{{ .Instance.Arch }}",
			"INSTANCE_KIND":   "{{ .Instance.Kind }}",
			"INSTANCE_TYPE":   "{{ .Instance.Type }}",
		},
	}

	result, err := renderer.Render(fileConfig, templateData)
	if err != nil {
		t.Fatalf("Failed to process env template: %v", err)
	}

	resultStr := string(result)
	expectedLines := []string{
		"CLUSTER_FQDN=test-cluster.example.com",
		"CLUSTER_ID=test-cluster-id",
		"ENVIRONMENT=production",
		"IMAGE_ID=ami-test123",
		"INSTANCE_ARCH=arm64",
		"INSTANCE_ID=test-instance-123",
		"INSTANCE_KIND=knc",
		"INSTANCE_TYPE=t4g.medium",
		"K8S_NODE_LABELS=controlplane",
		"PROVIDER_KIND=aws",
		"SERVER_AGENT=10.0.0.1:8994",
	}

	for _, expectedLine := range expectedLines {
		if !strings.Contains(resultStr, expectedLine) {
			t.Errorf("Expected line %q not found in result:\n%s", expectedLine, resultStr)
		}
	}
}

func TestTemplateRenderer_ProcessJSONTemplate(t *testing.T) {
	renderer := NewTemplateRenderer(nil)

	// Create test template data
	templateData := pki.CertificateTemplateData{
		Instance: pki.InstanceData{
			ID:   "test-instance-123",
			Type: "t4g.medium",
			IP4:  "172.16.0.1",
		},
		Vars: map[string]string{
			"CLUSTER_DNS_IP": "10.96.0.10",
		},
	}

	// Create file config for JSON template
	fileConfig := &config.FileConfig{
		Kind: "json",
		Template: map[string]interface{}{
			"kind":          "KubeletConfiguration",
			"apiVersion":    "kubelet.config.k8s.io/v1beta1",
			"address":       "{{ .Instance.IP4 }}",
			"port":          10250,
			"cgroupDriver":  "systemd",
			"clusterDomain": "cluster.local",
			"clusterDNS":    []interface{}{"{{ .Vars.CLUSTER_DNS_IP }}"},
			"nodeLabels": map[string]interface{}{
				"instance.nadrama.com/id":   "{{ .Instance.ID }}",
				"instance.nadrama.com/type": "{{ .Instance.Type }}",
			},
		},
	}

	result, err := renderer.Render(fileConfig, templateData)
	if err != nil {
		t.Fatalf("Failed to process JSON template: %v", err)
	}

	// Parse result back to verify structure
	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("Failed to parse result JSON: %v", err)
	}

	// Verify templated values were processed
	if parsed["address"] != "172.16.0.1" {
		t.Errorf("Expected address to be '172.16.0.1', got %v", parsed["address"])
	}

	if parsed["port"] != float64(10250) {
		t.Errorf("Expected port to be 10250, got %v", parsed["port"])
	}

	nodeLabels, ok := parsed["nodeLabels"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected nodeLabels to be an object")
	}

	if nodeLabels["instance.nadrama.com/id"] != "test-instance-123" {
		t.Errorf("Expected instance ID to be 'test-instance-123', got %v", nodeLabels["instance.nadrama.com/id"])
	}
}

func TestTemplateRenderer_ProcessStringTemplate(t *testing.T) {
	renderer := NewTemplateRenderer(nil)

	// Create test template data
	templateData := pki.CertificateTemplateData{
		Instance: pki.InstanceData{
			ID: "test-instance-123",
		},
		Vars: map[string]string{
			"Environment": "production",
		},
	}

	// Create file config for string template
	fileConfig := &config.FileConfig{
		Kind:     "string",
		Template: "#!/bin/bash\necho \"Instance {{ .Instance.ID }} starting\"\nexport ENV={{ .Vars.Environment }}",
	}

	result, err := renderer.Render(fileConfig, templateData)
	if err != nil {
		t.Fatalf("Failed to process string template: %v", err)
	}

	expected := "#!/bin/bash\necho \"Instance test-instance-123 starting\"\nexport ENV=production"
	if string(result) != expected {
		t.Errorf("Expected:\n%s\n\nGot:\n%s", expected, string(result))
	}
}
