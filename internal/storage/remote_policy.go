package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LoadRemotePolicy returns nil payload when no policy has been cached.
func (j *Journal) LoadRemotePolicy(ctx context.Context) (int64, []byte, error) {
	var version int64
	var payload []byte
	err := j.db.QueryRowContext(ctx, `SELECT version, payload FROM remote_policy_cache WHERE singleton = 1`).Scan(&version, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil
	}
	return version, payload, err
}

// SaveRemotePolicy never replaces a newer cached version.
func (j *Journal) SaveRemotePolicy(ctx context.Context, version int64, payload []byte) error {
	if version <= 0 || len(payload) == 0 {
		return errors.New("valid remote policy version and payload required")
	}
	result, err := j.db.ExecContext(ctx, `INSERT INTO remote_policy_cache(singleton, version, payload) VALUES (1, ?, ?)
		ON CONFLICT(singleton) DO UPDATE SET version = excluded.version, payload = excluded.payload
		WHERE excluded.version > remote_policy_cache.version`, version, payload)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("remote policy version %d is stale", version)
	}
	return nil
}
