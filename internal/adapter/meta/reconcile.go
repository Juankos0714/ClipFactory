package meta

// Reconciliación anti-duplicados: buscar entre los videos recientes de la
// página uno cuyo detalle contenga el marker determinista
// "cf-<clipID>-<videoID>" (ver db.PublicationKeysForClip). Lo llama
// executePublish antes de reintentar un upload para no duplicar un Reel que
// ya se subió en un intento anterior que murió antes de actualizar la DB.
//
// UNA llamada de lectura: GET /{page-id}/videos?fields=id,description&limit=50
// (los videos de página —incluidos los Reels— aparecen en la conexión /videos
// de la página, ordenados por creación descendente).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// pageVideosResponse es el fragmento de la respuesta de GET /{page}/videos.
type pageVideosResponse struct {
	Data []struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	} `json:"Data"`
	// Graph API devuelve la clave "data"; json.Unmarshal no distingue
	// mayúsculas en el match de claves, así que este tag funciona para ambas.
	Paging struct {
		Next string `json:"next"`
	} `json:"paging"`
}

// FindRecentByMarker implementa worker.Reconciler: busca el marker en la
// descripción de los últimos 50 videos de la página y devuelve (videoID, URL)
// del primero que coincida. ("", "", nil) = no está (seguir con el upload).
func (p *Publisher) FindRecentByMarker(ctx context.Context, markers []string) (string, string, error) {
	if len(markers) == 0 {
		return "", "", nil
	}

	endpoint := fmt.Sprintf("%s/%s/%s/videos?fields=id,description&limit=50",
		p.GraphURL, p.GraphVersion, url.PathEscape(p.PageID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", fmt.Errorf("meta: create reconcile request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.AccessToken)

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("meta: list videos (reconciliación): %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("meta: list videos devolvió %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var pvr pageVideosResponse
	if err := json.Unmarshal(body, &pvr); err != nil {
		return "", "", fmt.Errorf("meta: decode videos response: %w", err)
	}

	for _, item := range pvr.Data {
		for _, m := range markers {
			if m != "" && strings.Contains(item.Description, m) {
				return item.ID, fmt.Sprintf("https://www.facebook.com/%s/videos/%s", p.PageID, item.ID), nil
			}
		}
	}
	return "", "", nil
}
