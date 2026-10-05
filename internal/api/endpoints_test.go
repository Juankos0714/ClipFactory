package api

// Cobertura de endpoints del contrato que aún no tenían test: 3.5 processed,
// 3.7 workers y 3.8 logs. Reusan el SQLite real y los helpers de api_test.go.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/db"
)

// --------------------------------------------------------- 3.5 processed

// /processed sirve el clip ya procesado (DataDir/completed), no el original.
func TestClipProcessed(t *testing.T) {
	srv := setupServer(t, nil)
	sid := seedSource(t, srv, "twitch", "c9", "canal9", true)
	scid := seedSourceClip(t, srv, sid, "ClipP1", "downloaded")

	// original descargado pero sin clip procesado ++' 404 (no 500)
	incoming := filepath.Join(srv.cfg.DataDir, "incoming", "ClipP1.mp4")
	if err := os.MkdirAll(filepath.Dir(incoming), 0o755); err != nil {
		t.Fatalf("mkdir incoming: %v", err)
	}
	if err := os.WriteFile(incoming, []byte("ORIGINAL-XXXX"), 0o644); err != nil {
		t.Fatalf("write incoming: %v", err)
	}
	video := &db.Video{SourceClipID: scid, Filepath: incoming, Status: "incoming"}
	if err := db.InsertVideo(srv.db, video); err != nil {
		t.Fatalf("insert incoming: %v", err)
	}
	rr := do(t, srv, http.MethodGet, fmt.Sprintf("/api/clips/%d/processed", scid), nil, "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("processed sin clip = %d, want 404", rr.Code)
	}

	// con clip procesado ++' 200 video/mp4 sirviendo el archivo de completed/
	processed := filepath.Join(srv.cfg.DataDir, "completed", "ClipP1.mp4")
	if err := os.MkdirAll(filepath.Dir(processed), 0o755); err != nil {
		t.Fatalf("mkdir completed: %v", err)
	}
	if err := os.WriteFile(processed, []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatalf("write processed: %v", err)
	}
	clip := &db.Clip{VideoID: video.ID, Filepath: processed, Status: "completed"}
	if err := db.InsertClip(srv.db, clip); err != nil {
		t.Fatalf("insert clip: %v", err)
	}

	rr = do(t, srv, http.MethodGet, fmt.Sprintf("/api/clips/%d/processed", scid), nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("processed = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", got)
	}
	if got := rr.Body.String(); got != "0123456789abcdef" {
		t.Errorf("body = %q, want el archivo procesado (no el original)", got)
	}
	// processed NO es cacheable (a diferencia de thumbnail): el worker lo puede reescribir
	if got := rr.Header().Get("Cache-Control"); got != "" {
		t.Errorf("processed lleva Cache-Control %q, want vacío", got)
	}
}

// ------------------------------------------------------------------ 3.7 Workers

// /api/workers deriva los workers activos de los jobs 'running' (locked_by).
func TestListWorkers(t *testing.T) {
	srv := setupServer(t, nil)

	// sin jobs corriendo ++' data: [] (array vacío, nunca null)
	rr := do(t, srv, http.MethodGet, "/api/workers", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("workers = %d", rr.Code)
	}
	var list struct {
		Data []WorkerDTO `json:"data"`
	}
	decodeBody(t, rr, &list)
	if list.Data == nil {
		t.Fatal("workers sin jobs debe devolver data: [], no null")
	}
	if len(list.Data) != 0 {
		t.Errorf("workers sin jobs = %+v, want []", list.Data)
	}

	// dos jobs queued (discovery es idempotente por source → dos sources)
	// ++' ninguno es worker todavía
	for _, cid := range []string{"c11", "c12"} {
		sid := seedSource(t, srv, "twitch", cid, "canal-"+cid, true)
		rr = do(t, srv, http.MethodPost, fmt.Sprintf("/api/sources/%d/discovery", sid), nil, "")
		if rr.Code != http.StatusOK {
			t.Fatalf("discovery %s = %d (body: %s)", cid, rr.Code, rr.Body.String())
		}
	}
	var jobs struct {
		Data []JobDTO `json:"data"`
	}
	rr = do(t, srv, http.MethodGet, "/api/jobs", nil, "")
	decodeBody(t, rr, &jobs)
	if len(jobs.Data) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs.Data))
	}
	rr = do(t, srv, http.MethodGet, "/api/workers", nil, "")
	decodeBody(t, rr, &list)
	if len(list.Data) != 0 {
		t.Errorf("jobs queued no son workers: %+v", list.Data)
	}

	// db.LockJob lo deja 'running' con locked_by ++' aparece en /api/workers
	if err := db.LockJob(srv.db, jobs.Data[0].ID, "worker-a"); err != nil {
		t.Fatalf("LockJob: %v", err)
	}
	rr = do(t, srv, http.MethodGet, "/api/workers", nil, "")
	decodeBody(t, rr, &list)
	if len(list.Data) != 1 {
		t.Fatalf("workers = %+v, want 1 (solo el job con lock)", list.Data)
	}
	if w := list.Data[0]; w.LockedBy != "worker-a" || w.JobType != "discovery" || w.LastLockedAt == nil {
		t.Errorf("worker DTO = %+v", w)
	}

	// job completado ++' el worker desaparece (ya no hay job running)
	if err := db.CompleteJob(srv.db, jobs.Data[0].ID); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	rr = do(t, srv, http.MethodGet, "/api/workers", nil, "")
	decodeBody(t, rr, &list)
	if len(list.Data) != 0 {
		t.Errorf("tras completar, workers = %+v, want []", list.Data)
	}
}

