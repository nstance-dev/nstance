// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package leader

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	infrastructurev1beta1 "github.com/nstance-dev/nstance/api/v1beta1"
	"github.com/nstance-dev/nstance/internal/operator/config"
	"github.com/nstance-dev/nstance/internal/operator/connection"
	"github.com/nstance-dev/nstance/internal/operator/drain"
	"github.com/nstance-dev/nstance/internal/operator/sync"
	"github.com/nstance-dev/nstance/internal/renewal"
	"github.com/nstance-dev/nstance/pkg/client/registration"
)

const (
	componentName             = "nstance-operator"
	kubeconfigRefreshInterval = 5 * time.Minute
)

// Manager handles operator leader runtime: registration, connections, and subsystem orchestration
type Manager struct {
	client        client.Client
	mgr           ctrl.Manager
	connMgr       *connection.Manager
	connProvider  *connection.Provider
	syncMgr       *sync.Manager
	onSyncReady   func(*sync.Manager)
	onClusterName func(string)
	drainCoord    *drain.Coordinator
	namespace     string
	clusterName   string
	configPath    string

	// Certificate renewal state
	currentCert *x509.Certificate
	privateKey  ed25519.PrivateKey
	loader      *config.Loader
}

// New creates a new runtime manager
func New(c client.Client, mgr ctrl.Manager, configPath string, connProvider *connection.Provider, onSyncReady func(*sync.Manager), onClusterName func(string)) *Manager {
	return &Manager{
		client:        c,
		mgr:           mgr,
		configPath:    configPath,
		connProvider:  connProvider,
		onSyncReady:   onSyncReady,
		onClusterName: onClusterName,
	}
}

