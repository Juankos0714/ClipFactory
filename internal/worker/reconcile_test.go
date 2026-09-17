package worker

// Tests de la RECONCILIACIÓN anti-duplicados en executePublish:
//   - publisher con Reconciler + marker encontrado → NO se re-sub, se registra
//     el video ya subido (crash entre upload y update de DB)
//   - reconciliador sin match → el upload procede normal
//   - publish fresco (sin intentos previos) → ni consulta la API
//   - la descripción lleva el marker determinista cf-<clip>-<video>

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/juankos0714/clipfactory/internal/db"
)

// reconcilingPublisher simula un publisher real (YouTube/Meta): registra el
// marker que le toca buscar y decide si "la plataforma" ya tiene el video.
type reconcilingPublisher struct {
	markersSeen [][]string // lo que FindRecentByMarker recibió en cada llamada
	found       string     // externalID a devolver ("" = no encontrado)
	uploads     int
}

func (p *reconcilingPublisher) FindRecentByMarker(ctx context.Context, markers []string) (string, string, error) {
	p.markersSeen = append(p.markersSeen, markers)
	if p.found != "" {
		return p.found, "https:// reconciled.example/" + p.found, nil
	}
	return "", "", nil
}

func (p *reconcilingPublisher) UploadVideo(ctx context.Context, videoPath, title, description string, tags []string) (string, string, error) {
	p.uploads++
	return fmt.Sprintf("new_upload_%d", p.uploads), fmt.Sprintf("https://uploaded.example/%d", p.uploads), nil
}

func TestExecutePublishReconcilesAfterCrash(t *testing.T) {
	// ESCENARIO: el intento anterior subió el video (external_id ext_prev en la
	// plataforma) y el worker murió antes del update. El reintento debe
	// encontrarlo por marker y NO volver a subir.
	p := &reconcilingPublisher{found: "ext_prev"}
	w, publication, _ := setupPublishWorker(t, p)

	// simulate intento previo: attempts=1, la DB sigue 'pending'
	if err := db.UpdatePublicationStatus(w.db, publication.ID, "pending", "", "", "upload interrumpido", nil, nil, true); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	if err := w.executeJob(context.Background(), *job); err != nil {
		t.Fatalf("executeJob: %v", err)
	}

	// NO se subió de nuevo
	if p.uploads != 0 {
		t.Errorf("expected 0 uploads tras reconciliar, got %d", p.uploads)
	}
	// el reconciliador recibió los markers
	if len(p.markersSeen) != 1 || len(p.markersSeen[0]) == 0 {
		t.Fatalf("expected FindRecentByMarker called with markers, got %+v", p.markersSeen)
	}
	// y la publication quedó 'published' con el external_id recuperado
	got, err := db.GetPublicationByID(w.db, publication.ID)
	if err != nil || got == nil {
		t.Fatalf("get publication: %v %v", err, got)
	}
	if got.Status != "published" {
		t.Errorf("expected 'published' tras reconciliar, got '%s'", got.Status)
	}
	if got.ExternalID != "ext_prev" {
		t.Errorf("expected external_id 'ext_prev' recuperado, got %q", got.ExternalID)
	}
}

func TestExecutePublishReconcileMissUploads(t *testing.T) {
	// el reconciliador no encuentra el marker: el upload procede normal
	p := &reconcilingPublisher{found: ""} // miss
	w, publication, _ := setupPublishWorker(t, p)

	if err := db.UpdatePublicationStatus(w.db, publication.ID, "pending", "", "", "fallo previo", nil, nil, true); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	if err := w.executeJob(context.Background(), *job); err != nil {
		t.Fatalf("executeJob: %v", err)
	}
	if p.uploads != 1 {
		t.Errorf("expected 1 upload tras miss, got %d", p.uploads)
	}
}

func TestExecutePublishFreshSkipsReconcile(t *testing.T) {
	// publish fresco (attempts=0, sin next_retry_at): no puede ser duplicado,
	// NO consulta la API de reconciliación (ahorra una llamada por clip)
	p := &reconcilingPublisher{found: "no-deberia-usarse"}
	w, publication, _ := setupPublishWorker(t, p)

	job := publishJobFor(publication.ID)
	enqueueAndLock(t, w, job)

	if err := w.executeJob(context.Background(), *job); err != nil {
		t.Fatalf("executeJob: %v", err)
	}
	if len(p.markersSeen) != 0 {
		t.Errorf("publish fresco no debe llamar FindRecentByMarker, got %+v", p.markersSeen)
	}
	if p.uploads != 1 {
		t.Errorf("expected 1 upload, got %d", p.uploads)
	}
}

func TestExecutePublishDescriptionCarriesMarker(t *testing.T) {
	// la descripción enviada al publisher debe contener el marker determinista
	// cf-<clipID>-<videoID> (es lo que el reintento buscará en la plataforma)
	p := &reconcilingPublisher{}
	w, publication, clip := setupPublishWorker(t, p)

	var capturedDesc string
	orig := p.uploads
	_ = orig
	// capturar la descripción: UploadVideo la recibe como 3er parámetro;
	// redefinimos el método vía wrapper mínimo
	wp := &descCapturePublisher{inner: p, captured: &capturedDesc}
	w2, pub2, _ := setupPublishWorker(t, wp)
	_ = w
	_ = publication
	_ = clip

	job := publishJobFor(pub2.ID)
	enqueueAndLock(t, w2, job)
	if err := w2.executeJob(context.Background(), *job); err != nil {
		t.Fatalf("executeJob: %v", err)
	}

	if !strings.Contains(capturedDesc, "cf-") {
		t.Errorf("expected deterministic marker in description, got %q", capturedDesc)
	}
}

// descCapturePublisher envuelve un Publisher y captura la descripción.
type descCapturePublisher struct {
	inner    Publisher
	captured *string
}

func (d *descCapturePublisher) UploadVideo(ctx context.Context, videoPath, title, description string, tags []string) (string, string, error) {
	*d.captured = description
	return d.inner.UploadVideo(ctx, videoPath, title, description, tags)
}
