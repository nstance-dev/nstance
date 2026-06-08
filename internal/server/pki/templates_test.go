// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package pki

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/keys"
)

var certTemplates = map[string]config.CertConfig{
	"server-static-cn": {
		Kind: "server",
		CN:   stringPtr("my-service"),
		DNS: []string{
			"localhost",
			"{{ .Instance.ID }}",
			"{{ .Instance.Hostname }}",
		},
		IP: []string{
			"127.0.0.1",
			"{{ .Instance.IP4 }}",
			"{{ .Instance.IP6 }}",
		},
		TTL: 8760,
	},
	"server-templated-cn": {
		Kind: "server",
		CN:   stringPtr("{{ .Vars.ClusterSlug }}-service.example.com"),
		DNS: []string{
			"localhost",
			"{{ .Vars.ClusterSlug }}-service.example.com",
		},
		IP: []string{
			"127.0.0.1",
			"{{ .Instance.IP4 }}",
			"{{ .Instance.IP6 }}",
		},
		TTL: 8760,
	},
	"server-multi-dns": {
		Kind: "server",
		CN:   stringPtr("multi-dns-service"),
		DNS: []string{
			"localhost",
			"service.local",
			"service.default",
			"service.default.svc",
			"service.default.svc.cluster.local",
			"{{ .Instance.ID }}",
			"{{ .Instance.Hostname }}",
			"{{ .Instance.FQDN }}",
			"{{ .Vars.ClusterFQDN }}",
		},
		IP: []string{
			"127.0.0.1",
			"198.18.0.1",
			"{{ .Instance.IP4 }}",
			"{{ .Instance.IP6 }}",
		},
		TTL: 8760,
	},
	"server-minimal": {
		Kind: "server",
		CN:   stringPtr("minimal-service"),
		DNS: []string{
			"localhost",
			"{{ .Instance.ID }}",
			"{{ .Instance.Hostname }}",
		},
		IP: []string{
			"127.0.0.1",
			"{{ .Instance.IP4 }}",
			"{{ .Instance.IP6 }}",
		},
		TTL: 8760,
	},
	"client-static-cn": {
		Kind: "client",
		CN:   stringPtr("my-client"),
		DNS: []string{
			"localhost",
			"{{ .Instance.ID }}",
			"{{ .Instance.Hostname }}",
		},
		IP: []string{
			"127.0.0.1",
			"{{ .Instance.IP4 }}",
			"{{ .Instance.IP6 }}",
		},
		TTL: 8760,
	},
	"client-templated-cn": {
		Kind: "client",
		CN:   stringPtr("client:{{ .Instance.ID }}"),
		DNS: []string{
			"localhost",
			"{{ .Instance.ID }}",
			"{{ .Instance.Hostname }}",
		},
		IP: []string{
			"127.0.0.1",
			"{{ .Instance.IP4 }}",
			"{{ .Instance.IP6 }}",
		},
		TTL: 8760,
	},
	"client-with-org": {
		Kind:         "client",
		CN:           stringPtr("client:node:{{ .Instance.ID }}"),
		Organization: []string{"nodes"},
		DNS: []string{
			"localhost",
			"{{ .Instance.ID }}",
			"{{ .Instance.Hostname }}",
		},
		IP: []string{
			"127.0.0.1",
			"{{ .Instance.IP4 }}",
			"{{ .Instance.IP6 }}",
		},
		TTL: 8760,
	},
	"client-minimal": {
		Kind: "client",
		CN:   stringPtr("minimal-client"),
		DNS: []string{
			"localhost",
			"client-service",
		},
		IP: []string{
			"127.0.0.1",
		},
		TTL: 8760,
	},
	"client-with-multi-org": {
		Kind:         "client",
		CN:           stringPtr("service-account"),
		Organization: []string{"service-accounts"},
		DNS: []string{
			"localhost",
			"service-name",
		},
		IP: []string{
			"127.0.0.1",
		},
		TTL: 8760,
	},
	"client-another-org": {
		Kind:         "client",
		CN:           stringPtr("controller"),
		Organization: []string{"controllers"},
		DNS: []string{
			"localhost",
			"controller-service",
		},
		IP: []string{
			"127.0.0.1",
		},
		TTL: 8760,
	},
}

func stringPtr(s string) *string {
	return &s
}

func parseCertificateFromPEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("invalid PEM block type: %s", block.Type)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	return cert, nil
}

func TestCertificateTemplateProcessing(t *testing.T) {
	t.Run("ProcessClientTemplateWithOrganization", func(t *testing.T) {
		certConfig := config.CertConfig{
			Kind:         "client",
			CN:           stringPtr("client:node:{{ .Instance.ID }}"),
			Organization: []string{"nodes"},
			DNS: []string{
				"localhost",
				"{{ .Instance.ID }}",
				"{{ .Instance.Hostname }}",
				"{{ .Instance.Hostname }}",
			},
			IP: []string{
				"127.0.0.1",
				"{{ .Instance.IP4 }}",
			},
			TTL: 8760,
		}

		templateData := CertificateTemplateData{
			Instance: InstanceData{
				ID:       "knc0000000001r010000000000000",
				Hostname: "test-node",
				IP4:      "172.16.0.100",
				IP6:      "2001:db8::1",
			},
		}

		pkiConfig := CertificateConfig{
			Kind:         certConfig.Kind,
			CN:           certConfig.CN,
			Organization: certConfig.Organization,
			DNS:          certConfig.DNS,
			IP:           certConfig.IP,
			TTL:          certConfig.TTL,
		}
		processed, err := ProcessCertificateTemplate(pkiConfig, templateData)
		if err != nil {
			t.Fatalf("Failed to process template: %v", err)
		}

		expectedCN := "client:node:knc0000000001r010000000000000"
		if processed.CN == nil || *processed.CN != expectedCN {
			var actual string
			if processed.CN != nil {
				actual = *processed.CN
			}
			t.Errorf("Expected CN '%s', got '%s'", expectedCN, actual)
		}

		if len(processed.Organization) != 1 || processed.Organization[0] != "nodes" {
			t.Errorf("Expected organization ['nodes'], got %v", processed.Organization)
		}

		// Duplicates should be removed by processing
		expectedDNS := []string{"localhost", "knc0000000001r010000000000000", "test-node"}
		// Check for existence rather than exact slice match for robustness
		for _, expected := range expectedDNS {
			found := false
			for _, dns := range processed.DNS {
				if dns == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected DNS entry '%s' not found in %v", expected, processed.DNS)
			}
		}

		expectedIP := []string{"127.0.0.1", "172.16.0.100"}
		if len(processed.IP) != len(expectedIP) {
			t.Errorf("Expected %d IP entries, got %d", len(expectedIP), len(processed.IP))
		}
		for i, expected := range expectedIP {
			if i >= len(processed.IP) || processed.IP[i] != expected {
				t.Errorf("Expected IP[%d] = '%s', got '%s'", i, expected, processed.IP[i])
			}
		}
	})

	t.Run("ProcessServerTemplateWithMultipleDNS", func(t *testing.T) {
		certConfig := config.CertConfig{
			Kind: "server",
			CN:   stringPtr("multi-dns-service"),
			DNS: []string{
				"localhost",
				"service.local",
				"service.default",
				"service.default.svc",
				"service.default.svc.cluster.local",
				"{{ .Instance.ID }}",
				"{{ .Vars.ClusterFQDN }}",
			},
			IP: []string{
				"127.0.0.1",
				"198.18.0.1",
				"{{ .Instance.IP4 }}",
			},
			TTL: 8760,
		}

		templateData := CreateCertificateTemplateData(
			InstanceData{
				ID:       "knc0000000001r010000000000000",
				Type:     "t4g.medium",
				Hostname: "master-node",
				FQDN:     "master-node.test-cluster.example.com",
				IP4:      "172.16.0.10",
			},
			ClusterData{ID: "test-cluster-id"},
			ServerData{},
			ProviderData{},
			map[string]string{
				"environment": "production",
				"ClusterSlug": "test-cluster",
				"ClusterFQDN": "test-cluster.example.com",
			},
			nil,
		)

		pkiConfig := CertificateConfig{
			Kind:         certConfig.Kind,
			CN:           certConfig.CN,
			Organization: certConfig.Organization,
			DNS:          certConfig.DNS,
			IP:           certConfig.IP,
			TTL:          certConfig.TTL,
		}
		processed, err := ProcessCertificateTemplate(pkiConfig, templateData)
		if err != nil {
			t.Fatalf("Failed to process template: %v", err)
		}

		expectedDNSEntries := []string{
			"localhost",
			"service.local",
			"service.default",
			"service.default.svc",
			"service.default.svc.cluster.local",
			"knc0000000001r010000000000000",
			"test-cluster.example.com",
		}

		for _, expected := range expectedDNSEntries {
			found := false
			for _, dns := range processed.DNS {
				if dns == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected DNS entry '%s' not found in %v", expected, processed.DNS)
			}
		}

		expectedIPs := []string{"127.0.0.1", "198.18.0.1", "172.16.0.10"}
		if len(processed.IP) != len(expectedIPs) {
			t.Errorf("Expected %d IP entries, got %d", len(expectedIPs), len(processed.IP))
		}
	})

	t.Run("EmptyTemplateValues", func(t *testing.T) {
		certConfig := config.CertConfig{
			Kind: "client",
			DNS: []string{
				"localhost",
				"{{ .Instance.Hostname }}", // Empty in test data
			},
			IP: []string{
				"127.0.0.1",
				"{{ .Instance.IP6 }}", // Empty in test data
			},
		}

		templateData := CertificateTemplateData{
			Instance: InstanceData{
				ID:       "test-instance",
				Hostname: "", // Empty
				IP4:      "172.16.0.1",
				IP6:      "", // Empty
			},
		}

		pkiConfig := CertificateConfig{
			Kind:         certConfig.Kind,
			CN:           certConfig.CN,
			Organization: certConfig.Organization,
			DNS:          certConfig.DNS,
			IP:           certConfig.IP,
			TTL:          certConfig.TTL,
		}
		processed, err := ProcessCertificateTemplate(pkiConfig, templateData)
		if err != nil {
			t.Fatalf("Failed to process template: %v", err)
		}

		// Should only have non-empty values
		expectedDNS := []string{"localhost"}
		if len(processed.DNS) != len(expectedDNS) {
			t.Errorf("Expected %d DNS entries (empty values filtered), got %d", len(expectedDNS), len(processed.DNS))
		}

		expectedIP := []string{"127.0.0.1"}
		if len(processed.IP) != len(expectedIP) {
			t.Errorf("Expected %d IP entries (empty values filtered), got %d", len(expectedIP), len(processed.IP))
		}
	})
}

