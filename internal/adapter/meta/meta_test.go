package meta

// Tests del Publisher de Meta/Facebook contra un httptest.Server (sin red
// real), replicando el estilo de los tests de internal/adapter/youtube.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/juankos0714/clipfactory/internal/adapter"
)

// writeTempVideo crea un archivo de video falso para los uploads.
func writeTempVideo(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(path, []byte("video-bytes"), 0o600); err != nil {
		t.Fatalf("write temp video: %v", err)
	}
	return path
}

// newTestServer arma un Publisher apuntando a un servidor que responde la
// secuencia de requests en `handlers`. Cada handler recibe la request y escribe
// la respuesta; verify opcional chequea lo recibido y devuelve error de test.
func newTestPublisher(t *testing.T, status int, body string, verify func(r *http.Request, bodyBytes []byte)) (*Publisher, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bs, _ := io.ReadAll(r.Body)
		if verify != nil {
			verify(r, bs)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	p := NewPublisher("page123", "token-abc", "v21.0")
	p.SetGraphURL(srv.URL)
	return p, srv
}

func TestUploadVideoMultipartOK(t *testing.T) {
	videoPath := writeTempVideo(t)

	p, _ := newTestPublisher(t, http.StatusOK, `{"id":"fb_video_42"}`, func(r *http.Request, body []byte) {
		if !strings.Contains(r.URL.Path, "/v21.0/page123/videos") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("expected multipart content type, got %q", ct)
		}
		// el body multipart debe incluir upload_type=reel y el archivo
		bs := string(body)
		if !strings.Contains(bs, `name="upload_type"`) || !strings.Contains(bs, "reel") {
			t.Error("expected upload_type=reel in multipart body")
		}
		if !strings.Contains(bs, `name="source"`) {
			t.Error("expected source file field in multipart body")
		}
		if !strings.Contains(bs, "video-bytes") {
			t.Error("expected video bytes in multipart body")
		}
	})

	id, url, err := p.UploadVideo(context.Background(), videoPath, "Título", "desc", []string{"tag"})
	if err != nil {
		t.Fatalf("UploadVideo: %v", err)
	}
	if id != "fb_video_42" {
		t.Errorf("expected external id 'fb_video_42', got %q", id)
	}
	if url != "https://www.facebook.com/page123/videos/fb_video_42" {
		t.Errorf("unexpected url: %q", url)
	}
}

func TestUploadVideoFileURLOK(t *testing.T) {
	videoPath := writeTempVideo(t)

	p, _ := newTestPublisher(t, http.StatusOK, `{"id":"fb_url_7"}`, func(r *http.Request, body []byte) {
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
			t.Errorf("expected form content type in file_url mode, got %q", ct)
		}
		bs := string(body)
		if !strings.Contains(bs, "upload_type=reel") {
			t.Error("expected upload_type=reel in form body")
		}
		if !strings.Contains(bs, "file_url=https%3A%2F%2Ffiles.example.com") {
			t.Errorf("expected encoded file_url in body: %s", bs)
		}
	})
	p.SetFilesBaseURL("https://files.example.com")

	id, _, err := p.UploadVideo(context.Background(), videoPath, "T", "d", nil)
	if err != nil {
		t.Fatalf("UploadVideo: %v", err)
	}
	if id != "fb_url_7" {
		t.Errorf("expected 'fb_url_7', got %q", id)
	}
}

func TestUploadVideoValidationError(t *testing.T) {
	// sin token
	p := NewPublisher("page123", "", "v21.0")
	if _, _, err := p.UploadVideo(context.Background(), "x.mp4", "", "", nil); err == nil {
		t.Error("expected error for missing access token")
	}

	// sin page id
	p2 := NewPublisher("", "tok", "v21.0")
	if _, _, err := p2.UploadVideo(context.Background(), "x.mp4", "", "", nil); err == nil {
		t.Error("expected error for missing page id")
	}
}

func TestUploadVideoMissingFile(t *testing.T) {
	p, _ := newTestPublisher(t, http.StatusOK, `{"id":"x"}`, nil)
	if _, _, err := p.UploadVideo(context.Background(), "/no/existe/video.mp4", "", "", nil); err == nil {
		t.Error("expected error for missing video file (multipart mode)")
	}
}

