package db

// Este archivo contiene TODO el esquema de la base de datos de ClipFactory.
//
// DECISIONES GENERALES DEL ESQUEMA
//
// 1. SQLite + WAL: el pipeline corre en un único servidor con hardware modesto.
//    WAL (Write-Ahead Logging) permite que el worker escriba mientras hay lecturas
//    concurrentes sin bloquearse entre sí.
//
// 2. Timestamps como TEXTO RFC3339 UTC ("2026-09-13T10:00:00Z"):
//    - son comparables lexicográficamente en SQL (un string mayor = fecha posterior),
//    - evitan depender de datetime() de SQLite, que usa "YYYY-MM-DD HH:MM:SS" y
//      rompe las comparaciones si se mezcla con otros formatos,
//    - se generan siempre con NowUTC() (ver abajo), nunca con el default de la tabla,
//      para garantizar un único formato en toda la DB.
//
// 3. Idempotencia por índices UNIQUE:
//    - (platform, channel_id) en sources: no duplicar canales,
//    - (platform, platform_clip_id) en source_clips: re-listar clips no duplica,
//    - filepath en videos/clips: la ruta local identifica al archivo,
//    - (clip_id, platform) en publications: una publicación por plataforma y clip,
//      con reintentos independientes (un fallo en YouTube no bloquea a Meta).
//
// 4. Borrado en cascada (ON DELETE CASCADE): si se elimina un source, se borran sus
//    source_clips; si se elimina un source_clip, su video; si un video, sus clips;
//    si un clip, sus publicaciones. Requiere PRAGMA foreign_keys = ON (ver InitDB).
//
// 5. Migraciones VERSIONADAS: cada paso vive en migrationSteps con un nombre y
//    versión consecutiva; la tabla schema_migrations registra qué versiones ya se
//    aplicaron. MigrateDB() es idempotente: en cada arranque solo ejecuta las
//    versiones faltantes, en orden. Las DBs creadas antes del versionado (sin la
//    tabla) se adoptan en la versión 1 sin re-ejecutar el DDL (ver SchemaVersion).

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// migration representa UN paso de migración: un nombre legible y el SQL a ejecutar.
//
// El nombre queda registrado en schema_migrations para auditoría ("¿qué versión
// corrió esta DB?"); el SQL puede ser más de una sentencia (CREATE TABLE + índices).
type migration struct {
	Version int
	Name    string
	SQL     []string
}

