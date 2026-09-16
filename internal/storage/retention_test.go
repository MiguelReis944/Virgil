package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneKeepsUndeliveredRows(t *testing.T) {
	db, journal := openJournal(t, filepath.Join(t.TempDir(), "virgil.db"))
	defer journal.Close()
	defer db.Close()
	old := time.Now().Add(-48 * time.Hour)
	local := testEvent(t, journal.InstallationID(), old)
	pending := testEvent(t, journal.InstallationID(), old)
	if err := journal.Append(context.Background(), local, nil); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(context.Background(), pending, []string{"collector"}); err != nil {
		t.Fatal(err)
	}
	deleted, err := journal.Prune(context.Background(), time.Now().Add(-24*time.Hour))
	if err != nil || deleted != 1 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	if _, err := journal.Get(context.Background(), local.EventID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("local event retained: %v", err)
	}
	if _, err := journal.Get(context.Background(), pending.EventID); err != nil {
		t.Fatalf("pending event deleted: %v", err)
	}
	if _, err := db.Exec("UPDATE outbox SET state='delivered' WHERE event_id=?", pending.EventID); err != nil {
		t.Fatal(err)
	}
	deleted, err = journal.Prune(context.Background(), time.Now().Add(-24*time.Hour))
	if err != nil || deleted != 1 {
		t.Fatalf("delivered deleted=%d err=%v", deleted, err)
	}
}
