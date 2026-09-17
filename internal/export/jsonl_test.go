package export

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestJSONLRequiresPathAndAllowlist(t *testing.T) {
	_, j := openTestJournal(t)
	if err := JSONL(context.Background(), j, "", []string{"event_id"}); err == nil {
		t.Fatal("expected error for empty path")
	}
	if err := JSONL(context.Background(), j, filepath.Join(t.TempDir(), "out.jsonl"), nil); err == nil {
		t.Fatal("expected error for nil allowlist")
	}
}

func TestJSONLWritesRedactedLines(t *testing.T) {
	_, j := openTestJournal(t)
	appendTo(t, j, "dest-z")
	// also append one with no destination (journal-only)
	out := filepath.Join(t.TempDir(), "events.jsonl")
	allowed := []string{"event_id", "event_type", "status", "provider"}
	if err := JSONL(context.Background(), j, out, allowed); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lines := 0
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("invalid JSON line: %s", sc.Text())
		}
		if _, ok := m["event_id"]; !ok {
			t.Fatalf("missing event_id in line")
		}
		lines++
	}
	if lines == 0 {
		t.Fatal("no lines written")
	}
}

func TestJSONLStableEventIDsExport(t *testing.T) {
	_, j := openTestJournal(t)
	ev := appendTo(t, j, "dest-w")
	out := filepath.Join(t.TempDir(), "events.jsonl")
	if err := JSONL(context.Background(), j, out, []string{"event_id", "status"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	var m map[string]any
	json.Unmarshal(data[:len(data)-1], &m)
	if m["event_id"] != ev.EventID {
		t.Fatalf("event ID changed: got %v want %s", m["event_id"], ev.EventID)
	}
}
