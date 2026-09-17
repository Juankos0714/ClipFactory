// Package meta implementa la publicación de clips en Facebook (Meta) como
// Reels de página, vía la Graph API.
//
// FLUJO DE PUBLICACIÓN (video de página en modo reel):
//
//	POST /{graph-version}/{PAGE_ID}/videos
//	  upload_type=reel, description=..., y el video como file_url o multipart
//	→ responde { "id": "<video_id>" }
//
//	La URL pública se construye con el ID:
//	  https://www.facebook.com/{PAGE_ID}/videos/{id}
//
// file_url vs subida binaria: la Graph API de videos acepta file_url (el
// servidor de Facebook baja el archivo). El pipeline genera los clips en
// disco local, así que file_url exige que data/completed/ sea servible por
// HTTP; para el MVP se soporta AMBOS modos:
//
//   - si FilesBaseURL está configurado (SetFilesBaseURL), se usa file_url;
//   - si no, se hace upload multipart directo (multipart/form-data con el
//     campo "source"), que es el flujo documentado para videos no resumibles.
//
// CUOTAS Y LÍMITES: no hay cuota diaria estilo YouTube, pero la API devuelve
// 400 con error.code 4/17/32 ("application limit reached", "user requests") en
// casos de rate limit — se clasifican como RateLimitError para que el worker
// aplique backoff (igual que youtube.RateLimitError).
package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/juankos0714/clipfactory/internal/adapter"
)

// DefaultGraphURL es la URL base de la Graph API de Meta.
const DefaultGraphURL = "https://graph.facebook.com"

// Publisher publica videos (Reels) en una página de Facebook.
//
// Es stateless: cada UploadVideo es independiente. GraphURL y FilesBaseURL son
// sobrescribibles para apuntar los tests a un httptest.Server.
type Publisher struct {
	PageID       string // ID de la página donde se publican los Reels
	AccessToken  string // Page Access Token
	GraphVersion string // versión de la Graph API ("v21.0", ...)

	// FilesBaseURL (opcional): base pública donde el clip queda servible por
	// HTTP (ej: "https://clips.midominio.com"). Si está configurado, se publica
	// con file_url={FilesBaseURL}/{nombre-del-clip}; si no, upload multipart.
	FilesBaseURL string

	GraphURL   string // endpoint Graph (sobrescribible en tests)
	HTTPClient *http.Client
}

// NewPublisher crea un Publisher de Meta/Facebook para una página.
func NewPublisher(pageID, accessToken, graphVersion string) *Publisher {
	if graphVersion == "" {
		graphVersion = "v21.0"
	}
	return &Publisher{
		PageID:       pageID,
		AccessToken:  accessToken,
		GraphVersion: graphVersion,
		GraphURL:     DefaultGraphURL,
		HTTPClient:   &http.Client{Timeout: 10 * time.Minute},
	}
}

// SetFilesBaseURL configura la base pública de archivos (activa el modo file_url).
func (p *Publisher) SetFilesBaseURL(base string) { p.FilesBaseURL = base }

// SetGraphURL configura la URL base de la Graph API (tests).
func (p *Publisher) SetGraphURL(url string) { p.GraphURL = url }

// validate verifica las credenciales mínimas.
func (p *Publisher) validate() error {
	if p.PageID == "" {
		return fmt.Errorf("meta: PageID es requerido")
	}
	if p.AccessToken == "" {
		return fmt.Errorf("meta: AccessToken es requerido (Page Access Token, no token de usuario)")
	}
	return nil
}

// videoPublishResponse es la respuesta de POST /{page-id}/videos.
type videoPublishResponse struct {
	ID string `json:"id"` // ID del video publicado en la página
}