// Start is called when the manager starts and after leader election
func (m *Manager) Start(ctx context.Context) error {
	logger := ctrl.Log.WithName("leader")
	logger.Info("operator leader runtime starting")

	// Get namespace from env or default to current namespace
	m.namespace = config.GetEnv("NSTANCE_NAMESPACE", getCurrentNamespace())

	loader := config.NewLoader(m.client, m.namespace)

	// Step 1: Load configuration from file
	logger.Info("loading operator configuration", "path", m.configPath)

	opConfig, err := config.LoadConfigFromFile(m.configPath)
	if err != nil {
		logger.Error(err, "FATAL: failed to load operator configuration")
		return fmt.Errorf("failed to load operator configuration: %w", err)
	}

	shardConfigs := opConfig.Shards
	m.clusterName = opConfig.CAPIClusterName()
	if m.onClusterName != nil {
		m.onClusterName(m.clusterName)
	}

	logger.Info("loaded and validated shard endpoints", "shards", len(shardConfigs))

	// Build operator endpoints map for connections
	operatorEndpoints := make(map[string]string, len(shardConfigs))
	for shard, endpoints := range shardConfigs {
		operatorEndpoints[shard] = endpoints.OperatorAddr
	}

	// Step 1.5: Load cluster CA certificate for TLS verification
	caConfigMapName := config.GetEnv("NSTANCE_CA_CONFIGMAP", "nstance-cluster-ca")
	logger.Info("loading cluster CA certificate", "configMap", caConfigMapName)
	caCert, err := loader.LoadShardCA(ctx, caConfigMapName)
	if err != nil {
		logger.Error(err, "FATAL: failed to load cluster CA certificate")
		return fmt.Errorf("failed to load cluster CA: %w", err)
	}
	logger.Info("loaded cluster CA certificate")

	// Step 2: Check for existing client certificate
	certSecretName := config.GetEnv("NSTANCE_CERT_SECRET", "nstance-operator-cert")
	logger.Info("checking for existing certificate", "secret", certSecretName)
	tlsConfig, privateKey, err := loader.LoadCertificate(ctx, certSecretName, caCert)
	if err == nil {
		// Extract the x509 certificate from the TLS config for renewal logic
		if len(tlsConfig.Certificates) > 0 && len(tlsConfig.Certificates[0].Certificate) > 0 {
			cert, err := x509.ParseCertificate(tlsConfig.Certificates[0].Certificate[0])
			if err != nil {
				logger.Error(err, "failed to parse certificate for renewal")
			} else {
				m.currentCert = cert
				m.privateKey = privateKey
				m.loader = loader
			}
		}
		// return early/skip registration and connect to shards
		logger.Info("loaded existing certificate, skipping registration")
		return m.startServices(ctx, operatorEndpoints, tlsConfig)
	}

	if client.IgnoreNotFound(err) != nil {
		logger.Error(err, "failed to load certificate Secret")
		return err
	}

	logger.Info("no certificate found, checking for keypair")

	// Step 3: Load or generate keypair
	keySecretName := config.GetEnv("NSTANCE_KEY_SECRET", "nstance-operator-key")
	logger.Info("loading or generating keypair", "secret", keySecretName)

	privateKey, generated, err := loader.LoadOrGenerateKeypair(ctx, keySecretName)
	if err != nil {
		logger.Error(err, "failed to load or generate keypair")
		return err
	}

	if generated {
		logger.Info("generated new keypair and stored in Secret")
	} else {
		logger.Info("loaded existing keypair from Secret")
	}

	// Step 4: Register with server
	nonceSecretName := config.GetEnv("NSTANCE_NONCE_SECRET", "nstance-operator-nonce")
	logger.Info("loading registration nonce", "secret", nonceSecretName)
	nonce, err := loader.LoadNonce(ctx, nonceSecretName)
	if err != nil {
		logger.Error(err, "FATAL: registration nonce not found - configuration error")
		return fmt.Errorf("failed to load registration nonce: %w", err)
	}
	logger.Info("registering with nstance-server")

	// Ed25519 public key is derived from private key
	publicKey := privateKey.Public().(ed25519.PublicKey)

	// Parse CA certificate for registration
	block, _ := pem.Decode(caCert)
	if block == nil {
		logger.Error(nil, "FATAL: failed to decode CA certificate PEM")
		return fmt.Errorf("failed to decode CA certificate PEM")
	}
	parsedCACert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		logger.Error(err, "FATAL: failed to parse CA certificate")
		return fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	// Try registration on each shard until one succeeds
	var certPEM []byte
	var lastErr error
	for shard, endpoints := range shardConfigs {
		regEndpoint := endpoints.RegistrationAddr
		cfg := registration.Config{
			ServerAddress: regEndpoint,
			ServerCACert:  *parsedCACert,
		}

		client, err := registration.NewClient(cfg)
		if err != nil {
			logger.Error(err, "failed to create registration client", "shard", shard, "endpoint", regEndpoint)
			lastErr = fmt.Errorf("shard %s (%s) client creation failed: %w", shard, regEndpoint, err)
			continue
		}

		certPEM, err = func() ([]byte, error) {
			defer func() {
				if closeErr := client.Close(); closeErr != nil {
					logger.Error(closeErr, "error closing registration client", "shard", shard)
				}
			}()
			return client.RegisterOperator(ctx, nonce, publicKey)
		}()

		if err == nil {
			logger.Info("registration successful", "shard", shard)
			break
		}

		lastErr = fmt.Errorf("shard %s (%s): %w", shard, regEndpoint, err)
		logger.Error(err, "registration failed on shard", "shard", shard, "endpoint", regEndpoint)
	}

	if certPEM == nil {
		logger.Error(lastErr, "FATAL: registration failed on all shards")
		return fmt.Errorf("registration failed on all shards: %w", lastErr)
	}

	logger.Info("storing certificate")

	// Step 5: Store certificate with our private key
	privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		logger.Error(err, "failed to marshal private key")
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	if err := loader.StoreCertificate(ctx, certSecretName, certPEM, privateKeyPEM); err != nil {
		logger.Error(err, "failed to store certificate")
		return err
	}

	logger.Info("certificate stored successfully")

	// Build TLS config directly from the certificate we just received
	tlsConfig, err = config.BuildTLSConfig(certPEM, privateKeyPEM, caCert)
	if err != nil {
		logger.Error(err, "failed to build TLS config")
		return err
	}

	// Parse the newly received certificate for renewal logic
	certBlock, _ := pem.Decode(certPEM)
	if certBlock != nil {
		cert, certErr := x509.ParseCertificate(certBlock.Bytes)
		if certErr != nil {
			logger.Error(certErr, "failed to parse new certificate for renewal")
		} else {
			m.currentCert = cert
			m.privateKey = privateKey
			m.loader = loader
		}
	}

	// Step 6: Start services and run until leadership lost
	return m.startServices(ctx, operatorEndpoints, tlsConfig)
}

// NeedLeaderElection implements LeaderElectionRunnable
func (m *Manager) NeedLeaderElection() bool {
	return true
}

