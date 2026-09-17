package export

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/MiguelReis944/Virgil/internal/redaction"
	"github.com/MiguelReis944/Virgil/internal/storage"
)

const jsonlPageSize = 200

// JSONL writes redacted canonical events to path, one JSON line per event.
// allowed must be non-empty; each line is filtered through redaction.ForExport.
// Uses stable event IDs from the journal — events are not re-IDed on export.
func JSONL(ctx context.Context, j *storage.Journal, path string, allowed []string) error {
	if path == "" {
		return errors.New("output path is required")
	}
	if len(allowed) == 0 {
		return errors.New("allowlist is required")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	cursor := ""
	for {
		events, next, err := j.List(ctx, cursor, jsonlPageSize)
		if err != nil {
			return err
		}
		for _, ev := range events {
			filtered, err := redaction.ForExport(ev, allowed)
			if err != nil {
				return err
			}
			line, err := json.Marshal(filtered)
			if err != nil {
				return err
			}
			if _, err := w.Write(append(line, '\n')); err != nil {
				return err
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return w.Flush()
}
