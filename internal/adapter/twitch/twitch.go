package twitch

// Adaptador de Twitch para ClipFactory.
//
// Dos piezas externas:
//
//  1. API Helix (oficial, https://dev.twitch.tv/docs/api/reference/#get-clips):
//     listar clips de un canal. Requiere Client-ID y opcionalmente un App Access
//     Token (ver docs/guia-twitch.md para obtenerlos).
//
//  2. TwitchDownloaderCLI (comunidad, https://github.com/lay295/TwitchDownloader):
//     descargar el video del clip. Twitch NO expone descargas por API, por eso se
//     delega en esta herramienta externa (el adapter solo guarda la ruta).
//
// BaseURL es sobrescribible (SetBaseURL) para apuntar a un httptest.Server en los
// tests: así se verifican headers y query params sin tocar la API real.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PlatformAdapter define la interfaz que todos los adaptadores de plataforma deben implementar.
//
// ListClips acepta maxPages variadic (0/omitido = todas las páginas) para que los
// callers puedan limitar el trabajo del discovery en canales muy activos.
type PlatformAdapter interface {
	// ListClips lista los clips existentes en un canal, ordenados por fecha de creación.
	// Devuelve clips nuevos desde afterTimestamp (hora de la última revisión).
	ListClips(ctx context.Context, channelID string, afterTimestamp time.Time, maxPages ...int) ([]ClipInfo, error)

	// DownloadClip descarga un clip específico a la ruta indicada.
	// El archivo debe ser el clip en sí (no el VOD completo).
	DownloadClip(ctx context.Context, clipID string, destPath string) error

	// ValidateCredentials verifica que las credenciales necesarias estén configuradas.
	ValidateCredentials() error
}

// ClipInfo representa la metadata de un clip obtenida del origen.
//
// Es una estructura NEUTRA: no menciona a Twitch — el worker y la DB trabajan
// con ella, de modo que agregar Kick u otra plataforma solo requiere mapear
// su respuesta a este mismo struct.
type ClipInfo struct {
	ID           string
	Title        string
	DurationSec  float64
	CreatedAt    time.Time
	ThumbnailURL string
	VideoURL     string // URL de descarga del video (puede ser obtenida por herramienta externa)
	ChannelID    string
	ChannelName  string
}

// TwitchAdapter implementa PlatformAdapter para Twitch usando la API Helix.
type TwitchAdapter struct {
	ClientID       string
	AuthToken      string // OAuth token para Helix (opcional para listar clips públicos, requerido para algunos endpoints)
	HTTPClient     *http.Client
	BaseURL        string // URL base de la API (sobrescribible para tests)
	downloaderPath string // ruta a TwitchDownloaderCLI para descargar videos
}

// NewTwitchAdapter crea un nuevo adaptador de Twitch con defaults de producción:
// HTTP client con timeout de 30s, API real de Twitch y TwitchDownloaderCLI en PATH.
func NewTwitchAdapter(clientID, authToken string) *TwitchAdapter {
	return &TwitchAdapter{
		ClientID:       clientID,
		AuthToken:      authToken,
		HTTPClient:     &http.Client{Timeout: 30 * time.Second},
		BaseURL:        "https://api.twitch.tv",
		downloaderPath: "TwitchDownloaderCLI", // debe estar en PATH o se puede configurar
	}
}

// SetBaseURL configura la URL base de la API (útil para tests).
func (a *TwitchAdapter) SetBaseURL(url string) {
	a.BaseURL = url
}

// SetDownloaderPath configura la ruta al binario de TwitchDownloaderCLI.
func (a *TwitchAdapter) SetDownloaderPath(path string) {
	a.downloaderPath = path
}

// ValidateCredentials verifica que el ClientID esté configurado (mínimo requerido para Helix).
func (a *TwitchAdapter) ValidateCredentials() error {
	if a.ClientID == "" {
		return fmt.Errorf("twitch: ClientID es requerido")
	}
	return nil
}

// helixClipsResponse es la estructura de respuesta de GET /helix/clips.
// Documentación: https://dev.twitch.tv/docs/api/reference/#get-clips
//
// Solo se declaran los campos que ClipFactory usa; el resto del JSON se ignora
// silenciosamente (encoding/json no exige exhaustividad).
type helixClipsResponse struct {
	Data []helixClip `json:"data"`
	// Pagination trae el cursor para la próxima página ("" si no hay más)
	Pagination helixPagination `json:"pagination"`
}

type helixPagination struct {
	Cursor string `json:"cursor"`
}

