package storage

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MiguelReis944/Virgil/internal/redaction"
	"github.com/MiguelReis944/Virgil/internal/telemetry"
)

//go:embed migrations/*.sql
var migrations embed.FS

const writeQueueSize = 64

type appendRequest struct {
	event        telemetry.Event
	destinations []string
	reply        chan error
}

type Journal struct {
	db             *sql.DB // write-only connection (MaxOpenConns=1)
	readDB         *sql.DB // optional concurrent read pool (WAL mode)
	installationID string
	writes         chan appendRequest
	done           chan struct{}
	mu             sync.RWMutex
	closed         bool
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return fmt.Errorf("initialize migrations: %w", err)
	}
	paths, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(paths)
	for _, path := range paths {
		name := strings.TrimPrefix(path, "migrations/")
		if len(name) < 3 {
			return fmt.Errorf("invalid migration name")
		}
		version, err := strconv.Atoi(name[:3])
		if err != nil {
			return fmt.Errorf("invalid migration version: %w", err)
		}
		var applied bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=?)", version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		script, err := migrations.ReadFile(path)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(script)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", version, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_migrations(version) VALUES (?)", version); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func localInstallationID(db *sql.DB) (string, error) {
	id, err := telemetry.NewID(16)
	if err != nil {
		return "", err
	}
	if _, err := db.Exec("INSERT OR IGNORE INTO installation(singleton, installation_id) VALUES (1, ?)", "install_"+id); err != nil {
		return "", err
	}
	var persisted string
	if err := db.QueryRow("SELECT installation_id FROM installation WHERE singleton=1").Scan(&persisted); err != nil {
		return "", err
	}
	return persisted, nil
}

func NewJournal(db *sql.DB) (*Journal, error) {
	return NewJournalWithReadPool(db, nil)
}

// NewJournalWithReadPool creates a Journal with a dedicated read pool.
// In WAL mode SQLite allows concurrent readers alongside the single writer;
// pass readDB from OpenReadPool to take advantage of that concurrency.
func NewJournalWithReadPool(db, readDB *sql.DB) (*Journal, error) {
	if db == nil {
		return nil, errors.New("database connection is required")
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	id, err := localInstallationID(db)
	if err != nil {
		return nil, err
	}
	if readDB == nil {
		readDB = db
	}
	j := &Journal{
		db: db, readDB: readDB, installationID: id,
		writes: make(chan appendRequest, writeQueueSize),
		done:   make(chan struct{}),
	}
	go j.writer()
	return j, nil
}

func (j *Journal) InstallationID() string {
	return j.installationID
}

func (j *Journal) writer() {
	defer close(j.done)
	for request := range j.writes {
		request.reply <- j.appendTransaction(request.event, request.destinations)
	}
}

func (j *Journal) Append(ctx context.Context, event telemetry.Event, destinations []string) error {
	request := appendRequest{
		event: event, destinations: append([]string(nil), destinations...),
		reply: make(chan error, 1),
	}
	j.mu.RLock()
	if j.closed {
		j.mu.RUnlock()
		return errors.New("journal is closed")
	}
	select {
	case j.writes <- request:
		j.mu.RUnlock()
	case <-ctx.Done():
		j.mu.RUnlock()
		return ctx.Err()
	}
	select {
	case err := <-request.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (j *Journal) Record(ctx context.Context, event telemetry.Event) error {
	return j.Append(ctx, event, nil)
}

func (j *Journal) appendTransaction(event telemetry.Event, destinations []string) error {
	if event.ContentCapture || event.InstallationID != j.installationID || event.EventID == "" {
		return errors.New("invalid event for journal")
	}
	prepared, err := redaction.Prepare(event)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		return err
	}
	tx, err := j.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"INSERT INTO events(event_id, installation_id, created_at_unix_ns, status, payload) VALUES (?, ?, ?, ?, ?)",
		event.EventID, event.InstallationID, event.CreatedAt.UnixNano(), event.Status, encoded,
	); err != nil {
		return err
	}
	for _, destination := range destinations {
		if destination == "" || len(destination) > 64 {
			return errors.New("invalid destination")
		}
		if _, err := tx.Exec(
			"INSERT INTO outbox(destination, event_id, idempotency_key, state, next_attempt_unix_ns) VALUES (?, ?, ?, 'pending', ?)",
			destination, event.EventID, event.InstallationID+":"+event.EventID, time.Now().UTC().UnixNano(),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ContentRecord holds the optional prompt/response content for an event.
type ContentRecord struct {
	EventID       string
	PromptJSON    string
	ResponseText  string
	ErrorMessage  string
	CreatedAtNS   int64
}

// SaveContent stores prompt/response content for an event. Best-effort: errors are logged, not fatal.
func (j *Journal) SaveContent(ctx context.Context, rec ContentRecord) error {
	j.mu.RLock()
	if j.closed {
		j.mu.RUnlock()
		return errors.New("journal is closed")
	}
	j.mu.RUnlock()
	_, err := j.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO event_content(event_id, prompt_json, response_text, error_message, created_at_unix_ns) VALUES (?,?,?,?,?)`,
		rec.EventID, rec.PromptJSON, rec.ResponseText, rec.ErrorMessage, rec.CreatedAtNS,
	)
	return err
}

// GetContent retrieves prompt/response content for an event.
func (j *Journal) GetContent(ctx context.Context, eventID string) (ContentRecord, error) {
	var rec ContentRecord
	err := j.readDB.QueryRowContext(ctx,
		`SELECT event_id, COALESCE(prompt_json,''), COALESCE(response_text,''), COALESCE(error_message,''), created_at_unix_ns FROM event_content WHERE event_id=?`,
		eventID,
	).Scan(&rec.EventID, &rec.PromptJSON, &rec.ResponseText, &rec.ErrorMessage, &rec.CreatedAtNS)
	if err != nil {
		return ContentRecord{}, err
	}
	return rec, nil
}

func (j *Journal) Get(ctx context.Context, eventID string) (telemetry.Event, error) {
	var encoded []byte
	if err := j.readDB.QueryRowContext(ctx, "SELECT payload FROM events WHERE event_id=?", eventID).Scan(&encoded); err != nil {
		return telemetry.Event{}, err
	}
	return telemetry.DecodeEvent(strings.NewReader(string(encoded)))
}

// ListRecent returns the most recent limit events ordered newest-first.
func (j *Journal) ListRecent(ctx context.Context, limit int64) ([]telemetry.Event, error) {
	rows, err := j.readDB.QueryContext(ctx,
		"SELECT payload FROM events ORDER BY created_at_unix_ns DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []telemetry.Event
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return nil, err
		}
		ev, err := telemetry.DecodeEvent(strings.NewReader(string(encoded)))
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func (j *Journal) Close() {
	j.mu.Lock()
	if !j.closed {
		j.closed = true
		close(j.writes)
	}
	j.mu.Unlock()
	<-j.done
}
