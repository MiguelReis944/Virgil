package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

func openTestDB(t *testing.T) (*sql.DB, *Journal) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "virgil.db")
	db, j := openJournal(t, path)
	t.Cleanup(func() { j.Close(); db.Close() })
	return db, j
}

func makeTestEvent(t *testing.T, installID string) telemetry.Event {
	return testEvent(t, installID, time.Now())
}

func appendEventWithDest(t *testing.T, j *Journal, dest string) telemetry.Event {
	t.Helper()
	ev := makeTestEvent(t, j.installationID)
	if err := j.Append(context.Background(), ev, []string{dest}); err != nil {
		t.Fatalf("append: %v", err)
	}
	return ev
}

func TestLeaseExpiresAfterRestart(t *testing.T) {
	db, _ := openTestDB(t)
	j, err := NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	ev := appendEventWithDest(t, j, "dest-a")
	now := time.Now()
	deliveries, err := j.Lease(context.Background(), "dest-a", 10, now)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("lease: %v deliveries=%d", err, len(deliveries))
	}
	if deliveries[0].EventID != ev.EventID {
		t.Fatalf("wrong event id")
	}
	// Re-lease after expiry: simulate stale lease by leasing with past now
	past := now.Add(-leaseDuration - time.Second)
	// Manually expire the lease by setting it in the past via Fail then re-lease
	// Simpler: lease with a time far in the future so the row is in 'sending',
	// then lease again with now past the lease expiry.
	future := now.Add(leaseDuration + time.Hour)
	// set lease expiry to past via direct update for test
	_, err = db.Exec("UPDATE outbox SET lease_until_unix_ns=? WHERE event_id=?",
		past.UnixNano(), ev.EventID)
	if err != nil {
		t.Fatal(err)
	}
	_ = future
	deliveries2, err := j.Lease(context.Background(), "dest-a", 10, now)
	if err != nil || len(deliveries2) != 1 {
		t.Fatalf("re-lease after expiry: %v deliveries=%d", err, len(deliveries2))
	}
}

func TestDestinationADoesNotAckB(t *testing.T) {
	db, _ := openTestDB(t)
	j, _ := NewJournal(db)
	defer j.Close()
	ev := appendEventWithDest(t, j, "dest-b")
	j.Lease(context.Background(), "dest-b", 10, time.Now())
	// Ack for wrong destination should have no effect
	if err := j.Ack(context.Background(), "dest-a", []string{ev.EventID}); err != nil {
		t.Fatal(err)
	}
	var state string
	db.QueryRow("SELECT state FROM outbox WHERE event_id=? AND destination=?", ev.EventID, "dest-b").Scan(&state)
	if state != "sending" {
		t.Fatalf("wrong destination ack affected state: %s", state)
	}
}

func TestPermanentErrorDeadLetters(t *testing.T) {
	db, _ := openTestDB(t)
	j, _ := NewJournal(db)
	defer j.Close()
	ev := appendEventWithDest(t, j, "dest-c")
	now := time.Now()
	j.Lease(context.Background(), "dest-c", 10, now)
	// exhaust retries
	for i := 0; i < maxAttempts; i++ {
		if err := j.Fail(context.Background(), "dest-c", ev.EventID, "test_error", now); err != nil {
			t.Fatalf("fail %d: %v", i, err)
		}
		// re-lease to bring back to 'sending' for next fail
		if i < maxAttempts-1 {
			db.Exec("UPDATE outbox SET state='sending' WHERE event_id=? AND destination=?", ev.EventID, "dest-c")
		}
	}
	var state string
	db.QueryRow("SELECT state FROM outbox WHERE event_id=? AND destination=?", ev.EventID, "dest-c").Scan(&state)
	if state != "dead_letter" {
		t.Fatalf("expected dead_letter, got %s", state)
	}
}

func TestListPaginates(t *testing.T) {
	db, _ := openTestDB(t)
	j, _ := NewJournal(db)
	defer j.Close()
	// append 5 events
	for i := 0; i < 5; i++ {
		ev := makeTestEvent(t, j.installationID)
		j.Append(context.Background(), ev, nil)
	}
	page1, cursor, err := j.List(context.Background(), "", 3)
	if err != nil || len(page1) != 3 || cursor == "" {
		t.Fatalf("page1: %v len=%d cursor=%q", err, len(page1), cursor)
	}
	page2, cursor2, err := j.List(context.Background(), cursor, 3)
	if err != nil || len(page2) != 2 || cursor2 != "" {
		t.Fatalf("page2: %v len=%d cursor=%q", err, len(page2), cursor2)
	}
}

func TestJSONLStableEventIDs(t *testing.T) {
	db, _ := openTestDB(t)
	j, _ := NewJournal(db)
	defer j.Close()
	ev := makeTestEvent(t, j.installationID)
	j.Append(context.Background(), ev, nil)
	events, _, err := j.List(context.Background(), "", 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("list: %v len=%d", err, len(events))
	}
	if events[0].EventID != ev.EventID {
		t.Fatalf("event ID changed: got %s want %s", events[0].EventID, ev.EventID)
	}
}
