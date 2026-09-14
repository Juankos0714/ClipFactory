package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/db"
)

// fakeDownloader implementa Downloader en memoria para los tests del worker.
type fakeDownloader struct {
	failFor   map[string]error // clipID → error a devolver
	downloads []string         // clipIDs descargados (orden de llamada)
	// dir es un punto de control: si se setea, escribe un archivo en el destino
	// para simular la salida del CLI (necesario para el flujo completo)
	writeFile bool
}

func (f *fakeDownloader) DownloadClip(ctx context.Context, clipID string, destPath string) error {
	if err, ok := f.failFor[clipID]; ok {
		return err
	}
	f.downloads = append(f.downloads, clipID)
	if f.writeFile {
		// igual que el contrato de TwitchAdapter: el downloader crea el directorio
		// destino (MkdirAll) y escribe el archivo final
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destPath, []byte("fake"), 0o600)
	}
	return nil
}

// setupDownloadWorker crea worker + source_clip 'detected' listo para descargar.
// Devuelve el worker, la conn y el source_clip.
func setupDownloadWorker(t *testing.T, dl Downloader) (*Worker, *db.SourceClip) {
	t.Helper()
	conn := openTestDB(t)
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		Downloader:        dl,
		DataDir:           t.TempDir(),
		WorkerID:          "dl-worker",
		MaxConcurrentJobs: 1,
		PollInterval:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	sc := &db.SourceClip{Platform: "twitch", PlatformClipID: "ClipID42", Status: "detected"}
	if err := db.UpsertSourceClip(conn, sc); err != nil {
		t.Fatalf("upsert source clip: %v", err)
	}
	return w, sc
}