// startServices establishes connections and starts all runtime subsystems
// It blocks until the context is canceled (leadership lost), then exits the process
func (m *Manager) startServices(ctx context.Context, operatorEndpoints map[string]string, tlsConfig *tls.Config) error {
	logger := ctrl.Log.WithName("leader")
	logger.Info("establishing gRPC connections to all shards")

	m.connMgr = connection.NewManager(tlsConfig, logger.WithName("connection"))

	if err := m.connMgr.Connect(ctx, operatorEndpoints); err != nil {
		logger.Error(err, "failed to connect to shards")
		return err
	}

	logger.Info("connected to all shards", "count", len(operatorEndpoints))

	// Ensure the CAPI Cluster resource exists for this operator's cluster+tenant
	if err := m.ensureCAPICluster(ctx, logger); err != nil {
		logger.Error(err, "failed to ensure CAPI Cluster resource")
		return err
	}
	if config.GetEnv("NSTANCE_CAPI_ENDPOINT", "") == "" {
		go func() {
			ticker := time.NewTicker(kubeconfigRefreshInterval)
			defer ticker.Stop()
			m.refreshKubeconfig(ctx, logger.WithName("kubeconfig-refresh"), ticker.C)
		}()
	}

	// Step 7: Start sync manager for bidirectional sync
	syncInterval := 30 * time.Second
	recorder := m.mgr.GetEventRecorderFor(componentName)
	m.syncMgr = sync.NewManager(m.client, logger.WithName("sync"), recorder, syncInterval, m.namespace, m.clusterName)

	// Wire up sync manager to controller
	if m.onSyncReady != nil {
		m.onSyncReady(m.syncMgr)
	}

	go func() {
		if err := m.syncMgr.Start(ctx, m.connMgr.GetAllConnections()); err != nil && err != context.Canceled {
			logger.Error(err, "sync manager stopped unexpectedly")
		}
	}()

	logger.Info("sync manager started", "interval", syncInterval)

	// Step 8: Populate connection provider so controllers can use the connections
	// Controllers are already set up in main.go before mgr.Start() to ensure informers work
	m.connProvider.Set(m.connMgr.GetAllConnections())
	logger.Info("connections available to controllers")

	// Step 9: Start drain coordination
	drainCoord, err := drain.NewCoordinator(m.client, logger.WithName("drain"), m.connMgr.GetAllConnections(), m.mgr.GetConfig())
	if err != nil {
		logger.Error(err, "failed to create drain coordinator")
		return err
	}

	m.drainCoord = drainCoord

	if err := m.drainCoord.Start(ctx); err != nil {
		logger.Error(err, "failed to start drain coordinator")
		return err
	}

	logger.Info("drain coordinator started")

	// Step 10: Start certificate renewal goroutine (if we have certificate info)
	if m.currentCert != nil && m.privateKey != nil && m.loader != nil {
		renewalLogger := slog.New(logr.ToSlogHandler(logger.WithName("cert-renewal")))
		renewal.Start(ctx, renewal.Config{
			CurrentCert: m.currentCert,
			PrivateKey:  m.privateKey,
			Store:       &operatorCertStore{loader: m.loader, privateKey: m.privateKey},
			Connections: m.connMgr,
			Logger:      renewalLogger,
		})
	} else {
		logger.Info("certificate renewal disabled: missing certificate info")
	}

	logger.Info("leader runtime fully started, blocking until leadership lost")
	<-ctx.Done()

	logger.Info("leadership lost, exiting to allow restart")
	os.Exit(1)
	return nil
}

// operatorCertStore adapts the operator's loader to renewal.CertificateStore.
type operatorCertStore struct {
	loader     *config.Loader
	privateKey ed25519.PrivateKey
}