// migrationSteps es la lista ORDENADA de migraciones del esquema.
//
// REGLAS para agregar una nueva:
//   - version consecutiva (lastVersion + 1): nunca reciclar ni reordenar versiones
//     ya publicadas — las DBs existentes comparan por número.
//   - el SQL debe ser idempotente o de una sola aplicación (corre UNA vez por DB).
//   - para cambiar una columna existente usar el patrón estándar de SQLite:
//     CREATE TABLE nueva → INSERT SELECT → DROP vieja → RENAME.
var migrationSteps = []migration{
	{
		Version: 1,
		Name:    "initial_schema",
		SQL: []string{
			// sources: canales de origen a monitorear
			`CREATE TABLE IF NOT EXISTS sources (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				platform TEXT NOT NULL,                    -- ej: "twitch", "kick"
				channel_id TEXT NOT NULL,                  -- ID de canal en la plataforma
				channel_name TEXT NOT NULL,                -- nombre legible del canal
				active INTEGER NOT NULL DEFAULT 1,         -- 1 = monitorear, 0 = pausado
				last_checked_at TEXT,                      -- timestamp de última revisión
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_sources_platform_channel ON sources(platform, channel_id)`,

			// source_clips: clips detectados en el origen antes de descargar
			`CREATE TABLE IF NOT EXISTS source_clips (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				platform TEXT NOT NULL,
				platform_clip_id TEXT NOT NULL,            -- ID único del clip en la plataforma
				source_id INTEGER NOT NULL,                -- FK a sources
				title TEXT,
				duration_seconds REAL,
				created_at_platform TEXT,                  -- fecha de creación original en la plataforma
				status TEXT NOT NULL DEFAULT 'detected',   -- detected, downloaded, skipped, error
				error_message TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_source_clips_platform_id ON source_clips(platform, platform_clip_id)`,
			`CREATE INDEX IF NOT EXISTS idx_source_clips_source ON source_clips(source_id)`,
			`CREATE INDEX IF NOT EXISTS idx_source_clips_status ON source_clips(status)`,

			// videos: archivo descargado localmente
			`CREATE TABLE IF NOT EXISTS videos (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				source_clip_id INTEGER NOT NULL,           -- FK a source_clips
				filepath TEXT NOT NULL UNIQUE,             -- ruta local del archivo descargado
				duration_seconds REAL,
				width INTEGER,
				height INTEGER,
				file_hash TEXT,                            -- hash para detectar cambios
				status TEXT NOT NULL DEFAULT 'incoming',   -- incoming, processing, completed, failed
				error_message TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (source_clip_id) REFERENCES source_clips(id) ON DELETE CASCADE
			)`,
			`CREATE INDEX IF NOT EXISTS idx_videos_status ON videos(status)`,
			`CREATE INDEX IF NOT EXISTS idx_videos_source_clip ON videos(source_clip_id)`,

			// clips: clip generado a partir de un video
			`CREATE TABLE IF NOT EXISTS clips (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				video_id INTEGER NOT NULL,                 -- FK a videos
				start_time_seconds REAL NOT NULL,          -- timestamp de inicio en el video fuente
				end_time_seconds REAL NOT NULL,            -- timestamp de fin en el video fuente
				filepath TEXT NOT NULL UNIQUE,             -- ruta del clip procesado
				thumbnail_path TEXT,                       -- ruta de la thumbnail generada
				duration_seconds REAL,
				width INTEGER,                             -- debe ser 1080
				height INTEGER,                            -- debe ser 1920
				status TEXT NOT NULL DEFAULT 'processing', -- processing, completed, failed
				error_message TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (video_id) REFERENCES videos(id) ON DELETE CASCADE
			)`,
			`CREATE INDEX IF NOT EXISTS idx_clips_status ON clips(status)`,
			`CREATE INDEX IF NOT EXISTS idx_clips_video ON clips(video_id)`,

			// publications: un registro por (clip, plataforma) para poder reintentar sin bloquear otras plataformas
			`CREATE TABLE IF NOT EXISTS publications (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				clip_id INTEGER NOT NULL,                  -- FK a clips
				platform TEXT NOT NULL,                    -- youtube, meta
				status TEXT NOT NULL DEFAULT 'pending',    -- pending, published, error, waiting_rate_limit
				attempts INTEGER NOT NULL DEFAULT 0,
				next_retry_at TEXT,                        -- timestamp del próximo reintento (backoff)
				external_id TEXT,                          -- ID de la publicación en la plataforma externa
				external_url TEXT,                         -- URL pública de la publicación
				error_message TEXT,
				published_at TEXT,                         -- timestamp de publicación exitosa
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (clip_id) REFERENCES clips(id) ON DELETE CASCADE
			)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_publications_clip_platform ON publications(clip_id, platform)`,
			`CREATE INDEX IF NOT EXISTS idx_publications_status ON publications(status)`,
			`CREATE INDEX IF NOT EXISTS idx_publications_next_retry ON publications(next_retry_at)`,

			// jobs: cola de trabajos interna
			`CREATE TABLE IF NOT EXISTS jobs (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				type TEXT NOT NULL,                        -- discovery, download, process, thumbnail, publish, poll_publications
				reference_id INTEGER NOT NULL,             -- apunta a source_clips, videos, clips o publications
				reference_type TEXT NOT NULL,              -- para saber a qué tabla apunta reference_id
				status TEXT NOT NULL DEFAULT 'queued',     -- queued, running, done, error
				attempts INTEGER NOT NULL DEFAULT 0,
				locked_at TEXT,                            -- timestamp cuando un worker tomó el job
				locked_by TEXT,                            -- identifier del worker (para debugging)
				error_message TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			)`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status)`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_type ON jobs(type)`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_reference ON jobs(reference_type, reference_id)`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_locked ON jobs(locked_at)`,

			// logs: registro de eventos para auditoría y debugging
			`CREATE TABLE IF NOT EXISTS logs (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				level TEXT NOT NULL,                       -- debug, info, warn, error
				module TEXT NOT NULL,                      -- discovery, download, process, publish, worker
				message TEXT NOT NULL,
				video_id INTEGER,
				clip_id INTEGER,
				job_id INTEGER,
				created_at TEXT NOT NULL DEFAULT (datetime('now'))
			)`,
			`CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level)`,
			`CREATE INDEX IF NOT EXISTS idx_logs_module ON logs(module)`,
			`CREATE INDEX IF NOT EXISTS idx_logs_created ON logs(created_at)`,
		},
	},
	{
		Version: 2,
		Name:    "add_check_constraints",
		SQL: []string{
			// Recrear sources con CHECK constraint
			`CREATE TABLE sources_new (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				platform TEXT NOT NULL CHECK (platform IN ('twitch', 'kick')),
				channel_id TEXT NOT NULL,
				channel_name TEXT NOT NULL,
				active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
				last_checked_at TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			)`,
			`INSERT INTO sources_new (id, platform, channel_id, channel_name, active, last_checked_at, created_at, updated_at)
			 SELECT id, platform, channel_id, channel_name, active, last_checked_at, created_at, updated_at FROM sources`,
			`DROP TABLE sources`,
			`ALTER TABLE sources_new RENAME TO sources`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_sources_platform_channel ON sources(platform, channel_id)`,

			// Recrear source_clips con CHECK constraint
			`CREATE TABLE source_clips_new (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				platform TEXT NOT NULL CHECK (platform IN ('twitch', 'kick')),
				platform_clip_id TEXT NOT NULL,
				source_id INTEGER NOT NULL,
				title TEXT,
				duration_seconds REAL,
				created_at_platform TEXT,
				status TEXT NOT NULL DEFAULT 'detected' CHECK (status IN ('detected', 'downloaded', 'skipped', 'error')),
				error_message TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (source_id) REFERENCES sources(id) ON DELETE CASCADE
			)`,
			`INSERT INTO source_clips_new (id, platform, platform_clip_id, source_id, title, duration_seconds, created_at_platform, status, error_message, created_at, updated_at)
			 SELECT id, platform, platform_clip_id, source_id, title, duration_seconds, created_at_platform, status, error_message, created_at, updated_at FROM source_clips`,
			`DROP TABLE source_clips`,
			`ALTER TABLE source_clips_new RENAME TO source_clips`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_source_clips_platform_id ON source_clips(platform, platform_clip_id)`,
			`CREATE INDEX IF NOT EXISTS idx_source_clips_source ON source_clips(source_id)`,
			`CREATE INDEX IF NOT EXISTS idx_source_clips_status ON source_clips(status)`,

			// Recrear videos con CHECK constraint
			`CREATE TABLE videos_new (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				source_clip_id INTEGER NOT NULL,
				filepath TEXT NOT NULL UNIQUE,
				duration_seconds REAL,
				width INTEGER,
				height INTEGER,
				file_hash TEXT,
				status TEXT NOT NULL DEFAULT 'incoming' CHECK (status IN ('incoming', 'processing', 'completed', 'failed')),
				error_message TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (source_clip_id) REFERENCES source_clips(id) ON DELETE CASCADE
			)`,
			`INSERT INTO videos_new (id, source_clip_id, filepath, duration_seconds, width, height, file_hash, status, error_message, created_at, updated_at)
			 SELECT id, source_clip_id, filepath, duration_seconds, width, height, file_hash, status, error_message, created_at, updated_at FROM videos`,
			`DROP TABLE videos`,
			`ALTER TABLE videos_new RENAME TO videos`,
			`CREATE INDEX IF NOT EXISTS idx_videos_status ON videos(status)`,
			`CREATE INDEX IF NOT EXISTS idx_videos_source_clip ON videos(source_clip_id)`,

			// Recrear clips con CHECK constraint
			`CREATE TABLE clips_new (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				video_id INTEGER NOT NULL,
				start_time_seconds REAL NOT NULL,
				end_time_seconds REAL NOT NULL,
				filepath TEXT NOT NULL UNIQUE,
				thumbnail_path TEXT,
				duration_seconds REAL,
				width INTEGER,
				height INTEGER,
				status TEXT NOT NULL DEFAULT 'processing' CHECK (status IN ('processing', 'completed', 'failed')),
				error_message TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (video_id) REFERENCES videos(id) ON DELETE CASCADE
			)`,
			`INSERT INTO clips_new (id, video_id, start_time_seconds, end_time_seconds, filepath, thumbnail_path, duration_seconds, width, height, status, error_message, created_at, updated_at)
			 SELECT id, video_id, start_time_seconds, end_time_seconds, filepath, thumbnail_path, duration_seconds, width, height, status, error_message, created_at, updated_at FROM clips`,
			`DROP TABLE clips`,
			`ALTER TABLE clips_new RENAME TO clips`,
			`CREATE INDEX IF NOT EXISTS idx_clips_status ON clips(status)`,
			`CREATE INDEX IF NOT EXISTS idx_clips_video ON clips(video_id)`,

			// Recrear publications con CHECK constraint
			`CREATE TABLE publications_new (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				clip_id INTEGER NOT NULL,
				platform TEXT NOT NULL CHECK (platform IN ('youtube', 'meta')),
				status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'published', 'error', 'waiting_rate_limit', 'failed')),
				attempts INTEGER NOT NULL DEFAULT 0,
				next_retry_at TEXT,
				external_id TEXT,
				external_url TEXT,
				error_message TEXT,
				published_at TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (clip_id) REFERENCES clips(id) ON DELETE CASCADE
			)`,
			`INSERT INTO publications_new (id, clip_id, platform, status, attempts, next_retry_at, external_id, external_url, error_message, published_at, created_at, updated_at)
			 SELECT id, clip_id, platform, status, attempts, next_retry_at, external_id, external_url, error_message, published_at, created_at, updated_at FROM publications`,
			`DROP TABLE publications`,
			`ALTER TABLE publications_new RENAME TO publications`,
			`CREATE UNIQUE INDEX IF NOT EXISTS idx_publications_clip_platform ON publications(clip_id, platform)`,
			`CREATE INDEX IF NOT EXISTS idx_publications_status ON publications(status)`,
			`CREATE INDEX IF NOT EXISTS idx_publications_next_retry ON publications(next_retry_at)`,

			// Recrear jobs con CHECK constraint
			`CREATE TABLE jobs_new (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				type TEXT NOT NULL CHECK (type IN ('discovery', 'download', 'process', 'thumbnail', 'publish', 'poll_publications')),
				reference_id INTEGER NOT NULL,
				reference_type TEXT NOT NULL,
				status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'error')),
				attempts INTEGER NOT NULL DEFAULT 0,
				locked_at TEXT,
				locked_by TEXT,
				error_message TEXT,
				created_at TEXT NOT NULL DEFAULT (datetime('now')),
				updated_at TEXT NOT NULL DEFAULT (datetime('now'))
			)`,
			`INSERT INTO jobs_new (id, type, reference_id, reference_type, status, attempts, locked_at, locked_by, error_message, created_at, updated_at)
			 SELECT id, type, reference_id, reference_type, status, attempts, locked_at, locked_by, error_message, created_at, updated_at FROM jobs`,
			`DROP TABLE jobs`,
			`ALTER TABLE jobs_new RENAME TO jobs`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status)`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_type ON jobs(type)`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_reference ON jobs(reference_type, reference_id)`,
			`CREATE INDEX IF NOT EXISTS idx_jobs_locked ON jobs(locked_at)`,
		},
	},
	// Próxima migración: {Version: 3, Name: "...", SQL: []string{...}} — nunca
	// modificar la v1: las DBs existentes ya la tienen aplicada.
}

// validateMigrations verifica invariantes de la lista de migraciones ANTES de
// ejecutar nada: versiones consecutivas empezando en 1 y sin duplicados. Si
// alguien agrega una migración con hueco o versión repetida, arranca con error
// claro en vez de corromper silenciosamente el historial.
func validateMigrations() error {
	for i, m := range migrationSteps {
		if m.Version != i+1 {
			return fmt.Errorf("migraciones fuera de orden: posición %d tiene versión %d (esperada %d)", i, m.Version, i+1)
		}
	}
	return nil
}

// MigrateDB ejecuta las migraciones pendientes para llegar al esquema completo.
//
// Flujo:
//  1. Valida la lista de migraciones (versiones consecutivas, sin duplicados).
//  2. Crea schema_migrations si no existe (siempre, es la tabla de control).
//  3. Adopta DBs PREVIAS al versionado: si no hay filas pero las tablas de negocio
//     ya existen, registra la v1 como aplicada sin re-ejecutar el DDL (todo es
//     CREATE IF NOT EXISTS, pero así el historial refleja la realidad).
//  4. Ejecuta cada versión faltante en una transacción: o se aplica completa
//     (DDL + registro en schema_migrations) o no se aplica nada.
//
// Es idempotente: puede correr en cada arranque del worker sin dañar datos.
func MigrateDB(db *sql.DB) error {
	if err := validateMigrations(); err != nil {
		return err
	}

	// la tabla de control SIEMPRE se crea primero: el loop de abajo la consulta
	// (migrationApplied) para decidir qué versiones faltan
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// adopción de DBs creadas antes del versionado (control vacía + tablas de
	// negocio presentes): la v1 se registra como aplicada SIN re-ejecutar el DDL.
	// Para DBs nuevas es un no-op y el loop de abajo aplica la v1 normalmente.
	if err := adoptLegacySchema(db); err != nil {
		return fmt.Errorf("adopt legacy schema: %w", err)
	}

	for _, m := range migrationSteps {
		applied, err := migrationApplied(db, m.Version)
		if err != nil {
			return fmt.Errorf("check migration %d (%s): %w", m.Version, m.Name, err)
		}
		if applied {
			continue
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d (%s): %w", m.Version, m.Name, err)
		}

		for i, stmt := range m.SQL {
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d (%s) failed (stmt %d): %w", m.Version, m.Name, i, err)
			}
		}

		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			m.Version, m.Name, NowUTC(),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("register migration %d (%s): %w", m.Version, m.Name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d (%s): %w", m.Version, m.Name, err)
		}
	}

	return nil
}

// migrationApplied informa si una versión ya está registrada en schema_migrations.
// Asume que la tabla existe (MigrateDB la crea antes del loop de versiones).
func migrationApplied(db *sql.DB, version int) (bool, error) {
	var one int
	err := db.QueryRow(`SELECT 1 FROM schema_migrations WHERE version = ?`, version).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// adoptLegacySchema registra la v1 como aplicada en DBs que ya tenían las
// tablas de negocio pero no schema_migrations (creadas con el MigrateDB
// idempotente anterior al versionado). Si no hay ninguna tabla de negocio es
// una DB nueva: no hace nada y el loop de MigrateDB aplicará la v1 completo.
func adoptLegacySchema(db *sql.DB) error {
	// ¿ya está registrada la v1?
	applied, err := migrationApplied(db, 1)
	if err != nil {
		return err
	}
	if applied {
		return nil
	}

	// ¿existen tablas de negocio? (DB legacy) o es una DB nueva a medio migrar?
	var tables int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN
		 ('sources','source_clips','videos','clips','publications','jobs','logs')`,
	).Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		return nil // DB nueva vacía: no hay nada que adoptar
	}

	_, err = db.Exec(
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (1, 'initial_schema', ?)`,
		NowUTC(),
	)
	return err
}

// SchemaVersion devuelve la versión de esquema aplicada más alta (0 si ninguna).
// Útil para el CLI 'status' y para tests.
func SchemaVersion(db *sql.DB) (int, error) {
	var version sql.NullInt64
	err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, err
	}
	if !version.Valid {
		return 0, nil
	}
	return int(version.Int64), nil
}

// AppliedMigrations devuelve las migraciones registradas (versión y nombre),
// ordenadas por versión. Para auditoría y el CLI 'status'.
func AppliedMigrations(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT version || ' - ' || name FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ensureForeignKeys activa las claves foráneas en SQLite (por defecto están desactivadas).
func EnsureForeignKeys(db *sql.DB) error {
	_, err := db.Exec("PRAGMA foreign_keys = ON")
	return err
}

// InitDB inicializa la base de datos: abre la conexión, activa foreign keys, y aplica migraciones.
//
// Es la única forma "de producción" de abrir la DB (los tests abren su propia
// conexión :memory:). Parámetros del DSN:
//   - mode=rwc: crea el archivo si no existe (read-write-create)
//   - _journal_mode=WAL: concurrencia lector/escritor (ver cabecera del archivo)
//   - _foreign_keys=on: activa el chequeo de FK y las cascadas
//
// NOTA: el driver modernc.org/sqlite no siempre honra _foreign_keys en el DSN según
// la versión; por robustez, EnsureForeignKeys re-ejecuta el PRAGMA después de abrir
// y los tests lo hacen explícitamente.
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath+"?mode=rwc&_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	// activar foreign keys explícitamente (el DSN no siempre es honrado)
	if err := EnsureForeignKeys(db); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	// aplicar migraciones pendientes (versionadas, ver migrationSteps)
	if err := MigrateDB(db); err != nil {
		return nil, fmt.Errorf("migrate database: %w", err)
	}

	return db, nil
}

// NowDevuelve un timestamp ISO 8601 para usar en columnas created_at/updated_at.
func NowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}
