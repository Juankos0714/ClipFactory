package kick

// Adaptador de Kick para ClipFactory: plataforma de ORIGEN de clips (igual
// papel que Twitch, destino final: YouTube/Meta).
//
// Piezas:
//
//  1. Descubrimiento: la API pública no-oficial de kick.com
//     (https://kick.com/api/v2/channels/{slug}/clips) devuelve los clips más
//     recientes de un canal en JSON con clave "clips" (no "data"). No requiere
//     autenticación (endpoints públicos) pero kick.com suele bloquear clientes
//     sin User-Agent de navegador: se manda una con headers realistas.
//
//  2. Descarga: la respuesta incluye clip.video_url, una URL de playlist HLS
//     (.m3u8) en clips.kick.com. Se baja con ffmpeg (ya instalado en el
//     contenedor) que remuxe a MP4: ffmpeg -i <m3u8> -c copy output.mp4.
//     No hace falta TwitchDownloaderCLI para Kick.
//
// ESTABILIDAD: la API de kick.com NO es oficial ni documentada; puede cambiar
// sin aviso. Todo endpoint/URL es sobrescribible (SetBaseURL)
// para tests y para mover el base si cambia el CDN.
//
// channel_id para Kick = slug del canal (ej: "illojuan" de kick.com/illojuan).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/juankos0714/clipfactory/internal/adapter/twitch" // twitch.ClipInfo: estructura neutra de metadata
)

// Endpoints por defecto (sobrescribibles para tests).
const (
	DefaultBaseURL = "https://kick.com"
)

// userAgent realista: kick.com bloquea clientes que no parecen navegador.
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// KickAdapter implementa la misma superficie que TwitchAdapter
// (ListClips / DownloadClip / ValidateCredentials) para Kick.
type KickAdapter struct {
	HTTPClient *http.Client
	BaseURL    string // API pública de kick.com (sobrescribible en tests)
}

// NewKickAdapter crea un adaptador de Kick con defaults de producción.
func NewKickAdapter() *KickAdapter {
	return &KickAdapter{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		BaseURL:    DefaultBaseURL,
	}
}

// SetBaseURL configura la URL de la API (tests).
func (a *KickAdapter) SetBaseURL(u string) { a.BaseURL = u }

// ValidateCredentials verifica la configuración mínima (Kick no requiere
// credenciales: los endpoints de clips son públicos).
func (a *KickAdapter) ValidateCredentials() error {
	if a.HTTPClient == nil {
		return fmt.Errorf("kick: HTTPClient no configurado")
	}
	return nil
}

// kickClip es un clip individual de la respuesta de /api/v2/channels/{slug}/clips.
// Solo se declaran los campos que ClipFactory usa. La API devuelve clave "clips"
// (no "data") y el video es una playlist HLS (.m3u8), no un MP4 directo.
type kickClip struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Duration     float64 `json:"duration"`   // segundos
	CreatedAt    string  `json:"created_at"` // formato RFC3339 con offset ("2026-10-24T03:46:22.964793Z")
	ThumbnailURL string  `json:"thumbnail_url"`
	VideoURL     string  `json:"video_url"`   // playlist HLS (.m3u8) en clips.kick.com
	ViewCount    int64   `json:"view_count"`  // cantidad de visualizaciones
	Channel      struct {
		ID       int64  `json:"id"`
		Slug     string `json:"slug"`
		Username string `json:"username"`
	} `json:"channel"`
}

// toClipInfo mapea un clip de Kick a la estructura neutra de ClipFactory
// (twitch.ClipInfo — el worker trabaja con ese tipo, plataforma-agnóstico).
func (k kickClip) toClipInfo() (twitch.ClipInfo, error) {
	if k.ID == "" {
		return twitch.ClipInfo{}, fmt.Errorf("kick: clip sin id")
	}

	createdAt, err := time.Parse(time.RFC3339, k.CreatedAt)
	if err != nil {
		return twitch.ClipInfo{}, fmt.Errorf("kick: clip %s: parse created_at %q: %w", k.ID, k.CreatedAt, err)
	}

	return twitch.ClipInfo{
		ID:           k.ID,
		Title:        k.Title,
		DurationSec:  k.Duration,
		CreatedAt:    createdAt.UTC(),
		ThumbnailURL: k.ThumbnailURL,
		VideoURL:     k.VideoURL, // URL HLS (playlist .m3u8); DownloadClip la usa con ffmpeg
		ChannelID:    k.Channel.Slug,
		ChannelName:  k.Channel.Username,
	}, nil
}

