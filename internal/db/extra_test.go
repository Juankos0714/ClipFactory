package db

// Tests complementarios del CRUD: clips, publications (backoff/next_retry_at),
// jobs (locking) y cleanup con retención. Reusa helpers de models_test.go.

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---- helpers compartidos ----

// createSourceClipVideo inserta la cadena completa source -> source_clip -> video
// y devuelve los IDs creados.
func createSourceClipVideo(t *testing.T, db *sql.DB, videoPath string) (sourceID, clipID, videoID int64) {
	t.Helper()

	source := &Source{Platform: "twitch", ChannelID: "12345", ChannelName: "test_channel", Active: true}
	if err := InsertSource(db, source); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	sc := &SourceClip{Platform: "twitch", PlatformClipID: "clip123", SourceID: source.ID, Status: "downloaded"}
	if err := UpsertSourceClip(db, sc); err != nil {
		t.Fatalf("upsert source clip: %v", err)
	}
	v := &Video{SourceClipID: sc.ID, Filepath: videoPath, Status: "completed"}
	if err := InsertVideo(db, v); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	return source.ID, sc.ID, v.ID
}

// createClip inserta un clip para un video y devuelve su ID.
func createClip(t *testing.T, db *sql.DB, videoID int64, clipPath string) int64 {
	t.Helper()
	c := &Clip{VideoID: videoID, Filepath: clipPath, Width: 1080, Height: 1920, Status: "completed"}
	if err := InsertClip(db, c); err != nil {
		t.Fatalf("insert clip: %v", err)
	}
	return c.ID
}

// ---- UpdateClipStatus / GetClipByFilepath ----

func TestUpdateClipStatus(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _, videoID := createSourceClipVideo(t, db, "/tmp/test.mp4")
	clipID := createClip(t, db, videoID, "/tmp/clip.mp4")

	if err := UpdateClipStatus(db, clipID, "failed", "ffmpeg exploded"); err != nil {
		t.Fatalf("update clip status: %v", err)
	}

	var status, errMsg string
	if err := db.QueryRow("SELECT status, error_message FROM clips WHERE id = ?", clipID).Scan(&status, &errMsg); err != nil {
		t.Fatalf("query clip: %v", err)
	}
	if status != "failed" {
		t.Errorf("expected status 'failed', got '%s'", status)
	}
	if errMsg != "ffmpeg exploded" {
		t.Errorf("expected error_message 'ffmpeg exploded', got '%s'", errMsg)
	}
}

func TestGetClipByFilepath(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _, videoID := createSourceClipVideo(t, db, "/tmp/test.mp4")
	createClip(t, db, videoID, "/tmp/clip.mp4")

	got, err := GetClipByFilepath(db, "/tmp/clip.mp4")
	if err != nil {
		t.Fatalf("get clip by filepath: %v", err)
	}
	if got == nil {
		t.Fatal("expected clip, got nil")
	}
	if got.VideoID != videoID {
		t.Errorf("expected video_id %d, got %d", videoID, got.VideoID)
	}
	if got.Width != 1080 || got.Height != 1920 {
		t.Errorf("expected 1080x1920, got %dx%d", got.Width, got.Height)
	}

	missing, err := GetClipByFilepath(db, "/tmp/nope.mp4")
	if err != nil {
		t.Fatalf("get missing clip: %v", err)
	}
	if missing != nil {
		t.Errorf("expected nil for missing clip, got %+v", missing)
	}
}

// ---- UpdatePublicationStatus ----

func TestUpdatePublicationStatus(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _, videoID := createSourceClipVideo(t, db, "/tmp/test.mp4")
	clipID := createClip(t, db, videoID, "/tmp/clip.mp4")

	p := &Publication{ClipID: clipID, Platform: "youtube", Status: "pending"}
	if err := InsertPublication(db, p); err != nil {
		t.Fatalf("insert publication: %v", err)
	}

	publishedAt := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	nextRetry := time.Date(2026, 9, 13, 11, 0, 0, 0, time.UTC)
	if err := UpdatePublicationStatus(db, p.ID, "published", "yt-123", "https://youtu.be/yt-123", "", &publishedAt, &nextRetry, true); err != nil {
		t.Fatalf("update publication status: %v", err)
	}

	var status, extID, extURL string
	var attempts int
	var publishedAtStr, nextRetryStr sql.NullString
	if err := db.QueryRow(
		"SELECT status, external_id, external_url, attempts, published_at, next_retry_at FROM publications WHERE id = ?", p.ID,
	).Scan(&status, &extID, &extURL, &attempts, &publishedAtStr, &nextRetryStr); err != nil {
		t.Fatalf("query publication: %v", err)
	}

	if status != "published" {
		t.Errorf("expected status 'published', got '%s'", status)
	}
	if extID != "yt-123" {
		t.Errorf("expected external_id 'yt-123', got '%s'", extID)
	}
	if extURL != "https://youtu.be/yt-123" {
		t.Errorf("expected external_url 'https://youtu.be/yt-123', got '%s'", extURL)
	}
	if attempts != 1 {
		t.Errorf("expected attempts incremented to 1, got %d", attempts)
	}
	if !publishedAtStr.Valid || publishedAtStr.String != "2026-09-13T10:00:00Z" {
		t.Errorf("expected published_at '2026-09-13T10:00:00Z', got '%v'", publishedAtStr)
	}
	if !nextRetryStr.Valid || nextRetryStr.String != "2026-09-13T11:00:00Z" {
		t.Errorf("expected next_retry_at '2026-09-13T11:00:00Z', got '%v'", nextRetryStr)
	}
}

