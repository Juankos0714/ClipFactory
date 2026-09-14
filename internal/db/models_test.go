package db

// Tests del CRUD tipado de models.go: inserts, búsquedas por ID/filepath,
// upserts idempotentes y constraints UNIQUE. Todos usan una DB :memory:
// compartida creada por setupTestDB.

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// una sola conexión: con :memory: cada conexión nueva es una DB distinta
	db.SetMaxOpenConns(1)
	// activar foreign keys explícitamente (los DSN _foreign_keys no son confiables en modernc)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := MigrateDB(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestInsertSource(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	source := &Source{
		Platform:    "twitch",
		ChannelID:   "12345",
		ChannelName: "test_channel",
		Active:      true,
	}

	if err := InsertSource(db, source); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	if source.ID == 0 {
		t.Errorf("expected source.ID > 0, got %d", source.ID)
	}

	// verificar que se insertó correctamente
	var id int64
	var platform, channelID, channelName string
	var active int
	err := db.QueryRow("SELECT id, platform, channel_id, channel_name, active FROM sources WHERE id = ?", source.ID).
		Scan(&id, &platform, &channelID, &channelName, &active)
	if err != nil {
		t.Fatalf("query source: %v", err)
	}

	if id != source.ID {
		t.Errorf("expected id %d, got %d", source.ID, id)
	}
	if platform != source.Platform {
		t.Errorf("expected platform %s, got %s", source.Platform, platform)
	}
	if channelID != source.ChannelID {
		t.Errorf("expected channel_id %s, got %s", source.ChannelID, channelID)
	}
	if channelName != source.ChannelName {
		t.Errorf("expected channel_name %s, got %s", source.ChannelName, channelName)
	}
	if active != 1 {
		t.Errorf("expected active 1, got %d", active)
	}
}

func TestGetSources(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar fuentes
	sources := []Source{
		{Platform: "twitch", ChannelID: "1", ChannelName: "chan1", Active: true},
		{Platform: "twitch", ChannelID: "2", ChannelName: "chan2", Active: false},
		{Platform: "kick", ChannelID: "3", ChannelName: "chan3", Active: true},
	}

	for i := range sources {
		sources[i].CreatedAt = time.Now().UTC()
		sources[i].UpdatedAt = time.Now().UTC()
		if err := InsertSource(db, &sources[i]); err != nil {
			t.Fatalf("insert source %d: %v", i, err)
		}
	}

	// obtener fuentes activas
	gotSources, err := GetSources(db)
	if err != nil {
		t.Fatalf("get sources: %v", err)
	}

	if len(gotSources) != 2 {
		t.Errorf("expected 2 active sources, got %d", len(gotSources))
	}

	// verificar que las fuentes activas son las correctas
	var foundChan1, foundChan3 bool
	for _, s := range gotSources {
		if s.ChannelID == "1" && s.Platform == "twitch" {
			foundChan1 = true
		}
		if s.ChannelID == "3" && s.Platform == "kick" {
			foundChan3 = true
		}
	}
	if !foundChan1 {
		t.Error("expected active source chan1 to be returned")
	}
	if !foundChan3 {
		t.Error("expected active source chan3 to be returned")
	}
}

