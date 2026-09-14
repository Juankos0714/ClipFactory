package twitch

// Tests del TwitchAdapter contra un httptest.Server: validación de credenciales,
// construcción del request Helix (headers Client-ID/Authorization, query params)
// y manejo de errores HTTP.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateCredentials(t *testing.T) {
	if err := (&TwitchAdapter{}).ValidateCredentials(); err == nil {
		t.Error("expected error when ClientID is empty")
	}

	if err := (&TwitchAdapter{ClientID: "abc"}).ValidateCredentials(); err != nil {
		t.Errorf("expected valid credentials, got: %v", err)
	}
}

func TestNewTwitchAdapter(t *testing.T) {
	a := NewTwitchAdapter("client-1", "token-1")
	if a.ClientID != "client-1" {
		t.Errorf("expected ClientID 'client-1', got '%s'", a.ClientID)
	}
	if a.AuthToken != "token-1" {
		t.Errorf("expected AuthToken 'token-1', got '%s'", a.AuthToken)
	}
	if a.HTTPClient == nil {
		t.Error("expected HTTPClient to be initialized")
	}
	if a.HTTPClient.Timeout != 30*time.Second {
		t.Errorf("expected HTTPClient timeout 30s, got %v", a.HTTPClient.Timeout)
	}
}

func TestListClipsRequest(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	var gotClientID, gotAuth string
	var gotMethod string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		gotClientID = r.Header.Get("Client-ID")
		gotAuth = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
	}))
	defer ts.Close()

	a := NewTwitchAdapter("my-client-id", "my-token")
	a.SetBaseURL(ts.URL)

	after := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	clips, err := a.ListClips(context.Background(), "12345", after, 1)
	if err != nil {
		t.Fatalf("list clips: %v", err)
	}
	if len(clips) != 0 {
		t.Errorf("expected 0 clips for empty data, got %d", len(clips))
	}

	if gotMethod != http.MethodGet {
		t.Errorf("expected GET, got %s", gotMethod)
	}
	if gotPath != "/helix/clips" {
		t.Errorf("expected path /helix/clips, got %s", gotPath)
	}
	if gotClientID != "my-client-id" {
		t.Errorf("expected Client-ID header 'my-client-id', got '%s'", gotClientID)
	}
	if gotAuth != "Bearer my-token" {
		t.Errorf("expected Authorization header 'Bearer my-token', got '%s'", gotAuth)
	}
	if gotQuery.Get("broadcaster_id") != "12345" {
		t.Errorf("expected broadcaster_id '12345', got '%s'", gotQuery.Get("broadcaster_id"))
	}
	if gotQuery.Get("first") != "100" {
		t.Errorf("expected first '100', got '%s'", gotQuery.Get("first"))
	}
	if gotQuery.Get("started_at") != "2026-09-01T12:00:00Z" {
		t.Errorf("expected started_at '2026-09-01T12:00:00Z', got '%s'", gotQuery.Get("started_at"))
	}
}

func TestListClipsNoAuthToken(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("expected no Authorization header, got '%s'", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
	}))
	defer ts.Close()

	a := NewTwitchAdapter("my-client-id", "")
	a.SetBaseURL(ts.URL)

	if _, err := a.ListClips(context.Background(), "12345", time.Time{}, 1); err != nil {
		t.Fatalf("list clips: %v", err)
	}
}

func TestListClipsHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
	}))
	defer ts.Close()

	a := NewTwitchAdapter("my-client-id", "bad-token")
	a.SetBaseURL(ts.URL)

	_, err := a.ListClips(context.Background(), "12345", time.Time{}, 1)
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
	want := "api returned status 401"
	if len(err.Error()) < len(want) || err.Error()[len(err.Error())-len(want):] != want {
		t.Errorf("expected error to end with %q, got %q", want, err.Error())
	}
}