func TestGetPendingPublicationsRespectsNextRetry(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _, videoID := createSourceClipVideo(t, db, "/tmp/test.mp4")
	clipID1 := createClip(t, db, videoID, "/tmp/clip1.mp4")
	clipID2 := createClip(t, db, videoID, "/tmp/clip2.mp4")

	// publicación con next_retry_at en el futuro: no debe aparecer
	future := time.Now().UTC().Add(1 * time.Hour)
	pFuture := &Publication{ClipID: clipID1, Platform: "youtube", Status: "pending", NextRetryAt: &future}
	if err := InsertPublication(db, pFuture); err != nil {
		t.Fatalf("insert publication: %v", err)
	}

	pending, err := GetPendingPublications(db, "youtube", 10)
	if err != nil {
		t.Fatalf("get pending publications: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending (next_retry in future), got %d", len(pending))
	}

	// con next_retry_at en el pasado: debe aparecer
	past := time.Now().UTC().Add(-1 * time.Hour)
	pPast := &Publication{ClipID: clipID2, Platform: "youtube", Status: "pending", NextRetryAt: &past}
	if err := InsertPublication(db, pPast); err != nil {
		t.Fatalf("insert publication 2: %v", err)
	}

	pending, err = GetPendingPublications(db, "youtube", 10)
	if err != nil {
		t.Fatalf("get pending publications (2): %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending (next_retry in past), got %d", len(pending))
	}
	if pending[0].ID != pPast.ID {
		t.Errorf("expected publication %d, got %d", pPast.ID, pending[0].ID)
	}
}

// ---- GetPendingJobs ----

func TestGetPendingJobs(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	j1 := &Job{Type: "discovery", ReferenceID: 1, ReferenceType: "source_clips"}
	if err := EnqueueJob(db, j1); err != nil {
		t.Fatalf("enqueue job 1: %v", err)
	}
	j2 := &Job{Type: "download", ReferenceID: 2, ReferenceType: "source_clips"}
	if err := EnqueueJob(db, j2); err != nil {
		t.Fatalf("enqueue job 2: %v", err)
	}
	// un job en error no debe aparecer
	if err := FailJob(db, j2.ID, "boom"); err != nil {
		t.Fatalf("fail job 2: %v", err)
	}

	jobs, err := GetPendingJobs(db, 10)
	if err != nil {
		t.Fatalf("get pending jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 pending job, got %d", len(jobs))
	}
	if jobs[0].ID != j1.ID {
		t.Errorf("expected job %d, got %d", j1.ID, jobs[0].ID)
	}
	if jobs[0].Status != "queued" {
		t.Errorf("expected status 'queued', got '%s'", jobs[0].Status)
	}
}

