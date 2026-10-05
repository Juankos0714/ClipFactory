package api

// Tests de los middlewares (internal/api/middleware.go). Los que necesitan un
// Server reusan el setupServer/do de api_test.go; los que son units puros
// (recover, statusRecorder) se prueban sin DB.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/juankos0714/clipfactory/config"
)

// ------------------------------------------------------------------- CORS

func TestCORSAllowedOrigins(t *testing.T) {
	srv := setupServer(t, func(c *config.Config) {
		c.CORSOrigins = []string{"https://clipfactory-abc.vercel.app"}
	})

	cases := []struct {
		origin  string
		allowed bool
	}{
		{"http://localhost:5173", true},                       // desarrollo: siempre
		{"https://clipfactory-abc.vercel.app", true},          // allowlist explícita
		{"https://otro-vercel-preview-xyz.vercel.app", false}, // no está en la allowlist
		{"https://evil.example.com", false},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
		req.Header.Set("Origin", tc.origin)
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)

		got := rr.Header().Get("Access-Control-Allow-Origin")
		if tc.allowed {
			if got != tc.origin {
				t.Errorf("origin %s: Access-Control-Allow-Origin = %q, want %q", tc.origin, got, tc.origin)
			}
			if rr.Header().Get("Vary") != "Origin" {
				t.Errorf("origin %s: falta Vary: Origin (cache poisoning)", tc.origin)
			}
		} else if got != "" {
			t.Errorf("origin %s: no debía recibir Access-Control-Allow-Origin, got %q", tc.origin, got)
		}
	}
}

// Preflight OPTIONS: 204 + los headers de.methods/headers, y sin cuerpo.
func TestCORSPreflight(t *testing.T) {
	srv := setupServer(t, nil)

	req := httptest.NewRequest(http.MethodOptions, "/api/sources", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", rr.Code)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Errorf("preflight sin Access-Control-Allow-Origin: %v", rr.Header())
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("preflight sin Access-Control-Allow-Methods")
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("preflight sin Access-Control-Allow-Headers")
	}
	if rr.Body.Len() != 0 {
		t.Errorf("preflight debería ir sin cuerpo, tiene %d bytes", rr.Body.Len())
	}
}

// El preflight de un origin no permitido igual responde 204, pero SIN
// Access-Control-Allow-Origin: el navegador lo bloquea igual (fail closed).
func TestCORSPreflightOriginNoPermitido(t *testing.T) {
	srv := setupServer(t, nil)

	req := httptest.NewRequest(http.MethodOptions, "/api/sources", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("origin no permitido recibió Access-Control-Allow-Origin = %q", got)
	}
}

// --------------------------------------------------------- panic recovery

func TestRecoverMiddlewareConviertePanicEn500(t *testing.T) {
	srv := &Server{}
	h := srv.recoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	var env errorEnvelope
	decodeBody(t, rr, &env)
	if env.Error.Code != "INTERNAL_ERROR" {
		t.Errorf("code = %q, want INTERNAL_ERROR", env.Error.Code)
	}
	// el panic no debe filtrar al cliente
	if got := rr.Body.String(); strings.Contains(got, "boom") {
		t.Errorf("el body filtró el panic: %s", got)
	}
}

// http.ErrAbortHandler debe re-propagarse: no es un panic del handler.
func TestRecoverMiddlewareNoTragaErrAbortHandler(t *testing.T) {
	srv := &Server{}
	h := srv.recoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler {
			t.Errorf("ErrAbortHandler no se re-propagó, recover() = %v", rec)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/health", nil))
	t.Error("se esperaba panic de http.ErrAbortHandler")
}

// ------------------------------------------------------------ logging status

// statusRecorder es lo que hace que el log refleje el status real (no siempre 200).
func TestLoggingMiddlewareCapturaElStatus(t *testing.T) {
	srv := &Server{}
	var rec *statusRecorder
	h := srv.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec = w.(*statusRecorder)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec == nil {
		t.Fatal("el handler no recibió el statusRecorder")
	}
	if rec.status != http.StatusServiceUnavailable {
		t.Errorf("status capturado = %d, want 503", rec.status)
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status pasado al writer = %d, want 503", rr.Code)
	}
}

// Un handler que solo escribe cuerpo (sin WriteHeader explícito) se loguea como 200:
// el default lo pone loggingMiddleware al crear el recorder.
func TestLoggingMiddlewareDefault200SinWriteHeader(t *testing.T) {
	srv := &Server{}
	var rec *statusRecorder
	h := srv.loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec = w.(*statusRecorder)
		_, _ = w.Write([]byte("ok"))
	}))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if rec == nil {
		t.Fatal("el handler no recibió el statusRecorder")
	}
	if rec.status != http.StatusOK {
		t.Errorf("status capturado = %d, want 200", rec.status)
	}
}