func (s *operatorCertStore) StoreCertificate(ctx context.Context, certPEM []byte) error {
	privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(s.privateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	certSecretName := config.GetEnv("NSTANCE_CERT_SECRET", "nstance-operator-cert")
	return s.loader.StoreCertificate(ctx, certSecretName, certPEM, privateKeyPEM)
}

// ensureCAPICluster ensures a CAPI Cluster resource, its NstanceCluster
// infrastructure ref, and a kubeconfig secret exist for this operator's
// cluster+tenant. The Cluster is required by CAPI for MachinePool ownership.
//
// When NSTANCE_CAPI_ENDPOINT is not set (self-managed cluster), the
// controlPlaneEndpoint is derived from the in-cluster API server and the
// kubeconfig secret is auto-managed with short-lived SA tokens.
//
// When NSTANCE_CAPI_ENDPOINT is set (external cluster), the provided endpoint
// is used for the controlPlaneEndpoint and the kubeconfig secret is expected
// to be pre-provisioned by the administrator.
func (m *Manager) ensureCAPICluster(ctx context.Context, logger logr.Logger) error {
	// Determine control plane endpoint and kubeconfig management mode
	externalEndpoint := config.GetEnv("NSTANCE_CAPI_ENDPOINT", "")
	selfManaged := externalEndpoint == ""

	var host string
	var port int32
	var err error
	if selfManaged {
		host, port, err = parseAPIServerEndpoint(m.mgr.GetConfig().Host)
	} else {
		host, port, err = parseAPIServerEndpoint(externalEndpoint)
	}
	if err != nil {
		return fmt.Errorf("failed to parse API server endpoint: %w", err)
	}

	// Ensure the NstanceCluster stub exists (the controller will mark it ready)
	var nstanceCluster infrastructurev1beta1.NstanceCluster
	nstanceClusterKey := client.ObjectKey{Namespace: m.namespace, Name: m.clusterName}
	if err := m.client.Get(ctx, nstanceClusterKey, &nstanceCluster); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to check for NstanceCluster: %w", err)
		}
		nstanceCluster = infrastructurev1beta1.NstanceCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      m.clusterName,
				Namespace: m.namespace,
			},
			Spec: infrastructurev1beta1.NstanceClusterSpec{
				ControlPlaneEndpoint: infrastructurev1beta1.APIEndpoint{
					Host: host,
					Port: port,
				},
			},
		}
		if err := m.client.Create(ctx, &nstanceCluster); err != nil {
			return fmt.Errorf("failed to create NstanceCluster: %w", err)
		}
		logger.Info("created NstanceCluster resource", "name", m.clusterName, "endpoint", fmt.Sprintf("%s:%d", host, port))
	}

	// Ensure the CAPI Cluster exists referencing the NstanceCluster
	var cluster clusterv1.Cluster
	key := client.ObjectKey{Namespace: m.namespace, Name: m.clusterName}
	if err := m.client.Get(ctx, key, &cluster); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to check for CAPI Cluster: %w", err)
		}
		cluster = clusterv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      m.clusterName,
				Namespace: m.namespace,
			},
			Spec: clusterv1.ClusterSpec{
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: infrastructurev1beta1.GroupVersion.Group,
					Kind:     "NstanceCluster",
					Name:     m.clusterName,
				},
			},
		}
		if err := m.client.Create(ctx, &cluster); err != nil {
			return fmt.Errorf("failed to create CAPI Cluster: %w", err)
		}
		logger.Info("created CAPI Cluster resource", "name", m.clusterName)
	}

	// For self-managed clusters, auto-manage the kubeconfig secret.
	// For external clusters, the admin must pre-provision the secret.
	if selfManaged {
		if err := m.ensureKubeconfigSecret(ctx, logger); err != nil {
			return fmt.Errorf("failed to ensure kubeconfig secret: %w", err)
		}
	} else {
		logger.Info("external cluster endpoint configured, skipping kubeconfig secret management", "endpoint", externalEndpoint)
	}

	return nil
}

