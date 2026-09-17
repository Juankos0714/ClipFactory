package worker

// Tests del job 'publish' (publicación del clip en YouTube vía el Publisher).
//
// Se usa un fakePublisher que replica el contrato del adaptador real
// (internal/adapter/youtube): devuelve (externalID, externalURL, err) y puede
// devolver *youtube.RateLimitError. Los tests REALES del adaptador (OAuth
// refresh + upload resumable) viven en internal/adapter/youtube.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/adapter/youtube"
	"github.com/juankos0714/clipfactory/internal/db"
)

// fakePublisher implementa Publisher en memoria.
type fakePublisher struct {
	// failFor: videoPath → error a devolver
	failFor map[string]error
	// uploads registra las llamadas: videoPath, title, description, tags(,)
	uploads [][4]string
	// writeOutput: si true, el upload "sucede" y devuelve ID/URL falsos
	writeOutput bool
}

func (f *fakePublisher) UploadVideo(ctx context.Context, videoPath, title, description string, tags []string) (string, string, error) {
	if err, ok := f.failFor[videoPath]; ok {
		return "", "", err
	}
	f.uploads = append(f.uploads, [4]string{videoPath, title, description, strings.Join(tags, ",")})
	if f.writeOutput {
		return "yt_vid_123", "https://youtube.com/watch?v=yt_vid_123", nil
	}
	return "", "", errors.New("upload fallido simulado")
}

// rateLimitPublisher falla SIEMPRE con *youtube.RateLimitError.
type rateLimitPublisher struct{}

func (r *rateLimitPublisher) UploadVideo(ctx context.Context, videoPath, title, description string, tags []string) (string, string, error) {
	return "", "", &youtube.RateLimitError{Detail: "quotaExceeded"}
}

// setupPublishWorker crea worker + cadena completa source → source_clip → video
// → clip 'completed' con archivo y thumbnail, + publication 'pending'.
// Devuelve el worker, la publicación y el clip.
func setupPublishWorker(t *testing.T, pub Publisher) (*Worker, *db.Publication, *db.Clip) {
	t.Helper()
	conn := openTestDB(t)
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		Publisher:         pub,
		DataDir:           t.TempDir(),
		WorkerID:          "pub-worker",
		MaxConcurrentJobs: 1,
		PollInterval:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	pubs := setupPublishChains(t, conn, w.cfg.DataDir, 1)
	clip, err := db.GetClipByID(w.db, pubs[0].ClipID)
	if err != nil || clip == nil {
		t.Fatalf("get clip: %v %v", err, clip)
	}
	return w, pubs[0], clip
}

// setupPublishChains inserta n cadenas completas source → source_clip → video
// → clip 'completed' (+ archivos reales en disco) → publication 'pending'
// sobre una MISMA DB. Sirve tanto para tests de un solo job como para tests
// del loop del worker con varios jobs concurrentes.
func setupPublishChains(t *testing.T, conn *sql.DB, dataDir string, n int) []*db.Publication {
	t.Helper()

	var sourceID int64
	if err := conn.QueryRow("SELECT id FROM sources WHERE channel_id = '12345'").Scan(&sourceID); err != nil {
		t.Fatalf("get source: %v", err)
	}

	var pubs []*db.Publication
	for i := 0; i < n; i++ {
		clipID := fmt.Sprintf("ClipIDPub%d", i)
		// 'downloaded' es el único estado de source_clips compatible con "ya hay
		// video" (el schema v2 restringe status a detected/downloaded/skipped/error).
		sc := &db.SourceClip{Platform: "twitch", PlatformClipID: clipID, SourceID: sourceID, Status: "downloaded"}
		if err := db.UpsertSourceClip(conn, sc); err != nil {
			t.Fatalf("upsert source clip %d: %v", i, err)
		}

		srcPath := filepath.Join(dataDir, "incoming", clipID+".mp4")
		if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
			t.Fatalf("mkdir incoming: %v", err)
		}
		if err := os.WriteFile(srcPath, []byte("raw-video-bytes"), 0o600); err != nil {
			t.Fatalf("write incoming file: %v", err)
		}

		v := &db.Video{SourceClipID: sc.ID, Filepath: srcPath, Status: "incoming"}
		if err := db.InsertVideo(conn, v); err != nil {
			t.Fatalf("insert video %d: %v", i, err)
		}

		// clip completado: archivo real + thumbnail registrada en DB
		clipPath := filepath.Join(dataDir, "completed", clipID+".mp4")
		if err := os.MkdirAll(filepath.Dir(clipPath), 0o755); err != nil {
			t.Fatalf("mkdir completed: %v", err)
		}
		if err := os.WriteFile(clipPath, []byte("processed-video"), 0o600); err != nil {
			t.Fatalf("write completed file: %v", err)
		}
		thumbPath := clipPath + ".jpg"
		if err := os.WriteFile(thumbPath, []byte("jpeg-bytes"), 0o600); err != nil {
			t.Fatalf("write thumbnail: %v", err)
		}

		clip := &db.Clip{
			VideoID:       v.ID,
			StartTimeSec:  0,
			EndTimeSec:    30,
			Filepath:      clipPath,
			ThumbnailPath: thumbPath,
			DurationSec:   30,
			Width:         1080,
			Height:        1920,
			Status:        "completed",
		}
		if err := db.InsertClip(conn, clip); err != nil {
			t.Fatalf("insert clip %d: %v", i, err)
		}

		p := &db.Publication{ClipID: clip.ID, Platform: "youtube", Status: "pending"}
		if err := db.InsertPublication(conn, p); err != nil {
			t.Fatalf("insert publication %d: %v", i, err)
		}
		pubs = append(pubs, p)
	}
	return pubs
}

