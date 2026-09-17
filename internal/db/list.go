package db

// Este archivo implementa las LECTURAS que necesita la API HTTP aditiva
// (internal/api): listados filtrados con paginación y los agregados por JOIN que
// el contrato expone como DTOs (ClipListItem, PublicationListItem).
//
// No duplica lógica de negocio: solo SELECTs. Los JOINs existen para que el
// frontend no reensamble en JS lo que la DB sabe hacer (regla de AGENTS.md §3).

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// SourceFilter acota la lista de canales. El cero value (todos los campos en "")
// significa "sin filtro".
type SourceFilter struct {
	Platform string // "" = todas las plataformas
	Active   *bool  // nil = activos e inactivos
}

// ListSources devuelve canales según el filtro, ordenados por plataforma y nombre.
//
// A diferencia de GetSources (que solo devuelve active=1 y lo usa el discovery
// para saber a quién monitorear), esta lista es la vista administrativa: incluye
// canales pausados.
func ListSources(db *sql.DB, f SourceFilter) ([]Source, error) {
	query := `SELECT id, platform, channel_id, channel_name, active, last_checked_at, created_at, updated_at FROM sources`
	var where []string
	var args []interface{}

	if f.Platform != "" {
		where = append(where, "platform = ?")
		args = append(args, f.Platform)
	}
	if f.Active != nil {
		where = append(where, "active = ?")
		args = append(args, boolToInt(*f.Active))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY platform, channel_name, id"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer rows.Close()

	out := []Source{}
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// scanSource escanea una fila de sources (helper compartido con la lista).
func scanSource(rows *sql.Rows) (*Source, error) {
	s := &Source{}
	var lastChecked, createdAt, updatedAt sql.NullString
	err := rows.Scan(&s.ID, &s.Platform, &s.ChannelID, &s.ChannelName, &s.Active, &lastChecked, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	s.LastCheckedAt = parseTimeOrNull(lastChecked)
	s.CreatedAt = parseTime(createdAt)
	s.UpdatedAt = parseTime(updatedAt)
	return s, nil
}

// UpdateSource aplica un update PARCIAL sobre un canal: solo los campos no-nil
// se escriben (channel_name, channel_id y active). Devuelve true si la fila
// existía (RowsAffected > 0), false si no.
func UpdateSource(db *sql.DB, id int64, channelName, channelID *string, active *bool) (bool, error) {
	var sets []string
	var args []interface{}

	if channelName != nil {
		sets = append(sets, "channel_name = ?")
		args = append(args, *channelName)
	}
	if channelID != nil {
		sets = append(sets, "channel_id = ?")
		args = append(args, *channelID)
	}
	if active != nil {
		sets = append(sets, "active = ?")
		args = append(args, boolToInt(*active))
	}
	if len(sets) == 0 {
		// nada que actualizar: tratarlo como "existe/no existe" sin tocar updated_at
		s, err := GetSourceByID(db, id)
		return s != nil, err
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, NowUTC(), id)

	res, err := db.Exec("UPDATE sources SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return false, fmt.Errorf("update source %d: %w", id, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// DeleteSource elimina un canal (borrado físico). ON DELETE CASCADE limpia sus
// source_clips → videos → clips → publications. Devuelve false si no existía.
func DeleteSource(db *sql.DB, id int64) (bool, error) {
	res, err := db.Exec(`DELETE FROM sources WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete source %d: %w", id, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SourceClipCounts son los conteos derivados de GET /api/sources/:id.
type SourceClipCounts struct {
	Detected   int // source_clips del canal (todos)
	Downloaded int // source_clips con status='downloaded'
	Completed  int // clips procesados con status='completed' de ese canal
	Failed     int // source_clips con status='error'
}

// GetSourceClipCounts calcula los conteos derivados de un canal.
func GetSourceClipCounts(db *sql.DB, sourceID int64) (SourceClipCounts, error) {
	var c SourceClipCounts
	err := db.QueryRow(
		`SELECT
		   COUNT(*),
		   COALESCE(SUM(CASE WHEN status = 'downloaded' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END), 0)
		 FROM source_clips WHERE source_id = ?`,
		sourceID,
	).Scan(&c.Detected, &c.Downloaded, &c.Failed)
	if err != nil {
		return c, fmt.Errorf("source clip counts %d: %w", sourceID, err)
	}

	err = db.QueryRow(
		`SELECT COUNT(*)
		 FROM clips c
		 JOIN videos v ON v.id = c.video_id
		 JOIN source_clips sc ON sc.id = v.source_clip_id
		 WHERE sc.source_id = ? AND c.status = 'completed'`,
		sourceID,
	).Scan(&c.Completed)
	if err != nil {
		return c, fmt.Errorf("source completed clips %d: %w", sourceID, err)
	}
	return c, nil
}

// ClipListFilter acota el listado agregado de clips detectados.
type ClipListFilter struct {
	ID       int64  // > 0 = solo ese source_clip (GET /api/clips/:id)
	SourceID int64  // > 0 = solo ese canal (GET /api/sources/:id/clips)
	Platform string // twitch/kick
	Status   string // estado de source_clips O de clips procesados (ver ListClips)
	From, To string // RFC3339 sobre source_clips.created_at_platform
	Sort     string // "newest" | "duration" (default newest)
	Order    string // "asc" | "desc" (default desc)
	Limit    int
	Offset   int
}

// ClipRow es el agregado source_clip → channel → video → clip (DTO ClipListItem
// del contrato menos las publications, que se cargan aparte por página).
type ClipRow struct {
	SourceClip SourceClip
	Channel    Source
	Video      *Video
	Clip       *Clip
}

// ListClips devuelve una página del listado agregado y el total de filas que
// cumplen el filtro (sin paginar).
//
// Filtro de Status: acepta los estados REALES de source_clips
// (detected/downloaded/skipped/error) y, además, los estados del clip procesado
// (processing/completed/failed) — el contrato los ofrece en el mismo filtro
// porque el frontend muestra el pipeline completo en una sola pantalla.
func ListClips(db *sql.DB, f ClipListFilter) ([]ClipRow, int, error) {
	where, args := clipWhere(f)
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	// total ANTES de paginar
	var total int
	if err := db.QueryRow(
		`SELECT COUNT(*)
		 FROM source_clips sc
		 JOIN sources s ON s.id = sc.source_id
		 LEFT JOIN videos v ON v.id = (SELECT MAX(id) FROM videos WHERE source_clip_id = sc.id)
		 LEFT JOIN clips c ON c.id = (SELECT MAX(id) FROM clips WHERE video_id = v.id)`+whereSQL,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count clips: %w", err)
	}

	query := `
		SELECT
		  sc.id, sc.platform, sc.platform_clip_id, sc.source_id, sc.title, sc.duration_seconds,
		  sc.created_at_platform, sc.status, sc.error_message, sc.created_at, sc.updated_at,
		  s.id, s.platform, s.channel_id, s.channel_name, s.active, s.last_checked_at, s.created_at, s.updated_at,
		  v.id, v.filepath, v.duration_seconds, v.width, v.height, v.status,
		  c.id, c.filepath, c.thumbnail_path, c.status, c.duration_seconds
		FROM source_clips sc
		JOIN sources s ON s.id = sc.source_id
		LEFT JOIN videos v ON v.id = (SELECT MAX(id) FROM videos WHERE source_clip_id = sc.id)
		LEFT JOIN clips c ON c.id = (SELECT MAX(id) FROM clips WHERE video_id = v.id)` +
		whereSQL + clipOrderBy(f) + ` LIMIT ? OFFSET ?`

	args = append(args, f.Limit, f.Offset)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list clips: %w", err)
	}
	defer rows.Close()

	out := []ClipRow{}
	for rows.Next() {
		row, err := scanClipRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *row)
	}
	return out, total, rows.Err()
}

// GetClipRow devuelve el agregado de un source_clip o (nil, nil) si no existe.
func GetClipRow(db *sql.DB, id int64) (*ClipRow, error) {
	rows, _, err := ListClips(db, ClipListFilter{ID: id, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// clipWhere construye el WHERE compartido entre el count y el select.
func clipWhere(f ClipListFilter) ([]string, []interface{}) {
	var where []string
	var args []interface{}

	if f.ID > 0 {
		where = append(where, "sc.id = ?")
		args = append(args, f.ID)
	}
	if f.SourceID > 0 {
		where = append(where, "sc.source_id = ?")
		args = append(args, f.SourceID)
	}
	if f.Platform != "" {
		where = append(where, "sc.platform = ?")
		args = append(args, f.Platform)
	}
	if f.Status != "" {
		if isClipStatus(f.Status) {
			where = append(where, "c.status = ?")
		} else {
			where = append(where, "sc.status = ?")
		}
		args = append(args, f.Status)
	}
	if f.From != "" {
		where = append(where, "sc.created_at_platform >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "sc.created_at_platform <= ?")
		args = append(args, f.To)
	}
	return where, args
}

// clipOrderBy traduce sort/order a SQL. Los identificadores NO se parametrizan:
// se elige entre una lista blanca fija (seguro contra inyección).
func clipOrderBy(f ClipListFilter) string {
	order := "DESC"
	if strings.EqualFold(f.Order, "asc") {
		order = "ASC"
	}
	switch f.Sort {
	case "duration":
		return " ORDER BY sc.duration_seconds " + order + ", sc.id DESC"
	default: // "newest" y cero value
		return " ORDER BY COALESCE(sc.created_at_platform, sc.created_at) " + order + ", sc.id DESC"
	}
}

// isClipStatus distingue los estados del clip procesado de los de source_clips.
func isClipStatus(s string) bool {
	switch s {
	case "processing", "completed", "failed":
		return true
	}
	return false
}

// scanClipRow escanea la fila del SELECT agregado de ListClips.
func scanClipRow(rows *sql.Rows) (*ClipRow, error) {
	var row ClipRow

	var scTitle, scCreatedPlatform, scErr, scCreatedAt, scUpdatedAt sql.NullString
	var scDuration sql.NullFloat64

	var sLastChecked, sCreatedAt, sUpdatedAt sql.NullString

	var vID, vWidth, vHeight sql.NullInt64
	var vFilepath, vStatus sql.NullString
	var vDuration sql.NullFloat64

	var cID sql.NullInt64
	var cFilepath, cThumb, cStatus sql.NullString
	var cDuration sql.NullFloat64

	err := rows.Scan(
		&row.SourceClip.ID, &row.SourceClip.Platform, &row.SourceClip.PlatformClipID, &row.SourceClip.SourceID,
		&scTitle, &scDuration, &scCreatedPlatform, &row.SourceClip.Status, &scErr, &scCreatedAt, &scUpdatedAt,
		&row.Channel.ID, &row.Channel.Platform, &row.Channel.ChannelID, &row.Channel.ChannelName, &row.Channel.Active,
		&sLastChecked, &sCreatedAt, &sUpdatedAt,
		&vID, &vFilepath, &vDuration, &vWidth, &vHeight, &vStatus,
		&cID, &cFilepath, &cThumb, &cStatus, &cDuration,
	)
	if err != nil {
		return nil, fmt.Errorf("scan clip row: %w", err)
	}

	row.SourceClip.Title = scTitle.String
	row.SourceClip.DurationSeconds = scDuration.Float64
	row.SourceClip.CreatedAtPlatform = parseTimeOrNull(scCreatedPlatform)
	row.SourceClip.ErrorMessage = scErr.String
	row.SourceClip.CreatedAt = parseTime(scCreatedAt)
	row.SourceClip.UpdatedAt = parseTime(scUpdatedAt)

	row.Channel.LastCheckedAt = parseTimeOrNull(sLastChecked)
	row.Channel.CreatedAt = parseTime(sCreatedAt)
	row.Channel.UpdatedAt = parseTime(sUpdatedAt)

	if vID.Valid {
		v := &Video{ID: vID.Int64, SourceClipID: row.SourceClip.ID, Filepath: vFilepath.String, Status: vStatus.String}
		if vDuration.Valid {
			v.DurationSeconds = vDuration.Float64
		}
		if vWidth.Valid {
			v.Width = int(vWidth.Int64)
		}
		if vHeight.Valid {
			v.Height = int(vHeight.Int64)
		}
		row.Video = v
	}

	if cID.Valid {
		c := &Clip{ID: cID.Int64, Filepath: cFilepath.String, ThumbnailPath: cThumb.String, Status: cStatus.String}
		if cDuration.Valid {
			c.DurationSec = cDuration.Float64
		}
		row.Clip = c
	}
	return &row, nil
}

// PublicationRow es una publication con los datos del clip/canal que el DTO
// PublicationListItem necesita.
type PublicationRow struct {
	Publication  Publication
	ClipID       int64
	ClipTitle    string
	ClipPlatform string
}

// ListPublications devuelve una página de publications con JOIN a clip/canal y
// el total sin paginar. Filtros: platform, status, from/to (created_at).
func ListPublications(db *sql.DB, platform, status, from, to string, limit, offset int) ([]PublicationRow, int, error) {
	var where []string
	var args []interface{}
	if platform != "" {
		where = append(where, "p.platform = ?")
		args = append(args, platform)
	}
	if status != "" {
		where = append(where, "p.status = ?")
		args = append(args, status)
	}
	if from != "" {
		where = append(where, "p.created_at >= ?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "p.created_at <= ?")
		args = append(args, to)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	join := ` FROM publications p
		JOIN clips c ON c.id = p.clip_id
		LEFT JOIN videos v ON v.id = c.video_id
		LEFT JOIN source_clips sc ON sc.id = v.source_clip_id`

	var total int
	if err := db.QueryRow(`SELECT COUNT(*)`+join+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count publications: %w", err)
	}

	query := `SELECT
		  p.id, p.clip_id, p.platform, p.status, p.attempts, p.next_retry_at,
		  p.external_id, p.external_url, p.error_message, p.published_at, p.created_at, p.updated_at,
		  c.id, COALESCE(sc.title, ''), COALESCE(sc.platform, '')` +
		join + whereSQL + ` ORDER BY p.created_at DESC, p.id DESC LIMIT ? OFFSET ?`

	args = append(args, limit, offset)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list publications: %w", err)
	}
	defer rows.Close()

	out := []PublicationRow{}
	for rows.Next() {
		var row PublicationRow
		var nextRetry, extID, extURL, errMsg, pubAt, createdAt, updatedAt sql.NullString
		err := rows.Scan(
			&row.Publication.ID, &row.Publication.ClipID, &row.Publication.Platform, &row.Publication.Status, &row.Publication.Attempts,
			&nextRetry, &extID, &extURL, &errMsg, &pubAt, &createdAt, &updatedAt,
			&row.ClipID, &row.ClipTitle, &row.ClipPlatform,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("scan publication row: %w", err)
		}
		row.Publication.NextRetryAt = parseTimeOrNull(nextRetry)
		row.Publication.ExternalID = extID.String
		row.Publication.ExternalURL = extURL.String
		row.Publication.ErrorMessage = errMsg.String
		row.Publication.PublishedAt = parseTimeOrNull(pubAt)
		row.Publication.CreatedAt = parseTime(createdAt)
		row.Publication.UpdatedAt = parseTime(updatedAt)
		out = append(out, row)
	}
	return out, total, rows.Err()
}

// ListPublicationsByClipIDs devuelve las publications de un conjunto de clips,
// agrupadas por clip_id (para armar el DTO ClipListItem sin N+1).
func ListPublicationsByClipIDs(db *sql.DB, clipIDs []int64) (map[int64][]Publication, error) {
	out := map[int64][]Publication{}
	if len(clipIDs) == 0 {
		return out, nil
	}

	placeholders := make([]string, len(clipIDs))
	args := make([]interface{}, len(clipIDs))
	for i, id := range clipIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := db.Query(
		`SELECT id, clip_id, platform, status, attempts, next_retry_at, external_id, external_url, error_message, published_at, created_at, updated_at
		 FROM publications WHERE clip_id IN (`+strings.Join(placeholders, ",")+`) ORDER BY created_at ASC`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("list publications by clips: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p Publication
		var nextRetry, extID, extURL, errMsg, pubAt, createdAt, updatedAt sql.NullString
		if err := rows.Scan(&p.ID, &p.ClipID, &p.Platform, &p.Status, &p.Attempts, &nextRetry, &extID, &extURL, &errMsg, &pubAt, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan publication by clip: %w", err)
		}
		p.NextRetryAt = parseTimeOrNull(nextRetry)
		p.ExternalID = extID.String
		p.ExternalURL = extURL.String
		p.ErrorMessage = errMsg.String
		p.PublishedAt = parseTimeOrNull(pubAt)
		p.CreatedAt = parseTime(createdAt)
		p.UpdatedAt = parseTime(updatedAt)
		out[p.ClipID] = append(out[p.ClipID], p)
	}
	return out, rows.Err()
}

// JobFilter acota la lista de jobs.
type JobFilter struct {
	Type        string
	Status      string
	IncludeDone bool // false (default del contrato) excluye los jobs terminados
	Limit       int
	Offset      int
}

// ListJobs devuelve una página de jobs y el total sin paginar.
func ListJobs(db *sql.DB, f JobFilter) ([]Job, int, error) {
	var where []string
	var args []interface{}
	if f.Type != "" {
		where = append(where, "type = ?")
		args = append(args, f.Type)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	} else if !f.IncludeDone {
		where = append(where, "status != 'done'")
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs`+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count jobs: %w", err)
	}

	query := `SELECT id, type, reference_id, reference_type, status, attempts, locked_at, locked_by, error_message, created_at, updated_at FROM jobs` +
		whereSQL + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, f.Limit, f.Offset)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *j)
	}
	return out, total, rows.Err()
}

// GetActiveJob devuelve el job en vuelo (queued/running) más reciente para una
// referencia (type, reference_id, reference_type), o (nil, nil) si no hay.
//
// Lo usa la API aditiva para responder una acción idempotente (EnsureActiveJob
// con created=false) devolviendo el job REAL que ya estaba en la cola, en vez de
// un DTO vacío.
func GetActiveJob(db *sql.DB, jobType string, referenceID int64, referenceType string) (*Job, error) {
	rows, err := db.Query(
		`SELECT id, type, reference_id, reference_type, status, attempts, locked_at, locked_by, error_message, created_at, updated_at
		 FROM jobs WHERE type = ? AND reference_id = ? AND reference_type = ? AND status IN ('queued', 'running')
		 ORDER BY id DESC LIMIT 1`,
		jobType, referenceID, referenceType,
	)
	if err != nil {
		return nil, fmt.Errorf("get active job: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	return scanJob(rows)
}

// GetJobByID devuelve un job o (nil, nil) si no existe.
func GetJobByID(db *sql.DB, id int64) (*Job, error) {
	rows, err := db.Query(
		`SELECT id, type, reference_id, reference_type, status, attempts, locked_at, locked_by, error_message, created_at, updated_at FROM jobs WHERE id = ?`,
		id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	return scanJob(rows)
}

// scanJob escanea una fila de jobs.
func scanJob(rows *sql.Rows) (*Job, error) {
	j := &Job{}
	var lockedAt, lockedBy, errMsg, createdAt, updatedAt sql.NullString
	if err := rows.Scan(&j.ID, &j.Type, &j.ReferenceID, &j.ReferenceType, &j.Status, &j.Attempts, &lockedAt, &lockedBy, &errMsg, &createdAt, &updatedAt); err != nil {
		return nil, fmt.Errorf("scan job: %w", err)
	}
	j.LockedAt = parseTimeOrNull(lockedAt)
	j.LockedBy = lockedBy.String
	j.ErrorMessage = errMsg.String
	j.CreatedAt = parseTime(createdAt)
	j.UpdatedAt = parseTime(updatedAt)
	return j, nil
}

// CancelJob marca 'error' un job en 'queued' o 'running' (best-effort). El
// worker que lo tuviera tomado lo verá fallar/descartar en su próximo chequeo.
// Devuelve false si el job no estaba cancelable (ya terminó o no existe).
func CancelJob(db *sql.DB, id int64) (bool, error) {
	res, err := db.Exec(
		`UPDATE jobs SET status = 'error', error_message = 'cancelado por el operador', updated_at = ?
		 WHERE id = ? AND status IN ('queued', 'running')`,
		NowUTC(), id,
	)
	if err != nil {
		return false, fmt.Errorf("cancel job %d: %w", id, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListLogs devuelve una página de logs y el total sin paginar.
func ListLogs(db *sql.DB, level, module string, limit, offset int) ([]Log, int, error) {
	var where []string
	var args []interface{}
	if level != "" {
		where = append(where, "level = ?")
		args = append(args, level)
	}
	if module != "" {
		where = append(where, "module = ?")
		args = append(args, module)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM logs`+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count logs: %w", err)
	}

	query := `SELECT id, level, module, message, video_id, clip_id, job_id, created_at FROM logs` +
		whereSQL + ` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list logs: %w", err)
	}
	defer rows.Close()

	out := []Log{}
	for rows.Next() {
		var l Log
		var videoID, clipID, jobID sql.NullInt64
		var createdAt sql.NullString
		if err := rows.Scan(&l.ID, &l.Level, &l.Module, &l.Message, &videoID, &clipID, &jobID, &createdAt); err != nil {
			return nil, 0, fmt.Errorf("scan log: %w", err)
		}
		l.VideoID = videoID
		l.ClipID = clipID
		l.JobID = jobID
		l.CreatedAt = parseTime(createdAt)
		out = append(out, l)
	}
	return out, total, rows.Err()
}

// WorkerInfo es un worker derivado de la DB: qué job tiene en 'running' y desde
// cuándo. No hay tabla de workers: el estado se infiere de jobs.locked_by.
type WorkerInfo struct {
	LockedBy     string
	JobType      string
	LastLockedAt *time.Time
}

// ListWorkers devuelve los workers con al menos un job en 'running'.
func ListWorkers(db *sql.DB) ([]WorkerInfo, error) {
	rows, err := db.Query(
		`SELECT locked_by, type, MAX(locked_at)
		 FROM jobs
		 WHERE status = 'running' AND locked_by IS NOT NULL AND locked_by != ''
		 GROUP BY locked_by, type
		 ORDER BY MAX(locked_at) DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list workers: %w", err)
	}
	defer rows.Close()

	out := []WorkerInfo{}
	for rows.Next() {
		var w WorkerInfo
		var lockedAt sql.NullString
		if err := rows.Scan(&w.LockedBy, &w.JobType, &lockedAt); err != nil {
			return nil, fmt.Errorf("scan worker: %w", err)
		}
		w.LastLockedAt = parseTimeOrNull(lockedAt)
		out = append(out, w)
	}
	return out, rows.Err()
}

// WorkerAlive informa si hay un job 'running' con lock fresco (< 30s), es decir
// un worker activo AHORA. Es la señal que usa GET /api/health para el estado del
// worker (el server no comparte memoria con el proceso worker).
func WorkerAlive(db *sql.DB) (bool, error) {
	stale := time.Now().UTC().Add(-30 * time.Second).Format(time.RFC3339)
	var one int
	err := db.QueryRow(
		`SELECT 1 FROM jobs WHERE status = 'running' AND locked_at >= ? LIMIT 1`,
		stale,
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
