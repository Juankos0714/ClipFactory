package api

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
	"time"
)

// ------------------------------------------------ Middleware: panic recovery

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				log.Printf("[api] panic recuperado en %s: %v", r.URL.Path, rec)
				s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "error interno del servidor")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ------------------------------------------------ Middleware: logging

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("[api] %s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, time.Since(start))
	})
}

// ------------------------------------------------------------ Middleware: CORS

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed: acceso desde el mismo origen o desde un origin de la allowlist.
// No es una frontera de seguridad; el token sigue siendo obligatorio.
func (s *Server) originAllowed(origin string) bool {
	if strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "https://*.vercel.app") {
		return true
	}
	for _, o := range s.cfg.CORSOrigins {
		if origin == o {
			return true
		}
	}
	return false
}

// -------------------------------------------------- Middleware: auth (Bearer)

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIToken == "" || r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		const prefix = "Bearer "
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, prefix) ||
			subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, prefix)), []byte(s.cfg.APIToken)) != 1 {
			s.writeError(w, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "token de API inválido o ausente (Authorization: Bearer <token>)")
			return
		}
		next.ServeHTTP(w, r)
	})
}
