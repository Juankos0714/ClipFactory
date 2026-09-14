package ffmpeg

// Tests del procesador ffmpeg.
//
// Los tests REALES (proceso completo con ffmpeg instalado) se saltan si el
// binario no está disponible: así la suite corre igual en cualquier máquina,
// y dentro de la imagen Docker (que sí tiene ffmpeg) validan de punta a punta.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hasFFmpeg informa si ffmpeg/ffprobe están disponibles en el entorno.
func hasFFmpeg(t *testing.T) (string, string) {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg no está en PATH: saltando tests reales de procesamiento")
	}
	fp, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe no está en PATH: saltando tests reales de procesamiento")
	}
	return ff, fp
}

// generateTestVideo crea un video horizontal 1280x720 de 1 segundo con ffmpeg
// (patrón testsrc con audio), para tener una entrada realista y barata.
func generateTestVideo(t *testing.T, ffmpegPath, destPath string) {
	t.Helper()
	args := []string{
		"-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=1280x720:rate=15",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest",
		destPath,
	}
	cmd := exec.Command(ffmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generar video de prueba: %v: %s", err, strings.TrimSpace(string(out)))
	}
}

func TestProcessVideoValidations(t *testing.T) {
	p := NewProcessor()
	ctx := context.Background()

	if err := p.ProcessVideo(ctx, "", "/tmp/out.mp4"); err == nil {
		t.Error("expected error for empty srcPath")
	}
	if err := p.ProcessVideo(ctx, "/tmp/in.mp4", ""); err == nil {
		t.Error("expected error for empty destPath")
	}
	// fuente inexistente: error claro, no un fallo críptico de ffmpeg
	err := p.ProcessVideo(ctx, "/no/existe/in.mp4", filepath.Join(t.TempDir(), "out.mp4"))
	if err == nil {
		t.Error("expected error for missing source file")
	} else if !strings.Contains(err.Error(), "no encontrado") {
		t.Errorf("expected 'no encontrado' in error, got: %v", err)
	}
}

func TestProcessVideoIdempotentWhenOutputExists(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.mp4")
	// pre-crear la salida: debe ser no-op sin invocar ffmpeg
	if err := os.WriteFile(dest, []byte("ya procesado"), 0o600); err != nil {
		t.Fatalf("create dest: %v", err)
	}

	// binario inexistente: si intentara ejecutar ffmpeg, fallaría
	p := &Processor{FFmpegPath: "/no/existe/ffmpeg"}
	if err := p.ProcessVideo(context.Background(), filepath.Join(dir, "in.mp4"), dest); err != nil {
		t.Fatalf("expected idempotent no-op, got error: %v", err)
	}

	// la salida quedó intacta
	data, _ := os.ReadFile(dest)
	if string(data) != "ya procesado" {
		t.Error("expected existing output to be untouched")
	}
}

func TestProcessVideoMissingBinary(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mp4")
	if err := os.WriteFile(src, []byte("no es un video"), 0o600); err != nil {
		t.Fatalf("create src: %v", err)
	}

	p := &Processor{FFmpegPath: "/no/existe/ffmpeg"}
	err := p.ProcessVideo(context.Background(), src, filepath.Join(dir, "out.mp4"))
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if !strings.Contains(err.Error(), "proceso falló") {
		t.Errorf("expected 'proceso falló' in error, got: %v", err)
	}
}

func TestProcessVideoInvalidInputFile(t *testing.T) {
	ff, _ := hasFFmpeg(t)
	dir := t.TempDir()

	// archivo que NO es video: ffmpeg debe fallar y el error incluir el motivo
	src := filepath.Join(dir, "notavideo.mp4")
	if err := os.WriteFile(src, []byte("esto no es un mp4"), 0o600); err != nil {
		t.Fatalf("create fake src: %v", err)
	}

	p := &Processor{FFmpegPath: ff}
	err := p.ProcessVideo(context.Background(), src, filepath.Join(dir, "out.mp4"))
	if err == nil {
		t.Fatal("expected error processing non-video file, got nil")
	}
	if !strings.Contains(err.Error(), "proceso falló") {
		t.Errorf("expected 'proceso falló' in error, got: %v", err)
	}
	// no debe haber quedado salida
	if _, err := os.Stat(filepath.Join(dir, "out.mp4")); !os.IsNotExist(err) {
		t.Error("expected no output file after failed processing")
	}
}

// TestProcessVideoRealCropToVertical es EL test de aceptación del job process:
// genera un video horizontal real, lo procesa y verifica con ffprobe que la
// salida es exactamente 1080x1920.
func TestProcessVideoRealCropToVertical(t *testing.T) {
	ff, ffprobe := hasFFmpeg(t)
	dir := t.TempDir()

	src := filepath.Join(dir, "input.mp4")
	generateTestVideo(t, ff, src)

	// sanity: la entrada es 1280x720
	w, h, err := ProbeDimensions(context.Background(), ffprobe, src)
	if err != nil {
		t.Fatalf("probe input: %v", err)
	}
	if w != 1280 || h != 720 {
		t.Fatalf("input fixture: expected 1280x720, got %dx%d", w, h)
	}

	p := &Processor{FFmpegPath: ff}
	dest := filepath.Join(dir, "completed", "output.mp4")
	if err := p.ProcessVideo(context.Background(), src, dest); err != nil {
		t.Fatalf("process video: %v", err)
	}

	// la salida debe existir y ser exactamente 1080x1920
	w, h, err = ProbeDimensions(context.Background(), ffprobe, dest)
	if err != nil {
		t.Fatalf("probe output: %v", err)
	}
	if w != Width || h != Height {
		t.Errorf("expected output %dx%d, got %dx%d", Width, Height, w, h)
	}

	// no queda .part
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Errorf("expected .part to be gone, stat err: %v", err)
	}

	// idempotencia con salida real: segunda pasada es no-op y no corrompe
	before, _ := os.Stat(dest)
	if err := p.ProcessVideo(context.Background(), src, dest); err != nil {
		t.Fatalf("second process (should be no-op): %v", err)
	}
	after, _ := os.Stat(dest)
	if before.Size() != after.Size() {
		t.Error("expected output file untouched on second pass")
	}
}

func TestProcessVideoContextCancelled(t *testing.T) {
	ff, _ := hasFFmpeg(t)
	dir := t.TempDir()

	src := filepath.Join(dir, "input.mp4")
	generateTestVideo(t, ff, src)

	p := &Processor{FFmpegPath: ff}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelar antes de empezar

	err := p.ProcessVideo(ctx, src, filepath.Join(dir, "out.mp4"))
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
}
