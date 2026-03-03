// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package localdb

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (db *DB) CreateInstance(instance *Instance) error {
	query := `
	INSERT INTO instances (
		id, tenant, group_key, on_demand, provider_id, provider_at, hostname, fqdn, ip4, ip6, provider_state, nonce, issued_at,
		instance_pub, registered_at, certificates_at, health_at, health, infra_config_hash,
		created_at, updated_at, deleted_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := db.conn.Exec(query,
		instance.ID,
		instance.Tenant,
		instance.Group,
		instance.OnDemand,
		instance.ProviderID,
		instance.ProviderAt,
		instance.Hostname,
		instance.FQDN,
		instance.IP4,
		instance.IP6,
		instance.ProviderState,
		instance.Nonce,
		instance.IssuedAt,
		instance.InstancePub,
		instance.RegisteredAt,
		instance.CertificatesAt,
		instance.HealthAt,
		instance.Health,
		instance.InfraConfigHash,
		instance.CreatedAt,
		instance.UpdatedAt,
		instance.DeletedAt,
	)

	return err
}

func (db *DB) GetInstance(instanceID string) (*Instance, error) {
	query := `SELECT ` + instanceColumns + ` FROM instances WHERE id = ? AND deleted_at IS NULL`

	instance := &Instance{}
	err := scanInstance(db.conn.QueryRow(query, instanceID), instance)
	if err != nil {
		return nil, err
	}

	return instance, nil
}

func (db *DB) GetInstanceByProviderID(providerID string) (*Instance, error) {
	query := `SELECT ` + instanceColumns + ` FROM instances WHERE provider_id = ? AND deleted_at IS NULL`

	instance := &Instance{}
	err := scanInstance(db.conn.QueryRow(query, providerID), instance)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return instance, nil
}

func (db *DB) UpdateInstance(instance *Instance) error {
	now := time.Now().UTC()
	instance.UpdatedAt = &now

	query := `
	UPDATE instances SET
		group_key = ?, on_demand = ?, provider_id = ?, provider_at = ?, hostname = ?, fqdn = ?, ip4 = ?, ip6 = ?, provider_state = ?,
		instance_pub = ?, registered_at = ?, certificates_at = ?,
		health_at = ?, health = ?, updated_at = ?, deleted_at = ?
	WHERE id = ?
	`

	result, err := db.conn.Exec(query,
		instance.Group,
		instance.OnDemand,
		instance.ProviderID,
		instance.ProviderAt,
		instance.Hostname,
		instance.FQDN,
		instance.IP4,
		instance.IP6,
		instance.ProviderState,
		instance.InstancePub,
		instance.RegisteredAt,
		instance.CertificatesAt,
		instance.HealthAt,
		instance.Health,
		instance.UpdatedAt,
		instance.DeletedAt,
		instance.ID,
	)

	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

func (db *DB) ListInstances() ([]*Instance, error) {
	query := `SELECT ` + instanceColumns + ` FROM instances WHERE deleted_at IS NULL ORDER BY created_at DESC`

	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var instances []*Instance
	for rows.Next() {
		instance := &Instance{}
		if err := scanInstance(rows, instance); err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}

	return instances, rows.Err()
}

func (db *DB) DeleteInstance(instanceID string) error {
	now := time.Now().UTC()

	query := `UPDATE instances SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`

	result, err := db.conn.Exec(query, now, now, instanceID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// FindDeletedInstancesPastRetention returns instances that were deleted before the cutoff time.
// These instances are eligible for S3 record cleanup.
func (db *DB) FindDeletedInstancesPastRetention(cutoff time.Time) ([]*Instance, error) {
	query := `SELECT ` + instanceColumns + ` FROM instances WHERE deleted_at IS NOT NULL AND deleted_at < ?`

	rows, err := db.conn.Query(query, cutoff)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var instances []*Instance
	for rows.Next() {
		instance := &Instance{}
		if err := scanInstance(rows, instance); err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}

	return instances, rows.Err()
}

// PurgeDeletedInstance permanently removes a deleted instance record from the database.
// This should only be called after the S3 record has been deleted.
func (db *DB) PurgeDeletedInstance(instanceID string) error {
	query := `DELETE FROM instances WHERE id = ? AND deleted_at IS NOT NULL`

	result, err := db.conn.Exec(query, instanceID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// ValidateAgentNonce checks if an agent nonce is valid
// Agent nonces must exist in instances table (server created the record) and not be registered yet
func (db *DB) ValidateAgentNonce(nonce string) error {
	query := `SELECT registered_at FROM instances WHERE nonce = ?`

	var registeredAt *time.Time
	err := db.conn.QueryRow(query, nonce).Scan(&registeredAt)
	if err == sql.ErrNoRows {
		return fmt.Errorf("nonce not found")
	}
	if err != nil {
		return err
	}

	if registeredAt != nil {
		return fmt.Errorf("nonce already used")
	}

	return nil
}

// GetInstanceByNonce returns an instance by its registration nonce JWT
func (db *DB) GetInstanceByNonce(nonce string) (*Instance, error) {
	query := `SELECT ` + instanceColumns + ` FROM instances WHERE nonce = ? AND deleted_at IS NULL`
	instance := &Instance{}
	err := scanInstance(db.conn.QueryRow(query, nonce), instance)
	if err != nil {
		return nil, err
	}
	return instance, nil
}

// MarkInstanceRegistered marks an instance as registered with authoritative IPs/hostname from agent
func (db *DB) MarkInstanceRegistered(instanceID string, publicKeyPEM []byte, privateIPv4, privateIPv6, hostname string) error {
	now := time.Now().UTC()
	publicKeyStr := string(publicKeyPEM)
	var hostnamePtr *string
	if hostname != "" {
		hostnamePtr = &hostname
	}
	_, err := db.conn.Exec(`
		UPDATE instances SET instance_pub = ?, registered_at = ?, ip4 = ?, ip6 = ?, hostname = ?, updated_at = ?
		WHERE id = ? AND deleted_at IS NULL`,
		publicKeyStr, now, privateIPv4, privateIPv6, hostnamePtr, now, instanceID)
	return err
}

// CreateAgentRegistrationRecord updates an existing instance record to mark it as registered
func (db *DB) CreateAgentRegistrationRecord(instanceID, nonce string, publicKeyPEM []byte) error {
	now := time.Now().UTC()
	publicKeyStr := string(publicKeyPEM)

	query := `UPDATE instances SET instance_pub = ?, registered_at = ?, updated_at = ? WHERE id = ? AND nonce = ? AND deleted_at IS NULL`

	result, err := db.conn.Exec(query, publicKeyStr, now, now, instanceID, nonce)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

func (db *DB) UpdateInstanceHealth(instanceID string, health []byte) error {
	now := time.Now().UTC()

	query := `UPDATE instances SET health = ?, health_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`

	result, err := db.conn.Exec(query, health, now, now, instanceID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// UpdateInstanceProviderState updates only the provider_state field for an instance.
// This is used to sync the latest status from the provider before making decisions based on it.
func (db *DB) UpdateInstanceProviderState(instanceID string, providerState []byte) error {
	now := time.Now().UTC()

	query := `UPDATE instances SET provider_state = ?, provider_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`

	result, err := db.conn.Exec(query, providerState, now, now, instanceID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// SeedFromProviderData updates existing instance records with current IP/hostname from provider.
func (db *DB) SeedFromProviderData(instances []*Instance) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	updateQuery := `
		UPDATE instances SET 
			provider_id = ?, hostname = COALESCE(?, hostname), ip4 = COALESCE(?, ip4), ip6 = COALESCE(?, ip6), provider_state = ?, provider_at = ?, updated_at = ?
		WHERE id = ?
	`

	now := time.Now().UTC()
	for _, instance := range instances {
		_, err := tx.Exec(updateQuery,
			instance.ProviderID,
			instance.Hostname,
			instance.IP4,
			instance.IP6,
			instance.ProviderState,
			now,
			now,
			instance.ID,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (db *DB) SeedFromS3Data(instances []*Instance) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Use a consistent timestamp for this sync operation
	syncTime := time.Now().UTC()

	// Prepare the upsert query
	query := `
	INSERT INTO instances (
		id, tenant, group_key, on_demand, provider_id, provider_at, hostname, fqdn, ip4, ip6, nonce, issued_at,
		instance_pub, registered_at, certificates_at, health_at, health,
		created_at, updated_at, deleted_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		tenant = excluded.tenant,
		group_key = excluded.group_key,
		on_demand = excluded.on_demand,
		provider_id = COALESCE(excluded.provider_id, instances.provider_id),
		provider_at = COALESCE(excluded.provider_at, instances.provider_at),
		hostname = excluded.hostname,
		fqdn = excluded.fqdn,
		ip4 = excluded.ip4,
		ip6 = excluded.ip6,
		nonce = excluded.nonce,
		issued_at = excluded.issued_at,
		instance_pub = excluded.instance_pub,
		registered_at = excluded.registered_at,
		certificates_at = excluded.certificates_at,
		health_at = excluded.health_at,
		health = excluded.health,
		updated_at = excluded.updated_at,
		deleted_at = excluded.deleted_at
	`

	for _, instance := range instances {
		// Update the timestamp to mark this record as 'seen' in this sync
		// We use the syncTime to ensure we can identify which records were updated
		instance.UpdatedAt = &syncTime

		_, err := tx.Exec(query,
			instance.ID,
			instance.Tenant,
			instance.Group,
			instance.OnDemand,
			instance.ProviderID,
			instance.ProviderAt,
			instance.Hostname,
			instance.FQDN,
			instance.IP4,
			instance.IP6,
			instance.Nonce,
			instance.IssuedAt,
			instance.InstancePub,
			instance.RegisteredAt,
			instance.CertificatesAt,
			instance.HealthAt,
			instance.Health,
			instance.CreatedAt,
			instance.UpdatedAt,
			instance.DeletedAt,
		)
		if err != nil {
			return err
		}
	}

	// Delete instances that are registered (have registered_at) but were NOT updated
	// in this sync (meaning they are no longer present in S3).
	// We do NOT delete instances where registered_at IS NULL, as these are likely
	// provider-only instances (unregistered) that we want to preserve.
	deleteQuery := `
		DELETE FROM instances 
		WHERE registered_at IS NOT NULL 
		AND updated_at < ?
	`
	if _, err := tx.Exec(deleteQuery, syncTime); err != nil {
		return err
	}

	return tx.Commit()
}

// GetProviderIDsByGroup returns sorted provider IDs for all active instances in a group.
// If excludeOnDemand is true, on-demand instances (created via CreateInstance/Machine) are
// excluded since their lifecycle is managed by individual Machine/NstanceMachine resources.
func (db *DB) GetProviderIDsByGroup(groupKey string, excludeOnDemand bool) ([]string, error) {
	query := `
		SELECT provider_id
		FROM instances
		WHERE group_key = ?
		AND deleted_at IS NULL
		AND provider_id IS NOT NULL
		AND (provider_state IS NULL OR json_extract(provider_state, '$.status') NOT IN ('deleting', 'deleted'))
	`
	if excludeOnDemand {
		query += ` AND on_demand = 0`
	}
	query += ` ORDER BY provider_id ASC`

	rows, err := db.conn.Query(query, groupKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var providerIDs []string
	for rows.Next() {
		var providerID string
		if err := rows.Scan(&providerID); err != nil {
			return nil, err
		}
		providerIDs = append(providerIDs, providerID)
	}

	return providerIDs, rows.Err()
}

// GetInstancesByGroup returns all active instances for a group, ordered by creation time (oldest first)
// If excludeOnDemand is true, on-demand instances are excluded from the results
func (db *DB) GetInstancesByGroup(groupKey string, excludeOnDemand bool) ([]string, error) {
	query := `
		SELECT id
		FROM instances
		WHERE group_key = ?
		AND deleted_at IS NULL
		AND (provider_state IS NULL OR json_extract(provider_state, '$.status') NOT IN ('deleting', 'deleted'))
	`
	args := []any{groupKey}

	if excludeOnDemand {
		query += " AND on_demand = 0"
	}

	query += " ORDER BY created_at ASC"

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var instanceIDs []string
	for rows.Next() {
		var instanceID string
		if err := rows.Scan(&instanceID); err != nil {
			return nil, err
		}
		instanceIDs = append(instanceIDs, instanceID)
	}

	return instanceIDs, rows.Err()
}

// GetOldestManagedInstanceByGroup returns the oldest non-draining managed (not on-demand) instance for a group
// Returns nil if no eligible instances exist
func (db *DB) GetOldestManagedInstanceByGroup(groupKey string) (*Instance, error) {
	query := `
		SELECT ` + instanceColumns + `
		FROM instances
		WHERE group_key = ?
		AND deleted_at IS NULL
		AND on_demand = 0
		AND drain_started_at IS NULL
		AND (provider_state IS NULL OR json_extract(provider_state, '$.status') NOT IN ('deleting', 'deleted'))
		ORDER BY created_at ASC
		LIMIT 1
	`

	instance := &Instance{}
	err := scanInstance(db.conn.QueryRow(query, groupKey), instance)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}

	return instance, nil
}

// GetInstanceCountByGroup returns the count of active instances for a group
// If excludeOnDemand is true, on-demand instances are excluded from the count
func (db *DB) GetInstanceCountByGroup(groupKey string, excludeOnDemand bool) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM instances
		WHERE group_key = ?
		AND deleted_at IS NULL
		AND (provider_state IS NULL OR json_extract(provider_state, '$.status') NOT IN ('deleting', 'deleted'))
	`
	args := []any{groupKey}

	if excludeOnDemand {
		query += " AND on_demand = 0"
	}

	var count int
	err := db.conn.QueryRow(query, args...).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

func (db *DB) MarkDrainStarted(instanceID string) error {
	now := time.Now().UTC()

	query := `UPDATE instances SET drain_started_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`

	result, err := db.conn.Exec(query, now, now, instanceID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

func (db *DB) MarkDrainAcked(instanceID string) error {
	now := time.Now().UTC()

	query := `UPDATE instances SET drain_acked_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`

	result, err := db.conn.Exec(query, now, now, instanceID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

func (db *DB) GetInstancesPendingDrain(tenant string) ([]*Instance, error) {
	if tenant == "" {
		return nil, fmt.Errorf("tenant is required")
	}

	query := `
		SELECT ` + instanceColumns + `
		FROM instances
		WHERE tenant = ?
		AND drain_started_at IS NOT NULL
		AND deleted_at IS NULL
		ORDER BY drain_started_at ASC
	`

	rows, err := db.conn.Query(query, tenant)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var instances []*Instance
	for rows.Next() {
		instance := &Instance{}
		if err := scanInstance(rows, instance); err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}

	return instances, rows.Err()
}

// FindDanglingInstances finds unregistered instances past timeout.
// These are instances where the agent never registered before maxAge elapsed.
// SQLite is already filtered to our shard (populated from S3 + provider on leader election).
func (db *DB) FindDanglingInstances(maxAge time.Duration) ([]*Instance, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	query := `
		SELECT ` + instanceColumns + `
		FROM instances
		WHERE registered_at IS NULL
		AND deleted_at IS NULL
		AND issued_at IS NOT NULL
		AND issued_at < ?
		ORDER BY issued_at ASC
	`

	rows, err := db.conn.Query(query, cutoff)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var instances []*Instance
	for rows.Next() {
		instance := &Instance{}
		if err := scanInstance(rows, instance); err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}

	return instances, rows.Err()
}

// GetInstancesOlderThan returns instances older than the specified duration
// If ondemandOnly is true, only on-demand instances are returned; if false, only managed instances are returned
func (db *DB) GetInstancesOlderThan(maxAge time.Duration, ondemandOnly bool) ([]*Instance, error) {
	cutoff := time.Now().Add(-maxAge)

	query := `
		SELECT ` + instanceColumns + `
		FROM instances
		WHERE created_at < ?
		AND deleted_at IS NULL
		AND (provider_state IS NULL OR json_extract(provider_state, '$.status') NOT IN ('deleting', 'deleted'))
	`
	args := []any{cutoff}

	if ondemandOnly {
		query += " AND on_demand = 1"
	} else {
		query += " AND on_demand = 0"
	}

	query += " ORDER BY created_at ASC"

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var instances []*Instance
	for rows.Next() {
		instance := &Instance{}
		if err := scanInstance(rows, instance); err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}

	return instances, rows.Err()
}

// MarkInstancesDeleted marks the given instance IDs as deleted
func (db *DB) MarkInstancesDeleted(instanceIDs []string) error {
	if len(instanceIDs) == 0 {
		return nil
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	query := `UPDATE instances SET deleted_at = ?, updated_at = ? WHERE id = ?`

	for _, id := range instanceIDs {
		if _, err := tx.Exec(query, now, now, id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// MarkInstancesDeletedIfProviderMissing marks instances as deleted if their provider_id
// is not in the provided set of existing provider IDs. This handles the case where
// VM instances were terminated while nstance-server was down.
func (db *DB) MarkInstancesDeletedIfProviderMissing(providerOwnership map[string]string) ([]string, error) {
	// Get all instances with a provider_id that are not already deleted
	query := `SELECT id, provider_id FROM instances WHERE provider_id IS NOT NULL AND deleted_at IS NULL`

	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var instanceIDsToDelete []string
	for rows.Next() {
		var id string
		var providerID string
		if err := rows.Scan(&id, &providerID); err != nil {
			return nil, err
		}

		ownerID, exists := providerOwnership[providerID]
		if !exists || ownerID != id {
			instanceIDsToDelete = append(instanceIDsToDelete, id)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(instanceIDsToDelete) == 0 {
		return nil, nil
	}

	return instanceIDsToDelete, db.MarkInstancesDeleted(instanceIDsToDelete)
}

// QueryStaleHealthInstances returns instances with stale health_at timestamps
func (db *DB) QueryStaleHealthInstances(threshold time.Time) ([]*Instance, error) {
	query := `
		SELECT ` + instanceColumns + `
		FROM instances
		WHERE deleted_at IS NULL
		AND registered_at IS NOT NULL
		AND health_at IS NOT NULL
		AND health_at < ?
		ORDER BY health_at ASC
	`

	rows, err := db.conn.Query(query, threshold)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var instances []*Instance
	for rows.Next() {
		instance := &Instance{}
		if err := scanInstance(rows, instance); err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}

	return instances, rows.Err()
}

// FindStalePreInserts finds pre-inserted instance records where the provider call never completed.
// These are identified by:
// - ProviderID IS NULL (provider never created the instance)
// - IssuedAt < (now - maxAge) (older than threshold)
// - DeletedAt IS NULL (not already deleted)
func (db *DB) FindStalePreInserts(maxAge time.Duration) ([]*Instance, error) {
	cutoff := time.Now().UTC().Add(-maxAge)
	query := `
		SELECT ` + instanceColumns + `
		FROM instances
		WHERE provider_id IS NULL
		AND issued_at IS NOT NULL
		AND issued_at < ?
		AND deleted_at IS NULL
	`

	rows, err := db.conn.Query(query, cutoff)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var instances []*Instance
	for rows.Next() {
		instance := &Instance{}
		if err := scanInstance(rows, instance); err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}

	return instances, rows.Err()
}

// HighestProviderID returns the highest numeric provider_id across all instances
// (including soft-deleted ones). Where the provider uses numeric provider
// instance IDs, and allows the client to specify the provider ID (i.e. in
// Proxmox VE), this can be used to seed provider ID counters so that
// recently deleted VM IDs are not immediately reused
func (db *DB) HighestProviderID(ctx context.Context) (int64, error) {
	query := `SELECT COALESCE(MAX(CAST(provider_id AS INTEGER)), 0) FROM instances WHERE provider_id IS NOT NULL`

	var highest int64
	if err := db.conn.QueryRowContext(ctx, query).Scan(&highest); err != nil {
		return 0, err
	}

	return highest, nil
}

// FindUnhealthyProviderInstances finds instances where the provider reports an unhealthy status.
func (db *DB) FindUnhealthyProviderInstances() ([]*Instance, error) {
	query := `
		SELECT ` + instanceColumns + `
		FROM instances
		WHERE deleted_at IS NULL
		AND provider_id IS NOT NULL
		AND provider_state IS NOT NULL
		AND json_extract(provider_state, '$.status') IN ('stopping', 'stopped', 'suspending', 'suspended', 'deleting', 'deleted', 'repairing')
	`

	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var instances []*Instance
	for rows.Next() {
		instance := &Instance{}
		if err := scanInstance(rows, instance); err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}

	return instances, rows.Err()
}
