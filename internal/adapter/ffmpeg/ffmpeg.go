// Package ffmpeg implementa el procesamiento de video para ClipFactory usando
// el binario ffmpeg (recorte a formato vertical 1080x1920 para Shorts/TikTok).
//
// ESTRATEGIA DE RECORTE (crop central + escala):
//
//	El video horizontal (16:9) se recorta al centro al ratio 9:16 y se escala a
//	1080x1920. Un clip de Twitch de 1920x1080 queda:
//
//	  alto de destino 1920 → el crop debe ser 1080 de ancho en el original
//	  (1920 * 9/16 = 1080) → crop=1080:1920 centrado → scale a 1080x1920 (ya es 1:1)
//
//	  Para entradas de otras resoluciones el filtro calcula el crop dinámico:
//	  crop='min(iw,ih*9/16)':ih' (ancho = alto * 9/16, centrado), luego scale=1080:1920.
//
//	Alternativa considerada y descartada: letterbox (barras negras laterales con
//	pad). El crop central pierde los bordes pero llena la pantalla completa, que
//	es lo esperado en Shorts/TikTok (ver docs/guia-twitch.md §10 para el caso
//	de clips "wide").
//
// ATOMICIDAD: ffmpeg escribe a un .part y se renombra al final, igual que la
// descarga: nunca queda un mp4 corrupto con nombre final.
//
// HARDWARE: el filtro usa solo libx264 (software). VAAPI (iGPU) queda como
// mejora futura; en el i3-3220 el fallback software es el camino normal.
package ffmpeg

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Width y Height son las dimensiones de salida obligatorias del pipeline.
const (
	Width  = 1080
	Height = 1920
)

// Processor recorta videos al formato vertical del pipeline.
type Processor struct {
	// FFmpegPath es la ruta al binario (default "ffmpeg": en PATH).
	FFmpegPath string
}

// NewProcessor crea un Processor con el binario por defecto (PATH).
func NewProcessor() *Processor {
	return &Processor{FFmpegPath: "ffmpeg"}
}

// SetFFmpegPath configura la ruta al binario ffmpeg (útil para tests).
func (p *Processor) SetFFmpegPath(path string) { p.FFmpegPath = path }

// ProcessVideo recorta srcPath a 1080x1920 (crop central 9:16) y escribe el
// resultado en destPath. El clip se procesa completo (los start/end del schema
// quedan para la fase de subdivisión en múltiples clips, aún no implementada).
//
// Contrato:
//   - crea el directorio destino si no existe (igual que TwitchAdapter.DownloadClip)
//   - escribe a destPath+".part" y renombra al final (atomicidad)
//   - no sobrescribe: si destPath ya existe es no-op (idempotencia del job process)
//   - el archivo de salida nunca lleva audio re-codificado innecesariamente (copy)
func (p *Processor) ProcessVideo(ctx context.Context, srcPath, destPath string) error {
	if srcPath == "" {
		return fmt.Errorf("ffmpeg: srcPath vacío")
	}
	if destPath == "" {
		return fmt.Errorf("ffmpeg: destPath vacío")
	}

	// idempotencia: si ya fue procesado, no-op (el job se re-encoló tras un crash)
	if _, err := os.Stat(destPath); err == nil {
		log.Printf("[ffmpeg] %s ya existe, no-op", destPath)
		return nil
	}

	// verificar que la entrada exista ANTES de invocar ffmpeg (error claro)
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("ffmpeg: video fuente no encontrado %s: %w", srcPath, err)
	}

	// asegurar el directorio destino (data/completed puede no existir aún)
	if dir := filepath.Dir(destPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dest dir: %w", err)
		}
	}

	tmpPath := destPath + ".part"
	defer os.Remove(tmpPath) // no-op si el rename fue exitoso

	// cadena de filtros:
	//   1. crop=min(iw\,ih*9/16):ih  → recorte central al ratio 9:16
	//      (min por si la entrada ya es vertical; la coma se escapa con \,)
	//   2. scale=1080:1920           → tamaño final exacto
	//   3. setsar=1                  → aspect ratio cuadrado (evita distorsión)
	filters := fmt.Sprintf("crop='min(iw,ih*%d/%d)':ih,scale=%d:%d,setsar=1",
		Width, Height, Width, Height)

	// flags:
	//   -y             → sobrescribir el .part (nuestro tmp, seguro)
	//   -movflags +faststart → moov al inicio (streaming inmediato en YouTube)
	//   -preset veryfast → trade-off CPU/calidad adecuado para el i3-3220
	//   -crf 23        → calidad visualmente transparente para clips cortos
	//   -c:a copy      → el audio no se toca (el crop no lo afecta)
	//   -f mp4         → OBLIGATORIO: el .part no tiene extensión reconocible y
	//                    ffmpeg no puede inferir el formato de salida del nombre
	args := []string{
		"-y",
		"-i", srcPath,
		"-vf", filters,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-c:a", "copy",
		"-movflags", "+faststart",
		"-f", "mp4",
		tmpPath,
	}

	// #nosec G204 — la ruta del binario es configurable por el operador
	cmd := exec.CommandContext(ctx, p.FFmpegPath, args...)
	var out strings.Builder
	cmd.Stderr = &out
	cmd.Stdout = &out

	log.Printf("[ffmpeg] processing %s → %s (%dx%d)", srcPath, destPath, Width, Height)
	if err := cmd.Run(); err != nil {
		msg := out.String()
		if len(msg) > 800 {
			msg = msg[len(msg)-800:] // el error real está al final del log de ffmpeg
		}
		return fmt.Errorf("ffmpeg: proceso falló para %s: %v: %s", srcPath, err, strings.TrimSpace(msg))
	}

	// verificar que la salida exista y tenga contenido
	info, err := os.Stat(tmpPath)
	if err != nil {
		return fmt.Errorf("ffmpeg: no produjo salida en %s: %w", tmpPath, err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("ffmpeg: salida vacía para %s", srcPath)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("ffmpeg: rename %s → %s: %w", tmpPath, destPath, err)
	}

	log.Printf("[ffmpeg] video procesado (%d bytes) → %s", info.Size(), destPath)
	return nil
}

