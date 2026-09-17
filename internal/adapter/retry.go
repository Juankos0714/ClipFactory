// Package adapter contiene los adaptadores a servicios externos (twitch,
// kick, youtube, meta, ffmpeg). Este archivo define la clasificación de
// errores de PUBLICACIÓN que comparten los publishers (youtube, meta): el
// worker la usa para decidir entre reintentar con backoff o marcar la
// publicación como fallida definitivamente.
package adapter

import (
	"errors"
	"strings"
)

// MaxPublishAttempts es el techo de reintentos de una publicación. Al
// alcanzarlo, el worker marca la publication 'failed' (dead-letter) y NO
// re-encola más jobs publish para ella: sin techo, un clip cuyo upload falla
// para siempre ocuparía la cola cada 24h indefinidamente.
//
// 10 intentos con backoff 1h→2h→4h→8h→16h→24h(cap) cubren ~5 días de
// reintentos antes de rendirse, margen razonable para caídas de la API.
const MaxPublishAttempts = 10

// PermanentError marca un fallo de publicación que NO va a resolver un
// reintento: credenciales inválidas/revocadas, parámetros rechazados por la
// API, archivo rechazado, permisos insuficientes, etc. El worker la detecta
// con errors.As y marca la publication 'failed' SIN reintentar.
//
// Criterio: el operador debe corregir algo (credenciales, metadata, contenido)
// antes de que el mismo clip pueda publicarse; reintentar automático sería
// ruido y, en el peor caso, más errores 401 contra la API.
type PermanentError struct {
	Detail string // causa específica, para error_message de la publication
}

func (e *PermanentError) Error() string {
	return "error permanente (no reintentar): " + e.Detail
}

// ErrMessageTruncated limita el detalle guardado en publications.error_message
// (TEXT sin límite en SQLite, pero un error HTTP completo puede traer PII o
// basura kilométrica; 512 chars alcanzan para diagnosticar).
const ErrMessageTruncated = 512

// NewPermanentError construye un PermanentError con detalle recortado.
func NewPermanentError(detail string) *PermanentError {
	if len(detail) > ErrMessageTruncated {
		detail = detail[:ErrMessageTruncated]
	}
	return &PermanentError{Detail: detail}
}

// oauthPermanentTokens son las respuestas del token endpoint de Google que
// indican credenciales muertas: reintentar no las revive.
var oauthPermanentTokens = []string{
	"invalid_grant",  // refresh_token revocado o expirado (el caso común)
	"invalid_client", // client_id/secret incorrectos
	"unauthorized_client",
}

// ClassifyTokenError clasifica un fallo del endpoint OAuth de token: si el
// cuerpo contiene invalid_grant/invalid_client/unauthorized_client es
// permanente (credenciales revocadas); cualquier otra cosa (500, timeout de
// red, body ilegible) se asume transitoria.
func ClassifyTokenError(status int, body string) error {
	lower := strings.ToLower(body)
	for _, tok := range oauthPermanentTokens {
		if strings.Contains(lower, tok) {
			return NewPermanentError(
				"credenciales OAuth rechazadas (" + tok + "): renovar refresh_token/credenciales")
		}
	}
	// sin señal conocida: transitorio (la API de tokens puede fallar puntualmente)
	return nil
}

// IsPermanent recorre la cadena del error y devuelve true si es permanente
// (no reintentar). Los RateLimitError ya los maneja el worker por separado
// (waiting_rate_limit); acá solo importa la dicotomía retryable/permanent.
// Errores desconocidos (network timeout, 5xx, cualquier cosa no clasificada)
// se asumen TRANSITORIOS: es la elección conservadora — el techo
// MaxPublishAttempts garantiza que igualmente no reintenta para siempre.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	var pe *PermanentError
	return errors.As(err, &pe)
}
