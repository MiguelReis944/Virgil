package storage

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

const maxAttempts = 10
const leaseDuration = 5 * time.Minute

// Delivery is an outbox row leased for sending.
type Delivery struct {
	Destination    string
	EventID        string
	IdempotencyKey string
	Event          telemetry.Event
	Attempts       int
}

// Lease moves up to limit pending/retryable rows for destination into 'sending'
// and returns them. Rows whose lease_until_unix_ns has expired are re-leased.
func (j *Journal) Lease(ctx context.Context, destination string, limit int, now time.Time) ([]Delivery, error) {
	leaseUntil := now.Add(leaseDuration).UTC().UnixNano()
	nowNs := now.UTC().UnixNano()
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT o.event_id, o.idempotency_key, o.attempts, e.payload
		FROM outbox o
		JOIN events e ON e.event_id = o.event_id
		WHERE o.destination = ?
		  AND (
			(o.state = 'pending' AND o.next_attempt_unix_ns <= ?)
			OR (o.state = 'retryable' AND o.next_attempt_unix_ns <= ?)
			OR (o.state = 'sending' AND o.lease_until_unix_ns IS NOT NULL AND o.lease_until_unix_ns < ?)
		  )
		ORDER BY o.next_attempt_unix_ns
		LIMIT ?`,
		destination, nowNs, nowNs, nowNs, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []Delivery
	for rows.Next() {
		var d Delivery
		var payload []byte
		d.Destination = destination
		if err := rows.Scan(&d.EventID, &d.IdempotencyKey, &d.Attempts, &payload); err != nil {
			return nil, err
		}
		d.Event, err = telemetry.DecodeEvent(strings.NewReader(string(payload)))
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	for _, d := range deliveries {
		if _, err := tx.ExecContext(ctx, `
			UPDATE outbox SET state='sending', lease_until_unix_ns=? WHERE destination=? AND event_id=?`,
			leaseUntil, destination, d.EventID,
		); err != nil {
			return nil, err
		}
	}
	return deliveries, tx.Commit()
}

// Ack marks the given event IDs as delivered for destination.
// Only IDs currently in 'sending' state for this destination are updated.
func (j *Journal) Ack(ctx context.Context, destination string, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range eventIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE outbox SET state='delivered', lease_until_unix_ns=NULL
			WHERE destination=? AND event_id=? AND state='sending'`,
			destination, id,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Fail records a delivery failure. After maxAttempts it moves the row to dead_letter;
// otherwise it schedules an exponential backoff retry with jitter.
func (j *Journal) Fail(ctx context.Context, destination, eventID, errorCode string, now time.Time) error {
	if errorCode == "" || len(errorCode) > 64 {
		return errors.New("invalid error code")
	}
	var attempts int
	err := j.db.QueryRowContext(ctx,
		"SELECT attempts FROM outbox WHERE destination=? AND event_id=?",
		destination, eventID,
	).Scan(&attempts)
	if err != nil {
		return err
	}
	attempts++
	if attempts >= maxAttempts {
		_, err = j.db.ExecContext(ctx, `
			UPDATE outbox SET state='dead_letter', attempts=?, last_error_code=?, lease_until_unix_ns=NULL
			WHERE destination=? AND event_id=?`,
			attempts, errorCode, destination, eventID,
		)
		return err
	}
	// Exponential backoff: 2^attempts seconds + random jitter up to 30s
	backoffSec := int64(1) << attempts
	jitter := rand.Int64N(30)
	nextAttempt := now.Add(time.Duration(backoffSec+jitter) * time.Second).UTC().UnixNano()
	_, err = j.db.ExecContext(ctx, `
		UPDATE outbox SET state='retryable', attempts=?, last_error_code=?, next_attempt_unix_ns=?, lease_until_unix_ns=NULL
		WHERE destination=? AND event_id=?`,
		attempts, errorCode, nextAttempt, destination, eventID,
	)
	return err
}

// DeadLetter stops retrying a permanently rejected delivery.
func (j *Journal) DeadLetter(ctx context.Context, destination, eventID, errorCode string) error {
	if errorCode == "" || len(errorCode) > 64 {
		return errors.New("invalid error code")
	}
	_, err := j.db.ExecContext(ctx, `UPDATE outbox
		SET state='dead_letter', attempts=attempts+1, last_error_code=?, lease_until_unix_ns=NULL
		WHERE destination=? AND event_id=? AND state='sending'`, errorCode, destination, eventID)
	return err
}

// OutboxStats returns counts per state for a destination (for diagnostics).
func (j *Journal) OutboxStats(ctx context.Context, destination string) (map[string]int64, error) {
	rows, err := j.db.QueryContext(ctx,
		"SELECT state, COUNT(*) FROM outbox WHERE destination=? GROUP BY state", destination,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]int64)
	for rows.Next() {
		var state string
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		result[state] = count
	}
	return result, rows.Err()
}

// List returns up to limit events starting after cursor (event_id).
// Returns the next cursor (empty if no more).
func (j *Journal) List(ctx context.Context, cursor string, limit int) ([]telemetry.Event, string, error) {
	if limit <= 0 || limit > 1000 {
		return nil, "", errors.New("limit must be 1–1000")
	}
	var query string
	var args []any
	if cursor == "" {
		query = "SELECT event_id, payload FROM events ORDER BY created_at_unix_ns, event_id LIMIT ?"
		args = []any{limit + 1}
	} else {
		var afterNs int64
		if scanErr := j.db.QueryRowContext(ctx,
			"SELECT created_at_unix_ns FROM events WHERE event_id=?", cursor,
		).Scan(&afterNs); scanErr != nil {
			return nil, "", scanErr
		}
		query = `SELECT event_id, payload FROM events
			WHERE created_at_unix_ns > ? OR (created_at_unix_ns = ? AND event_id > ?)
			ORDER BY created_at_unix_ns, event_id LIMIT ?`
		args = []any{afterNs, afterNs, cursor, limit + 1}
	}
	rows, err := j.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var events []telemetry.Event
	for rows.Next() {
		var id string
		var payload []byte
		if err := rows.Scan(&id, &payload); err != nil {
			return nil, "", err
		}
		ev, err := telemetry.DecodeEvent(strings.NewReader(string(payload)))
		if err != nil {
			return nil, "", err
		}
		events = append(events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var nextCursor string
	if len(events) > limit {
		events = events[:limit]
		nextCursor = events[limit-1].EventID
	}
	return events, nextCursor, nil
}

// ensure json is used (payload encoding in tests)
var _ = json.Marshal
