package twitch

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// RateLimitError marca un 429 de la API Helix. El worker la detecta con
// errors.As y re-encola el job discovery con created_at = now + RetryAfter
// (los jobs solo se ofrecen cuando created_at <= now, así que el job "duerme"
// hasta que la ventana de rate limit se restablece).
//
// Helix rate-limita por app (800 pts/min en el tier default): un 429 no es un
// fallo del canal ni del pipeline, es señal de esperar y reintentar.
type RateLimitError struct {
	RetryAfter time.Duration // cuánto esperar antes del reintento
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("twitch: rate limit (429), reintentar en %v", e.RetryAfter)
}

// DefaultTwitchRetryAfter es la espera ante un 429 cuando Helix no envía el
// header Retry-After (no lo envía hoy). 60s cubre de sobra la ventana de
// rate limit por minuto de Helix.
const DefaultTwitchRetryAfter = 60 * time.Second

// parseRetryAfter parsea el header Retry-After (segundos, o fecha HTTP); si
// viene vacío o inválido devuelve def. Helix no lo envía hoy, pero el parser
// queda listo por si la API lo agrega (y sirve para los tests).
func parseRetryAfter(h string, def time.Duration) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return def
	}
	// formato 1: segundos
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return def
		}
		return time.Duration(secs) * time.Second
	}
	// formato 2: fecha HTTP (http.Date)
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
		return def
	}
	return def
}
