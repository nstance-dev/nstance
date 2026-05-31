// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package localdb

import (
	"fmt"
	"time"

	"github.com/refreshjs/puidv7"
)

// Operator represents an operator registration record in the local database
type Operator struct {
	ID           string     `json:"id"`
	ClusterID    string     `json:"cluster_id"`
	Tenant       string     `json:"tenant"`
	Nonce        string     `json:"nonce"`
	PublicKey    *string    `json:"public_key"`
	RegisteredAt time.Time  `json:"registered_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    *time.Time `json:"updated_at"`
}

// ValidateOperatorNonce checks if an operator nonce is valid.
// Operator nonces must NOT exist in operators table (external nonce, first use).
func (db *DB) ValidateOperatorNonce(nonce string) error {
	query := `SELECT COUNT(*) FROM operators WHERE nonce = ?`

	var count int
	err := db.conn.QueryRow(query, nonce).Scan(&count)
	if err != nil {
		return err
	}

	if count > 0 {
		return fmt.Errorf("nonce already used")
	}

	return nil
}

// CreateOperatorRegistrationRecord inserts a new record for operator registration
func (db *DB) CreateOperatorRegistrationRecord(clusterID, tenant, nonce string, publicKeyPEM []byte) error {
	id, err := puidv7.New("opr")
	if err != nil {
		return fmt.Errorf("generating operator ID: %w", err)
	}

	now := time.Now().UTC()
	publicKeyStr := string(publicKeyPEM)

	query := `
	INSERT INTO operators (id, cluster_id, tenant, nonce, public_key, registered_at, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	_, err = db.conn.Exec(query, id, clusterID, tenant, nonce, publicKeyStr, now, now)
	return err
}

// GetOperator returns an operator by ID
func (db *DB) GetOperator(id string) (*Operator, error) {
	query := `SELECT id, cluster_id, tenant, nonce, public_key, registered_at, created_at, updated_at FROM operators WHERE id = ?`

	op := &Operator{}
	err := db.conn.QueryRow(query, id).Scan(
		&op.ID,
		&op.ClusterID,
		&op.Tenant,
		&op.Nonce,
		&op.PublicKey,
		&op.RegisteredAt,
		&op.CreatedAt,
		&op.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// GetOperatorByClusterID returns the most recent operator for a cluster
func (db *DB) GetOperatorByClusterID(clusterID string) (*Operator, error) {
	query := `SELECT id, cluster_id, tenant, nonce, public_key, registered_at, created_at, updated_at FROM operators WHERE cluster_id = ? ORDER BY created_at DESC LIMIT 1`

	op := &Operator{}
	err := db.conn.QueryRow(query, clusterID).Scan(
		&op.ID,
		&op.ClusterID,
		&op.Tenant,
		&op.Nonce,
		&op.PublicKey,
		&op.RegisteredAt,
		&op.CreatedAt,
		&op.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	return op, nil
}

// UpdateOperatorRegistration updates the public key for an existing operator registration
// This is used during certificate renewal to update the cached registration data
func (db *DB) UpdateOperatorRegistration(clusterID, publicKey string) error {
	now := time.Now().UTC()

	query := `
	UPDATE operators 
	SET public_key = ?, updated_at = ?
	WHERE cluster_id = ?
	`

	result, err := db.conn.Exec(query, publicKey, now, clusterID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("no operator found with cluster_id %s", clusterID)
	}

	return nil
}
