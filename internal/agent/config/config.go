// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
	"github.com/puidv7/puidv7-go"
	"golang.org/x/sys/unix"
)

// Config defines the agent runtime configuration. Each field is populated from environment variables.
type Config struct {
	Debug            bool          `env:"DEBUG" envDefault:"false"`
	Environment      string        `env:"ENVIRONMENT" envDefault:"development"`
	RegistrationAddr string        `env:"SERVER_REGISTRATION_ADDR" validate:"required,addr"`
	AgentAddr        string        `env:"SERVER_AGENT_ADDR" validate:"required,addr"`
	IdentityDir      string        `env:"IDENTITY_DIR" envDefault:"/opt/nstance-agent/identity" validate:"required,dirpath,dirwriteable"`
	KeysDir          string        `env:"KEYS_DIR" envDefault:"/opt/nstance-agent/keys" validate:"required,dirpath,dirwriteable"`
	RecvDir          string        `env:"RECV_DIR" envDefault:"/opt/nstance-agent/recv" validate:"required,dirpath,dirwriteable"`
	IdentityMode     string        `env:"IDENTITY_MODE" envDefault:"0600"`
	KeysMode         string        `env:"KEYS_MODE" envDefault:"0640"`
	RecvMode         string        `env:"RECV_MODE" envDefault:"0640"`
	InstanceKind     string        `env:"INSTANCE_KIND"`
	InstanceID       string        `env:"INSTANCE_ID" validate:"required,puidv7"`
	InstanceHostname string        `env:"INSTANCE_HOSTNAME" validate:"omitempty,hostname"`
	InstanceFQDN     string        `env:"INSTANCE_FQDN" validate:"omitempty,fqdn"`
	InstanceIPv4     string        `env:"INSTANCE_IPV4" validate:"omitempty,ipv4"`
	InstanceIPv6     string        `env:"INSTANCE_IPV6" validate:"omitempty,ipv6"`
	ReportInterval   time.Duration `env:"REPORT_INTERVAL" envDefault:"60s" validate:"gte=0"`
	MetricsInterface string        `env:"METRICS_INTERFACE"`
	SpotPollInterval time.Duration `env:"SPOT_POLL_INTERVAL" envDefault:"2s" validate:"gte=0"`
}

// parseFileMode parses an octal string to os.FileMode
func parseFileMode(s string) (os.FileMode, error) {
	val, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, err
	}
	return os.FileMode(val), nil
}

// IdentityFileMode returns the IdentityMode as os.FileMode
func (c Config) IdentityFileMode() os.FileMode {
	mode, _ := parseFileMode(c.IdentityMode) // validation ensures this is valid
	return mode
}

// KeysFileMode returns the KeysMode as os.FileMode
func (c Config) KeysFileMode() os.FileMode {
	mode, _ := parseFileMode(c.KeysMode)
	return mode
}

// RecvFileMode returns the RecvMode as os.FileMode
func (c Config) RecvFileMode() os.FileMode {
	mode, _ := parseFileMode(c.RecvMode)
	return mode
}

