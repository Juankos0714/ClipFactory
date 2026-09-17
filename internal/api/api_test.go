package api

// Tests de la API REST aditiva. Usan una DB SQLite real (temp) creada con
// db.InitDB (aplica migraciones) y un Server montado sobre ella, sin ejecutar
// el worker: los jobs se encolan y se verifican leyendo la tabla jobs.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/config"
	"github.com/juankos0714/clipfactory/internal/db"
)

// setupServer arma un Server sobre una DB temporal y lo devuelve con su config.
func setupServer(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		DataDir:      filepath.Join(dir, "data"),
		DBPath:       filepath.Join(dir, "test.db"),
		PollInterval: 5 * time.Second,
	}
	for _, sub := range []string{"incoming", "completed", "thumbnails"} {
		if err := os.MkdirAll(filepath.Join(cfg.DataDir, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	if mutate != nil {
		mutate(cfg)
	}
	conn, err := db.InitDB(cfg.DBPath)
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return NewServer(cfg, conn)
}

// do realiza una petición HTTP contra el Server.
func do(t *testing.T, srv *Server, method, path string, body interface{}, token string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

func decodeBody(t *testing.T, rr *httptest.ResponseRecorder, dst interface{}) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body: %v (body: %s)", err, rr.Body.String())
	}
}

// ---------------------------------------------------------------- seed helpers

func seedSource(t *testing.T, srv *Server, platform, channelID, name string, active bool) int64 {
	t.Helper()
	src := &db.Source{Platform: platform, ChannelID: channelID, ChannelName: name, Active: active}
	if err := db.InsertSource(srv.db, src); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	return src.ID
}

func seedSourceClip(t *testing.T, srv *Server, sid int64, clipID, status string) int64 {
	t.Helper()
	now := time.Now().UTC()
	sc := &db.SourceClip{
		Platform:          "twitch",
		PlatformClipID:    clipID,
		SourceID:          sid,
		Title:             "clip " + clipID,
		DurationSeconds:   30,
		CreatedAtPlatform: &now,
		Status:            status,
	}
	if err := db.UpsertSourceClip(srv.db, sc); err != nil {
		t.Fatalf("seed source_clip: %v", err)
	}
	return sc.ID
}

// ----------------------------------------------------------------- 3.1 Health

func TestHealth(t *testing.T) {
	srv := setupServer(t, nil)
	rr := do(t, srv, http.MethodGet, "/api/health", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var out struct {
		Status        string `json:"status"`
		SchemaVersion int    `json:"schema_version"`
		Worker        string `json:"worker"`
	}
	decodeBody(t, rr, &out)
	if out.Status != "degraded" {
		t.Errorf("status = %q, want degraded (sin worker)", out.Status)
	}
	if out.SchemaVersion < 1 {
		t.Errorf("schema_version = %d, want >= 1", out.SchemaVersion)
	}
}

func TestSystemOverview(t *testing.T) {
	srv := setupServer(t, nil)
	seedSource(t, srv, "twitch", "chan9", "chan9", true)
	rr := do(t, srv, http.MethodGet, "/api/system/overview", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("overview status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var out map[string]interface{}
	decodeBody(t, rr, &out)
	if _, ok := out["sources"]; !ok {
		t.Errorf("overview sin key sources: %v", out)
	}
}

func TestSystemConfigOmitsSecrets(t *testing.T) {
	srv := setupServer(t, func(c *config.Config) {
		c.Twitch.ClientID = "secret-client-id"
		c.YouTube.RefreshToken = "secret-token"
		c.MinFreeDiskSpace = 1024 * 1024 * 1024
	})
	rr := do(t, srv, http.MethodGet, "/api/system/config", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("config status = %d (body: %s)", rr.Code, rr.Body.String())
	}
	raw := rr.Body.String()
	if strings.Contains(raw, "secret-client-id") || strings.Contains(raw, "secret-token") {
		t.Fatalf("config expone secretos: %s", raw)
	}
	var out struct {
		PollIntervalSeconds int             `json:"poll_interval_seconds"`
		Concurrency         int             `json:"concurrency"`
		MaxDiskUsageGB      float64         `json:"max_disk_usage_gb"`
		Credentials         map[string]bool `json:"credentials"`
	}
	decodeBody(t, rr, &out)
	if !out.Credentials["twitch"] {
		t.Errorf("credentials.twitch = false, want true (presente)")
	}
	if out.MaxDiskUsageGB != 1.0 {
		t.Errorf("max_disk_usage_gb = %v, want 1", out.MaxDiskUsageGB)
	}
}

// ----------------------------------------------------------------- 3.2 Sources

func TestSourcesCRUD(t *testing.T) {
	srv := setupServer(t, nil)

	// POST → 201
	rr := do(t, srv, http.MethodPost, "/api/sources", map[string]interface{}{
		"platform": "twitch", "channel_id": "4919", "channel_name": "illojuan", "active": true,
	}, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create source = %d, want 201 (body: %s)", rr.Code, rr.Body.String())
	}
	var created SourceDTO
	decodeBody(t, rr, &created)
	if created.ID == 0 || created.ChannelName != "illojuan" {
		t.Fatalf("creada mal: %+v", created)
	}

	// duplicado (platform, channel_id) → 409
	rr = do(t, srv, http.MethodPost, "/api/sources", map[string]interface{}{
		"platform": "twitch", "channel_id": "4919", "channel_name": "otro", "active": true,
	}, "")
	if rr.Code != http.StatusConflict {
		t.Errorf("duplicado = %d, want 409 (body: %s)", rr.Code, rr.Body.String())
	}

	// validación: plataforma desconocida → 400
	rr = do(t, srv, http.MethodPost, "/api/sources", map[string]interface{}{
		"platform": "x", "channel_id": "1",
	}, "")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("platform inválida = %d, want 400", rr.Code)
	}

	// GET /api/sources → envelope con paginación
	rr = do(t, srv, http.MethodGet, "/api/sources?page=1&pageSize=10", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list sources = %d", rr.Code)
	}
	var list struct {
		Data       []SourceDTO   `json:"data"`
		Pagination paginationDTO `json:"pagination"`
	}
	decodeBody(t, rr, &list)
	if len(list.Data) != 1 || list.Pagination.Total != 1 {
		t.Errorf("lista inesperada: %+v", list)
	}

	// GET detalle → conteos derivados (camelCase)
	rr = do(t, srv, http.MethodGet, fmt.Sprintf("/api/sources/%d", created.ID), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get source = %d", rr.Code)
	}
	var detail struct {
		SourceDTO
		Counts struct {
			ClipsDetected   int `json:"clipsDetected"`
			ClipsDownloaded int `json:"clipsDownloaded"`
		} `json:"counts"`
	}
	decodeBody(t, rr, &detail)
	if detail.Counts.ClipsDetected != 0 {
		t.Errorf("counts.clipsDetected = %d, want 0", detail.Counts.ClipsDetected)
	}

	// PATCH active=false → 200 y queda inactivo
	rr = do(t, srv, http.MethodPatch, fmt.Sprintf("/api/sources/%d", created.ID), map[string]interface{}{"active": false}, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("patch source = %d (body: %s)", rr.Code, rr.Body.String())
	}
	var patched SourceDTO
	decodeBody(t, rr, &patched)
	if patched.Active {
		t.Errorf("active sigue true tras PATCH")
	}

	// DELETE → 204, luego GET → 404
	rr = do(t, srv, http.MethodDelete, fmt.Sprintf("/api/sources/%d", created.ID), nil, "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete source = %d", rr.Code)
	}
	rr = do(t, srv, http.MethodGet, fmt.Sprintf("/api/sources/%d", created.ID), nil, "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("get borrado = %d, want 404", rr.Code)
	}
}

func TestSourcesPaginationBounds(t *testing.T) {
	srv := setupServer(t, nil)
	rr := do(t, srv, http.MethodGet, "/api/sources?page=1&pageSize=101", nil, "")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("pageSize 101 = %d, want 400", rr.Code)
	}
	rr = do(t, srv, http.MethodGet, "/api/sources?page=0", nil, "")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("page 0 = %d, want 400", rr.Code)
	}
}

