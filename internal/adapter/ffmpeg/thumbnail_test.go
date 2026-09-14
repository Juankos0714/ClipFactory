package ffmpeg

// Tests de GenerateThumbnail (extracción de frames con ffmpeg).
//
// Igual que en ffmpeg_test.go: los tests reales se saltan si no hay ffmpeg;
// dentro de la imagen Docker validan de punta a punta.

import (
	"context"
	"image"
	_ "image/jpeg" // registrar el decoder JPEG para image.DecodeConfig
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateThumbnailValidations(t *testing.T) {
	p := NewProcessor()
	ctx := context.Background()

	if err := p.GenerateThumbnail(ctx, "", "/tmp/t.jpg", 0); err == nil {
		t.Error("expected error for empty videoPath")
	}
	if err := p.GenerateThumbnail(ctx, "/tmp/v.mp4", "", 0); err == nil {
		t.Error("expected error for empty thumbPath")
	}
	err := p.GenerateThumbnail(ctx, "/no/existe.mp4", filepath.Join(t.TempDir(), "t.jpg"), 0)
	if err == nil {
		t.Error("expected error for missing video")
	} else if !strings.Contains(err.Error(), "no encontrado") {
		t.Errorf("expected 'no encontrado' in error, got: %v", err)
	}
}

func TestGenerateThumbnailIdempotentWhenExists(t *testing.T) {
	dir := t.TempDir()
	thumb := filepath.Join(dir, "thumb.jpg")
	if err := os.WriteFile(thumb, []byte("ya existe"), 0o600); err != nil {
		t.Fatalf("create thumb: %v", err)
	}

	// binario inexistente: si intentara ejecutar ffmpeg, fallaría
	p := &Processor{FFmpegPath: "/no/existe/ffmpeg"}
	if err := p.GenerateThumbnail(context.Background(), "/cualquier/v.mp4", thumb, 0); err != nil {
		t.Fatalf("expected idempotent no-op, got error: %v", err)
	}
	data, _ := os.ReadFile(thumb)
	if string(data) != "ya existe" {
		t.Error("expected existing thumbnail to be untouched")
	}
}

func TestGenerateThumbnailMissingBinary(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(video, []byte("no es video"), 0o600); err != nil {
		t.Fatalf("create video: %v", err)
	}

	p := &Processor{FFmpegPath: "/no/existe/ffmpeg"}
	err := p.GenerateThumbnail(context.Background(), video, filepath.Join(dir, "t.jpg"), 0)
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	if !strings.Contains(err.Error(), "thumbnail falló") {
		t.Errorf("expected 'thumbnail falló' in error, got: %v", err)
	}
}

// TestGenerateThumbnailReal es el test de aceptación: genera un video real,
// extrae el frame y verifica que el JPEG resultante sea válido y de 1080x1920.
func TestGenerateThumbnailReal(t *testing.T) {
	ff, _ := hasFFmpeg(t)
	dir := t.TempDir()

	// video de entrada: el mismo fixture vertical que produce ProcessVideo
	src := filepath.Join(dir, "input.mp4")
	generateTestVideo(t, ff, src)

	p := &Processor{FFmpegPath: ff}

	// primero producir un clip vertical real con ProcessVideo
	clip := filepath.Join(dir, "completed", "output.mp4")
	if err := p.ProcessVideo(context.Background(), src, clip); err != nil {
		t.Fatalf("process video (fixture): %v", err)
	}

	// extraer thumbnail del clip
	thumb := filepath.Join(dir, "thumbnails", "output.jpg")
	if err := p.GenerateThumbnail(context.Background(), clip, thumb, 0.5); err != nil {
		t.Fatalf("generate thumbnail: %v", err)
	}

	// el archivo debe ser un JPEG válido de 1080x1920
	f, err := os.Open(thumb)
	if err != nil {
		t.Fatalf("open thumbnail: %v", err)
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("thumbnail no es una imagen válida: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("expected jpeg format, got %s", format)
	}
	if cfg.Width != Width || cfg.Height != Height {
		t.Errorf("expected thumbnail %dx%d, got %dx%d", Width, Height, cfg.Width, cfg.Height)
	}

	// no queda .part
	if _, err := os.Stat(thumb + ".part"); !os.IsNotExist(err) {
		t.Errorf("expected .part to be gone, stat err: %v", err)
	}

	// idempotencia con salida real: segunda pasada no-op y no corrompe
	before, _ := os.Stat(thumb)
	if err := p.GenerateThumbnail(context.Background(), clip, thumb, 0.5); err != nil {
		t.Fatalf("second pass (should be no-op): %v", err)
	}
	after, _ := os.Stat(thumb)
	if before.Size() != after.Size() {
		t.Error("expected thumbnail untouched on second pass")
	}
}

// TestGenerateThumbnailRealShortClip verifica el caso límite de clips muy cortos
// (los de Twitch duran 5-60s, pero el seek 0.5s debe funcionar incluso en 1s).
func TestGenerateThumbnailRealShortClip(t *testing.T) {
	ff, _ := hasFFmpeg(t)
	dir := t.TempDir()

	// video de 1 segundo: el default atSec=0.5 cae dentro de la duración
	src := filepath.Join(dir, "short.mp4")
	generateTestVideo(t, ff, src)

	p := &Processor{FFmpegPath: ff}
	thumb := filepath.Join(dir, "thumb.jpg")
	if err := p.GenerateThumbnail(context.Background(), src, thumb, 0); err != nil {
		t.Fatalf("generate thumbnail from 1s clip: %v", err)
	}

	if _, err := os.Stat(thumb); err != nil {
		t.Errorf("expected thumbnail file: %v", err)
	}
}
