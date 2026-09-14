package db

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateDB(t *testing.T) {
	// crear DB en memoria para testing
	db, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// ejecutar migraciones
	if err := MigrateDB(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// verificar que las tablas existen
	tables := []string{"sources", "source_clips", "videos", "clips", "publications", "jobs", "logs"}
	for _, table := range tables {
		var count int
		err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='%s'", table)).Scan(&count)
		if err != nil {
			t.Errorf("error checking table %s: %v", table, err)
		}
		if count == 0 {
			t.Errorf("table %s does not exist", table)
		}
	}
}

func TestEnsureForeignKeys(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// activar foreign keys
	if err := EnsureForeignKeys(db); err != nil {
		t.Fatalf("ensure foreign keys: %v", err)
	}

	// verificar que foreign keys esté activo
	var fkEnabled int
	err = db.QueryRow("PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Errorf("error checking foreign_keys pragma: %v", err)
	}
	if fkEnabled != 1 {
		t.Errorf("foreign_keys should be 1, got %d", fkEnabled)
	}
}

func TestInitDB(t *testing.T) {
	// crear archivo temporal para la DB
	tmpFile, err := os.CreateTemp("", "clipfactory-test-*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// inicializar DB
	db, err := InitDB(tmpPath)
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	defer db.Close()

	// verificar que las tablas existen (excluyendo sqlite_sequence, tabla interna de SQLite)
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name != 'sqlite_sequence'").Scan(&count)
	if err != nil {
		t.Errorf("error counting tables: %v", err)
	}
	if count != 7 {
		t.Errorf("expected 7 tables, got %d", count)
	}
}

func TestMigrateDBIdempotency(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	// ejecutar migraciones dos veces
	if err := MigrateDB(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := MigrateDB(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// verificar que las tablas siguen existiendo (excluyendo sqlite_sequence)
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name != 'sqlite_sequence'").Scan(&count)
	if err != nil {
		t.Errorf("error counting tables: %v", err)
	}
	if count != 7 {
		t.Errorf("expected 7 tables, got %d", count)
	}
}