// ----------------------------------------------------------------- 3.3/3.4/3.5 Clips

func TestListClipsBacklogSortRejected(t *testing.T) {
	srv := setupServer(t, nil)
	rr := do(t, srv, http.MethodGet, "/api/clips?sort=views", nil, "")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("sort=views = %d, want 400 (BACKLOG, no simular)", rr.Code)
	}
	rr = do(t, srv, http.MethodGet, "/api/clips?min_views=10", nil, "")
	if rr.Code != http.StatusBadRequest {
		t.Errorf("min_views = %d, want 400 (BACKLOG)", rr.Code)
	}
}

func TestClipDetailDTO(t *testing.T) {
	srv := setupServer(t, nil)
	sid := seedSource(t, srv, "twitch", "c1", "canal1", true)
	scid := seedSourceClip(t, srv, sid, "ClipAB1", "detected")

	// sin video aún: video=null, clip=null, metrics.available=false
	rr := do(t, srv, http.MethodGet, fmt.Sprintf("/api/clips/%d", scid), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get clip = %d (body: %s)", rr.Code, rr.Body.String())
	}
	var item ClipItemDTO
	decodeBody(t, rr, &item)
	if item.SourceClip.ID != scid || item.Video != nil || item.Clip != nil {
		t.Errorf("DTO inesperado: %+v", item)
	}
	if item.Metrics.Available {
		t.Errorf("metrics.available debe ser false (no hay métricas en el backend)")
	}

	// añadir video + clip → árbol completo
	video := &db.Video{SourceClipID: scid, Filepath: filepath.Join(srv.cfg.DataDir, "incoming", "ClipAB1.mp4"), Status: "incoming"}
	if err := db.InsertVideo(srv.db, video); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	clip := &db.Clip{VideoID: video.ID, Filepath: filepath.Join(srv.cfg.DataDir, "completed", "ClipAB1.mp4"), Status: "completed"}
	if err := db.InsertClip(srv.db, clip); err != nil {
		t.Fatalf("insert clip: %v", err)
	}
	rr = do(t, srv, http.MethodGet, fmt.Sprintf("/api/clips/%d", scid), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get clip full = %d", rr.Code)
	}
	decodeBody(t, rr, &item)
	if item.Video == nil || item.Clip == nil || item.Video.ID != video.ID || item.Clip.ID != clip.ID {
		t.Errorf("árbol incompleto: %+v", item)
	}
	if item.Channel.ChannelName != "canal1" {
		t.Errorf("channel ref mal: %+v", item.Channel)
	}
}

