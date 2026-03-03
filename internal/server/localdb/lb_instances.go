// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package localdb

import (
	"database/sql"
	"fmt"
	"time"
)

// LBInstanceStatus represents the status of an instance's load balancer registration
const (
	LBStatusPending      = "pending"
	LBStatusRegistered   = "registered"
	LBStatusDeregistered = "deregistered"
	LBStatusFailed       = "failed"
)

// LBInstance represents a load balancer instance registration record
type LBInstance struct {
	LBKey      string    `json:"lb_key"`
	InstanceID string    `json:"instance_id"`
	Status     string    `json:"status"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// UpsertLBInstance creates or updates a load balancer instance record
func (db *DB) UpsertLBInstance(lbKey, instanceID, status string) error {
	query := `
		INSERT INTO lb_instances (lb_key, instance_id, status, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(lb_key, instance_id) 
		DO UPDATE SET 
			status = excluded.status,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err := db.conn.Exec(query, lbKey, instanceID, status)
	if err != nil {
		return fmt.Errorf("upserting lb_instance: %w", err)
	}

	return nil
}

// GetLBInstancesForInstance returns all load balancer registrations for an instance
func (db *DB) GetLBInstancesForInstance(instanceID string) ([]*LBInstance, error) {
	query := `
		SELECT lb_key, instance_id, status, updated_at
		FROM lb_instances
		WHERE instance_id = ?
	`

	rows, err := db.conn.Query(query, instanceID)
	if err != nil {
		return nil, fmt.Errorf("querying lb_instances: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var lbInstances []*LBInstance
	for rows.Next() {
		var lb LBInstance
		if err := rows.Scan(&lb.LBKey, &lb.InstanceID, &lb.Status, &lb.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning lb_instance: %w", err)
		}
		lbInstances = append(lbInstances, &lb)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating lb_instances: %w", err)
	}

	return lbInstances, nil
}

// GetLBInstance returns a specific load balancer registration
func (db *DB) GetLBInstance(lbKey, instanceID string) (*LBInstance, error) {
	query := `
		SELECT lb_key, instance_id, status, updated_at
		FROM lb_instances
		WHERE lb_key = ? AND instance_id = ?
	`

	var lb LBInstance
	err := db.conn.QueryRow(query, lbKey, instanceID).Scan(
		&lb.LBKey, &lb.InstanceID, &lb.Status, &lb.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying lb_instance: %w", err)
	}

	return &lb, nil
}

// DeleteLBInstancesForInstance deletes all load balancer registrations for an instance
func (db *DB) DeleteLBInstancesForInstance(instanceID string) error {
	query := `DELETE FROM lb_instances WHERE instance_id = ?`

	_, err := db.conn.Exec(query, instanceID)
	if err != nil {
		return fmt.Errorf("deleting lb_instances: %w", err)
	}

	return nil
}

// GetPendingOrFailedLBInstances returns all instances with pending or failed LB registrations
func (db *DB) GetPendingOrFailedLBInstances() ([]*LBInstance, error) {
	query := `
		SELECT lb_key, instance_id, status, updated_at
		FROM lb_instances
		WHERE status IN (?, ?)
		ORDER BY updated_at ASC
	`

	rows, err := db.conn.Query(query, LBStatusPending, LBStatusFailed)
	if err != nil {
		return nil, fmt.Errorf("querying pending/failed lb_instances: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var lbInstances []*LBInstance
	for rows.Next() {
		var lb LBInstance
		if err := rows.Scan(&lb.LBKey, &lb.InstanceID, &lb.Status, &lb.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning lb_instance: %w", err)
		}
		lbInstances = append(lbInstances, &lb)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pending/failed lb_instances: %w", err)
	}

	return lbInstances, nil
}
