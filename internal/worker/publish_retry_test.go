package worker

// Tests de la política de reintentos de executePublish: clasificación
// retryable/permanent (adapter.IsPermanent), dead-letter en 'failed' sin
// reintentos para errores permanentes, y techo MaxPublishAttempts para los
// transitorios (con backoff mientras haya intentos disponibles).

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/adapter"
	"github.com/juankos0714/clipfactory/internal/db"
)

// permanentPublisher falla SIEMPRE con un error permanente (simula
// credenciales revocadas: 401 de la API / invalid_grant del token endpoint).
type permanentPublisher struct {
	uploads int
}

func (p *permanentPublisher) UploadVideo(ctx context.Context, videoPath, title, description string, tags []string) (string, string, error) {
	p.uploads++
	return "", "", adapter.NewPermanentError("HTTP 401: credenciales inválidas (invalid_grant)")
}

func TestExecutePublishPermanentFailsImmediately(t *testing.T) {
	p := &permanentPublisher{}
	w, publication, _ := setupPublishWorker(t, p)

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	// el error SÍ se propaga (job 'error'): es un fallo real del pipeline
	if err := w.executeJob(context.Background(), *job); err == nil {
		t.Fatal("expected error from executeJob on permanent failure")
	}

	got, err := db.GetPublicationByID(w.db, publication.ID)
	if err != nil || got == nil {
		t.Fatalf("get publication: %v %v", err, got)
	}
	// DEAD-LETTER: 'failed' en el PRIMER intento, sin next_retry_at
	if got.Status != "failed" {
		t.Errorf("expected status 'failed' (dead-letter), got '%s'", got.Status)
	}
	if got.Attempts != 1 {
		t.Errorf("expected attempts=1, got %d", got.Attempts)
	}
	if got.NextRetryAt != nil {
		t.Errorf("permanent no debe programar reintento, got next_retry_at=%v", got.NextRetryAt)
	}
	if !strings.Contains(got.ErrorMessage, "401") {
		t.Errorf("expected cause in error_message, got '%s'", got.ErrorMessage)
	}

	// el publisher fue llamado UNA sola vez: no se reintenta
	if p.uploads != 1 {
		t.Errorf("permanent error no debe reintentar, publisher llamado %d veces", p.uploads)
	}

	// el job quedó 'error' (no 'done': no es un no-op; visible en monitoreo)
	var status string
	if err := w.db.QueryRow("SELECT status FROM jobs WHERE id = ?", job.ID).Scan(&status); err != nil {
		t.Fatalf("query job: %v", err)
	}
	if status != "error" {
		t.Errorf("expected job 'error', got '%s'", status)
	}
}

// TestExecutePublishTransientExhaustsAttempts verifica el techo: con
// attempts ya en MaxPublishAttempts-1, un fallo transitorio más mata la
// publication ('failed') en vez de programar otro backoff.
func TestExecutePublishTransientExhaustsAttempts(t *testing.T) {
	pub := &fakePublisher{writeOutput: false} // error genérico (transitorio)
	w, publication, _ := setupPublishWorker(t, pub)

	// simular que ya se agotaron casi todos los intentos
	last := adapter.MaxPublishAttempts - 1
	if err := db.UpdatePublicationStatus(w.db, publication.ID, "error", "", "", "fallos previos", nil, nil, false); err != nil {
		t.Fatalf("seed attempts: %v", err)
	}
	if _, err := w.db.Exec(`UPDATE publications SET attempts = ? WHERE id = ?`, last, publication.ID); err != nil {
		t.Fatalf("set attempts: %v", err)
	}

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	if err := w.executeJob(context.Background(), *job); err == nil {
		t.Fatal("expected error from executeJob when attempts exhausted")
	}

	got, err := db.GetPublicationByID(w.db, publication.ID)
	if err != nil || got == nil {
		t.Fatalf("get publication: %v %v", err, got)
	}
	if got.Status != "failed" {
		t.Errorf("expected dead-letter 'failed' tras agotar %d intentos, got '%s'", adapter.MaxPublishAttempts, got.Status)
	}
	if got.Attempts != adapter.MaxPublishAttempts {
		t.Errorf("expected attempts=%d, got %d", adapter.MaxPublishAttempts, got.Attempts)
	}
	if got.NextRetryAt != nil {
		t.Errorf("agotados los intentos no debe haber next_retry_at, got %v", got.NextRetryAt)
	}
	if !strings.Contains(got.ErrorMessage, "agotados") {
		t.Errorf("expected 'agotados' in error_message, got '%s'", got.ErrorMessage)
	}
}

// TestExecutePublishTransientStillBacksOff verifica que ANTES del techo el
// comportamiento transitorio no cambió: 'error' + backoff + re-encolado.
func TestExecutePublishTransientStillBacksOff(t *testing.T) {
	pub := &fakePublisher{writeOutput: false}
	w, publication, _ := setupPublishWorker(t, pub)

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	if err := w.executeJob(context.Background(), *job); err == nil {
		t.Fatal("expected error from executeJob on transient failure")
	}

	got, err := db.GetPublicationByID(w.db, publication.ID)
	if err != nil || got == nil {
		t.Fatalf("get publication: %v %v", err, got)
	}
	if got.Status != "error" {
		t.Errorf("expected 'error' (aún hay intentos), got '%s'", got.Status)
	}
	if got.NextRetryAt == nil {
		t.Fatal("expected next_retry_at (backoff sigue activo antes del techo)")
	}
	if until := time.Until(*got.NextRetryAt); until < 55*time.Minute || until > 65*time.Minute {
		t.Errorf("expected ~1h backoff (2^0), got %v", until)
	}
}

// TestExecutePublishPermanentWrapped verifica el errors.As a través de la
// cadena: los adapters envuelven los permanentes con %w ("youtube: auth: ..."),
// así que el worker debe detectarlos aunque vengan envueltos.
func TestExecutePublishPermanentWrapped(t *testing.T) {
	// publisher que devuelve el PermanentError envuelto, como hace
	// youtube.UploadVideo con su fmt.Errorf("youtube: auth: %w", err)
	inner := adapter.NewPermanentError("token revocado")
	p := &errPublisher{err: fmt.Errorf("youtube: auth: %w", inner)}
	w, publication, _ := setupPublishWorker(t, p)

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	if err := w.executeJob(context.Background(), *job); err == nil {
		t.Fatal("expected error")
	}

	got, err := db.GetPublicationByID(w.db, publication.ID)
	if err != nil || got == nil {
		t.Fatalf("get publication: %v %v", err, got)
	}
	if got.Status != "failed" {
		t.Errorf("PermanentError envuelto debe dead-letter igual, got '%s'", got.Status)
	}
}

// errPublisher falla siempre con el error dado.
type errPublisher struct {
	err error
}

func (p *errPublisher) UploadVideo(ctx context.Context, videoPath, title, description string, tags []string) (string, string, error) {
	return "", "", p.err
}