func TestClipActions(t *testing.T) {
	srv := setupServer(t, nil)
	sid := seedSource(t, srv, "twitch", "c2", "canal2", true)
	scid := seedSourceClip(t, srv, sid, "ClipZ9", "detected")

	// queue-for-download → 200, encola job download
	rr := do(t, srv, http.MethodPost, fmt.Sprintf("/api/source-clips/%d/download", scid), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("download = %d (body: %s)", rr.Code, rr.Body.String())
	}
	var action jobActionDTO
	decodeBody(t, rr, &action)
	if action.Job.Type != "download" || action.Job.ReferenceType != "source_clips" || !action.Created {
		t.Errorf("job download mal: %+v", action)
	}
	jobID := action.Job.ID

	// segunda llamada → idempotente (created=false, mismo job activo)
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/clips/%d/queue-for-download", scid), nil, "")
	decodeBody(t, rr, &action)
	if rr.Code != http.StatusOK || action.Created || action.Job.ID != jobID {
		t.Errorf("download repetido no fue idempotente: code=%d %+v", rr.Code, action)
	}

	// skip desde detected → 200, status skipped
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/source-clips/%d/skip", scid), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("skip = %d (body: %s)", rr.Code, rr.Body.String())
	}
	var sc SourceClipDTO
	decodeBody(t, rr, &sc)
	if sc.Status != "skipped" {
		t.Errorf("status tras skip = %q, want skipped", sc.Status)
	}

	// download sobre un clip ya skipped → 409
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/source-clips/%d/download", scid), nil, "")
	if rr.Code != http.StatusConflict {
		t.Errorf("download de clip skipped = %d, want 409", rr.Code)
	}
}