func TestExecuteDownloadSuccess(t *testing.T) {
	dl := &fakeDownloader{writeFile: true}
	w, sc := setupDownloadWorker(t, dl)

	// encolar y lockear el job de download
	job := &db.Job{Type: "download", ReferenceID: sc.ID, ReferenceType: "source_clips"}
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(w.db, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := w.executeDownload(context.Background(), *job); err != nil {
		t.Fatalf("executeDownload: %v", err)
	}

	// 1. el downloader fue invocado con el platform_clip_id
	if len(dl.downloads) != 1 || dl.downloads[0] != "ClipID42" {
		t.Errorf("expected downloader called with ClipID42, got %v", dl.downloads)
	}

	// 2. existe una fila en videos apuntando al incoming, con status incoming
	destPath := filepath.Join(w.cfg.DataDir, "incoming", "ClipID42.mp4")
	v, err := db.GetVideoByFilepath(w.db, destPath)
	if err != nil || v == nil {
		t.Fatalf("expected video row at %s, err=%v v=%v", destPath, err, v)
	}
	if v.Status != "incoming" {
		t.Errorf("expected video status 'incoming', got '%s'", v.Status)
	}
	if v.SourceClipID != sc.ID {
		t.Errorf("expected video.source_clip_id=%d, got %d", sc.ID, v.SourceClipID)
	}

	// 3. el archivo existe en disco
	if _, err := os.Stat(destPath); err != nil {
		t.Errorf("expected file on disk: %v", err)
	}

	// 4. source_clip pasó a downloaded
	gotSC, err := db.GetSourceClipByID(w.db, sc.ID)
	if err != nil || gotSC == nil {
		t.Fatalf("get source_clip: %v %v", err, gotSC)
	}
	if gotSC.Status != "downloaded" {
		t.Errorf("expected source_clip status 'downloaded', got '%s'", gotSC.Status)
	}

	// 5. se encoló un job 'process' apuntando al video
	jobs, err := db.GetPendingJobs(w.db, 10)
	if err != nil {
		t.Fatalf("get pending jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Type != "process" || jobs[0].ReferenceID != v.ID || jobs[0].ReferenceType != "videos" {
		t.Errorf("expected one process job for video %d, got %+v", v.ID, jobs)
	}
}

func TestExecuteDownloadIdempotentOnRerun(t *testing.T) {
	dl := &fakeDownloader{writeFile: true}
	w, sc := setupDownloadWorker(t, dl)

	job := &db.Job{Type: "download", ReferenceID: sc.ID, ReferenceType: "source_clips"}
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// primera ejecución
	if err := w.executeDownload(context.Background(), *job); err != nil {
		t.Fatalf("first executeDownload: %v", err)
	}

	// segunda ejecución del mismo job (simula re-encolado tras crash):
	// NO debe volver a llamar al downloader ni crear otra fila en videos
	if err := w.executeDownload(context.Background(), *job); err != nil {
		t.Fatalf("second executeDownload: %v", err)
	}
	if len(dl.downloads) != 1 {
		t.Errorf("expected downloader called once, got %d times", len(dl.downloads))
	}

	var videoCount int
	if err := w.db.QueryRow("SELECT COUNT(*) FROM videos").Scan(&videoCount); err != nil {
		t.Fatalf("count videos: %v", err)
	}
	if videoCount != 1 {
		t.Errorf("expected 1 video row, got %d", videoCount)
	}

	// y el source_clip ya marcado downloaded es no-op directo
	scAfter, _ := db.GetSourceClipByID(w.db, sc.ID)
	if scAfter.Status != "downloaded" {
		t.Errorf("expected source_clip 'downloaded', got '%s'", scAfter.Status)
	}
}

func TestExecuteDownloadAlreadyDownloadedIsNoOp(t *testing.T) {
	dl := &fakeDownloader{writeFile: true}
	w, sc := setupDownloadWorker(t, dl)

	// marcar el clip como ya descargado antes del job
	if err := db.UpdateSourceClipStatus(w.db, sc.ID, "downloaded", ""); err != nil {
		t.Fatalf("pre-set status: %v", err)
	}

	job := &db.Job{Type: "download", ReferenceID: sc.ID, ReferenceType: "source_clips"}
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := w.executeDownload(context.Background(), *job); err != nil {
		t.Fatalf("executeDownload: %v", err)
	}
	if len(dl.downloads) != 0 {
		t.Errorf("expected no downloads for already-downloaded clip, got %v", dl.downloads)
	}
}

func TestExecuteDownloadFailureMarksError(t *testing.T) {
	dlErr := errors.New("fake: network down")
	dl := &fakeDownloader{failFor: map[string]error{"badclip": dlErr}}
	w, sc := setupDownloadWorker(t, dl)
	sc.PlatformClipID = "badclip"
	// recrear el source_clip con el ID que falla
	sc2 := &db.SourceClip{Platform: "twitch", PlatformClipID: "badclip", SourceID: sc.SourceID, Status: "detected"}
	if err := db.UpsertSourceClip(w.db, sc2); err != nil {
		t.Fatalf("upsert badclip: %v", err)
	}

	job := &db.Job{Type: "download", ReferenceID: sc2.ID, ReferenceType: "source_clips"}
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	err := w.executeDownload(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error from failing download, got nil")
	}
	if !strings.Contains(err.Error(), "network down") {
		t.Errorf("expected underlying error in chain, got: %v", err)
	}

	// source_clip debe quedar en error con el mensaje
	got, _ := db.GetSourceClipByID(w.db, sc2.ID)
	if got.Status != "error" {
		t.Errorf("expected source_clip 'error', got '%s'", got.Status)
	}
	if !strings.Contains(got.ErrorMessage, "network down") {
		t.Errorf("expected error_message with cause, got '%s'", got.ErrorMessage)
	}

	// no debe haber video ni job process encolado
	var videoCount int
	w.db.QueryRow("SELECT COUNT(*) FROM videos").Scan(&videoCount)
	if videoCount != 0 {
		t.Errorf("expected 0 videos after failed download, got %d", videoCount)
	}
	jobs, _ := db.GetPendingJobs(w.db, 10)
	for _, j := range jobs {
		if j.Type == "process" {
			t.Errorf("unexpected process job enqueued: %+v", j)
		}
	}
}

func TestExecuteDownloadMissingSourceClip(t *testing.T) {
	dl := &fakeDownloader{}
	w, _ := setupDownloadWorker(t, dl)

	job := &db.Job{Type: "download", ReferenceID: 987654, ReferenceType: "source_clips"}
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	err := w.executeDownload(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error for nonexistent source_clip, got nil")
	}
	if !strings.Contains(err.Error(), "no existe") {
		t.Errorf("expected 'no existe' in error, got: %v", err)
	}
}

func TestExecuteDownloadWrongReferenceType(t *testing.T) {
	dl := &fakeDownloader{}
	w, _ := setupDownloadWorker(t, dl)

	job := &db.Job{Type: "download", ReferenceID: 1, ReferenceType: "videos"}
	err := w.executeDownload(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error for wrong reference_type, got nil")
	}
	if !strings.Contains(err.Error(), "reference_type") {
		t.Errorf("expected 'reference_type' in error, got: %v", err)
	}
}

func TestExecuteDownloadNoDownloaderConfigured(t *testing.T) {
	conn := openTestDB(t)
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		Downloader:        nil, // sin downloader
		DataDir:           t.TempDir(),
		WorkerID:          "no-dl",
		MaxConcurrentJobs: 1,
		PollInterval:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	job := &db.Job{Type: "download", ReferenceID: 1, ReferenceType: "source_clips"}
	err = w.executeDownload(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error when no downloader is configured, got nil")
	}
	if !strings.Contains(err.Error(), "no downloader") {
		t.Errorf("expected 'no downloader' in error, got: %v", err)
	}
}

func TestExecuteJobDownloadErrorFailsJob(t *testing.T) {
	dlErr := errors.New("fake: boom")
	dl := &fakeDownloader{failFor: map[string]error{"badclip": dlErr}}
	w, _ := setupDownloadWorker(t, dl)
	sc := &db.SourceClip{Platform: "twitch", PlatformClipID: "badclip", Status: "detected"}
	if err := db.UpsertSourceClip(w.db, sc); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	job := &db.Job{Type: "download", ReferenceID: sc.ID, ReferenceType: "source_clips"}
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(w.db, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	// executeJob (el wrapper completo) debe capturar el error y marcar el job
	if err := w.executeJob(context.Background(), *job); err == nil {
		t.Fatal("expected executeJob to propagate the error, got nil")
	}

	var status, errMsg string
	if err := w.db.QueryRow("SELECT status, error_message FROM jobs WHERE id = ?", job.ID).Scan(&status, &errMsg); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != "error" {
		t.Errorf("expected job status 'error', got '%s'", status)
	}
	if !strings.Contains(errMsg, "boom") {
		t.Errorf("expected job error_message with cause, got '%s'", errMsg)
	}
}

func TestExecuteJobDownloadSuccessCompletesJob(t *testing.T) {
	dl := &fakeDownloader{writeFile: true}
	w, sc := setupDownloadWorker(t, dl)

	job := &db.Job{Type: "download", ReferenceID: sc.ID, ReferenceType: "source_clips"}
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
		t.Errorf("expected job status 'done', got '%s'", status)
	}
}

func TestDownloadFullPipelineViaWorkerLoop(t *testing.T) {
	dl := &fakeDownloader{writeFile: true}
	w, sc := setupDownloadWorker(t, dl)

	// encolar el download y arrancar el worker real
	if err := db.EnqueueJob(w.db, &db.Job{Type: "download", ReferenceID: sc.ID, ReferenceType: "source_clips"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// el download debe completarse y dejar un job process encolado...
	// (el propio worker puede llegar a procesarlo antes de que miremos: contar en
	// cualquier estado, lo que verifica es que EXISTE y que nació del download)
	deadline := time.Now().Add(5 * time.Second)
	var processQueued bool
	for time.Now().Before(deadline) {
		var done int
		w.db.QueryRow("SELECT COUNT(*) FROM jobs WHERE type='download' AND status='done'").Scan(&done)
		if done == 1 {
			var proc int
			w.db.QueryRow("SELECT COUNT(*) FROM jobs WHERE type='process'").Scan(&proc)
			processQueued = proc == 1
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	w.Stop()

	if !processQueued {
		t.Fatal("expected download done + process job created after worker loop")
	}

	// ...y el clip quedar descargado con su video asociado
	gotSC, _ := db.GetSourceClipByID(w.db, sc.ID)
	if gotSC.Status != "downloaded" {
		t.Errorf("expected source_clip 'downloaded', got '%s'", gotSC.Status)
	}
	var videoCount int
	w.db.QueryRow("SELECT COUNT(*) FROM videos").Scan(&videoCount)
	if videoCount != 1 {
		t.Errorf("expected 1 video, got %d", videoCount)
	}
}

func TestDownloadCreatesIncomingDir(t *testing.T) {
	// DataDir que NO existe: el flujo debe crear data/incoming (el fake escribe
	// directo, así que verificamos que el worker crea el dir antes de llamar al
	// downloader... en realidad el worker no lo crea: lo hace el adaptador real.
	// Este test documenta el contrato: el Downloader es responsable del MkdirAll.)
	dataDir := filepath.Join(t.TempDir(), "no-existe")
	conn := openTestDB(t)
	w, err := NewWorker(WorkerConfig{
		DB: conn, Downloader: &fakeDownloader{writeFile: true},
		DataDir: dataDir, WorkerID: "mkdir-test",
		MaxConcurrentJobs: 1, PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	sc := &db.SourceClip{Platform: "twitch", PlatformClipID: "ClipID77", Status: "detected"}
	if err := db.UpsertSourceClip(w.db, sc); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	job := &db.Job{Type: "download", ReferenceID: sc.ID, ReferenceType: "source_clips"}
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// el fake NO crea directorios: fallará si el worker tampoco los crea.
	// El contrato real: el adaptador hace MkdirAll. Acá lo verificamos con un
	// downloader que sí lo hace (como TwitchAdapter).
	dlWithMkdir := &mkdirDownloader{inner: &fakeDownloader{writeFile: true}}
	w2, err := NewWorker(WorkerConfig{
		DB: conn, Downloader: dlWithMkdir,
		DataDir: dataDir, WorkerID: "mkdir-test2",
		MaxConcurrentJobs: 1, PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker 2: %v", err)
	}
	job2 := &db.Job{Type: "download", ReferenceID: sc.ID, ReferenceType: "source_clips"}
	if err := db.EnqueueJob(w2.db, job2); err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}
	if err := w2.executeDownload(context.Background(), *job2); err != nil {
		t.Fatalf("executeDownload with mkdir downloader: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "incoming", "ClipID77.mp4")); err != nil {
		t.Errorf("expected file in incoming dir: %v", err)
	}
	_ = fmt.Sprint() // mantener import fmt usado
	_ = w           // worker original sin uso adicional
}

// mkdirDownloader envuelve otro Downloader y crea el directorio destino antes de
// delegar — replica el comportamiento de TwitchAdapter.DownloadClip (MkdirAll).
type mkdirDownloader struct {
	inner *fakeDownloader
}

func (m *mkdirDownloader) DownloadClip(ctx context.Context, clipID string, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	return m.inner.DownloadClip(ctx, clipID, destPath)
}
