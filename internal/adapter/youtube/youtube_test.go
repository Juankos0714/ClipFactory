package youtube

// Tests del Publisher de YouTube contra un httptest.Server que emula:
//   - POST /token  → access token
//   - POST /upload → inicia sesión resumable (header Location)
//   - PUT  <location> → acepta los bytes y devuelve el videoId
//
// Mismo patrón que los tests del adaptador de Twitch.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestServer arma un Publisher + servidor fake de Google y devuelve también
// los registros de lo recibido (para verificar headers/headers/body).
type fakeGoogle struct {
	mu sync.Mutex

	tokenCalls  int
	uploadInits int

	receivedUpload []byte
	receivedMeta   videoResource
	receivedAuth   string // header Authorization del PUT

	tokenStatus  int // default 200
	initStatus   int // default 200
	finalStatus  int // default 200
	finalBody    string
	initNoLocate bool
}

func newTestPublisher(t *testing.T, fg *fakeGoogle) (*Publisher, *httptest.Server) {
	t.Helper()

	mux := http.NewServeMux()

	// endpoint OAuth
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		fg.mu.Lock()
		fg.tokenCalls++
		status := fg.tokenStatus
		fg.mu.Unlock()

		if status == 0 {
			status = http.StatusOK
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error": "invalid_client"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tokenResponse{AccessToken: "fake-access-token", ExpiresIn: 3599})
	})

	// paso 1: iniciar sesión resumable
	mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		fg.mu.Lock()
		fg.uploadInits++
		status := fg.initStatus
		noLocate := fg.initNoLocate
		fg.mu.Unlock()

		if status == 0 {
			status = http.StatusOK
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error": {"errors": [{"reason": "quotaExceeded"}]}}`)
			return
		}
		if noLocate {
			w.WriteHeader(http.StatusOK)
			return
		}

		// parsear los metadatos recibidos para verificarlos luego
		body, _ := io.ReadAll(r.Body)
		var meta videoResource
		if err := json.Unmarshal(body, &meta); err == nil {
			fg.mu.Lock()
			fg.receivedMeta = meta
			fg.mu.Unlock()
		}

		w.Header().Set("Location", "http://"+r.Host+"/upload-session-123")
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// endpoint dinámico para la session URL (el PUT va a /upload-session-123)
	mux.HandleFunc("/upload-session-123", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		fg.mu.Lock()
		fg.receivedUpload = body
		fg.receivedAuth = r.Header.Get("Authorization")
		status := fg.finalStatus
		finalBody := fg.finalBody
		fg.mu.Unlock()

		if status == 0 {
			status = http.StatusOK
		}
		if finalBody == "" {
			finalBody = `{"id": "fakeVideoId123", "kind": "youtube#video"}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, finalBody)
	})

	p := NewPublisher("cid", "csecret", "rtoken")
	p.TokenURL = ts.URL + "/token"
	p.UploadURL = ts.URL + "/upload/youtube/v3/videos"
	return p, ts
}

func newTestVideo(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, []byte("FAKE_MP4_BYTES"), 0o600); err != nil {
		t.Fatalf("write video: %v", err)
	}
	return path
}

func TestPublisherValidate(t *testing.T) {
	if err := (&Publisher{}).validate(); err == nil {
		t.Error("expected error with empty credentials")
	}
	if err := (&Publisher{ClientID: "a"}).validate(); err == nil {
		t.Error("expected error without ClientSecret")
	}
	if err := (&Publisher{ClientID: "a", ClientSecret: "b"}).validate(); err == nil {
		t.Error("expected error without RefreshToken")
	}
	if err := (&Publisher{ClientID: "a", ClientSecret: "b", RefreshToken: "c"}).validate(); err != nil {
		t.Errorf("expected valid credentials, got: %v", err)
	}
}

func TestUploadVideoFullFlow(t *testing.T) {
	fg := &fakeGoogle{}
	p, _ := newTestPublisher(t, fg)
	video := newTestVideo(t)

	id, url, err := p.UploadVideo(context.Background(), video, "Mi Clip", "Descripción", []string{"twitch", "clip"})
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if id != "fakeVideoId123" {
		t.Errorf("expected videoId 'fakeVideoId123', got %q", id)
	}
	if url != "https://youtu.be/fakeVideoId123" {
		t.Errorf("expected youtu.be URL, got %q", url)
	}

	// flujo: 1 token + 1 init + 1 PUT con los bytes
	if fg.tokenCalls != 1 {
		t.Errorf("expected 1 token call, got %d", fg.tokenCalls)
	}
	if fg.uploadInits != 1 {
		t.Errorf("expected 1 init call, got %d", fg.uploadInits)
	}
	if string(fg.receivedUpload) != "FAKE_MP4_BYTES" {
		t.Errorf("expected video bytes uploaded, got %q", string(fg.receivedUpload))
	}
	if fg.receivedAuth != "Bearer fake-access-token" {
		t.Errorf("expected Bearer auth on PUT, got %q", fg.receivedAuth)
	}

	// metadatos: título, categoría default, privacidad default
	if fg.receivedMeta.Snippet.Title != "Mi Clip" {
		t.Errorf("expected title 'Mi Clip', got %q", fg.receivedMeta.Snippet.Title)
	}
	if fg.receivedMeta.Snippet.CategoryID != "20" {
		t.Errorf("expected default category 20, got %q", fg.receivedMeta.Snippet.CategoryID)
	}
	if fg.receivedMeta.Status.PrivacyStatus != "public" {
		t.Errorf("expected default privacy 'public', got %q", fg.receivedMeta.Status.PrivacyStatus)
	}
	if fg.receivedMeta.Status.SelfDeclaredMadeForKids {
		t.Error("expected SelfDeclaredMadeForKids=false")
	}
}

func TestTokenRefreshCached(t *testing.T) {
	fg := &fakeGoogle{}
	p, _ := newTestPublisher(t, fg)
	video := newTestVideo(t)

	// dos uploads: el token debe pedirse UNA sola vez (está cacheado)
	if _, _, err := p.UploadVideo(context.Background(), video, "t1", "", nil); err != nil {
		t.Fatalf("upload 1: %v", err)
	}
	if _, _, err := p.UploadVideo(context.Background(), video, "t2", "", nil); err != nil {
		t.Fatalf("upload 2: %v", err)
	}
	if fg.tokenCalls != 1 {
		t.Errorf("expected token cached across uploads, got %d token calls", fg.tokenCalls)
	}
}

func TestTokenRefreshOnExpiry(t *testing.T) {
	fg := &fakeGoogle{}
	p, _ := newTestPublisher(t, fg)
	video := newTestVideo(t)

	// primer upload: token fresco
	if _, _, err := p.UploadVideo(context.Background(), video, "t", "", nil); err != nil {
		t.Fatalf("upload: %v", err)
	}

	// simular expiración: forzar la marca de vencimiento al pasado
	p.mu.Lock()
	p.tokenExpiry = time.Now().Add(-2 * time.Hour)
	p.mu.Unlock()

	if _, _, err := p.UploadVideo(context.Background(), video, "t", "", nil); err != nil {
		t.Fatalf("upload 2: %v", err)
	}
	if fg.tokenCalls != 2 {
		t.Errorf("expected token refresh after expiry, got %d token calls", fg.tokenCalls)
	}
}

func TestUploadQuotaExceeded(t *testing.T) {
	fg := &fakeGoogle{initStatus: http.StatusForbidden}
	p, _ := newTestPublisher(t, fg)
	video := newTestVideo(t)

	_, _, err := p.UploadVideo(context.Background(), video, "t", "", nil)
	if err == nil {
		t.Fatal("expected error for quota exceeded, got nil")
	}
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("expected RateLimitError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "cuota") {
		t.Errorf("expected 'cuota' in error, got: %v", err)
	}
}

func TestUploadRateLimitExceeded(t *testing.T) {
	// rateLimitExceeded (transitorio) también clasifica como RateLimitError
	fg := &fakeGoogle{finalStatus: http.StatusForbidden, finalBody: `{"error": {"errors": [{"reason": "rateLimitExceeded"}]}}`}
	p, _ := newTestPublisher(t, fg)
	video := newTestVideo(t)

	_, _, err := p.UploadVideo(context.Background(), video, "t", "", nil)
	if err == nil {
		t.Fatal("expected error for rate limit, got nil")
	}
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("expected RateLimitError, got %T: %v", err, err)
	}
}

func TestUploadGenericAPIError(t *testing.T) {
	fg := &fakeGoogle{initStatus: http.StatusBadRequest, finalBody: ""}
	p, _ := newTestPublisher(t, fg)
	video := newTestVideo(t)

	_, _, err := p.UploadVideo(context.Background(), video, "t", "", nil)
	if err == nil {
		t.Fatal("expected error for 400, got nil")
	}
	// NO debe clasificarse como rate limit
	var rle *RateLimitError
	if errors.As(err, &rle) {
		t.Errorf("400 no debe clasificarse como RateLimitError: %v", err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected status in error, got: %v", err)
	}
}

func TestUploadMissingFile(t *testing.T) {
	fg := &fakeGoogle{}
	p, _ := newTestPublisher(t, fg)

	_, _, err := p.UploadVideo(context.Background(), "/no/existe.mp4", "t", "", nil)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "leer video") {
		t.Errorf("expected 'leer video' in error, got: %v", err)
	}
	// no debió llamarse al endpoint de token
	if fg.tokenCalls != 0 {
		t.Errorf("expected no token call before file read, got %d", fg.tokenCalls)
	}
}

func TestUploadTitleTruncated(t *testing.T) {
	fg := &fakeGoogle{}
	p, _ := newTestPublisher(t, fg)
	video := newTestVideo(t)

	longTitle := strings.Repeat("a", 250) // límite YouTube: 100
	if _, _, err := p.UploadVideo(context.Background(), video, longTitle, strings.Repeat("d", 6000), nil); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if len(fg.receivedMeta.Snippet.Title) != 100 {
		t.Errorf("expected title truncated to 100, got %d", len(fg.receivedMeta.Snippet.Title))
	}
	if len(fg.receivedMeta.Snippet.Description) != 5000 {
		t.Errorf("expected description truncated to 5000, got %d", len(fg.receivedMeta.Snippet.Description))
	}
}

func TestUploadInitWithoutLocation(t *testing.T) {
	fg := &fakeGoogle{initNoLocate: true}
	p, _ := newTestPublisher(t, fg)
	video := newTestVideo(t)

	_, _, err := p.UploadVideo(context.Background(), video, "t", "", nil)
	if err == nil {
		t.Fatal("expected error for missing Location header, got nil")
	}
	if !strings.Contains(err.Error(), "Location") {
		t.Errorf("expected 'Location' in error, got: %v", err)
	}
}

func TestTokenEndpointError(t *testing.T) {
	fg := &fakeGoogle{tokenStatus: http.StatusUnauthorized}
	p, _ := newTestPublisher(t, fg)
	video := newTestVideo(t)

	_, _, err := p.UploadVideo(context.Background(), video, "t", "", nil)
	if err == nil {
		t.Fatal("expected error for token endpoint failure, got nil")
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("expected 'auth' in error chain, got: %v", err)
	}
}