func TestGetPendingJobsLimit(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	for i := 0; i < 5; i++ {
		j := &Job{Type: "process", ReferenceID: int64(i), ReferenceType: "videos"}
		if err := EnqueueJob(db, j); err != nil {
			t.Fatalf("enqueue job %d: %v", i, err)
		}
	}

	jobs, err := GetPendingJobs(db, 3)
	if err != nil {
		t.Fatalf("get pending jobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Errorf("expected 3 jobs (limit), got %d", len(jobs))
	}
}

func TestGetPendingJobsStaleLock(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	j := &Job{Type: "thumbnail", ReferenceID: 1, ReferenceType: "videos"}
	if err := EnqueueJob(db, j); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	if err := LockJob(db, j.ID, "worker-dead"); err != nil {
		t.Fatalf("lock job: %v", err)
	}

	// lock reciente: no debe aparecer
	jobs, err := GetPendingJobs(db, 10)
	if err != nil {
		t.Fatalf("get pending jobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("expected 0 jobs with fresh lock, got %d", len(jobs))
	}

	// simular job re-encolado tras crash: status vuelve a 'queued' pero con lock viejo (>30s)
	old := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	if _, err := db.Exec("UPDATE jobs SET status = 'queued', locked_at = ? WHERE id = ?", old, j.ID); err != nil {
		t.Fatalf("backdate lock: %v", err)
	}

	jobs, err = GetPendingJobs(db, 10)
	if err != nil {
		t.Fatalf("get pending jobs (2): %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("expected 1 job with stale lock, got %d", len(jobs))
	}
}

// TestGetPendingJobsFutureCreatedAt: un job 'queued' con created_at FUTURO no
// se ofrece todavía (mecanismo de backoff de requeuePublish: el job duerme hasta
// su hora). Cuando la hora llega, GetPendingJobs lo devuelve.
func TestGetPendingJobsFutureCreatedAt(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// encolar directo con created_at futuro (como hace requeuePublish)
	future := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	res, err := db.Exec(
		`INSERT INTO jobs (type, reference_id, reference_type, status, created_at, updated_at)
		 VALUES ('publish', 1, 'publications', 'queued', ?, ?)`,
		future, NowUTC(),
	)
	if err != nil {
		t.Fatalf("insert future job: %v", err)
	}
	jobID, _ := res.LastInsertId()

	// y uno normal (created_at = now): este SÍ debe salir
	jNow := &Job{Type: "publish", ReferenceID: 2, ReferenceType: "publications"}
	if err := EnqueueJob(db, jNow); err != nil {
		t.Fatalf("enqueue now job: %v", err)
	}

	jobs, err := GetPendingJobs(db, 10)
	if err != nil {
		t.Fatalf("get pending jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected only the now-job, got %d jobs", len(jobs))
	}
	if jobs[0].ID != jNow.ID {
		t.Errorf("expected job %d (now), got job %d", jNow.ID, jobs[0].ID)
	}

	// la hora del job futuro llega: ahora sí se ofrece
	if _, err := db.Exec(`UPDATE jobs SET created_at = ? WHERE id = ?`, NowUTC(), jobID); err != nil {
		t.Fatalf("backdate created_at: %v", err)
	}
	jobs, err = GetPendingJobs(db, 10)
	if err != nil {
		t.Fatalf("get pending jobs (2): %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs once future job is due, got %d", len(jobs))
	}
}

// ---- CleanupOldCompletedVideos ----

func TestCleanupOldCompletedVideos(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.mp4")
	newPath := filepath.Join(dir, "new.mp4")
	if err := os.WriteFile(oldPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("write old file: %v", err)
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o600); err != nil {
		t.Fatalf("write new file: %v", err)
	}

	// cadena completa para el video VIEJO (se eliminará en el cleanup)
	_, _, _ = createSourceClipVideo(t, db, oldPath)

	// video nuevo con la misma cadena (fuente distinta para no chocar con el unique index)
	source := &Source{Platform: "twitch", ChannelID: "67890", ChannelName: "other_channel", Active: true}
	if err := InsertSource(db, source); err != nil {
		t.Fatalf("insert source 2: %v", err)
	}
	sc2 := &SourceClip{Platform: "twitch", PlatformClipID: "clip456", SourceID: source.ID, Status: "downloaded"}
	if err := UpsertSourceClip(db, sc2); err != nil {
		t.Fatalf("upsert source clip 2: %v", err)
	}
	v2 := &Video{SourceClipID: sc2.ID, Filepath: newPath, Status: "completed"}
	if err := InsertVideo(db, v2); err != nil {
		t.Fatalf("insert video 2: %v", err)
	}

	// marcar el video viejo como actualizado hace 2 horas
	oldTS := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec("UPDATE videos SET updated_at = ? WHERE filepath = ?", oldTS, oldPath); err != nil {
		t.Fatalf("backdate video: %v", err)
	}

	deleted, err := CleanupOldCompletedVideos(db, 1*time.Hour)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// el archivo viejo ya no debe existir; el nuevo sí
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("expected old file to be deleted, stat err: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("expected new file to remain, stat err: %v", err)
	}

	// el registro del video viejo debe haberse eliminado
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM videos WHERE filepath = ?", oldPath).Scan(&count); err != nil {
		t.Fatalf("count videos: %v", err)
	}
	if count != 0 {
		t.Errorf("expected old video row deleted, got count %d", count)
	}
}

// ---- constraints ----

func TestSourceUniquePlatformChannel(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	s1 := &Source{Platform: "twitch", ChannelID: "999", ChannelName: "chan", Active: true}
	if err := InsertSource(db, s1); err != nil {
		t.Fatalf("insert source 1: %v", err)
	}
	s2 := &Source{Platform: "twitch", ChannelID: "999", ChannelName: "chan-dup", Active: true}
	if err := InsertSource(db, s2); err == nil {
		t.Error("expected unique constraint violation for duplicate (platform, channel_id)")
	}
}

func TestVideoUniqueFilepath(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _, videoID := createSourceClipVideo(t, db, "/tmp/same.mp4")

	// insertar el mismo filepath en otro video debe fallar
	dup := &Video{SourceClipID: videoID, Filepath: "/tmp/same.mp4", Status: "incoming"}
	if err := InsertVideo(db, dup); err == nil {
		t.Error("expected unique constraint violation for duplicate filepath")
	}
}

func TestPublicationUniqueClipPlatform(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _, videoID := createSourceClipVideo(t, db, "/tmp/test.mp4")
	clipID := createClip(t, db, videoID, "/tmp/clip.mp4")

	p1 := &Publication{ClipID: clipID, Platform: "youtube", Status: "pending"}
	if err := InsertPublication(db, p1); err != nil {
		t.Fatalf("insert publication 1: %v", err)
	}
	p2 := &Publication{ClipID: clipID, Platform: "youtube", Status: "pending"}
	if err := InsertPublication(db, p2); err == nil {
		t.Error("expected unique constraint violation for duplicate (clip_id, platform)")
	}
}

func TestForeignKeyCascade(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	// foreign_keys ya está activo por el DSN _foreign_keys=on

	_, scID, _ := createSourceClipVideo(t, db, "/tmp/test.mp4")

	var videoCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM videos WHERE source_clip_id = ?", scID).Scan(&videoCount); err != nil {
		t.Fatalf("count videos: %v", err)
	}
	if videoCount != 1 {
		t.Fatalf("expected 1 video, got %d", videoCount)
	}

	// borrar el source_clip debe cascada al video
	if _, err := db.Exec("DELETE FROM source_clips WHERE id = ?", scID); err != nil {
		t.Fatalf("delete source clip: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM videos WHERE source_clip_id = ?", scID).Scan(&videoCount); err != nil {
		t.Fatalf("count videos (2): %v", err)
	}
	if videoCount != 0 {
		t.Errorf("expected cascade delete of videos, got count %d", videoCount)
	}
}

func TestForeignKeyEnforced(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	// foreign_keys ya está activo por el DSN _foreign_keys=on

	// insertar video con source_clip_id inexistente debe fallar
	v := &Video{SourceClipID: 424242, Filepath: "/tmp/orphan.mp4", Status: "incoming"}
	if err := InsertVideo(db, v); err == nil {
		t.Error("expected FK violation for nonexistent source_clip_id")
	}
}

// ---- timestamps y NULLs ----

func TestTimestampsAreUTC(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _, videoID := createSourceClipVideo(t, db, "/tmp/test.mp4")

	v, err := GetVideoByFilepath(db, "/tmp/test.mp4")
	if err != nil {
		t.Fatalf("get video: %v", err)
	}
	if v.CreatedAt.IsZero() {
		t.Error("expected CreatedAt to be populated")
	}
	if v.CreatedAt.Location() != time.UTC {
		t.Errorf("expected CreatedAt in UTC, got %v", v.CreatedAt.Location())
	}
	_ = videoID
}

func TestPublicationWithNullFields(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	_, _, videoID := createSourceClipVideo(t, db, "/tmp/test.mp4")
	clipID := createClip(t, db, videoID, "/tmp/clip.mp4")

	// publicación con todos los campos opcionales en NULL
	p := &Publication{ClipID: clipID, Platform: "tiktok", Status: "pending"}
	if err := InsertPublication(db, p); err != nil {
		t.Fatalf("insert publication: %v", err)
	}

	pending, err := GetPendingPublications(db, "tiktok", 10)
	if err != nil {
		t.Fatalf("get pending publications: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 publication, got %d", len(pending))
	}
	got := pending[0]
	if got.ExternalID != "" || got.ExternalURL != "" || got.ErrorMessage != "" {
		t.Errorf("expected empty external/error fields, got %+v", got)
	}
	if got.NextRetryAt != nil || got.PublishedAt != nil {
		t.Errorf("expected nil NextRetryAt/PublishedAt, got %+v / %+v", got.NextRetryAt, got.PublishedAt)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("expected CreatedAt/UpdatedAt populated")
	}
}

func TestJobWithNullFields(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	j := &Job{Type: "publish", ReferenceID: 7, ReferenceType: "publications"}
	if err := EnqueueJob(db, j); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}

	jobs, err := GetPendingJobs(db, 10)
	if err != nil {
		t.Fatalf("get pending jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	got := jobs[0]
	if got.LockedAt != nil {
		t.Errorf("expected nil LockedAt, got %v", got.LockedAt)
	}
	if got.LockedBy != "" {
		t.Errorf("expected empty LockedBy, got '%s'", got.LockedBy)
	}
	if got.ErrorMessage != "" {
		t.Errorf("expected empty ErrorMessage, got '%s'", got.ErrorMessage)
	}
}
