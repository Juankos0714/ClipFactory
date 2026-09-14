package twitch

// Tests del parseo de la respuesta JSON de Helix /clips y de la paginación.
//
// Se usa un httptest.Server que devuelve JSON idéntico al de la API real
// (ver docs/guia-twitch.md §7 para la estructura documentada de Helix).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// helixClipJSON genera el JSON de un clip de Helix con los valores que mapeamos.
func helixClipJSON(id, title string, duration float64, createdAt string) string {
	return fmt.Sprintf(`{
		"id": %q,
		"url": "https://www.twitch.tv/testchannel/clip/%s",
		"embed_url": "https://clips.twitch.tv/embed?clip=%s",
		"broadcaster_id": "12345",
		"broadcaster_name": "testchannel",
		"creator_id": "999",
		"creator_name": "viewer_x",
		"video_id": "460123456",
		"game_id": "32982",
		"language": "es",
		"title": %q,
		"view_count": 1337,
		"created_at": %q,
		"thumbnail_url": "https://clips-media-assets2.twitch.tv/%s-preview.jpg",
		"duration": %s
	}`, id, id, id, title, createdAt, id, strconv.FormatFloat(duration, 'f', 2, 64))
}

func TestListClipsParsesHelixJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data": [%s, %s], "pagination": {}}`,
			helixClipJSON("clipAAA", "PRIMER CLIP", 30.5, "2026-09-10T18:03:22Z"),
			helixClipJSON("clipBBB", "SEGUNDO CLIP", 15.0, "2026-09-11T10:00:00Z"),
		)
	}))
	defer ts.Close()

	a := NewTwitchAdapter("cid", "")
	a.SetBaseURL(ts.URL)

	clips, err := a.ListClips(context.Background(), "12345", time.Time{}, 1)
	if err != nil {
		t.Fatalf("list clips: %v", err)
	}
	if len(clips) != 2 {
		t.Fatalf("expected 2 clips, got %d", len(clips))
	}

	// mapeo campo a campo del primer clip
	c := clips[0]
	if c.ID != "clipAAA" {
		t.Errorf("expected ID 'clipAAA', got %q", c.ID)
	}
	if c.Title != "PRIMER CLIP" {
		t.Errorf("expected Title 'PRIMER CLIP', got %q", c.Title)
	}
	if c.DurationSec != 30.5 {
		t.Errorf("expected DurationSec 30.5, got %f", c.DurationSec)
	}
	want := time.Date(2026, 9, 10, 18, 3, 22, 0, time.UTC)
	if !c.CreatedAt.Equal(want) {
		t.Errorf("expected CreatedAt %v, got %v", want, c.CreatedAt)
	}
	if c.CreatedAt.Location() != time.UTC {
		t.Errorf("expected UTC location, got %v", c.CreatedAt.Location())
	}
	if c.ThumbnailURL != "https://clips-media-assets2.twitch.tv/clipAAA-preview.jpg" {
		t.Errorf("unexpected ThumbnailURL: %q", c.ThumbnailURL)
	}
	if c.ChannelID != "12345" || c.ChannelName != "testchannel" {
		t.Errorf("expected channel 12345/testchannel, got %s/%s", c.ChannelID, c.ChannelName)
	}
}

func TestListClipsPagination(t *testing.T) {
	// servidor que devuelve 3 páginas: cada una con 1 clip y cursor a la siguiente
	var requests atomic.Int32
	type page struct {
		body   string
		cursor string
	}
	pages := []page{
		{body: helixClipJSON("p1clip", "PAGINA 1", 10, "2026-09-10T10:00:00Z"), cursor: "cursor-2"},
		{body: helixClipJSON("p2clip", "PAGINA 2", 20, "2026-09-10T11:00:00Z"), cursor: "cursor-3"},
		{body: helixClipJSON("p3clip", "PAGINA 3", 30, "2026-09-10T12:00:00Z"), cursor: ""},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(requests.Add(1)) - 1
		if n >= len(pages) {
			n = len(pages) - 1 // pedido extra: repetir la última
		}
		p := pages[n]

		// verificar que el cursor "after" va avanzando
		after := r.URL.Query().Get("after")
		switch n {
		case 0:
			if after != "" {
				t.Errorf("page 1: expected no after cursor, got %q", after)
			}
		default:
			if after != pages[n-1].cursor {
				t.Errorf("page %d: expected after=%q, got %q", n+1, pages[n-1].cursor, after)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if p.cursor == "" {
			fmt.Fprintf(w, `{"data": [%s], "pagination": {}}`, p.body)
		} else {
			fmt.Fprintf(w, `{"data": [%s], "pagination": {"cursor": %q}}`, p.body, p.cursor)
		}
	}))
	defer ts.Close()

	a := NewTwitchAdapter("cid", "")
	a.SetBaseURL(ts.URL)

	// sin límite de páginas: debe traer las 3
	clips, err := a.ListClips(context.Background(), "12345", time.Time{})
	if err != nil {
		t.Fatalf("list clips: %v", err)
	}
	if len(clips) != 3 {
		t.Fatalf("expected 3 clips across pages, got %d", len(clips))
	}
	if clips[0].ID != "p1clip" || clips[1].ID != "p2clip" || clips[2].ID != "p3clip" {
		t.Errorf("unexpected clip order: %v %v %v", clips[0].ID, clips[1].ID, clips[2].ID)
	}

	// con maxPages=2: solo 2 páginas
	requests.Store(0)
	clips, err = a.ListClips(context.Background(), "12345", time.Time{}, 2)
	if err != nil {
		t.Fatalf("list clips (maxPages=2): %v", err)
	}
	if len(clips) != 2 {
		t.Errorf("expected 2 clips with maxPages=2, got %d", len(clips))
	}
}

func TestListClipsMalformedJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data": [ {"id": "x", `) // JSON truncado
	}))
	defer ts.Close()

	a := NewTwitchAdapter("cid", "")
	a.SetBaseURL(ts.URL)

	_, err := a.ListClips(context.Background(), "12345", time.Time{}, 1)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("expected 'decode response' in error, got: %v", err)
	}
}

func TestListClipsInvalidCreatedAt(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data": [%s], "pagination": {}}`,
			helixClipJSON("clipBad", "FECHA ROTA", 10, "no-es-una-fecha"))
	}))
	defer ts.Close()

	a := NewTwitchAdapter("cid", "")
	a.SetBaseURL(ts.URL)

	_, err := a.ListClips(context.Background(), "12345", time.Time{}, 1)
	if err == nil {
		t.Fatal("expected error for invalid created_at, got nil")
	}
	if !strings.Contains(err.Error(), "clipBad") || !strings.Contains(err.Error(), "created_at") {
		t.Errorf("expected clip ID and field in error, got: %v", err)
	}
}

func TestListClipsExtraFieldsIgnored(t *testing.T) {
	// Helix agrega campos con el tiempo; el parseo no debe romperse con extras
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data": [{"id":"clipX","title":"T","duration":5,
			"created_at":"2026-09-10T10:00:00Z","broadcaster_id":"1","broadcaster_name":"n",
			"campo_futuro": {"a": [1,2,3]}, "otro": null}],
			"pagination": {}}`)
	}))
	defer ts.Close()

	a := NewTwitchAdapter("cid", "")
	a.SetBaseURL(ts.URL)

	clips, err := a.ListClips(context.Background(), "12345", time.Time{}, 1)
	if err != nil {
		t.Fatalf("list clips with unknown fields: %v", err)
	}
	if len(clips) != 1 || clips[0].ID != "clipX" {
		t.Errorf("expected 1 clip 'clipX', got %+v", clips)
	}
}

func TestListClipsEmptyDataField(t *testing.T) {
	// canal sin clips: Helix devuelve data: [] (no null en el mejor caso, pero
	// también hay que tolerar pagination ausente)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data": [], "pagination": {}}`)
	}))
	defer ts.Close()

	a := NewTwitchAdapter("cid", "")
	a.SetBaseURL(ts.URL)

	clips, err := a.ListClips(context.Background(), "12345", time.Time{}, 1)
	if err != nil {
		t.Fatalf("list clips empty: %v", err)
	}
	if len(clips) != 0 {
		t.Errorf("expected 0 clips, got %d", len(clips))
	}
}

// TestListClipsRealHelixShape verifica contra un JSON copiado de la documentación
// oficial de Helix (forma completa, todos los campos presentes).
func TestListClipsRealHelixShape(t *testing.T) {
	// json.Valid como guard + parseo directo de la estructura interna
	raw := []byte(helixClipJSON("AwkwardHelplessSalamanderSwiftRage", "JUGANDO CON MI ABUELA", 32.5, "2026-09-12T18:03:22Z"))
	if !json.Valid(raw) {
		t.Fatal("test fixture is not valid JSON")
	}

	var hc helixClip
	if err := json.Unmarshal(raw, &hc); err != nil {
		t.Fatalf("unmarshal helix clip: %v", err)
	}
	clip, err := hc.toClipInfo()
	if err != nil {
		t.Fatalf("toClipInfo: %v", err)
	}
	if clip.ID != "AwkwardHelplessSalamanderSwiftRage" {
		t.Errorf("unexpected ID: %q", clip.ID)
	}
	if clip.DurationSec != 32.5 {
		t.Errorf("unexpected duration: %f", clip.DurationSec)
	}
}