func TestUpsertSourceClip(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar fuente primero
	source := &Source{
		Platform:    "twitch",
		ChannelID:   "12345",
		ChannelName: "test_channel",
		Active:      true,
	}
	source.CreatedAt = time.Now().UTC()
	source.UpdatedAt = time.Now().UTC()
	if err := InsertSource(db, source); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	// crear source clip
	clip := &SourceClip{
		Platform:        "twitch",
		PlatformClipID:  "clip123",
		SourceID:        source.ID,
		Title:           "Test Clip",
		DurationSeconds: 60.0,
		Status:          "detected",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	// primera inserción
	if err := UpsertSourceClip(db, clip); err != nil {
		t.Fatalf("upsert source clip: %v", err)
	}

	if clip.ID == 0 {
		t.Errorf("expected clip.ID > 0, got %d", clip.ID)
	}

	// segunda inserción (debe ser idempotente)
	clip2 := &SourceClip{
		Platform:       "twitch",
		PlatformClipID: "clip123",
		SourceID:       source.ID,
		Title:          "Different Title",
		Status:         "downloaded",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	if err := UpsertSourceClip(db, clip2); err != nil {
		t.Fatalf("upsert source clip (second): %v", err)
	}

	// verificar que el ID es el mismo (no se creó un nuevo registro)
	if clip2.ID != clip.ID {
		t.Errorf("expected same ID after upsert, got %d vs %d", clip.ID, clip2.ID)
	}

	// verificar que el título no cambió (upsert no modifica si ya existe)
	var title string
	err := db.QueryRow("SELECT title FROM source_clips WHERE id = ?", clip.ID).Scan(&title)
	if err != nil {
		t.Fatalf("query title: %v", err)
	}
	if title != "Test Clip" {
		t.Errorf("expected title 'Test Clip', got '%s'", title)
	}
}

func TestInsertVideo(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar source clip primero
	source := &Source{
		Platform:    "twitch",
		ChannelID:   "12345",
		ChannelName: "test_channel",
		Active:      true,
	}
	source.CreatedAt = time.Now().UTC()
	source.UpdatedAt = time.Now().UTC()
	if err := InsertSource(db, source); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	clip := &SourceClip{
		Platform:       "twitch",
		PlatformClipID: "clip123",
		SourceID:       source.ID,
		Status:         "downloaded",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := UpsertSourceClip(db, clip); err != nil {
		t.Fatalf("upsert source clip: %v", err)
	}

	// insertar video
	video := &Video{
		SourceClipID:    clip.ID,
		Filepath:        "/tmp/test.mp4",
		DurationSeconds: 60.0,
		Width:           1920,
		Height:          1080,
		Status:          "incoming",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	if err := InsertVideo(db, video); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	if video.ID == 0 {
		t.Errorf("expected video.ID > 0, got %d", video.ID)
	}

	// verificar que se insertó correctamente
	var id int64
	var filepath string
	var status string
	err := db.QueryRow("SELECT id, filepath, status FROM videos WHERE id = ?", video.ID).
		Scan(&id, &filepath, &status)
	if err != nil {
		t.Fatalf("query video: %v", err)
	}

	if id != video.ID {
		t.Errorf("expected id %d, got %d", video.ID, id)
	}
	if filepath != video.Filepath {
		t.Errorf("expected filepath %s, got %s", video.Filepath, filepath)
	}
	if status != video.Status {
		t.Errorf("expected status %s, got %s", video.Status, status)
	}
}

func TestGetVideoByFilepath(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar video
	source := &Source{
		Platform:    "twitch",
		ChannelID:   "12345",
		ChannelName: "test_channel",
		Active:      true,
	}
	source.CreatedAt = time.Now().UTC()
	source.UpdatedAt = time.Now().UTC()
	if err := InsertSource(db, source); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	clip := &SourceClip{
		Platform:       "twitch",
		PlatformClipID: "clip123",
		SourceID:       source.ID,
		Status:         "downloaded",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := UpsertSourceClip(db, clip); err != nil {
		t.Fatalf("upsert source clip: %v", err)
	}
	video := &Video{
		SourceClipID: clip.ID,
		Filepath:     "/tmp/test.mp4",
		Status:       "incoming",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	if err := InsertVideo(db, video); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	// buscar por filepath
	gotVideo, err := GetVideoByFilepath(db, "/tmp/test.mp4")
	if err != nil {
		t.Fatalf("get video by filepath: %v", err)
	}

	if gotVideo == nil {
		t.Error("expected video, got nil")
		return
	}

	if gotVideo.ID != video.ID {
		t.Errorf("expected id %d, got %d", video.ID, gotVideo.ID)
	}
	if gotVideo.Filepath != video.Filepath {
		t.Errorf("expected filepath %s, got %s", video.Filepath, gotVideo.Filepath)
	}

	// buscar un filepath que no existe
	gotVideo2, err := GetVideoByFilepath(db, "/tmp/nonexistent.mp4")
	if err != nil {
		t.Fatalf("get video by filepath (nonexistent): %v", err)
	}

	if gotVideo2 != nil {
		t.Errorf("expected nil for nonexistent filepath, got %v", gotVideo2)
	}
}

func TestUpdateVideoStatus(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar video
	source := &Source{
		Platform:    "twitch",
		ChannelID:   "12345",
		ChannelName: "test_channel",
		Active:      true,
	}
	source.CreatedAt = time.Now().UTC()
	source.UpdatedAt = time.Now().UTC()
	if err := InsertSource(db, source); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	clip := &SourceClip{
		Platform:       "twitch",
		PlatformClipID: "clip123",
		SourceID:       source.ID,
		Status:         "downloaded",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := UpsertSourceClip(db, clip); err != nil {
		t.Fatalf("upsert source clip: %v", err)
	}
	video := &Video{
		SourceClipID: clip.ID,
		Filepath:     "/tmp/test.mp4",
		Status:       "incoming",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := InsertVideo(db, video); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	// actualizar estado
	if err := UpdateVideoStatus(db, video.ID, "processing", ""); err != nil {
		t.Fatalf("update video status: %v", err)
	}

	// verificar que el estado se actualizó
	var status string
	err := db.QueryRow("SELECT status FROM videos WHERE id = ?", video.ID).Scan(&status)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}

	if status != "processing" {
		t.Errorf("expected status 'processing', got '%s'", status)
	}
}

func TestInsertClip(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar video primero
	source := &Source{
		Platform:    "twitch",
		ChannelID:   "12345",
		ChannelName: "test_channel",
		Active:      true,
	}
	source.CreatedAt = time.Now().UTC()
	source.UpdatedAt = time.Now().UTC()
	if err := InsertSource(db, source); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	clip := &SourceClip{
		Platform:       "twitch",
		PlatformClipID: "clip123",
		SourceID:       source.ID,
		Status:         "downloaded",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := UpsertSourceClip(db, clip); err != nil {
		t.Fatalf("upsert source clip: %v", err)
	}
	video := &Video{
		SourceClipID: clip.ID,
		Filepath:     "/tmp/test.mp4",
		Status:       "completed",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := InsertVideo(db, video); err != nil {
		t.Fatalf("insert video: %v", err)
	}

	// insertar clip
	c := &Clip{
		VideoID:      video.ID,
		StartTimeSec: 10.0,
		EndTimeSec:   40.0,
		Filepath:     "/tmp/clip.mp4",
		DurationSec:  30.0,
		Width:        1080,
		Height:       1920,
		Status:       "completed",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	if err := InsertClip(db, c); err != nil {
		t.Fatalf("insert clip: %v", err)
	}

	if c.ID == 0 {
		t.Errorf("expected clip.ID > 0, got %d", c.ID)
	}

	// verificar que se insertó correctamente
	var id int64
	var filepath string
	var width, height int
	var status string
	err := db.QueryRow("SELECT id, filepath, width, height, status FROM clips WHERE id = ?", c.ID).
		Scan(&id, &filepath, &width, &height, &status)
	if err != nil {
		t.Fatalf("query clip: %v", err)
	}

	if id != c.ID {
		t.Errorf("expected id %d, got %d", c.ID, id)
	}
	if filepath != c.Filepath {
		t.Errorf("expected filepath %s, got %s", c.Filepath, filepath)
	}
	if width != c.Width {
		t.Errorf("expected width %d, got %d", c.Width, width)
	}
	if height != c.Height {
		t.Errorf("expected height %d, got %d", c.Height, height)
	}
	if status != c.Status {
		t.Errorf("expected status %s, got %s", c.Status, status)
	}
}

func TestInsertPublication(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar clip primero
	source := &Source{
		Platform:    "twitch",
		ChannelID:   "12345",
		ChannelName: "test_channel",
		Active:      true,
	}
	source.CreatedAt = time.Now().UTC()
	source.UpdatedAt = time.Now().UTC()
	if err := InsertSource(db, source); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	clip := &SourceClip{
		Platform:       "twitch",
		PlatformClipID: "clip123",
		SourceID:       source.ID,
		Status:         "downloaded",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := UpsertSourceClip(db, clip); err != nil {
		t.Fatalf("upsert source clip: %v", err)
	}
	video := &Video{
		SourceClipID: clip.ID,
		Filepath:     "/tmp/test.mp4",
		Status:       "completed",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := InsertVideo(db, video); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	c := &Clip{
		VideoID:   video.ID,
		Filepath:  "/tmp/clip.mp4",
		Width:     1080,
		Height:    1920,
		Status:    "completed",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := InsertClip(db, c); err != nil {
		t.Fatalf("insert clip: %v", err)
	}

	// insertar publicación
	p := &Publication{
		ClipID:    c.ID,
		Platform:  "youtube",
		Status:    "pending",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	if err := InsertPublication(db, p); err != nil {
		t.Fatalf("insert publication: %v", err)
	}

	if p.ID == 0 {
		t.Errorf("expected publication.ID > 0, got %d", p.ID)
	}

	// verificar que se insertó correctamente
	var id int64
	var platform, status string
	err := db.QueryRow("SELECT id, platform, status FROM publications WHERE id = ?", p.ID).
		Scan(&id, &platform, &status)
	if err != nil {
		t.Fatalf("query publication: %v", err)
	}

	if id != p.ID {
		t.Errorf("expected id %d, got %d", p.ID, id)
	}
	if platform != p.Platform {
		t.Errorf("expected platform %s, got %s", p.Platform, platform)
	}
	if status != p.Status {
		t.Errorf("expected status %s, got %s", p.Status, status)
	}
}

func TestGetPendingPublications(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar clips y publicaciones
	source := &Source{
		Platform:    "twitch",
		ChannelID:   "12345",
		ChannelName: "test_channel",
		Active:      true,
	}
	source.CreatedAt = time.Now().UTC()
	source.UpdatedAt = time.Now().UTC()
	if err := InsertSource(db, source); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	sc := &SourceClip{
		Platform:       "twitch",
		PlatformClipID: "clip123",
		SourceID:       source.ID,
		Status:         "downloaded",
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := UpsertSourceClip(db, sc); err != nil {
		t.Fatalf("upsert source clip: %v", err)
	}
	v := &Video{
		SourceClipID: sc.ID,
		Filepath:     "/tmp/test.mp4",
		Status:       "completed",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := InsertVideo(db, v); err != nil {
		t.Fatalf("insert video: %v", err)
	}
	c := &Clip{
		VideoID:   v.ID,
		Filepath:  "/tmp/clip.mp4",
		Width:     1080,
		Height:    1920,
		Status:    "completed",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := InsertClip(db, c); err != nil {
		t.Fatalf("insert clip: %v", err)
	}

	// insertar varias publicaciones (con diferentes clip_id para evitar UNIQUE constraint)
	c2 := &Clip{
		VideoID:   v.ID,
		Filepath:  "/tmp/clip2.mp4",
		Width:     1080,
		Height:    1920,
		Status:    "completed",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := InsertClip(db, c2); err != nil {
		t.Fatalf("insert clip2: %v", err)
	}

	p1 := &Publication{
		ClipID:    c.ID,
		Platform:  "youtube",
		Status:    "pending",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := InsertPublication(db, p1); err != nil {
		t.Fatalf("insert publication 1: %v", err)
	}
	p2 := &Publication{
		ClipID:    c2.ID,
		Platform:  "youtube",
		Status:    "error",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := InsertPublication(db, p2); err != nil {
		t.Fatalf("insert publication 2: %v", err)
	}
	p3 := &Publication{
		ClipID:    c.ID,
		Platform:  "meta",
		Status:    "pending",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := InsertPublication(db, p3); err != nil {
		t.Fatalf("insert publication 3: %v", err)
	}

	// obtener publicaciones pendientes para youtube
	pending, err := GetPendingPublications(db, "youtube", 10)
	if err != nil {
		t.Fatalf("get pending publications: %v", err)
	}

	// debería obtener p1 (pending) y p2 (error)
	if len(pending) != 2 {
		t.Errorf("expected 2 pending/error publications for youtube, got %d", len(pending))
	}

	// verificar que p1 está en la lista
	foundP1 := false
	for _, p := range pending {
		if p.ID == p1.ID {
			foundP1 = true
			break
		}
	}
	if !foundP1 {
		t.Error("expected p1 to be in pending list")
	}

	// obtener publicaciones pendientes para meta
	pendingMeta, err := GetPendingPublications(db, "meta", 10)
	if err != nil {
		t.Fatalf("get pending publications (meta): %v", err)
	}

	if len(pendingMeta) != 1 {
		t.Errorf("expected 1 pending publication for meta, got %d", len(pendingMeta))
	}

	if len(pendingMeta) > 0 && pendingMeta[0].ID != p3.ID {
		t.Errorf("expected pending publication ID %d, got %d", p3.ID, pendingMeta[0].ID)
	}
}

func TestEnqueueJob(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar un job
	job := &Job{
		Type:          "discovery",
		ReferenceID:   123,
		ReferenceType: "source_clips",
		Status:        "queued",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}

	if err := EnqueueJob(db, job); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}

	if job.ID == 0 {
		t.Errorf("expected job.ID > 0, got %d", job.ID)
	}

	// verificar que se insertó correctamente
	var id int64
	var jobType, refType, status string
	err := db.QueryRow("SELECT id, type, reference_type, status FROM jobs WHERE id = ?", job.ID).
		Scan(&id, &jobType, &refType, &status)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}

	if id != job.ID {
		t.Errorf("expected id %d, got %d", job.ID, id)
	}
	if jobType != job.Type {
		t.Errorf("expected type %s, got %s", job.Type, jobType)
	}
	if refType != job.ReferenceType {
		t.Errorf("expected reference_type %s, got %s", job.ReferenceType, refType)
	}
	if status != job.Status {
		t.Errorf("expected status %s, got %s", job.Status, status)
	}
}

func TestLockJob(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar un job
	job := &Job{
		Type:          "download",
		ReferenceID:   456,
		ReferenceType: "videos",
		Status:        "queued",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := EnqueueJob(db, job); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}

	// lockear el job
	if err := LockJob(db, job.ID, "worker-1"); err != nil {
		t.Fatalf("lock job: %v", err)
	}

	// verificar que el job se bloqueó
	var status, lockedBy string
	var lockedAt sql.NullString
	err := db.QueryRow("SELECT status, locked_by, locked_at FROM jobs WHERE id = ?", job.ID).
		Scan(&status, &lockedBy, &lockedAt)
	if err != nil {
		t.Fatalf("query job: %v", err)
	}

	if status != "running" {
		t.Errorf("expected status 'running', got '%s'", status)
	}
	if lockedBy != "worker-1" {
		t.Errorf("expected locked_by 'worker-1', got '%s'", lockedBy)
	}
	if !lockedAt.Valid {
		t.Error("expected locked_at to be set")
	}

	// intentar lockear el mismo job de nuevo (debe fallar porque ya está running)
	err = LockJob(db, job.ID, "worker-2")
	if err == nil {
		t.Error("expected lock job to fail for already running job")
	}

	// insertar otro job y bloquearlo
	job2 := &Job{
		Type:          "process",
		ReferenceID:   789,
		ReferenceType: "clips",
		Status:        "queued",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := EnqueueJob(db, job2); err != nil {
		t.Fatalf("enqueue job2: %v", err)
	}

	if err := LockJob(db, job2.ID, "worker-2"); err != nil {
		t.Fatalf("lock job2: %v", err)
	}

	// verificar que el segundo job se bloqueó correctamente
	err = db.QueryRow("SELECT status, locked_by FROM jobs WHERE id = ?", job2.ID).
		Scan(&status, &lockedBy)
	if err != nil {
		t.Fatalf("query job2: %v", err)
	}

	if status != "running" {
		t.Errorf("expected status 'running' for job2, got '%s'", status)
	}
	if lockedBy != "worker-2" {
		t.Errorf("expected locked_by 'worker-2' for job2, got '%s'", lockedBy)
	}
}

func TestCompleteJob(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar y bloquear un job
	job := &Job{
		Type:          "thumbnail",
		ReferenceID:   111,
		ReferenceType: "videos",
		Status:        "queued",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := EnqueueJob(db, job); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	if err := LockJob(db, job.ID, "worker-1"); err != nil {
		t.Fatalf("lock job: %v", err)
	}

	// completar el job
	if err := CompleteJob(db, job.ID); err != nil {
		t.Fatalf("complete job: %v", err)
	}

	// verificar que el job se completó
	var status string
	err := db.QueryRow("SELECT status FROM jobs WHERE id = ?", job.ID).Scan(&status)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}

	if status != "done" {
		t.Errorf("expected status 'done', got '%s'", status)
	}
}

func TestFailJob(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar y bloquear un job
	job := &Job{
		Type:          "publish",
		ReferenceID:   222,
		ReferenceType: "clips",
		Status:        "queued",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := EnqueueJob(db, job); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	if err := LockJob(db, job.ID, "worker-1"); err != nil {
		t.Fatalf("lock job: %v", err)
	}

	// fallar el job
	if err := FailJob(db, job.ID, "error de prueba"); err != nil {
		t.Fatalf("fail job: %v", err)
	}

	// verificar que el job falló
	var status, errorMessage string
	err := db.QueryRow("SELECT status, error_message FROM jobs WHERE id = ?", job.ID).
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

func TestInsertLog(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// insertar un log
	logEntry := &Log{
		Level:     "info",
		Module:    "worker",
		Message:   "test log message",
		CreatedAt: time.Now().UTC(),
	}

	if err := InsertLog(db, logEntry); err != nil {
		t.Fatalf("insert log: %v", err)
	}

	if logEntry.ID == 0 {
		t.Errorf("expected log.ID > 0, got %d", logEntry.ID)
	}

	// verificar que se insertó correctamente
	var id int64
	var level, module, message string
	err := db.QueryRow("SELECT id, level, module, message FROM logs WHERE id = ?", logEntry.ID).
		Scan(&id, &level, &module, &message)
	if err != nil {
		t.Fatalf("query log: %v", err)
	}

	if id != logEntry.ID {
		t.Errorf("expected id %d, got %d", logEntry.ID, id)
	}
	if level != logEntry.Level {
		t.Errorf("expected level %s, got %s", logEntry.Level, level)
	}
	if module != logEntry.Module {
		t.Errorf("expected module %s, got %s", logEntry.Module, module)
	}
	if message != logEntry.Message {
		t.Errorf("expected message %s, got %s", logEntry.Message, message)
	}
}

func TestNowUTC(t *testing.T) {
	ts := NowUTC()
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("parse timestamp: %v", err)
	}

	// verificar que el timestamp es UTC
	if parsed.Location() != time.UTC {
		t.Errorf("expected UTC timezone, got %v", parsed.Location())
	}

	// verificar que el timestamp es reciente
	now := time.Now().UTC()
	if parsed.Sub(now) > 5*time.Second || now.Sub(parsed) > 5*time.Second {
		t.Errorf("expected recent timestamp, got %v", parsed)
	}
}
