package worker

// Tests del re-encolado automático de publications: job poll_publications,
// requeuePublish (job futuro con created_at), enrutado de publish por
// plataforma (youtube/meta) y discovery/download por plataforma (twitch/kick).
// Reusa los fakes definidos en discovery_test.go, download_test.go y publish_test.go.

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/adapter/twitch"
	"github.com/juankos0714/clipfactory/internal/db"
)

// metaPublisher es un Publisher fake que se registra como plataforma "meta".
type metaPublisher struct {
	failWith error
	uploads  int
}

func (m *metaPublisher) UploadVideo(ctx context.Context, videoPath, title, description string, tags []string) (string, string, error) {
	m.uploads++
	if m.failWith != nil {
		return "", "", m.failWith
	}
	return "meta_vid_1", "https://facebook.com/meta_vid_1", nil
}

// TestPollPublicationsEnqueuesPending: publications en error con next_retry_at
// vencido se re-encolan como jobs publish; las que tienen job en vuelo no se duplican.
func TestPollPublicationsEnqueuesPending(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.publishers = map[string]Publisher{
		"youtube": &fakePublisher{writeOutput: true},
		"meta":    &metaPublisher{},
	}

	// 2 cadenas completas por plataforma
	ytPubs := setupPublishChains(t, conn, t.TempDir(), 2)
	metaPubs := setupPublishChains(t, conn, t.TempDir(), 2)

	// youtube pub 1: error con next_retry vencido → debe re-encolarse
	past := time.Now().UTC().Add(-1 * time.Hour)
	if err := db.UpdatePublicationStatus(conn, ytPubs[0].ID, "error", "", "", "boom", nil, &past, true); err != nil {
		t.Fatalf("update pub: %v", err)
	}
	// youtube pub 2: queda pending sin next_retry → también debe re-encolarse

	// meta pub 1: error con next_retry FUTURO → NO debe re-encolarse
	future := time.Now().UTC().Add(2 * time.Hour)
	if err := db.UpdatePublicationStatus(conn, metaPubs[0].ID, "error", "", "", "boom", nil, &future, true); err != nil {
		t.Fatalf("update pub: %v", err)
	}
	// meta pub 2: ya published → NO debe re-encolarse
	if err := db.UpdatePublicationStatus(conn, metaPubs[1].ID, "published", "x", "y", "", &past, nil, false); err != nil {
		t.Fatalf("update pub: %v", err)
	}

	if err := w.pollPublications(context.Background()); err != nil {
		t.Fatalf("pollPublications: %v", err)
	}

	// jobs publish encolados por el poll: yt pub1 (error vencido) + yt pub2 (pending) = 2
	var queued int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='publish' AND status='queued'`).Scan(&queued); err != nil {
		t.Fatalf("count: %v", err)
	}
	if queued != 2 {
		t.Errorf("expected 2 queued publish jobs, got %d", queued)
	}
}

// TestPollPublicationsSkipsWithActiveJob: una publication con un publish ya
// encolado no recibe otro (idempotencia del poll).
func TestPollPublicationsSkipsWithActiveJob(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.publishers = map[string]Publisher{"youtube": &fakePublisher{writeOutput: true}}

	pubs := setupPublishChains(t, conn, t.TempDir(), 1)

	// encolar un publish manual ANTES del poll
	job := &db.Job{Type: "publish", ReferenceID: pubs[0].ID, ReferenceType: "publications"}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if err := w.pollPublications(context.Background()); err != nil {
		t.Fatalf("pollPublications: %v", err)
	}

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='publish' AND reference_id=?`, pubs[0].ID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 publish job (no duplicates), got %d", count)
	}
}

// TestMaybePollPublicationsInterval: respeta el intervalo y delega en
// EnsureActiveJob (no duplica polls en vuelo).
func TestMaybePollPublicationsInterval(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.cfg.PollPublicationsInterval = time.Hour // no vence nunca durante el test

	w.maybePollPublications(context.Background())
	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='poll_publications'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 poll job, got %d", count)
	}

	// segundo tick: intervalo no vencido → no encola otro
	w.maybePollPublications(context.Background())
	if err := conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='poll_publications'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected still 1 poll job, got %d", count)
	}

	// intervalo 0 = desactivado (worker nuevo, otra DB)
	w2 := newTestWorker(t, openTestDB(t))
	w2.cfg.PollPublicationsInterval = 0
	w2.maybePollPublications(context.Background())
	if err := w2.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='poll_publications'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no poll job when disabled, got %d", count)
	}
}

