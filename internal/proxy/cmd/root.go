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
	"github.com/nstance-dev/nstance/internal/proxy/config"
)

// commandConfig contains nstance-proxy's environment configuration.
type commandConfig struct {
	ConfigPath      string        `env:"NSTANCE_PROXY_CONFIG" envDefault:"/run/nstance/nstance-proxy.json"`
	SocketPath      string        `env:"NSTANCE_PROXY_SOCKET" envDefault:"/run/nstance/nstance-server.sock"`
	HoldTimeout     time.Duration `env:"NSTANCE_PROXY_HOLD_TIMEOUT" envDefault:"2m"`
	DialTimeout     time.Duration `env:"NSTANCE_PROXY_DIAL_TIMEOUT" envDefault:"10s"`
	ShutdownTimeout time.Duration `env:"NSTANCE_PROXY_SHUTDOWN_TIMEOUT" envDefault:"30s"`
	BindHost        string        `env:"NSTANCE_PROXY_BIND_HOST"`
	Debug           bool          `env:"NSTANCE_DEBUG" envDefault:"false"`
}

var (
	flagDebug   bool
	flagVersion bool
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
	level := slog.LevelInfo
	if flagDebug || cfg.Debug {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	staticConfig, err := config.Load(cfg.ConfigPath)
	if err != nil {
		return err
	}
	waker, err := proxy.NewUnixWaker(cfg.SocketPath)
	if err != nil {
		return err
	}
	defer func() { _ = waker.Close() }()
	server, err := proxy.New(proxy.Options{
		Config:          staticConfig,
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
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		return err
	}
	logger.Info("Nstance proxy started", "listeners", len(staticConfig.Listeners))
	<-ctx.Done()
	return server.Close()
}
