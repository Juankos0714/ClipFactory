package db

// Tests de las consultas agregadas de internal/db/stats.go (backend del CLI
// 'status'). Cada test construye su propio escenario en una DB :memory: y
// verifica conteos por estado/plataforma/tipo, incluyendo casos borde:
// DB vacía (mapas vacíos, no nil) y estados inesperados (se cuentan igual).

import (
	"database/sql"
	"fmt"
	"testing"
)

// setupStatsDB abre una DB de prueba ya migrada.
func setupStatsDB(t *testing.T) *sql.DB {
	t.Helper()
	conn := setupTestDB(t)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestGetSourceStats: conteo por plataforma separando activos/inactivos.
func TestGetSourceStats(t *testing.T) {
	db := setupStatsDB(t)

	// 2 twitch activos, 1 twitch inactivo, 1 kick activo
	fixtures := []Source{
		{Platform: "twitch", ChannelID: "1", ChannelName: "a", Active: true},
		{Platform: "twitch", ChannelID: "2", ChannelName: "b", Active: true},
		{Platform: "twitch", ChannelID: "3", ChannelName: "c", Active: false},
		{Platform: "kick", ChannelID: "xokas", ChannelName: "xokas", Active: true},
	}
	for i := range fixtures {
		if err := InsertSource(db, &fixtures[i]); err != nil {
			t.Fatalf("insert source %d: %v", i, err)
		}
	}

	got, err := GetSourceStats(db)
	if err != nil {
		t.Fatalf("GetSourceStats: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 platforms, got %d (%v)", len(got), got)
	}
	if got["twitch"] != (SourceCounts{Active: 2, Inactive: 1}) {
		t.Errorf("twitch: expected {2 1}, got %+v", got["twitch"])
	}
	if got["kick"] != (SourceCounts{Active: 1, Inactive: 0}) {
		t.Errorf("kick: expected {1 0}, got %+v", got["kick"])
	}
}

// TestGetSourceStatsEmpty: sin canales, mapa vacío (no nil).
func TestGetSourceStatsEmpty(t *testing.T) {
	db := setupStatsDB(t)

	got, err := GetSourceStats(db)
	if err != nil {
		t.Fatalf("GetSourceStats: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil map for empty DB")
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

// TestGetSourceClipStats: agrupado por plataforma y estado, e incluye estados
// fuera de la máquina documentada (los datos mandan, no el enum).
func TestGetSourceClipStats(t *testing.T) {
	db := setupStatsDB(t)

	source := &Source{Platform: "twitch", ChannelID: "1", ChannelName: "a", Active: true}
	if err := InsertSource(db, source); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	ksrc := &Source{Platform: "kick", ChannelID: "xokas", ChannelName: "xokas", Active: true}
	if err := InsertSource(db, ksrc); err != nil {
		t.Fatalf("insert kick source: %v", err)
	}

	// twitch: 2 detected, 1 downloaded, 1 error
	twitchFixtures := []SourceClip{
		{Platform: "twitch", PlatformClipID: "t1", SourceID: source.ID, Status: "detected"},
		{Platform: "twitch", PlatformClipID: "t2", SourceID: source.ID, Status: "detected"},
		{Platform: "twitch", PlatformClipID: "t3", SourceID: source.ID, Status: "downloaded"},
		{Platform: "twitch", PlatformClipID: "t4", SourceID: source.ID, Status: "error"},
	}
	for i := range twitchFixtures {
		if err := UpsertSourceClip(db, &twitchFixtures[i]); err != nil {
			t.Fatalf("upsert twitch clip %d: %v", i, err)
		}
	}

	// kick: 1 detected
	kc := &SourceClip{Platform: "kick", PlatformClipID: "k1", SourceID: ksrc.ID, Status: "detected"}
	if err := UpsertSourceClip(db, kc); err != nil {
		t.Fatalf("upsert kick clip: %v", err)
	}

	// un estado "raro" (fuera del enum documentado): debe contarse igual
	if _, err := db.Exec(`UPDATE source_clips SET status='raro' WHERE platform_clip_id='t4'`); err != nil {
		t.Fatalf("set odd status: %v", err)
	}

	got, err := GetSourceClipStats(db)
	if err != nil {
		t.Fatalf("GetSourceClipStats: %v", err)
	}
	if got["twitch"]["detected"] != 2 {
		t.Errorf("twitch detected: expected 2, got %d", got["twitch"]["detected"])
	}
	if got["twitch"]["downloaded"] != 1 {
		t.Errorf("twitch downloaded: expected 1, got %d", got["twitch"]["downloaded"])
	}
	if got["twitch"]["raro"] != 1 {
		t.Errorf("twitch 'raro': expected 1 (datos mandan), got %d", got["twitch"]["raro"])
	}
	if got["kick"]["detected"] != 1 {
		t.Errorf("kick detected: expected 1, got %d", got["kick"]["detected"])
	}
}

// TestGetVideoAndClipStats: conteos simples por estado de videos y clips.
func TestGetVideoAndClipStats(t *testing.T) {
	db := setupStatsDB(t)

	// cadena válida source → source_clip → video (la FK de videos exige source_clip)
	_, scID, _ := createSourceClipVideo(t, db, "/tmp/stats-v0.mp4") // status completed

	// 3 videos más sobre el mismo source_clip: completed, incoming, failed
	for i, status := range []string{"completed", "incoming", "failed"} {
		if _, err := db.Exec(
			`INSERT INTO videos (source_clip_id, filepath, status, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)`,
			scID, fmt.Sprintf("/data/v%d.mp4", i), status, NowUTC(), NowUTC(),
		); err != nil {
			t.Fatalf("insert video %d: %v", i, err)
		}
	}

	got, err := GetVideoStats(db)
	if err != nil {
		t.Fatalf("GetVideoStats: %v", err)
	}
	if got["completed"] != 2 || got["incoming"] != 1 || got["failed"] != 1 {
		t.Errorf("video stats inesperadas: %v", got)
	}

	// clips: 1 completed, 1 failed (FK video_id=1; para stats basta)
	if _, err := db.Exec(
		`INSERT INTO clips (video_id, start_time_seconds, end_time_seconds, filepath, status, created_at, updated_at)
		 VALUES (1, 0, 30, '/data/c1.mp4', 'completed', ?, ?)`, NowUTC(), NowUTC(),
	); err != nil {
		t.Fatalf("insert clip: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO clips (video_id, start_time_seconds, end_time_seconds, filepath, status, created_at, updated_at)
		 VALUES (1, 0, 30, '/data/c2.mp4', 'failed', ?, ?)`, NowUTC(), NowUTC(),
	); err != nil {
		t.Fatalf("insert clip 2: %v", err)
	}

	clips, err := GetClipStats(db)
	if err != nil {
		t.Fatalf("GetClipStats: %v", err)
	}
	if clips["completed"] != 1 || clips["failed"] != 1 {
		t.Errorf("clip stats inesperadas: %v", clips)
	}
}

// TestGetPublicationStats: agrupado por plataforma y estado.
func TestGetPublicationStats(t *testing.T) {
	db := setupStatsDB(t)

	_, _, videoID := createSourceClipVideo(t, db, "/tmp/pub-stats.mp4")
	if videoID == 0 {
		t.Fatal("setup video")
	}

	// un clip por publication: UNIQUE(clip_id, platform) prohíbe dos filas
	// para el mismo par, así que cada fixture necesita su propio clip
	fixtures := []struct {
		platform string
		status   string
	}{
		{"youtube", "published"},
		{"youtube", "error"},
		{"meta", "pending"},
		{"meta", "pending"},
		{"meta", "waiting_rate_limit"},
	}
	for i, f := range fixtures {
		clipID := createClip(t, db, videoID, fmt.Sprintf("/tmp/pub-clip-%d.mp4", i))
		pub := &Publication{ClipID: clipID, Platform: f.platform, Status: f.status}
		if err := InsertPublication(db, pub); err != nil {
			t.Fatalf("insert publication %d: %v", i, err)
		}
	}

	got, err := GetPublicationStats(db)
	if err != nil {
		t.Fatalf("GetPublicationStats: %v", err)
	}
	if got["youtube"]["published"] != 1 || got["youtube"]["error"] != 1 {
		t.Errorf("youtube stats inesperadas: %v", got["youtube"])
	}
	if got["meta"]["pending"] != 2 || got["meta"]["waiting_rate_limit"] != 1 {
		t.Errorf("meta stats inesperadas: %v", got["meta"])
	}
}

// TestGetJobStats: agrupado por tipo y estado.
func TestGetJobStats(t *testing.T) {
	db := setupStatsDB(t)

	// 3 discovery (2 done, 1 queued), 2 publish (1 error, 1 queued), 1 poll en queued
	fixtures := []struct {
		jtype  string
		status string
	}{
		{"discovery", "done"},
		{"discovery", "done"},
		{"discovery", "queued"},
		{"publish", "error"},
		{"publish", "queued"},
		{"poll_publications", "queued"},
	}
	for i, f := range fixtures {
		res, err := db.Exec(
			`INSERT INTO jobs (type, reference_id, reference_type, status, created_at, updated_at)
			 VALUES (?, 1, 'system', ?, ?, ?)`,
			f.jtype, f.status, NowUTC(), NowUTC(),
		)
		if err != nil {
			t.Fatalf("insert job %d: %v", i, err)
		}
		_ = res
	}

	got, err := GetJobStats(db)
	if err != nil {
		t.Fatalf("GetJobStats: %v", err)
	}
	if got["discovery"]["done"] != 2 || got["discovery"]["queued"] != 1 {
		t.Errorf("discovery stats inesperadas: %v", got["discovery"])
	}
	if got["publish"]["error"] != 1 || got["publish"]["queued"] != 1 {
		t.Errorf("publish stats inesperadas: %v", got["publish"])
	}
	if got["poll_publications"]["queued"] != 1 {
		t.Errorf("poll_publications stats inesperadas: %v", got["poll_publications"])
	}
}

// TestGetJobStatsEmpty: cola vacía → mapa vacío no nil.
func TestGetJobStatsEmpty(t *testing.T) {
	db := setupStatsDB(t)

	got, err := GetJobStats(db)
	if err != nil {
		t.Fatalf("GetJobStats: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("expected empty non-nil map, got %v", got)
	}
}
