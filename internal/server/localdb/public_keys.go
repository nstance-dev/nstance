// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package localdb

import (
	"database/sql"
	"fmt"
	"time"
)

// StorePublicKeys stores public keys transactionally - all keys must be stored successfully or none are stored
func (db *DB) StorePublicKeys(instanceID string, keys []*PublicKeySubmission) error {
	if len(keys) == 0 {
		return nil
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	query := `
		INSERT OR REPLACE INTO public_keys (
			instance_id, filename, public_key_pem, submitted_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`

	now := time.Now().UTC()
	for _, key := range keys {
		_, err := tx.Exec(query,
			instanceID,
			key.Filename,
			key.PublicKeyPEM,
			now,
			now,
			now,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetPendingPublicKeys retrieves all unprocessed public keys for an instance
func (db *DB) GetPendingPublicKeys(instanceID string) ([]*PublicKey, error) {
	query := `
		SELECT id, instance_id, filename, public_key_pem, certificate_name, submitted_at, 
		       processed_at, certificate_serial, certificate_issued_at, created_at, updated_at
		FROM public_keys 
		WHERE instance_id = ? AND processed_at IS NULL
		ORDER BY submitted_at ASC
	`

	rows, err := db.conn.Query(query, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var publicKeys []*PublicKey
	for rows.Next() {
		pk := &PublicKey{}
		err := rows.Scan(
			&pk.ID,
			&pk.InstanceID,
			&pk.Filename,
			&pk.PublicKeyPEM,
			&pk.CertificateName,
			&pk.SubmittedAt,
			&pk.ProcessedAt,
			&pk.CertificateSerial,
			&pk.CertificateIssuedAt,
			&pk.CreatedAt,
			&pk.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		publicKeys = append(publicKeys, pk)
	}

	return publicKeys, rows.Err()
}

// MarkPublicKeysProcessed marks public keys as processed with certificate serial numbers
func (db *DB) MarkPublicKeysProcessed(instanceID string, filenames []string, serialNumbers []string) error {
	if len(filenames) != len(serialNumbers) {
		return fmt.Errorf("filenames and serial numbers count mismatch: %d != %d", len(filenames), len(serialNumbers))
	}

	if len(filenames) == 0 {
		return nil
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	query := `
		UPDATE public_keys 
		SET processed_at = ?, certificate_serial = ?, certificate_issued_at = ?, updated_at = ?
		WHERE instance_id = ? AND filename = ? AND processed_at IS NULL
	`

	now := time.Now().UTC()
	for i, filename := range filenames {
		result, err := tx.Exec(query, now, serialNumbers[i], now, now, instanceID, filename)
		if err != nil {
			return err
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return err
		}

		if rowsAffected == 0 {
			return fmt.Errorf("public key %s not found or already processed for instance %s", filename, instanceID)
		}
	}

	return tx.Commit()
}

// GetPublicKeyByFilename retrieves a specific public key by instance ID and filename
func (db *DB) GetPublicKeyByFilename(instanceID, filename string) (*PublicKey, error) {
	query := `
		SELECT id, instance_id, filename, public_key_pem, certificate_name, submitted_at, 
		       processed_at, certificate_serial, certificate_issued_at, created_at, updated_at
		FROM public_keys 
		WHERE instance_id = ? AND filename = ?
		ORDER BY submitted_at DESC
		LIMIT 1
	`

	pk := &PublicKey{}
	err := db.conn.QueryRow(query, instanceID, filename).Scan(
		&pk.ID,
		&pk.InstanceID,
		&pk.Filename,
		&pk.PublicKeyPEM,
		&pk.CertificateName,
		&pk.SubmittedAt,
		&pk.ProcessedAt,
		&pk.CertificateSerial,
		&pk.CertificateIssuedAt,
		&pk.CreatedAt,
		&pk.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return pk, nil
}