// clipsResponse es la envolvente de la lista de clips.
// La API usa "clips" (no "data").
type clipsResponse struct {
	Clips []kickClip `json:"clips"`
}

// ListClips lista los clips más recientes de un canal de Kick.
//
// GET {BaseURL}/api/v2/channels/{channelID}/clips, con channelID = slug del
// canal. afterTimestamp filtra localmente los clips creados después de esa
// marca (la API no soporta el filtro server-side); maxPages se acepta por
// compatibilidad con la interface pero la lista de Kick es de una sola página.
//
// El filtro temporal replica la semántica del discovery de Twitch: solo clips
// nuevos entran al pipeline (UpsertSourceClip es no-op para existentes, pero
// filtrar acá evita procesar el listado completo en cada pasada).
func (a *KickAdapter) ListClips(ctx context.Context, channelID string, afterTimestamp time.Time, maxPages ...int) ([]twitch.ClipInfo, error) {
	if err := a.ValidateCredentials(); err != nil {
		return nil, err
	}
	if channelID == "" {
		return nil, fmt.Errorf("kick: channelID (slug) vacío")
	}

	endpoint := fmt.Sprintf("%s/api/v2/channels/%s/clips", a.BaseURL, url.PathEscape(channelID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("kick: create request: %w", err)
	}
	// headers de navegador: kick.com rechaza clientes "desconocidos"
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kick: api request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("kick: canal %q no encontrado (¿slug correcto?)", channelID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kick: api returned status %d", resp.StatusCode)
	}

	var parsed clipsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("kick: decode response: %w", err)
	}

	var clips []twitch.ClipInfo
	for _, kc := range parsed.Clips {
		clip, err := kc.toClipInfo()
		if err != nil {
			return nil, err
		}
		// filtro local: solo clips creados después de la última revisión
		if !afterTimestamp.IsZero() && !clip.CreatedAt.After(afterTimestamp) {
			continue
		}
		clips = append(clips, clip)
	}
	return clips, nil
}

// DownloadClip descarga el clip de Kick al destino indicado.
//
// clipID es el ID del clip (campo "id" de la API). La URL del video se
// resuelve en dos pasos:
//  1. GET /api/v2/clips/{clipID} → video_url (playlist HLS .m3u8)
//  2. ffmpeg -i <m3u8> -c copy -movflags +faststart output.mp4
//
// El archivo se baja a .part y se renombra al final (atomicidad).
// Se requiere ffmpeg instalado en el sistema (ya incluido en la imagen Docker).
func (a *KickAdapter) DownloadClip(ctx context.Context, clipID string, destPath string) error {
	if clipID == "" {
		return fmt.Errorf("kick: clipID vacío")
	}
	if destPath == "" {
		return fmt.Errorf("kick: destPath vacío")
	}

	// paso 1: resolver el video_url del clip (playlist HLS)
	endpoint := fmt.Sprintf("%s/api/v2/clips/%s", a.BaseURL, url.PathEscape(clipID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("kick: create clip request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("kick: clip request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kick: clip request devolvió %d para %s", resp.StatusCode, clipID)
	}

	var detail kickClip
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return fmt.Errorf("kick: decode clip response: %w", err)
	}
	if detail.VideoURL == "" {
		return fmt.Errorf("kick: clip %s sin video_url", clipID)
	}

	// asegurar el directorio destino antes de que ffmpeg escriba
	if dir := filepath.Dir(destPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("kick: create dest dir: %w", err)
		}
	}

	// paso 2: descargar HLS y remuxear a MP4 con ffmpeg (escrito a .part para atomicidad)
	tmpPath := destPath + ".part"
	defer os.Remove(tmpPath) // no-op si el rename fue exitoso

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", detail.VideoURL,
		"-c", "copy",
		"-movflags", "+faststart",
		"-y", // sobrescribir si existe
		tmpPath,
	)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kick: ffmpeg descarga falló para %s: %w", clipID, err)
	}

	// verificar que el archivo no esté vacío
	info, err := os.Stat(tmpPath)
	if err != nil {
		return fmt.Errorf("kick: stat archivo descargado: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("kick: archivo vacío para clip %s", clipID)
	}

	// renombrar atómicamente
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("kick: rename %s → %s: %w", tmpPath, destPath, err)
	}
	return nil
}
