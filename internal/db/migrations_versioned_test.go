package db

// Tests del versionado de migraciones (tabla schema_migrations): adopción de
// DBs legacy (creadas antes del versionado), orden de versiones, SchemaVersion
// y AppliedMigrations. Reusa helpers de migrations_test.go y models_test.go.

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigrateDBLegacyAdoption: una DB creada con el MigrateDB viejo (idempotente,
// SIN tabla schema_migrations) debe ser adoptada: se registra la v1 como aplicada
// SIN re-ejecutar el DDL y conserva todos sus datos.
func TestMigrateDBLegacyAdoption(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("foreign keys: %v", err)
	}

	// simular DB legacy: las 7 tablas de negocio con el DDL viejo y datos adentro,
	// pero SIN schema_migrations (como quedó una DB creada antes del versionado).
	// El SQL de la v1 es todo CREATE IF NOT EXISTS de negocio: crea las tablas sin
	// la tabla de control, replicando exactamente el estado legacy.
	for _, stmt := range migrationSteps[0].SQL {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("legacy ddl: %v", err)
		}
	}
	src := &Source{Platform: "twitch", ChannelID: "42", ChannelName: "canal_legacy", Active: true}
	if err := InsertSource(conn, src); err != nil {
		t.Fatalf("insert source legacy: %v", err)
	}

	// correr el MigrateDB nuevo: debe adoptar, no recrear
	if err := MigrateDB(conn); err != nil {
		t.Fatalf("migrate over legacy db: %v", err)
	}

	version, err := SchemaVersion(conn)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if version != 1 {
		t.Errorf("expected schema version 1 after adoption, got %d", version)
	}

	// los datos previos deben seguir intactos (el DDL no se re-ejecutó)
	got, err := GetSourceByID(conn, src.ID)
	if err != nil || got == nil {
		t.Fatalf("get legacy source: %v %v", err, got)
	}
	if got.ChannelName != "canal_legacy" {
		t.Errorf("expected legacy data preserved, got %+v", got)
	}

	// idempotencia: correr de nuevo no cambia nada
	if err := MigrateDB(conn); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	version, _ = SchemaVersion(conn)
	if version != 1 {
		t.Errorf("expected version still 1, got %d", version)
	}
}

// TestMigrateDBNewDBRecordsV1: una DB nueva arranca en versión 1 con la
// migración registrada con su nombre.
func TestMigrateDBNewDBRecordsV1(t *testing.T) {
	conn := setupTestDB(t)
	defer conn.Close()

	version, err := SchemaVersion(conn)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if version != 1 {
		t.Errorf("expected schema version 1, got %d", version)
	}

	applied, err := AppliedMigrations(conn)
	if err != nil {
		t.Fatalf("applied migrations: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("expected 1 applied migration, got %d", len(applied))
	}
	if applied[0] != "1 - initial_schema" {
		t.Errorf("expected '1 - initial_schema', got %q", applied[0])
	}
}

// TestValidateMigrationsRejectsGaps: la validación de invariantes debe rechazar
// versiones con huecos o duplicadas antes de tocar la DB.
func TestValidateMigrationsRejectsGaps(t *testing.T) {
	original := migrationSteps
	defer func() { migrationSteps = original }()

	// hueco: versiones 1 y 3
	migrationSteps = []migration{
		{Version: 1, Name: "a", SQL: nil},
		{Version: 3, Name: "c", SQL: nil},
	}
	if err := validateMigrations(); err == nil {
		t.Error("expected error for non-consecutive versions")
	}

	// duplicada
	migrationSteps = []migration{
		{Version: 1, Name: "a", SQL: nil},
		{Version: 1, Name: "b", SQL: nil},
	}
	if err := validateMigrations(); err == nil {
		t.Error("expected error for duplicated version")
	}

	// restaurar la lista real antes de validar (el defer solo cubre panics/salida)
	migrationSteps = original
	if err := validateMigrations(); err != nil {
		t.Errorf("expected real migrations to validate, got: %v", err)
	}
}

// TestMigrateDBFileBasedIdempotency: sobre un archivo real (como en producción)
// dos corridas seguidas dejan la misma versión y los mismos registros.
func TestMigrateDBFileBasedIdempotency(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "clipfactory.db")

	conn1, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("init db first time: %v", err)
	}
	v1, err := SchemaVersion(conn1)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if v1 != 1 {
		t.Errorf("expected version 1, got %d", v1)
	}
	if err := conn1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// segunda apertura del mismo archivo: idempotente
	conn2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("init db second time: %v", err)
	}
	defer conn2.Close()
	v2, err := SchemaVersion(conn2)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if v2 != v1 {
		t.Errorf("expected version %d after reopen, got %d", v1, v2)
	}
}

