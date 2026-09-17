package meta

// Tests de la reconciliación anti-duplicados: FindRecentByMarker recorre los
// videos recientes de la página buscando el marker determinista.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetaFindRecentByMarkerHit(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[
			{"id":"fb_old","description":"video sin marker"},
			{"id":"fb_hit","description":"Clip — Clip generado con ClipFactory\ncf-3-8 cf-3"}
		]}`)
	}))
	defer srv.Close()

	p := NewPublisher("page123", "token", "v21.0")
	p.SetGraphURL(srv.URL)

	extID, extURL, err := p.FindRecentByMarker(context.Background(), []string{"cf-3-8", "cf-3"})
	if err != nil {
		t.Fatalf("FindRecentByMarker: %v", err)
	}
	if extID != "fb_hit" {
		t.Errorf("expected fb_hit, got %q", extID)
	}
	if extURL != "https://www.facebook.com/page123/videos/fb_hit" {
		t.Errorf("unexpected URL %q", extURL)
	}
	// UNA llamada de lectura (más el token va por header, no por endpoint)
	if len(paths) != 1 {
		t.Errorf("expected 1 API call, got %v", paths)
	}
}

func TestMetaFindRecentByMarkerMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"fb_x","description":"nada que ver"}]}`)
	}))
	defer srv.Close()

	p := NewPublisher("page123", "token", "v21.0")
	p.SetGraphURL(srv.URL)

	extID, extURL, err := p.FindRecentByMarker(context.Background(), []string{"cf-99-99"})
	if err != nil {
		t.Fatalf("FindRecentByMarker: %v", err)
	}
	if extID != "" || extURL != "" {
		t.Errorf("expected empty (no match), got %q %q", extID, extURL)
	}
}

func TestMetaFindRecentByMarkerNoMarkers(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()

	p := NewPublisher("page123", "token", "v21.0")
	p.SetGraphURL(srv.URL)

	extID, _, err := p.FindRecentByMarker(context.Background(), nil)
	if err != nil || extID != "" {
		t.Errorf("expected no-op, got %q %v", extID, err)
	}
	if called {
		t.Error("no debe llamar a la API sin markers")
	}
}

func TestMetaFindRecentByMarkerAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"boom","code":2}}`)
	}))
	defer srv.Close()

	p := NewPublisher("page123", "token", "v21.0")
	p.SetGraphURL(srv.URL)

	if _, _, err := p.FindRecentByMarker(context.Background(), []string{"cf-1-1"}); err == nil {
		t.Fatal("expected error when the API fails")
	}
}
