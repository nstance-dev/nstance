// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"log/slog"

	"github.com/nstance-dev/nstance/internal/agent/config"
	"github.com/nstance-dev/nstance/internal/agent/keygen"
	"github.com/nstance-dev/nstance/internal/agent/receiver"
	"github.com/nstance-dev/nstance/internal/buildvars"
	"github.com/nstance-dev/nstance/internal/identity"
	"github.com/nstance-dev/nstance/pkg/client/agent"
	"github.com/nstance-dev/nstance/pkg/client/registration"
	"github.com/nstance-dev/nstance/pkg/health"
	"github.com/nstance-dev/nstance/pkg/instanceinfo"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "nstance-agent",
	Short: "Nstance Agent",
	Long:  `nstance-agent is a deamon for running on every VM instance of your infrastructure`,
}

var (
	flagDebug   bool
	flagVersion bool
)

func init() {
	pflags := rootCmd.PersistentFlags()
	pflags.BoolVarP(&flagDebug, "debug", "v", false, "Enable debug output")
	pflags.BoolVar(&flagVersion, "version", false, "Show version information")
	if flag := pflags.Lookup("debug"); flag != nil {
		flag.NoOptDefVal = "true"
	}
}

func NewRootCmd() *cobra.Command {
	rootCmd.Run = func(cmd *cobra.Command, args []string) {
		// print version if requested
		if flagVersion {
			fmt.Printf("nstance-agent %s\n", buildvars.BuildVersion())
			envDebug := os.Getenv("NSTANCE_DEBUG")
			if flagDebug || envDebug == "true" || envDebug == "1" {
				fmt.Printf("build version: %s\n", buildvars.BuildVersion())
				fmt.Printf("build date: %s\n", buildvars.BuildDate())
				fmt.Printf("commit hash: %s\n", buildvars.CommitHash())
				fmt.Printf("commit date: %s\n", buildvars.CommitDate())
				fmt.Printf("commit branch: %s\n", buildvars.CommitBranch())
			}
			return
		}

		// load config from environment variables and validate
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid config/environment variables: %v\n", err)
			os.Exit(1)
		}
		if flagDebug || cfg.Debug {
			cfg.Debug = true
			fmt.Println("Debug output ENABLED")
		}

		// create structured logger with appropriate level
		logLevel := slog.LevelInfo
		if cfg.Debug {
			logLevel = slog.LevelDebug
		}
		logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))

		// Detect if running on spot instance
		var spotMonitor *instanceinfo.SpotMonitor
		client, err := instanceinfo.New()
		if err != nil {
			logger.Info("provider detection failed, skipping spot monitoring", "err", err)
		} else {
			isSpot, err := client.IsSpot(context.Background())
			if err != nil {
				logger.Info("spot detection failed, skipping spot monitoring", "err", err)
			} else if isSpot {
				logger.Info("running on spot instance, enabling termination monitoring", "provider", client.Provider())
				spotMonitor, err = instanceinfo.NewSpotMonitor(instanceinfo.SpotMonitorConfig{
					PollInterval: cfg.SpotPollInterval,
					Logger:       logger,
				})
				if err != nil {
					logger.Error("failed to create spot monitor", "err", err)
					os.Exit(1)
				}
			} else {
				logger.Info("not running on spot instance, spot monitoring disabled")
			}
		}

		// load identity: nonce (if exists still), instance keypair, server CA certificate, and client certificate (if issued)
		ident, err := identity.LoadOrCreate(cfg.IdentityDir, logger, cfg.IdentityFileMode())
		if err != nil {
			logger.Error("error loading identity files", "err", err)
			os.Exit(1)
		}

		// handle shutdown signals
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		shutdownErrsCh := make(chan error, 1)
		go func() {
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			defer signal.Stop(sigCh)
			select {
			case sig := <-sigCh:
				select {
				case shutdownErrsCh <- fmt.Errorf("%s", sig):
				default:
				}
			case <-ctx.Done():
			}
		}()

		// register instance public key using nonce, and store client cert then delete nonce
		if len(ident.Nonce) > 0 {
			// instantiate grpc client for registration
			registrationClient, err := registration.NewClient(registration.Config{
				ServerAddress: cfg.RegistrationAddr,
				ServerCACert:  *ident.CACert,
			})
			if err != nil {
				logger.Error("unable to create registration service client", "err", err)
				jitterWaitThenExit(logger)
				return
			}
			defer func() {
				err := registrationClient.Close()
				if err != nil {
					logger.Error("unable to close registration client", "err", err)
				}
			}()

			// register then store client certificate and delete nonce
			logger.Info("registering instance with Nstance server")
			ipv4, ipv6 := identity.LocalIPs(cfg.InstanceIPv4, cfg.InstanceIPv6)
			hostname := identity.LocalHostname(cfg.InstanceHostname)
			if err := registrationClient.RegisterAgent(ctx, ident, string(ident.Nonce), ipv4, ipv6, hostname); err != nil {
				logger.Error("unable to register instance", "err", err)
				jitterWaitThenExit(logger)
				return
			}
			logger.Info("agent registration successful")
		}

		// connect to agent service using client certificate
		agentClient, err := agent.NewClient(logger, agent.Config{
			ServerAddress: cfg.AgentAddr,
			ServerCACert:  *ident.CACert,
			ClientCert:    ident.ClientCert,
			PrivateKey:    *ident.PrivateKey,
			InstanceID:    cfg.InstanceID,
		})
		if err != nil {
			logger.Error("unable to create agent service client", "err", err)
			jitterWaitThenExit(logger)
			return
		}
		defer func() {
			err := agentClient.Close()
			if err != nil {
				logger.Error("unable to close agent client", "err", err)
			}
		}()

		// setup file receiver and start streaming files
		filesReceiver := receiver.NewReceiver(
			logger,
			receiver.ReceiverConfig{
				RecvDir:     cfg.RecvDir,
				IdentityDir: cfg.IdentityDir,
				FileMode:    cfg.RecvFileMode(),
			},
		)
		go func() {
			for {
				err := agentClient.StreamFiles(ctx, filesReceiver)
				if ctx.Err() != nil || errors.Is(err, context.Canceled) {
					return
				}
				if err == nil {
					logger.Info("file stream closed by server, reconnecting", "retry_in", "2s")
					select {
					case <-time.After(2 * time.Second):
						continue
					case <-ctx.Done():
						return
					}
				}
				select {
				case shutdownErrsCh <- err:
				default:
				}
				return
			}
		}()

		// setup key request handler
		keyHandler := keygen.New(logger, cfg.KeysDir, cfg.KeysFileMode(), agentClient)

		// start receiving key generation requests from server
		go func() {
			err := agentClient.ReceiveKeyRequests(ctx, keyHandler)
			if err != nil && !errors.Is(err, context.Canceled) {
				select {
				case shutdownErrsCh <- fmt.Errorf("key request stream error: %w", err):
				default:
				}
			}
		}()

		// Start spot monitor if running on spot instance
		if spotMonitor != nil {
			go spotMonitor.Start(ctx)
		}

		// start reporting metrics on interval
		metricsReportingInterval := cfg.MetricsInterval
		if metricsReportingInterval > 0 {
			logger.Info("starting metrics reporter", "interval", metricsReportingInterval, "instance_id", cfg.InstanceID)
			getTerminationNotice := func() *health.TerminationNotice {
				if spotMonitor != nil {
					return spotMonitor.GetTerminationNotice()
				}
				return nil
			}
			metricsCfg := health.ReportConfig{
				InstanceID:           cfg.InstanceID,
				RecvDir:              cfg.RecvDir,
				IdentityDir:          cfg.IdentityDir,
				GetTerminationNotice: getTerminationNotice,
			}

			// Create channel for health reports
			reportChan := make(chan health.Report, 10)

			// Start health report collector (sends to channel)
			go health.ReportLoop(ctx, logger, metricsReportingInterval, metricsCfg, func(report health.Report) error {
				select {
				case reportChan <- report:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				default:
					return fmt.Errorf("health report channel full")
				}
			})

			// Start health report stream (reads from channel and sends to server)
			go func() {
				for {
					err := agentClient.SubmitHealthReports(ctx, reportChan)
					if err == context.Canceled || err == context.DeadlineExceeded {
						logger.Info("Health reporting stopped")
						return
					}

					logger.Error("Health report stream failed, reconnecting",
						"error", err,
						"retry_in", "5s")

					// Exponential backoff reconnection
					select {
					case <-time.After(5 * time.Second):
						continue
					case <-ctx.Done():
						return
					}
				}
			}()
		} else {
			logger.Info("metrics reporter disabled")
		}

		// block until shutdown signal is received, then exit
		err = <-shutdownErrsCh
		logger.Info("shutting down...", "reason", err)
		cancel()
		logger.Info("exiting.")
	}
	return rootCmd
}

func jitterWaitThenExit(logger *slog.Logger) {
	// generate a random amount of time to wait before exiting
	// to introduce jitter / so we don't constantly retry
	waitFor := time.Duration(rand.Intn(10)) * time.Second
	logger.Info("waiting before exiting", "wait", waitFor)
	time.Sleep(waitFor)
	logger.Info("exiting...")
	os.Exit(1)
}
