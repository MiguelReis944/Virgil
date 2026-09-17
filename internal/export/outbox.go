// Package export delivers outbox events to configured destinations.
package export

import (
	"context"
	"time"

	"github.com/MiguelReis944/Virgil/internal/storage"
)

// Sender delivers a batch of deliveries to one destination and returns acked event IDs.
type Sender interface {
	Send(ctx context.Context, batch []storage.Delivery, allowed []string) ([]string, error)
}

// DrainOnce leases up to limit outbox rows for destination, calls sender.Send,
// acks successes, and fails the rest. Returns the count of acked deliveries.
func DrainOnce(ctx context.Context, j *storage.Journal, destination string, sender Sender, allowed []string, limit int) (int, error) {
	now := time.Now()
	deliveries, err := j.Lease(ctx, destination, limit, now)
	if err != nil {
		return 0, err
	}
	if len(deliveries) == 0 {
		return 0, nil
	}
	acked, sendErr := sender.Send(ctx, deliveries, allowed)
	ackedSet := make(map[string]bool, len(acked))
	for _, id := range acked {
		ackedSet[id] = true
	}
	if len(acked) > 0 {
		if err := j.Ack(ctx, destination, acked); err != nil {
			return 0, err
		}
	}
	for _, d := range deliveries {
		if !ackedSet[d.EventID] {
			code := "send_failed"
			if sendErr != nil {
				code = "send_error"
			}
			_ = j.Fail(ctx, destination, d.EventID, code, now)
		}
	}
	return len(acked), sendErr
}
