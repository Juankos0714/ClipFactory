package kick

// Adaptador de Kick para ClipFactory: plataforma de ORIGEN de clips (igual
// papel que Twitch, destino final: YouTube/Meta).
//
// Piezas:
//
//  1. Descubrimiento: la API pública no-oficial de kick.com
//     (https://kick.com/api/v2/channels/{slug}/clips) devuelve los clips más
//     recientes de un canal en JSON. No requiere autenticación (endpoints
//     públicos) pero kick.com suele bloquear clientes sin User-Agent de
//     navegador: se manda una con headers realistas.
//
//  2. Descarga: la respuesta incluye clip.video_filename, la ruta relativa del
//     MP4 en el CDN (https://files.kick.com/...). Se baja con HTTP directo —
//     no hace falta TwitchDownloaderCLI para Kick.
//
// ESTABILIDAD: la API de kick.com NO es oficial ni documentada; puede cambiar
// sin aviso. Todo endpoint/URL es sobrescribible (SetBaseURL / SetFilesURL)
// para tests y para mover el base si cambia el CDN.
//
// channel_id para Kick = slug del canal (ej: "illojuan" de kick.com/illojuan).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/juankos0714/clipfactory/internal/adapter/twitch" // twitch.ClipInfo: estructura neutra de metadata
)

// Endpoints por defecto (sobrescribibles para tests).
const (
	DefaultBaseURL  = "https://kick.com"
	DefaultFilesURL = "https://files.kick.com"
)

// userAgent realista: kick.com bloquea clientes que no parecen navegador.
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// KickAdapter implementa la misma superficie que TwitchAdapter
// (ListClips / DownloadClip / ValidateCredentials) para Kick.
type KickAdapter struct {
	HTTPClient *http.Client
	BaseURL    string // API pública de kick.com (sobrescribible en tests)
	FilesURL   string // CDN de archivos (sobrescribible en tests)
}

// NewKickAdapter crea un adaptador de Kick con defaults de producción.
func NewKickAdapter() *KickAdapter {
	return &KickAdapter{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		BaseURL:    DefaultBaseURL,
		FilesURL:   DefaultFilesURL,
	}
}

// SetBaseURL configura la URL de la API (tests).
func (a *KickAdapter) SetBaseURL(u string) { a.BaseURL = u }

// SetFilesURL configura la URL del CDN de archivos (tests).
func (a *KickAdapter) SetFilesURL(u string) { a.FilesURL = u }

// ValidateCredentials verifica la configuración mínima (Kick no requiere
// credenciales: los endpoints de clips son públicos).
func (a *KickAdapter) ValidateCredentials() error {
	if a.HTTPClient == nil {
		return fmt.Errorf("kick: HTTPClient no configurado")
	}
	return nil
}

// kickClip es un clip individual de la respuesta de /api/v2/channels/{slug}/clips.
// Solo se declaran los campos que ClipFactory usa.
type kickClip struct {
	ID            string  `json:"id"`
	Slug          string  `json:"slug"` // identificador público del clip (en la URL)
	Title         string  `json:"title"`
	Duration      float64 `json:"duration"`   // segundos
	CreatedAt     string  `json:"created_at"` // formato RFC3339 con offset ("2026-09-01T12:00:00Z" o +00:00)
	ThumbnailURL  string  `json:"thumbnail_url"`
	VideoFilename string  `json:"video_filename"` // ruta del MP4 en el CDN
	Channel       struct {
		ID       int64  `json:"id"`
		Slug     string `json:"slug"`
		Username string `json:"username"`
	} `json:"channel"`
}

// toClipInfo mapea un clip de Kick a la estructura neutra de ClipFactory
// (twitch.ClipInfo — el worker trabaja con ese tipo, plataforma-agnóstico).
func (k kickClip) toClipInfo() (twitch.ClipInfo, error) {
	if k.Slug == "" && k.ID == "" {
		return twitch.ClipInfo{}, fmt.Errorf("kick: clip sin id ni slug")
	}
	id := k.Slug
	if id == "" {
		id = k.ID
	}

	createdAt, err := time.Parse(time.RFC3339, k.CreatedAt)
	if err != nil {
		return twitch.ClipInfo{}, fmt.Errorf("kick: clip %s: parse created_at %q: %w", id, k.CreatedAt, err)
	}

	return twitch.ClipInfo{
		ID:           id,
		Title:        k.Title,
		DurationSec:  k.Duration,
		CreatedAt:    createdAt.UTC(),
		ThumbnailURL: k.ThumbnailURL,
		VideoURL:     k.VideoFilename, // ruta relativa del CDN; DownloadClip la resuelve
		ChannelID:    k.Channel.Slug,
		ChannelName:  k.Channel.Username,
	}, nil
}

// clipsResponse es la envolvente de la lista de clips.
type clipsResponse struct {
	Data []kickClip `json:"data"`
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
	for _, kc := range parsed.Data {
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

// DownloadClip descarga el MP4 de un clip de Kick al destino indicado.
//
// clipID es el slug del clip (lo mismo que ClipInfo.ID). La URL del archivo se
// resuelve en dos pasos para no depender de otro endpoint:
//  1. GET /api/v2/clips/{slug} → video_filename (ruta relativa en el CDN)
//  2. GET {FilesURL}{video_filename} → bytes del MP4
//
// El archivo se baja a .part y se renombra al final (atomicidad, igual que el
// adaptador de Twitch).
func (a *KickAdapter) DownloadClip(ctx context.Context, clipID string, destPath string) error {
	if clipID == "" {
		return fmt.Errorf("kick: clipID vacío")
	}
	if destPath == "" {
		return fmt.Errorf("kick: destPath vacío")
	}

	// paso 1: resolver el video_filename del clip
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
	if detail.VideoFilename == "" {
		return fmt.Errorf("kick: clip %s sin video_filename", clipID)
	}

	// paso 2: bajar el MP4 del CDN
	fileURL := detail.VideoFilename
	if !strings.HasPrefix(fileURL, "http") {
		fileURL = a.FilesURL + fileURL
	}
	fileReq, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return fmt.Errorf("kick: create file request: %w", err)
	}
	fileReq.Header.Set("User-Agent", userAgent)

	fileResp, err := a.HTTPClient.Do(fileReq)
	if err != nil {
		return fmt.Errorf("kick: file download: %w", err)
	}
	defer fileResp.Body.Close()
	if fileResp.StatusCode != http.StatusOK {
		return fmt.Errorf("kick: file download devolvió %d para %s", fileResp.StatusCode, fileURL)
	}

	// asegurar el directorio destino (data/incoming puede no existir aún)
	if dir := filepath.Dir(destPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("kick: create dest dir: %w", err)
		}
	}

	// bajar a .part y renombrar (atomicidad: no dejar un mp4 corrupto con el
	// nombre final si el proceso muere a mitad)
	tmpPath := destPath + ".part"
	defer os.Remove(tmpPath) // no-op si el rename fue exitoso

	out, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("kick: create temp file: %w", err)
	}
	written, err := io.Copy(out, fileResp.Body)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("kick: escribir archivo: %w", err)
	}
	if written == 0 {
		return fmt.Errorf("kick: archivo vacío para clip %s", clipID)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("kick: rename %s → %s: %w", tmpPath, destPath, err)
	}
	return nil
}