// helixClip es un clip individual de la respuesta de Helix.
// Los comentarios de cada campo documentan el mapeo a ClipInfo.
type helixClip struct {
	ID              string  `json:"id"`               // → ClipInfo.ID (y source_clips.platform_clip_id)
	URL             string  `json:"url"`              // página pública del clip (no es la URL del video)
	EmbedURL        string  `json:"embed_url"`        // player embebible
	BroadcasterID   string  `json:"broadcaster_id"`   // → ClipInfo.ChannelID
	BroadcasterName string  `json:"broadcaster_name"` // → ClipInfo.ChannelName
	CreatorID       string  `json:"creator_id"`       // quien recortó el clip (no el streamer)
	CreatorName     string  `json:"creator_name"`
	VideoID         string  `json:"video_id"`      // VOD del que proviene el clip ("" si el VOD expiró)
	GameID          string  `json:"game_id"`       // categoría del stream (para filtrar en el futuro)
	Language        string  `json:"language"`      // "es", "en", ...
	Title           string  `json:"title"`         // → ClipInfo.Title
	ViewCount       int     `json:"view_count"`    // popularidad (útil para ordenar/filtrar)
	CreatedAt       string  `json:"created_at"`    // RFC3339 → ClipInfo.CreatedAt
	ThumbnailURL    string  `json:"thumbnail_url"` // → ClipInfo.ThumbnailURL
	Duration        float64 `json:"duration"`      // segundos → ClipInfo.DurationSec
}

// toClipInfo mapea un clip de Helix a la estructura neutra de ClipFactory.
// created_at que no se pueda parsear se deja como zero time (el caller decide).
func (h helixClip) toClipInfo() (ClipInfo, error) {
	createdAt, err := time.Parse(time.RFC3339, h.CreatedAt)
	if err != nil {
		return ClipInfo{}, fmt.Errorf("clip %s: parse created_at %q: %w", h.ID, h.CreatedAt, err)
	}
	return ClipInfo{
		ID:           h.ID,
		Title:        h.Title,
		DurationSec:  h.Duration,
		CreatedAt:    createdAt.UTC(),
		ThumbnailURL: h.ThumbnailURL,
		// VideoURL: Helix no expone la URL del video (por eso usamos
		// TwitchDownloaderCLI para descargar; ver DownloadClip)
		ChannelID:   h.BroadcasterID,
		ChannelName: h.BroadcasterName,
	}, nil
}

// ListClips lista los clips de un canal usando la API Helix de Twitch.
//
// GET {BaseURL}/helix/clips con:
//   - broadcaster_id: canal a consultar (ID numérico, NO el nombre — ver
//     GetChannelIDByName y docs/guia-twitch.md §6)
//   - first: página de 100 (máximo permitido por Helix)
//   - started_at: filtro desde afterTimestamp (RFC3339); ventana máx. de 7 días
//
// Sigue pagination.cursor automáticamente hasta traer todas las páginas (o hasta
// maxPages si se especifica; 0 = sin límite). Cada página es un request HTTP
// (1 punto de rate limit cada una, ver docs/guia-twitch.md §10).
//
// Headers: Client-ID siempre; Authorization Bearer solo si hay AuthToken
// (con token el rate limit es 800 pts/min, sin él 30 pts/min).
func (a *TwitchAdapter) ListClips(ctx context.Context, channelID string, afterTimestamp time.Time, maxPages ...int) ([]ClipInfo, error) {
	if err := a.ValidateCredentials(); err != nil {
		return nil, err
	}

	// límite de páginas: 0/negativo = sin límite (iterar hasta cursor vacío)
	pageLimit := 0
	if len(maxPages) > 0 {
		pageLimit = maxPages[0]
	}

	var allClips []ClipInfo
	cursor := ""
	page := 0

	for {
		page++
		respBody, nextCursor, err := a.fetchClipsPage(ctx, channelID, afterTimestamp, cursor)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", page, err)
		}

		// parsear y mapear a ClipInfo; un solo clip con created_at corrupto aborta
		// la sync (mejor fallar y reintentar todo el job que sincronizar a medias
		// con datos incompletos)
		for _, hc := range respBody.Data {
			clip, err := hc.toClipInfo()
			if err != nil {
				return nil, err
			}
			allClips = append(allClips, clip)
		}

		// fin de paginación: sin cursor o alcanzado el límite de páginas
		if nextCursor == "" || (pageLimit > 0 && page >= pageLimit) {
			break
		}
		cursor = nextCursor
	}

	return allClips, nil
}

