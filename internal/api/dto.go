package api

// DTOs JSON de la API aditiva (contrato docs/API_CONTRACT.md §3).
//
// Los nombres de campo serializan EXACTAMENTE lo que consume el frontend
// (web/src/types/api.ts): snake_case salvo los campos que ese archivo define
// en camelCase (counts.clipsDetected..., worker.lockedBy...).

import (
	"database/sql"
	"time"

	"github.com/juankos0714/clipfactory/internal/db"
)

// ---------------------------------------------------------------- Time helpers

// ts formatea un timestamp como RFC3339 UTC (misma convención que la DB).
func ts(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// tsp devuelve el puntero del string RFC3339 o nil (JSON null).
func tsp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := ts(*t)
	return &v
}

// strPtr convierte "" en nil (los campos nullable del contrato son null, no "").
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func f64Ptr(f float64) *float64 {
	if f == 0 {
		return nil
	}
	return &f
}

func intPtr(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}

func nullInt(n sql.NullInt64) *int64 {
	if n.Valid {
		v := n.Int64
		return &v
	}
	return nil
}

// ------------------------------------------------------------------ Sources

// SourceDTO es una fila de la tabla sources (§3.2).
type SourceDTO struct {
	ID            int64   `json:"id"`
	Platform      string  `json:"platform"`
	ChannelID     string  `json:"channel_id"`
	ChannelName   string  `json:"channel_name"`
	Active        bool    `json:"active"`
	LastCheckedAt *string `json:"last_checked_at"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// SourceCountsDTO son los conteos derivados de GET /api/sources/:id.
// Los nombres en camelCase los exige web/src/types/api.ts (SourceDetail).
type SourceCountsDTO struct {
	ClipsDetected   int `json:"clipsDetected"`
	ClipsDownloaded int `json:"clipsDownloaded"`
	ClipsCompleted  int `json:"clipsCompleted"`
	ClipsFailed     int `json:"clipsFailed"`
}

// SourceDetailDTO es el detalle de un canal con sus conteos.
type SourceDetailDTO struct {
	SourceDTO
	Counts SourceCountsDTO `json:"counts"`
}

// ----------------------------------------------------------- Source clips

// SourceClipDTO es una fila de source_clips (§3.3).
type SourceClipDTO struct {
	ID                int64   `json:"id"`
	Platform          string  `json:"platform"`
	PlatformClipID    string  `json:"platform_clip_id"`
	SourceID          int64   `json:"source_id"`
	Title             string  `json:"title"`
	DurationSeconds   float64 `json:"duration_seconds"`
	CreatedAtPlatform *string `json:"created_at_platform"`
	Status            string  `json:"status"`
	ErrorMessage      *string `json:"error_message"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
}

// ------------------------------------------------------------------- Clips

// VideoDTO es la parte "video" del DTO agregado (§3.4).
type VideoDTO struct {
	ID              int64    `json:"id"`
	Filepath        string   `json:"filepath"`
	Status          string   `json:"status"`
	Width           *int     `json:"width"`
	Height          *int     `json:"height"`
	DurationSeconds *float64 `json:"duration_seconds"`
}

// ClipDTO es la parte "clip procesado" del DTO agregado (§3.4).
type ClipDTO struct {
	ID            int64    `json:"id"`
	Filepath      string   `json:"filepath"`
	ThumbnailPath *string  `json:"thumbnail_path"`
	Status        string   `json:"status"`
	DurationSec   *float64 `json:"duration_sec"`
}

// ChannelDTO es la referencia al canal de origen dentro del DTO (§3.4).
type ChannelDTO struct {
	ID          int64  `json:"id"`
	ChannelName string `json:"channel_name"`
	Platform    string `json:"platform"`
	ChannelID   string `json:"channel_id"`
}

