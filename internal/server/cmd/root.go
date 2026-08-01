// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nstance-dev/nstance/internal/buildvars"
	"github.com/nstance-dev/nstance/internal/proto"
	"github.com/nstance-dev/nstance/internal/server/api"
	"github.com/nstance-dev/nstance/internal/server/api/agent"
	"github.com/nstance-dev/nstance/internal/server/api/operator"
	"github.com/nstance-dev/nstance/internal/server/api/registration"
	"github.com/nstance-dev/nstance/internal/server/cluster"
	"github.com/nstance-dev/nstance/internal/server/config"
	"github.com/nstance-dev/nstance/internal/server/election"
	"github.com/nstance-dev/nstance/internal/server/gc"
	"github.com/nstance-dev/nstance/internal/server/health"
	"github.com/nstance-dev/nstance/internal/server/images"
	"github.com/nstance-dev/nstance/internal/server/infra"
	"github.com/nstance-dev/nstance/internal/server/infra/provider"
	"github.com/nstance-dev/nstance/internal/server/instances"
	"github.com/nstance-dev/nstance/internal/server/localdb"
	"github.com/nstance-dev/nstance/internal/server/pki"
	"github.com/nstance-dev/nstance/internal/server/reconciler"
	"github.com/nstance-dev/nstance/internal/server/secrets"
	"github.com/nstance-dev/nstance/internal/server/storage"
	"github.com/nstance-dev/nstance/internal/server/tenantstate"
	"github.com/nstance-dev/nstance/pkg/instanceinfo"
)

var rootCmd = &cobra.Command{
	Use:   "nstance-server",
	Short: "Nstance Server",
	Long:  `nstance-server is run per-zone shard for same-shard agents to connect to`,
}

const validateRemote = ":remote:"

var (
	flagDebug         bool
	flagVersion       bool
	flagServerID      string
	flagShard         string
	flagStorage       string
	flagBucket        string
	flagPrefix        string
	flagValidate      string
	flagCacheDir      string
	flagAdvertiseHost string
)

func init() {
	pflags := rootCmd.PersistentFlags()
	pflags.BoolVarP(&flagDebug, "debug", "v", false, "Enable debug output")
	pflags.BoolVar(&flagVersion, "version", false, "Show version information")
	if flag := pflags.Lookup("debug"); flag != nil {
		flag.NoOptDefVal = "true"
	}

	lflags := rootCmd.Flags()
	lflags.StringVar(&flagServerID, "id", "", "Server unique identifier")
	lflags.StringVar(&flagShard, "shard", "", "Server zone shard identifier")
	lflags.StringVar(&flagStorage, "storage", "", "Storage provider: s3, gcs, or file")
	lflags.StringVar(&flagBucket, "bucket", "", "Object storage bucket for config and state")
	lflags.StringVar(&flagPrefix, "prefix", "", "Key prefix for shard data (default: shard/{shard}/)")
	lflags.StringVar(&flagValidate, "validate", "", "Validate configuration file and exit (no value: fetch shard config file from object storage and validate)")
	if flag := lflags.Lookup("validate"); flag != nil {
		flag.NoOptDefVal = validateRemote
	}
	lflags.StringVar(&flagCacheDir, "cachedir", "./cache", "Directory for cache and database files")
	lflags.StringVar(&flagAdvertiseHost, "advertise-host", "", "Override advertise host for health and election addrs (per-instance)")
}

