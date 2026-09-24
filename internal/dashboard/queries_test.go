package dashboard

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiguelReis944/Virgil/internal/storage"
)

func TestQueryHourlySeriesIncludesDateForRangesOver24Hours(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(journal.Close)
	now := time.Date(2026, 9, 24, 15, 30, 0, 0, time.Local)
	for i, at := range []time.Time{now.Add(-2 * time.Hour), now.Add(-26 * time.Hour)} {
		payload, _ := json.Marshal(map[string]any{"provider": "local", "requested_model": "fixture", "latency_ms": 1})
		if _, err := db.Exec(`INSERT INTO events(event_id,installation_id,status,created_at_unix_ns,payload) VALUES (?, 'install_test', 'success', ?, ?)`, string(rune('a'+i)), at.UnixNano(), payload); err != nil {
			t.Fatal(err)
		}
	}
	points, err := QueryHourlySeriesAt(context.Background(), db, 48, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 2 {
		t.Fatalf("points=%+v", points)
	}
	if points[0].Label == points[1].Label || len(points[0].Label) < len("09-23 13:00") {
		t.Fatalf("labels=%q,%q", points[0].Label, points[1].Label)
	}
}

func TestQueryHourlySeriesKeepsCompactLabelsWithin24Hours(t *testing.T) {
	db, err := storage.Open(filepath.Join(t.TempDir(), "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	journal, err := storage.NewJournal(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(journal.Close)
	now := time.Now()
	if _, err := db.Exec(`INSERT INTO events(event_id,installation_id,status,created_at_unix_ns,payload) VALUES ('a', 'install_test', 'success', ?, '{"latency_ms":1}')`, now.Add(-time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	points, err := QueryHourlySeriesAt(context.Background(), db, 24, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 1 || len(points[0].Label) != len("15:00") {
		t.Fatalf("points=%+v", points)
	}
}
