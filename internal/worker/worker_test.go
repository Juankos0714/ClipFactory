package worker

import (
	"database/sql"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/db"
	_ "modernc.org/sqlite"
)

func setupTestWorker(t *testing.T) (*Worker, *sql.DB) {
	// crear DB en memoria
	conn, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// una sola conexión: con :memory: cada conexión nueva es una DB distinta
	conn.SetMaxOpenConns(1)
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping db: %v", err)
	}
	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	// aplicar migraciones
	if err := db.MigrateDB(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// insertar una fuente de prueba
	if _, err := conn.Exec("INSERT INTO sources (platform, channel_id, channel_name, active) VALUES ('twitch', '12345', 'test_channel', 1)"); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	// crear worker con la DB inyectada (misma que usa el test)
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		WorkerID:          "test-worker",
		MaxConcurrentJobs: 2,
		PollInterval:      100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	return w, conn
}

func TestWorkerInit(t *testing.T) {
	w, conn := setupTestWorker(t)
	defer conn.Close()
	defer w.Stop()

	if w == nil {
		t.Error("expected worker to be created")
	}

	// verificar que el worker se creó correctamente
	status := w.Status()
	if status["worker_id"] != "test-worker" {
		t.Errorf("expected worker_id 'test-worker', got %v", status["worker_id"])
	}
	if status["max_concurrent"] != 2 {
		t.Errorf("expected max_concurrent 2, got %v", status["max_concurrent"])
	}
}

func TestWorkerStartStop(t *testing.T) {
	w, conn := setupTestWorker(t)
	defer conn.Close()

	if err := w.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}

	// verificar que el worker está corriendo
	status := w.Status()
	if status["started"] != true {
		t.Error("expected worker to be started")
	}

	// detener el worker
	w.Stop()

	// verificar que el worker se detuvo
	status = w.Status()
	if status["started"] != false {
		t.Error("expected worker to be stopped")
	}
}

func TestWorkerEnqueueAndProcessJob(t *testing.T) {
	w, conn := setupTestWorker(t)
	defer conn.Close()

	// encolar un job
	job := &db.Job{
		Type:          "discovery",
		ReferenceID:   1,
		ReferenceType: "source_clips",
		Status:        "queued",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}

	// iniciar el worker
	if err := w.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	defer w.Stop()

	// esperar un poco para que el worker procese el job
	time.Sleep(500 * time.Millisecond)

	// verificar que el job se procesó
	var status string
	err := conn.QueryRow("SELECT status FROM jobs WHERE id = ?", job.ID).Scan(&status)
	if err != nil {
		t.Fatalf("query job status: %v", err)
	}

	// el job puede estar en 'done' o 'error' dependiendo de la implementación
	if status != "done" && status != "error" {
		t.Errorf("expected job status 'done' or 'error', got '%s'", status)
	}
}

func TestWorkerConcurrency(t *testing.T) {
	w, conn := setupTestWorker(t)
	defer conn.Close()

	// encolar varios jobs
	for i := 0; i < 5; i++ {
		job := &db.Job{
			Type:          "process",
			ReferenceID:   int64(i),
			ReferenceType: "videos",
			Status:        "queued",
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
		}
		if err := db.EnqueueJob(conn, job); err != nil {
			t.Fatalf("enqueue job %d: %v", i, err)
		}
	}

	// iniciar el worker
	if err := w.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	defer w.Stop()

	// esperar un poco para que el worker procese los jobs
	time.Sleep(1 * time.Second)

	// contar los jobs procesados
	var doneCount, errorCount int
	conn.QueryRow("SELECT COUNT(*) FROM jobs WHERE status = 'done'").Scan(&doneCount)
	conn.QueryRow("SELECT COUNT(*) FROM jobs WHERE status = 'error'").Scan(&errorCount)

	total := doneCount + errorCount
	if total < 1 {
		t.Errorf("expected at least 1 job processed, got %d done + %d error", doneCount, errorCount)
	}
}

func TestWorkerLocking(t *testing.T) {
	_, conn := setupTestWorker(t)
	defer conn.Close()

	// encolar un job
	job := &db.Job{
		Type:          "download",
		ReferenceID:   999,
		ReferenceType: "source_clips",
		Status:        "queued",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}

	// intentar lockear el job directamente
	if err := db.LockJob(conn, job.ID, "test-worker"); err != nil {
		t.Fatalf("lock job: %v", err)
	}

	// verificar que el job se bloqueó
	var status, lockedBy string
	var lockedAt sql.NullString
	err := conn.QueryRow("SELECT status, locked_by, locked_at FROM jobs WHERE id = ?", job.ID).
		Scan(&status, &lockedBy, &lockedAt)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}

	if status != "running" {
		t.Errorf("expected status 'running', got '%s'", status)
	}
	if lockedBy != "test-worker" {
		t.Errorf("expected locked_by 'test-worker', got '%s'", lockedBy)
	}
	if !lockedAt.Valid {
		t.Error("expected locked_at to be set")
	}

	// intentar lockear el mismo job de nuevo (debe fallar porque ya está running)
	err = db.LockJob(conn, job.ID, "another-worker")
	if err == nil {
		t.Error("expected lock job to fail for already running job")
	}
}

func TestWorkerCompleteJob(t *testing.T) {
	_, conn := setupTestWorker(t)
	defer conn.Close()

	// encolar y bloquear un job
	job := &db.Job{
		Type:          "thumbnail",
		ReferenceID:   888,
		ReferenceType: "videos",
		Status:        "queued",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	if err := db.LockJob(conn, job.ID, "test-worker"); err != nil {
		t.Fatalf("lock job: %v", err)
	}

	// completar el job
	if err := db.CompleteJob(conn, job.ID); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	// verificar que el job se completó
	var status string
	err := conn.QueryRow("SELECT status FROM jobs WHERE id = ?", job.ID).Scan(&status)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}

	if status != "done" {
		t.Errorf("expected status 'done', got '%s'", status)
	}
}

func TestWorkerFailJob(t *testing.T) {
	_, conn := setupTestWorker(t)
	defer conn.Close()

	// encolar y bloquear un job
	job := &db.Job{
		Type:          "publish",
		ReferenceID:   777,
		ReferenceType: "clips",
		Status:        "queued",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	if err := db.LockJob(conn, job.ID, "test-worker"); err != nil {
		t.Fatalf("lock job: %v", err)
	}

	// fallar el job
	if err := db.FailJob(conn, job.ID, "error de prueba"); err != nil {
		t.Fatalf("fail job: %v", err)
	}

	// verificar que el job falló
	var status, errorMessage string
	err := conn.QueryRow("SELECT status, error_message FROM jobs WHERE id = ?", job.ID).
		Scan(&status, &errorMessage)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}

	if status != "error" {
		t.Errorf("expected status 'error', got '%s'", status)
	}
	if errorMessage != "error de prueba" {
		t.Errorf("expected error_message 'error de prueba', got '%s'", errorMessage)
	}
}
