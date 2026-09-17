package export

import (
	"context"

	"github.com/MiguelReis944/Virgil/internal/controlplane"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

// controlPlaneSender wraps a controlplane.Client to implement Sender.
type controlPlaneSender struct {
	client *controlplane.Client
}

// Send submits deliveries to the Control Plane and returns accepted event IDs.
// The allowed list is ignored — the CP receives the full redacted event payload.
func (s *controlPlaneSender) Send(ctx context.Context, batch []storage.Delivery, _ []string) ([]string, error) {
	ack, err := s.client.SendBatch(ctx, batch)
	if err != nil {
		return nil, err
	}
	return ack.AcceptedIDs, nil
}

// DrainControlPlane leases up to limit outbox rows for destination and delivers
// them to the Control Plane. Behaviour on revocation and transport errors mirrors
// DrainOnce: revoked credentials cause the rows to be failed in the outbox.
func DrainControlPlane(ctx context.Context, j *storage.Journal, destination string, client *controlplane.Client, allowed []string, limit int) (int, error) {
	sender := &controlPlaneSender{client: client}
	return DrainOnce(ctx, j, destination, sender, allowed, limit)
}