func publishJobFor(pubID int64) *db.Job {
	return &db.Job{Type: "publish", ReferenceID: pubID, ReferenceType: "publications"}
}

// enqueueAndLock encola un job y lo marca como running (locked), replicando lo
// que hace el loop del worker antes de ejecutar el handler.
func enqueueAndLock(t *testing.T, w *Worker, job *db.Job) {
	t.Helper()
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(w.db, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}
}

func TestExecutePublishSuccess(t *testing.T) {
	pub := &fakePublisher{writeOutput: true}
	w, publication, clip := setupPublishWorker(t, pub)

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	if err := w.executeJob(context.Background(), *job); err != nil {
		t.Fatalf("executeJob: %v", err)
	}

	// 1. el publisher fue invocado exactamente una vez con el archivo del clip
	if len(pub.uploads) != 1 || pub.uploads[0][0] != clip.Filepath {
		t.Errorf("expected 1 upload of %s, got %+v", clip.Filepath, pub.uploads)
	}

	// 2. la publicación quedó published con external_id/url y published_at
	got, err := db.GetPublicationByID(w.db, publication.ID)
	if err != nil || got == nil {
		t.Fatalf("get publication: %v %v", err, got)
	}
	if got.Status != "published" {
		t.Errorf("expected status 'published', got '%s'", got.Status)
	}
	if got.ExternalID != "yt_vid_123" || got.ExternalURL != "https://youtube.com/watch?v=yt_vid_123" {
		t.Errorf("unexpected external id/url: %s %s", got.ExternalID, got.ExternalURL)
	}
	if got.PublishedAt == nil {
		t.Errorf("expected published_at set")
	}
	if got.ErrorMessage != "" {
		t.Errorf("expected empty error_message, got '%s'", got.ErrorMessage)
	}

	// 3. el job quedó 'done'
	var status string
	if err := w.db.QueryRow("SELECT status FROM jobs WHERE id = ?", job.ID).Scan(&status); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != "done" {
		t.Errorf("expected job 'done', got '%s'", status)
	}
}

func TestExecutePublishRateLimit(t *testing.T) {
	// el publisher falla SIEMPRE con RateLimitError (cuota agotada)
	rl := &rateLimitPublisher{}
	w, publication, _ := setupPublishWorker(t, rl)

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	// el rate limit NO se propaga como error del job: la publicación se
	// reprograma y el reintento natural lo hace GetPendingPublications
	if err := w.executeJob(context.Background(), *job); err != nil {
		t.Fatalf("executeJob con rate limit NO debe devolver error: %v", err)
	}

	got, err := db.GetPublicationByID(w.db, publication.ID)
	if err != nil || got == nil {
		t.Fatalf("get publication: %v %v", err, got)
	}
	if got.Status != "waiting_rate_limit" {
		t.Errorf("expected status 'waiting_rate_limit', got '%s'", got.Status)
	}
	// la cuota no es un fallo del pipeline: no incrementa attempts
	if got.Attempts != 0 {
		t.Errorf("rate limit no debe incrementar attempts, got %d", got.Attempts)
	}
	if got.NextRetryAt == nil {
		t.Fatalf("expected next_retry_at set (reset de cuota)")
	}
	// el reintento debe ser a ~24h (reset diario de cuota)
	until := time.Until(*got.NextRetryAt)
	if until < 23*time.Hour || until > 25*time.Hour {
		t.Errorf("expected next_retry_at ~24h out, got %v", until)
	}
	if !strings.Contains(got.ErrorMessage, "cuota") {
		t.Errorf("expected error_message mentioning quota, got '%s'", got.ErrorMessage)
	}

	// el job queda 'done': quién reintenta es GetPendingPublications
	var status string
	if err := w.db.QueryRow("SELECT status FROM jobs WHERE id = ?", job.ID).Scan(&status); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != "done" {
		t.Errorf("expected job 'done' (reintento via GetPendingPublications), got '%s'", status)
	}
}

