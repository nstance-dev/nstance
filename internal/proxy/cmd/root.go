// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/spf13/cobra"

	"github.com/nstance-dev/nstance/internal/buildvars"
	"github.com/nstance-dev/nstance/internal/proxy"
	proxyconfig "github.com/nstance-dev/nstance/pkg/proxy"
)

// commandConfig contains nstance-proxy's environment configuration.
type commandConfig struct {
	SocketPath      string        `env:"NSTANCE_PROXY_SOCKET" envDefault:"/run/nstance/nstance-server.sock"`
	HoldTimeout     time.Duration `env:"NSTANCE_PROXY_HOLD_TIMEOUT" envDefault:"2m"`
	DialTimeout     time.Duration `env:"NSTANCE_PROXY_DIAL_TIMEOUT" envDefault:"10s"`
	ShutdownTimeout time.Duration `env:"NSTANCE_PROXY_SHUTDOWN_TIMEOUT" envDefault:"30s"`
	BindHost        string        `env:"NSTANCE_PROXY_BIND_HOST"`
	Debug           bool          `env:"NSTANCE_DEBUG" envDefault:"false"`
}

var (
	flagDebug      bool
	flagVersion    bool
	flagSocketPath string
	flagBindHost   string
)

// NewRootCmd creates the nstance-proxy command.
func NewRootCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "nstance-proxy",
		Short: "Hold connections while waking sleeping Nstance tenants",
		RunE:  run,
	}
	command.Flags().BoolVarP(&flagDebug, "debug", "v", false, "Enable debug output")
	command.Flags().BoolVar(&flagVersion, "version", false, "Show version information")
	command.Flags().StringVar(&flagSocketPath, "socket", "", "Override the nstance-server Unix socket")
	command.Flags().StringVar(&flagBindHost, "bind-host", "", "Override the proxy listener bind host")
	return command
}

// run composes and runs nstance-proxy until its process context is canceled.
func run(_ *cobra.Command, _ []string) error {
	if flagVersion {
		fmt.Printf("nstance-proxy %s\n", buildvars.BuildVersion())
		return nil
	}
	var cfg commandConfig
	if err := env.Parse(&cfg); err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	if flagSocketPath != "" {
		cfg.SocketPath = flagSocketPath
	}
	if flagBindHost != "" {
		cfg.BindHost = flagBindHost
	}
	level := slog.LevelInfo
	if flagDebug || cfg.Debug {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	waker, err := proxy.NewUnixWaker(cfg.SocketPath)
	if err != nil {
		return err
	}
	defer func() { _ = waker.Close() }()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	configs := make(chan proxyconfig.Config, 1)
	go watchConfig(ctx, logger, waker, configs)
	var initial proxyconfig.Config
	select {
	case initial = <-configs:
	case <-ctx.Done():
		return ctx.Err()
	}
	server, err := proxy.New(proxy.Options{
		Config:          initial,
		Waker:           waker,
		HoldTimeout:     cfg.HoldTimeout,
		DialTimeout:     cfg.DialTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
		BindHost:        cfg.BindHost,
		Logger:          logger,
	})
	if err != nil {
		return err
	}
	if err := server.Start(ctx); err != nil {
		return err
	}
	logger.Info("Nstance proxy started", "listeners", len(initial.Listeners))
	go func() {
		retry := time.NewTicker(100 * time.Millisecond)
		defer retry.Stop()
		var pending *proxyconfig.Config
		for {
			select {
			case next := <-configs:
				pending = &next
			case <-retry.C:
				if pending == nil {
					continue
				}
				if err := server.Reconcile(*pending); err != nil {
					logger.Error("Failed to reconcile proxy configuration", "error", err)
				} else {
					pending = nil
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	<-ctx.Done()
	return server.Close()
}

// watchConfig reconnects the local configuration stream until ctx is canceled.
func watchConfig(ctx context.Context, logger *slog.Logger, client *proxy.UnixWaker, configs chan proxyconfig.Config) {
	backoff := 100 * time.Millisecond
	for ctx.Err() == nil {
		err := client.WatchConfig(ctx, func(cfg proxyconfig.Config) error {
			select {
			case <-configs:
			default:
			}
			select {
			case configs <- cfg:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		logger.Warn("Proxy control watch disconnected", "error", err, "retry", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}