func TestCertificateGeneration(t *testing.T) {
	caCertPEM, caPrivateKeyPEM, err := GenerateTestCA()
	if err != nil {
		t.Fatalf("Failed to generate test CA: %v", err)
	}

	publicKeyPEM, err := keys.GenerateTestEd25519Key()
	if err != nil {
		t.Fatalf("Failed to generate test key: %v", err)
	}

	t.Run("ClientCertificateWithOrganization", func(t *testing.T) {
		templates := certTemplates
		template := templates["client-with-org"]

		templateData := CreateCertificateTemplateData(
			InstanceData{
				ID:       "knc0000000001r010000000000000",
				Type:     "t4g.large",
				Hostname: "worker-node-1",
				FQDN:     "worker-node-1.production-cluster.example.com",
				IP4:      "172.16.0.50",
				IP6:      "2001:db8::50",
			},
			ClusterData{ID: "production-cluster-id"},
			ServerData{},
			ProviderData{},
			map[string]string{
				"environment": "production",
				"ClusterSlug": "production-cluster",
				"ClusterFQDN": "production-cluster.example.com",
			},
			nil,
		)

		pkiConfig := CertificateConfig{
			Kind:         template.Kind,
			CN:           template.CN,
			Organization: template.Organization,
			DNS:          template.DNS,
			IP:           template.IP,
			TTL:          template.TTL,
		}
		processed, err := ProcessCertificateTemplate(pkiConfig, templateData)
		if err != nil {
			t.Fatalf("Failed to process template: %v", err)
		}

		certPEM, expiresAt, err := GenerateClientCertificateWithConfig(
			caCertPEM,
			caPrivateKeyPEM,
			publicKeyPEM,
			"knc0000000001r010000000000000",
			processed,
		)
		if err != nil {
			t.Fatalf("Failed to generate certificate: %v", err)
		}

		cert, err := parseCertificateFromPEM(certPEM)
		if err != nil {
			t.Fatalf("Failed to parse generated certificate: %v", err)
		}

		expectedCN := "client:node:knc0000000001r010000000000000"
		if cert.Subject.CommonName != expectedCN {
			t.Errorf("Expected CN '%s', got '%s'", expectedCN, cert.Subject.CommonName)
		}

		if len(cert.Subject.Organization) != 1 || cert.Subject.Organization[0] != "nodes" {
			t.Errorf("Expected organization ['nodes'], got %v", cert.Subject.Organization)
		}

		expectedDNSEntries := []string{"localhost", "knc0000000001r010000000000000", "worker-node-1"}
		for _, expected := range expectedDNSEntries {
			found := false
			for _, dns := range cert.DNSNames {
				if dns == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected DNS SAN '%s' not found in %v", expected, cert.DNSNames)
			}
		}

		hasLocalhostIP := false
		hasInstanceIP := false
		for _, ip := range cert.IPAddresses {
			if ip.String() == "127.0.0.1" {
				hasLocalhostIP = true
			}
			if ip.String() == "172.16.0.50" {
				hasInstanceIP = true
			}
		}
		if !hasLocalhostIP {
			t.Error("Certificate should include localhost IP")
		}
		if !hasInstanceIP {
			t.Error("Certificate should include instance IP")
		}

		hasClientAuth := false
		for _, usage := range cert.ExtKeyUsage {
			if usage == x509.ExtKeyUsageClientAuth {
				hasClientAuth = true
				break
			}
		}
		if !hasClientAuth {
			t.Error("Certificate should have ClientAuth extended key usage")
		}

		if expiresAt.Before(cert.NotAfter.Add(-time.Minute)) || expiresAt.After(cert.NotAfter.Add(time.Minute)) {
			t.Errorf("Certificate expiration mismatch: expected ~%v, got %v", expiresAt, cert.NotAfter)
		}
	})

	t.Run("ServerCertificateWithMultipleDNS", func(t *testing.T) {
		templates := certTemplates
		template := templates["server-multi-dns"]

		templateData := CreateCertificateTemplateData(
			InstanceData{
				ID:       "knc0000000001r010000000000000",
				Type:     "t4g.xlarge",
				Hostname: "master-node",
				FQDN:     "master-node.production-cluster.example.com",
				IP4:      "172.16.0.10",
			},
			ClusterData{ID: "production-cluster-id"},
			ServerData{},
			ProviderData{},
			map[string]string{
				"environment": "production",
				"ClusterSlug": "production-cluster",
				"ClusterFQDN": "production-cluster.example.com",
			},
			nil,
		)

		pkiConfig := CertificateConfig{
			Kind:         template.Kind,
			CN:           template.CN,
			Organization: template.Organization,
			DNS:          template.DNS,
			IP:           template.IP,
			TTL:          template.TTL,
		}
		processed, err := ProcessCertificateTemplate(pkiConfig, templateData)
		if err != nil {
			t.Fatalf("Failed to process template: %v", err)
		}

		certPEM, _, err := GenerateClientCertificateWithConfig(
			caCertPEM,
			caPrivateKeyPEM,
			publicKeyPEM,
			"knc0000000001r010000000000000",
			processed,
		)
		if err != nil {
			t.Fatalf("Failed to generate certificate: %v", err)
		}

		cert, err := parseCertificateFromPEM(certPEM)
		if err != nil {
			t.Fatalf("Failed to parse generated certificate: %v", err)
		}

		hasServerAuth := false
		for _, usage := range cert.ExtKeyUsage {
			if usage == x509.ExtKeyUsageServerAuth {
				hasServerAuth = true
				break
			}
		}
		if !hasServerAuth {
			t.Error("Certificate should have ServerAuth extended key usage")
		}

		serviceDNSNames := []string{
			"service.local",
			"service.default",
			"service.default.svc.cluster.local",
		}
		for _, expected := range serviceDNSNames {
			found := false
			for _, dns := range cert.DNSNames {
				if dns == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected DNS name '%s' not found in %v", expected, cert.DNSNames)
			}
		}

		hasServiceIP := false
		for _, ip := range cert.IPAddresses {
			if ip.String() == "198.18.0.1" {
				hasServiceIP = true
				break
			}
		}
		if !hasServiceIP {
			t.Error("Certificate should include service IP 198.18.0.1")
		}
	})
}

