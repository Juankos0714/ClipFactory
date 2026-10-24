package kick

// Tests del adaptador de Kick contra httptest.Server (sin red real): parseo de
// la API de clips, filtro temporal, descarga en dos pasos y errores HTTP.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clipsJSON es una respuesta mínima de /api/v2/channels/{slug}/clips (formato actual de Kick).
const clipsJSON = `{
  "clips": [
    {
      "id": "clip-reciente",
      "title": "Clip reciente",
      "duration": 42.5,
      "created_at": "2026-09-10T12:00:00Z",
      "thumbnail_url": "https://clips.kick.com/thumbs/c-1.jpg",
      "video_url": "https://clips.kick.com/clips/d3/clip-reciente/playlist.m3u8",
      "view_count": 100,
      "channel": {"id": 7, "slug": "illojuan", "username": "illojuan"}
    },
    {
      "id": "clip-viejo",
      "title": "Clip viejo",
      "duration": 30,
      "created_at": "2026-08-01T12:00:00Z",
      "thumbnail_url": "https://clips.kick.com/thumbs/c-2.jpg",
      "video_url": "https://clips.kick.com/clips/d4/clip-viejo/playlist.m3u8",
      "view_count": 50,
      "channel": {"id": 7, "slug": "illojuan", "username": "illojuan"}
    }
  ],
  "nextCursor": "{\"view\":0,\"id\":\"clip-viejo\"}"
}`

// clipDetailJSON es la respuesta de /api/v2/clips/{id} (formato actual de Kick).
const clipDetailJSON = `{
  "id": "clip-reciente",
  "video_url": "https://clips.kick.com/clips/d3/clip-reciente/playlist.m3u8"
}`

// newTestAdapter arma un KickAdapter apuntando a un servidor de prueba con un
// mux mínimo que simula la API (lista de clips y detalle del clip).
func newTestAdapter(t *testing.T, listStatus, detailStatus int) (*KickAdapter, string) {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/channels/illojuan/clips", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("expected User-Agent header (kick.com lo exige)")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(listStatus)
		_, _ = w.Write([]byte(clipsJSON))
	})
	mux.HandleFunc("/api/v2/clips/clip-reciente", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(detailStatus)
		_, _ = w.Write([]byte(clipDetailJSON))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	a := NewKickAdapter()
	a.SetBaseURL(srv.URL)
	return a, srv.URL
}

func TestListClipsOK(t *testing.T) {
	a, _ := newTestAdapter(t, http.StatusOK, http.StatusOK)

	// sin filtro temporal: trae todos
	clips, err := a.ListClips(context.Background(), "illojuan", time.Time{})
	if err != nil {
		t.Fatalf("ListClips: %v", err)
	}
	if len(clips) != 2 {
		t.Fatalf("expected 2 clips, got %d", len(clips))
	}
	first := clips[0]
	if first.ID != "clip-reciente" || first.Title != "Clip reciente" || first.DurationSec != 42.5 {
		t.Errorf("unexpected first clip: %+v", first)
	}
	if first.ChannelID != "illojuan" {
		t.Errorf("expected channel slug as ChannelID, got %q", first.ChannelID)
	}
	if first.VideoURL != "https://clips.kick.com/clips/d3/clip-reciente/playlist.m3u8" {
		t.Errorf("expected video_url (HLS) in VideoURL, got %q", first.VideoURL)
	}

	// con filtro temporal: solo el clip de septiembre
	clips, err = a.ListClips(context.Background(), "illojuan", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ListClips with after: %v", err)
	}
	if len(clips) != 1 || clips[0].ID != "clip-reciente" {
		t.Errorf("expected only 'clip-reciente' after 2026-09-01, got %+v", clips)
	}
}

func TestListClipsChannelNotFound(t *testing.T) {
	a, _ := newTestAdapter(t, http.StatusNotFound, http.StatusOK)

	_, err := a.ListClips(context.Background(), "no-existe", time.Time{})
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "no encontrado") {
		t.Errorf("expected friendly 404 message, got: %v", err)
	}
}

func TestListClipsBadJSON(t *testing.T) {
	a, _ := newTestAdapter(t, http.StatusOK, http.StatusOK)
	// sobrescribir el handler con contenido inválido
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/channels/illojuan/clips", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a.SetBaseURL(srv.URL)

	if _, err := a.ListClips(context.Background(), "illojuan", time.Time{}); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestListClipsInvalidCreatedAt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/channels/illojuan/clips", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"clips":[{"id":"x","created_at":"no-es-fecha"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := NewKickAdapter()
	a.SetBaseURL(srv.URL)
	if _, err := a.ListClips(context.Background(), "illojuan", time.Time{}); err == nil {
		t.Error("expected error for unparseable created_at")
	}
}

func TestDownloadClipDetailFails(t *testing.T) {
	a, _ := newTestAdapter(t, http.StatusOK, http.StatusInternalServerError)

	dest := filepath.Join(t.TempDir(), "clip.mp4")
	if err := a.DownloadClip(context.Background(), "clip-reciente", dest); err == nil {
		t.Error("expected error when clip detail endpoint fails")
	}
}

func TestDownloadClipNoVideoURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/clips/clip-reciente", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"clip-reciente"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := NewKickAdapter()
	a.SetBaseURL(srv.URL)
	dest := filepath.Join(t.TempDir(), "clip.mp4")
	if err := a.DownloadClip(context.Background(), "clip-reciente", dest); err == nil {
		t.Error("expected error when clip has no video_url")
	}
}

func TestDownloadClipValidation(t *testing.T) {
	a := NewKickAdapter()
	if err := a.DownloadClip(context.Background(), "", "/tmp/x.mp4"); err == nil {
		t.Error("expected error for empty clipID")
	}
	if err := a.DownloadClip(context.Background(), "clip", ""); err == nil {
		t.Error("expected error for empty destPath")
	}
}

func TestToClipInfoIDFallback(t *testing.T) {
	// solo id: usa id directamente
	kc := kickClip{ID: "only-id", CreatedAt: "2026-09-01T10:00:00Z"}
	info, err := kc.toClipInfo()
	if err != nil {
		t.Fatalf("toClipInfo: %v", err)
	}
	if info.ID != "only-id" {
		t.Errorf("expected ID to be id, got %q", info.ID)
	}

	// created_at corrupto: error
	kc2 := kickClip{ID: "x", CreatedAt: "garbage"}
	if _, err := kc2.toClipInfo(); err == nil {
		t.Error("expected error for invalid created_at")
	}

	// vacío: error
	kc3 := kickClip{CreatedAt: "2026-09-01T10:00:00Z"}
	if _, err := kc3.toClipInfo(); err == nil {
		t.Error("expected error for empty clip")
	}
}

func TestValidateCredentials(t *testing.T) {
	a := NewKickAdapter()
	if err := a.ValidateCredentials(); err != nil {
		t.Errorf("expected valid adapter, got %v", err)
	}
	a.HTTPClient = nil
	if err := a.ValidateCredentials(); err == nil {
		t.Error("expected error for nil HTTPClient")
	}
}