func TestListClipsServerDown(t *testing.T) {
	a := NewTwitchAdapter("my-client-id", "")
	// servidor cerrado: el request debe fallar
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts.Close()
	a.SetBaseURL(ts.URL)

	_, err := a.ListClips(context.Background(), "12345", time.Time{}, 1)
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

func TestListClipsRequiresCredentials(t *testing.T) {
	a := &TwitchAdapter{} // sin ClientID
	if _, err := a.ListClips(context.Background(), "12345", time.Time{}, 1); err == nil {
		t.Error("expected error when ClientID is empty")
	}
}

func TestListClipsContextCancelled(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer ts.Close()

	a := NewTwitchAdapter("my-client-id", "")
	a.SetBaseURL(ts.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelar inmediatamente

	if _, err := a.ListClips(ctx, "12345", time.Time{}, 1); err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestDownloadClipValidations(t *testing.T) {
	a := NewTwitchAdapter("my-client-id", "")

	if err := a.DownloadClip(context.Background(), "", "/tmp/clip.mp4"); err == nil {
		t.Error("expected error for empty clipID")
	}
	if err := a.DownloadClip(context.Background(), "clip123", ""); err == nil {
		t.Error("expected error for empty destPath")
	}
}

func TestDownloadClipMissingBinary(t *testing.T) {
	a := NewTwitchAdapter("my-client-id", "")
	a.SetDownloaderPath("/no/existe/twitchdownloader-xyz")

	err := a.DownloadClip(context.Background(), "clip123", filepath.Join(t.TempDir(), "clip.mp4"))
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
	// el error debe ser claro: el binario no existe
	if !strings.Contains(err.Error(), "downloader falló") {
		t.Errorf("expected 'downloader falló' in error, got: %v", err)
	}
}

func TestDownloadClipSuccess(t *testing.T) {
	// fake de TwitchDownloaderCLI: escribe un archivo donde se le pida y sale 0
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-downloader")
	script := "#!/bin/sh\nout=\"\"\nwhile [ $# -gt 0 ]; do case \"$1\" in -o) out=\"$2\"; shift 2;; *) shift;; esac; done\nprintf 'FAKE_VIDEO_CONTENT' > \"$out\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake downloader: %v", err)
	}

	a := NewTwitchAdapter("my-client-id", "")
	a.SetDownloaderPath(fake)

	dest := filepath.Join(dir, "incoming", "clip123.mp4")
	if err := a.DownloadClip(context.Background(), "clip123", dest); err != nil {
		t.Fatalf("download clip: %v", err)
	}

	// el archivo final debe existir con el contenido del fake
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != "FAKE_VIDEO_CONTENT" {
		t.Errorf("expected FAKE_VIDEO_CONTENT, got %q", string(data))
	}
	// y no debe quedar el .part
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Errorf("expected .part file to be gone, stat err: %v", err)
	}
}

func TestDownloadClipFailingBinary(t *testing.T) {
	// fake que falla y escribe a stderr (como el CLI real con un clip inexistente)
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-downloader-fail")
	script := "#!/bin/sh\necho 'FATAL: clip not found' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake failing downloader: %v", err)
	}

	a := NewTwitchAdapter("my-client-id", "")
	a.SetDownloaderPath(fake)

	err := a.DownloadClip(context.Background(), "clip404", filepath.Join(dir, "clip404.mp4"))
	if err == nil {
		t.Fatal("expected error from failing binary, got nil")
	}
	if !strings.Contains(err.Error(), "clip not found") {
		t.Errorf("expected stderr content in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("expected exit status in error, got: %v", err)
	}
	// no debe haber quedado ningún archivo
	if _, err := os.Stat(filepath.Join(dir, "clip404.mp4")); !os.IsNotExist(err) {
		t.Error("expected no output file after failed download")
	}
}

func TestDownloadClipEmptyOutput(t *testing.T) {
	// fake que sale 0 pero crea un archivo VACÍO en el destino -o: debe detectarse
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-downloader-empty")
	script := "#!/bin/sh\nout=\"\"\nwhile [ $# -gt 0 ]; do case \"$1\" in -o) out=\"$2\"; shift 2;; *) shift;; esac; done\n: > \"$out\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake empty downloader: %v", err)
	}

	a := NewTwitchAdapter("my-client-id", "")
	a.SetDownloaderPath(fake)

	err := a.DownloadClip(context.Background(), "clip123", filepath.Join(dir, "clip.mp4"))
	if err == nil {
		t.Fatal("expected error for empty output file, got nil")
	}
	if !strings.Contains(err.Error(), "archivo vacío") {
		t.Errorf("expected 'archivo vacío' in error, got: %v", err)
	}
}

func TestDownloadClipContextCancelled(t *testing.T) {
	// fake que tarda 5s: con el contexto cancelado, el exec debe abortar antes
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-downloader-slow")
	script := "#!/bin/sh\nsleep 5\nprintf 'late' > \"$5\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake slow downloader: %v", err)
	}

	a := NewTwitchAdapter("my-client-id", "")
	a.SetDownloaderPath(fake)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := a.DownloadClip(ctx, "clip123", filepath.Join(dir, "clip.mp4"))
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
}

func TestImplementsPlatformAdapter(t *testing.T) {
	var _ PlatformAdapter = (*TwitchAdapter)(nil)
}
