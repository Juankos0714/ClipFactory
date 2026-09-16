package worker

// TEST E2E DEL PIPELINE COMPLETO (system test):
//
//	discovery → download → process → thumbnail → publish
//
// A diferencia de los tests por-fase (que usan fakes para las piezas vecinas),
// este corre el worker REAL — Start/loop/processJobs/executeJob, auto-discovery
// incluido — sobre una DB real y con ffmpeg REAL para las fases de proceso y
// thumbnail. Solo la descarga es un fake (escribe un MP4 chico de verdad con
// ffmpeg: probar el downloader de Twitch requeriría red y credenciales) y el
// publisher es un fake en memoria.
//
// Verifica el CONTRACTO DE EXTREMO A EXTREMO:
//   - el auto-discovery encola el job discovery solo
//   - la cadena de jobs se dispara sola (cada fase encola la siguiente)
//   - los estados de DB quedan consistentes en cada tabla
//   - el archivo procesado existe, es 1080x1920 y tiene thumbnail en disco
//   - la publication queda 'published' con external_id/URL
//
// Se salta con aviso si ffmpeg/ffprobe no están en PATH (mismo criterio que
// los tests de adapter/ffmpeg).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/adapter/ffmpeg"
	"github.com/juankos0714/clipfactory/internal/adapter/twitch"
	"github.com/juankos0714/clipfactory/internal/db"
)

// ffOrSkip se asegura de que ffmpeg/ffprobe estén disponibles.
func ffOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg no está en PATH: saltando test E2E del pipeline")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe no está en PATH: saltando test E2E del pipeline")
	}
}

// e2eDownloader simula la descarga: genera un MP4 REAL chico con ffmpeg
// (testsrc 640x360, 2s con audio silencioso) en destPath, imitando el
// contrato de los downloaders reales (archivo atómico en destino).
type e2eDownloader struct {
	ffmpegPath string
	downloads  []string
}

