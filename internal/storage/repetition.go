package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// LoadRepetitionState reads persisted repetition state for (runID, kind).
// kind is one of "tool_error", "provider_error", "tool_call".
// Returns zero values with no error when no row exists yet.
func (j *Journal) LoadRepetitionState(ctx context.Context, runID, kind string) (count int64, lastValues []string, err error) {
	var raw string
	err = j.readDB.QueryRowContext(ctx,
		"SELECT count, last_values FROM run_repetition_state WHERE run_id=? AND kind=?",
		runID, kind,
	).Scan(&count, &raw)
	if err != nil {
		// sql.ErrNoRows → zero state, no error
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil, nil
		}
		return 0, nil, err
	}
	if raw != "" && raw != "[]" {
		_ = json.Unmarshal([]byte(raw), &lastValues)
	}
	return count, lastValues, nil
}

// SaveRepetitionState upserts repetition state for (runID, kind).
func (j *Journal) SaveRepetitionState(ctx context.Context, runID, kind string, count int64, lastValues []string) error {
	encoded, err := json.Marshal(lastValues)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx,
		`INSERT INTO run_repetition_state(run_id, kind, count, last_values, updated_at_ns)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(run_id, kind) DO UPDATE SET
		   count=excluded.count,
		   last_values=excluded.last_values,
		   updated_at_ns=excluded.updated_at_ns`,
		runID, kind, count, string(encoded), time.Now().UnixNano(),
	)
	return err
}
