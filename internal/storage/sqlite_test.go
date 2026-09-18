package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

func testEvent(t *testing.T, installID string, created time.Time) telemetry.Event {
	t.Helper()
	event, err := telemetry.Build(telemetry.Attempt{
		InstallationID: installID,
		RunID:          "run_fixture",
		TraceID:        "00000000000000000000000000000001",
		SpanID:         "0000000000000001",
		Provider:       "fixture",
		RequestedModel: "fixture-model",
		UsageSource:    "unknown",
		Status:         "success",
		StartedAt:      created.Add(-time.Millisecond),
		EndedAt:        created,
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func openJournal(t *testing.T, path string) (*sql.DB, *Journal) {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := NewJournal(db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db, journal
}

func TestMigrateEmptyAndExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.db")
	db, journal := openJournal(t, path)
	journal.Close()
	again, err := NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("migration count=%d", count)
	}
}

func TestRestartPreservesEventAndInstallationID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.db")
	db, journal := openJournal(t, path)
	id := journal.InstallationID()
	event := testEvent(t, id, time.Now())
	if err := journal.Append(context.Background(), event, nil); err != nil {
		t.Fatal(err)
	}
	journal.Close()
	db.Close()
	db, journal = openJournal(t, path)
	defer journal.Close()
	defer db.Close()
	if journal.InstallationID() != id {
		t.Fatalf("installation changed: %q -> %q", id, journal.InstallationID())
	}
	got, err := journal.Get(context.Background(), event.EventID)
	if err != nil || got.EventID != event.EventID || got.TraceID != event.TraceID {
		t.Fatalf("restarted event=%+v err=%v", got, err)
	}
}

func TestAppendRollsBackEventAndOutboxTogether(t *testing.T) {
	db, journal := openJournal(t, filepath.Join(t.TempDir(), "virgil.db"))
	defer journal.Close()
	defer db.Close()
	event := testEvent(t, journal.InstallationID(), time.Now())
	if err := journal.Append(context.Background(), event, []string{"collector", "collector"}); err == nil {
		t.Fatal("duplicate destination accepted")
	}
	if _, err := journal.Get(context.Background(), event.EventID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("event survived failed transaction: %v", err)
	}
}

func TestConcurrentAppendPersistsEveryEvent(t *testing.T) {
	db, journal := openJournal(t, filepath.Join(t.TempDir(), "virgil.db"))
	defer journal.Close()
	defer db.Close()
	const count = 25
	events := make([]telemetry.Event, count)
	for i := range events {
		events[i] = testEvent(t, journal.InstallationID(), time.Now())
	}
	var group sync.WaitGroup
	errors := make(chan error, count)
	for _, event := range events {
		group.Add(1)
		go func(e telemetry.Event) {
			defer group.Done()
			errors <- journal.Append(context.Background(), e, nil)
		}(event)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var got int
	if err := db.QueryRow("SELECT count(*) FROM events").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != count {
		t.Fatalf("events=%d, want %d", got, count)
	}
}

func TestAppendRejectsUnsafeMetadataBeforeSQLite(t *testing.T) {
	db, journal := openJournal(t, filepath.Join(t.TempDir(), "virgil.db"))
	defer journal.Close()
	defer db.Close()
	event := testEvent(t, journal.InstallationID(), time.Now())
	event.ResponseModel = "private canary in model"
	if err := journal.Append(context.Background(), event, nil); err == nil {
		t.Fatal("persisted unsafe metadata")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("unsafe events persisted=%d", count)
	}
}