func (d *e2eDownloader) DownloadClip(ctx context.Context, clipID string, destPath string) error {
	d.downloads = append(d.downloads, clipID)
	if dir := filepath.Dir(destPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	// #nosec G204 — binario verificado con LookPath en el setup del test
	cmd := exec.CommandContext(ctx, d.ffmpegPath, "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=640x360:rate=15",
		"-f", "lavfi", "-i", "anullsrc=duration=2:sample_rate=44100",
		"-shortest", "-pix_fmt", "yuv420p", "-f", "mp4", destPath,
	)
	if _, err := cmd.CombinedOutput(); err != nil {
		return err
	}
	info, err := os.Stat(destPath)
	if err != nil || info.Size() == 0 {
		return err
	}
	return nil
}

// e2ePublisher graba las subidas y devuelve IDs/URLs fijos.
type e2ePublisher struct {
	uploads []string
}

func (p *e2ePublisher) UploadVideo(ctx context.Context, videoPath, title, description string, tags []string) (string, string, error) {
	p.uploads = append(p.uploads, filepath.Base(videoPath))
	return "e2e-video-id", "https://youtube.com/watch?v=e2e-video-id", nil
}

// waitForCondition sondea hasta que cond sea true o venza el deadline.
func waitForCondition(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout esperando: %s", what)
}

// TestSystemPipelineEndToEnd: el pipeline COMPLETO corre solo desde el
// arranque del worker, con ffmpeg real para process/thumbnail.
func TestSystemPipelineEndToEnd(t *testing.T) {
	ffOrSkip(t)

	conn := openTestDB(t) // trae un source twitch activo (channel_id '12345')
	tmp := t.TempDir()

	ffPath, _ := exec.LookPath("ffmpeg")

	// --- piezas: ffmpeg real, download/publish fake, DB real ---
	proc := ffmpeg.NewProcessor()
	proc.SetFFmpegPath(ffPath)

	dl := &e2eDownloader{ffmpegPath: ffPath}
	pub := &e2ePublisher{}

	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		WorkerID:          "e2e-worker",
		MaxConcurrentJobs: 1, // determinista: una fase por vez
		PollInterval:      25 * time.Millisecond,
		DataDir:           tmp,
		Processor:         proc,
		Thumbnailer:       proc,
		Downloaders:       map[string]Downloader{"twitch": dl},
		Publishers:        map[string]Publisher{"youtube": pub},
		DiscoverOnStart:   true,
		// auto-encolado de publications cada tick corto: tras el thumbnail,
		// el operador crea la publication; el poll la re-encola solo
		PollPublicationsInterval: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	// un clip "detectado" en el canal: el discovery del E2E usa un
	// discoverer fake (la API de Twitch real requeriría credenciales)
	w.discoverers = map[string]Discoverer{
		"twitch": &fakeDiscoverer{clips: map[string][]twitch.ClipInfo{
			"12345": {clipInfo("e2e-clip-1", 2.0, time.Now().UTC())},
		}},
	}

	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// --- FASE 1-3: discovery → download → process → thumbnail ---
	// el thumbnail deja clips.thumbnail_path apuntando al JPEG final
	waitForCondition(t, 30*time.Second, "clip con thumbnail generado", func() bool {
		var n int
		err := conn.QueryRow(
			`SELECT COUNT(*) FROM clips c WHERE c.thumbnail_path != '' AND c.status='completed'`,
		).Scan(&n)
		return err == nil && n > 0
	})
	t.Log("fases discovery→download→process→thumbnail completadas")

	// --- verificación de estados intermedios ---
	var videoStatus, clipStatus string
	var clipID, thumbPath, clipFile string
	if err := conn.QueryRow(
		`SELECT v.status, c.status, c.id, c.thumbnail_path, c.filepath
		 FROM clips c JOIN videos v ON v.id = c.video_id`,
	).Scan(&videoStatus, &clipStatus, &clipID, &thumbPath, &clipFile); err != nil {
		t.Fatalf("verificar video/clip: %v", err)
	}
	if videoStatus != "completed" || clipStatus != "completed" {
		t.Errorf("estados: video=%q clip=%q (quería completed/completed)", videoStatus, clipStatus)
	}

	// --- FASE 4: el operador crea la publication (flujo real: CLI/SQL) ---
	if _, err := conn.Exec(
		`INSERT INTO publications (clip_id, platform, status, created_at, updated_at)
		 VALUES (?, 'youtube', 'pending', ?, ?)`,
		clipID, db.NowUTC(), db.NowUTC(),
	); err != nil {
		t.Fatalf("insert publication: %v", err)
	}

	// el poll_publications debe re-encolar y el worker publicar
	waitForCondition(t, 15*time.Second, "publication 'published' por el poll", func() bool {
		var n int
		err := conn.QueryRow(
			`SELECT COUNT(*) FROM publications WHERE status='published' AND external_id != ''`,
		).Scan(&n)
		return err == nil && n > 0
	})

	// --- apagar y verificar el estado FINAL completo ---
	w.Stop()

	// 1. cadenas de jobs completas: discovery/download/process/thumbnail/publish done
	var doneCount int
	if err := conn.QueryRow(
		`SELECT COUNT(DISTINCT type) FROM jobs WHERE type IN ('discovery','download','process','thumbnail','publish') AND status='done'`,
	).Scan(&doneCount); err != nil {
		t.Fatalf("count jobs done: %v", err)
	}
	if doneCount != 5 {
		t.Errorf("esperaba 5 tipos de job 'done', hay %d", doneCount)
	}

	// 2. source_clip y video en estado final correcto
	var scStatus string
	if err := conn.QueryRow(`SELECT status FROM source_clips WHERE platform_clip_id='e2e-clip-1'`).Scan(&scStatus); err != nil {
		t.Fatalf("source_clip: %v", err)
	}
	if scStatus != "downloaded" {
		t.Errorf("source_clip status: esperaba 'downloaded', hay %q", scStatus)
	}

	// 3. el archivo procesado existe, es MP4 y 1080x1920 (ffprobe real)
	if _, err := os.Stat(clipFile); err != nil {
		t.Fatalf("el clip procesado no existe en disco: %s", clipFile)
	}
	if filepath.Ext(clipFile) != ".mp4" {
		t.Errorf("extensión inesperada: %s", clipFile)
	}
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=p=0", clipFile).Output()
	if err != nil {
		t.Fatalf("ffprobe sobre el clip: %v", err)
	}
	if got := string(out); got != "1080,1920\n" {
		t.Errorf("dimensiones del clip: esperaba 1080,1920, ffprobe dijo %q", got)
	}

	// 4. el thumbnail existe en disco y el registro apunta a él
	if _, err := os.Stat(thumbPath); err != nil {
		t.Errorf("el thumbnail no existe en disco: %s", thumbPath)
	}

	// 5. la publication quedó con los datos del publisher fake
	var extID, extURL string
	if err := conn.QueryRow(
		`SELECT external_id, external_url FROM publications WHERE status='published'`,
	).Scan(&extID, &extURL); err != nil {
		t.Fatalf("publication: %v", err)
	}
	if extID != "e2e-video-id" || extURL != "https://youtube.com/watch?v=e2e-video-id" {
		t.Errorf("datos externos: id=%q url=%q", extID, extURL)
	}
	if len(pub.uploads) != 1 {
		t.Errorf("publisher llamado %d veces (esperaba 1: idempotencia)", len(pub.uploads))
	}

	// 6. integridad referencial de toda la cadena (FK activadas)
	var orphans int
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM source_clips sc
		 LEFT JOIN sources s ON s.id = sc.source_id
		 WHERE s.id IS NULL`,
	).Scan(&orphans); err != nil || orphans != 0 {
		t.Errorf("source_clips huérfanos: %d (%v)", orphans, err)
	}
}