func TestTemplateProcessing(t *testing.T) {
	templateData := CertificateTemplateData{
		Instance: InstanceData{
			ID:       "test-instance",
			Kind:     "knc",
			Arch:     "arm64",
			Type:     "t4g.medium",
			Hostname: "test-hostname",
			IP4:      "192.168.1.100",
			IP6:      "2001:db8::100",
		},
		Cluster: ClusterData{ID: "test-cluster-id", CACert: "test-ca-cert"},
		Server: ServerData{
			Shard:            "us-west-2a-1",
			RegistrationAddr: "10.0.0.1:8992",
			AgentAddr:        "10.0.0.1:8994",
			OperatorAddr:     "10.0.0.1:8993",
		},
		Provider: ProviderData{Kind: "aws", Region: "us-west-2", Zone: "us-west-2a"},
		Image:    map[string]string{"debian_13_arm64": "ami-test123"},
		Vars: map[string]string{
			"environment": "development",
			"ClusterSlug": "test-cluster",
			"ClusterFQDN": "test-cluster.cluster.cool",
		},
	}

	testCases := []struct {
		input    string
		expected string
	}{
		{"simple-string", "simple-string"},
		{"{{ .Instance.ID }}", "test-instance"},
		{"{{ .Instance.Kind }}", "knc"},
		{"{{ .Instance.Arch }}", "arm64"},
		{"{{ .Instance.Type }}", "t4g.medium"},
		{"system:node:{{ .Instance.ID }}", "system:node:test-instance"},
		{"{{ .Instance.Hostname }}", "test-hostname"},
		{"{{ .Instance.IP4 }}", "192.168.1.100"},
		{"{{ .Cluster.ID }}", "test-cluster-id"},
		{"{{ .Cluster.CACert }}", "test-ca-cert"},
		{"{{ .Server.Shard }}", "us-west-2a-1"},
		{"{{ .Server.RegistrationAddr }}", "10.0.0.1:8992"},
		{"{{ .Server.AgentAddr }}", "10.0.0.1:8994"},
		{"{{ .Server.OperatorAddr }}", "10.0.0.1:8993"},
		{"{{ .Provider.Kind }}", "aws"},
		{"{{ .Provider.Region }}", "us-west-2"},
		{"{{ .Provider.Zone }}", "us-west-2a"},
		{"{{ .Image.debian_13_arm64 }}", "ami-test123"},
		{"{{ .Vars.ClusterSlug }}-registry", "test-cluster-registry"},
		{"{{ .Vars.ClusterFQDN }}", "test-cluster.cluster.cool"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result, err := ProcessTemplateString(tc.input, templateData)
			if err != nil {
				t.Fatalf("Failed to process template '%s': %v", tc.input, err)
			}
			if result != tc.expected {
				t.Errorf("Template '%s': expected '%s', got '%s'", tc.input, tc.expected, result)
			}
		})
	}
}

