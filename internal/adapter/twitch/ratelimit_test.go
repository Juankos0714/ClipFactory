package twitch

// Tests del manejo de rate limit (429) del adapter Helix: el 429 debe
// convertirse en *RateLimitError con Retry-After parseado (header si viene,
// default si no), distinguible de otros errores HTTP.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListClips429BecomesRateLimitError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 429 SIN header Retry-After (lo que hace Helix hoy)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	a := NewTwitchAdapter("my-client-id", "token")
	a.SetBaseURL(ts.URL)

	_, err := a.ListClips(context.Background(), "12345", time.Time{}, 1)
	if err == nil {
		t.Fatal("expected error for 429, got nil")
	}
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("429 debe clasificarse *RateLimitError, got %T: %v", err, err)
	}
	if rle.RetryAfter != DefaultTwitchRetryAfter {
		t.Errorf("expected default retry-after %v, got %v", DefaultTwitchRetryAfter, rle.RetryAfter)
	}
}

func TestListClips429WithRetryAfterHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	a := NewTwitchAdapter("my-client-id", "token")
	a.SetBaseURL(ts.URL)

	_, err := a.ListClips(context.Background(), "12345", time.Time{}, 1)
	if err == nil {
		t.Fatal("expected error for 429, got nil")
	}
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("expected *RateLimitError, got %T: %v", err, err)
	}
	if rle.RetryAfter != 7*time.Second {
		t.Errorf("expected RetryAfter=7s del header, got %v", rle.RetryAfter)
	}
}

func TestListClips429OnSecondPage(t *testing.T) {
	// el 429 puede llegar en la página N de la paginación: ListClips debe
	// propagar el RateLimitError (envuelto en "page N:") sin convertirlo en
	// error genérico — el errors.As del worker atraviesa el %w.
	pages := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if pages == 1 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"data":[],"pagination":{"cursor":"CUR-1"}}`))
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	a := NewTwitchAdapter("my-client-id", "token")
	a.SetBaseURL(ts.URL)

	_, err := a.ListClips(context.Background(), "12345", time.Time{}, 0) // sin límite de páginas
	if err == nil {
		t.Fatal("expected error for 429 on page 2, got nil")
	}
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("429 en página 2 debe seguir siendo *RateLimitError, got %T: %v", err, err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name string
		hdr  string
		want time.Duration
	}{
		{"vacío → default", "", DefaultTwitchRetryAfter},
		{"segundos", "30", 30 * time.Second},
		{"negativo → default", "-5", DefaultTwitchRetryAfter},
		{"no numérico → default", "pronto", DefaultTwitchRetryAfter},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfter(tc.hdr, DefaultTwitchRetryAfter); got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.hdr, got, tc.want)
			}
		})
	}
}
