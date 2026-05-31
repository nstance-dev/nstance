// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/nstance-dev/nstance/internal/identifiers"
)

// Config holds the operator configuration loaded from ConfigMap and Secrets
type Config struct {
	// ShardEndpoints maps zone shard IDs to gRPC endpoints
	// Example: {"us-west-2a": "[2600::a]:8992"}
	ShardEndpoints map[string]string

	// TLSConfig for mTLS with nstance-server
	TLSConfig *tls.Config

	// PrivateKey is the operator's Ed25519 private key
	PrivateKey ed25519.PrivateKey

	// Namespace where the operator is running
	Namespace string
}

// Loader handles loading operator configuration from Kubernetes resources
type Loader struct {
	client    client.Client
	namespace string
}

// NewLoader creates a new config loader
func NewLoader(c client.Client, namespace string) *Loader {
	return &Loader{
		client:    c,
		namespace: namespace,
	}
}

// ShardEndpoints contains the endpoints for a single shard
type ShardEndpoints struct {
	RegistrationAddr string `json:"registration_addr" yaml:"registration_addr"`
	OperatorAddr     string `json:"operator_addr" yaml:"operator_addr"`
}

// OperatorConfig represents the structure of the configuration file
type OperatorConfig struct {
	ClusterID string                    `json:"cluster_id" yaml:"cluster_id"`
	Tenant    string                    `json:"tenant" yaml:"tenant"`
	Shards    map[string]ShardEndpoints `json:"shards" yaml:"shards"`
}

// CAPIClusterName returns the CAPI Cluster resource name for this operator,
// combining the cluster ID and tenant with a double-hyphen separator.
func (c *OperatorConfig) CAPIClusterName() string {
	return c.ClusterID + "--" + c.Tenant
}

// LoadConfigFromFile reads the operator configuration from a local file
func LoadConfigFromFile(path string) (*OperatorConfig, error) {
	configData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var config OperatorConfig
	if err := yaml.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse operator configuration: %w", err)
	}

	if len(config.Shards) == 0 {
		return nil, fmt.Errorf("no shards configured in config file")
	}

	if config.ClusterID == "" {
		return nil, fmt.Errorf("cluster_id is required in config file")
	}

	if config.Tenant == "" {
		return nil, fmt.Errorf("tenant is required in config file")
	}

	if err := identifiers.Validate("cluster ID", config.ClusterID); err != nil {
		return nil, fmt.Errorf("cluster_id: %w", err)
	}

	if err := identifiers.Validate("tenant ID", config.Tenant); err != nil {
		return nil, fmt.Errorf("tenant: %w", err)
	}

	// Validate shard endpoints
	for shard, endpoints := range config.Shards {
		if err := identifiers.Validate("shard ID", shard); err != nil {
			return nil, fmt.Errorf("shard %q: %w", shard, err)
		}
		if endpoints.RegistrationAddr == "" {
			return nil, fmt.Errorf("shard %s: registration_addr cannot be empty", shard)
		}
		if endpoints.OperatorAddr == "" {
			return nil, fmt.Errorf("shard %s: operator_addr cannot be empty", shard)
		}
	}

	return &config, nil
}

// LoadShardCA loads the cluster CA certificate from ConfigMap
func (l *Loader) LoadShardCA(ctx context.Context, configMapName string) ([]byte, error) {
	var cm corev1.ConfigMap
	if err := l.client.Get(ctx, types.NamespacedName{
		Name:      configMapName,
		Namespace: l.namespace,
	}, &cm); err != nil {
		return nil, fmt.Errorf("failed to get CA ConfigMap %s/%s: %w", l.namespace, configMapName, err)
	}

	caCert, ok := cm.Data["ca.crt"]
	if !ok || caCert == "" {
		return nil, fmt.Errorf("ConfigMap %s/%s missing 'ca.crt' key or empty", l.namespace, configMapName)
	}

	return []byte(caCert), nil
}

