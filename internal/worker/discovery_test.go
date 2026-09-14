package worker

// Tests del job 'discovery': parseo de clips nuevos, idempotencia del upsert,
// encolado de downloads solo para clips nuevos, y casos de error.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/adapter/twitch"
	"github.com/juankos0714/clipfactory/internal/db"
)

// fakeDiscoverer implementa Discoverer en memoria.
type fakeDiscoverer struct {
	// clips por channelID: lo que devuelve cada llamada
	clips    map[string][]twitch.ClipInfo
	failWith error
	// registro de llamadas para verificar parámetros
	calls []listCall
}

type listCall struct {
	channelID string
	after     time.Time
	maxPages  int
}

func (f *fakeDiscoverer) ListClips(ctx context.Context, channelID string, afterTimestamp time.Time, maxPages ...int) ([]twitch.ClipInfo, error) {
	f.calls = append(f.calls, listCall{channelID: channelID, after: afterTimestamp, maxPages: firstOrZero(maxPages)})
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.clips[channelID], nil
}

func firstOrZero(xs []int) int {
	if len(xs) > 0 {
		return xs[0]
	}
	return 0
}

// clipInfo helper: arma un ClipInfo mínimo.
func clipInfo(id string, dur float64, createdAt time.Time) twitch.ClipInfo {
	return twitch.ClipInfo{
		ID:          id,
		Title:       "clip " + id,
		DurationSec: dur,
		CreatedAt:   createdAt,
		ChannelID:   "12345",
		ChannelName: "test_channel",
	}
}

// setupDiscoveryWorker crea worker + source activo listo para discovery.
func setupDiscoveryWorker(t *testing.T, disc Discoverer) (*Worker, *db.Source) {
	t.Helper()
	conn := openTestDB(t)
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		Discoverer:        disc,
		DataDir:           t.TempDir(),
		WorkerID:          "disc-worker",
		MaxConcurrentJobs: 2,
		PollInterval:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	// openTestDB ya insertó un source 'twitch/12345': rescatarlo
	var sourceID int64
	if err := conn.QueryRow("SELECT id FROM sources WHERE channel_id = '12345'").Scan(&sourceID); err != nil {
		t.Fatalf("get seeded source: %v", err)
	}
	source, err := db.GetSourceByID(conn, sourceID)
	if err != nil || source == nil {
		t.Fatalf("get source: %v %v", err, source)
	}
	return w, source
}

func discoveryJobFor(sourceID int64) *db.Job {
	return &db.Job{Type: "discovery", ReferenceID: sourceID, ReferenceType: "sources"}
}

func TestExecuteDiscoveryInsertsNewClipsAndEnqueuesDownloads(t *testing.T) {
	now := time.Now().UTC()
	disc := &fakeDiscoverer{clips: map[string][]twitch.ClipInfo{
		"12345": {
			clipInfo("clipN1", 30.5, now.Add(-1*time.Hour)),
			clipInfo("clipN2", 15.0, now.Add(-2*time.Hour)),
		},
	}}
	w, source := setupDiscoveryWorker(t, disc)

	job := discoveryJobFor(source.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := w.executeDiscovery(context.Background(), *job); err != nil {
		t.Fatalf("executeDiscovery: %v", err)
	}

	// 1. los dos clips existen en source_clips como 'detected'
	for _, id := range []string{"clipN1", "clipN2"} {
		var status, title string
		var dur float64
		if err := w.db.QueryRow(
			"SELECT status, title, duration_seconds FROM source_clips WHERE platform_clip_id = ?", id,
		).Scan(&status, &title, &dur); err != nil {
			t.Fatalf("source_clip %s: %v", id, err)
		}
		if status != "detected" {
			t.Errorf("clip %s: expected status 'detected', got '%s'", id, status)
		}
		if title != "clip "+id {
			t.Errorf("clip %s: expected title 'clip %s', got '%s'", id, id, title)
		}
		if dur != map[string]float64{"clipN1": 30.5, "clipN2": 15.0}[id] {
			t.Errorf("clip %s: unexpected duration %f", id, dur)
		}
	}

	// 2. se encolaron 2 jobs download apuntando a los source_clips nuevos
	// (el job discovery en sí sigue 'queued': GetPendingJobs también lo devuelve)
	jobs, err := db.GetPendingJobs(w.db, 10)
	if err != nil {
		t.Fatalf("get pending jobs: %v", err)
	}
	var downloadJobs []db.Job
	for _, j := range jobs {
		if j.Type == "download" {
			downloadJobs = append(downloadJobs, j)
			if j.ReferenceType != "source_clips" {
				t.Errorf("download job con reference_type inesperado: %+v", j)
			}
		}
	}
	if len(downloadJobs) != 2 {
		t.Fatalf("expected 2 download jobs, got %d: %+v", len(downloadJobs), jobs)
	}

	// 3. last_checked_at quedó actualizado
	updated, _ := db.GetSourceByID(w.db, source.ID)
	if updated.LastCheckedAt == nil {
		t.Error("expected last_checked_at to be set after discovery")
	}
}

func TestExecuteDiscoveryIsIdempotent(t *testing.T) {
	now := time.Now().UTC()
	disc := &fakeDiscoverer{clips: map[string][]twitch.ClipInfo{
		"12345": {clipInfo("clipDup", 20, now.Add(-1*time.Hour))},
	}}
	w, source := setupDiscoveryWorker(t, disc)

	// primera pasada
	job := discoveryJobFor(source.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := w.executeDiscovery(context.Background(), *job); err != nil {
		t.Fatalf("first discovery: %v", err)
	}

	// marcar el clip como ya descargado (como si el pipeline hubiera avanzado)
	var scID int64
	w.db.QueryRow("SELECT id FROM source_clips WHERE platform_clip_id = 'clipDup'").Scan(&scID)
	if err := db.UpdateSourceClipStatus(w.db, scID, "downloaded", ""); err != nil {
		t.Fatalf("set downloaded: %v", err)
	}

	// segunda pasada con el MISMO clip (Helix puede devolverlo de nuevo)
	job2 := discoveryJobFor(source.ID)
	if err := db.EnqueueJob(w.db, job2); err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}
	if err := w.executeDiscovery(context.Background(), *job2); err != nil {
		t.Fatalf("second discovery: %v", err)
	}

	// no debe duplicar el source_clip
	var count int
	w.db.QueryRow("SELECT COUNT(*) FROM source_clips WHERE platform_clip_id = 'clipDup'").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 source_clip, got %d", count)
	}

	// el status NO debe haber vuelto a 'detected' (upsert preserva el estado avanzado)
	var status string
	w.db.QueryRow("SELECT status FROM source_clips WHERE id = ?", scID).Scan(&status)
	if status != "downloaded" {
		t.Errorf("expected status preserved 'downloaded', got '%s'", status)
	}

	// solo 1 job download en total (de la primera pasada)
	var dlCount int
	w.db.QueryRow("SELECT COUNT(*) FROM jobs WHERE type = 'download'").Scan(&dlCount)
	if dlCount != 1 {
		t.Errorf("expected 1 download job total, got %d", dlCount)
	}
}