func TestClipQueueProcessAndThumbnail(t *testing.T) {
	srv := setupServer(t, nil)
	sid := seedSource(t, srv, "twitch", "c3", "canal3", true)
	scid := seedSourceClip(t, srv, sid, "ClipPP", "downloaded")

	video := &db.Video{SourceClipID: scid, Filepath: filepath.Join(srv.cfg.DataDir, "incoming", "ClipPP.mp4"), Status: "incoming"}
	if err := db.InsertVideo(srv.db, video); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	clip := &db.Clip{VideoID: video.ID, Filepath: filepath.Join(srv.cfg.DataDir, "completed", "ClipPP.mp4"), Status: "completed"}
	if err := db.InsertClip(srv.db, clip); err != nil {
		t.Fatalf("insert clip: %v", err)
	}

	// queue-for-process → job process con ref=videos
	rr := do(t, srv, http.MethodPost, fmt.Sprintf("/api/clips/%d/queue-for-process", scid), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("queue-for-process = %d (body: %s)", rr.Code, rr.Body.String())
	}
	var action jobActionDTO
	decodeBody(t, rr, &action)
	if action.Job.Type != "process" || action.Job.ReferenceID != video.ID {
		t.Errorf("job process mal: %+v", action)
	}

	// regenerate-thumbnail → job thumbnail con ref=clips
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/clips/%d/regenerate-thumbnail", scid), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("regenerate-thumbnail = %d (body: %s)", rr.Code, rr.Body.String())
	}
	decodeBody(t, rr, &action)
	if action.Job.Type != "thumbnail" || action.Job.ReferenceID != clip.ID {
		t.Errorf("job thumbnail mal: %+v", action)
	}
}

// ----------------------------------------------------------------- 3.2 discovery

func TestDiscoverSourceIdempotent(t *testing.T) {
	srv := setupServer(t, nil)
	sid := seedSource(t, srv, "kick", "k1", "kick1", true)

	rr := do(t, srv, http.MethodPost, fmt.Sprintf("/api/sources/%d/discovery", sid), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("discovery = %d (body: %s)", rr.Code, rr.Body.String())
	}
	var action jobActionDTO
	decodeBody(t, rr, &action)
	if !action.Created || action.Job.Type != "discovery" {
		t.Fatalf("discovery mal: %+v", action)
	}

	// segunda llamada → no duplica (created=false)
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/sources/%d/discovery", sid), nil, "")
	decodeBody(t, rr, &action)
	if action.Created {
		t.Errorf("discovery duplicado encolado (created=true)")
	}

	// discovery de canal inexistente → 404
	rr = do(t, srv, http.MethodPost, "/api/sources/9999/discovery", nil, "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("discovery inexistente = %d, want 404", rr.Code)
	}
}

// ----------------------------------------------------------------- 3.7 Publications