// LoadOrGenerateKeypair loads existing keypair from Secret or generates a new one
func (l *Loader) LoadOrGenerateKeypair(ctx context.Context, secretName string) (ed25519.PrivateKey, bool, error) {
	// Try to load existing keypair
	var secret corev1.Secret
	err := l.client.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: l.namespace,
	}, &secret)

	if err == nil {
		// Secret exists, try to load keypair
		privateKeyPEM, ok := secret.Data["private.key"]
		if !ok {
			return nil, false, fmt.Errorf("secret %s/%s missing 'private.key'", l.namespace, secretName)
		}

		privateKey, err := parsePrivateKey(privateKeyPEM)
		if err != nil {
			return nil, false, fmt.Errorf("failed to parse private key: %w", err)
		}

		return privateKey, false, nil
	}

	// Secret doesn't exist, generate new keypair
	if client.IgnoreNotFound(err) != nil {
		return nil, false, fmt.Errorf("failed to get Secret %s/%s: %w", l.namespace, secretName, err)
	}

	// Generate new Ed25519 keypair
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, false, fmt.Errorf("failed to generate Ed25519 keypair: %w", err)
	}

	// Marshal keys to PEM format
	privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, false, fmt.Errorf("failed to marshal private key: %w", err)
	}

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	publicKeyBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, false, fmt.Errorf("failed to marshal public key: %w", err)
	}

	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	})

	// Store in Secret
	newSecret := &corev1.Secret{
		ObjectMeta: secret.ObjectMeta,
		Data: map[string][]byte{
			"private.key": privateKeyPEM,
			"public.key":  publicKeyPEM,
		},
	}
	newSecret.Name = secretName
	newSecret.Namespace = l.namespace

	if err := l.client.Create(ctx, newSecret); err != nil {
		return nil, false, fmt.Errorf("failed to create keypair Secret: %w", err)
	}

	return privateKey, true, nil
}

// LoadCertificate loads the client certificate from Secret and builds TLS config with CA verification
func (l *Loader) LoadCertificate(ctx context.Context, secretName string, caCert []byte) (*tls.Config, ed25519.PrivateKey, error) {
	var secret corev1.Secret
	if err := l.client.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: l.namespace,
	}, &secret); err != nil {
		return nil, nil, err
	}

	certPEM, ok := secret.Data["tls.crt"]
	if !ok {
		return nil, nil, fmt.Errorf("secret %s/%s missing 'tls.crt'", l.namespace, secretName)
	}

	keyPEM, ok := secret.Data["tls.key"]
	if !ok {
		return nil, nil, fmt.Errorf("secret %s/%s missing 'tls.key'", l.namespace, secretName)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load X509 key pair: %w", err)
	}

	privateKey, err := parsePrivateKey(keyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	// Add CA verification if CA cert is provided
	if len(caCert) > 0 {
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, nil, fmt.Errorf("failed to add CA certificate to pool")
		}
		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, privateKey, nil
}

// BuildTLSConfig builds a TLS config from certificate and key PEM data with CA verification
func BuildTLSConfig(certPEM, keyPEM, caCertPEM []byte) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to load X509 key pair: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	if len(caCertPEM) > 0 {
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCertPEM) {
			return nil, fmt.Errorf("failed to add CA certificate to pool")
		}
		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}

// LoadNonce loads the registration nonce JWT from Secret
func (l *Loader) LoadNonce(ctx context.Context, secretName string) (string, error) {
	var secret corev1.Secret
	if err := l.client.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: l.namespace,
	}, &secret); err != nil {
		return "", fmt.Errorf("failed to get nonce Secret %s/%s: %w", l.namespace, secretName, err)
	}

	nonce, ok := secret.Data["nonce.jwt"]
	if !ok {
		return "", fmt.Errorf("secret %s/%s missing 'nonce.jwt'", l.namespace, secretName)
	}

	return string(nonce), nil
}

// StoreCertificate stores the client certificate in Secret
func (l *Loader) StoreCertificate(ctx context.Context, secretName string, certPEM, keyPEM []byte) error {
	var secret corev1.Secret
	err := l.client.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: l.namespace,
	}, &secret)

	secret.Data = map[string][]byte{
		"tls.crt": certPEM,
		"tls.key": keyPEM,
	}

	if err != nil {
		// Secret doesn't exist, create it
		if client.IgnoreNotFound(err) == nil {
			secret.Name = secretName
			secret.Namespace = l.namespace
			return l.client.Create(ctx, &secret)
		}
		return err
	}

	// Secret exists, update it
	return l.client.Update(ctx, &secret)
}

// parsePrivateKey parses PEM-encoded Ed25519 private key
func parsePrivateKey(pemBytes []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	// Parse PKCS8 format (standard for Ed25519)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an Ed25519 private key")
	}

	return privateKey, nil
}

// GetEnv gets environment variable with fallback
func GetEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