// PublicationDTO es una fila de publications.
type PublicationDTO struct {
	ID           int64   `json:"id"`
	ClipID       int64   `json:"clip_id"`
	Platform     string  `json:"platform"`
	Status       string  `json:"status"`
	Attempts     int     `json:"attempts"`
	NextRetryAt  *string `json:"next_retry_at"`
	ExternalID   *string `json:"external_id"`
	ExternalURL  *string `json:"external_url"`
	ErrorMessage *string `json:"error_message"`
	PublishedAt  *string `json:"published_at"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// MetricsDTO son métricas de plataforma. Hasta que el backend las recolecte
// (🧭 BACKLOG) todo es null y Available=false — nunca se simulán métricas.
type MetricsDTO struct {
	Views        *int `json:"views"`
	ViewsPerHour *int `json:"views_per_hour"`
	Growth       *int `json:"growth"`
	Engagement   *int `json:"engagement"`
	Available    bool `json:"available"`
}

// ClipItemDTO es el DTO agregado que el backend compone por JOIN (§3.4).
type ClipItemDTO struct {
	SourceClip   SourceClipDTO    `json:"source_clip"`
	Video        *VideoDTO        `json:"video"`
	Clip         *ClipDTO         `json:"clip"`
	Channel      ChannelDTO       `json:"channel"`
	Publications []PublicationDTO `json:"publications"`
	Metrics      MetricsDTO       `json:"metrics"`
}

// -------------------------------------------------------------------- Jobs

// JobDTO es una fila de jobs (§3.8).
type JobDTO struct {
	ID            int64   `json:"id"`
	Type          string  `json:"type"`
	ReferenceID   int64   `json:"reference_id"`
	ReferenceType string  `json:"reference_type"`
	Status        string  `json:"status"`
	Attempts      int     `json:"attempts"`
	LockedAt      *string `json:"locked_at"`
	LockedBy      *string `json:"locked_by"`
	ErrorMessage  *string `json:"error_message"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

// jobActionDTO es la respuesta de una acción que encola un job. "created"
// (true si se encoló uno nuevo; false si ya había uno activo) solo es
// relevante en las acciones idempotentes (EnsureActiveJob).
type jobActionDTO struct {
	Job     JobDTO `json:"job"`
	Created bool   `json:"created"`
}

// -------------------------------------------------------- Publications

type clipRefDTO struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Platform string `json:"platform"`
}

// PublicationListItemDTO es una publication con referencia a su clip (§3.7).
type PublicationListItemDTO struct {
	PublicationDTO
	Clip clipRefDTO `json:"clip"`
}

// ----------------------------------------------------------------- Workers

// WorkerDTO es un worker derivado de la DB. Los nombres en camelCase los exige
// web/src/types/api.ts (WorkerInfo).
type WorkerDTO struct {
	LockedBy     string  `json:"lockedBy"`
	JobType      string  `json:"jobType"`
	LastLockedAt *string `json:"lastLockedAt"`
}

// -------------------------------------------------------------------- Logs

// LogDTO es una fila de logs (§3.12).
type LogDTO struct {
	ID        int64  `json:"id"`
	Level     string `json:"level"`
	Module    string `json:"module"`
	Message   string `json:"message"`
	VideoID   *int64 `json:"video_id"`
	ClipID    *int64 `json:"clip_id"`
	JobID     *int64 `json:"job_id"`
	CreatedAt string `json:"created_at"`
}

// -------------------------------------------------------------- Conversions

func toSourceDTO(s *db.Source) SourceDTO {
	return SourceDTO{
		ID:            s.ID,
		Platform:      s.Platform,
		ChannelID:     s.ChannelID,
		ChannelName:   s.ChannelName,
		Active:        s.Active,
		LastCheckedAt: tsp(s.LastCheckedAt),
		CreatedAt:     ts(s.CreatedAt),
		UpdatedAt:     ts(s.UpdatedAt),
	}
}

func toSourceDetailDTO(s *db.Source, c db.SourceClipCounts) SourceDetailDTO {
	d := SourceDetailDTO{SourceDTO: toSourceDTO(s)}
	d.Counts = SourceCountsDTO{
		ClipsDetected:   c.Detected,
		ClipsDownloaded: c.Downloaded,
		ClipsCompleted:  c.Completed,
		ClipsFailed:     c.Failed,
	}
	return d
}

