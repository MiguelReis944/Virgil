package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRemotePolicyCacheSurvivesRestartAndRejectsStaleVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "virgil.db")
	db, journal := openJournal(t, path)
	ctx := context.Background()
	if version, payload, err := journal.LoadRemotePolicy(ctx); err != nil || version != 0 || payload != nil {
		t.Fatalf("empty cache: %d %s %v", version, payload, err)
	}
	if err := journal.SaveRemotePolicy(ctx, 2, []byte(`{"version":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := journal.SaveRemotePolicy(ctx, 1, []byte(`{"version":1}`)); err == nil {
		t.Fatal("stale version accepted")
	}
	journal.Close()
	db.Close()
	db, journal = openJournal(t, path)
	defer db.Close()
	defer journal.Close()
	version, payload, err := journal.LoadRemotePolicy(ctx)
	if err != nil || version != 2 || string(payload) != `{"version":2}` {
		t.Fatalf("reloaded cache: %d %s %v", version, payload, err)
	}
}