// GenerateThumbnail extrae un frame de videoPath y lo guarda como JPEG en
// thumbPath, para las vistas previas de clips en YouTube/TikTok/Meta.
//
// Contrato (igual que ProcessVideo):
//   - crea el directorio destino si no existe
//   - escribe a thumbPath+".part" y renombra al final (atomicidad)
//   - no sobrescribe: si thumbPath ya existe es no-op (idempotencia del job)
//   - el frame se toma en atSec (default 0.5s para evitar frames negros del inicio)
func (p *Processor) GenerateThumbnail(ctx context.Context, videoPath, thumbPath string, atSec float64) error {
	if videoPath == "" {
		return fmt.Errorf("ffmpeg: videoPath vacío")
	}
	if thumbPath == "" {
		return fmt.Errorf("ffmpeg: thumbPath vacío")
	}

	// idempotencia: si ya existe, no-op (job re-encolado tras un crash)
	if _, err := os.Stat(thumbPath); err == nil {
		log.Printf("[ffmpeg] thumbnail %s ya existe, no-op", thumbPath)
		return nil
	}

	if _, err := os.Stat(videoPath); err != nil {
		return fmt.Errorf("ffmpeg: video no encontrado %s: %w", videoPath, err)
	}

	if dir := filepath.Dir(thumbPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create thumb dir: %w", err)
		}
	}

	// atSec negativo o cero: usar 0.5s (el frame 0 de Twitch suele ser negro)
	if atSec <= 0 {
		atSec = 0.5
	}

	tmpPath := thumbPath + ".part"
	defer os.Remove(tmpPath) // no-op si el rename fue exitoso

	// flags:
	//   -ss antes de -i → seek rápido (no decodifica desde el inicio)
	//   -frames:v 1     → un solo frame
	//   -q:v 2          → calidad JPEG alta (escala 2-31, menor es mejor)
	//   -f image2       → OBLIGATORIO: el .part no tiene extensión reconocible
	args := []string{
		"-y",
		"-ss", strconv.FormatFloat(atSec, 'f', 2, 64),
		"-i", videoPath,
		"-frames:v", "1",
		"-q:v", "2",
		"-f", "image2",
		tmpPath,
	}

	// #nosec G204 — la ruta del binario es configurable por el operador
	cmd := exec.CommandContext(ctx, p.FFmpegPath, args...)
	var out strings.Builder
	cmd.Stderr = &out
	cmd.Stdout = &out

	log.Printf("[ffmpeg] generating thumbnail %s → %s (t=%.2fs)", videoPath, thumbPath, atSec)
	if err := cmd.Run(); err != nil {
		msg := out.String()
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return fmt.Errorf("ffmpeg: thumbnail falló para %s: %v: %s", videoPath, err, strings.TrimSpace(msg))
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		return fmt.Errorf("ffmpeg: no produjo thumbnail en %s: %w", tmpPath, err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("ffmpeg: thumbnail vacío para %s", videoPath)
	}

	if err := os.Rename(tmpPath, thumbPath); err != nil {
		return fmt.Errorf("ffmpeg: rename %s → %s: %w", tmpPath, thumbPath, err)
	}

	log.Printf("[ffmpeg] thumbnail generado (%d bytes) → %s", info.Size(), thumbPath)
	return nil
}

// ProbeDimensions devuelve el ancho y alto reales de un archivo de video,
// usando ffprobe (mismo paquete que ffmpeg). Lo usan los tests para verificar
// que la salida quedó exactamente en 1080x1920.
func ProbeDimensions(ctx context.Context, ffprobePath, videoPath string) (int, int, error) {
	// -v error: log limpio; -show_entries stream=width,height: primera stream de video
	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0:s=x",
		videoPath,
	}
	// #nosec G204 — ruta configurable por el caller (tests)
	cmd := exec.CommandContext(ctx, ffprobePath, args...)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe %s: %w", videoPath, err)
	}
	// salida esperada: "1080x1920"
	parts := strings.SplitN(strings.TrimSpace(string(out)), "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("ffprobe: salida inesperada %q", string(out))
	}
	w, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe: width inválido en %q: %w", string(out), err)
	}
	h, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe: height inválido en %q: %w", string(out), err)
	}
	return w, h, nil
}
