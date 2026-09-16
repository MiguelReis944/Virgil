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
	return result.RowsAffected()
}