// graphError es la envolvente de errores de la Graph API.
// https://developers.facebook.com/docs/graph-api/using-graph-api/error-handling
type graphError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// UploadVideo sube videoPath a la página de Facebook como Reel y devuelve el
// ID externo del video y su URL pública.
//
// Implementa el mismo contrato que youtube.Publisher.UploadVideo para que el
// worker los trate por igual (interface worker.Publisher).
func (p *Publisher) UploadVideo(ctx context.Context, videoPath, title, description string, tags []string) (string, string, error) {
	if err := p.validate(); err != nil {
		return "", "", err
	}

	// Facebook Reels no usa tags en este endpoint; el título va en la
	// descripción (los Reels de página no tienen campo title separado).
	text := description
	if title != "" {
		text = title + " — " + description
	}

	endpoint := fmt.Sprintf("%s/%s/%s/videos", p.GraphURL, p.GraphVersion, url.PathEscape(p.PageID))

	var resp *http.Response
	var err error
	if p.FilesBaseURL != "" {
		// modo file_url: Facebook baja el archivo por HTTP
		fileURL := p.FilesBaseURL + "/" + url.PathEscape(filepath.Base(videoPath))
		form := url.Values{}
		form.Set("upload_type", "reel")
		form.Set("description", text)
		form.Set("file_url", fileURL)
		form.Set("access_token", p.AccessToken)

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if reqErr != nil {
			return "", "", fmt.Errorf("meta: create request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err = p.HTTPClient.Do(req)
	} else {
		// modo multipart: subir el archivo directamente
		resp, err = p.uploadMultipart(ctx, endpoint, videoPath, text)
	}
	if err != nil {
		return "", "", fmt.Errorf("meta: publish video: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return "", "", p.classifyHTTPError(resp.StatusCode, body)
	}

	var vr videoPublishResponse
	if err := json.Unmarshal(body, &vr); err != nil {
		return "", "", fmt.Errorf("meta: decode response: %w", err)
	}
	if vr.ID == "" {
		return "", "", fmt.Errorf("meta: respuesta sin id de video")
	}

	externalURL := fmt.Sprintf("https://www.facebook.com/%s/videos/%s", p.PageID, vr.ID)
	return vr.ID, externalURL, nil
}

// uploadMultipart hace el upload directo del archivo (multipart/form-data,
// campo "source" según la documentación de la Graph API de videos).
// Usa streaming: el archivo se lee desde disco y se escribe al request sin
// cargarlo completo en memoria.
func (p *Publisher) uploadMultipart(ctx context.Context, endpoint, videoPath, description string) (*http.Response, error) {
	file, err := os.Open(videoPath)
	if err != nil {
		return nil, fmt.Errorf("meta: abrir video %s: %w", videoPath, err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("meta: stat video %s: %w", videoPath, err)
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer mw.Close()

		if err := mw.WriteField("upload_type", "reel"); err != nil {
			pw.CloseWithError(fmt.Errorf("write upload_type: %w", err))
			return
		}
		if err := mw.WriteField("description", description); err != nil {
			pw.CloseWithError(fmt.Errorf("write description: %w", err))
			return
		}
		if err := mw.WriteField("access_token", p.AccessToken); err != nil {
			pw.CloseWithError(fmt.Errorf("write access_token: %w", err))
			return
		}

		fw, err := mw.CreateFormFile("source", filepath.Base(videoPath))
		if err != nil {
			pw.CloseWithError(fmt.Errorf("create form file: %w", err))
			return
		}

		if _, err := io.Copy(fw, file); err != nil {
			pw.CloseWithError(fmt.Errorf("copy file to form: %w", err))
			return
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.ContentLength = fileInfo.Size() + int64(mw.BoundaryLength()) + 500 // approximate overhead

	return p.HTTPClient.Do(req)
}

// classifyHTTPError convierte errores HTTP de la Graph API en errores con la
// señal que el worker necesita: los códigos 4/17/32/613 son rate limit
// (transitorios → backoff), 401/403 → permanentes (token inválido/expirado o
// sin permisos sobre la página: reintentar no los arregla, el operador debe
// renovar el token) y el resto queda genérico = TRANSITORIO (con techo
// MaxPublishAttempts en el worker; conservador: no clasificar de más).
func (p *Publisher) classifyHTTPError(status int, body []byte) error {
	raw := strings.TrimSpace(string(body))
	var ge graphError
	if err := json.Unmarshal(body, &ge); err == nil && ge.Error.Message != "" {
		raw = fmt.Sprintf("código %d: %s", ge.Error.Code, ge.Error.Message)
		switch ge.Error.Code {
		case 4, 17, 32, 613: // "application limit reached", "user requests", "temporary issue"
			return &RateLimitError{Detail: raw}
		}
	}
	switch status {
	case http.StatusUnauthorized:
		return adapter.NewPermanentError(fmt.Sprintf("HTTP 401: %s", raw))
	case http.StatusForbidden:
		return adapter.NewPermanentError(fmt.Sprintf("HTTP 403: %s", raw))
	}
	return fmt.Errorf("meta: api devolvió %d: %s", status, raw)
}

// RateLimitError marca errores de rate limit de la Graph API. executePublish
// la detecta con errors.As (mismo patrón que youtube.RateLimitError) y
// programa el reintento con backoff.
type RateLimitError struct {
	Detail string
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("meta: rate limit: %s", e.Detail)
}
