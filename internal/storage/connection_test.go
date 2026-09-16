package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenCreatesWorkingSQLiteConnection(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "nested", "virgil.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Fatalf("database ping: %v", err)
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal mode = %q, want wal", mode)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	var db *sql.DB
	db, err := Open("")
	if err == nil {
		db.Close()
		t.Fatal("accepted empty database path")
	}
}
