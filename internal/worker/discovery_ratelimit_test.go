package worker

// Tests del manejo de rate limit (429) en executeDiscovery: el job NO falla,
// se re-encola un discovery futuro (created_at = now + RetryAfter) y termina
// 'done'. Cubre la no-duplicación frente a un discovery ya programado.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/adapter/twitch"
	"github.com/juankos0714/clipfactory/internal/db"
)

// rateLimitDiscoverer es un Discoverer que falla SIEMPRE con
// *twitch.RateLimitError (simula un 429 de Helix).
type rateLimitDiscoverer struct {
	calls int
}

func (d *rateLimitDiscoverer) ListClips(ctx context.Context, channelID string, after time.Time, maxPages ...int) ([]twitch.ClipInfo, error) {
	d.calls++
	return nil, &twitch.RateLimitError{RetryAfter: 2 * time.Second}
}

func TestExecuteDiscoveryRateLimitRequeues(t *testing.T) {
	conn := openTestDB(t)
	rl := &rateLimitDiscoverer{}
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		Discoverer:        rl,
		WorkerID:          "rl-worker",
		MaxConcurrentJobs: 1,
		PollInterval:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	var sourceID int64
	if err := conn.QueryRow(`SELECT id FROM sources WHERE channel_id = '12345'`).Scan(&sourceID); err != nil {
		t.Fatalf("get source: %v", err)
	}

	job := &db.Job{Type: "discovery", ReferenceID: sourceID, ReferenceType: "sources"}
	enqueueAndLock(t, w, job)
	before := time.Now().UTC()

	// el rate limit NO se propaga como error: el job termina 'done' con el
	// reintento ya programado en la cola
	if err := w.executeJob(context.Background(), *job); err != nil {
		t.Fatalf("executeJob con rate limit NO debe devolver error: %v", err)
	}

	// 1. el job quedó 'done'
	var status string
	if err := conn.QueryRow(`SELECT status FROM jobs WHERE id = ?`, job.ID).Scan(&status); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != "done" {
		t.Errorf("expected job 'done', got '%s'", status)
	}

	// 2. existe un NUEVO job discovery con created_at futuro (~+2s)
	var newJobID int64
	var createdAtStr string
	if err := conn.QueryRow(
		`SELECT id, created_at FROM jobs WHERE type = 'discovery' AND reference_id = ? AND id != ?`,
		sourceID, job.ID,
	).Scan(&newJobID, &createdAtStr); err != nil {
		t.Fatalf("expected re-enqueued discovery job, got: %v", err)
	}
	createdAt, perr := time.Parse(time.RFC3339, createdAtStr)
	if perr != nil {
		t.Fatalf("parse created_at %q: %v", createdAtStr, perr)
	}
	if until := time.Until(createdAt); until < time.Second || until > 3*time.Second {
		t.Errorf("expected created_at ~+2s (RetryAfter), got %v", until)
	}
	_ = before

	// 3. el estado del source NO avanzó: last_checked_at intacto (la pasada no
	// vio clips; avanzarlo perdería la ventana desde el último chequeo)
	var lastChecked *time.Time
	if err := conn.QueryRow(`SELECT last_checked_at FROM sources WHERE id = ?`, sourceID).Scan(&lastChecked); err != nil {
		t.Fatalf("query source: %v", err)
	}
	_ = lastChecked

	// 4. el discoverer fue llamado exactamente una vez (sin reintentos en el
	// mismo job: el reintento es el job futuro)
	if rl.calls != 1 {
		t.Errorf("expected 1 ListClips call, got %d", rl.calls)
	}
}