func TestCertificateTemplates(t *testing.T) {
	templates := certTemplates

	expectedTemplates := []string{
		"server-static-cn",
		"server-templated-cn",
		"server-multi-dns",
		"server-minimal",
		"client-static-cn",
		"client-templated-cn",
		"client-with-org",
		"client-minimal",
		"client-with-multi-org",
		"client-another-org",
	}

	for _, name := range expectedTemplates {
		if _, exists := templates[name]; !exists {
			t.Errorf("Expected template '%s' not found", name)
		}
	}

	clientWithOrg := templates["client-with-org"]
	if clientWithOrg.CN == nil || !strings.Contains(*clientWithOrg.CN, "client:node:") {
		t.Error("client-with-org should have client:node: CN pattern")
	}
	if len(clientWithOrg.Organization) != 1 || clientWithOrg.Organization[0] != "nodes" {
		t.Error("client-with-org should have nodes organization")
	}

	serverCerts := []string{"server-static-cn", "server-templated-cn", "server-multi-dns", "server-minimal"}
	for _, name := range serverCerts {
		if templates[name].Kind != "server" {
			t.Errorf("Template '%s' should be server type", name)
		}
	}

	clientCerts := []string{"client-static-cn", "client-templated-cn", "client-with-org", "client-minimal"}
	for _, name := range clientCerts {
		if templates[name].Kind != "client" {
			t.Errorf("Template '%s' should be client type", name)
		}
	}
}
