package api

// Handlers de la API REST aditiva. Cada handler es un "re-envoltura" delgada
// sobre las lecturas/escrituras del paquete internal/db: NO duplica lógica de
// negocio ni ejecutores (eso vive en internal/worker). Mapea exactamente los
// endpoints 🆕 REQUIRED de docs/API_CONTRACT.md.
//
// Los endpoints 🧭 BACKLOG (métricas, automations, review, pause/resume,
// priority, SSE, sync-file) NO se registran: el frontend los muestra bloqueados.
// sort=views/engagement/growth responde 400 VALIDATION_ERROR (requiere métricas).

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/juankos0714/clipfactory/internal/db"
)

// withActiveJob resuelve el job a devolver en una acción idempotente: si no se
// creó uno nuevo (EnsureActiveJob=false) cargamos el job REAL que ya estaba en
// vuelo, para que la respuesta del DTO sea consistente (ID, estado...).
func (s *Server) withActiveJob(job *db.Job, created bool) *db.Job {
	if created {
		return job
	}
	if existing, err := db.GetActiveJob(s.db, job.Type, job.ReferenceID, job.ReferenceType); err == nil && existing != nil {
		return existing
	}
	return job
}

// routes registra todos los endpoints en el mux (patrones método+ruta de Go 1.22+).
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// 3.1 System & Health
	mux.HandleFunc("GET /api/health", s.handleSystemHealth)
	mux.HandleFunc("GET /api/system/overview", s.handleSystemOverview)
	mux.HandleFunc("GET /api/system/config", s.handleSystemConfig)

	// 3.2 Channels / Sources
	mux.HandleFunc("GET /api/sources", s.handleListSources)
	mux.HandleFunc("POST /api/sources", s.handleCreateSource)
	mux.HandleFunc("GET /api/sources/{id}", s.handleGetSource)
	mux.HandleFunc("PATCH /api/sources/{id}", s.handleUpdateSource)
	mux.HandleFunc("DELETE /api/sources/{id}", s.handleDeleteSource)
	mux.HandleFunc("POST /api/sources/{id}/discovery", s.handleDiscoverSource)
	mux.HandleFunc("GET /api/sources/{id}/clips", s.handleListSourceClips)

	// 3.3 Source clips
	mux.HandleFunc("GET /api/source-clips/{id}", s.handleGetSourceClip)
	mux.HandleFunc("POST /api/source-clips/{id}/download", s.handleSourceClipDownload)
	mux.HandleFunc("POST /api/source-clips/{id}/skip", s.handleSourceClipSkip)

	// 3.4/3.5 Clips (listado unificado + detalle + archivos)
	mux.HandleFunc("GET /api/clips", s.handleListClips)
	mux.HandleFunc("GET /api/clips/{id}", s.handleGetClip)
	mux.HandleFunc("GET /api/clips/{id}/video", s.handleClipVideo)
	mux.HandleFunc("GET /api/clips/{id}/thumbnail", s.handleClipThumbnail)
	mux.HandleFunc("GET /api/clips/{id}/processed", s.handleClipProcessed)
	mux.HandleFunc("POST /api/clips/{id}/queue-for-download", s.handleSourceClipDownload)
	mux.HandleFunc("POST /api/clips/{id}/queue-for-process", s.handleClipQueueProcess)
	mux.HandleFunc("POST /api/clips/{id}/regenerate-thumbnail", s.handleClipRegenThumbnail)

	// 3.7 Publications
	mux.HandleFunc("GET /api/publications", s.handleListPublications)
	mux.HandleFunc("GET /api/publications/{id}", s.handleGetPublication)
	mux.HandleFunc("POST /api/publications", s.handleCreatePublication)
	mux.HandleFunc("POST /api/publications/{id}/retry", s.handleRetryPublication)
	mux.HandleFunc("POST /api/publications/{id}/cancel", s.handleCancelPublication)

	// 3.8 Jobs / Cola
	mux.HandleFunc("GET /api/jobs", s.handleListJobs)
	mux.HandleFunc("GET /api/jobs/stats", s.handleJobStats)
	mux.HandleFunc("GET /api/jobs/{id}", s.handleGetJob)
	mux.HandleFunc("POST /api/jobs/{id}/retry", s.handleRetryJob)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.handleCancelJob)

	// 3.11 Workers / 3.12 Logs
	mux.HandleFunc("GET /api/workers", s.handleListWorkers)
	mux.HandleFunc("GET /api/logs", s.handleListLogs)

	return mux
}