func toSourceClipDTO(sc *db.SourceClip) SourceClipDTO {
	return SourceClipDTO{
		ID:                sc.ID,
		Platform:          sc.Platform,
		PlatformClipID:    sc.PlatformClipID,
		SourceID:          sc.SourceID,
		Title:             sc.Title,
		DurationSeconds:   sc.DurationSeconds,
		CreatedAtPlatform: tsp(sc.CreatedAtPlatform),
		Status:            sc.Status,
		ErrorMessage:      strPtr(sc.ErrorMessage),
		CreatedAt:         ts(sc.CreatedAt),
		UpdatedAt:         ts(sc.UpdatedAt),
	}
}

func toVideoDTO(v *db.Video) VideoDTO {
	return VideoDTO{
		ID:              v.ID,
		Filepath:        v.Filepath,
		Status:          v.Status,
		Width:           intPtr(v.Width),
		Height:          intPtr(v.Height),
		DurationSeconds: f64Ptr(v.DurationSeconds),
	}
}

func toClipDTO(c *db.Clip) ClipDTO {
	return ClipDTO{
		ID:            c.ID,
		Filepath:      c.Filepath,
		ThumbnailPath: strPtr(c.ThumbnailPath),
		Status:        c.Status,
		DurationSec:   f64Ptr(c.DurationSec),
	}
}

func toChannelDTO(s *db.Source) ChannelDTO {
	return ChannelDTO{
		ID:          s.ID,
		ChannelName: s.ChannelName,
		Platform:    s.Platform,
		ChannelID:   s.ChannelID,
	}
}

func toPublicationDTO(p *db.Publication) PublicationDTO {
	return PublicationDTO{
		ID:           p.ID,
		ClipID:       p.ClipID,
		Platform:     p.Platform,
		Status:       p.Status,
		Attempts:     p.Attempts,
		NextRetryAt:  tsp(p.NextRetryAt),
		ExternalID:   strPtr(p.ExternalID),
		ExternalURL:  strPtr(p.ExternalURL),
		ErrorMessage: strPtr(p.ErrorMessage),
		PublishedAt:  tsp(p.PublishedAt),
		CreatedAt:    ts(p.CreatedAt),
		UpdatedAt:    ts(p.UpdatedAt),
	}
}

func toJobDTO(j *db.Job) JobDTO {
	return JobDTO{
		ID:            j.ID,
		Type:          j.Type,
		ReferenceID:   j.ReferenceID,
		ReferenceType: j.ReferenceType,
		Status:        j.Status,
		Attempts:      j.Attempts,
		LockedAt:      tsp(j.LockedAt),
		LockedBy:      strPtr(j.LockedBy),
		ErrorMessage:  strPtr(j.ErrorMessage),
		CreatedAt:     ts(j.CreatedAt),
		UpdatedAt:     ts(j.UpdatedAt),
	}
}

func toClipItemDTO(row *db.ClipRow, pubs []db.Publication) ClipItemDTO {
	d := ClipItemDTO{
		SourceClip:   toSourceClipDTO(&row.SourceClip),
		Channel:      toChannelDTO(&row.Channel),
		Metrics:      MetricsDTO{Available: false},
		Publications: make([]PublicationDTO, 0, len(pubs)),
	}
	if row.Video != nil {
		v := toVideoDTO(row.Video)
		d.Video = &v
	}
	if row.Clip != nil {
		c := toClipDTO(row.Clip)
		d.Clip = &c
	}
	for i := range pubs {
		d.Publications = append(d.Publications, toPublicationDTO(&pubs[i]))
	}
	return d
}

func toPublicationListItemDTO(row *db.PublicationRow) PublicationListItemDTO {
	d := PublicationListItemDTO{PublicationDTO: toPublicationDTO(&row.Publication)}
	d.Clip = clipRefDTO{ID: row.ClipID, Title: row.ClipTitle, Platform: row.ClipPlatform}
	return d
}

func toWorkerDTO(w *db.WorkerInfo) WorkerDTO {
	return WorkerDTO{LockedBy: w.LockedBy, JobType: w.JobType, LastLockedAt: tsp(w.LastLockedAt)}
}

func toLogDTO(l *db.Log) LogDTO {
	return LogDTO{
		ID:        l.ID,
		Level:     l.Level,
		Module:    l.Module,
		Message:   l.Message,
		VideoID:   nullInt(l.VideoID),
		ClipID:    nullInt(l.ClipID),
		JobID:     nullInt(l.JobID),
		CreatedAt: ts(l.CreatedAt),
	}
}