// Load builds a Config from environment variables, applies defaults, normalises values, and validates the result.
func Load() (Config, error) {
	var cfg Config

	options := env.Options{
		Prefix: "NSTANCE_",
	}

	if err := env.ParseWithOptions(&cfg, options); err != nil {
		return Config{}, err
	}

	// normalise directory paths to clean, absolute paths
	var err error
	cfg.IdentityDir, err = filepath.Abs(filepath.Clean(cfg.IdentityDir))
	if err != nil {
		return Config{}, fmt.Errorf("identity_dir: %w", err)
	}
	cfg.KeysDir, err = filepath.Abs(filepath.Clean(cfg.KeysDir))
	if err != nil {
		return Config{}, fmt.Errorf("keys_dir: %w", err)
	}
	cfg.RecvDir, err = filepath.Abs(filepath.Clean(cfg.RecvDir))
	if err != nil {
		return Config{}, fmt.Errorf("recv_dir: %w", err)
	}

	// validate file modes
	if identityMode, err := parseFileMode(cfg.IdentityMode); err != nil {
		return Config{}, fmt.Errorf("identity_mode: invalid octal value %q: %w", cfg.IdentityMode, err)
	} else if identityMode > 0o777 {
		return Config{}, fmt.Errorf("identity_mode: %s is too permissive (max 0777)", cfg.IdentityMode)
	}
	if keysMode, err := parseFileMode(cfg.KeysMode); err != nil {
		return Config{}, fmt.Errorf("keys_mode: invalid octal value %q: %w", cfg.KeysMode, err)
	} else if keysMode > 0o777 {
		return Config{}, fmt.Errorf("keys_mode: %s is too permissive (max 0777)", cfg.KeysMode)
	}
	if recvMode, err := parseFileMode(cfg.RecvMode); err != nil {
		return Config{}, fmt.Errorf("recv_mode: invalid octal value %q: %w", cfg.RecvMode, err)
	} else if recvMode > 0o777 {
		return Config{}, fmt.Errorf("recv_mode: %s is too permissive (max 0777)", cfg.RecvMode)
	}

	// validate all config
	if err := validators.Struct(cfg); err != nil {
		var verrs validator.ValidationErrors
		if errors.As(err, &verrs) {
			var builder strings.Builder
			for _, v := range verrs {
				builder.WriteString(v.Field())
				builder.WriteString(" failed validation on '")
				builder.WriteString(v.Tag())
				builder.WriteString("' validator\n")
			}
			return Config{}, errors.New(strings.TrimSuffix(builder.String(), "\n"))
		}
		return Config{}, err
	}
	return cfg, nil
}

var validators = buildValidator()

func buildValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())

	if err := v.RegisterValidation("puidv7", func(fl validator.FieldLevel) bool {
		value := fl.Field().String()
		if value == "" {
			return true
		}
		_, err := puidv7.Decode(value, "")
		return err == nil
	}); err != nil {
		panic(fmt.Sprintf("registering puidv7 validator: %v", err))
	}

	if err := v.RegisterValidation("dirpath", func(fl validator.FieldLevel) bool {
		value := fl.Field().String()
		if value == "" {
			return true
		}
		clean := filepath.Clean(value)
		return clean != "" && clean != "."
	}); err != nil {
		panic(fmt.Sprintf("registering dirpath validator: %v", err))
	}

	if err := v.RegisterValidation("dirwriteable", func(fl validator.FieldLevel) bool {
		value := fl.Field().String()
		if value == "" {
			return true
		}
		clean := filepath.Clean(value)
		return clean != "" && clean != "." && unix.Access(clean, unix.W_OK) == nil
	}); err != nil {
		panic(fmt.Sprintf("registering dirwriteable validator: %v", err))
	}

	if err := v.RegisterValidation("csvbasenames", func(fl validator.FieldLevel) bool {
		value := fl.Field()
		if value.Kind() != reflect.Slice {
			return false
		}
		for i := 0; i < value.Len(); i++ {
			name := value.Index(i).String()
			if name == "" {
				continue
			}
			if filepath.Base(name) != name || strings.Contains(name, string(filepath.Separator)) {
				return false
			}
		}
		return true
	}); err != nil {
		panic(fmt.Sprintf("registering csvbasenames validator: %v", err))
	}

	if err := v.RegisterValidation("hostname", func(fl validator.FieldLevel) bool {
		value := fl.Field().String()
		if value == "" {
			return false
		}
		// Simple hostname validation - can be IP or hostname
		if ip := net.ParseIP(value); ip != nil {
			return true // Valid IP address
		}
		// Basic hostname validation (simplified)
		return len(value) > 0 && len(value) <= 253 && !strings.HasPrefix(value, ".") && !strings.HasSuffix(value, ".")
	}); err != nil {
		panic(fmt.Sprintf("registering hostname validator: %v", err))
	}

	if err := v.RegisterValidation("addr", func(fl validator.FieldLevel) bool {
		value := fl.Field().String()
		if value == "" {
			return false
		}
		_, portStr, err := net.SplitHostPort(value)
		if err != nil {
			return false
		}
		port, err := strconv.Atoi(portStr)
		return err == nil && port >= 1 && port <= 65535
	}); err != nil {
		panic(fmt.Sprintf("registering addr validator: %v", err))
	}

	return v
}
