// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package localdb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Instance represents an instance record in the local database
type Instance struct {
	ID              string     `json:"id"`                // Instance ID (puidv7)
	Tenant          string     `json:"tenant"`            // Tenant identifier
	Group           string     `json:"group"`             // Group key (from config groups map)
	OnDemand        bool       `json:"on_demand"`         // Whether this is an on-demand instance (not managed by group reconciliation)
	ProviderID      *string    `json:"provider_id"`       // Provider's instance ID (e.g., i-1234567890abcdef0)
	ProviderAt      *time.Time `json:"provider_at"`       // When provider data was last cached
	Hostname        *string    `json:"hostname"`          // Instance hostname
	FQDN            *string    `json:"fqdn"`              // Fully qualified domain name
	IP4             *string    `json:"ip4"`               // IPv4 address
	IP6             *string    `json:"ip6"`               // IPv6 address
	ProviderState   []byte     `json:"provider_state"`    // Provider-specific state data (JSON)
	Nonce           string     `json:"nonce"`             // Registration nonce JWT
	IssuedAt        *time.Time `json:"issued_at"`         // When nonce was issued (nullable)
	InstancePub     *string    `json:"instance_pub"`      // Instance public key
	RegisteredAt    *time.Time `json:"registered_at"`     // When agent registered
	CertificatesAt  *time.Time `json:"certificates_at"`   // When certificates were issued
	HealthAt        *time.Time `json:"health_at"`         // When health was last updated
	Health          []byte     `json:"health"`            // Latest health report (JSON)
	InfraConfigHash *string    `json:"infra_config_hash"` // Infra config hash at provision time
	DrainStartedAt  *time.Time `json:"drain_started_at"`  // When marked for deletion (waiting for drain)
	DrainAckedAt    *time.Time `json:"drain_acked_at"`    // When operator acknowledged drain complete
	CreatedAt       time.Time  `json:"created_at"`        // When record was created
	UpdatedAt       *time.Time `json:"updated_at"`        // Last update time
	DeletedAt       *time.Time `json:"deleted_at"`        // When marked for deletion
}

// PublicKeySubmission represents a public key submitted by an agent
type PublicKeySubmission struct {
	Filename     string `json:"filename"`
	PublicKeyPEM string `json:"public_key_pem"`
}