// TestExecutePublishRateLimitRequeues: tras cuota agotada, el job queda 'done'
// PERO existe un job publish futuro (created_at ~24h) que lo reintenta solo.
func TestExecutePublishRateLimitRequeues(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.publishers = map[string]Publisher{"youtube": &rateLimitPublisher{}}

	pubs := setupPublishChains(t, conn, t.TempDir(), 1)

	job := &db.Job{Type: "publish", ReferenceID: pubs[0].ID, ReferenceType: "publications"}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(conn, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	// el handler no debe fallar: la cuota no es un error del job
	if err := w.executePublish(context.Background(), *job); err != nil {
		t.Fatalf("executePublish: %v", err)
	}

	// la publication quedó en waiting_rate_limit
	got, err := db.GetPublicationByID(conn, pubs[0].ID)
	if err != nil || got == nil {
		t.Fatalf("get publication: %v %v", err, got)
	}
	if got.Status != "waiting_rate_limit" {
		t.Errorf("expected waiting_rate_limit, got %q", got.Status)
	}

	// y existe el job de reintento: queued, futuro (~24h), distinto del actual
	var futureCount int
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM jobs WHERE type='publish' AND reference_id=? AND status='queued' AND created_at > ?`,
		pubs[0].ID, db.NowUTC(),
	).Scan(&futureCount); err != nil {
		t.Fatalf("count future: %v", err)
	}
	if futureCount != 1 {
		t.Errorf("expected 1 future publish job after rate limit, got %d", futureCount)
	}
}

// TestExecutePublishErrorRequeuesWithBackoff: tras un error genérico queda un
// job futuro con created_at ≈ now+1h (backoff 2^0).
func TestExecutePublishErrorRequeuesWithBackoff(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.publishers = map[string]Publisher{"youtube": &fakePublisher{}} // falla con "upload fallido simulado"

	pubs := setupPublishChains(t, conn, t.TempDir(), 1)

	job := &db.Job{Type: "publish", ReferenceID: pubs[0].ID, ReferenceType: "publications"}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(conn, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	_ = w.executePublish(context.Background(), *job) // error esperado

	// job futuro con created_at entre now+55m y now+65m
	var raw string
	if err := conn.QueryRow(
		`SELECT created_at FROM jobs WHERE type='publish' AND reference_id=? AND status='queued' AND created_at > ?`,
		pubs[0].ID, db.NowUTC(),
	).Scan(&raw); err != nil {
		t.Fatalf("expected future publish job, got: %v", err)
	}
	createdAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("parse created_at: %v", err)
	}
	want := time.Now().UTC().Add(time.Hour)
	if diff := createdAt.Sub(want); diff > 5*time.Minute || diff < -5*time.Minute {
		t.Errorf("expected created_at ~+1h, got %v (diff %v)", createdAt, diff)
	}
}

// TestPublishRoutesByPlatform: dos publications (youtube y meta) con dos
// publishers distintos → cada una publica con su plataforma, sin cruzarse.
func TestPublishRoutesByPlatform(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)

	yt := &fakePublisher{writeOutput: true}
	meta := &metaPublisher{}
	w.publishers = map[string]Publisher{"youtube": yt, "meta": meta}

	dataDir := t.TempDir()
	ytPubs := setupPublishChains(t, conn, dataDir, 1)
	// dataDir distinto: los filepaths de videos/clips son UNIQUE y las cadenas
	// de setup usan el nombre de archivo derivado del índice
	metaPubs := setupPublishChains(t, conn, t.TempDir(), 1)

	// cambiar la plataforma de la segunda publication a 'meta'
	if _, err := conn.Exec(`UPDATE publications SET platform='meta' WHERE id=?`, metaPubs[0].ID); err != nil {
		t.Fatalf("update platform: %v", err)
	}

	for _, p := range []*db.Publication{ytPubs[0], metaPubs[0]} {
		job := &db.Job{Type: "publish", ReferenceID: p.ID, ReferenceType: "publications"}
		if err := db.EnqueueJob(conn, job); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		if err := db.LockJob(conn, job.ID, w.cfg.WorkerID); err != nil {
			t.Fatalf("lock: %v", err)
		}
		if err := w.executePublish(context.Background(), *job); err != nil {
			t.Fatalf("executePublish pub %d: %v", p.ID, err)
		}
	}

	if yt.uploads == nil {
		t.Fatal("expected youtube uploads recorded")
	}
	if len(yt.uploads) != 1 {
		t.Errorf("expected youtube publisher used once, got %d", len(yt.uploads))
	}
	if meta.uploads != 1 {
		t.Errorf("expected meta publisher used once, got %d", meta.uploads)
	}

	// cada publication registró su éxito
	var ytStatus, metaStatus string
	_ = conn.QueryRow(`SELECT status FROM publications WHERE id=?`, ytPubs[0].ID).Scan(&ytStatus)
	_ = conn.QueryRow(`SELECT status FROM publications WHERE id=?`, metaPubs[0].ID).Scan(&metaStatus)
	if ytStatus != "published" || metaStatus != "published" {
		t.Errorf("expected both published, got yt=%s meta=%s", ytStatus, metaStatus)
	}
}

// TestPublishUnknownPlatformFailsClear: una publication de una plataforma sin
// publisher falla con mensaje claro (no pánico). V2 solo admite youtube/meta
// en el CHECK, así que usamos 'meta' sin publisher registrado en el worker.
func TestPublishUnknownPlatformFailsClear(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.publishers = map[string]Publisher{"youtube": &fakePublisher{writeOutput: true}}

	pubs := setupPublishChains(t, conn, t.TempDir(), 1)
	if _, err := conn.Exec(`UPDATE publications SET platform='meta' WHERE id=?`, pubs[0].ID); err != nil {
		t.Fatalf("update platform: %v", err)
	}

	job := &db.Job{Type: "publish", ReferenceID: pubs[0].ID, ReferenceType: "publications"}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(conn, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	err := w.executePublish(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error for platform without publisher")
	}
	if !strings.Contains(err.Error(), "meta") {
		t.Errorf("expected clear message mentioning platform, got: %v", err)
	}
}

// TestDiscoveryRoutesByPlatform: un source de plataforma 'kick' usa el
// discoverer registrado para kick (el fake), no el de twitch.
func TestDiscoveryRoutesByPlatform(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)

	// discoverers por plataforma: el fake devuelve clips según channelID
	w.discoverers = map[string]Discoverer{
		"twitch": &fakeDiscoverer{},
		"kick": &fakeDiscoverer{clips: map[string][]twitch.ClipInfo{
			"xokas": {{ID: "kickclip1", Title: "K", CreatedAt: time.Now().UTC()}},
		}},
	}

	// registrar el source kick
	src := &db.Source{Platform: "kick", ChannelID: "xokas", ChannelName: "xokas", Active: true}
	if err := db.UpsertSource(conn, src); err != nil {
		t.Fatalf("upsert source: %v", err)
	}

	job := &db.Job{Type: "discovery", ReferenceID: src.ID, ReferenceType: "sources"}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(conn, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	if err := w.executeDiscovery(context.Background(), *job); err != nil {
		t.Fatalf("executeDiscovery: %v", err)
	}

	// el clip de kick quedó registrado con platform='kick' y su download encolado
	sc, err := db.GetSourceClipByID(conn, scID(t, conn, "kickclip1"))
	if err != nil || sc == nil {
		t.Fatalf("get source clip: %v %v", err, sc)
	}
	if sc.Platform != "kick" || sc.PlatformClipID != "kickclip1" {
		t.Errorf("expected kick clip registered, got %+v", sc)
	}
}

// scID busca el ID de un source_clip por su platform_clip_id.
func scID(t *testing.T, conn *sql.DB, platformClipID string) int64 {
	t.Helper()
	var id int64
	if err := conn.QueryRow(`SELECT id FROM source_clips WHERE platform_clip_id=?`, platformClipID).Scan(&id); err != nil {
		t.Fatalf("find source_clip %s: %v", platformClipID, err)
	}
	return id
}

// TestDiscoveryUnknownPlatformFailsClear: source de plataforma sin discoverer
// falla con mensaje claro. V2 solo admite twitch/kick en el CHECK, así que
// usamos 'kick' sin discoverer registrado en el worker.
func TestDiscoveryUnknownPlatformFailsClear(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.discoverers = map[string]Discoverer{"twitch": &fakeDiscoverer{}}

	src := &db.Source{Platform: "kick", ChannelID: "x", ChannelName: "x", Active: true}
	if err := db.UpsertSource(conn, src); err != nil {
		t.Fatalf("upsert source: %v", err)
	}

	job := &db.Job{Type: "discovery", ReferenceID: src.ID, ReferenceType: "sources"}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := db.LockJob(conn, job.ID, w.cfg.WorkerID); err != nil {
		t.Fatalf("lock: %v", err)
	}

	err := w.executeDiscovery(context.Background(), *job)
	if err == nil {
		t.Fatal("expected error for platform without discoverer")
	}
	if !strings.Contains(err.Error(), "kick") {
		t.Errorf("expected clear message mentioning platform, got: %v", err)
	}
}

// TestDownloadRoutesByPlatform: un source_clip de plataforma 'kick' se descarga
// con el downloader registrado para kick.
func TestDownloadRoutesByPlatform(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)

	kickDl := &fakeDownloader{}
	w.downloaders = map[string]Downloader{
		"twitch": &fakeDownloader{},
		"kick":   kickDl,
	}

	// cadena source_clip 'kick' (la fila de source del test ya existe)
	var sourceID int64
	if err := conn.QueryRow(`SELECT id FROM sources WHERE channel_id='12345'`).Scan(&sourceID); err != nil {
		t.Fatalf("get source: %v", err)
	}
	sc := &db.SourceClip{Platform: "kick", PlatformClipID: "kickclip-dl", SourceID: sourceID, Status: "detected"}
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

	if err := w.executeDownload(context.Background(), *job); err != nil {
		t.Fatalf("executeDownload: %v", err)
	}
	if len(kickDl.downloads) != 1 || kickDl.downloads[0] != "kickclip-dl" {
		t.Errorf("expected kick downloader called once for kickclip-dl, got %v", kickDl.downloads)
	}
}

// TestPollWithNoPublishersIsNoop: sin publishers, el poll no falla (job done).
func TestPollWithNoPublishersIsNoop(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.publishers = map[string]Publisher{}
	if err := w.pollPublications(context.Background()); err != nil {
		t.Errorf("expected no-op poll, got: %v", err)
	}
}