func TestPublicationsFlow(t *testing.T) {
	srv := setupServer(t, nil)
	sid := seedSource(t, srv, "twitch", "c4", "canal4", true)
	scid := seedSourceClip(t, srv, sid, "ClipPub", "downloaded")

	// armar clip procesado (requisito para publication)
	video := &db.Video{SourceClipID: scid, Filepath: filepath.Join(srv.cfg.DataDir, "incoming", "ClipPub.mp4"), Status: "completed"}
	if err := db.InsertVideo(srv.db, video); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	clip := &db.Clip{VideoID: video.ID, Filepath: filepath.Join(srv.cfg.DataDir, "completed", "ClipPub.mp4"), Status: "completed"}
	if err := db.InsertClip(srv.db, clip); err != nil {
		t.Fatalf("insert clip: %v", err)
	}

	// POST publication → 201
	rr := do(t, srv, http.MethodPost, "/api/publications", map[string]interface{}{"clip_id": clip.ID, "platform": "youtube"}, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create publication = %d (body: %s)", rr.Code, rr.Body.String())
	}
	var pub PublicationDTO
	decodeBody(t, rr, &pub)
	if pub.Status != "pending" {
		t.Errorf("status = %q, want pending", pub.Status)
	}

	// duplicado (clip_id, platform) → 409
	rr = do(t, srv, http.MethodPost, "/api/publications", map[string]interface{}{"clip_id": clip.ID, "platform": "youtube"}, "")
	if rr.Code != http.StatusConflict {
		t.Errorf("publication duplicada = %d, want 409", rr.Code)
	}

	// clip inexistente → 404
	rr = do(t, srv, http.MethodPost, "/api/publications", map[string]interface{}{"clip_id": 9999, "platform": "youtube"}, "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("publication clip inexistente = %d, want 404", rr.Code)
	}

	// retry → encola job publish; repetido → idempotente (created=false)
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/publications/%d/retry", pub.ID), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("retry publication = %d (body: %s)", rr.Code, rr.Body.String())
	}
	var action jobActionDTO
	decodeBody(t, rr, &action)
	if action.Job.Type != "publish" || action.Job.ReferenceID != pub.ID {
		t.Errorf("job publish mal: %+v", action)
	}
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/publications/%d/retry", pub.ID), nil, "")
	decodeBody(t, rr, &action)
	if action.Created {
		t.Errorf("retry publication duplicado (created=true)")
	}

	// cancel → failed (dead-letter)
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/publications/%d/cancel", pub.ID), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("cancel publication = %d (body: %s)", rr.Code, rr.Body.String())
	}
	decodeBody(t, rr, &pub)
	if pub.Status != "failed" {
		t.Errorf("status tras cancel = %q, want failed", pub.Status)
	}

	// cancel de una ya failed → 409
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/publications/%d/cancel", pub.ID), nil, "")
	if rr.Code != http.StatusConflict {
		t.Errorf("cancel de failed = %d, want 409", rr.Code)
	}
}

// ----------------------------------------------------------------- 3.8 Jobs

func TestJobsStatsAndRetry(t *testing.T) {
	srv := setupServer(t, nil)
	sid := seedSource(t, srv, "twitch", "c5", "canal5", true)
	var action jobActionDTO
	rr := do(t, srv, http.MethodPost, fmt.Sprintf("/api/sources/%d/discovery", sid), nil, "")
	decodeBody(t, rr, &action)

	// jobs stats (conteos por tipo/estado)
	rr = do(t, srv, http.MethodGet, "/api/jobs/stats", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("jobs stats = %d", rr.Code)
	}
	var stats map[string]map[string]int
	decodeBody(t, rr, &stats)
	if stats["discovery"]["queued"] != 1 {
		t.Errorf("jobs.stats discovery.queued = %d, want 1", stats["discovery"]["queued"])
	}

	// listar jobs (default excluye done)
	rr = do(t, srv, http.MethodGet, "/api/jobs", nil, "")
	var list struct {
		Data []JobDTO `json:"data"`
	}
	decodeBody(t, rr, &list)
	if len(list.Data) != 1 || list.Data[0].Type != "discovery" {
		t.Errorf("jobs list = %+v", list.Data)
	}

	// retry solo de jobs en error → 409 si está queued
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/jobs/%d/retry", list.Data[0].ID), nil, "")
	if rr.Code != http.StatusConflict {
		t.Errorf("retry job queued = %d, want 409", rr.Code)
	}

	// cancel de job queued → 200 (queda error), retry ahora sí → 200
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/jobs/%d/cancel", list.Data[0].ID), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("cancel job = %d (body: %s)", rr.Code, rr.Body.String())
	}
	rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/jobs/%d/retry", list.Data[0].ID), nil, "")
	if rr.Code != http.StatusOK {
		t.Errorf("retry job error = %d, want 200", rr.Code)
	}
}

// -------------------------------------------------------------- Auth (bearer)

