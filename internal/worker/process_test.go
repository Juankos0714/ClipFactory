package worker

// Tests del job 'process' (recorte a 1080x1920 con ffmpeg).
//
// Se usa un fakeProcessor que replica el contrato del real (crea dirs, escribe
// salida, puede fallar); los tests REALES de ffmpeg viven en adapter/ffmpeg.

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

// fakeProcessor implementa Processor en memoria.
type fakeProcessor struct {
	// failFor: srcPath → error a devolver
	failFor map[string]error
	// processed registra las llamadas (srcPath → destPath)
	processed [][2]string
	// writeOutput: si true, escribe un archivo en destPath (simula ffmpeg OK)
	writeOutput bool
}

func (f *fakeProcessor) ProcessVideo(ctx context.Context, srcPath, destPath string) error {
	if err, ok := f.failFor[srcPath]; ok {
		return err
	}
	f.processed = append(f.processed, [2]string{srcPath, destPath})
	if f.writeOutput {
		// igual que el contrato del real: el processor crea el dir destino
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destPath, []byte("processed-video"), 0o600)
	}
	return nil
}

// setupProcessWorker crea worker + video 'incoming' listo para procesar.
// Devuelve worker, video y la ruta de salida esperada.
func setupProcessWorker(t *testing.T, proc Processor) (*Worker, *db.Video, string) {
	t.Helper()
	conn := openTestDB(t)
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		Processor:         proc,
		DataDir:           t.TempDir(),
		WorkerID:          "proc-worker",
		MaxConcurrentJobs: 1,
		PollInterval:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	// cadena source → source_clip → video con un archivo "descargado" real
	var sourceID int64
	conn.QueryRow("SELECT id FROM sources WHERE channel_id = '12345'").Scan(&sourceID)
	sc := &db.SourceClip{Platform: "twitch", PlatformClipID: "ClipIDProc", SourceID: sourceID, Status: "downloaded"}
	if err := db.UpsertSourceClip(conn, sc); err != nil {
		t.Fatalf("upsert source clip: %v", err)
	}

	srcPath := filepath.Join(w.cfg.DataDir, "incoming", "ClipIDProc.mp4")
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir incoming: %v", err)
	}
	if err := os.WriteFile(srcPath, []byte("raw-video-bytes"), 0o600); err != nil {
		t.Fatalf("write incoming file: %v", err)
	}

	v := &db.Video{SourceClipID: sc.ID, Filepath: srcPath, Status: "incoming"}
	if err := db.InsertVideo(conn, v); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	destPath := filepath.Join(w.cfg.DataDir, "completed", "ClipIDProc.mp4")
	return w, v, destPath
}

func processJobFor(videoID int64) *db.Job {
	return &db.Job{Type: "process", ReferenceID: videoID, ReferenceType: "videos"}
}