// ensureKubeconfigSecret creates or refreshes the <cluster>-kubeconfig secret
// that CAPI's ClusterCache uses to connect to the "workload" cluster. We point
// it at the management cluster's API server using a short-lived token from a
// dedicated minimal-privilege ServiceAccount (nstance-capi-workload).
func (m *Manager) ensureKubeconfigSecret(ctx context.Context, logger logr.Logger) error {
	secretName := m.clusterName + "-kubeconfig"
	key := client.ObjectKey{Namespace: m.namespace, Name: secretName}

	var existing corev1.Secret
	exists := false
	if err := m.client.Get(ctx, key, &existing); err == nil {
		exists = true
		// Check if the token is near expiry (within 10 minutes)
		if expiryStr, ok := existing.Annotations["nstance.dev/token-expiry"]; ok {
			expiry, err := time.Parse(time.RFC3339, expiryStr)
			if err == nil && time.Until(expiry) > 10*time.Minute {
				return nil // token still valid
			}
		}
		logger.Info("kubeconfig token expired or missing expiry, refreshing", "secret", secretName)
	} else if client.IgnoreNotFound(err) != nil {
		return err
	}

	// Request a short-lived token from the dedicated CAPI ServiceAccount
	saName := config.GetEnv("NSTANCE_CAPI_SERVICEACCOUNT", "nstance-capi-workload")
	clientset, err := kubernetes.NewForConfig(m.mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	expirationSeconds := int64(3600) // 1 hour
	tokenResponse, err := clientset.CoreV1().ServiceAccounts(m.namespace).CreateToken(
		ctx, saName,
		&authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				ExpirationSeconds: &expirationSeconds,
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to create token for SA %s: %w", saName, err)
	}

	// Get the cluster CA data for the kubeconfig
	caData := m.mgr.GetConfig().CAData
	if len(caData) == 0 && m.mgr.GetConfig().CAFile != "" {
		caData, err = os.ReadFile(m.mgr.GetConfig().CAFile)
		if err != nil {
			return fmt.Errorf("reading CA file: %w", err)
		}
	}
	if len(caData) == 0 {
		caData, _ = os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	}

	// Build kubeconfig pointing at the in-cluster API server address
	kubeconfig := clientcmdapi.NewConfig()
	kubeconfig.Clusters[m.clusterName] = &clientcmdapi.Cluster{
		Server:                   "https://kubernetes.default.svc:443",
		CertificateAuthorityData: caData,
	}
	kubeconfig.AuthInfos[m.clusterName] = &clientcmdapi.AuthInfo{
		Token: tokenResponse.Status.Token,
	}
	kubeconfig.Contexts[m.clusterName] = &clientcmdapi.Context{
		Cluster:  m.clusterName,
		AuthInfo: m.clusterName,
	}
	kubeconfig.CurrentContext = m.clusterName

	kubeconfigData, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to serialize kubeconfig: %w", err)
	}

	if exists {
		existing.Data = map[string][]byte{"value": kubeconfigData}
		if existing.Labels == nil {
			existing.Labels = make(map[string]string)
		}
		existing.Labels[clusterv1.ClusterNameLabel] = m.clusterName
		if existing.Annotations == nil {
			existing.Annotations = make(map[string]string)
		}
		existing.Annotations["nstance.dev/token-expiry"] = tokenResponse.Status.ExpirationTimestamp.Format(time.RFC3339)
		if err := m.client.Update(ctx, &existing); err != nil {
			return fmt.Errorf("failed to update kubeconfig secret: %w", err)
		}
		logger.Info("refreshed kubeconfig secret for CAPI cluster", "secret", secretName)
	} else {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: m.namespace,
				Labels: map[string]string{
					clusterv1.ClusterNameLabel: m.clusterName,
				},
				Annotations: map[string]string{
					"nstance.dev/token-expiry": tokenResponse.Status.ExpirationTimestamp.Format(time.RFC3339),
				},
			},
			Data: map[string][]byte{"value": kubeconfigData},
		}
		if err := m.client.Create(ctx, secret); err != nil {
			return fmt.Errorf("failed to create kubeconfig secret: %w", err)
		}
		logger.Info("created kubeconfig secret for CAPI cluster", "secret", secretName)
	}

	return nil
}

// refreshKubeconfig periodically refreshes the self-managed CAPI kubeconfig until the context is canceled.
func (m *Manager) refreshKubeconfig(ctx context.Context, logger logr.Logger, ticks <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			if err := m.ensureKubeconfigSecret(ctx, logger); err != nil {
				logger.Error(err, "failed to refresh CAPI kubeconfig")
			}
		}
	}
}

// parseAPIServerEndpoint extracts the host and port from a REST config Host
// field, which may be a URL like "https://host:port" or just "host:port".
func parseAPIServerEndpoint(hostURL string) (string, int32, error) {
	if !strings.Contains(hostURL, "://") {
		hostURL = "https://" + hostURL
	}
	u, err := url.Parse(hostURL)
	if err != nil {
		return "", 0, fmt.Errorf("invalid API server URL %q: %w", hostURL, err)
	}
	hostname := u.Hostname()
	portStr := u.Port()
	if portStr == "" {
		if u.Scheme == "https" {
			return hostname, 443, nil
		}
		return hostname, 80, nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	return hostname, int32(port), nil
}

// getCurrentNamespace returns the namespace the operator is running in
func getCurrentNamespace() string {
	if ns, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return string(ns)
	}
	return "default"
}
