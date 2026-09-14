package db

// Este archivo implementa el CRUD tipado sobre el esquema definido en migrations.go.
//
// CONVENCIONES:
//
//   - Todas las funciones reciben *sql.DB como primer parámetro (no usan un singleton
//     global) para que sean testables con DBs en memoria y seguras para varios workers.
//
//   - Los INSERTs devuelven el LastInsertId en el campo ID del struct recibido, de
//     modo que el llamador puede encadenar inserts (source → source_clip → video...).
//
//   - Los timestamps se escriben SIEMPRE con NowUTC() (RFC3339 en UTC). Al LEER, el
//     driver entrega los TEXT como string: por eso los Scan se hacen en sql.NullString
//     y se convierten con parseTime/parseTimeOrNull. Escanear directo a time.Time
//     falla con "unsupported Scan, storing driver.Value type string".
//
//   - Las columnas nullable (error_message, next_retry_at, published_at, locked_at,
//     file_hash, thumbnail_path...) también se escanean en sql.NullString para no
//     morir con "converting NULL to string is unsupported".

import (
	"database/sql"
	"fmt"
	"time"
)

// Source representa un canal de origen a monitorear.
//
// Una fila por canal. El discovery consulta periódicamente los canales con
// Active=1 y crea source_clips por cada clip nuevo encontrado.
type Source struct {
	ID            int64
	Platform      string // "twitch", "kick"
	ChannelID     string // ID numérico del canal en la plataforma
	ChannelName   string // nombre legible (logs y display)
	Active        bool   // 1 = el discovery lo consulta, 0 = pausado
	LastCheckedAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SourceClip representa un clip detectado en el origen antes de descargarlo.
//
// Máquina de estados (columna Status):
//
//	detected   → el discovery lo vio, aún no se descarga
//	downloaded → existe una fila en videos apuntando acá
//	skipped    → decidimos no descargarlo (p.ej. demasiado corto, visto antes)
//	error      → la descarga falló ( ErrorMessage con el detalle)
type SourceClip struct {
	ID                int64
	Platform          string
	PlatformClipID    string // ID del clip en la plataforma (UNIQUE con Platform)
	SourceID          int64  // FK a sources
	Title             string
	DurationSeconds   float64
	CreatedAtPlatform *time.Time // fecha de creación del clip EN la plataforma
	Status            string     // detected, downloaded, skipped, error
	ErrorMessage      string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Video representa un archivo descargado localmente.
//
// Máquina de estados (columna Status):
//
//	incoming   → descargado a data/incoming/, esperando proceso
//	processing → ffmpeg está trabajando con él
//	completed  → clips generados y verificados
//	failed     → el proceso falló ( ErrorMessage con el detalle)
//
// Filepath es UNIQUE: la ruta local identifica al archivo y hace idempotente
// la descarga (un reintento no crea dos filas para el mismo archivo).
type Video struct {
	ID              int64
	SourceClipID    int64  // FK a source_clips
	Filepath        string // ruta local (UNIQUE: hace idempotente la descarga)
	DurationSeconds float64
	Width           int
	Height          int
	FileHash        string
	Status          string // incoming, processing, completed, failed
	ErrorMessage    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Clip representa un clip procesado (recortado a 1080x1920).
//
// Es la unidad que se publica: una fila por archivo vertical listo para
// YouTube Shorts/TikTok/Reels. Width/Height deberían ser siempre 1080/1920
// (el schema lo documenta como comentario; validarlo es tarea del proceso).
type Clip struct {
	ID            int64
	VideoID       int64 // FK a videos
	StartTimeSec  float64
	EndTimeSec    float64
	Filepath      string // ruta del clip vertical (UNIQUE)
	ThumbnailPath string // ruta del JPEG de vista previa (lo llena el job thumbnail)
	DurationSec   float64
	Width         int    // 1080 en el pipeline actual
	Height        int    // 1920 en el pipeline actual
	Status        string // processing, completed, failed
	ErrorMessage  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Publication representa un intento de publicación en una plataforma específica.
//
// Hay UNA fila por (Clip, Plataforma): UNIQUE (clip_id, platform). Así un fallo
// en una plataforma no bloquea ni reinicia a las otras, y cada una mantiene su
// propio backoff con Attempts + NextRetryAt.
//
// Estados: pending → published | error | waiting_rate_limit.
// UpdatePublicationStatus incrementa Attempts en cada intento.
type Publication struct {
	ID           int64
	ClipID       int64  // FK a clips
	Platform     string // youtube, meta, tiktok, kick
	Status       string // pending, published, error, waiting_rate_limit
	Attempts     int    // intentos fallidos (alimenta el backoff exponencial)
	NextRetryAt  *time.Time
	ExternalID   string // videoId en la plataforma externa (ej: "dQw4w9WgXcQ")
	ExternalURL  string // URL pública (ej: https://youtu.be/<id>)
	ErrorMessage string
	PublishedAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Job representa un trabajo en la cola interna.
//
// Estados: queued → running → done | error.
//
// Al tomar un job, LockJob hace UPDATE ... WHERE status='queued' y setea
// LockedAt/LockedBy (identificador del worker). Si un worker muere con el job
// en 'running', GetPendingJobs lo vuelve a ofrecer cuando LockedAt tenga más
// de 30 segundos (semántica "at-least-once": el trabajo puede repetirse, la
// idempotencia la dan los UNIQUE del esquema).
type Job struct {
	ID            int64
	Type          string // discovery, download, process, thumbnail, publish
	ReferenceID   int64  // ID de la fila a la que apunta (cambia según ReferenceType)
	ReferenceType string // "sources", "source_clips", "videos", "clips", "publications"
	Status        string // queued, running, done, error
	Attempts      int
	LockedAt      *time.Time
	LockedBy      string
	ErrorMessage  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Log representa un registro de evento para auditoría.
type Log struct {
	ID        int64
	Level     string
	Module    string
	Message   string
	VideoID   sql.NullInt64
	ClipID    sql.NullInt64
	JobID     sql.NullInt64
	CreatedAt time.Time
}

// InsertSource inserta un nuevo canal de origen.
func InsertSource(db *sql.DB, s *Source) error {
	res, err := db.Exec(
		`INSERT INTO sources (platform, channel_id, channel_name, active, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		s.Platform, s.ChannelID, s.ChannelName, boolToInt(s.Active), NowUTC(), NowUTC(),
	)
	if err != nil {
		return err
	}
	s.ID, _ = res.LastInsertId()
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(i int) bool {
	return i != 0
}

// parseTimeOrNull convierte un TEXT de la DB (RFC3339 o "YYYY-MM-DD HH:MM:SS")
// en *time.Time en UTC. Devuelve nil si es NULL/vacío o no se puede parsear.
//
// Acepta el formato datetime de SQLite porque registros viejos (o escritos por
// otra herramienta) pueden tener ese formato; los nuevos siempre son RFC3339.
func parseTimeOrNull(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s.String)
	if err != nil {
		// intentar con formato datetime de SQLite: "2006-01-02 15:04:05"
		t, err = time.Parse("2006-01-02 15:04:05", s.String)
		if err != nil {
			return nil
		}
	}
	return &t
}

// parseTime es como parseTimeOrNull pero devuelve zero time en vez de nil,
// para escanear columnas NOT NULL (created_at, updated_at) directamente.
func parseTime(s sql.NullString) time.Time {
	if t := parseTimeOrNull(s); t != nil {
		return *t
	}
	return time.Time{}
}

// GetSources devuelve todos los canales activos (que deben ser monitoreados).
func GetSources(db *sql.DB) ([]Source, error) {
	rows, err := db.Query(`SELECT id, platform, channel_id, channel_name, active, last_checked_at, created_at, updated_at FROM sources WHERE active = 1 ORDER BY platform, channel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []Source
	for rows.Next() {
		var s Source
		var lastChecked, createdAt, updatedAt sql.NullString
		err := rows.Scan(&s.ID, &s.Platform, &s.ChannelID, &s.ChannelName, &s.Active, &lastChecked, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		s.LastCheckedAt = parseTimeOrNull(lastChecked)
		s.CreatedAt = parseTime(createdAt)
		s.UpdatedAt = parseTime(updatedAt)
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// UpsertSourceClip inserta o actualiza un clip detectado en el origen.
// Es idempotente: si ya existe (por platform + platform_clip_id), no crea otra fila.
//
// ¿Por qué no INSERT OR IGNORE? Porque necesitamos conocer el ID existente para
// actualizar el estado en re-listados (p.ej. detected → downloaded) y para que el
// llamador tenga sc.ID apuntando a la fila correcta.
func UpsertSourceClip(db *sql.DB, sc *SourceClip) error {
	// buscar existente
	var existingID int64
	err := db.QueryRow(
		`SELECT id FROM source_clips WHERE platform = ? AND platform_clip_id = ?`,
		sc.Platform, sc.PlatformClipID,
	).Scan(&existingID)
	if err == sql.ErrNoRows {
		// insertar nuevo
		res, err := db.Exec(
			`INSERT INTO source_clips (platform, platform_clip_id, source_id, title, duration_seconds, created_at_platform, status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			sc.Platform, sc.PlatformClipID, sc.SourceID, sc.Title, sc.DurationSeconds, sc.CreatedAtPlatform, sc.Status, NowUTC(), NowUTC(),
		)
		if err != nil {
			return err
		}
		sc.ID, _ = res.LastInsertId()
		return nil
	}
	if err != nil {
		return err
	}
	// ya existe: NO regresar estados avanzados del pipeline. Si el clip ya está
	// downloaded/skipped/error, un re-descubrimiento (status 'detected') no debe
	// reiniciarlo — eso duplicaría descargas. Solo se actualiza el status si la
	// fila sigue en 'detected' (p.ej. el discovery corrige un estado inicial).
	// Título y duración originales del discovery se preservan siempre.
	if sc.Status != "" {
		_, err = db.Exec(
			`UPDATE source_clips SET status = ?, error_message = NULL, updated_at = ?
			 WHERE id = ? AND status = 'detected'`,
			sc.Status, NowUTC(), existingID,
		)
		if err == nil {
			// recargar el estado real de la fila para que el llamador lo vea
			_ = db.QueryRow(`SELECT status FROM source_clips WHERE id = ?`, existingID).Scan(&sc.Status)
		}
	}
	sc.ID = existingID
	return err
}

// SourceClipExists informa si ya existe un source_clip para (platform, platformClipID).
// El discovery lo usa para encolar jobs download SOLO de clips genuinamente nuevos
// (re-encolar sobre clips aún 'detected' duplicaría trabajos).
func SourceClipExists(db *sql.DB, platform, platformClipID string) (bool, error) {
	var one int
	err := db.QueryRow(
		`SELECT 1 FROM source_clips WHERE platform = ? AND platform_clip_id = ?`,
		platform, platformClipID,
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// GetSourceByID busca un canal por su ID primario.
// Devuelve (nil, nil) si no existe — mismo contrato que GetVideoByFilepath.
func GetSourceByID(db *sql.DB, id int64) (*Source, error) {
	s := &Source{}
	var lastChecked, createdAt, updatedAt sql.NullString
	err := db.QueryRow(
		`SELECT id, platform, channel_id, channel_name, active, last_checked_at, created_at, updated_at
		 FROM sources WHERE id = ?`,
		id,
	).Scan(&s.ID, &s.Platform, &s.ChannelID, &s.ChannelName, &s.Active, &lastChecked, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.LastCheckedAt = parseTimeOrNull(lastChecked)
	s.CreatedAt = parseTime(createdAt)
	s.UpdatedAt = parseTime(updatedAt)
	return s, nil
}

// UpdateSourceLastChecked guarda el timestamp de la última pasada de discovery
// del canal. Es la marca que usa la próxima pasada como afterTimestamp
// ("clips nuevos desde la última revisión").
func UpdateSourceLastChecked(db *sql.DB, id int64, when time.Time) error {
	_, err := db.Exec(
		`UPDATE sources SET last_checked_at = ?, updated_at = ? WHERE id = ?`,
		when.UTC().Format(time.RFC3339), NowUTC(), id,
	)
	return err
}

// GetSourceClipByID busca un source_clip por su ID primario.
// Devuelve (nil, nil) si no existe — mismo contrato que GetVideoByFilepath.
func GetSourceClipByID(db *sql.DB, id int64) (*SourceClip, error) {
	sc := &SourceClip{}
	var title, errMsg, createdAt, updatedAt sql.NullString
	var durationSec sql.NullFloat64
	var createdAtPlatform sql.NullString
	err := db.QueryRow(
		`SELECT id, platform, platform_clip_id, source_id, title, duration_seconds, created_at_platform, status, error_message, created_at, updated_at
		 FROM source_clips WHERE id = ?`,
		id,
	).Scan(&sc.ID, &sc.Platform, &sc.PlatformClipID, &sc.SourceID, &title, &durationSec, &createdAtPlatform, &sc.Status, &errMsg, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if title.Valid {
		sc.Title = title.String
	}
	if durationSec.Valid {
		sc.DurationSeconds = durationSec.Float64
	}
	if createdAtPlatform.Valid {
		sc.CreatedAtPlatform = parseTimeOrNull(createdAtPlatform)
	}
	if errMsg.Valid {
		sc.ErrorMessage = errMsg.String
	}
	sc.CreatedAt = parseTime(createdAt)
	sc.UpdatedAt = parseTime(updatedAt)
	return sc, nil
}

// UpdateSourceClipStatus actualiza el estado de un source_clip.
//
// errorMessage se usa cuando status='error'; en los demás casos conviene pasar ""
// (se guarda NULL, limpiando errores anteriores).
func UpdateSourceClipStatus(db *sql.DB, id int64, status, errorMessage string) error {
	var errMsg interface{}
	if errorMessage != "" {
		errMsg = errorMessage
	}
	_, err := db.Exec(
		`UPDATE source_clips SET status = ?, error_message = ?, updated_at = ? WHERE id = ?`,
		status, errMsg, NowUTC(), id,
	)
	return err
}

// InsertVideo inserta un video descargado (status 'incoming' por convención del caller).
// Devuelve el ID generado en v.ID. Si el filepath ya existe el INSERT falla por el
// UNIQUE; el caller (executeDownload) interpreta eso como "otro worker ganó" y reusa
// la fila existente en vez de duplicar.
func InsertVideo(db *sql.DB, v *Video) error {
	res, err := db.Exec(
		`INSERT INTO videos (source_clip_id, filepath, duration_seconds, width, height, file_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.SourceClipID, v.Filepath, v.DurationSeconds, v.Width, v.Height, v.FileHash, v.Status, NowUTC(), NowUTC(),
	)
	if err != nil {
		return err
	}
	v.ID, _ = res.LastInsertId()
	return nil
}

// GetVideoByFilepath busca un video por su ruta local.
func GetVideoByFilepath(db *sql.DB, filepath string) (*Video, error) {
	v := &Video{}
	var errMsg, fileHash, createdAt, updatedAt sql.NullString
	err := db.QueryRow(
		`SELECT id, source_clip_id, filepath, duration_seconds, width, height, file_hash, status, error_message, created_at, updated_at
		 FROM videos WHERE filepath = ?`,
		filepath,
	).Scan(&v.ID, &v.SourceClipID, &v.Filepath, &v.DurationSeconds, &v.Width, &v.Height, &fileHash, &v.Status, &errMsg, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if errMsg.Valid {
		v.ErrorMessage = errMsg.String
	}
	if fileHash.Valid {
		v.FileHash = fileHash.String
	}
	v.CreatedAt = parseTime(createdAt)
	v.UpdatedAt = parseTime(updatedAt)
	return v, nil
}

// GetVideoByID busca un video por su ID primario.
// Devuelve (nil, nil) si no existe — mismo contrato que GetVideoByFilepath.
func GetVideoByID(db *sql.DB, id int64) (*Video, error) {
	v := &Video{}
	var errMsg, fileHash, createdAt, updatedAt sql.NullString
	err := db.QueryRow(
		`SELECT id, source_clip_id, filepath, duration_seconds, width, height, file_hash, status, error_message, created_at, updated_at
		 FROM videos WHERE id = ?`,
		id,
	).Scan(&v.ID, &v.SourceClipID, &v.Filepath, &v.DurationSeconds, &v.Width, &v.Height, &fileHash, &v.Status, &errMsg, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if errMsg.Valid {
		v.ErrorMessage = errMsg.String
	}
	if fileHash.Valid {
		v.FileHash = fileHash.String
	}
	v.CreatedAt = parseTime(createdAt)
	v.UpdatedAt = parseTime(updatedAt)
	return v, nil
}

// UpdateVideoStatus actualiza el estado de un video.
func UpdateVideoStatus(db *sql.DB, id int64, status, errorMessage string) error {
	_, err := db.Exec(
		`UPDATE videos SET status = ?, error_message = ?, updated_at = ? WHERE id = ?`,
		status, errorMessage, NowUTC(), id,
	)
	return err
}

// InsertClip inserta un clip procesado (status 'completed' por convención del caller).
// Devuelve el ID generado en c.ID. Igual que InsertVideo: el UNIQUE(filepath) hace
// que un reintento duplicado falle y el caller reuse la fila existente.
func InsertClip(db *sql.DB, c *Clip) error {
	res, err := db.Exec(
		`INSERT INTO clips (video_id, start_time_seconds, end_time_seconds, filepath, thumbnail_path, duration_seconds, width, height, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.VideoID, c.StartTimeSec, c.EndTimeSec, c.Filepath, c.ThumbnailPath, c.DurationSec, c.Width, c.Height, c.Status, NowUTC(), NowUTC(),
	)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	return nil
}

// GetClipByFilepath busca un clip por su ruta.
func GetClipByFilepath(db *sql.DB, filepath string) (*Clip, error) {
	c := &Clip{}
	var errMsg, thumbPath, createdAt, updatedAt sql.NullString
	err := db.QueryRow(
		`SELECT id, video_id, start_time_seconds, end_time_seconds, filepath, thumbnail_path, duration_seconds, width, height, status, error_message, created_at, updated_at
		 FROM clips WHERE filepath = ?`,
		filepath,
	).Scan(&c.ID, &c.VideoID, &c.StartTimeSec, &c.EndTimeSec, &c.Filepath, &thumbPath, &c.DurationSec, &c.Width, &c.Height, &c.Status, &errMsg, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if errMsg.Valid {
		c.ErrorMessage = errMsg.String
	}
	if thumbPath.Valid {
		c.ThumbnailPath = thumbPath.String
	}
	c.CreatedAt = parseTime(createdAt)
	c.UpdatedAt = parseTime(updatedAt)
	return c, nil
}

// GetClipByID busca un clip por su ID primario.
// Devuelve (nil, nil) si no existe — mismo contrato que GetVideoByFilepath.
func GetClipByID(db *sql.DB, id int64) (*Clip, error) {
	c := &Clip{}
	var errMsg, thumbPath, createdAt, updatedAt sql.NullString
	err := db.QueryRow(
		`SELECT id, video_id, start_time_seconds, end_time_seconds, filepath, thumbnail_path, duration_seconds, width, height, status, error_message, created_at, updated_at
		 FROM clips WHERE id = ?`,
		id,
	).Scan(&c.ID, &c.VideoID, &c.StartTimeSec, &c.EndTimeSec, &c.Filepath, &thumbPath, &c.DurationSec, &c.Width, &c.Height, &c.Status, &errMsg, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if errMsg.Valid {
		c.ErrorMessage = errMsg.String
	}
	if thumbPath.Valid {
		c.ThumbnailPath = thumbPath.String
	}
	c.CreatedAt = parseTime(createdAt)
	c.UpdatedAt = parseTime(updatedAt)
	return c, nil
}

// UpdateClipThumbnail guarda la ruta del thumbnail generado para un clip.
func UpdateClipThumbnail(db *sql.DB, id int64, thumbnailPath string) error {
	_, err := db.Exec(
		`UPDATE clips SET thumbnail_path = ?, updated_at = ? WHERE id = ?`,
		thumbnailPath, NowUTC(), id,
	)
	return err
}

// UpdateClipStatus actualiza el estado de un clip.
func UpdateClipStatus(db *sql.DB, id int64, status, errorMessage string) error {
	_, err := db.Exec(
		`UPDATE clips SET status = ?, error_message = ?, updated_at = ? WHERE id = ?`,
		status, errorMessage, NowUTC(), id,
	)
	return err
}

// InsertPublication inserta una nueva publicación pendiente ('pending' por convención
// del caller). UNIQUE (clip_id, platform): un segundo INSERT para la misma
// combinación falla — el operador decide si reusar la fila o borrarla.
func InsertPublication(db *sql.DB, p *Publication) error {
	// convertir next_retry_at a RFC3339 para comparaciones lexicográficas consistentes en SQL
	var nextRetryStr interface{}
	if p.NextRetryAt != nil {
		nextRetryStr = p.NextRetryAt.UTC().Format(time.RFC3339)
	}
	res, err := db.Exec(
		`INSERT INTO publications (clip_id, platform, status, attempts, next_retry_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ClipID, p.Platform, p.Status, p.Attempts, nextRetryStr, NowUTC(), NowUTC(),
	)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	return nil
}

// GetPendingPublications devuelve publicaciones disponibles para el job publish:
// pendientes ('pending'), reintentables ('error') y liberadas de rate-limit
// ('waiting_rate_limit' con next_retry_at vencido).
// Orden por created_at ASC (FIFO). La ventana de reintento la controla next_retry_at.
func GetPendingPublications(db *sql.DB, platform string, limit int) ([]Publication, error) {
	rows, err := db.Query(
		`SELECT id, clip_id, platform, status, attempts, next_retry_at, external_id, external_url, error_message, published_at, created_at, updated_at
		 FROM publications
		 WHERE platform = ? AND status IN ('pending', 'error', 'waiting_rate_limit') AND (next_retry_at IS NULL OR next_retry_at <= ?)
		 ORDER BY created_at ASC LIMIT ?`,
		// comparación lexicográfica consistente con timestamps RFC3339 almacenados
		platform, NowUTC(), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pubs []Publication
	for rows.Next() {
		var p Publication
		var nextRetry, extID, extURL, errMsg, pubAt, createdAt, updatedAt sql.NullString
		err := rows.Scan(&p.ID, &p.ClipID, &p.Platform, &p.Status, &p.Attempts, &nextRetry, &extID, &extURL, &errMsg, &pubAt, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		p.NextRetryAt = parseTimeOrNull(nextRetry)
		if extID.Valid {
			p.ExternalID = extID.String
		}
		if extURL.Valid {
			p.ExternalURL = extURL.String
		}
		if errMsg.Valid {
			p.ErrorMessage = errMsg.String
		}
		p.PublishedAt = parseTimeOrNull(pubAt)
		p.CreatedAt = parseTime(createdAt)
		p.UpdatedAt = parseTime(updatedAt)
		pubs = append(pubs, p)
	}
	return pubs, rows.Err()
}

// UpdatePublicationStatus actualiza el estado de una publicación.
//
// countAttempt controla si se incrementa el contador de intentos: true para
// errores reales (alimenta el backoff exponencial), false para transiciones
// que NO son fallos del pipeline (éxito 'published' o 'waiting_rate_limit',
// donde la cuota agotada no es culpa del clip).
func UpdatePublicationStatus(db *sql.DB, id int64, status, externalID, externalURL, errorMessage string, publishedAt *time.Time, nextRetryAt *time.Time, countAttempt bool) error {
	// convertir a RFC3339 para comparaciones lexicográficas consistentes en SQL
	var publishedAtStr, nextRetryAtStr interface{}
	if publishedAt != nil {
		publishedAtStr = publishedAt.UTC().Format(time.RFC3339)
	}
	if nextRetryAt != nil {
		nextRetryAtStr = nextRetryAt.UTC().Format(time.RFC3339)
	}
	attemptsSQL := "attempts"
	if countAttempt {
		attemptsSQL = "attempts + 1"
	}
	_, err := db.Exec(
		`UPDATE publications SET status = ?, external_id = ?, external_url = ?, error_message = ?, published_at = ?, next_retry_at = ?, attempts = `+attemptsSQL+`, updated_at = ? WHERE id = ?`,
		status, externalID, externalURL, errorMessage, publishedAtStr, nextRetryAtStr, NowUTC(), id,
	)
	return err
}

// GetPublicationByID busca una publicación por su ID primario.
// Devuelve (nil, nil) si no existe — mismo contrato que GetVideoByFilepath.
func GetPublicationByID(db *sql.DB, id int64) (*Publication, error) {
	p := &Publication{}
	var nextRetry, extID, extURL, errMsg, pubAt, createdAt, updatedAt sql.NullString
	err := db.QueryRow(
		`SELECT id, clip_id, platform, status, attempts, next_retry_at, external_id, external_url, error_message, published_at, created_at, updated_at
		 FROM publications WHERE id = ?`,
		id,
	).Scan(&p.ID, &p.ClipID, &p.Platform, &p.Status, &p.Attempts, &nextRetry, &extID, &extURL, &errMsg, &pubAt, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.NextRetryAt = parseTimeOrNull(nextRetry)
	if extID.Valid {
		p.ExternalID = extID.String
	}
	if extURL.Valid {
		p.ExternalURL = extURL.String
	}
	if errMsg.Valid {
		p.ErrorMessage = errMsg.String
	}
	p.PublishedAt = parseTimeOrNull(pubAt)
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return p, nil
}

// GetClipByPublication busca el clip asociado a una publicación.
// Devuelve (nil, nil) si la publicación no existe o no tiene clip.
func GetClipByPublication(db *sql.DB, publicationID int64) (*Clip, error) {
	p, err := GetPublicationByID(db, publicationID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	return GetClipByID(db, p.ClipID)
}

// GetPendingJobs devuelve jobs disponibles para procesar, más viejos primero.
//
// Dos condiciones para ofrecer un job:
//  1. status = 'queued' (nadie lo está ejecutando), o
//  2. locked_at tiene más de 30 segundos: el worker que lo tomó murió y el job
//     se considera huérfano (stale lock). Con el umbral en RFC3339, la comparación
//     es lexicográfica y correcta; NO usar datetime(locked_at) porque ese formato
//     no coincide con el texto que guardamos (ver cabecera de migrations.go).
func GetPendingJobs(db *sql.DB, limit int) ([]Job, error) {
	rows, err := db.Query(
		`SELECT id, type, reference_id, reference_type, status, attempts, locked_at, locked_by, error_message, created_at, updated_at
		 FROM jobs
		 WHERE status = 'queued' AND (locked_at IS NULL OR locked_at < ?)
		 ORDER BY created_at ASC LIMIT ?`,
		// umbral de stale-lock en RFC3339 para comparar lexicográficamente con locked_at
		time.Now().UTC().Add(-30*time.Second).Format(time.RFC3339), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		var lockedAt, lockedBy, errMsg, createdAt, updatedAt sql.NullString
		err := rows.Scan(&j.ID, &j.Type, &j.ReferenceID, &j.ReferenceType, &j.Status, &j.Attempts, &lockedAt, &lockedBy, &errMsg, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		j.LockedAt = parseTimeOrNull(lockedAt)
		if lockedBy.Valid {
			j.LockedBy = lockedBy.String
		}
		if errMsg.Valid {
			j.ErrorMessage = errMsg.String
		}
		j.CreatedAt = parseTime(createdAt)
		j.UpdatedAt = parseTime(updatedAt)
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// LockJob marca un job como running con el worker que lo tomó.
//
// Es atómico a nivel SQL: el WHERE status='queued' garantiza que si dos workers
// intentan tomar el mismo job a la vez, solo uno logra el UPDATE (RowsAffected=1)
// y el otro recibe error. Esto reemplaza a un mutex: la cola puede escalar a
// varios procesos sobre la misma DB sin coordinación extra.
func LockJob(db *sql.DB, id int64, workerID string) error {
	res, err := db.Exec(
		`UPDATE jobs SET status = 'running', locked_at = ?, locked_by = ?, updated_at = ? WHERE id = ? AND status = 'queued'`,
		NowUTC(), workerID, NowUTC(), id,
	)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("job %d not found or not in queued status", id)
	}
	return nil
}

// CompleteJob marca un job como done (fin exitoso del handler).
func CompleteJob(db *sql.DB, id int64) error {
	_, err := db.Exec(
		`UPDATE jobs SET status = 'done', updated_at = ? WHERE id = ?`,
		NowUTC(), id,
	)
	return err
}

// FailJob marca un job como error.
func FailJob(db *sql.DB, id int64, errorMessage string) error {
	_, err := db.Exec(
		`UPDATE jobs SET status = 'error', error_message = ?, updated_at = ? WHERE id = ?`,
		errorMessage, NowUTC(), id,
	)
	return err
}

// EnqueueJob inserta un nuevo job en la cola (status 'queued').
// Devuelve el ID generado en j.ID, útil para loguear la cadena de jobs
// (discovery → download → process → thumbnail → publish).
func EnqueueJob(db *sql.DB, j *Job) error {
	res, err := db.Exec(
		`INSERT INTO jobs (type, reference_id, reference_type, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'queued', ?, ?)`,
		j.Type, j.ReferenceID, j.ReferenceType, NowUTC(), NowUTC(),
	)
	if err != nil {
		return err
	}
	j.ID, _ = res.LastInsertId()
	return nil
}

// InsertLog inserta un registro de log en la base de datos.
//
// Los logs en DB (además de los del stdout) permiten auditoría posterior y
// correlacionar eventos con video_id/clip_id/job_id (todos nullable).
func InsertLog(db *sql.DB, l *Log) error {
	res, err := db.Exec(
		`INSERT INTO logs (level, module, message, video_id, clip_id, job_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		l.Level, l.Module, l.Message, l.VideoID, l.ClipID, l.JobID, NowUTC(),
	)
	if err != nil {
		return err
	}
	l.ID, _ = res.LastInsertId()
	return nil
}

// CleanupOldCompletedVideos elimina videos completados antiguos según la política de retención.
// Retorna la cantidad de archivos eliminados.
//
// IMPORTANTE: primero se recolectan los videos a eliminar y se CIERRA el cursor
// antes de ejecutar los DELETE. Si se hiciera DELETE mientras el cursor está
// abierto, con SetMaxOpenConns(1) (necesario para DBs :memory:) la consulta
// entraría en deadlock esperando la misma conexión.
func CleanupOldCompletedVideos(db *sql.DB, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)

	// 1) recolectar los videos a eliminar sin mantener el cursor abierto
	type videoToDelete struct {
		id       int64
		filepath string
	}
	var toDelete []videoToDelete

	rows, err := db.Query(
		`SELECT id, filepath FROM videos WHERE status = 'completed' AND updated_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var v videoToDelete
		if err := rows.Scan(&v.id, &v.filepath); err != nil {
			rows.Close()
			return 0, err
		}
		toDelete = append(toDelete, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close() // liberar la conexión ANTES de los DELETE

	// 2) eliminar archivos y registros ahora que el cursor está cerrado
	var count int64
	for _, v := range toDelete {
		// borrar el archivo del disco; si falla (p.ej. ya no existe), se conserva el registro
		if err := osRemove(v.filepath); err == nil {
			if _, err := db.Exec(`DELETE FROM videos WHERE id = ?`, v.id); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}

// osRemove es un helper para eliminar archivos. Se puede mockear en tests.
var osRemove = osRemoveImpl

func osRemoveImpl(path string) error {
	return nil // implementación real en platform.go
}