func TestAuth(t *testing.T) {
	// Sin token configurado → API pública (desarrollo).
	srv := setupServer(t, nil)
	rr := do(t, srv, http.MethodGet, "/api/health", nil, "")
	if rr.Code != http.StatusOK {
		t.Errorf("sin token, health = %d, want 200", rr.Code)
	}

	// Con token configurado → 401 sin Authorization, 200 con el correcto.
	srv = setupServer(t, func(c *config.Config) { c.APIToken = "s3cret" })
	rr = do(t, srv, http.MethodGet, "/api/sources", nil, "")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("sin auth = %d, want 401", rr.Code)
	}
	rr = do(t, srv, http.MethodGet, "/api/sources", nil, "malo")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("token malo = %d, want 401", rr.Code)
	}
	rr = do(t, srv, http.MethodGet, "/api/sources", nil, "s3cret")
	if rr.Code != http.StatusOK {
		t.Errorf("token bueno = %d, want 200", rr.Code)
	}
	// health siempre pública (aliveness no requiere credenciales)
	rr = do(t, srv, http.MethodGet, "/api/health", nil, "")
	if rr.Code != http.StatusOK {
		t.Errorf("health con auth activo = %d, want 200", rr.Code)
	}
}

// ------------------------------------------------------------ Archivos (3.5)

func TestServeVideoAndThumbnail(t *testing.T) {
	srv := setupServer(t, nil)
	sid := seedSource(t, srv, "twitch", "c6", "canal6", true)
	scid := seedSourceClip(t, srv, sid, "ClipF1", "downloaded")

	// video con archivo real DENTRO de DataDir
	videoPath := filepath.Join(srv.cfg.DataDir, "incoming", "ClipF1.mp4")
	if err := os.WriteFile(videoPath, []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatalf("write video file: %v", err)
	}
	video := &db.Video{SourceClipID: scid, Filepath: videoPath, Status: "incoming"}
	if err := db.InsertVideo(srv.db, video); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	// GET video → 200, video/mp4
	rr := do(t, srv, http.MethodGet, fmt.Sprintf("/api/clips/%d/video", scid), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get video = %d (body: %s)", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "video/mp4") {
		t.Errorf("content-type = %q, want video/mp4", ct)
	}

	// byte-range: RESPECTA el rango (necesario para scrubbing)
	rr = do(t, srv, http.MethodGet, fmt.Sprintf("/api/clips/%d/video", scid), nil, "")
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/clips/%d/video", scid), nil)
	req.Header.Set("Range", "bytes=0-3")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusPartialContent {
		t.Errorf("range = %d, want 206", rr.Code)
	}
	if rr.Body.String() != "0123" {
		t.Errorf("range body = %q, want 0123", rr.Body.String())
	}

	// archivo FUERA de DataDir → 404 (no se exponen rutas arbitrarias)
	evil := filepath.Join(t.TempDir(), "evil.mp4")
	os.WriteFile(evil, []byte("x"), 0o644)
	evilVideo := &db.Video{SourceClipID: scid, Filepath: evil, Status: "incoming"}
	if err := db.InsertVideo(srv.db, evilVideo); err != nil {
		t.Fatalf("insert evil video: %v", err)
	}
	// ListClips toma el video con MAX(id): ahora el clip apunta al "evil", cuyo
	// path está fuera del DataDir → el servidor debe negar el archivo (404).
	rr = do(t, srv, http.MethodGet, fmt.Sprintf("/api/clips/%d/video", scid), nil, "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("video fuera del DataDir = %d, want 404 (no se debe exponer)", rr.Code)
	}
}

// -------------------------------------------------------- BACKLOG no simulados

func TestBacklogEndpointsNotFound(t *testing.T) {
	srv := setupServer(t, nil)
	for _, path := range []string{
		"/api/analytics/overview",
		"/api/analytics/timeseries?granularity=day",
		"/api/automations",
		"/api/clips/1/review",
		"/api/sources/sync-file",
		"/api/jobs/1/priority",
		"/api/jobs/1/pause",
		"/api/events",
	} {
		rr := do(t, srv, http.MethodGet, path, nil, "")
		if rr.Code == http.StatusOK {
			t.Errorf("%s: endpoint BACKLOG respondió 200 (no debe existir)", path)
		}
	}
}
