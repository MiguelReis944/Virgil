package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	_ "modernc.org/sqlite"
)

// Open returns the write connection (MaxOpenConns=1).
// Use OpenReadPool for a concurrent-reader pool in WAL mode.
func Open(path string) (*sql.DB, error) {
	return openDB(path, 1)
}

// OpenReadPool returns a read-only pool. WAL mode allows concurrent readers
// alongside the single write connection returned by Open.
func OpenReadPool(path string) (*sql.DB, error) {
	n := runtime.NumCPU()
	if n < 2 {
		n = 2
	}
	return openDB(path, n)
}

func openDB(path string, maxConns int) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	// Embed busy_timeout and foreign_keys in the DSN so every connection in the
	// pool receives them automatically, not just the first one.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(maxConns)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	// WAL mode is a file-level setting; set it once via Exec (it persists).
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	return db, nil
}