func TestExecuteProcessSuccess(t *testing.T) {
	proc := &fakeProcessor{writeOutput: true}
	w, video, destPath := setupProcessWorker(t, proc)

	job := processJobFor(video.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := w.executeProcess(context.Background(), *job); err != nil {
		t.Fatalf("executeProcess: %v", err)
	}

	// 1. ffmpeg fue invocado con las rutas correctas
	if len(proc.processed) != 1 {
		t.Fatalf("expected 1 ProcessVideo call, got %d", len(proc.processed))
	}
	if proc.processed[0][0] != video.Filepath || proc.processed[0][1] != destPath {
		t.Errorf("expected %s → %s, got %s → %s",
			video.Filepath, destPath, proc.processed[0][0], proc.processed[0][1])
	}

	// 2. el clip existe en la DB con 1080x1920 y completed
	clip, err := db.GetClipByFilepath(w.db, destPath)
	if err != nil || clip == nil {
		t.Fatalf("expected clip row at %s, err=%v clip=%v", destPath, err, clip)
	}
	if clip.VideoID != video.ID {
		t.Errorf("expected clip.video_id=%d, got %d", video.ID, clip.VideoID)
	}
	if clip.Width != 1080 || clip.Height != 1920 {
		t.Errorf("expected 1080x1920, got %dx%d", clip.Width, clip.Height)
	}
	if clip.Status != "completed" {
		t.Errorf("expected clip 'completed', got '%s'", clip.Status)
	}

	// 3. el archivo de salida existe en disco
	if _, err := os.Stat(destPath); err != nil {
		t.Errorf("expected output file on disk: %v", err)
	}

	// 4. el video pasó a completed
	updated, _ := db.GetVideoByID(w.db, video.ID)
	if updated.Status != "completed" {
		t.Errorf("expected video 'completed', got '%s'", updated.Status)
	}

	// 5. se encoló un job thumbnail apuntando al clip
	jobs, err := db.GetPendingJobs(w.db, 10)
	if err != nil {
		t.Fatalf("get pending jobs: %v", err)
	}
	var thumbJobs []db.Job
	for _, j := range jobs {
		if j.Type == "thumbnail" {
			thumbJobs = append(thumbJobs, j)
			if j.ReferenceType != "clips" || j.ReferenceID != clip.ID {
				t.Errorf("unexpected thumbnail job: %+v", j)
			}
		}
	}
	if len(thumbJobs) != 1 {
		t.Errorf("expected 1 thumbnail job, got %d", len(thumbJobs))
	}
}

func TestExecuteProcessIdempotentOnRerun(t *testing.T) {
	proc := &fakeProcessor{writeOutput: true}
	w, video, _ := setupProcessWorker(t, proc)

	job := processJobFor(video.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := w.executeProcess(context.Background(), *job); err != nil {
		t.Fatalf("first process: %v", err)
	}

	// segunda pasada: clip ya existe → NO vuelve a llamar al processor
	if err := w.executeProcess(context.Background(), *job); err != nil {
		t.Fatalf("second process: %v", err)
	}
	if len(proc.processed) != 1 {
		t.Errorf("expected processor called once, got %d times", len(proc.processed))
	}

	var clipCount int
	w.db.QueryRow("SELECT COUNT(*) FROM clips").Scan(&clipCount)
	if clipCount != 1 {
		t.Errorf("expected 1 clip row, got %d", clipCount)
	}
}

func TestExecuteProcessAlreadyCompletedIsNoOp(t *testing.T) {
	proc := &fakeProcessor{}
	w, video, _ := setupProcessWorker(t, proc)

	if err := db.UpdateVideoStatus(w.db, video.ID, "completed", ""); err != nil {
		t.Fatalf("pre-set completed: %v", err)
	}

	job := processJobFor(video.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := w.executeProcess(context.Background(), *job); err != nil {
		t.Fatalf("executeProcess: %v", err)
	}
	if len(proc.processed) != 0 {
		t.Errorf("expected no processing for completed video, got %v", proc.processed)
	}
}

func TestExecuteProcessFailureMarksVideoFailed(t *testing.T) {
	procErr := errors.New("ffmpeg: salida corrupta")
	proc := &fakeProcessor{failFor: map[string]error{}}
	w, video, _ := setupProcessWorker(t, proc)
	proc.failFor[video.Filepath] = procErr

	job := processJobFor(video.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	err := w.executeProcess(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error from failing processor, got nil")
	}
	if !strings.Contains(err.Error(), "salida corrupta") {
		t.Errorf("expected underlying error in chain, got: %v", err)
	}

	// el video queda 'failed' con el motivo
	updated, _ := db.GetVideoByID(w.db, video.ID)
	if updated.Status != "failed" {
		t.Errorf("expected video 'failed', got '%s'", updated.Status)
	}
	if !strings.Contains(updated.ErrorMessage, "salida corrupta") {
		t.Errorf("expected cause in error_message, got '%s'", updated.ErrorMessage)
	}

	// no debe haber clip ni thumbnail job
	var clipCount int
	w.db.QueryRow("SELECT COUNT(*) FROM clips").Scan(&clipCount)
	if clipCount != 0 {
		t.Errorf("expected 0 clips after failure, got %d", clipCount)
	}
	jobs, _ := db.GetPendingJobs(w.db, 10)
	for _, j := range jobs {
		if j.Type == "thumbnail" {
			t.Errorf("unexpected thumbnail job after failure: %+v", j)
		}
	}
}

func TestExecuteProcessMissingVideo(t *testing.T) {
	proc := &fakeProcessor{}
	w, _, _ := setupProcessWorker(t, proc)

	job := processJobFor(987654)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	err := w.executeProcess(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error for nonexistent video, got nil")
	}
	if !strings.Contains(err.Error(), "no existe") {
		t.Errorf("expected 'no existe' in error, got: %v", err)
	}
}

func TestExecuteProcessWrongReferenceType(t *testing.T) {
	proc := &fakeProcessor{}
	w, _, _ := setupProcessWorker(t, proc)

	job := &db.Job{Type: "process", ReferenceID: 1, ReferenceType: "clips"}
	err := w.executeProcess(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error for wrong reference_type, got nil")
	}
	if !strings.Contains(err.Error(), "reference_type") {
		t.Errorf("expected 'reference_type' in error, got: %v", err)
	}
}

func TestExecuteProcessNoProcessorConfigured(t *testing.T) {
	conn := openTestDB(t)
	w, err := NewWorker(WorkerConfig{
		DB: conn, Processor: nil,
		WorkerID: "no-proc", MaxConcurrentJobs: 1, PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	job := processJobFor(1)
	err = w.executeProcess(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error when no processor configured, got nil")
	}
	if !strings.Contains(err.Error(), "no processor") {
		t.Errorf("expected 'no processor' in error, got: %v", err)
	}
}

func TestExecuteJobProcessSuccessCompletesJob(t *testing.T) {
	proc := &fakeProcessor{writeOutput: true}
	w, video, _ := setupProcessWorker(t, proc)

	job := processJobFor(video.ID)
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

func TestExecuteJobProcessErrorFailsJob(t *testing.T) {
	proc := &fakeProcessor{failFor: map[string]error{}}
	w, video, _ := setupProcessWorker(t, proc)
	proc.failFor[video.Filepath] = fmt.Errorf("ffmpeg broken")

	job := processJobFor(video.ID)
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
	if !strings.Contains(errMsg, "ffmpeg broken") {
		t.Errorf("expected cause in error_message, got '%s'", errMsg)
	}
}

func TestProcessFullPipelineViaWorkerLoop(t *testing.T) {
	proc := &fakeProcessor{writeOutput: true}
	w, video, _ := setupProcessWorker(t, proc)

	// encolar process y arrancar el worker: process debe completar y dejar
	// un job thumbnail encolado
	if err := db.EnqueueJob(w.db, processJobFor(video.ID)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	var thumbnailCreated int
	for time.Now().Before(deadline) {
		var done int
		w.db.QueryRow("SELECT COUNT(*) FROM jobs WHERE type='process' AND status IN ('done','error')").Scan(&done)
		if done >= 1 {
			w.db.QueryRow("SELECT COUNT(*) FROM jobs WHERE type='thumbnail'").Scan(&thumbnailCreated)
			if thumbnailCreated >= 1 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	w.Stop()

	if thumbnailCreated < 1 {
		t.Fatal("expected process done + thumbnail job created after worker loop")
	}

	updated, _ := db.GetVideoByID(w.db, video.ID)
	if updated.Status != "completed" {
		t.Errorf("expected video 'completed', got '%s'", updated.Status)
	}
}
