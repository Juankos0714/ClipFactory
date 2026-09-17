package adapter

// Tests de la clasificación retryable/permanent compartida por los publishers.
// La usa el worker (executePublish) para decidir entre backoff, dead-letter
// inmediata y techo de intentos.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPermanentErrorMessage(t *testing.T) {
	e := NewPermanentError("HTTP 401: credenciales inválidas")
	if !strings.Contains(e.Error(), "no reintentar") || !strings.Contains(e.Error(), "401") {
		t.Errorf("unexpected message: %s", e.Error())
	}
}

func TestNewPermanentErrorTruncatesDetail(t *testing.T) {
	detail := strings.Repeat("x", 5000)
	e := NewPermanentError(detail)
	if len(e.Detail) != ErrMessageTruncated {
		t.Errorf("expected detail truncated to %d, got %d", ErrMessageTruncated, len(e.Detail))
	}
}

func TestClassifyTokenErrorPermanent(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"invalid_grant", `{"error": "invalid_grant", "error_description": "Token has been expired or revoked."}`},
		{"invalid_client", `{"error": "invalid_client"}`},
		{"unauthorized_client", `{"error": "unauthorized_client"}`},
		{"mayúsculas", `{"error": "INVALID_GRANT"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ClassifyTokenError(400, tc.body)
			if err == nil {
				t.Fatalf("expected PermanentError for body %q", tc.body)
			}
			if !IsPermanent(err) {
				t.Errorf("expected permanent, got: %v", err)
			}
		})
	}
}

func TestClassifyTokenErrorTransient(t *testing.T) {
	// errores SIN señal de credenciales muertas: 500, body raro, vacío —
	// se asumen transitorios (nil = "no sé, tratalo como siempre")
	cases := []string{
		`{"error": "internal_error"}`,
		`<html>gateway timeout</html>`,
		``,
	}
	for _, body := range cases {
		if err := ClassifyTokenError(500, body); err != nil {
			t.Errorf("expected nil (transitorio) for body %q, got %v", body, err)
		}
	}
}

func TestIsPermanent(t *testing.T) {
	if IsPermanent(nil) {
		t.Error("nil no es permanente")
	}
	if IsPermanent(errors.New("network timeout")) {
		t.Error("error genérico = transitorio, no permanente")
	}

	// envuelto con %w: el errors.As debe encontrarlo a través de la cadena
	inner := NewPermanentError("token revocado")
	wrapped := fmt.Errorf("youtube: auth: %w", inner)
	if !IsPermanent(wrapped) {
		t.Errorf("PermanentError envuelto debe detectarse via errors.As: %v", wrapped)
	}

	// doble envoltura
	double := fmt.Errorf("publish clip 7: %w", wrapped)
	if !IsPermanent(double) {
		t.Errorf("PermanentError doblemente envuelto debe detectarse: %v", double)
	}
}

func TestMaxPublishAttemptsReasonable(t *testing.T) {
	// guardia de documento: si alguien baja el techo a 1, el backoff del
	// worker pierde sentido; si lo sube a 1000, la cola se inunda.
	if MaxPublishAttempts < 3 || MaxPublishAttempts > 50 {
		t.Errorf("MaxPublishAttempts=%d fuera del rango razonable", MaxPublishAttempts)
	}
}
