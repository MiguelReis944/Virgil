package export

import (
	"context"
	"errors"
	"time"

	"github.com/MiguelReis944/Virgil/internal/controlplane"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

// DrainControlPlane leases up to limit outbox rows for destination and delivers
// them to the Control Plane. Permanently invalid events go to dead letter.
func DrainControlPlane(ctx context.Context, j *storage.Journal, destination string, client *controlplane.Client, allowed []string, limit int) (int, error) {
	if len(allowed) == 0 {
		return 0, errors.New("export field allowlist is required")
	}
	now := time.Now()
	deliveries, err := j.Lease(ctx, destination, limit, now)
	if err != nil || len(deliveries) == 0 {
		return 0, err
	}
	ack, sendErr := client.SendBatch(ctx, deliveries, allowed)
	accepted := make(map[string]bool, len(ack.AcceptedIDs))
	for _, id := range ack.AcceptedIDs {
		accepted[id] = true
	}
	var count int
	for _, d := range deliveries {
		if accepted[d.EventID] {
			if err := j.Ack(ctx, destination, []string{d.EventID}); err != nil {
				return count, err
			}
			count++
			continue
		}
		if ack.Rejections[d.EventID] == "invalid_event" {
			if err := j.DeadLetter(ctx, destination, d.EventID, "invalid_event"); err != nil {
				return count, err
			}
			continue
		}
		code := "send_failed"
		if sendErr != nil {
			code = "send_error"
		}
		if err := j.Fail(ctx, destination, d.EventID, code, now); err != nil {
			return count, err
		}
	}
	return count, sendErr
}
