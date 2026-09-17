package youtube

// Reconciliación anti-duplicados: buscar entre los videos recién subidos al
// canal uno cuya descripción contenga el marker determinista
// "cf-<clipID>-<videoID>" (ver db.PublicationKeysForClip). Lo llama
// executePublish antes de reintentar un upload: si el intento anterior subió
// el video y murió antes de actualizar la DB, acá se encuentra y NO se duplica.
//
// Dos llamadas a la API pública de lectura (NO consumen cuota de upload):
//   1. channels.list (part=contentDetails, mine=true) → ID de la playlist
//      "uploads" del canal autorizado (la playlist UC... con UU...).
//   2. playlistItems.list (part=snippet, maxResults=50) → los últimos 50
//      uploads, con su descripción, para matchear el marker.
//
// 50 uploads cubre holgado la ventana de riesgo: solo importan los subidos
// desde el último intento fallido (horas, con cap de backoff de 24h).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DefaultAPIURL es la base de la Data API v3 (sobrescribible en tests).
const DefaultAPIURL = "https://www.googleapis.com/youtube/v3"

// playlistResponse / playlistItemsResponse son los fragmentos de respuesta que
// interesan (el JSON real tiene muchos más campos; json ignora los demás).
type playlistResponse struct {
	Items []struct {
		ContentDetails struct {
			RelatedPlaylists struct {
				Uploads string `json:"uploads"`
			} `json:"relatedPlaylists"`
		} `json:"contentDetails"`
	} `json:"items"`
}

type playlistItemsResponse struct {
	Items []struct {
		Snippet struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			ResourceID  struct {
				VideoID string `json:"videoId"`
			} `json:"resourceId"`
		} `json:"snippet"`
	} `json:"items"`
}

// FindRecentByMarker implementa worker.Reconciler: busca el marker en la
// descripción de los últimos 50 uploads del canal y devuelve (videoID, URL)
// del primero que coincida. (\"\", \"\", nil) = no está (seguir con el upload).
func (p *Publisher) FindRecentByMarker(ctx context.Context, markers []string) (string, string, error) {
	if len(markers) == 0 {
		return "", "", nil
	}

	token, err := p.getAccessToken(ctx)
	if err != nil {
		return "", "", fmt.Errorf("youtube: auth (reconciliación): %w", err)
	}

	uploadsPlaylist, err := p.fetchUploadsPlaylist(ctx, token)
	if err != nil {
		return "", "", err
	}
	return p.findMarkerInUploads(ctx, token, uploadsPlaylist, markers)
}

// fetchUploadsPlaylist llama channels.list (mine=true) y devuelve la playlist
// de uploads del canal.
func (p *Publisher) fetchUploadsPlaylist(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.APIURL+"/channels?part=contentDetails&mine=true", nil)
	if err != nil {
		return "", fmt.Errorf("youtube: create channels request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("youtube: channels.list: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("youtube: channels.list devolvió %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var pr playlistResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return "", fmt.Errorf("youtube: decode channels response: %w", err)
	}
	if len(pr.Items) == 0 || pr.Items[0].ContentDetails.RelatedPlaylists.Uploads == "" {
		return "", fmt.Errorf("youtube: canal sin playlist de uploads")
	}
	return pr.Items[0].ContentDetails.RelatedPlaylists.Uploads, nil
}

// findMarkerInUploads llama playlistItems.list y busca los markers en las
// descripciones de los últimos 50 uploads.
func (p *Publisher) findMarkerInUploads(ctx context.Context, token, uploadsPlaylist string, markers []string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/playlistItems?part=snippet&playlistId=%s&maxResults=50", p.APIURL, uploadsPlaylist), nil)
	if err != nil {
		return "", "", fmt.Errorf("youtube: create playlistItems request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("youtube: playlistItems.list: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("youtube: playlistItems.list devolvió %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var pir playlistItemsResponse
	if err := json.Unmarshal(body, &pir); err != nil {
		return "", "", fmt.Errorf("youtube: decode playlistItems response: %w", err)
	}

	for _, item := range pir.Items {
		desc := item.Snippet.Description
		for _, m := range markers {
			if m != "" && strings.Contains(desc, m) {
				vid := item.Snippet.ResourceID.VideoID
				return vid, "https://youtu.be/" + vid, nil
			}
		}
	}
	return "", "", nil
}
