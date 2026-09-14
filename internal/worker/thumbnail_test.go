package worker

// Tests del job 'thumbnail' (extracción de frame con ffmpeg).
//
// fakeThumbnailer replica el contrato del real; los tests reales de ffmpeg
// viven en adapter/ffmpeg (thumbnail_test.go).

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

// fakeThumbnailer implementa Thumbnailer en memoria.
type fakeThumbnailer struct {
	failFor   map[string]error // videoPath → error a devolver
	generated [][2]string      // pares (videoPath, thumbPath)
	writeOut  bool             // si true, escribe el archivo (simula ffmpeg OK)
}

func (f *fakeThumbnailer) GenerateThumbnail(ctx context.Context, videoPath, thumbPath string, atSec float64) error {
	if err, ok := f.failFor[videoPath]; ok {
		return err
	}
	f.generated = append(f.generated, [2]string{videoPath, thumbPath})
	if f.writeOut {
		if err := os.MkdirAll(filepath.Dir(thumbPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(thumbPath, []byte("jpeg-bytes"), 0o600)
	}
	return nil
}

// setupThumbnailWorker crea worker + clip 'completed' (con su video) listos
// para generar thumbnail. Devuelve worker, clip y la ruta de thumbnail esperada.
func setupThumbnailWorker(t *testing.T, thumb Thumbnailer) (*Worker, *db.Clip, string) {
	t.Helper()
	conn := openTestDB(t)
	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		Thumbnailer:       thumb,
		DataDir:           t.TempDir(),
		WorkerID:          "thumb-worker",
		MaxConcurrentJobs: 1,
		PollInterval:      50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	// cadena source → source_clip → video → clip completado
	var sourceID int64
	conn.QueryRow("SELECT id FROM sources WHERE channel_id = '12345'").Scan(&sourceID)
	sc := &db.SourceClip{Platform: "twitch", PlatformClipID: "ClipIDThumb", SourceID: sourceID, Status: "downloaded"}
	if err := db.UpsertSourceClip(conn, sc); err != nil {
		t.Fatalf("upsert source clip: %v", err)
	}

	// el archivo del clip debe existir (el thumbnailer real lo lee)
	clipPath := filepath.Join(w.cfg.DataDir, "completed", "ClipIDThumb.mp4")
	if err := os.MkdirAll(filepath.Dir(clipPath), 0o755); err != nil {
		t.Fatalf("mkdir completed: %v", err)
	}
	if err := os.WriteFile(clipPath, []byte("processed-video"), 0o600); err != nil {
		t.Fatalf("write clip file: %v", err)
	}

	v := &db.Video{SourceClipID: sc.ID, Filepath: filepath.Join(w.cfg.DataDir, "incoming", "ClipIDThumb.mp4"), Status: "completed"}
	if err := db.InsertVideo(conn, v); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	c := &db.Clip{VideoID: v.ID, Filepath: clipPath, Width: 1080, Height: 1920, Status: "completed"}
	if err := db.InsertClip(conn, c); err != nil {
		t.Fatalf("insert clip: %v", err)
	}

	expectedThumb := filepath.Join(w.cfg.DataDir, "thumbnails", "ClipIDThumb.mp4.jpg")
	return w, c, expectedThumb
}

func thumbnailJobFor(clipID int64) *db.Job {
	return &db.Job{Type: "thumbnail", ReferenceID: clipID, ReferenceType: "clips"}
}

func TestExecuteThumbnailSuccess(t *testing.T) {
	thumb := &fakeThumbnailer{writeOut: true}
	w, clip, expectedThumb := setupThumbnailWorker(t, thumb)

	job := thumbnailJobFor(clip.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := w.executeThumbnail(context.Background(), *job); err != nil {
		t.Fatalf("executeThumbnail: %v", err)
	}

	// 1. el thumbnailer fue invocado con el clip y la ruta esperada
	if len(thumb.generated) != 1 {
		t.Fatalf("expected 1 GenerateThumbnail call, got %d", len(thumb.generated))
	}
	if thumb.generated[0][0] != clip.Filepath || thumb.generated[0][1] != expectedThumb {
		t.Errorf("expected %s → %s, got %s → %s",
			clip.Filepath, expectedThumb, thumb.generated[0][0], thumb.generated[0][1])
	}

	// 2. el archivo existe en disco
	if _, err := os.Stat(expectedThumb); err != nil {
		t.Errorf("expected thumbnail file on disk: %v", err)
	}

	// 3. clips.thumbnail_path quedó registrado
	updated, err := db.GetClipByID(w.db, clip.ID)
	if err != nil || updated == nil {
		t.Fatalf("get clip: %v %v", err, updated)
	}
	if updated.ThumbnailPath != expectedThumb {
		t.Errorf("expected thumbnail_path '%s', got '%s'", expectedThumb, updated.ThumbnailPath)
	}

	// 4. el status del clip NO cambió (thumbnail no toca la máquina de estados)
	if updated.Status != "completed" {
		t.Errorf("expected clip status preserved 'completed', got '%s'", updated.Status)
	}
}

func TestExecuteThumbnailIdempotentWhenFileExists(t *testing.T) {
	thumb := &fakeThumbnailer{writeOut: true}
	w, clip, expectedThumb := setupThumbnailWorker(t, thumb)

	// primera pasada
	job := thumbnailJobFor(clip.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := w.executeThumbnail(context.Background(), *job); err != nil {
		t.Fatalf("first thumbnail: %v", err)
	}

	// segunda pasada: el archivo existe → no-op, no vuelve a invocar
	if err := w.executeThumbnail(context.Background(), *job); err != nil {
		t.Fatalf("second thumbnail: %v", err)
	}
	if len(thumb.generated) != 1 {
		t.Errorf("expected thumbnailer called once, got %d times", len(thumb.generated))
	}
	_ = expectedThumb
}

func TestExecuteThumbnailRegeneratesMissingFile(t *testing.T) {
	thumb := &fakeThumbnailer{writeOut: true}
	w, clip, expectedThumb := setupThumbnailWorker(t, thumb)

	// clip con thumbnail_path registrado pero el archivo fue borrado del disco
	if err := db.UpdateClipThumbnail(w.db, clip.ID, expectedThumb); err != nil {
		t.Fatalf("pre-set thumbnail_path: %v", err)
	}

	job := thumbnailJobFor(clip.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := w.executeThumbnail(context.Background(), *job); err != nil {
		t.Fatalf("executeThumbnail: %v", err)
	}

	// SÍ debe regenerar (el registro apuntaba a un archivo inexistente)
	if len(thumb.generated) != 1 {
		t.Errorf("expected regeneration for missing file, got %d calls", len(thumb.generated))
	}
}

func TestExecuteThumbnailFailure(t *testing.T) {
	thumbErr := errors.New("ffmpeg: clip corrupto")
	thumb := &fakeThumbnailer{failFor: map[string]error{}}
	w, clip, _ := setupThumbnailWorker(t, thumb)
	thumb.failFor[clip.Filepath] = thumbErr

	job := thumbnailJobFor(clip.ID)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	err := w.executeThumbnail(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error from failing thumbnailer, got nil")
	}
	if !strings.Contains(err.Error(), "clip corrupto") {
		t.Errorf("expected underlying error in chain, got: %v", err)
	}

	// thumbnail_path NO debe haberse registrado tras el fallo
	updated, _ := db.GetClipByID(w.db, clip.ID)
	if updated.ThumbnailPath != "" {
		t.Errorf("expected empty thumbnail_path after failure, got '%s'", updated.ThumbnailPath)
	}
	// y el clip sigue 'completed' (publicable aunque el thumbnail haya fallado)
	if updated.Status != "completed" {
		t.Errorf("expected clip status preserved 'completed', got '%s'", updated.Status)
	}
}

func TestExecuteThumbnailMissingClip(t *testing.T) {
	thumb := &fakeThumbnailer{}
	w, _, _ := setupThumbnailWorker(t, thumb)

	job := thumbnailJobFor(987654)
	if err := db.EnqueueJob(w.db, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	err := w.executeThumbnail(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error for nonexistent clip, got nil")
	}
	if !strings.Contains(err.Error(), "no existe") {
		t.Errorf("expected 'no existe' in error, got: %v", err)
	}
}

func TestExecuteThumbnailWrongReferenceType(t *testing.T) {
	thumb := &fakeThumbnailer{}
	w, _, _ := setupThumbnailWorker(t, thumb)

	job := &db.Job{Type: "thumbnail", ReferenceID: 1, ReferenceType: "videos"}
	err := w.executeThumbnail(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error for wrong reference_type, got nil")
	}
	if !strings.Contains(err.Error(), "reference_type") {
		t.Errorf("expected 'reference_type' in error, got: %v", err)
	}
}

func TestExecuteThumbnailNoThumbnailerConfigured(t *testing.T) {
	conn := openTestDB(t)
	w, err := NewWorker(WorkerConfig{
		DB: conn, Thumbnailer: nil,
		WorkerID: "no-thumb", MaxConcurrentJobs: 1, PollInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	job := thumbnailJobFor(1)
	err = w.executeThumbnail(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error when no thumbnailer configured, got nil")
	}
	if !strings.Contains(err.Error(), "no thumbnailer") {
		t.Errorf("expected 'no thumbnailer' in error, got: %v", err)
	}
}

func TestExecuteJobThumbnailSuccessCompletesJob(t *testing.T) {
	thumb := &fakeThumbnailer{writeOut: true}
	w, clip, _ := setupThumbnailWorker(t, thumb)

	job := thumbnailJobFor(clip.ID)
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

func TestThumbnailFullPipelineViaWorkerLoop(t *testing.T) {
	thumb := &fakeThumbnailer{writeOut: true}
	w, clip, _ := setupThumbnailWorker(t, thumb)

	// encolar thumbnail y arrancar el worker
	if err := db.EnqueueJob(w.db, thumbnailJobFor(clip.ID)); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	deadline := time.Now().Add(6 * time.Second)
	var done int
	for time.Now().Before(deadline) {
		w.db.QueryRow("SELECT COUNT(*) FROM jobs WHERE type='thumbnail' AND status='done'").Scan(&done)
		if done >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	w.Stop()

	if done < 1 {
		t.Fatal("expected thumbnail job done after worker loop")
	}
	updated, _ := db.GetClipByID(w.db, clip.ID)
	if updated.ThumbnailPath == "" {
		t.Error("expected clip to have thumbnail_path after worker loop")
	}
	_ = fmt.Sprint() // mantener fmt usado si se agregan aserciones
}
