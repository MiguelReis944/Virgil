package storage

import (
	"context"
	"time"
)

func (j *Journal) Prune(ctx context.Context, before time.Time) (int64, error) {
	result, err := j.db.ExecContext(ctx, `DELETE FROM events
		WHERE created_at_unix_ns < ?
		AND NOT EXISTS (
			SELECT 1 FROM outbox
			WHERE outbox.event_id = events.event_id
			AND outbox.state <> 'delivered'
		)`, before.UTC().UnixNano())
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	// Prune completed policy_runs whose start time is before the cutoff.
	// Reservations cascade-delete via the FK on run_id.
	beforeNs := before.UTC().UnixNano()
	if _, err := j.db.ExecContext(ctx, `DELETE FROM policy_runs WHERE started_at_unix_ns < ?`, beforeNs); err != nil {
		return n, err
	}
	return n, nil
}