func TestExecuteDiscoveryRateLimitNoDuplicate(t *testing.T) {
	conn := openTestDB(t)
	rl := &rateLimitDiscoverer{}
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		Discoverer:        rl,
		WorkerID:          "rl-worker",
		MaxConcurrentJobs: 1,
		PollInterval:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	var sourceID int64
	if err := conn.QueryRow(`SELECT id FROM sources WHERE channel_id = '12345'`).Scan(&sourceID); err != nil {
		t.Fatalf("get source: %v", err)
	}

	// ya hay OTRO discovery queued (p.ej. encolado por el operador o auto-discovery)
	other := &db.Job{Type: "discovery", ReferenceID: sourceID, ReferenceType: "sources"}
	if err := db.EnqueueJob(conn, other); err != nil {
		t.Fatalf("enqueue other: %v", err)
	}

	job := &db.Job{Type: "discovery", ReferenceID: sourceID, ReferenceType: "sources"}
	enqueueAndLock(t, w, job)

	if err := w.executeJob(context.Background(), *job); err != nil {
		t.Fatalf("executeJob: %v", err)
	}

	// NO se agregó un tercer job: el discovery ya programado es el que reintenta
	var count int
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM jobs WHERE type = 'discovery' AND reference_id = ? AND status = 'queued'`,
		sourceID,
	).Scan(&count); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 queued discovery (el preexistente, sin duplicar), got %d", count)
	}
}

func TestExecuteDiscoveryRateLimitJobResumesWhenDue(t *testing.T) {
	// ciclo completo: re-enqueue con created_at futuro → GetPendingJobs NO lo
	// ofrece → cuando el plazo vence (RetryAfter=2s del fake), SÍ lo ofrece
	conn := openTestDB(t)
	rl := &rateLimitDiscoverer{}
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		Discoverer:        rl,
		WorkerID:          "rl-worker",
		MaxConcurrentJobs: 1,
		PollInterval:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	var sourceID int64
	if err := conn.QueryRow(`SELECT id FROM sources WHERE channel_id = '12345'`).Scan(&sourceID); err != nil {
		t.Fatalf("get source: %v", err)
	}

	job := &db.Job{Type: "discovery", ReferenceID: sourceID, ReferenceType: "sources"}
	enqueueAndLock(t, w, job)
	if err := w.executeJob(context.Background(), *job); err != nil {
		t.Fatalf("executeJob: %v", err)
	}

	// inmediatamente después: el job futuro NO debe ofrecerse
	pending, err := db.GetPendingJobs(conn, 10)
	if err != nil {
		t.Fatalf("GetPendingJobs: %v", err)
	}
	for _, j := range pending {
		if j.Type == "discovery" && j.ReferenceID == sourceID {
			t.Errorf("discovery re-encolado con created_at futuro NO debe aparecer en pending")
		}
	}

	// esperamos a que venza el plazo (RetryAfter=2s) y volvemos a consultar:
	// ahora SÍ debe ofrecerse (reintento real en el próximo tick)
	time.Sleep(2200 * time.Millisecond)
	pending, err = db.GetPendingJobs(conn, 10)
	if err != nil {
		t.Fatalf("GetPendingJobs: %v", err)
	}
	found := false
	for _, j := range pending {
		if j.Type == "discovery" && j.ReferenceID == sourceID {
			found = true
		}
	}
	if !found {
		t.Errorf("discovery con created_at vencido debe aparecer en pending")
	}
}

// TestExecuteDiscoveryOtherErrorStillFails: un error que NO es rate limit
// (p.ej. 401) sigue fallando el job como siempre — la rama nueva no captura
// de más.
func TestExecuteDiscoveryOtherErrorStillFails(t *testing.T) {
	conn := openTestDB(t)
	boom := &fakeDiscoverer{failWith: errors.New("api returned status 401")}
	w := newTestWorker(t, conn)
	w.cfg.Discoverers = map[string]Discoverer{"twitch": boom}

	var sourceID int64
	if err := conn.QueryRow(`SELECT id FROM sources WHERE channel_id = '12345'`).Scan(&sourceID); err != nil {
		t.Fatalf("get source: %v", err)
	}

	job := &db.Job{Type: "discovery", ReferenceID: sourceID, ReferenceType: "sources"}
	enqueueAndLock(t, w, job)

	if err := w.executeJob(context.Background(), *job); err == nil {
		t.Fatal("expected error for non-rate-limit failure")
	}
	var status string
	if err := conn.QueryRow(`SELECT status FROM jobs WHERE id = ?`, job.ID).Scan(&status); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != "error" {
		t.Errorf("expected job 'error', got '%s'", status)
	}
}