// ---------------------------------------------------------------------- 3.8 Logs

// /api/logs: envelope de paginación + filtros level/module del contrato.
func TestListLogsFiltrosYPaginacion(t *testing.T) {
	srv := setupServer(t, nil)
	seedLogs(t, srv, []logSeed{
		{"error", "worker", "fallo en ffmpeg"},
		{"error", "discovery", "no se pudo descubrir"},
		{"info", "worker", "job completado"},
		{"info", "discovery", "clip nuevo"},
	})

	// sin filtros ++' envelope de paginación completo
	rr := do(t, srv, http.MethodGet, "/api/logs", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("logs = %d", rr.Code)
	}
	var all struct {
		Data       []LogDTO      `json:"data"`
		Pagination paginationDTO `json:"pagination"`
	}
	decodeBody(t, rr, &all)
	if len(all.Data) != 4 {
		t.Errorf("logs totales = %d, want 4", len(all.Data))
	}
	if all.Pagination.Total != 4 || all.Pagination.PageCount != 1 || all.Pagination.HasNext || all.Pagination.PageSize != 50 {
		t.Errorf("paginación por defecto = %+v", all.Pagination)
	}
	for _, l := range all.Data {
		if l.CreatedAt == "" {
			t.Errorf("log %d sin created_at", l.ID)
		}
	}

	// filtro por level
	rr = do(t, srv, http.MethodGet, "/api/logs?level=error", nil, "")
	decodeBody(t, rr, &all)
	if len(all.Data) != 2 {
		t.Fatalf("logs level=error = %d, want 2", len(all.Data))
	}
	for _, l := range all.Data {
		if l.Level != "error" {
			t.Errorf("level=error devolvió level %q", l.Level)
		}
	}
	if all.Pagination.Total != 2 {
		t.Errorf("total con filtro = %d, want 2", all.Pagination.Total)
	}

	// filtro combinado level + module
	rr = do(t, srv, http.MethodGet, "/api/logs?level=error&module=discovery", nil, "")
	decodeBody(t, rr, &all)
	if len(all.Data) != 1 || all.Data[0].Module != "discovery" {
		t.Fatalf("logs error+discovery = %+v", all.Data)
	}
	if all.Data[0].VideoID != nil || all.Data[0].JobID != nil {
		t.Errorf("log sin relaciones debe traer *_ID en null: %+v", all.Data[0])
	}

	// filtro sin coincidencias ++' 200 con lista vacía (no error)
	rr = do(t, srv, http.MethodGet, "/api/logs?level=fatal", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("logs sin match = %d, want 200", rr.Code)
	}
	decodeBody(t, rr, &all)
	if all.Data == nil || len(all.Data) != 0 {
		t.Errorf("logs sin match = %+v, want data: []", all.Data)
	}
	if all.Pagination.Total != 0 || all.Pagination.PageCount != 0 {
		t.Errorf("paginación sin match = %+v, want total 0", all.Pagination)
	}

	// paginación real: pageSize=2 sobre 4 logs ++' 2 páginas con hasNext/hasPrev
	rr = do(t, srv, http.MethodGet, "/api/logs?page=1&pageSize=2", nil, "")
	decodeBody(t, rr, &all)
	if len(all.Data) != 2 || all.Pagination.PageCount != 2 || !all.Pagination.HasNext || all.Pagination.HasPrev {
		t.Errorf("page 1 de 2 = %d items, pag %+v", len(all.Data), all.Pagination)
	}
	firstPage := []int64{all.Data[0].ID, all.Data[1].ID}
	rr = do(t, srv, http.MethodGet, "/api/logs?page=2&pageSize=2", nil, "")
	decodeBody(t, rr, &all)
	if len(all.Data) != 2 || all.Pagination.HasNext || !all.Pagination.HasPrev {
		t.Errorf("page 2 de 2 = %d items, pag %+v", len(all.Data), all.Pagination)
	}
	// orden DESC por created_at: la página 2 trae los 2 más antiguos
	for i := range firstPage {
		if all.Data[i].ID == firstPage[i] {
			t.Errorf("página 2 repite la 1 (id %d)", all.Data[i].ID)
		}
	}

	// pageSize fuera de rango ++' 400 con envelope VALIDATION_ERROR
	rr = do(t, srv, http.MethodGet, "/api/logs?pageSize=101", nil, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("pageSize=101 = %d, want 400", rr.Code)
	}
	var env errorEnvelope
	decodeBody(t, rr, &env)
	if env.Error.Code != "VALIDATION_ERROR" || env.Error.Message == "" {
		t.Errorf("envelope = %+v", env.Error)
	}
}

// -------------------------------------------------------------- seed helpers

type logSeed struct {
	level, module, message string
}

func seedLogs(t *testing.T, srv *Server, seeds []logSeed) {
	t.Helper()
	// created_at crecientes para que el orden DESC por (created_at, id) sea determinista.
	base := time.Now().UTC().Add(-time.Duration(len(seeds)) * time.Minute)
	for i, s := range seeds {
		l := &db.Log{
			Level:     s.level,
			Module:    s.module,
			Message:   s.message,
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if err := db.InsertLog(srv.db, l); err != nil {
			t.Fatalf("insert log: %v", err)
		}
	}
}