func TestExecutePublishErrorSchedulesBackoff(t *testing.T) {
	pub := &fakePublisher{writeOutput: false} // falla con error genérico
	w, publication, _ := setupPublishWorker(t, pub)

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	// executePublish registra el backoff en la DB y devuelve error, que
	// executeJob convierte en job 'error' (visible en métricas/logs)
	if err := w.executeJob(context.Background(), *job); err == nil {
		t.Errorf("expected error from executeJob on generic failure")
	}

	got, err := db.GetPublicationByID(w.db, publication.ID)
	if err != nil || got == nil {
		t.Fatalf("get publication: %v %v", err, got)
	}
	if got.Status != "error" {
		t.Errorf("expected status 'error', got '%s'", got.Status)
	}
	if got.Attempts != 1 {
		t.Errorf("expected attempts=1, got %d", got.Attempts)
	}
	if got.NextRetryAt == nil {
		t.Fatalf("expected next_retry_at set (backoff exponencial)")
	}
	// attempts venía en 0 → delay = 2^0 = 1h
	until := time.Until(*got.NextRetryAt)
	if until < 55*time.Minute || until > 65*time.Minute {
		t.Errorf("expected next_retry_at ~1h out (2^0 backoff), got %v", until)
	}
	if !strings.Contains(got.ErrorMessage, "upload fallido simulado") {
		t.Errorf("expected error_message with the cause, got '%s'", got.ErrorMessage)
	}
}

func TestExecutePublishNoPublisher(t *testing.T) {
	// sin Publisher: el job debe fallar con mensaje claro (no panic)
	w, publication, _ := setupPublishWorker(t, nil)

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	err := w.executeJob(context.Background(), *job)
	if err == nil {
		t.Fatalf("expected error when no publisher configured")
	}
	if !strings.Contains(err.Error(), "no publisher configurado") {
		t.Errorf("expected clear error message, got: %v", err)
	}
}

func TestExecutePublishAlreadyPublishedNoOp(t *testing.T) {
	pub := &fakePublisher{writeOutput: true}
	w, publication, _ := setupPublishWorker(t, pub)

	// marcar como ya publicada (simula crash entre upload y update de DB,
	// o una re-entrega del job tras éxito)
	if err := db.UpdatePublicationStatus(w.db, publication.ID, "published", "ext_old", "https://old.url", "", nil, nil, false); err != nil {
		t.Fatalf("mark published: %v", err)
	}

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	if err := w.executeJob(context.Background(), *job); err != nil {
		t.Fatalf("executeJob: %v", err)
	}

	// NO se subió de nuevo
	if len(pub.uploads) != 0 {
		t.Errorf("expected no uploads for already-published, got %+v", pub.uploads)
	}
}

func TestExecutePublishMissingPublication(t *testing.T) {
	w, _, _ := setupPublishWorker(t, &fakePublisher{writeOutput: true})

	// job apuntando a una publication inexistente
	job := &db.Job{Type: "publish", ReferenceID: 99999, ReferenceType: "publications"}
	enqueueAndLock(t, w, job)

	err := w.executeJob(context.Background(), *job)
	if err == nil {
		t.Fatalf("expected error for missing publication")
	}
	if !strings.Contains(err.Error(), "no existe") {
		t.Errorf("expected 'no existe' in error, got: %v", err)
	}
}

func TestExecutePublishMissingClipFile(t *testing.T) {
	pub := &fakePublisher{writeOutput: true}
	w, publication, clip := setupPublishWorker(t, pub)

	// eliminar el archivo del clip: el job debe fallar ANTES de llamar al publisher
	if err := os.Remove(clip.Filepath); err != nil {
		t.Fatalf("remove clip file: %v", err)
	}

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	if err := w.executeJob(context.Background(), *job); err == nil {
		t.Fatalf("expected error when clip file is missing")
	}
	if len(pub.uploads) != 0 {
		t.Errorf("publisher must not be called when file is missing, got %+v", pub.uploads)
	}
}

// TestPendingPublicationsRespectsNextRetry verifica el ciclo de reintentos
// NATURAL de las publicaciones: una publicación en waiting_rate_limit (o error)
// con next_retry_at futuro NO aparece en GetPendingPublications; cuando el
// next_retry_at vence, vuelve a aparecer para reintentarse.
func TestPendingPublicationsRespectsNextRetry(t *testing.T) {
	w, publication, _ := setupPublishWorker(t, &fakePublisher{writeOutput: true})

	// simular cuota agotada con reset mañana
	tomorrow := time.Now().UTC().Add(24 * time.Hour)
	if err := db.UpdatePublicationStatus(w.db, publication.ID, "waiting_rate_limit", "", "", "cuota agotada", nil, &tomorrow, false); err != nil {
		t.Fatalf("update: %v", err)
	}

	pending, err := db.GetPendingPublications(w.db, "youtube", 10)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	for _, g := range pending {
		if g.ID == publication.ID {
			t.Errorf("publication con next_retry_at futuro no debe aparecer en pending")
		}
	}

	// cuando el next_retry_at vence, SÍ vuelve a aparecer
	yesterday := time.Now().UTC().Add(-25 * time.Hour)
	if err := db.UpdatePublicationStatus(w.db, publication.ID, "waiting_rate_limit", "", "", "cuota agotada", nil, &yesterday, false); err != nil {
		t.Fatalf("update: %v", err)
	}
	pending, err = db.GetPendingPublications(w.db, "youtube", 10)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	found := false
	for _, g := range pending {
		if g.ID == publication.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("publication con next_retry_at vencido debe aparecer en pending")
	}
}