// ------------------------------------------------ 3.1 System & Health

// handleSystemHealth: aliveness + versión de schema + estado del worker.
func (s *Server) handleSystemHealth(w http.ResponseWriter, r *http.Request) {
	version, err := db.SchemaVersion(s.db)
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "base de datos no disponible")
		return
	}
	alive, err := db.WorkerAlive(s.db)
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "no se pudo consultar el estado del worker")
		return
	}
	status := "ok"
	if !alive {
		status = "degraded"
	}
	workerState := "running"
	if !alive {
		workerState = "stopped"
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":         status,
		"schema_version": version,
		"worker":         workerState,
	})
}

// handleSystemOverview: la forma HTTP del `clipfactory status` (solo lecturas).
func (s *Server) handleSystemOverview(w http.ResponseWriter, r *http.Request) {
	type counts = map[string]int
	overview := map[string]interface{}{
		"sources":      map[string]interface{}{},
		"source_clips": map[string]interface{}{},
		"videos":       counts{},
		"clips":        counts{},
		"publications": map[string]interface{}{},
		"jobs":         map[string]interface{}{},
	}

	if m, err := db.GetSourceStats(s.db); err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "no se pudieron leer sources")
		return
	} else {
		out := map[string]interface{}{}
		for platform, c := range m {
			out[platform] = map[string]interface{}{"active": c.Active, "inactive": c.Inactive}
		}
		overview["sources"] = out
	}

	if m, err := db.GetSourceClipStats(s.db); err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "no se pudieron leer source_clips")
		return
	} else {
		overview["source_clips"] = m
	}

	for key, fn := range map[string]func() (map[string]int, error){
		"videos": func() (map[string]int, error) { return db.GetVideoStats(s.db) },
		"clips":  func() (map[string]int, error) { return db.GetClipStats(s.db) },
	} {
		if m, err := fn(); err != nil {
			s.writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "no se pudieron leer "+key)
			return
		} else {
			overview[key] = m
		}
	}

	if m, err := db.GetPublicationStats(s.db); err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "no se pudieron leer publications")
		return
	} else {
		overview["publications"] = m
	}

	if m, err := db.GetJobStats(s.db); err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "no se pudieron leer jobs")
		return
	} else {
		overview["jobs"] = m
	}

	s.writeJSON(w, http.StatusOK, overview)
}

// handleSystemConfig: config NO sensible (nunca credenciales reales).
func (s *Server) handleSystemConfig(w http.ResponseWriter, r *http.Request) {
	provider := filepath.Base(s.cfg.TwitchDownloaderPath)
	if provider == "." || provider == "" {
		provider = "TwitchDownloaderCLI"
	}
	minDisksGB := 0.0
	if s.cfg.MinFreeDiskSpace > 0 {
		minDisksGB = float64(s.cfg.MinFreeDiskSpace) / 1024 / 1024 / 1024
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"poll_interval_seconds": int(s.cfg.PollInterval.Seconds()),
		"concurrency":           s.cfg.MaxConcurrentJobs,
		"download_provider":     provider,
		"max_disk_usage_gb":     minDisksGB,
		"paths": map[string]string{
			"incoming":   filepath.Join(s.cfg.DataDir, "incoming"),
			"completed":  filepath.Join(s.cfg.DataDir, "completed"),
			"thumbnails": filepath.Join(s.cfg.DataDir, "thumbnails"),
		},
		"credentials": map[string]bool{
			"twitch":  s.cfg.Twitch.ClientID != "",
			"youtube": s.cfg.YouTube.ClientID != "" && s.cfg.YouTube.RefreshToken != "",
			"meta":    s.cfg.Meta.PageID != "" && s.cfg.Meta.AccessToken != "",
		},
	})
}

