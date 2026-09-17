// Package api implementa la API REST aditiva de ClipFactory (comando 'server').
//
// Es ADITIVO al backend CLI existente: no toca el worker ni modifica la lógica
// de negocio. Solo expone operaciones sobre la misma SQLite (paquete internal/db,
// reutilizado tal cual) y encola jobs con db.EnsureActiveJob / db.EnqueueJob —
// la máquina de estados y los ejecutores siguen viviendo en internal/worker.
//
// Convenciones del contrato (docs/API_CONTRACT.md §2):
//   - Errores con envoltorio {error:{code,message,details}}.
//   - Listas con envelope de paginación {data:[...],pagination:{...}}.
//   - Timestamps RFC3339 UTC, IDs int64, JSON snake_case.
//
// Auth: Bearer token (cfg.APIToken) OPCIONAL. Si el token está vacío la API es
// pública (desarrollo); si no, todo exige Authorization: Bearer <token> salvo
// GET /api/health. No se exponen secretos nunca.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/juankos0714/clipfactory/config"
)

// DefaultAddr es la dirección de escucha si el operador no configura una.
const DefaultAddr = ":8080"

// Server es la API HTTP aditiva. Tiene la DB compartida y la config del proceso.
// No ejecuta jobs: solo lee la DB (internal/db) y encola (EnsureActiveJob /
// EnqueueJob) para que el worker los procese.
type Server struct {
	cfg *config.Config
	db  *sql.DB
}

// NewServer construye la API sobre la conexión SQLite compartida.
func NewServer(cfg *config.Config, db *sql.DB) *Server {
	return &Server{cfg: cfg, db: db}
}

// Lista el router con la cadena de middlewares:
// recover → logging → CORS → auth → rutas.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.routes()
	h = s.authMiddleware(h)
	h = s.corsMiddleware(h)
	h = s.loggingMiddleware(h)
	h = s.recoverMiddleware(h)
	return h
}

// Run arranca el servidor HTTP en addr (vacío = DefaultAddr) y bloquea hasta
// que el contexto se cancele (shutdown graceful) o el servidor falle.
func (s *Server) Run(ctx context.Context, addr string) error {
	if addr == "" {
		addr = DefaultAddr
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("[api] escuchando en %s", addr)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		log.Println("[api] señal recibida, apagando gracefully...")
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// ------------------------------------------------------------- JSON plumbing

type errorEnvelope struct {
	Error struct {
		Code    string      `json:"code"`
		Message string      `json:"message"`
		Details interface{} `json:"details"`
	} `json:"error"`
}

// writeError escribe el envelope de error estándar del contrato.
func (s *Server) writeError(w http.ResponseWriter, status int, code, message string) {
	var e errorEnvelope
	e.Error.Code = code
	e.Error.Message = message
	s.writeJSON(w, status, e)
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[api] error escribiendo JSON: %v", err)
	}
}

// decodeJSON decodifica un body JSON; devuelve error de validación si no es JSON.
func (s *Server) decodeJSON(r *http.Request, dst interface{}) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

// paginationDTO es el envelope de paginación del contrato §2.2.
type paginationDTO struct {
	Page      int  `json:"page"`
	PageSize  int  `json:"pageSize"`
	Total     int  `json:"total"`
	PageCount int  `json:"pageCount"`
	HasNext   bool `json:"hasNext"`
	HasPrev   bool `json:"hasPrev"`
}

func pagination(page, pageSize, total int) paginationDTO {
	pageCount := 0
	if total > 0 {
		pageCount = (total + pageSize - 1) / pageSize
	}
	return paginationDTO{
		Page:      page,
		PageSize:  pageSize,
		Total:     total,
		PageCount: pageCount,
		HasNext:   page < pageCount,
		HasPrev:   page > 1,
	}
}

// listEnvelope es el envelope de listas {data, pagination}.
type listEnvelope[T any] struct {
	Data       []T           `json:"data"`
	Pagination paginationDTO `json:"pagination"`
}

// pageParams parsea page/pageSize con las reglas del contrato: page >= 1,
// pageSize entre 1 y 100 (default 50). Fuera de rango o inválido → 400.
func (s *Server) pageParams(r *http.Request, w http.ResponseWriter) (page, pageSize int, ok bool) {
	page = 1
	if v := r.URL.Query().Get("page"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "page debe ser un entero >= 1")
			return 0, 0, false
		}
		page = p
	}
	pageSize = 50
	if v := r.URL.Query().Get("pageSize"); v != "" {
		ps, err := strconv.Atoi(v)
		if err != nil || ps < 1 || ps > 100 {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "pageSize debe estar entre 1 y 100")
			return 0, 0, false
		}
		pageSize = ps
	}
	return page, pageSize, true
}

// pathID devuelve el ID numérico de un segmento de ruta ({id}); false si no es
// un entero positivo (el caller escribe el 400).
func pathID(r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(key), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// validaRFC3339 informa si un query de fecha es RFC3339 válido.
func (s *Server) validaRFC3339(v string) bool {
	_, err := time.Parse(time.RFC3339, v)
	return err == nil
}

// isUniqueErr detecta la violación de UNIQUE de SQLite (p.ej. InsertSource).
func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
