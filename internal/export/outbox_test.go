package export

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/storage"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

func openTestJournal(t *testing.T) (*sql.DB, *storage.Journal) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	j, err := storage.NewJournal(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close(); db.Close() })
	return db, j
}

func appendTo(t *testing.T, j *storage.Journal, dest string) telemetry.Event {
	t.Helper()
	ev, err := telemetry.Build(telemetry.Attempt{
		InstallationID: j.InstallationID(),
		RunID:          "run_fixture",
		TraceID:        "00000000000000000000000000000001",
		SpanID:         "0000000000000001",
		Provider:       "fixture",
		RequestedModel: "fixture-model",
		UsageSource:    "unknown",
		Status:         "success",
		StartedAt:      time.Now().Add(-time.Millisecond),
		EndedAt:        time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Append(context.Background(), ev, []string{dest}); err != nil {
		t.Fatal(err)
	}
	return ev
}

type fakeSender struct {
	ack []string
	err error
}

func (f *fakeSender) Send(_ context.Context, batch []storage.Delivery, _ []string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.ack, nil
}

func TestDrainAcksDelivered(t *testing.T) {
	_, j := openTestJournal(t)
	ev := appendTo(t, j, "dest-x")
	sender := &fakeSender{ack: []string{ev.EventID}}
	n, err := DrainOnce(context.Background(), j, "dest-x", sender, nil, 10)
	if err != nil || n != 1 {
		t.Fatalf("drain: %v n=%d", err, n)
	}
}

func TestDrainFailsOnSendError(t *testing.T) {
	_, j := openTestJournal(t)
	appendTo(t, j, "dest-y")
	sender := &fakeSender{err: context.DeadlineExceeded}
	n, err := DrainOnce(context.Background(), j, "dest-y", sender, nil, 10)
	if err == nil || n != 0 {
		t.Fatalf("expected error: %v n=%d", err, n)
	}
}
