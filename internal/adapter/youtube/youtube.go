// Package youtube implementa la publicación de clips en YouTube vía la
// YouTube Data API v3 (videos.insert con upload resumable).
//
// FLUJO DE AUTENTICACIÓN (app de escritorio — sin interacción en runtime):
//
//	Se usa un REFRESH TOKEN de usuario (flujo authorization_code + offline access)
//	generado UNA VEZ fuera del pipeline (ver docs/guia-youtube.md §4). En runtime
//	solo se canjea por un access token:
//	  POST https://oauth2.googleapis.com/token
//	    client_id, client_secret, refresh_token, grant_type=refresh_token
//	El access token dura ~1h; el adapter lo refresca automáticamente cuando
//	expira (con margen de 60s) y es thread-safe.
//
// UPLOAD RESUMABLE (videos.insert):
//
//	1. POST /upload/youtube/v3/videos?uploadType=resumable&part=snippet,status
//	   con los metadatos JSON → responde 200 y header Location (session URL)
//	2. PUT <session URL> con los bytes del video
//	3. 200/201 → { id, status } con el videoId externo
//
// CUOTAS: cada upload cuesta ~1600 unidades del default diario de 10,000
// (≈6 uploads/día). El worker trata el error 403 quotaExceeded como
// waiting_rate_limit y reintenta al día siguiente (ver executePublish).
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Endpoints de Google (sobrescribibles para tests con httptest).
const (
	DefaultTokenURL  = "https://oauth2.googleapis.com/token"
	DefaultUploadURL = "https://www.googleapis.com/upload/youtube/v3/videos"
)

// Publisher publica videos en YouTube.
type Publisher struct {
	ClientID     string
	ClientSecret string
	RefreshToken string

	// Opciones de metadata del upload
	PrivacyStatus string // "public", "unlisted", "private" (default: "public")
	CategoryID    string // categoryId de YouTube (default: "20" = Gaming)

	TokenURL  string // endpoint OAuth (tests)
	UploadURL string // endpoint videos.insert (tests)

	HTTPClient *http.Client

	// cache del access token (thread-safe: varios jobs pueden publicar a la vez)
	mu        sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

// NewPublisher crea un Publisher para YouTube.
func NewPublisher(clientID, clientSecret, refreshToken string) *Publisher {
	return &Publisher{
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		RefreshToken:  refreshToken,
		PrivacyStatus: "public",
		CategoryID:    "20", // Gaming
		TokenURL:      DefaultTokenURL,
		UploadURL:     DefaultUploadURL,
		HTTPClient:    &http.Client{Timeout: 10 * time.Minute}, // uploads de clips cortos
	}
}

// SetPrivacyStatus configura la privacidad de los videos subidos
// ("public", "unlisted", "private").
func (p *Publisher) SetPrivacyStatus(s string) { p.PrivacyStatus = s }

// SetCategoryID configura la categoryId de YouTube (ver docs/guia-youtube.md §6).
func (p *Publisher) SetCategoryID(id string) { p.CategoryID = id }

// validate verifica las credenciales mínimas.
func (p *Publisher) validate() error {
	if p.ClientID == "" {
		return fmt.Errorf("youtube: ClientID es requerido")
	}
	if p.ClientSecret == "" {
		return fmt.Errorf("youtube: ClientSecret es requerido")
	}
	if p.RefreshToken == "" {
		return fmt.Errorf("youtube: RefreshToken es requerido (ver docs/guia-youtube.md §4)")
	}
	return nil
}

// tokenResponse es la respuesta del endpoint OAuth de Google.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"` // segundos (usualmente 3599)
	TokenType   string `json:"token_type"`
}

// getAccessToken devuelve un access token vigente, refrescándolo si hace falta.
// Thread-safe: si dos goroutines llegan con el token vencido, solo una refresca.
func (p *Publisher) getAccessToken(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// margen de 60s: si vence en menos de un minuto, renovar ya
	if p.accessToken != "" && time.Now().Before(p.tokenExpiry.Add(-60*time.Second)) {
		return p.accessToken, nil
	}

	body, _ := json.Marshal(map[string]string{
		"client_id":     p.ClientID,
		"client_secret": p.ClientSecret,
		"refresh_token": p.RefreshToken,
		"grant_type":    "refresh_token",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", fmt.Errorf("token endpoint devolvió %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("token endpoint no devolvió access_token")
	}

	p.accessToken = tr.AccessToken
	// ExpiresIn suele ser 3599; guardar con 60s de margen adicional
	p.tokenExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return p.accessToken, nil
}

// videoSnippet/status son los metadatos del videos.insert.
// https://developers.google.com/youtube/v3/docs/videos/insert
type videoSnippet struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	CategoryID  string   `json:"categoryId"`
}

type videoStatus struct {
	PrivacyStatus      string `json:"privacyStatus"`
	SelfDeclaredMadeForKids bool `json:"selfDeclaredMadeForKids"`
}

type videoResource struct {
	Snippet videoSnippet `json:"snippet"`
	Status  videoStatus  `json:"status"`
}