// TestUpsertSource cubre el upsert idempotente de canales: inserta, actualiza
// nombre/active sin duplicar y reutiliza el ID existente.
func TestUpsertSource(t *testing.T) {
	conn := setupTestDB(t)
	defer conn.Close()

	s := &Source{Platform: "twitch", ChannelID: "99", ChannelName: "original", Active: true}
	if err := UpsertSource(conn, s); err != nil {
		t.Fatalf("upsert new: %v", err)
	}
	if s.ID == 0 {
		t.Fatal("expected ID assigned after insert")
	}

	// mismo (platform, channel_id): actualiza, no duplica
	s2 := &Source{Platform: "twitch", ChannelID: "99", ChannelName: "renombrado", Active: false}
	if err := UpsertSource(conn, s2); err != nil {
		t.Fatalf("upsert existing: %v", err)
	}
	if s2.ID != s.ID {
		t.Errorf("expected same ID %d, got %d", s.ID, s2.ID)
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM sources WHERE platform='twitch' AND channel_id='99'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}

	got, err := GetSourceByID(conn, s.ID)
	if err != nil || got == nil {
		t.Fatalf("get source: %v %v", err, got)
	}
	if got.ChannelName != "renombrado" || got.Active {
		t.Errorf("expected updated channel (name='renombrado', active=false), got %+v", got)
	}
}

// TestEnsureActiveJob: no duplica jobs con el mismo (type, ref) en vuelo y sí
// encola cuando no hay ninguno o el anterior ya terminó.
func TestEnsureActiveJob(t *testing.T) {
	conn := setupTestDB(t)
	defer conn.Close()

	j1 := &Job{Type: "publish", ReferenceID: 7, ReferenceType: "publications"}
	enqueued, err := EnsureActiveJob(conn, j1)
	if err != nil || !enqueued {
		t.Fatalf("first ensure should enqueue: %v %v", enqueued, err)
	}

	// mismo job mientras el primero está queued: NO duplica
	j2 := &Job{Type: "publish", ReferenceID: 7, ReferenceType: "publications"}
	enqueued, err = EnsureActiveJob(conn, j2)
	if err != nil || enqueued {
		t.Fatalf("second ensure should be a no-op: %v %v", enqueued, err)
	}

	// completado el primero, se puede encolar de nuevo
	if err := CompleteJob(conn, j1.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	enqueued, err = EnsureActiveJob(conn, j2)
	if err != nil || !enqueued {
		t.Fatalf("ensure after completion should enqueue: %v %v", enqueued, err)
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='publish' AND reference_id=7`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 jobs total, got %d", count)
	}
}

// TestHasActivePublishJob: true con job queued/running, false tras done/error,
// y exclusion del propio job (excludeJobID) para el re-encolado desde executePublish.
func TestHasActivePublishJob(t *testing.T) {
	conn := setupTestDB(t)
	defer conn.Close()

	j := &Job{Type: "publish", ReferenceID: 3, ReferenceType: "publications"}
	if err := EnqueueJob(conn, j); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	active, err := HasActivePublishJob(conn, 3, 0)
	if err != nil || !active {
		t.Errorf("expected active publish job, got %v %v", active, err)
	}

	// excluyendo el propio job (mismo ID): no hay OTRO activo
	active, err = HasActivePublishJob(conn, 3, j.ID)
	if err != nil || active {
		t.Errorf("expected no other active publish job when excluding self, got %v %v", active, err)
	}

	if err := FailJob(conn, j.ID, "boom"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	active, err = HasActivePublishJob(conn, 3, 0)
	if err != nil || active {
		t.Errorf("expected no active publish job after error, got %v %v", active, err)
	}
}

// TestInitDBLegacyFile: adopción sobre archivo real (el caso de producción:
// database/clipfactory.db creado con una versión anterior del binario).
func TestInitDBLegacyFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// crear la DB "vieja": DDL de negocio sin schema_migrations
	conn, err := sql.Open("sqlite", dbPath+"?mode=rwc&_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, stmt := range migrationSteps[0].SQL {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("legacy ddl: %v", err)
		}
	}
	if _, err := conn.Exec(`INSERT INTO sources (platform, channel_id, channel_name, active) VALUES ('kick', '7', 'kick_legacy', 1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// abrir con InitDB actual: adopta sin perder datos ni recrear tablas
	conn2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB over legacy file: %v", err)
	}
	defer conn2.Close()

	var count int
	if err := conn2.QueryRow(`SELECT COUNT(*) FROM sources WHERE platform='kick'`).Scan(&count); err != nil {
		t.Fatalf("count legacy rows: %v", err)
	}
	if count != 1 {
		t.Errorf("expected legacy row preserved, got %d rows", count)
	}
	version, err := SchemaVersion(conn2)
	if err != nil || version != 1 {
		t.Errorf("expected version 1 after adoption, got %d (%v)", version, err)
	}
}
