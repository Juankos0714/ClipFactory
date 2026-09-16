package worker

// Tests del apagado graceful: un job EN EJECUCIÓN cuando el ctx se cancela se
// RE-ENCOLA ('queued', sin error) en vez de marcarse 'error', y Close() solo
// cierra la DB que el worker abrió por sí mismo.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/db"
)

// ctxCancellingDownloader es un Downloader que devuelve error cuando el ctx
// está cancelado (simula una descarga abortada por el shutdown) y queda
// "trabajando" hasta que se le avisa.
type ctxCancellingDownloader struct {
	started   chan struct{}
	release   chan struct{}
	downloads int
}

func newCtxCancellingDownloader() *ctxCancellingDownloader {
	return &ctxCancellingDownloader{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (d *ctxCancellingDownloader) DownloadClip(ctx context.Context, clipID string, destPath string) error {
	d.downloads++
	close(d.started) // avisar que el handler ya está "en vuelo"
	<-d.release      // sostener el trabajo hasta que el test lo suelte
	if ctx.Err() != nil {
		return ctx.Err() // descarga abortada por el apagado
	}
	return nil
}

// TestExecuteJobRequeuesOnCancelledContext: handler que falla PORQUE el ctx fue
// cancelado (shutdown) → el job NO queda 'error', vuelve a 'queued'.
func TestExecuteJobRequeuesOnCancelledContext(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)

	// cadena source_clip válida para executeDownload
	var sourceID int64
	if err := conn.QueryRow(`SELECT id FROM sources WHERE channel_id='12345'`).Scan(&sourceID); err != nil {
		t.Fatalf("get source: %v", err)
	}
	sc := &db.SourceClip{Platform: "twitch", PlatformClipID: "shutdown-clip", SourceID: sourceID, Status: "detected"}
	if err := db.UpsertSourceClip(conn, sc); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// registrar el downloader por plataforma
	dl := newCtxCancellingDownloader()
	w.downloaders = map[string]Downloader{"twitch": dl}

	job := &db.Job{Type: "download", ReferenceID: sc.ID, ReferenceType: "source_clips"}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(conn, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w.executeJob(ctx, *job)
	}()

	<-dl.started        // el handler está en vuelo
	cancel()            // simular la señal → Stop() → ctx cancelado
	close(dl.release)   // soltar el handler
	<-done              // esperar a que executeJob retorne

	// el job debe estar 'queued' (re-encolado), NO 'error'
	var status, errMsg sql.NullString
	if err := conn.QueryRow(`SELECT status, error_message FROM jobs WHERE id = ?`, job.ID).Scan(&status, &errMsg); err != nil {
		t.Fatalf("scan job: %v", err)
	}
	if status.String != "queued" {
		t.Errorf("expected status 'queued' after shutdown, got %q (error_message=%v)", status.String, errMsg)
	}

	// y el source_clip NO debe quedar marcado como error de descarga
	got, err := db.GetSourceClipByID(conn, sc.ID)
	if err != nil || got == nil {
		t.Fatalf("get source_clip: %v %v", err, got)
	}
	if got.Status == "error" {
		t.Errorf("source_clip no debe marcarse error por un shutdown, got %q (%s)", got.Status, got.ErrorMessage)
	}
}

// TestExecuteJobFailsNormallyWithoutCancel: mismo handler fallando SIN ctx
// cancelado → el job SÍ queda 'error' (el re-encolado solo aplica al apagado).
func TestExecuteJobFailsNormallyWithoutCancel(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.downloaders = map[string]Downloader{"twitch": &fakeDownloader{failFor: map[string]error{"real-fail-clip": errors.New("boom de verdad")}}}

	var sourceID int64
	if err := conn.QueryRow(`SELECT id FROM sources WHERE channel_id='12345'`).Scan(&sourceID); err != nil {
		t.Fatalf("get source: %v", err)
	}
	sc := &db.SourceClip{Platform: "twitch", PlatformClipID: "real-fail-clip", SourceID: sourceID, Status: "detected"}
	if err := db.UpsertSourceClip(conn, sc); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	job := &db.Job{Type: "download", ReferenceID: sc.ID, ReferenceType: "source_clips"}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(conn, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	_ = w.executeJob(context.Background(), *job)

	var status string
	if err := conn.QueryRow(`SELECT status FROM jobs WHERE id = ?`, job.ID).Scan(&status); err != nil {
		t.Fatalf("scan job: %v", err)
	}
	if status != "error" {
		t.Errorf("expected status 'error' without shutdown, got %q", status)
	}
}

// TestCloseClosesOwnDBOnly: Close() cierra la DB solo si el worker la abrió
// (ownDB); con DB inyectada queda abierta.
func TestCloseClosesOwnDBOnly(t *testing.T) {
	// DB inyectada: Close() NO la cierra
	injected := openTestDB(t)
	w := newTestWorker(t, injected)
	if w.ownDB {
		t.Fatal("expected ownDB=false with injected DB")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close with injected db: %v", err)
	}
	if err := injected.Ping(); err != nil {
		t.Errorf("injected DB should still be open after Close(): %v", err)
	}

	// DB propia (NewWorker abre con DBPath): Close() SÍ la cierra
	w2, err := NewWorker(WorkerConfig{DBPath: filepath.Join(t.TempDir(), "own.db")})
	if err != nil {
		t.Fatalf("create worker with own db: %v", err)
	}
	if !w2.ownDB {
		t.Fatal("expected ownDB=true with DBPath")
	}
	if err := w2.Close(); err != nil {
		t.Fatalf("close own db: %v", err)
	}
	if err := w2.db.Ping(); err == nil {
		t.Error("own DB should be closed after Close()")
	}
	_ = os.RemoveAll // (import os guardado para usos futuros; el TempDir ya limpia)
}

// TestStopWaitsForInFlightJobAndRequeues: Stop() espera al job en curso y este
// queda re-encolado (no error) porque el ctx fue cancelado por el Stop.
func TestStopWaitsForInFlightJobAndRequeues(t *testing.T) {
	conn := openTestDB(t)

	dl := newCtxCancellingDownloader()
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		WorkerID:          "shutdown-worker",
		MaxConcurrentJobs: 1,
		PollInterval:      20 * time.Millisecond,
		Downloaders:       map[string]Downloader{"twitch": dl},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var sourceID int64
	if err := conn.QueryRow(`SELECT id FROM sources WHERE channel_id='12345'`).Scan(&sourceID); err != nil {
		t.Fatalf("get source: %v", err)
	}
	sc := &db.SourceClip{Platform: "twitch", PlatformClipID: "stop-clip", SourceID: sourceID, Status: "detected"}
	if err := db.UpsertSourceClip(conn, sc); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	job := &db.Job{Type: "download", ReferenceID: sc.ID, ReferenceType: "source_clips"}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// esperar a que el downloader esté "en vuelo"
	select {
	case <-dl.started:
	case <-time.After(3 * time.Second):
		t.Fatal("downloader nunca arrancó")
	}

	// Stop: cancela el ctx, espera al handler, y el job queda re-encolado
	stopped := make(chan struct{})
	go func() { w.Stop(); close(stopped) }()

	// darle un momento para que el Stop cancele el ctx antes de soltar al handler
	time.Sleep(50 * time.Millisecond)
	close(dl.release)

	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() no retornó: no esperó/terminó el job en curso")
	}

	var status string
	if err := conn.QueryRow(`SELECT status FROM jobs WHERE id = ?`, job.ID).Scan(&status); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if status != "queued" {
		t.Errorf("expected job requeued ('queued') after graceful stop, got %q", status)
	}
}
