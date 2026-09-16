package worker

// Tests del AUTO-DISCOVERY: el worker encola un job 'discovery' por cada canal
// ACTIVO en el primer tick del loop (una sola vez por proceso). Cubre: encolado
// solo de activos, idempotencia (no duplica en vuelo), una vez por proceso
// (no re-encola tras un discovery terminado), desactivado por config, el
// default de DefaultWorkerConfig y el flujo completo Start→primer tick→Stop.

import (
	"context"
	"testing"
	"time"

	"github.com/juankos0714/clipfactory/internal/db"
)

// TestMaybeDiscoverOnStartEnqueuesActive: encola discovery por cada source
// activo y NO para los pausados.
func TestMaybeDiscoverOnStartEnqueuesActive(t *testing.T) {
	conn := openTestDB(t) // ya trae un source twitch activo (channel_id 12345)
	w := newTestWorker(t, conn)
	w.cfg.DiscoverOnStart = true

	// un segundo canal activo (kick) y uno pausado
	if _, err := conn.Exec(`INSERT INTO sources (platform, channel_id, channel_name, active) VALUES ('kick', 'xokas', 'xokas', 1), ('twitch', '999', 'pausado', 0)`); err != nil {
		t.Fatalf("insert sources: %v", err)
	}

	w.maybeDiscoverOnStart(context.Background())

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='discovery' AND reference_type='sources'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 discovery jobs (solo canales activos), got %d", count)
	}
}

// TestMaybeDiscoverOnStartIdempotent: si ya hay un discovery en vuelo para un
// canal, no lo duplica (EnsureActiveJob).
func TestMaybeDiscoverOnStartIdempotent(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.cfg.DiscoverOnStart = true

	var sourceID int64
	if err := conn.QueryRow(`SELECT id FROM sources WHERE channel_id='12345'`).Scan(&sourceID); err != nil {
		t.Fatalf("get source: %v", err)
	}
	job := &db.Job{Type: "discovery", ReferenceID: sourceID, ReferenceType: "sources"}
	if err := db.EnqueueJob(conn, job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	w.maybeDiscoverOnStart(context.Background())

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='discovery' AND reference_id=?`, sourceID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 discovery job (no duplicado), got %d", count)
	}
}

// TestMaybeDiscoverOnStartOncePerProcess: el intento es UNA vez por proceso.
// Aunque el discovery anterior ya terminó (done), un segundo tick no re-encola:
// el reintento tras un fallo es tarea del operador (CLI discovery) o del
// próximo arranque, no un spam automático de jobs en error.
func TestMaybeDiscoverOnStartOncePerProcess(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn)
	w.cfg.DiscoverOnStart = true

	w.maybeDiscoverOnStart(context.Background())

	// simular que el discovery ya terminó
	if _, err := conn.Exec(`UPDATE jobs SET status='done' WHERE type='discovery'`); err != nil {
		t.Fatalf("update: %v", err)
	}

	// segundo intento (otro tick): no debe encolar nada nuevo
	w.maybeDiscoverOnStart(context.Background())

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='discovery'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected still 1 discovery job (una vez por proceso), got %d", count)
	}
}

// TestMaybeDiscoverOnStartDisabled: con DiscoverOnStart=false no encola nada
// (operador que prefiere disparar las pasadas a mano).
func TestMaybeDiscoverOnStartDisabled(t *testing.T) {
	conn := openTestDB(t)
	w := newTestWorker(t, conn) // el literal de WorkerConfig deja DiscoverOnStart en false
	if w.cfg.DiscoverOnStart {
		t.Fatal("test setup: expected DiscoverOnStart false by default in newTestWorker")
	}

	w.maybeDiscoverOnStart(context.Background())

	var count int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='discovery'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 discovery jobs when disabled, got %d", count)
	}
}

// TestDefaultWorkerConfigDiscoversOnStart: el default de producción tiene el
// auto-discovery activado (la primera pasada no debe requerir el CLI).
func TestDefaultWorkerConfigDiscoversOnStart(t *testing.T) {
	cfg := DefaultWorkerConfig("./database/clipfactory.db")
	if !cfg.DiscoverOnStart {
		t.Error("expected DefaultWorkerConfig to enable DiscoverOnStart")
	}
}

// TestWorkerStartEnqueuesDiscoveryOnFirstTick: flujo completo con el worker
// ARRANCADO: en el primer tick del loop encola el discovery del source activo
// y el job queda en la DB compartida (visible para el test). El job puede ya
// estar en error cuando lo vemos (sin discoverer configurado): lo que importa
// es que el auto-encolado ocurrió.
func TestWorkerStartEnqueuesDiscoveryOnFirstTick(t *testing.T) {
	conn := openTestDB(t) // trae el source twitch activo

	w, err := NewWorker(WorkerConfig{
		DB:                conn,
		WorkerID:          "auto-discovery-worker",
		MaxConcurrentJobs: 1,
		PollInterval:      20 * time.Millisecond,
		DiscoverOnStart:   true,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer w.Stop()

	// esperar a que el primer tick corra (PollInterval 20ms + margen)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM jobs WHERE type='discovery'`).Scan(&count); err == nil && count > 0 {
			return // encolado: OK
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected discovery job enqueued on first tick within 3s")
}
