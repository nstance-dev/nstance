// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package localdb

import (
	"database/sql"
	"fmt"
	"time"
)

// Image represents a cached image resolution
type Image struct {
	Name       string
	ImageID    string
	ResolvedAt time.Time
}

// GetImages retrieves all cached image resolutions
func (db *DB) GetImages() (map[string]string, error) {
	rows, err := db.conn.Query(`
		SELECT name, image_id
		FROM images
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("querying images: %w", err)
	}
	defer func() { _ = rows.Close() }()

	images := make(map[string]string)
	for rows.Next() {
		var name, imageID string
		if err := rows.Scan(&name, &imageID); err != nil {
			return nil, fmt.Errorf("scanning image row: %w", err)
		}
		images[name] = imageID
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating image rows: %w", err)
	}

	return images, nil
}

// UpsertImages batch inserts or updates multiple image resolutions
func (db *DB) UpsertImages(images map[string]string, resolvedAt time.Time) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`
		INSERT INTO images (name, image_id, resolved_at)
		VALUES (?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			image_id = excluded.image_id,
			resolved_at = excluded.resolved_at
	`)
	if err != nil {
		return fmt.Errorf("preparing statement: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for name, imageID := range images {
		if _, err := stmt.Exec(name, imageID, resolvedAt); err != nil {
			return fmt.Errorf("upserting image %s: %w", name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	return nil
}

// GetImage retrieves a single cached image resolution
func (db *DB) GetImage(name string) (string, time.Time, error) {
	var imageID string
	var resolvedAt time.Time

	err := db.conn.QueryRow(`
		SELECT image_id, resolved_at
		FROM images
		WHERE name = ?
	`, name).Scan(&imageID, &resolvedAt)

	if err == sql.ErrNoRows {
		return "", time.Time{}, fmt.Errorf("image %s not found in cache", name)
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("querying image %s: %w", name, err)
	}

	return imageID, resolvedAt, nil
}
