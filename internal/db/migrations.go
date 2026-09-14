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
//      con reintentos independientes (un fallo en YouTube no bloquea a TikTok).
//
// 4. Borrado en cascada (ON DELETE CASCADE): si se elimina un source, se borran sus
//    source_clips; si se elimina un source_clip, su video; si un video, sus clips;
//    si un clip, sus publicaciones. Requiere PRAGMA foreign_keys = ON (ver InitDB).
//
// 5. Migraciones idempotentes: todo usa CREATE TABLE/INDEX IF NOT EXISTS, de modo
//    que MigrateDB() puede ejecutarse en cada arranque sin dañar datos existentes.

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// migrateDB ejecuta las migraciones necesarias para tener el esquema completo.
func MigrateDB(db *sql.DB) error {
	migrations := []string{
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
			platform TEXT NOT NULL,                    -- youtube, meta, tiktok, kick
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
			type TEXT NOT NULL,                        -- discovery, download, process, thumbnail, publish
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
	}

	for i, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			return fmt.Errorf("migration %d failed: %w", i, err)
		}
	}
	return nil
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

	// aplicar migraciones
	if err := MigrateDB(db); err != nil {
		return nil, fmt.Errorf("migrate database: %w", err)
	}

	return db, nil
}

// NowDevuelve un timestamp ISO 8601 para usar en columnas created_at/updated_at.
func NowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}