func NewRootCmd() *cobra.Command {
	rootCmd.PreRun = func(cmd *cobra.Command, args []string) {
		validateFile, _ := cmd.Flags().GetString("validate")
		version, _ := cmd.Flags().GetBool("version")
		if validateFile != "" && validateFile != validateRemote {
			// Local file validation — no storage flags needed
		} else if validateFile == validateRemote {
			// Remote validation — need storage flags but not server ID
			_ = rootCmd.MarkFlagRequired("shard")
			_ = rootCmd.MarkFlagRequired("storage")
			_ = rootCmd.MarkFlagRequired("bucket")
		} else if !version {
			// Normal startup — require all flags
			_ = rootCmd.MarkFlagRequired("id")
			_ = rootCmd.MarkFlagRequired("shard")
			_ = rootCmd.MarkFlagRequired("storage")
			_ = rootCmd.MarkFlagRequired("bucket")
		}
	}
	rootCmd.Run = func(cmd *cobra.Command, args []string) {
		// print version if requested
		if flagVersion {
			fmt.Printf("nstance-server %s\n", buildvars.BuildVersion())
			if flagDebug {
				fmt.Printf("build version: %s\n", buildvars.BuildVersion())
				fmt.Printf("build date: %s\n", buildvars.BuildDate())
				fmt.Printf("commit hash: %s\n", buildvars.CommitHash())
				fmt.Printf("commit date: %s\n", buildvars.CommitDate())
				fmt.Printf("commit branch: %s\n", buildvars.CommitBranch())
			}
			return
		}

		// validate config file if requested
		if flagValidate != "" && flagValidate != validateRemote {
			config, err := config.ValidateFile(flagValidate)
			if err != nil {
				fmt.Fprintf(os.Stderr, "✗ Configuration validation failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✓ Configuration is valid")

			// Print detailed info in debug mode
			if flagDebug {
				if configJSON, err := json.MarshalIndent(config, "", "  "); err == nil {
					fmt.Println(string(configJSON))
				} else {
					fmt.Fprintf(os.Stderr, "✗ Failed to marshal config for debug output: %v\n", err)
				}
			}

			return
		}

		// validate remote shard config if --validate with no value
		if flagValidate == validateRemote {
			ctx := context.Background()
			logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
			if flagDebug {
				logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
			}

			baseStorage, baseStorageCleanup, err := storage.New(ctx, logger, flagStorage, flagBucket)
			if err != nil {
				fmt.Fprintf(os.Stderr, "✗ Failed to create storage client: %v\n", err)
				os.Exit(1)
			}
			defer baseStorageCleanup()

			shardPrefix := flagPrefix
			if shardPrefix == "" {
				shardPrefix = fmt.Sprintf("shard/%s/", flagShard)
			}
			shardStorage := storage.NewScopedStorage(baseStorage, shardPrefix)

			data, _, err := shardStorage.Get(ctx, "config.jsonc")
			if err != nil {
				fmt.Fprintf(os.Stderr, "✗ Failed to fetch remote config from %s%s: %v\n", shardPrefix, "config.jsonc", err)
				os.Exit(1)
			}

			config, err := config.ParseBytes(data)
			if err != nil {
				fmt.Fprintf(os.Stderr, "✗ Remote configuration validation failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("✓ Remote shard configuration is valid")

			if flagDebug {
				if configJSON, err := json.MarshalIndent(config, "", "  "); err == nil {
					fmt.Println(string(configJSON))
				} else {
					fmt.Fprintf(os.Stderr, "✗ Failed to marshal config for debug output: %v\n", err)
				}
			}

			return
		}

		// create context that cancels on OS interrupt/termination signals
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		// create structured logger
		logLevel := slog.LevelInfo
		if flagDebug {
			logLevel = slog.LevelDebug
		}
		logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: logLevel,
		}))

		logger.Info("Starting Nstance Server",
			"shard", flagShard,
			"storage", flagStorage,
			"bucket", flagBucket,
			"version", buildvars.BuildVersion())

		// print flags and environment variables in debug mode
		if flagDebug {
			logger.Debug("Command line flags",
				"debug", flagDebug,
				"version", flagVersion,
				"shard", flagShard,
				"storage", flagStorage,
				"bucket", flagBucket,
				"prefix", flagPrefix,
				"cachedir", flagCacheDir,
				"advertise_host", flagAdvertiseHost)

			logger.Debug("Environment variables",
				"AWS_REGION", os.Getenv("AWS_REGION"),
				"AWS_PROFILE", os.Getenv("AWS_PROFILE"),
				"GOOGLE_PROJECT", os.Getenv("GOOGLE_PROJECT"))
		}

		// create base storage client from --storage and --bucket CLI flags
		baseStorage, baseStorageCleanup, err := storage.New(ctx, logger, flagStorage, flagBucket)
		if err != nil {
			logger.Error("Failed to create main storage", "error", err)
			os.Exit(1)
		}
		defer baseStorageCleanup()

		// create base cache storage client for local filesystem cache
		baseCacheStorage, err := storage.NewFileStorage(flagCacheDir)
		if err != nil {
			logger.Error("Failed to create cache storage", "error", err)
			os.Exit(1)
		}

		// create shard storage and shard cache storage clients
		shardPrefix := flagPrefix
		if shardPrefix == "" {
			shardPrefix = fmt.Sprintf("shard/%s/", flagShard)
		}
		shardStorage := storage.NewScopedStorage(baseStorage, shardPrefix)
		shardCacheStorage := storage.NewScopedStorage(baseCacheStorage, shardPrefix)

		// create and connect to local database (so it's ready to pass to config load as a callback)
		dbPath := filepath.Join(flagCacheDir, "db", "nstance.db")
		localDB, err := localdb.Open(dbPath)
		if err != nil {
			logger.Error("Failed to open local database", "error", err)
			os.Exit(1)
		}
		defer func() {
			err := localDB.Close()
			if err != nil {
				logger.Error("Failed to close local database", "error", err)
			}
		}()

		// create config loader - config is at config.jsonc within shard-scoped storage
		configLoader, err := config.NewLoader(config.LoaderOptions{
			Storage:       shardStorage,
			CacheStorage:  shardCacheStorage,
			LocalDB:       localDB,
			Logger:        logger,
			AdvertiseHost: flagAdvertiseHost,
		})
		if err != nil {
			logger.Error("Failed to create config loader", "error", err)
			os.Exit(1)
		}

		// validate and load config and groups, and sync to SQLite atomically, with retry
		var cfg *config.Config
		var loadErr error
		for attempt := 1; attempt <= 3; attempt++ {
			// Load configuration, dynamic groups, and sync to SQLite
			cfg, loadErr = configLoader.LoadConfigAndGroups(ctx, false)
			if loadErr != nil {
				logger.Warn("Failed to load configuration, retrying...",
					"attempt", attempt,
					"error", loadErr)
				if attempt < 3 {
					time.Sleep(time.Duration(1<<(attempt-1)) * time.Second)
				}
				continue
			}
			// All steps succeeded
			loadErr = nil
			break
		}
		// if still failing after 3 attempts (2 retries), hang indefinitely
		if loadErr != nil {
			logger.Error("FATAL: Failed to load configuration and sync to cache after 2 retries.",
				"error", loadErr,
				"action", "Server will hang to prevent restart loop. Manual intervention required.")
			// Hang indefinitely - don't exit to prevent restart loop burning S3 reads
			select {}
		}
		logger.Info("Configuration loaded successfully", "cluster_id", cfg.Cluster.ID)

		// create cluster storage - either from specific config, or same base storage as shard storage - and use cluster/ prefix
		var clusterStorage storage.Storage
		if cfg.Cluster.Storage != nil && cfg.Cluster.Storage.Bucket != "" {
			// Separate cluster bucket configured (supports S3-compatible backends)
			var clusterCleanup func()
			clusterRawStorage, clusterCleanup, err := storage.NewWithOptions(ctx, logger, storage.StorageOptions{
				Provider: cfg.Cluster.Storage.Provider,
				Bucket:   cfg.Cluster.Storage.Bucket,
				Region:   cfg.Cluster.Storage.Region,
				Endpoint: cfg.Cluster.Storage.Endpoint,
			})
			if err != nil {
				logger.Error("Failed to create cluster storage", "error", err)
				os.Exit(1)
			}
			defer clusterCleanup()
			clusterStorage = storage.NewScopedStorage(clusterRawStorage, cfg.Cluster.Storage.Prefix)
		} else {
			// Use shard bucket with cluster/ prefix
			clusterStorage = storage.NewScopedStorage(baseStorage, "cluster/")
		}

		// start HTTP health server (returns 503 until SetReady called)
		// note that we cannot do this earlier because the port to use is defined in the server config
		httpHealthServer, err := health.NewServer(health.Config{
			BindAddr: cfg.Shard.Bind.HealthAddr,
			Logger:   logger,
		})
		if err != nil {
			logger.Error("Failed to create HTTP health server", "error", err)
			os.Exit(1)
		}
		if err := httpHealthServer.Start(ctx); err != nil {
			logger.Error("Failed to start HTTP health server", "error", err)
			os.Exit(1)
		}
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), cfg.Shard.ShutdownTimeout.Duration())
			defer cancel()
			if err := httpHealthServer.Stop(stopCtx); err != nil {
				logger.Error("Failed to stop HTTP health server", "error", err)
			}
		}()
		logger.Info("HTTP health server started (not ready yet)", "addr", cfg.Shard.Bind.HealthAddr)

		// mark health server as ready - config loaded and synced to SQLite
		httpHealthServer.SetReady()
		logger.Info("HTTP health endpoint ready (returning 200 OK)")

		// print full config in debug mode
		if flagDebug {
			if configJSON, err := json.MarshalIndent(cfg, "", "  "); err == nil {
				fmt.Println("Full configuration loaded:")
				fmt.Println(string(configJSON))
			}
		}

		// create secrets store using cluster storage
		storeOpts := secrets.StoreOptions{
			Provider:  cfg.Cluster.Secrets.Provider,
			Prefix:    cfg.Cluster.Secrets.Prefix,
			CacheTTL:  cfg.Cluster.Secrets.CacheTTL.Duration(),
			ProjectID: cfg.Cluster.Secrets.ProjectID,
			Storage:   clusterStorage,
		}

		// load encryption key if configured (only used with object-storage secrets provider)
		if cfg.Cluster.Secrets.EncryptionKey != nil {
			storeOpts.EncryptionKeys = []secrets.KeyConfig{{
				Provider:  cfg.Cluster.Secrets.EncryptionKey.Provider,
				ProjectID: cfg.Cluster.Secrets.EncryptionKey.ProjectID,
				Source:    cfg.Cluster.Secrets.EncryptionKey.Source,
			}}
		}
		for _, k := range cfg.Cluster.Secrets.OldEncryptionKeys {
			storeOpts.EncryptionKeys = append(storeOpts.EncryptionKeys, secrets.KeyConfig{
				Provider:  k.Provider,
				ProjectID: k.ProjectID,
				Source:    k.Source,
			})
		}
		secretsStore, err := secrets.NewStore(ctx, storeOpts)
		if err != nil {
			logger.Error("Failed to create secrets store", "error", err)
			os.Exit(1)
		}
		if len(storeOpts.EncryptionKeys) > 0 && cfg.Cluster.Secrets.Provider == "object-storage" {
			logger.Info("Using object storage as an encrypted secrets store", "key_count", len(storeOpts.EncryptionKeys))
		}

		// create election manager for managing cluster and shard leader election
		electionManager := election.NewManager(election.ManagerConfig{
			ClusterID:  cfg.Cluster.ID,
			ShardID:    flagShard,
			ServerID:   flagServerID,
			ServerAddr: cfg.Shard.Advertise.ElectionAddr,
			Logger:     logger,
		})
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), cfg.Shard.ShutdownTimeout.Duration())
			defer cancel()
			if err := electionManager.Stop(stopCtx); err != nil {
				logger.Error("Failed to stop election manager", "error", err)
			}
		}()

		// bootstrap cluster: ensure CA and registration nonce key exist, and start leader election
		var caConfig *config.CertConfig
		if c, ok := cfg.Certificates["ca"]; ok {
			caConfig = &c
		}
		bootstrapResult, err := cluster.Bootstrap(ctx, cluster.BootstrapConfig{
			ElectionManager: electionManager,
			ElectionConfig: election.ElectionConfig{
				Storage:            clusterStorage,
				FrequentInterval:   cfg.Cluster.LeaderElection.FrequentInterval.Duration(),
				InfrequentInterval: cfg.Cluster.LeaderElection.InfrequentInterval.Duration(),
				LeaderTimeout:      cfg.Cluster.LeaderElection.LeaderTimeout.Duration(),
			},
			Storage:      clusterStorage,
			SecretsStore: secretsStore,
			CAConfig:     caConfig,
			TemplateVars: cfg.Defaults.Vars,
			Logger:       logger,
		}, cfg.IsClusterLeaderElectionEnabled())
		if err != nil {
			if errors.Is(err, cluster.ErrCAGenerationRequiresLeadership) {
				jitterWaitThenExit(logger, err.Error(), cfg.Shard.ErrorExitJitter)
				return
			}
			logger.Error("Failed to bootstrap cluster", "error", err)
			jitterWaitThenExit(logger, err.Error(), cfg.Shard.ErrorExitJitter)
			return
		}
		caCertData := bootstrapResult.CACert
		caKeyData := bootstrapResult.CAKey

		// create shard infrastructure provider for provisioning infrastructure
		infraProvider, err := infra.NewProviderWithOptions(infra.ProviderOptions{
			Config: infra.ProviderConfig{
				Kind:    cfg.Shard.Infra.Provider,
				Region:  cfg.Shard.Infra.Region,
				Zone:    cfg.Shard.Infra.Zone,
				Options: cfg.Shard.Infra.Options,
			},
			Logger:            logger,
			Shard:             flagShard,
			DevK8sDir:         filepath.Join(flagCacheDir, "..", "dev-k8s"), // temp/dev-k8s relative to temp/cache
			RegistrationAddr:  cfg.Shard.Advertise.RegistrationAddr,
			AgentAddr:         cfg.Shard.Advertise.AgentAddr,
			HighestProviderID: localDB.HighestProviderID,
		})
		if err != nil {
			logger.Error("Failed to create infrastructure provider", "error", err)
			os.Exit(1)
		}
		logger.Info("Infrastructure provider ready", "kind", cfg.Shard.Infra.Provider)

		// create image resolution service (if images are configured)
		var imageService *images.Service
		if len(cfg.Images) > 0 {
			var err error
			imageService, err = images.NewService(images.ServiceOptions{
				ProviderConfig: infra.ProviderConfig{
					Kind:    cfg.Shard.Infra.Provider,
					Region:  cfg.Shard.Infra.Region,
					Zone:    cfg.Shard.Infra.Zone,
					Options: cfg.Shard.Infra.Options,
				},
				DB:       localDB,
				Configs:  cfg.Images,
				Interval: cfg.Shard.ImageRefreshInterval.Duration(),
				Logger:   logger.With("component", "image-service"),
			})
			if err != nil {
				// Image resolution not supported for this provider - log warning but continue
				logger.Warn("Image resolution not supported for provider", "provider", cfg.Shard.Infra.Provider, "error", err)
				imageService = nil
			} else {
				logger.Info("Image resolution service created", "count", len(cfg.Images), "interval", cfg.Shard.ImageRefreshInterval)
			}
		}

		// build leader network config from shard config
		var leaderNetwork *infra.LeaderNetwork
		if cfg.Shard.LeaderNetwork != nil {
			leaderNetwork = &infra.LeaderNetwork{
				IP:          cfg.Shard.LeaderNetwork.IP,
				InterfaceID: cfg.Shard.LeaderNetwork.InterfaceID,
			}
		}

		// create instance info client for cloud provider instance ID resolution
		var instanceInfoClient *instanceinfo.Client
		if cfg.Shard.Infra.Provider == "tmux" {
			// for tmux dev environments, we use the mock provider with static ID, since there is no metadata service
			instanceInfoClient = instanceinfo.NewWithProvider(instanceinfo.NewMockProvider(flagServerID))
			logger.Info("Using tmux dev environment, skipping instance metadata detection", "server_id", flagServerID)
		} else {
			var err error
			instanceInfoClient, err = instanceinfo.New()
			if err != nil {
				if leaderNetwork != nil {
					logger.Error("Failed to detect cloud provider for instance metadata (required for leader network)", "error", err)
					os.Exit(1)
				}
				logger.Warn("Failed to detect cloud provider for instance metadata", "error", err)
			} else {
				logger.Info("Detected cloud provider", "provider", instanceInfoClient.Provider())
			}
		}

		// create instances manager
		instancesManagerOptions := instances.ManagerOptions{
			ConfigLoader: configLoader,
			SecretsStore: secretsStore,
			Storage:      shardStorage,
			LocalDB:      localDB,
			Provider:     infraProvider,
			CACert:       caCertData,
			Logger:       logger,
		}
		if imageService != nil {
			instancesManagerOptions.ImageGetter = imageService
		}
		instancesManager, err := instances.NewManager(instancesManagerOptions)
		if err != nil {
			logger.Error("Failed to create instances manager", "error", err)
			os.Exit(1)
		}
		logger.Info("Instances manager ready")

		// create operator service first so we can wire drain notifications
		var operatorService *operator.Service
		tenantState, err := tenantstate.New(shardStorage, logger)
		if err != nil {
			logger.Error("Failed to create tenant state manager", "error", err)
			os.Exit(1)
		}

		// create reconciler
		rec, err := reconciler.New(reconciler.Options{
			InstanceManager: instancesManager,
			ConfigLoader:    configLoader,
			LocalDB:         localDB,
			Provider:        infraProvider,
			NotifyDrain: func(instanceID, group, reason string, unhealthyAt, deleteAt time.Time) {
				if operatorService != nil {
					operatorService.NotifyDrain(operator.DrainNotification{
						InstanceID:  instanceID,
						Group:       group,
						Reason:      reason,
						UnhealthyAt: unhealthyAt,
						DeleteAt:    deleteAt,
					})
				}
			},
			NotifyError: func(tenant, group, instanceID, errMsg string) {
				if operatorService != nil {
					operatorService.NotifyError(tenant, &proto.ErrorEvent{
						Group:      group,
						InstanceId: instanceID,
						Error:      errMsg,
						Timestamp:  timestamppb.Now(),
					})
				}
			},
			IsLeader:        electionManager.IsShardLeader,
			CreateRateLimit: cfg.Shard.CreateRateLimit.Duration(),
			Logger:          logger,
		})
		if err != nil {
			logger.Error("Failed to create reconciler", "error", err)
			os.Exit(1)
		}

		logger.Info("Reconciler ready")

		// prepare garbage collection runner
		gcService := gc.NewInstanceGarbageCollector(localDB, shardStorage, rec, instancesManager, logger)
		gcRunner := gc.NewRunner(
			gcService,
			cfg.Shard.GarbageCollection.Interval.Duration(),
			cfg.Shard.RequestTimeout.Duration(),
			cfg.Shard.GarbageCollection.RegistrationTimeout.Duration(),
			cfg.Shard.GarbageCollection.DeletedRecordRetention.Duration(),
			cfg.Shard.HealthCheckInterval.Duration(),
			electionManager.IsShardLeader,
			logger.With("component", "gc-runner"),
		)

		// create gRPC services
		registrationService, err := registration.New(registration.Options{
			ShardStorage:    shardStorage,   // For agent registration (shard-scoped)
			ClusterStorage:  clusterStorage, // For operator registration (cluster-scoped)
			SecretsStore:    secretsStore,
			LocalDB:         localDB,
			InstanceManager: instancesManager,
			ConfigLoader:    configLoader,
			IsShardLeader:   electionManager.IsShardLeader,
			IsClusterLeader: electionManager.IsClusterLeader,
			Logger:          logger,
		})
		if err != nil {
			logger.Error("Failed to create registration service", "error", err)
			os.Exit(1)
		}

		// generate server TLS certificate using CA
		serverCertPEM, serverKeyPEM, err := pki.GenerateServerCertificate(caCertData, caKeyData, cfg.Shard.Bind.RegistrationAddr, cfg.Shard.Advertise.RegistrationAddr, cfg.Shard.Advertise.OperatorAddr, cfg.Shard.Advertise.AgentAddr)
		if err != nil {
			logger.Error("Failed to generate server certificate", "error", err)
			os.Exit(1)
		}
		logger.Info("Server certificate ready")

		// generate leader election TLS certificate using CA
		// Use advertise address so peers can verify the cert when connecting to this server's external IP
		electionCertPEM, electionKeyPEM, err := pki.GenerateServerCertificate(caCertData, caKeyData, cfg.Shard.Advertise.ElectionAddr)
		if err != nil {
			logger.Error("Failed to generate leader election certificate", "error", err)
			os.Exit(1)
		}
		logger.Info("Leader election certificate ready")

		// create TLS certificate for election health server
		electionCert, err := tls.X509KeyPair(electionCertPEM, electionKeyPEM)
		if err != nil {
			logger.Error("Failed to create leader election health certificate", "error", err)
			os.Exit(1)
		}

		// start election health server (serves both /health/leadership/cluster and /health/leadership/shard)
		if err := electionManager.StartHealthServer(ctx, election.HealthServerConfig{
			BindAddr: cfg.Shard.Bind.ElectionAddr,
			TLSCert:  electionCert,
		}); err != nil {
			logger.Error("Failed to start election health server", "error", err)
			os.Exit(1)
		}
		logger.Info("Election health server ready")

		// create agent gRPC service
		agentService, err := agent.New(agent.Options{
			Storage:        shardStorage,
			ConfigLoader:   configLoader,
			LocalDB:        localDB,
			SecretsStore:   secretsStore,
			CACertPEM:      caCertData,
			CAKeyPEM:       caKeyData,
			Shard:          flagShard,
			ImageGetter:    imageService,
			Logger:         logger,
			OnHealthReport: instancesManager.ReconcileLoadBalancers,
			OnSpotTermination: func(instanceID string, notice *proto.TerminationNotice) error {
				logger.Info("Enqueuing spot termination event", "instance_id", instanceID, "action", notice.Action)
				rec.Enqueue(reconciler.ReconcileEvent{
					Type:       reconciler.EventTerminationNotice,
					InstanceID: instanceID,
				})
				return nil
			},
			OnReconcileRequested: func(tenant, groupKey, reason string) error {
				rec.Enqueue(reconciler.ReconcileEvent{
					Type:      reconciler.EventGroupChanged,
					Tenant:    tenant,
					GroupKey:  groupKey,
					Timestamp: time.Now().UTC(),
					Cause:     reason,
				})
				return nil
			},
			OnInstanceDisconnect: func(instanceID string, graceful bool) error {
				logger.Info("Agent disconnected, enqueuing health check",
					"instance_id", instanceID,
					"graceful", graceful)
				rec.Enqueue(reconciler.ReconcileEvent{
					Type:             reconciler.EventCheckInstance,
					InstanceID:       instanceID,
					Timestamp:        time.Now().UTC(),
					Cause:            "disconnect",
					PreventDuplicate: true,
				})
				return nil
			},
		})
		if err != nil {
			logger.Error("Failed to create agent service", "error", err)
			os.Exit(1)
		}

		// create operator gRPC service (assign to variable declared above)
		operatorService, err = operator.New(operator.Options{
			ConfigLoader:    configLoader,
			TenantState:     tenantState,
			LocalDB:         localDB,
			InstanceManager: instancesManager,
			OnGroupChanged: func(tenant, groupKey string) {
				rec.Enqueue(reconciler.ReconcileEvent{
					Type:      reconciler.EventGroupChanged,
					Tenant:    tenant,
					GroupKey:  groupKey,
					Timestamp: time.Now().UTC(),
				})
			},
			OnDrainAcked: func(tenant, instanceID string) {
				rec.Enqueue(reconciler.ReconcileEvent{
					Type:       reconciler.EventDrainAcked,
					Tenant:     tenant,
					InstanceID: instanceID,
					Timestamp:  time.Now().UTC(),
				})
			},
			Logger: logger,
			// Certificate renewal dependencies
			ClusterStorage:  clusterStorage,
			CACertPEM:       caCertData,
			CAKeyPEM:        caKeyData,
			IsClusterLeader: electionManager.IsClusterLeader,
		})
		if err != nil {
			logger.Error("Failed to create operator service", "error", err)
			os.Exit(1)
		}

		// create gRPC server
		server, err := api.NewServer(api.ServerOptions{
			Config:              cfg,
			Logger:              logger,
			RegistrationService: registrationService,
			AgentService:        agentService,
			OperatorService:     operatorService,
			Reconciler:          rec,
			CACertPEM:           caCertData,
			ServerCertPEM:       serverCertPEM,
			ServerKeyPEM:        serverKeyPEM,
			Debug:               flagDebug,
		})
		if err != nil {
			logger.Error("Failed to create server", "error", err)
			os.Exit(1)
		}
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), cfg.Shard.ShutdownTimeout.Duration())
			defer cancel()
			err := server.Stop(stopCtx)
			if err != nil {
				logger.Error("Failed to stop server", "error", err)
			}
		}()

		// start shard leader election via election manager with inlined callbacks
		shutdownTimeout := cfg.Shard.ShutdownTimeout.Duration()
		if err := electionManager.StartShardElection(ctx, election.ElectionConfig{
			Storage:            shardStorage,
			PeerMode:           true, // Always enabled since we have CA cert
			CACert:             caCertData,
			FrequentInterval:   cfg.Shard.LeaderElection.FrequentInterval.Duration(),
			InfrequentInterval: cfg.Shard.LeaderElection.InfrequentInterval.Duration(),
			LeaderTimeout:      cfg.Shard.LeaderElection.LeaderTimeout.Duration(),
			OnAcquire: func(ctx context.Context) error {
				logger.Info("Acquired shard leadership")

				// Assign leader network if configured (cloud providers with ENI/alias IP)
				if leaderNetwork != nil {
					logger.Info("Assigning leader network", "ip", leaderNetwork.IP, "interfaceID", leaderNetwork.InterfaceID)

					providerInstanceID, err := instanceInfoClient.GetInstanceID(ctx)
					if err != nil {
						return fmt.Errorf("failed to get provider instance ID: %w", err)
					}

					// Assign leader network (critical operation - must succeed)
					if err := infraProvider.AssignLeaderNetwork(ctx, providerInstanceID, *leaderNetwork); err != nil {
						return fmt.Errorf("failed to assign leader network: %w", err)
					}
					logger.Info("Successfully assigned leader network")

					// Wait for the leader IP to be available at the OS level before starting services
					// Cloud provider APIs report assignment complete before the IP is routable on the host
					if leaderNetwork.IP != "" {
						if err := provider.WaitForIPBindable(ctx, logger, leaderNetwork.IP); err != nil {
							return fmt.Errorf("failed waiting for leader IP to be available: %w", err)
						}
					}
				}

				// Refresh configuration, dynamic groups, and sync to SQLite
				logger.Info("Became shard leader, refreshing configuration and rebuilding cache")
				refreshedCfg, err := configLoader.LoadConfigAndGroups(ctx, true)
				if err != nil {
					return fmt.Errorf("failed to refresh configuration on leadership acquisition: %w", err)
				}
				// Start image resolution service
				if imageService != nil {
					if err := imageService.Start(ctx); err != nil {
						return fmt.Errorf("failed to start image resolution service: %w", err)
					}
				}

				// Rebuild local cache from S3 and provider
				cacheCtx, cacheCancel := context.WithTimeout(ctx, 60*time.Second)
				defer cacheCancel()
				if err := instancesManager.RebuildCache(cacheCtx); err != nil {
					return fmt.Errorf("failed to rebuild authoritative cache: %w", err)
				}

				// Validate load balancers only after provider IDs have been rebuilt.
				if len(refreshedCfg.LoadBalancers) > 0 {
					lbCtx, lbCancel := context.WithTimeout(ctx, 60*time.Second)
					defer lbCancel()
					if err := infra.ValidateLoadBalancers(lbCtx, refreshedCfg, localDB, infraProvider, logger); err != nil {
						return fmt.Errorf("failed to validate load balancer groups on leadership acquisition: %w", err)
					}
				}

				// Start leader services only after the authoritative configuration
				// refresh, instance cache rebuild, and load-balancer observations
				// complete, so requests and watches cannot use stale state.
				logger.Info("Starting leader services")
				if err := server.Start(ctx); err != nil {
					return fmt.Errorf("failed to start gRPC server: %w", err)
				}
				logger.Info("Leader service started")

				// Trigger initial reconciliation.
				rec.Enqueue(reconciler.ReconcileEvent{
					Type:      reconciler.EventInitialReconcile,
					Timestamp: time.Now().UTC(),
				})

				// Start GC after cache rebuild and initial reconcile are enqueued
				// (so GC doesn't delete S3 records or enqueue GroupChanged events
				// before the cache is populated)
				gcRunner.Start(ctx)

				return nil
			},
			OnLose: func(ctx context.Context) error {
				logger.Info("Lost shard leadership, cleaning up")

				// Stop leader services
				logger.Info("Stopping leader services")
				gcRunner.Stop()

				stopCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
				defer cancel()
				if err := server.Stop(stopCtx); err != nil {
					logger.Error("Failed to stop leader service", "error", err)
				} else {
					logger.Info("Leader service stopped")
				}

				// Release leader network if configured
				if leaderNetwork != nil {
					logger.Info("Releasing leader network", "ip", leaderNetwork.IP, "interfaceID", leaderNetwork.InterfaceID)
					providerInstanceID, err := instanceInfoClient.GetInstanceID(ctx)
					if err != nil {
						return fmt.Errorf("failed to get provider instance ID: %w", err)
					}
					if err := infraProvider.ReleaseLeaderNetwork(ctx, providerInstanceID, *leaderNetwork); err != nil {
						return fmt.Errorf("failed to release leader network: %w", err)
					}
					logger.Info("Successfully released leader network")
				}

				return nil
			},
		}); err != nil {
			logger.Error("Failed to start shard leader election", "error", err)
			os.Exit(1)
		}
		logger.Info("Shard leader election started")

		// block until shutdown signal is received via context
		<-ctx.Done()

		// shutdown
		logger.Info("Shutting down server...", "reason", ctx.Err())
		logger.Info("Server shutdown complete")
	}
	return rootCmd
}

// jitterWaitThenExit waits for a random amount of time then exits
func jitterWaitThenExit(logger *slog.Logger, reason string, jitterCfg config.ErrorExitJitterConfig) {
	minWait := 10 * time.Second
	maxWait := 40 * time.Second
	if jitterCfg.MinDelay.Duration() > 0 {
		minWait = jitterCfg.MinDelay.Duration()
	}
	if jitterCfg.MaxDelay.Duration() > 0 {
		maxWait = jitterCfg.MaxDelay.Duration()
	}
	jitterRange := maxWait - minWait
	if jitterRange < 0 {
		jitterRange = 0
	}
	waitFor := minWait + time.Duration(rand.Int63n(int64(jitterRange)+1))
	logger.Info("Waiting before exiting", "wait", waitFor, "reason", reason)
	time.Sleep(waitFor)
	logger.Info("Exiting...")
	os.Exit(1)
}
