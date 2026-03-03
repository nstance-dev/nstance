// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package localdb

import (
	"database/sql"
	"fmt"
	"time"
)

// Group represents a group record with config hashes
type Group struct {
	Tenant            string
	GroupKey          string
	RuntimeConfigHash *string
	InfraConfigHash   *string
	HashesUpdatedAt   *time.Time
	CreatedAt         time.Time
	UpdatedAt         *time.Time
}

// UpsertGroup inserts or updates a group's config hashes
func (db *DB) UpsertGroup(tenant, groupKey, runtimeHash, infraHash string) error {
	if tenant == "" {
		return fmt.Errorf("tenant is required")
	}

	query := `
		INSERT INTO groups (tenant, group_key, runtime_config_hash, infra_config_hash, hashes_updated_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant, group_key) DO UPDATE SET
			runtime_config_hash = excluded.runtime_config_hash,
			infra_config_hash = excluded.infra_config_hash,
			hashes_updated_at = excluded.hashes_updated_at,
			updated_at = ?
	`
	now := time.Now().UTC()
	_, err := db.conn.Exec(query, tenant, groupKey, runtimeHash, infraHash, now, now, now)
	if err != nil {
		return fmt.Errorf("upserting group %s (tenant %s): %w", groupKey, tenant, err)
	}
	return nil
}

// GetGroup retrieves a group by tenant and key
func (db *DB) GetGroup(tenant, groupKey string) (*Group, error) {
	if tenant == "" {
		return nil, fmt.Errorf("tenant is required")
	}

	query := `
		SELECT tenant, group_key, runtime_config_hash, infra_config_hash, hashes_updated_at, created_at, updated_at
		FROM groups
		WHERE tenant = ? AND group_key = ?
	`
	var g Group
	err := db.conn.QueryRow(query, tenant, groupKey).Scan(
		&g.Tenant,
		&g.GroupKey,
		&g.RuntimeConfigHash,
		&g.InfraConfigHash,
		&g.HashesUpdatedAt,
		&g.CreatedAt,
		&g.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting group %s (tenant %s): %w", groupKey, tenant, err)
	}
	return &g, nil
}

// GetAllGroups retrieves all groups for a specific tenant
func (db *DB) GetAllGroups(tenant string) (map[string]*Group, error) {
	if tenant == "" {
		return nil, fmt.Errorf("tenant is required")
	}

	query := `
		SELECT tenant, group_key, runtime_config_hash, infra_config_hash, hashes_updated_at, created_at, updated_at
		FROM groups
		WHERE tenant = ?
	`
	rows, err := db.conn.Query(query, tenant)
	if err != nil {
		return nil, fmt.Errorf("querying groups for tenant %s: %w", tenant, err)
	}
	defer func() { _ = rows.Close() }()

	groups := make(map[string]*Group)
	for rows.Next() {
		var g Group
		if err := rows.Scan(
			&g.Tenant,
			&g.GroupKey,
			&g.RuntimeConfigHash,
			&g.InfraConfigHash,
			&g.HashesUpdatedAt,
			&g.CreatedAt,
			&g.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning group row: %w", err)
		}
		groups[g.GroupKey] = &g
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating group rows: %w", err)
	}

	return groups, nil
}
