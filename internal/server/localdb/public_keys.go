// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package localdb

import (
	"database/sql"
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

// GetPublicKeyByFilename retrieves a specific public key by instance ID and filename
func (db *DB) GetPublicKeyByFilename(instanceID, filename string) (*PublicKey, error) {
	query := `
		SELECT id, instance_id, filename, public_key_pem, certificate_name, submitted_at,
		       created_at, updated_at
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