// videoInsertResponse es la respuesta exitosa del videos.insert.
type videoInsertResponse struct {
	ID   string `json:"id"` // videoId externo (ej: dQw4w9WgXcQ)
	Kind string `json:"kind"`
	// status.uploadStatus es informativo ("uploaded", "processed", ...): el
	// procesamiento real de YouTube es asíncrono y no bloquea la publicación.
	Status struct {
		UploadStatus string `json:"uploadStatus"`
	} `json:"status"`
}

// UploadVideo sube videoPath a YouTube con upload resumable y devuelve el
// videoId externo y su URL pública (https://youtu.be/<id>).
//
// title/description/tags van al snippet. La API rechaza títulos >100 chars y
// descripciones >5000 (se recortan defensivamente acá).
func (p *Publisher) UploadVideo(ctx context.Context, videoPath, title, description string, tags []string) (string, string, error) {
	if err := p.validate(); err != nil {
		return "", "", err
	}

	// leer el video a memoria: los clips son <60s a 1080x1920 CRF23 (~5-15MB),
	// bien por debajo del límite razonable; el streaming con retry por chunks
	// queda para el backlog (ver docs/guia-youtube.md §8)
	data, err := os.ReadFile(videoPath)
	if err != nil {
		return "", "", fmt.Errorf("youtube: leer video %s: %w", videoPath, err)
	}

	token, err := p.getAccessToken(ctx)
	if err != nil {
		return "", "", fmt.Errorf("youtube: auth: %w", err)
	}

	// límites duros de la API
	if len(title) > 100 {
		title = title[:100]
	}
	if len(description) > 5000 {
		description = description[:5000]
	}

	meta := videoResource{
		Snippet: videoSnippet{
			Title:       title,
			Description: description,
			Tags:        tags,
			CategoryID:  p.CategoryID,
		},
		Status: videoStatus{
			PrivacyStatus:           p.PrivacyStatus,
			SelfDeclaredMadeForKids: false, // obligatorio declararlo explícitamente
		},
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return "", "", fmt.Errorf("youtube: marshal metadata: %w", err)
	}

	// PASO 1: iniciar la sesión resumable (solo metadatos)
	initURL := fmt.Sprintf("%s?uploadType=resumable&part=snippet,status", p.UploadURL)
	initReq, err := http.NewRequestWithContext(ctx, http.MethodPost, initURL, bytes.NewReader(metaJSON))
	if err != nil {
		return "", "", fmt.Errorf("youtube: create init request: %w", err)
	}
	initReq.Header.Set("Authorization", "Bearer "+token)
	initReq.Header.Set("Content-Type", "application/json; charset=UTF-8")

	initResp, err := p.HTTPClient.Do(initReq)
	if err != nil {
		return "", "", fmt.Errorf("youtube: init upload: %w", err)
	}
	defer initResp.Body.Close()

	if initResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(initResp.Body, 500))
		return "", "", p.classifyHTTPError(initResp.StatusCode, raw)
	}

	sessionURL := initResp.Header.Get("Location")
	if sessionURL == "" {
		return "", "", fmt.Errorf("youtube: init sin header Location (¿proxy intermedio?)")
	}

	// PASO 2: subir los bytes a la session URL
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURL, bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf("youtube: create upload request: %w", err)
	}
	upReq.Header.Set("Authorization", "Bearer "+token) // la session URL ya está pre-autorizada, pero el header es inofensivo
	upReq.Header.Set("Content-Type", "video/mp4")
	upReq.ContentLength = int64(len(data))

	upResp, err := p.HTTPClient.Do(upReq)
	if err != nil {
		return "", "", fmt.Errorf("youtube: upload bytes: %w", err)
	}
	defer upResp.Body.Close()

	if upResp.StatusCode != http.StatusOK && upResp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(io.LimitReader(upResp.Body, 500))
		return "", "", p.classifyHTTPError(upResp.StatusCode, raw)
	}

	var vr videoInsertResponse
	if err := json.NewDecoder(upResp.Body).Decode(&vr); err != nil {
		return "", "", fmt.Errorf("youtube: decode upload response: %w", err)
	}
	if vr.ID == "" {
		return "", "", fmt.Errorf("youtube: respuesta sin videoId")
	}

	externalURL := fmt.Sprintf("https://youtu.be/%s", vr.ID)
	return vr.ID, externalURL, nil
}

// classifyHTTPError convierte errores HTTP de la API en errores con la señal
// que el worker necesita: quotaExceeded → waiting_rate_limit (ver executePublish).
func (p *Publisher) classifyHTTPError(status int, body []byte) error {
	raw := strings.TrimSpace(string(body))
	switch {
	case status == http.StatusForbidden && strings.Contains(raw, "quotaExceeded"):
		return &RateLimitError{Detail: raw}
	case status == http.StatusForbidden && strings.Contains(raw, "rateLimitExceeded"):
		return &RateLimitError{Detail: raw}
	default:
		return fmt.Errorf("youtube: api devolvió %d: %s", status, raw)
	}
}

// RateLimitError marca errores de cuota/rate-limit de YouTube.
// executePublish la detecta con errors.As y programa el reintento para el día
// siguiente (la cuota de YouTube se resetea a medianoche PT).
type RateLimitError struct {
	Detail string
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("youtube: cuota agotada: %s", e.Detail)
}
