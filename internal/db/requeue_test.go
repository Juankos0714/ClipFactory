package db

// Tests del shutdown graceful a nivel DB: RequeueJob (job 'running' → 'queued'
// con guard por locked_by) y la recuperación de jobs 'running' huérfanos por
// GetPendingJobs/LockJob (el caso "docker kill -9": el job queda running con un
// lock que nadie va a liberar).

import (
	"database/sql"
	"testing"
	"time"
)

// enqueueAndLock encola un job y lo deja 'running' con el worker dado.
func enqueueAndLock(t *testing.T, conn *sql.DB, workerID string) *Job {
	t.Helper()
	j := &Job{Type: "download", ReferenceID: 1, ReferenceType: "source_clips"}
	if err := EnqueueJob(conn, j); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := LockJob(conn, j.ID, workerID); err != nil {
		t.Fatalf("lock: %v", err)
	}
	return j
}

// TestRequeueJobRunning: un job 'running' con lock propio vuelve a 'queued'
// con lock limpio.
func TestRequeueJobRunning(t *testing.T) {
	conn := setupTestDB(t)
	defer conn.Close()

	j := enqueueAndLock(t, conn, "worker-a")

	requeued, err := RequeueJob(conn, j.ID, "worker-a")
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if !requeued {
		t.Fatal("expected requeued=true")
	}

	var status string
	var lockedAt, lockedBy sql.NullString
	if err := conn.QueryRow(`SELECT status, locked_at, locked_by FROM jobs WHERE id = ?`, j.ID).Scan(&status, &lockedAt, &lockedBy); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if status != "queued" {
		t.Errorf("expected status 'queued', got %q", status)
	}
	if lockedAt.Valid || lockedBy.Valid {
		t.Errorf("expected lock cleared, got locked_at=%v locked_by=%v", lockedAt, lockedBy)
	}

	// y vuelve a ser ejecutable de inmediato
	pending, err := GetPendingJobs(conn, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != j.ID {
		t.Errorf("expected requeued job to be pending again, got %v", pending)
	}
}

// TestRequeueJobGuardLockedBy: NO se puede re-encolar un job con lock de OTRO
// worker (protección contra pisar a un worker vivo que lo retomó por stale lock).
func TestRequeueJobGuardLockedBy(t *testing.T) {
	conn := setupTestDB(t)
	defer conn.Close()

	j := enqueueAndLock(t, conn, "worker-a")

	requeued, err := RequeueJob(conn, j.ID, "worker-b")
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if requeued {
		t.Error("expected requeued=false when locked by another worker")
	}

	var status string
	if err := conn.QueryRow(`SELECT status FROM jobs WHERE id = ?`, j.ID).Scan(&status); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if status != "running" {
		t.Errorf("expected status untouched 'running', got %q", status)
	}
}

// TestRequeueJobNotRunning: un job 'queued' o 'done' no se toca.
func TestRequeueJobNotRunning(t *testing.T) {
	conn := setupTestDB(t)
	defer conn.Close()

	j := &Job{Type: "download", ReferenceID: 1, ReferenceType: "source_clips"}
	if err := EnqueueJob(conn, j); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	requeued, err := RequeueJob(conn, j.ID, "worker-a")
	if err != nil {
		t.Fatalf("requeue queued: %v", err)
	}
	if requeued {
		t.Error("expected requeued=false for a queued job")
	}

	if err := LockJob(conn, j.ID, "worker-a"); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if err := CompleteJob(conn, j.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	requeued, err = RequeueJob(conn, j.ID, "worker-a")
	if err != nil {
		t.Fatalf("requeue done: %v", err)
	}
	if requeued {
		t.Error("expected requeued=false for a done job")
	}
}

// TestGetPendingJobsIncludesStaleRunning: un job 'running' huérfano (worker
// muerto con kill -9, lock >30s) se vuelve a ofrecer, y LockJob permite
// tomarlo — sin esto sería irrecuperable.
func TestGetPendingJobsIncludesStaleRunning(t *testing.T) {
	conn := setupTestDB(t)
	defer conn.Close()

	j := enqueueAndLock(t, conn, "worker-muerto")

	// lock reciente: NO se ofrece (el worker dueño puede estar vivo y trabajando)
	pending, err := GetPendingJobs(conn, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending with fresh lock, got %d", len(pending))
	}
	if err := LockJob(conn, j.ID, "worker-b"); err == nil {
		t.Error("expected LockJob to fail while lock is fresh")
	}

	// envejecer el lock: ahora sí es huérfano
	old := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	if _, err := conn.Exec(`UPDATE jobs SET locked_at = ? WHERE id = ?`, old, j.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	pending, err = GetPendingJobs(conn, 10)
	if err != nil {
		t.Fatalf("pending (2): %v", err)
	}
	if len(pending) != 1 || pending[0].ID != j.ID {
		t.Fatalf("expected stale running job offered, got %v", pending)
	}

	// y LockJob lo toma (el OR 'running' + lock vencido de la cláusula WHERE)
	if err := LockJob(conn, j.ID, "worker-b"); err != nil {
		t.Errorf("expected LockJob to succeed on stale running job: %v", err)
	}
}