// fetchClipsPage hace UN request a /helix/clips y devuelve la respuesta parseada
// junto con el cursor de la próxima página ("" si no hay más).
func (a *TwitchAdapter) fetchClipsPage(ctx context.Context, channelID string, afterTimestamp time.Time, cursor string) (*helixClipsResponse, string, error) {
	// construir parámetros de consulta
	params := map[string]string{
		"broadcaster_id": channelID,
		"first":          "100", // máximo permitido por página
	}

	if !afterTimestamp.IsZero() {
		// Helix acepta RFC3339 para started_at; la ventana started↔ended es máx. 7 días
		params["started_at"] = afterTimestamp.UTC().Format(time.RFC3339)
	}
	if cursor != "" {
		params["after"] = cursor
	}

	// hacer request a Helix
	req, err := http.NewRequestWithContext(ctx,
		"GET",
		a.BaseURL+"/helix/clips",
		nil,
	)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}

	// headers requeridos por Helix
	req.Header.Set("Client-ID", a.ClientID)
	if a.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.AuthToken)
	}

	// agregar parámetros de query
	q := req.URL.Query()
	for k, v := range params {
		q.Add(k, v)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("api request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 429: rate limit de Helix — el worker necesita saber CUÁNDO reintentar.
		// Helix no envía Retry-After; usamos el default de 60s (basta para que
		// se restablezca la ventana por-app) y lo deja parametrizable para tests
		// y por si la API empieza a enviar el header.
		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), DefaultTwitchRetryAfter)
			return nil, "", &RateLimitError{RetryAfter: retryAfter}
		}
		return nil, "", fmt.Errorf("api returned status %d", resp.StatusCode)
	}

	// parsear el JSON de Helix
	var parsed helixClipsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, "", fmt.Errorf("decode response: %w", err)
	}
	return &parsed, parsed.Pagination.Cursor, nil
}

// DownloadClip descarga un clip de Twitch usando TwitchDownloaderCLI.
//
// Twitch no expone un endpoint oficial de descarga de clips: la especificación de
// ClipFactory indica usar TwitchDownloaderCLI (modo clipdownload):
//
//	TwitchDownloaderCLI -m clipdownload \
//		-u https://www.twitch.tv/<channel>/clip/<clipID> \
//		-o <destPath>
//
// La ruta al binario se configura con SetDownloaderPath (default: PATH del sistema).
// Requiere ffmpeg instalado (la herramienta lo usa para remuxear).
func (a *TwitchAdapter) DownloadClip(ctx context.Context, clipID string, destPath string) error {
	if clipID == "" {
		return fmt.Errorf("twitch: clipID vacío")
	}
	if destPath == "" {
		return fmt.Errorf("twitch: destPath vacío")
	}

	// TwitchDownloaderCLI acepta el ID pelado del clip (verificado contra CLI 1.56.5:
	// la URL https://www.twitch.tv/clip/<ID> da "Unable to parse Clip ID/URL" porque
	// twitch.tv redirige y el parser no la resuelve; el ID directo sí funciona).
	clipIDArg := clipID

	// asegurar que el directorio destino exista (data/incoming puede no existir aún)
	if dir := filepath.Dir(destPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dest dir: %w", err)
		}
	}

	// descargar a archivo temporal y luego renombrar (atomicidad): si el proceso
	// muere a mitad, no queda un .mp4 corrupto en el destino con el nombre final.
	// TwitchDownloaderCLI agrega su propia extensión si no la tiene; terminamos en
	// .part para que la salida final sea exactamente destPath.
	tmpPath := destPath + ".part"
	defer os.Remove(tmpPath) // no-op si el rename fue exitoso

	// #nosec G204 — la ruta del binario es configurable por el operador (downloaderPath)
	cmd := exec.CommandContext(ctx, a.downloaderPath,
		"clipdownload",
		"-u", clipIDArg,
		"-o", tmpPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr // un solo buffer: el CLI mezcla progreso y errores

	log.Printf("[twitch] downloading clip %s → %s (downloader: %s)", clipID, destPath, a.downloaderPath)
	if err := cmd.Run(); err != nil {
		// recortar stderr: puede incluir barras de progreso enormes
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:] // quedarnos con el final (donde está el error real)
		}
		return fmt.Errorf("twitch: downloader falló para clip %s: %v: %s", clipID, err, msg)
	}

	// verificar que el archivo temporal exista y tenga contenido
	info, err := os.Stat(tmpPath)
	if err != nil {
		return fmt.Errorf("twitch: downloader no produjo salida en %s: %w", tmpPath, err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("twitch: downloader produjo archivo vacío para clip %s", clipID)
	}

	// mover al destino final (mismo filesystem: rename atómico)
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("twitch: rename %s → %s: %w", tmpPath, destPath, err)
	}

	log.Printf("[twitch] clip %s descargado (%d bytes) → %s", clipID, info.Size(), destPath)
	return nil
}

// GetChannelIDByName obtiene el broadcaster ID numérico a partir del nombre de
// usuario. Necesario porque /helix/clips exige broadcaster_id y los usuarios suelen
// conocer el canal por su nombre (ver docs/guia-twitch.md §6).
//
// GET {BaseURL}/helix/users?login=<username> → data[0].id
// TODO: parsear el JSON y devolver data[0].id (hoy devuelve "" con status OK).
func (a *TwitchAdapter) GetChannelIDByName(ctx context.Context, username string) (string, error) {
	if err := a.ValidateCredentials(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "GET",
		a.BaseURL+"/helix/users?login="+username, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Client-ID", a.ClientID)

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("api request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("api returned status %d", resp.StatusCode)
	}

	// TODO: parsear JSON y extraer el ID del canal
	return "", nil
}