// PublicKey represents public key material submitted by an agent.
type PublicKey struct {
	ID              int64      `json:"id"`
	InstanceID      string     `json:"instance_id"`
	Filename        string     `json:"filename"`
	PublicKeyPEM    string     `json:"public_key_pem"`
	CertificateName *string    `json:"certificate_name"`
	SubmittedAt     time.Time  `json:"submitted_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       *time.Time `json:"updated_at"`
}

// DB manages local SQLite database operations
type DB struct {
	file string
	conn *sql.DB
}

// Open opens a SQLite database and initializes the schema
func Open(file string) (*DB, error) {
	if file == "" {
		return nil, fmt.Errorf("database file path not configured")
	}

	dbDir := filepath.Dir(file)
	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		if err := os.MkdirAll(dbDir, 0750); err != nil {
			return nil, fmt.Errorf("creating database directory %s: %w", dbDir, err)
		}
	}

	conn, err := sql.Open("sqlite3", file)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if _, err = conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	if _, err = conn.Exec("PRAGMA foreign_keys=ON"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}
	if _, err = conn.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("setting busy_timeout: %w", err)
	}

	db := &DB{
		file: file,
		conn: conn,
	}

	if err := db.createSchema(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}

	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

func (db *DB) createSchema() error {
	instancesTable := `
	CREATE TABLE IF NOT EXISTS instances (
		id TEXT PRIMARY KEY NOT NULL,
		tenant TEXT NOT NULL,
		group_key TEXT,
		on_demand INTEGER DEFAULT 0,
		provider_id TEXT,
		provider_at DATETIME,
		hostname TEXT,
		fqdn TEXT,
		ip4 TEXT,
		ip6 TEXT,
		provider_state TEXT,
		nonce TEXT NOT NULL UNIQUE,
		issued_at DATETIME,
		instance_pub TEXT,
		registered_at DATETIME,
		certificates_at DATETIME,
		health_at DATETIME,
		health TEXT,
		infra_config_hash TEXT,
		drain_started_at DATETIME,
		drain_acked_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME,
		deleted_at DATETIME
	)`

	publicKeysTable := `
	CREATE TABLE IF NOT EXISTS public_keys (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		instance_id TEXT NOT NULL,
		filename TEXT NOT NULL,
		public_key_pem TEXT NOT NULL,
		certificate_name TEXT,
		submitted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME,
		FOREIGN KEY (instance_id) REFERENCES instances (id),
		UNIQUE (instance_id, filename)
	)`

	imagesTable := `
	CREATE TABLE IF NOT EXISTS images (
		name TEXT PRIMARY KEY NOT NULL,
		image_id TEXT NOT NULL,
		resolved_at DATETIME NOT NULL
	)`

	lbInstancesTable := `
	CREATE TABLE IF NOT EXISTS lb_instances (
		lb_key TEXT NOT NULL,
		instance_id TEXT NOT NULL,
		status TEXT NOT NULL,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (instance_id) REFERENCES instances (id),
		PRIMARY KEY (lb_key, instance_id)
	)`

	groupsTable := `
	CREATE TABLE IF NOT EXISTS groups (
		tenant TEXT NOT NULL,
		group_key TEXT NOT NULL,
		runtime_config_hash TEXT,
		infra_config_hash TEXT,
		hashes_updated_at DATETIME,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME,
		PRIMARY KEY (tenant, group_key)
	)`

	operatorsTable := `
	CREATE TABLE IF NOT EXISTS operators (
		id TEXT PRIMARY KEY NOT NULL,
		cluster_id TEXT NOT NULL,
		tenant TEXT NOT NULL,
		nonce TEXT NOT NULL UNIQUE,
		public_key TEXT,
		registered_at DATETIME NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME
	)`

	indexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_instances_nonce ON instances (nonce)`,
		`CREATE INDEX IF NOT EXISTS idx_instances_tenant ON instances (tenant)`,
		`CREATE INDEX IF NOT EXISTS idx_instances_tenant_group ON instances (tenant, group_key)`,
		`CREATE INDEX IF NOT EXISTS idx_instances_registered_at ON instances (registered_at)`,
		`CREATE INDEX IF NOT EXISTS idx_instances_updated_at ON instances (updated_at)`,
		`CREATE INDEX IF NOT EXISTS idx_instances_deleted_at ON instances (deleted_at)`,
		`CREATE INDEX IF NOT EXISTS idx_instances_provider_id ON instances (provider_id)`,
		`CREATE INDEX IF NOT EXISTS idx_instances_provider_at ON instances (provider_at)`,
		`CREATE INDEX IF NOT EXISTS idx_public_keys_instance_id ON public_keys (instance_id)`,
		`CREATE INDEX IF NOT EXISTS idx_public_keys_filename ON public_keys (filename)`,
		`CREATE INDEX IF NOT EXISTS idx_lb_instances_status ON lb_instances (status)`,
		`CREATE INDEX IF NOT EXISTS idx_lb_instances_lb_key ON lb_instances (lb_key)`,
	}

	tables := []string{instancesTable, publicKeysTable, imagesTable, lbInstancesTable, groupsTable, operatorsTable}

	for _, tableSQL := range tables {
		if _, err := db.conn.Exec(tableSQL); err != nil {
			return err
		}
	}

	for _, indexSQL := range indexes {
		if _, err := db.conn.Exec(indexSQL); err != nil {
			return err
		}
	}

	return nil
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanInstance(s scanner, i *Instance) error {
	return s.Scan(
		&i.ID,
		&i.Tenant,
		&i.Group,
		&i.OnDemand,
		&i.ProviderID,
		&i.ProviderAt,
		&i.Hostname,
		&i.FQDN,
		&i.IP4,
		&i.IP6,
		&i.ProviderState,
		&i.Nonce,
		&i.IssuedAt,
		&i.InstancePub,
		&i.RegisteredAt,
		&i.CertificatesAt,
		&i.HealthAt,
		&i.Health,
		&i.InfraConfigHash,
		&i.DrainStartedAt,
		&i.DrainAckedAt,
		&i.CreatedAt,
		&i.UpdatedAt,
		&i.DeletedAt,
	)
}

const instanceColumns = `id, tenant, group_key, on_demand, provider_id, provider_at, hostname, fqdn, ip4, ip6, provider_state, nonce, issued_at, instance_pub, registered_at, certificates_at, health_at, health, infra_config_hash, drain_started_at, drain_acked_at, created_at, updated_at, deleted_at`