func TestAPIErrorGeneric(t *testing.T) {
	p, _ := newTestPublisher(t, http.StatusBadRequest, `{"error":{"message":"Invalid parameter","type":"OAuthException","code":100}}`, nil)

	_, _, err := p.UploadVideo(context.Background(), writeTempVideo(t), "", "", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var rle *RateLimitError
	if asRateLimit(err, &rle) {
		t.Errorf("code 100 no debe clasificarse como rate limit: %v", err)
	}
	if !strings.Contains(err.Error(), "código 100") {
		t.Errorf("expected graph error code in message, got: %v", err)
	}
}

func TestAPIErrorPermanent(t *testing.T) {
	// 401: token inválido/expirado → permanente, NO rate limit
	p401, _ := newTestPublisher(t, http.StatusUnauthorized, `{"error":{"message":"Invalid OAuth access token","type":"OAuthException","code":190}}`, nil)
	_, _, err := p401.UploadVideo(context.Background(), writeTempVideo(t), "", "", nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	var rle401 *RateLimitError
	if asRateLimit(err, &rle401) {
		t.Errorf("401 no debe clasificarse como rate limit: %v", err)
	}
	var pe401 *adapter.PermanentError
	if !errors.As(err, &pe401) {
		t.Fatalf("401 debe clasificarse como *adapter.PermanentError, got %T: %v", err, err)
	}

	// 403: sin permisos sobre la página → permanente también
	p403, _ := newTestPublisher(t, http.StatusForbidden, `{"error":{"message":"Requires pages_manage_videos","type":"OAuthException","code":200}}`, nil)
	_, _, err = p403.UploadVideo(context.Background(), writeTempVideo(t), "", "", nil)
	if err == nil {
		t.Fatal("expected error for 403")
	}
	var pe403 *adapter.PermanentError
	if !errors.As(err, &pe403) {
		t.Fatalf("403 debe clasificarse como *adapter.PermanentError, got %T: %v", err, err)
	}

	// 500: transitorio (sin clasificar) — el techo del worker lo acota
	p500, _ := newTestPublisher(t, http.StatusInternalServerError, `oops`, nil)
	_, _, err = p500.UploadVideo(context.Background(), writeTempVideo(t), "", "", nil)
	if err == nil {
		t.Fatal("expected error for 500")
	}
	var pe500 *adapter.PermanentError
	if errors.As(err, &pe500) {
		t.Errorf("500 no debe ser permanente: %v", err)
	}
}

func TestAPIErrorRateLimit(t *testing.T) {
	// code 4 = application request limit reached (rate limit transitorio)
	p, _ := newTestPublisher(t, http.StatusBadRequest, `{"error":{"message":"Application request limit reached","type":"OAuthException","code":4}}`, nil)

	_, _, err := p.UploadVideo(context.Background(), writeTempVideo(t), "", "", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var rle *RateLimitError
	if !asRateLimit(err, &rle) {
		t.Fatalf("expected *RateLimitError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "code 4") && !strings.Contains(err.Error(), "código 4") {
		t.Errorf("expected code detail in error, got: %v", err)
	}
}

func TestAPIEmptyIDFails(t *testing.T) {
	p, _ := newTestPublisher(t, http.StatusOK, `{}`, nil)
	if _, _, err := p.UploadVideo(context.Background(), writeTempVideo(t), "", "", nil); err == nil {
		t.Error("expected error for response without id")
	}
}

func TestRateLimitErrorMessage(t *testing.T) {
	e := &RateLimitError{Detail: "código 17: user request limit reached"}
	if !strings.Contains(e.Error(), "rate limit") || !strings.Contains(e.Error(), "17") {
		t.Errorf("unexpected message: %s", e.Error())
	}
}

// asRateLimit es errors.As re-declarado localmente para no importar el paquete
// solo por dos tests (mismo contrato).
func asRateLimit(err error, target **RateLimitError) bool {
	for err != nil {
		if rl, ok := err.(*RateLimitError); ok {
			*target = rl
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// jsonSnippet decodifica un JSON y devuelve un campo como string (helper de
// verificación en tests de form bodies).
func jsonSnippet(t *testing.T, body []byte, key string) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		return ""
	}
	return m[key]
}