func TestExecuteDiscoveryUsesLastCheckedAsAfter(t *testing.T) {
	disc := &fakeDiscoverer{clips: map[string][]twitch.ClipInfo{"12345": nil}}
	w, source := setupDiscoveryWorker(t, disc)

	// fijar last_checked_at a un timestamp conocido
	mark := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	if err := db.UpdateSourceLastChecked(w.db, source.ID, mark); err != nil {
		t.Fatalf("set last_checked: %v", err)
	}

	job := discoveryJobFor(source.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := w.executeDiscovery(context.Background(), *job); err != nil {
		t.Fatalf("executeDiscovery: %v", err)
	}

	if len(disc.calls) != 1 {
		t.Fatalf("expected 1 ListClips call, got %d", len(disc.calls))
	}
	call := disc.calls[0]
	if call.channelID != source.ChannelID {
		t.Errorf("expected channelID %q, got %q", source.ChannelID, call.channelID)
	}
	if !call.after.Equal(mark) {
		t.Errorf("expected after=%v (last_checked_at), got %v", mark, call.after)
	}
	if call.maxPages != 1 {
		t.Errorf("expected maxPages=1, got %d", call.maxPages)
	}
}

func TestExecuteDiscoveryFirstRunNoAfterFilter(t *testing.T) {
	disc := &fakeDiscoverer{clips: map[string][]twitch.ClipInfo{"12345": nil}}
	w, source := setupDiscoveryWorker(t, disc)

	// sin last_checked_at (primera pasada): after debe ser zero time
	job := discoveryJobFor(source.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := w.executeDiscovery(context.Background(), *job); err != nil {
		t.Fatalf("executeDiscovery: %v", err)
	}

	if len(disc.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(disc.calls))
	}
	if !disc.calls[0].after.IsZero() {
		t.Errorf("expected zero after on first run, got %v", disc.calls[0].after)
	}
}

func TestExecuteDiscoveryInactiveSourceSkipped(t *testing.T) {
	disc := &fakeDiscoverer{clips: map[string][]twitch.ClipInfo{"12345": nil}}
	w, source := setupDiscoveryWorker(t, disc)

	// desactivar el canal
	if _, err := w.db.Exec("UPDATE sources SET active = 0 WHERE id = ?", source.ID); err != nil {
		t.Fatalf("deactivate source: %v", err)
	}

	job := discoveryJobFor(source.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := w.executeDiscovery(context.Background(), *job); err != nil {
		t.Fatalf("executeDiscovery: %v", err)
	}

	if len(disc.calls) != 0 {
		t.Errorf("expected no ListClips calls for inactive source, got %d", len(disc.calls))
	}
}

func TestExecuteDiscoveryAPIError(t *testing.T) {
	disc := &fakeDiscoverer{failWith: errors.New("helix: 429 rate limited")}
	w, source := setupDiscoveryWorker(t, disc)

	job := discoveryJobFor(source.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	err := w.executeDiscovery(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error from failing discoverer, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("expected underlying error in chain, got: %v", err)
	}

	// last_checked_at NO debe avanzar cuando la consulta falla
	updated, _ := db.GetSourceByID(w.db, source.ID)
	if updated.LastCheckedAt != nil {
		t.Error("expected last_checked_at to remain unset after failed discovery")
	}
}

func TestExecuteDiscoveryNoDiscovererConfigured(t *testing.T) {
	conn := openTestDB(t)
	w, err := NewWorker(WorkerConfig{
		DB: conn, Discoverer: nil,
		WorkerID: "no-disc", MaxConcurrentJobs: 1, PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	job := &db.Job{Type: "discovery", ReferenceID: 1, ReferenceType: "sources"}
	err = w.executeDiscovery(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error when no discoverer configured, got nil")
	}
	if !strings.Contains(err.Error(), "no discoverer") {
		t.Errorf("expected 'no discoverer' in error, got: %v", err)
	}
}

func TestExecuteDiscoveryWrongReferenceType(t *testing.T) {
	disc := &fakeDiscoverer{}
	w, _ := setupDiscoveryWorker(t, disc)

	job := &db.Job{Type: "discovery", ReferenceID: 1, ReferenceType: "videos"}
	err := w.executeDiscovery(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error for wrong reference_type, got nil")
	}
	if !strings.Contains(err.Error(), "reference_type") {
		t.Errorf("expected 'reference_type' in error, got: %v", err)
	}
}

func TestExecuteDiscoveryMissingSource(t *testing.T) {
	disc := &fakeDiscoverer{}
	w, _ := setupDiscoveryWorker(t, disc)

	job := discoveryJobFor(987654)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	err := w.executeDiscovery(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error for nonexistent source, got nil")
	}
	if !strings.Contains(err.Error(), "no existe") {
		t.Errorf("expected 'no existe' in error, got: %v", err)
	}
}

func TestExecuteJobDiscoveryErrorFailsJob(t *testing.T) {
	disc := &fakeDiscoverer{failWith: fmt.Errorf("api down")}
	w, source := setupDiscoveryWorker(t, disc)

	job := discoveryJobFor(source.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(w.db, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := w.executeJob(context.Background(), *job); err == nil {
		t.Fatal("expected executeJob to propagate error, got nil")
	}

	var status, errMsg string
	if err := w.db.QueryRow("SELECT status, error_message FROM jobs WHERE id = ?", job.ID).Scan(&status, &errMsg); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != "error" {
		t.Errorf("expected job 'error', got '%s'", status)
	}
	if !strings.Contains(errMsg, "api down") {
		t.Errorf("expected cause in error_message, got '%s'", errMsg)
	}
}

func TestExecuteJobDiscoverySuccessCompletesJob(t *testing.T) {
	disc := &fakeDiscoverer{clips: map[string][]twitch.ClipInfo{"12345": nil}}
	w, source := setupDiscoveryWorker(t, disc)

	job := discoveryJobFor(source.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(w.db, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := w.executeJob(context.Background(), *job); err != nil {
		t.Fatalf("executeJob: %v", err)
	}

	var status string
	if err := w.db.QueryRow("SELECT status FROM jobs WHERE id = ?", job.ID).Scan(&status); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != "done" {
		t.Errorf("expected job 'done', got '%s'", status)
	}
}

func TestDiscoveryFullPipelineViaWorkerLoop(t *testing.T) {
	now := time.Now().UTC()
	// discovery que devuelve 1 clip; el downloader fake lo "descarga"
	dl := &fakeDownloader{writeFile: true}
	disc := &fakeDiscoverer{clips: map[string][]twitch.ClipInfo{
		"12345": {clipInfo("clipE2E", 25, now.Add(-30*time.Minute))},
	}}
	conn := openTestDB(t)
	w, err := NewWorker(WorkerConfig{
		DB: conn, Discoverer: disc, Downloader: dl,
		DataDir: t.TempDir(), WorkerID: "e2e",
		MaxConcurrentJobs: 2, PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	var sourceID int64
	conn.QueryRow("SELECT id FROM sources WHERE channel_id = '12345'").Scan(&sourceID)

	// encolar discovery y arrancar el worker: discovery → download deben completarse
	// y quedar un job process encolado por el download
	if err := db.EnqueueJob(conn, discoveryJobFor(sourceID)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	var downloadsDone, processCreated int
	for time.Now().Before(deadline) {
		conn.QueryRow("SELECT COUNT(*) FROM jobs WHERE type='download' AND status='done'").Scan(&downloadsDone)
		conn.QueryRow("SELECT COUNT(*) FROM jobs WHERE type='process'").Scan(&processCreated)
		if downloadsDone >= 1 && processCreated >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	w.Stop()

	if downloadsDone < 1 {
		t.Errorf("expected 1 download done, got %d", downloadsDone)
	}
	if processCreated < 1 {
		t.Errorf("expected 1 process job created, got %d", processCreated)
	}
	// y el clip descargado por el fake
	if len(dl.downloads) != 1 || dl.downloads[0] != "clipE2E" {
		t.Errorf("expected downloader called with clipE2E, got %v", dl.downloads)
	}
}
