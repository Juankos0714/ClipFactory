package worker

// Tests complementarios del worker: errores de construcción, Start/Stop
// idempotentes (doble Start, Stop sin Start, doble Stop) y handlers nil.

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/db"
	_ "modernc.org/sqlite"
)

// openTestDB crea una DB en memoria con migraciones aplicadas y una fuente de prueba.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// una sola conexión: con :memory: cada conexión nueva es una DB distinta
	conn.SetMaxOpenConns(1)
	if err := db.MigrateDB(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := conn.Exec("INSERT INTO sources (platform, channel_id, channel_name, active) VALUES ('twitch', '12345', 'test_channel', 1)"); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// newTestWorker crea un worker con la DB inyectada (sin arrancarlo).
func newTestWorker(t *testing.T, conn *sql.DB) *Worker {
	t.Helper()
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		WorkerID:          "status-worker",
		MaxConcurrentJobs: 1,
		PollInterval:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}
	return w
}

func TestNewWorkerInvalidDBPath(t *testing.T) {
	// ruta imposible: un archivo donde se espera un directorio no se puede crear como DB
	tmp := t.TempDir()
	blocked := filepath.Join(tmp, "blocked")
	if err := os.WriteFile(blocked, []byte("not a db"), 0o600); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}

	badPath := filepath.Join(blocked, "sub", "clipfactory.db")
	_, err := NewWorker(WorkerConfig{DBPath: badPath})
	if err == nil {
		t.Error("expected error for invalid DBPath, got nil")
	}
}

func TestNewWorkerInjectedDB(t *testing.T) {
	conn := openTestDB(t)

	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		WorkerID:          "injected-worker",
		MaxConcurrentJobs: 1,
		PollInterval:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker with injected DB: %v", err)
	}
	if w.db != conn {
		t.Error("expected worker to use the injected DB connection")
	}
}

func TestStartTwice(t *testing.T) {
	w := newTestWorker(t, openTestDB(t))

	if err := w.Start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	defer w.Stop()

	if err := w.Start(); err == nil {
		t.Error("expected error when starting an already-started worker")
	}
}

func TestStopWithoutStart(t *testing.T) {
	w := newTestWorker(t, openTestDB(t))
	// no debe entrar en pánico ni bloquearse
	w.Stop()
}

func TestStopTwice(t *testing.T) {
	w := newTestWorker(t, openTestDB(t))

	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	w.Stop()
	// la segunda llamada debe ser no-op (close de canal cerrado entra en pánico si no se protege)
	w.Stop()
}

func TestRestartAfterStop(t *testing.T) {
	w := newTestWorker(t, openTestDB(t))

	if err := w.Start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	w.Stop()

	// después de Stop, started vuelve a false: se puede arrancar de nuevo
	if err := w.Start(); err != nil {
		t.Fatalf("restart after stop: %v", err)
	}
	w.Stop()
}

func TestStatusFields(t *testing.T) {
	w := newTestWorker(t, openTestDB(t))

	status := w.Status()
	if status["worker_id"] != "status-worker" {
		t.Errorf("expected worker_id 'status-worker', got %v", status["worker_id"])
	}
	if status["started"] != false {
		t.Errorf("expected started false before Start, got %v", status["started"])
	}
	if status["max_concurrent"] != 1 {
		t.Errorf("expected max_concurrent 1, got %v", status["max_concurrent"])
	}
	if status["poll_interval"] != "50ms" {
		t.Errorf("expected poll_interval '50ms', got %v", status["poll_interval"])
	}

	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer w.Stop()

	status = w.Status()
	if status["started"] != true {
		t.Errorf("expected started true after Start, got %v", status["started"])
	}
}

func TestExecuteJobUnexpectedReferenceType(t *testing.T) {
	w := newTestWorker(t, openTestDB(t))

	// El schema v2 tiene CHECK(type IN (...)): un tipo realmente desconocido no
	// puede llegar a la cola (el default de executeJob es inalcanzable por DB).
	// Lo más cercano es un job válido cuyo handler no espera ese reference_type:
	// debe marcar el job 'error', sin pánico.
	job := &db.Job{Type: "download", ReferenceID: 1, ReferenceType: "videos"}
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	if err := db.LockJob(w.db, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock job: %v", err)
	}

	// no debe entrar en pánico; debe marcar el job como error
	w.executeJob(t.Context(), *job)

	var status, errMsg string
	if err := w.db.QueryRow("SELECT status, error_message FROM jobs WHERE id = ?", job.ID).Scan(&status, &errMsg); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != "error" {
		t.Errorf("expected status 'error', got '%s'", status)
	}
	if errMsg == "" {
		t.Error("expected error_message to be set for unknown job type")
	}
}

func TestExecuteKnownJobTypeCompletes(t *testing.T) {
	// 'download', 'discovery', 'process' y 'thumbnail' no van: tienen tests
	// dedicados (requieren Downloader/Discoverer/Processor/Thumbnailer).
	// 'publish' necesita su cadena completa de fixtures (clip + publication).
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.publishers = map[string]Publisher{"youtube": &fakePublisher{writeOutput: true}}
	pubs := setupPublishChains(t, conn, t.TempDir(), 1)

	job := &db.Job{Type: "publish", ReferenceID: pubs[0].ID, ReferenceType: "publications"}
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	if err := db.LockJob(w.db, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock job: %v", err)
	}

	w.executeJob(t.Context(), *job)

	var status string
	if err := w.db.QueryRow("SELECT status FROM jobs WHERE id = ?", job.ID).Scan(&status); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != "done" {
		t.Errorf("job type publish: expected status 'done', got '%s'", status)
	}
}

func TestWorkerProcessesJobsFromInjectedDB(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.publishers = map[string]Publisher{"youtube": &fakePublisher{writeOutput: true}}

	// 3 cadenas publication completas y sus jobs 'publish' encolados en la DB
	// compartida: el worker debe procesar los 3 sin configuración extra
	pubs := setupPublishChains(t, conn, t.TempDir(), 3)
	for _, p := range pubs {
		j := &db.Job{Type: "publish", ReferenceID: p.ID, ReferenceType: "publications"}
		if err := db.EnqueueJob(conn, j); err != nil {
			t.Fatalf("enqueue job: %v", err)
		}
	}

	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// esperar a que el worker los procese (poll 50ms + trabajo 100ms c/u)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var done int
		if err := conn.QueryRow("SELECT COUNT(*) FROM jobs WHERE status = 'done'").Scan(&done); err != nil {
			t.Fatalf("count done jobs: %v", err)
		}
		if done == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 3 jobs done, got %d after deadline", done)
		}
		time.Sleep(50 * time.Millisecond)
	}

	w.Stop()
}