// ------------------------------------------------ 3.2 Channels / Sources

func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := s.pageParams(r, w)
	if !ok {
		return
	}
	filter := db.SourceFilter{}
	if v := r.URL.Query().Get("platform"); v != "" {
		filter.Platform = v
	}
	if v := r.URL.Query().Get("active"); v != "" {
		active := v == "true" || v == "1"
		filter.Active = &active
	}
	sources, err := db.ListSources(s.db, filter)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudieron listar sources")
		return
	}
	start := (page - 1) * pageSize
	if start > len(sources) {
		start = len(sources)
	}
	end := start + pageSize
	if end > len(sources) {
		end = len(sources)
	}
	pageItems := make([]SourceDTO, 0, end-start)
	for i := start; i < end; i++ {
		pageItems = append(pageItems, toSourceDTO(&sources[i]))
	}
	s.writeJSON(w, http.StatusOK, listEnvelope[SourceDTO]{
		Data:       pageItems,
		Pagination: pagination(page, pageSize, len(sources)),
	})
}

type createSourceInput struct {
	Platform    string `json:"platform"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Active      *bool  `json:"active"`
}

func (s *Server) handleCreateSource(w http.ResponseWriter, r *http.Request) {
	var input createSourceInput
	if err := s.decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cuerpo JSON inválido")
		return
	}
	if input.Platform != "twitch" && input.Platform != "kick" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "platform debe ser twitch o kick")
		return
	}
	input.ChannelID = strings.TrimSpace(input.ChannelID)
	if input.ChannelID == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "channel_id es obligatorio")
		return
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	src := &db.Source{
		Platform:    input.Platform,
		ChannelID:   input.ChannelID,
		ChannelName: strings.TrimSpace(input.ChannelName),
		Active:      active,
	}
	if src.ChannelName == "" {
		src.ChannelName = src.ChannelID
	}
	if err := db.InsertSource(s.db, src); err != nil {
		if isUniqueErr(err) {
			s.writeError(w, http.StatusConflict, "CONFLICT", "ya existe un canal con esa plataforma y channel_id")
			return
		}
		log.Printf("[api] insert source: %v", err)
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo crear el canal")
		return
	}
	s.writeJSON(w, http.StatusCreated, toSourceDTO(src))
}

func (s *Server) handleGetSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	src, err := db.GetSourceByID(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el canal")
		return
	}
	if src == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "canal no encontrado")
		return
	}
	counts, err := db.GetSourceClipCounts(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudieron calcular los conteos")
		return
	}
	s.writeJSON(w, http.StatusOK, toSourceDetailDTO(src, counts))
}

func (s *Server) handleUpdateSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	var input struct {
		ChannelName *string `json:"channel_name"`
		ChannelID   *string `json:"channel_id"`
		Active      *bool   `json:"active"`
	}
	if err := s.decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cuerpo JSON inválido")
		return
	}
	if input.ChannelID != nil && strings.TrimSpace(*input.ChannelID) == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "channel_id no puede quedar vacío")
		return
	}
	existed, err := db.UpdateSource(s.db, id, input.ChannelName, input.ChannelID, input.Active)
	if err != nil {
		if isUniqueErr(err) {
			s.writeError(w, http.StatusConflict, "CONFLICT", "ya existe un canal con esa plataforma y channel_id")
			return
		}
		log.Printf("[api] update source %d: %v", id, err)
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo actualizar el canal")
		return
	}
	if !existed {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "canal no encontrado")
		return
	}
	src, _ := db.GetSourceByID(s.db, id)
	s.writeJSON(w, http.StatusOK, toSourceDTO(src))
}

func (s *Server) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	deleted, err := db.DeleteSource(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo eliminar el canal")
		return
	}
	if !deleted {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "canal no encontrado")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDiscoverSource: encola un job 'discovery' para el canal (idempotente
// vía EnsureActiveJob: si ya hay uno queued/running, devuelve ese con created=false).
func (s *Server) handleDiscoverSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	src, err := db.GetSourceByID(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el canal")
		return
	}
	if src == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "canal no encontrado")
		return
	}
	job := &db.Job{Type: "discovery", ReferenceID: id, ReferenceType: "sources"}
	created, err := db.EnsureActiveJob(s.db, job)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo encolar el discovery")
		return
	}
	s.writeJSON(w, http.StatusOK, jobActionDTO{Job: toJobDTO(s.withActiveJob(job, created)), Created: created})
}

// handleListSourceClips: los clips de un canal (mismo DTO que /api/clips).
func (s *Server) handleListSourceClips(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	src, err := db.GetSourceByID(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el canal")
		return
	}
	if src == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "canal no encontrado")
		return
	}
	s.listClips(w, r, id)
}

// ------------------------------------------------ 3.4 Listado de clips

// listClips comparte la lógica de GET /api/clips y GET /api/sources/:id/clips.
func (s *Server) listClips(w http.ResponseWriter, r *http.Request, sourceID int64) {
	page, pageSize, ok := s.pageParams(r, w)
	if !ok {
		return
	}

	q := r.URL.Query()
	filter := db.ClipListFilter{SourceID: sourceID}

	// canal solo por query (endpoint global /api/clips)
	if sourceID == 0 {
		if v := q.Get("channel_id"); v != "" {
			id, err := parseID(v)
			if err != nil {
				s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "channel_id inválido")
				return
			}
			filter.SourceID = id
		}
	}
	if v := q.Get("platform"); v != "" {
		filter.Platform = v
	}
	if v := q.Get("status"); v != "" {
		filter.Status = v
	}
	if from := q.Get("from"); from != "" {
		if !s.validaRFC3339(from) {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "from debe ser RFC3339")
			return
		}
		filter.From = from
	}
	if to := q.Get("to"); to != "" {
		if !s.validaRFC3339(to) {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "to debe ser RFC3339")
			return
		}
		filter.To = to
	}
	if q.Has("min_views") {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "min_views requiere métricas de plataforma (🧭 BACKLOG), no disponible")
		return
	}
	switch sort := q.Get("sort"); sort {
	case "", "newest", "duration":
		filter.Sort = sort
	case "views", "engagement", "growth":
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "sort="+sort+" requiere métricas de plataforma (🧭 BACKLOG), no disponible")
		return
	default:
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "sort inválido")
		return
	}
	if order := q.Get("order"); order != "" && order != "asc" && order != "desc" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "order debe ser asc o desc")
		return
	}
	filter.Order = q.Get("order")
	if filter.Sort == "" {
		filter.Sort = "newest"
	}
	filter.Limit = pageSize
	filter.Offset = (page - 1) * pageSize

	rows, total, err := db.ListClips(s.db, filter)
	if err != nil {
		log.Printf("[api] list clips: %v", err)
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudieron listar los clips")
		return
	}

	// publications del clip procesado de cada fila (una query por página, no por fila)
	clipIDs := make([]int64, 0, len(rows))
	for i := range rows {
		if rows[i].Clip != nil {
			clipIDs = append(clipIDs, rows[i].Clip.ID)
		}
	}
	pubsByClip, err := db.ListPublicationsByClipIDs(s.db, clipIDs)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudieron cargar las publications")
		return
	}

	items := make([]ClipItemDTO, 0, len(rows))
	for i := range rows {
		var clipID int64
		if rows[i].Clip != nil {
			clipID = rows[i].Clip.ID
		}
		items = append(items, toClipItemDTO(&rows[i], pubsByClip[clipID]))
	}
	s.writeJSON(w, http.StatusOK, listEnvelope[ClipItemDTO]{Data: items, Pagination: pagination(page, pageSize, total)})
}

// handleListClips: GET /api/clips (listado principal).
func (s *Server) handleListClips(w http.ResponseWriter, r *http.Request) {
	s.listClips(w, r, 0)
}

// ------------------------------------------------ 3.3 / 3.5 Detalle y acciones

// handleGetSourceClip: detalle de un clip detectado.
func (s *Server) handleGetSourceClip(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	sc, err := db.GetSourceClipByID(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el clip")
		return
	}
	if sc == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "clip no encontrado")
		return
	}
	s.writeJSON(w, http.StatusOK, toSourceClipDTO(sc))
}

// handleGetClip: DTO completo de un clip (árbol source_clip→video→clip→publications).
func (s *Server) handleGetClip(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	row, err := db.GetClipRow(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el clip")
		return
	}
	if row == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "clip no encontrado")
		return
	}
	clipIDs := []int64{}
	if row.Clip != nil {
		clipIDs = append(clipIDs, row.Clip.ID)
	}
	pubs, err := db.ListPublicationsByClipIDs(s.db, clipIDs)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudieron cargar las publications")
		return
	}
	var clipID int64
	if row.Clip != nil {
		clipID = row.Clip.ID
	}
	s.writeJSON(w, http.StatusOK, toClipItemDTO(row, pubs[clipID]))
}

// handleSourceClipDownload: encola job 'download' si el clip está 'detected'
// (compartido por /api/source-clips/:id/download y /api/clips/:id/queue-for-download).
// Idempotente: si ya hay un download queued/running, devuelve ese.
func (s *Server) handleSourceClipDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	sc, err := db.GetSourceClipByID(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el clip")
		return
	}
	if sc == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "clip no encontrado")
		return
	}
	if sc.Status != "detected" {
		s.writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("el clip está en estado %q; solo se puede descargar desde 'detected'", sc.Status))
		return
	}
	job := &db.Job{Type: "download", ReferenceID: id, ReferenceType: "source_clips"}
	created, err := db.EnsureActiveJob(s.db, job)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo encolar la descarga")
		return
	}
	s.writeJSON(w, http.StatusOK, jobActionDTO{Job: toJobDTO(s.withActiveJob(job, created)), Created: created})
}

// handleSourceClipSkip: acción de operador — marca el clip como 'skipped'.
func (s *Server) handleSourceClipSkip(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	sc, err := db.GetSourceClipByID(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el clip")
		return
	}
	if sc == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "clip no encontrado")
		return
	}
	switch sc.Status {
	case "skipped":
		s.writeJSON(w, http.StatusOK, toSourceClipDTO(sc)) // idempotente
		return
	case "downloaded", "completed":
		s.writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("el clip ya está en estado %q y no se puede saltar", sc.Status))
		return
	}
	if err := db.UpdateSourceClipStatus(s.db, id, "skipped", ""); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo marcar el clip como skipped")
		return
	}
	sc, _ = db.GetSourceClipByID(s.db, id)
	s.writeJSON(w, http.StatusOK, toSourceClipDTO(sc))
}

// handleClipQueueProcess: encola job 'process' para reprocesar el video.
func (s *Server) handleClipQueueProcess(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	row, err := db.GetClipRow(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el clip")
		return
	}
	if row == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "clip no encontrado")
		return
	}
	if row.Video == nil {
		s.writeError(w, http.StatusConflict, "CONFLICT", "el clip no tiene video descargado para procesar")
		return
	}
	job := &db.Job{Type: "process", ReferenceID: row.Video.ID, ReferenceType: "videos"}
	created, err := db.EnsureActiveJob(s.db, job)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo encolar el proceso")
		return
	}
	s.writeJSON(w, http.StatusOK, jobActionDTO{Job: toJobDTO(s.withActiveJob(job, created)), Created: created})
}

// handleClipRegenThumbnail: re-encola la generación de thumbnail del clip.
// El worker es idempotente: si el thumbnail ya existe y su archivo está en
// disco, el job es no-op (para regenerarlo es suficiente con que el archivo
// no exista o el operador lo pida explícitamente).
func (s *Server) handleClipRegenThumbnail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	row, err := db.GetClipRow(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el clip")
		return
	}
	if row == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "clip no encontrado")
		return
	}
	if row.Clip == nil {
		s.writeError(w, http.StatusConflict, "CONFLICT", "el clip no está procesado, no hay thumbnail que regenerar")
		return
	}
	job := &db.Job{Type: "thumbnail", ReferenceID: row.Clip.ID, ReferenceType: "clips"}
	created, err := db.EnsureActiveJob(s.db, job)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo encolar el thumbnail")
		return
	}
	s.writeJSON(w, http.StatusOK, jobActionDTO{Job: toJobDTO(s.withActiveJob(job, created)), Created: created})
}

// ------------------------------------------------ 3.5 Servir archivos

// resolveDataPath resuelve una ruta almacenada en la DB a una ruta absoluta,
// pero SOLO si queda DENTRO de cfg.DataDir (las rutas pueden ser relativas al
// cwd del worker, p.ej. "data/incoming/xxx.mp4"). Un path que escape de
// DataDir se rechaza (los archivos de trabajo no deben exponerse por HTTP).
func (s *Server) resolveDataPath(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	base := s.cfg.DataDir
	if base == "" {
		base = "."
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return absPath, true
}

// serveFile envía un archivo del DataDir con soporte byte-range (ServeContent)
// y tipo MIME explícito. cacheable=true agrega Cache-Control (thumbnails).
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, path, contentType string, cacheable bool) {
	abs, ok := s.resolveDataPath(path)
	if !ok {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "archivo no disponible")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			s.writeError(w, http.StatusNotFound, "NOT_FOUND", "archivo no encontrado en disco")
		} else {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo abrir el archivo")
		}
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el archivo")
		return
	}
	w.Header().Set("Content-Type", contentType)
	if cacheable {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	}
	http.ServeContent(w, r, filepath.Base(abs), stat.ModTime(), f)
}

func (s *Server) handleClipVideo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	row, err := db.GetClipRow(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el clip")
		return
	}
	if row == nil || row.Video == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "video no disponible para este clip")
		return
	}
	s.serveFile(w, r, row.Video.Filepath, "video/mp4", false)
}

func (s *Server) handleClipProcessed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	row, err := db.GetClipRow(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el clip")
		return
	}
	if row == nil || row.Clip == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "clip procesado no disponible")
		return
	}
	s.serveFile(w, r, row.Clip.Filepath, "video/mp4", false)
}

func (s *Server) handleClipThumbnail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	row, err := db.GetClipRow(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el clip")
		return
	}
	if row == nil || row.Clip == nil || row.Clip.ThumbnailPath == "" {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "thumbnail no disponible")
		return
	}
	s.serveFile(w, r, row.Clip.ThumbnailPath, "image/jpeg", true)
}

// ------------------------------------------------ 3.7 Publications

func (s *Server) handleListPublications(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := s.pageParams(r, w)
	if !ok {
		return
	}
	q := r.URL.Query()
	platform, status := q.Get("platform"), q.Get("status")
	from, to := q.Get("from"), q.Get("to")
	if from != "" && !s.validaRFC3339(from) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "from debe ser RFC3339")
		return
	}
	if to != "" && !s.validaRFC3339(to) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "to debe ser RFC3339")
		return
	}
	rows, total, err := db.ListPublications(s.db, platform, status, from, to, pageSize, (page-1)*pageSize)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudieron listar las publications")
		return
	}
	items := make([]PublicationListItemDTO, 0, len(rows))
	for i := range rows {
		items = append(items, toPublicationListItemDTO(&rows[i]))
	}
	s.writeJSON(w, http.StatusOK, listEnvelope[PublicationListItemDTO]{Data: items, Pagination: pagination(page, pageSize, total)})
}

// pubClipRef arma el "clip" del DTO de publication (id/título/platform del clip).
func (s *Server) pubClipRef(clipID int64) (clipRefDTO, error) {
	clip, err := db.GetClipByID(s.db, clipID)
	if err != nil {
		return clipRefDTO{}, err
	}
	if clip == nil {
		return clipRefDTO{ID: clipID}, nil
	}
	ref := clipRefDTO{ID: clip.ID}
	if video, err := db.GetVideoByID(s.db, clip.VideoID); err == nil && video != nil {
		if sc, err := db.GetSourceClipByID(s.db, video.SourceClipID); err == nil && sc != nil {
			ref.Title = sc.Title
			ref.Platform = sc.Platform
		}
	}
	return ref, nil
}

func (s *Server) handleGetPublication(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	pub, err := db.GetPublicationByID(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer la publication")
		return
	}
	if pub == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "publication no encontrada")
		return
	}
	ref, err := s.pubClipRef(pub.ClipID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el clip de la publication")
		return
	}
	dto := PublicationListItemDTO{PublicationDTO: toPublicationDTO(pub), Clip: ref}
	s.writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleCreatePublication(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ClipID   int64  `json:"clip_id"`
		Platform string `json:"platform"`
	}
	if err := s.decodeJSON(r, &input); err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cuerpo JSON inválido")
		return
	}
	if input.Platform != "youtube" && input.Platform != "meta" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "platform debe ser youtube o meta")
		return
	}
	clip, err := db.GetClipByID(s.db, input.ClipID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo verificar el clip")
		return
	}
	if clip == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "clip no encontrado")
		return
	}
	pub := &db.Publication{ClipID: input.ClipID, Platform: input.Platform, Status: "pending"}
	if err := db.InsertPublication(s.db, pub); err != nil {
		if isUniqueErr(err) {
			s.writeError(w, http.StatusConflict, "CONFLICT", "ya existe una publication para ese clip y plataforma")
			return
		}
		log.Printf("[api] insert publication: %v", err)
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo crear la publication")
		return
	}
	s.writeJSON(w, http.StatusCreated, toPublicationDTO(pub))
}

func (s *Server) handleRetryPublication(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	pub, err := db.GetPublicationByID(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer la publication")
		return
	}
	if pub == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "publication no encontrada")
		return
	}
	if pub.Status == "published" {
		s.writeError(w, http.StatusConflict, "CONFLICT", "la publication ya está publicada")
		return
	}
	job := &db.Job{Type: "publish", ReferenceID: id, ReferenceType: "publications"}
	created, err := db.EnsureActiveJob(s.db, job)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo encolar el publish")
		return
	}
	s.writeJSON(w, http.StatusOK, jobActionDTO{Job: toJobDTO(s.withActiveJob(job, created)), Created: created})
}

// handleCancelPublication: dead-letter manual — marca la publication 'failed'
// (no se re-encola más; GetPendingPublications lo excluye).
func (s *Server) handleCancelPublication(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	pub, err := db.GetPublicationByID(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer la publication")
		return
	}
	if pub == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "publication no encontrada")
		return
	}
	switch pub.Status {
	case "failed", "published":
		s.writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("la publication está en estado %q y no se puede cancelar", pub.Status))
		return
	}
	if err := db.UpdatePublicationStatus(s.db, id, "failed", "", "", "cancelada por el operador", nil, nil, false); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo cancelar la publication")
		return
	}
	pub, _ = db.GetPublicationByID(s.db, id)
	dto := PublicationListItemDTO{PublicationDTO: toPublicationDTO(pub)}
	if ref, err := s.pubClipRef(pub.ClipID); err == nil {
		dto.Clip = ref
	}
	s.writeJSON(w, http.StatusOK, dto)
}

// ------------------------------------------------ 3.8 Jobs / Cola

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := s.pageParams(r, w)
	if !ok {
		return
	}
	filter := db.JobFilter{
		Type:   r.URL.Query().Get("type"),
		Status: r.URL.Query().Get("status"),
		// IncludeDone=false: el contrato excluye por defecto los jobs terminados;
		// basta con consultar ?status=done para verlos.
		Limit:  pageSize,
		Offset: (page - 1) * pageSize,
	}
	jobs, total, err := db.ListJobs(s.db, filter)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudieron listar los jobs")
		return
	}
	items := make([]JobDTO, 0, len(jobs))
	for i := range jobs {
		items = append(items, toJobDTO(&jobs[i]))
	}
	s.writeJSON(w, http.StatusOK, listEnvelope[JobDTO]{Data: items, Pagination: pagination(page, pageSize, total)})
}

func (s *Server) handleJobStats(w http.ResponseWriter, r *http.Request) {
	stats, err := db.GetJobStats(s.db)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudieron leer las stats de jobs")
		return
	}
	s.writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	job, err := db.GetJobByID(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el job")
		return
	}
	if job == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "job no encontrado")
		return
	}
	s.writeJSON(w, http.StatusOK, toJobDTO(job))
}

// handleRetryJob: re-encola un job que falló (EnqueueJob con la MISMA referencia).
func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	job, err := db.GetJobByID(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo leer el job")
		return
	}
	if job == nil {
		s.writeError(w, http.StatusNotFound, "NOT_FOUND", "job no encontrado")
		return
	}
	if job.Status != "error" {
		s.writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("solo se re-encolan jobs en estado 'error' (actual: %q)", job.Status))
		return
	}
	newJob := &db.Job{Type: job.Type, ReferenceID: job.ReferenceID, ReferenceType: job.ReferenceType}
	if err := db.EnqueueJob(s.db, newJob); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo re-encolar el job")
		return
	}
	s.writeJSON(w, http.StatusOK, jobActionDTO{Job: toJobDTO(newJob), Created: true})
}

// handleCancelJob: marca 'error' un job queued/running (best-effort).
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "id inválido")
		return
	}
	cancelled, err := db.CancelJob(s.db, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo cancelar el job")
		return
	}
	if !cancelled {
		s.writeError(w, http.StatusConflict, "CONFLICT", "el job no está en un estado cancelable (queued/running) o no existe")
		return
	}
	job, _ := db.GetJobByID(s.db, id)
	s.writeJSON(w, http.StatusOK, toJobDTO(job))
}

// ------------------------------------------------ 3.11 Workers / 3.12 Logs

func (s *Server) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := db.ListWorkers(s.db)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudieron listar los workers")
		return
	}
	items := make([]WorkerDTO, 0, len(workers))
	for i := range workers {
		items = append(items, toWorkerDTO(&workers[i]))
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"data": items})
}

func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := s.pageParams(r, w)
	if !ok {
		return
	}
	rows, total, err := db.ListLogs(s.db, r.URL.Query().Get("level"), r.URL.Query().Get("module"), pageSize, (page-1)*pageSize)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudieron listar los logs")
		return
	}
	items := make([]LogDTO, 0, len(rows))
	for i := range rows {
		items = append(items, toLogDTO(&rows[i]))
	}
	s.writeJSON(w, http.StatusOK, listEnvelope[LogDTO]{Data: items, Pagination: pagination(page, pageSize, total)})
}

// parseID convierte un query/number a int64.
func parseID(v string) (int64, error) {
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("id inválido")
	}
	return id, nil
}
