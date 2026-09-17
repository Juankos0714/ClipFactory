package youtube

// Tests de la reconciliación anti-duplicados: FindRecentByMarker recorre la
// playlist de uploads buscando el marker determinista en las descripciones.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newReconcileServer arma un publisher con un fake de Google que responde
// channels.list (playlist UU123) y playlistItems.list con las descripciones
// dadas, y registra las rutas pedidas.
func newReconcileServer(t *testing.T, descriptions []string, itemStatus int, itemBody string) (*Publisher, *[]string) {
	t.Helper()

	var paths []string
	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, "/token")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{AccessToken: "fake-token", ExpiresIn: 3599})
	})

	mux.HandleFunc("/channels", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[{"contentDetails":{"relatedPlaylists":{"uploads":"UUuploads123"}}}]}`)
	})

	mux.HandleFunc("/playlistItems", func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if itemStatus != 0 && itemStatus != http.StatusOK {
			w.WriteHeader(itemStatus)
			fmt.Fprint(w, itemBody)
			return
		}
		var sb strings.Builder
		sb.WriteString(`{"items":[`)
		for i, d := range descriptions {
			if i > 0 {
				sb.WriteString(",")
			}
			// videoId distinto por item para verificar cuál matchea
			fmt.Fprintf(&sb, `{"snippet":{"title":"t%d","description":%q,"resourceId":{"videoId":"vid%d"}}}`, i, d, i)
		}
		sb.WriteString(`]}`)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, sb.String())
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	p := NewPublisher("cid", "csecret", "rtoken")
	p.TokenURL = srv.URL + "/token"
	p.APIURL = srv.URL
	return p, &paths
}

func TestFindRecentByMarkerHit(t *testing.T) {
	p, paths := newReconcileServer(t, []string{
		"video viejo sin marker",
		"Clip generado con ClipFactory\ncf-7-42 cf-7",
	}, 0, "")
	// marcar la primera playlistItems como vieja: reordenar no importa, el
	// match es por contenido

	extID, extURL, err := p.FindRecentByMarker(context.Background(), []string{"cf-7-42", "cf-7"})
	if err != nil {
		t.Fatalf("FindRecentByMarker: %v", err)
	}
	if extID != "vid1" {
		t.Errorf("expected vid1 (el que tiene el marker), got %q", extID)
	}
	if extURL != "https://youtu.be/vid1" {
		t.Errorf("expected youtu.be URL, got %q", extURL)
	}
	// flujo completo: token → channels → playlistItems
	if len(*paths) != 3 {
		t.Errorf("expected 3 API calls (token/channels/playlistItems), got %v", *paths)
	}
}

func TestFindRecentByMarkerMiss(t *testing.T) {
	p, _ := newReconcileServer(t, []string{"a", "b"}, 0, "")

	extID, extURL, err := p.FindRecentByMarker(context.Background(), []string{"cf-99-99"})
	if err != nil {
		t.Fatalf("FindRecentByMarker: %v", err)
	}
	if extID != "" || extURL != "" {
		t.Errorf("expected empty (no match), got %q %q", extID, extURL)
	}
}

func TestFindRecentByMarkerNoMarkers(t *testing.T) {
	// sin markers: no debe llamarse a la API
	p, paths := newReconcileServer(t, nil, 0, "")

	extID, _, err := p.FindRecentByMarker(context.Background(), nil)
	if err != nil || extID != "" {
		t.Errorf("expected no-op, got %q %v", extID, err)
	}
	if len(*paths) != 0 {
		t.Errorf("expected 0 API calls sin markers, got %v", *paths)
	}
}

func TestFindRecentByMarkerAPIError(t *testing.T) {
	p, _ := newReconcileServer(t, nil, http.StatusInternalServerError, `upstream error`)

	if _, _, err := p.FindRecentByMarker(context.Background(), []string{"cf-1-1"}); err == nil {
		t.Fatal("expected error when playlistItems fails")
	}
}
