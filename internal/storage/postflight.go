package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiguelReis944/Virgil/internal/redaction"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

// PostflightAndAppend runs ReconcilePolicy, IncrementDailyCounters, and the
// event append in a single SQLite transaction, cutting three separate commits
// down to one.
func (j *Journal) PostflightAndAppend(ctx context.Context, outcome PolicyOutcome, event telemetry.Event, destinations []string, costUSD string) error {
	if outcome.ReservationID == "" || outcome.RunID == "" {
		return errors.New("run and reservation IDs are required")
	}
	if event.ContentCapture || event.InstallationID != j.installationID || event.EventID == "" {
		return errors.New("invalid event for journal")
	}
	prepared, err := redaction.Prepare(event)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		return err
	}

	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := reconcilePolicyTx(ctx, tx, outcome); err != nil {
		return err
	}
	if err := incrementDailyCountersTx(ctx, tx, costUSD); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO events(event_id, installation_id, created_at_unix_ns, status, payload) VALUES (?, ?, ?, ?, ?)",
		event.EventID, event.InstallationID, event.CreatedAt.UnixNano(), event.Status, encoded,
	); err != nil {
		return err
	}
	for _, destination := range destinations {
		if destination == "" || len(destination) > 64 {
			return errors.New("invalid destination")
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO outbox(destination, event_id, idempotency_key, state, next_attempt_unix_ns) VALUES (?, ?, ?, 'pending', ?)",
			destination, event.EventID, event.InstallationID+":"+event.EventID, time.Now().UTC().UnixNano(),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
