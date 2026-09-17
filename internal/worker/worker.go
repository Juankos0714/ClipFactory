package worker

// Este paquete implementa el loop principal de ClipFactory: un worker que sondea
// la cola de jobs en SQLite y los ejecuta según su tipo.
//
// CICLO DE VIDA:
//
//   NewWorker(cfg)  → crea el worker (abre la DB o usa una inyectada, útil en tests)
//   w.Start()       → arranca goroutines: manejo de señales + loop de polling
//   w.loop()        → cada PollInterval: processJobs() → GetPendingJobs + LockJob
//                     + una vez: enqueueInitialDiscoveries() (auto-discovery)
//   w.executeJob()  → despacha por tipo (discovery/download/process/thumbnail/publish)
//   w.Stop()        → cierra stopCh, espera a las goroutines (shutdown graceful)
//
// CONCURRENCIA:
//
//   - 'started' es atomic.Bool: Start/Stop pueden llamarse desde cualquier goroutine
//     (incluido el handler de SIGINT) sin carrera de datos.
//   - stopCh se re-crea en cada Start: Stop cierra el canal, y un Start posterior
//     necesita uno nuevo (close sobre canal cerrado = panic).
//   - wg sincroniza el apagado: Stop espera a que el loop y los jobs en curso
//     terminen antes de retornar.
//   - El lock real de los jobs lo hace la DB (WHERE status='queued', ver db.LockJob):
//     varios procesos worker pueden correr sobre la misma DB sin duplicar trabajo.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/juankos0714/clipfactory/internal/adapter"
	"github.com/juankos0714/clipfactory/internal/adapter/twitch"
	"github.com/juankos0714/clipfactory/internal/adapter/youtube"
	"github.com/juankos0714/clipfactory/internal/db"
)

// WorkerConfig define la configuración del worker.
//
// DB permite inyectar una conexión existente (útil para tests, que comparten una
// DB :memory: entre el test y el worker). En producción DB es nil y el worker abre
// su propia conexión con InitDB(DBPath).
//
// Discoverer, Downloader, Processor, Thumbnailer y Publisher (opcionales):
// implementaciones para los jobs 'discovery', 'download', 'process',
// 'thumbnail' y 'publish'. En producción los provee cmd/clipfactory
// (adaptador de Twitch + ffmpeg + YouTube); en tests se usan fakes. Si son
// nil, los jobs correspondientes fallan con un mensaje claro.
type WorkerConfig struct {
	// DiscoverOnStart activa el AUTO-DISCOVERY: en el primer tick del loop, el
	// worker encola un job 'discovery' por cada canal ACTIVO de la tabla sources
	// (idempotente vía EnsureActiveJob: no duplica si ya hay uno en vuelo). Es lo
	// que hace que "arrancar el worker y ya" sea suficiente: sin esto, la primera
	// pasada requeriría encolar jobs a mano (CLI 'discovery' o sqlite3). El job
	// se encola UNA vez por proceso: si el discovery falla, el reintento lo maneja
	// el operador (CLI discovery) o el próximo arranque del worker. Default: true
	// (ver DefaultWorkerConfig; CLIPFACTORY_DISCOVER_ON_START=false para desactivar).
	DiscoverOnStart bool

	// PollPublicationsInterval cada cuánto encolar el job poll_publications, que
	// re-encola los publishes de publications pendientes (reintento automático
	// tras error o cuota agotada). 0 = sin auto-poll (los publishes se encolan
	// a mano o por otro mecanismo). Default: 5m (ver DefaultWorkerConfig).
	PollPublicationsInterval time.Duration

	// RetentionInterval cada cuánto ejecutar la limpieza de videos completados
	// antiguos. 0 = desactivado. Default: 24h (ver DefaultWorkerConfig).
	RetentionInterval time.Duration

	// RetentionMaxAge máxima antigüedad de videos 'completed' para conservar.
	// Default: 720h (30 días).
	RetentionMaxAge time.Duration

	// MinFreeDiskSpace espacio libre mínimo en bytes en DataDir para encolar
	// nuevos downloads. 0 = desactivado. Default: 1GB.
	MinFreeDiskSpace int64

	DB                *sql.DB               // nil = el worker abre su propia conexión con InitDB(DBPath)
	Discoverer        Discoverer            // (legacy) hoy Twitch: queda registrado como discoverer "twitch"
	Discoverers       map[string]Discoverer // por plataforma: "twitch", "kick" (agrega/sobrescribe al legacy)
	Downloader        Downloader            // (legacy) hoy Twitch: queda registrado como downloader "twitch"
	Downloaders       map[string]Downloader // por plataforma (agrega/sobrescribe al legacy)
	Processor         Processor             // job 'process'   (idem)
	Thumbnailer       Thumbnailer           // job 'thumbnail' (idem)
	Publisher         Publisher             // (legacy) hoy YouTube: queda registrado como publisher "youtube"
	Publishers        map[string]Publisher  // por plataforma: "youtube", "meta" (agrega/sobrescribe al legacy)
	DataDir           string                // raíz de archivos (incoming/, completed/, thumbnails/)
	DBPath            string
	WorkerID          string
	MaxConcurrentJobs int
	PollInterval      time.Duration
}

// DefaultWorkerConfig retorna la configuración por defecto para el hardware objetivo (i3-3220, 2 núcleos).
func DefaultWorkerConfig(dbPath string) WorkerConfig {
	return WorkerConfig{
		DBPath:                   dbPath,
		WorkerID:                 "worker-main",
		MaxConcurrentJobs:        2, // 1 ffmpeg + 1 download: adecuado para el i3-3220 (2 núcleos)
		PollInterval:             5 * time.Second,
		// auto-discovery: la primera pasada no necesita el CLI 'discovery'
		DiscoverOnStart:          true,
		PollPublicationsInterval: 5 * time.Minute, // re-encolado automático de publishes
		RetentionInterval:        24 * time.Hour,
		RetentionMaxAge:          720 * time.Hour, // 30 días
		MinFreeDiskSpace:         1024 * 1024 * 1024, // 1GB
	}
}

// Downloader es la capacidad de descargar clips de una plataforma.
//
// Desacopla al worker del adaptador concreto (Twitch hoy, Kick/Meta mañana) y
// permite tests con un fake en memoria. Lo implementa *twitch.TwitchAdapter.
type Downloader interface {
	DownloadClip(ctx context.Context, clipID string, destPath string) error
}

// Discoverer es la capacidad de listar clips nuevos de un canal.
//
// afterTimestamp indica "clips creados desde" (sources.last_checked_at de la
// pasada anterior). Lo implementa *twitch.TwitchAdapter con la API Helix.
type Discoverer interface {
	ListClips(ctx context.Context, channelID string, afterTimestamp time.Time, maxPages ...int) ([]twitch.ClipInfo, error)
}

// Processor es la capacidad de recortar un video al formato vertical
// (1080x1920) del pipeline.
//
// Contrato de implementación (lo cumple adapter/ffmpeg.Processor):
//   - crea el directorio destino si no existe
//   - escribe a un .part y renombra al final (atomicidad)
//   - es idempotente: si destPath ya existe, no-op
//
// Lo implementa *ffmpeg.Processor.
type Processor interface {
	ProcessVideo(ctx context.Context, srcPath, destPath string) error
}

// Thumbnailer es la capacidad de extraer un frame de un video como imagen
// de vista previa (thumbnail para YouTube/TikTok/Meta).
//
// Mismo contrato de atomicidad/idempotencia que Processor. atSec indica el
// segundo del frame a extraer (0 = el Thumbnailer elige uno por defecto).
// Lo implementa *ffmpeg.Processor.
type Thumbnailer interface {
	GenerateThumbnail(ctx context.Context, videoPath, thumbPath string, atSec float64) error
}

// Publisher es la capacidad de publicar un clip de video en una plataforma
// externa (YouTube hoy, Meta/TikTok/Kick mañana).
//
// Devuelve el ID externo de la publicación y su URL pública.
// Puede devolver *youtube.RateLimitError (o cualquier error que envuelva una
// señal de cuota) para que executePublish programe backoff diario.
// Lo implementa *youtube.Publisher.
type Publisher interface {
	UploadVideo(ctx context.Context, videoPath, title, description string, tags []string) (externalID string, externalURL string, err error)
}

// Worker es el proceso que ejecuta la cola de trabajos.
//
// Campos internos (no configurables): cada capacidad guardada acá es la versión
// "resuelta" de WorkerConfig (si cfg.Discoverer era nil, discoverer queda nil y el
// job fallará con mensaje claro).
type Worker struct {
	cfg              WorkerConfig
	db               *sql.DB               // conexión a SQLite (inyectada o propia)
	discoverers      map[string]Discoverer // por plataforma: twitch, kick
	downloader       Downloader            // (legacy) = downloaders["twitch"] si se configuró
	downloaders      map[string]Downloader // por plataforma: twitch (TwitchDownloaderCLI), kick (HTTP)
	processor        Processor             // nil = jobs 'process' fallan con mensaje claro
	thumbnailer      Thumbnailer           // nil = jobs 'thumbnail' fallan con mensaje claro
	publishers       map[string]Publisher  // por plataforma: youtube, meta
	ownDB            bool                  // true si el worker abrió su propia conexión (y debe cerrarla)
	stopCh           chan struct{}         // cerrado por Stop(): apaga el loop y las goroutines
	wg               sync.WaitGroup        // espera a loop + jobs en curso al hacer Stop()
	started          atomic.Bool           // guarda Start/Stop idempotentes y sin carreras
	pollMu           sync.Mutex            // serializa el poll de publications (EnsureActiveJob es check-then-insert)
	pollLastEnqueued time.Time             // última vez que se encoló (o intentó) el poll_publications
	discoveryTried   atomic.Bool           // auto-discovery ya intentado (UNA vez por proceso, no por tick)
	retentionLastRun time.Time             // última vez que se ejecutó la limpieza de retención
}

// DefaultDataDir es el directorio de datos por defecto (coincide con config.LoadConfig).
const DefaultDataDir = "./data"

// Dimensiones de salida del pipeline (1080x1920 vertical). Deben coincidir con
// adapter/ffmpeg (Width/Height): están duplicadas acá para que el worker no
// importe el paquete de ffmpeg solo por dos constantes.
const (
	ffmpegWidth  = 1080
	ffmpegHeight = 1920
)

// NewWorker crea un nuevo worker.
func NewWorker(cfg WorkerConfig) (*Worker, error) {
	conn := cfg.DB
	ownDB := false
	if conn == nil {
		var err error
		conn, err = db.InitDB(cfg.DBPath)
		if err != nil {
			return nil, fmt.Errorf("init db: %w", err)
		}
		ownDB = true
	}

	w := &Worker{
		cfg:         cfg,
		db:          conn,
		processor:   cfg.Processor,
		thumbnailer: cfg.Thumbnailer,
		ownDB:       ownDB,
		stopCh:      make(chan struct{}),
	}

	// Discoverers por plataforma: Discoverer (campo clásico, hoy Twitch) queda
	// registrado como "twitch" para no romper tests existentes; el mapa de
	// cfg.Discoverers agrega o sobrescribe por plataforma.
	w.discoverers = make(map[string]Discoverer)
	if cfg.Discoverer != nil {
		w.discoverers["twitch"] = cfg.Discoverer
	}
	for platform, d := range cfg.Discoverers {
		w.discoverers[platform] = d
	}

	// Downloaders por plataforma: misma lógica que los discoverers (el campo
	// clásico Downloader es Twitch → "twitch"; Kick trae el suyo por cfg).
	w.downloaders = make(map[string]Downloader)
	if cfg.Downloader != nil {
		w.downloaders["twitch"] = cfg.Downloader
		w.downloader = cfg.Downloader // compat con tests que leen el campo directo
	}
	for platform, d := range cfg.Downloaders {
		w.downloaders[platform] = d
	}

	// Publishers por plataforma: Publisher (campo clásico, hoy YouTube) queda
	// como "youtube" para no romper tests existentes; cfg.Publishers agrega o
	// sobrescribe (así main.go registra meta y futuras plataformas).
	w.publishers = make(map[string]Publisher)
	if cfg.Publisher != nil {
		w.publishers["youtube"] = cfg.Publisher
	}
	for platform, p := range cfg.Publishers {
		w.publishers[platform] = p
	}

	return w, nil
}

// Start inicia el worker: registra el manejo de señales (SIGINT/SIGTERM) y lanza
// el loop de polling en una goroutine. No bloquea: para detenerlo, llamar Stop().
//
// Devuelve error si el worker ya está corriendo (CompareAndSwap evita el doble
// arranque y la doble goroutine).
func (w *Worker) Start() error {
	if !w.started.CompareAndSwap(false, true) {
		return fmt.Errorf("worker already started")
	}

	// canal nuevo por cada arranque: después de Stop, stopCh queda cerrado
	w.stopCh = make(chan struct{})

	log.Printf("[worker] starting worker %s (max concurrent jobs: %d, poll interval: %s)",
		w.cfg.WorkerID, w.cfg.MaxConcurrentJobs, w.cfg.PollInterval)

	// manejar señales para shutdown graceful
	ctx, cancel := context.WithCancel(context.Background())
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		// registrar manejo de señales
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		select {
		case sig := <-sigs:
			log.Printf("[worker] received signal %v, shutting down", sig)
			cancel()
		case <-ctx.Done():
			// ya cancelado
		case <-w.stopCh:
			cancel()
		}
	}()

	// loop principal del worker
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.loop(ctx)
	}()

	return nil
}

// loop es el loop principal del worker: polling de jobs y ejecución.
func (w *Worker) loop(ctx context.Context) {
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[worker] context cancelled, stopping loop")
			return
		case <-w.stopCh:
			log.Println("[worker] stop channel closed, stopping loop")
			return
		case <-ticker.C:
			// auto-discovery en el primer tick (UNA vez por proceso): encola
			// discovery por cada source activo para que el pipeline arranque solo
			w.maybeDiscoverOnStart(ctx)
			w.processJobs(ctx)
			w.maybePollPublications(ctx)
			w.maybeRetentionCleanup(ctx)
		}
	}
}

// maybeDiscoverOnStart implementa el AUTO-DISCOVERY: en el primer tick del loop
// encola un job 'discovery' por cada canal ACTIVO (GetSources solo devuelve
// active=1). UNA sola vez por proceso (discoveryTried): si un discovery falla,
// el reintento es tarea del operador (CLI discovery) o del próximo arranque —
// re-encolar automáticamente en cada tick convertiría un fallo persistente
// (p.ej. canal inexistente) en spam de jobs en error.
//
// EnsureActiveJob hace el encolado idempotente: si ya hay un discovery en
// vuelo (queued/running) para el canal, no duplica (protege también contra
// varios workers arrancando sobre la misma DB).
//
// DiscoverOnStart=false lo desactiva por completo (operador que prefiere
// disparar las pasadas a mano con el CLI 'discovery').
func (w *Worker) maybeDiscoverOnStart(ctx context.Context) {
	if !w.cfg.DiscoverOnStart {
		return // desactivado por config
	}
	if !w.discoveryTried.CompareAndSwap(false, true) {
		return // ya se intentó en un tick anterior
	}

	sources, err := db.GetSources(w.db)
	if err != nil {
		log.Printf("[worker] auto-discovery: error listando sources: %v", err)
		return
	}
	if len(sources) == 0 {
		log.Printf("[worker] auto-discovery: sin canales activos en sources")
		return
	}

	enqueued := 0
	skipped := 0
	for _, src := range sources {
		job := &db.Job{Type: "discovery", ReferenceID: src.ID, ReferenceType: "sources"}
		added, err := db.EnsureActiveJob(w.db, job)
		if err != nil {
			log.Printf("[worker] auto-discovery: error encolando discovery de %s/%s: %v", src.Platform, src.ChannelID, err)
			continue
		}
		if added {
			enqueued++
		} else {
			skipped++
		}
	}
	log.Printf("[worker] auto-discovery: %d encolados, %d ya en vuelo (%d canales activos)", enqueued, skipped, len(sources))
	_ = ctx // reservado para cancelación futura
}

// processJobs toma hasta MaxConcurrentJobs jobs pendientes de la cola y los
// ejecuta, cada uno en su propia goroutine.
//
// Detalle importante: LockJob puede fallar aunque GetPendingJobs haya devuelto el
// job — otro worker (o el tick anterior de este mismo loop) puede haberlo tomado
// en el medio. Ese fallo es NORMAL y solo se loguea, no reintenta.
func (w *Worker) processJobs(ctx context.Context) {
	// obtener jobs pendientes
	jobs, err := db.GetPendingJobs(w.db, w.cfg.MaxConcurrentJobs)
	if err != nil {
		log.Printf("[worker] error getting pending jobs: %v", err)
		return
	}

	if len(jobs) == 0 {
		return
	}

	log.Printf("[worker] found %d pending jobs", len(jobs))

	for _, job := range jobs {
		// intentar bloquear el job
		if err := db.LockJob(w.db, job.ID, w.cfg.WorkerID); err != nil {
			// ya fue tomado por otro worker o no está en queued
			log.Printf("[worker] could not lock job %d: %v", job.ID, err)
			continue
		}

		log.Printf("[worker] locked job %d (type=%s, ref_type=%s, ref_id=%d)", job.ID, job.Type, job.ReferenceType, job.ReferenceID)

		// ejecutar en goroutine separada para no bloquear el loop de polling
		w.wg.Add(1)
		go func(j db.Job) {
			defer w.wg.Done()
			w.executeJob(ctx, j)
		}(job)
	}
}

// executeJob despacha un job según su tipo y lo marca como done o error.
//
// Contrato de los handlers:
//   - handler que retorna nil → job 'done'
//   - handler que retorna error → job 'error' con el mensaje (el error NO es
//     fatal para el worker: el pipeline sigue con los demás jobs)
//   - pánico en el handler → se recupera y marca 'error' (no queda colgado en
//     'running' hasta el stale-lock de 30s)
//   - apagado (ctx cancelado mientras el handler corre) → el job se RE-ENCOLA
//     ('queued', sin error): no fue un fallo del trabajo; el próximo arranque
//     lo retoma (handlers idempotentes, semántica at-least-once). Ver
//     requeueInterruptedJob.
func (w *Worker) executeJob(ctx context.Context, job db.Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[worker] job %d panicked: %v", job.ID, r)
			failErr := fmt.Errorf("panic: %v", r)
			db.FailJob(w.db, job.ID, failErr.Error())
			err = failErr // el error llega al llamador (útil en tests)
		}
	}()

	log.Printf("[worker] executing job %d (type=%s, ref_type=%s, ref_id=%d)", job.ID, job.Type, job.ReferenceType, job.ReferenceID)

	switch job.Type {
	case "discovery":
		err = w.executeDiscovery(ctx, job)
	case "download":
		err = w.executeDownload(ctx, job)
	case "process":
		err = w.executeProcess(ctx, job)
	case "thumbnail":
		err = w.executeThumbnail(ctx, job)
	case "publish":
		err = w.executePublish(ctx, job)
	case "poll_publications":
		err = w.pollPublications(ctx)
	default:
		log.Printf("[worker] unknown job type: %s", job.Type)
		err = fmt.Errorf("unknown job type: %s", job.Type)
	}

	if err != nil {
		// apagado en curso (señal → ctx cancelado): el job NO se marca 'error',
		// se re-encola para que el próximo arranque lo retome. La comprobación va
		// ANTES de FailJob: un ctx cancelado puede ser también la CAUSA del error
		// del handler (descarga/ffmpeg abortados), y eso no es un fallo del clip.
		if ctx.Err() != nil {
			w.requeueInterruptedJob(job)
			return err
		}
		if failErr := db.FailJob(w.db, job.ID, err.Error()); failErr != nil {
			log.Printf("[worker] error marking job %d as failed: %v", job.ID, failErr)
		}
		return err
	}

	// marcar como completado
	if err := db.CompleteJob(w.db, job.ID); err != nil {
		log.Printf("[worker] error completing job %d: %v", job.ID, err)
	}

	// éxito PERO ctx cancelado justo después del CompleteJob: re-encolar igual.
	// El handler terminó su trabajo, pero los pasos que él mismo dispara (p.ej.
	// enqueue del próximo stage) pueden haberse perdido con el ctx cancelado;
	// los handlers son idempotentes, así que repetir el job es seguro y el
	// pipeline no queda a medias.
	if ctx.Err() != nil {
		w.requeueInterruptedJob(job)
	}
	return nil
}

// requeueInterruptedJob devuelve a la cola un job que estaba EN EJECUCIÓN
// cuando llegó el apagado (ctx cancelado por SIGINT/SIGTERM).
//
// Por qué no 'error': el trabajo no falló, lo interrumpió el shutdown; marcarlo
// error contaminaría los conteos y dispararía backoffs de publications sin
// motivo. Por qué no dejarlo 'running': bloquearía hasta el stale-lock de 30s
// (y en Docker con restart=always el container rearranca antes). RequeueJob lo
// deja 'queued' al instante y la idempotencia de los handlers (at-least-once)
// hace seguro repetir el trabajo.
//
// Solo actúa si el job sigue 'running' y con NUESTRO lock (RequeueJob verifica
// locked_by): si mientras tanto otro worker lo retomó (stale lock) o cambió de
// estado, no se toca.
func (w *Worker) requeueInterruptedJob(job db.Job) {
	requeued, err := db.RequeueJob(w.db, job.ID, w.cfg.WorkerID)
	if err != nil {
		log.Printf("[worker] job %d: no se pudo re-encolar tras el apagado: %v (el stale-lock de 30s lo recuperará)", job.ID, err)
		return
	}
	if requeued {
		log.Printf("[worker] job %d re-encolado tras el apagado (se retomará al rearrancar)", job.ID)
	}
}

// maybePollPublications encola el job 'poll_publications' cuando el intervalo
// PollPublicationsInterval venció (0 = desactivado). Es el mecanismo de
// RE-ENCOLADO AUTOMÁTICO de publications: el job, al ejecutarse, encola un
// 'publish' por cada publication pendiente (tras error o cuota agotada).
//
// Es best-effort y a prueba de workers duplicados: EnsureActiveJob no encola
// si ya hay un poll en vuelo (queued/running), y el mutex serializa los ticks
// concurrentes de este mismo worker.
func (w *Worker) maybePollPublications(ctx context.Context) {
	interval := w.cfg.PollPublicationsInterval
	if interval <= 0 {
		return // desactivado (config explícita del operador)
	}

	w.pollMu.Lock()
	defer w.pollMu.Unlock()

	if !w.pollLastEnqueued.IsZero() && time.Since(w.pollLastEnqueued) < interval {
		return // aún no vence el intervalo
	}

	job := &db.Job{Type: "poll_publications", ReferenceID: 0, ReferenceType: "system"}
	enqueued, err := db.EnsureActiveJob(w.db, job)
	if err != nil {
		log.Printf("[worker] error encolando poll_publications: %v", err)
		return
	}
	w.pollLastEnqueued = time.Now() // con o sin encolado: no insistir hasta el próximo intervalo
	if enqueued {
		log.Printf("[worker] poll_publications encolado (job %d)", job.ID)
	}
	_ = ctx // reservado para cancelación futura
}

// maybeRetentionCleanup ejecuta la limpieza de videos completados antiguos
// según el intervalo RetentionInterval (default 24h). 0 = desactivado.
// Limpia videos con status='completed' más antiguos que RetentionMaxAge (default 30 días).
func (w *Worker) maybeRetentionCleanup(ctx context.Context) {
	interval := w.cfg.RetentionInterval
	if interval <= 0 {
		return // desactivado por config
	}

	if !w.retentionLastRun.IsZero() && time.Since(w.retentionLastRun) < interval {
		return // aún no vence el intervalo
	}

	w.retentionLastRun = time.Now()

	deleted, err := db.CleanupOldCompletedVideos(w.db, w.cfg.RetentionMaxAge)
	if err != nil {
		log.Printf("[worker] retention cleanup error: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("[worker] retention cleanup: %d videos antiguos eliminados", deleted)
	}
	_ = ctx // reservado para cancelación futura
}

// pollPublications es el corazón del re-encolado automático de publications.
//
// Recorre las publications reintentables de TODAS las plataformas conocidas
// (status 'pending' | 'error' | 'waiting_rate_limit' con next_retry_at vencido,
// ver GetPendingPublications) y encola un job 'publish' por cada una que NO
// tenga ya un publish en vuelo (HasActivePublishJob).
//
// Contrato con executePublish: las publicaciones con error/cuota quedan con
// status 'error'/'waiting_rate_limit' + next_retry_at; este job las re-encola
// cuando vence. Si una publication ya no es reintenteable (published, o el clip
// desapareció), el publish correspondiente la resuelve (no-op o error de job).
//
// El job apunta a reference_type='system', reference_id=0: es un job global.
func (w *Worker) pollPublications(ctx context.Context) error {
	platforms := w.publishPlatforms()
	if len(platforms) == 0 {
		log.Printf("[worker] poll_publications: sin publishers configurados, no hay nada que re-encolar")
		return nil
	}

	totalEnqueued := 0
	for _, platform := range platforms {
		pubs, err := db.GetPendingPublications(w.db, platform, 100)
		if err != nil {
			return fmt.Errorf("get pending publications de %s: %w", platform, err)
		}
		for i := range pubs {
			if pubs[i].Status == "published" {
				continue // éxito: nada que hacer
			}
			active, err := db.HasActivePublishJob(w.db, pubs[i].ID, 0)
			if err != nil {
				return fmt.Errorf("check active publish de publication %d: %w", pubs[i].ID, err)
			}
			if active {
				continue // ya hay un publish en vuelo para esta publication
			}
			job := &db.Job{Type: "publish", ReferenceID: pubs[i].ID, ReferenceType: "publications"}
			if err := db.EnqueueJob(w.db, job); err != nil {
				return fmt.Errorf("enqueue publish de publication %d: %w", pubs[i].ID, err)
			}
			totalEnqueued++
		}
	}

	log.Printf("[worker] poll_publications completo: %d publishes re-encolados", totalEnqueued)
	return nil
}

// publishPlatforms devuelve las plataformas con publisher configurado, orden
// determinístico (los mapas no garantizan orden y el poll debe ser estable).
func (w *Worker) publishPlatforms() []string {
	platforms := make([]string, 0, len(w.publishers))
	for p := range w.publishers {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)
	return platforms
}

// executeDiscovery ejecuta el job de discovery de clips nuevos de un canal.
//
// El job apunta a una fila de sources (reference_type='sources',
// reference_id=sources.id). Pasos:
//
//  1. Cargar el source y validar que esté activo.
//  2. Consultar clips nuevos con el Discoverer (desde last_checked_at, o sin
//     filtro si es la primera pasada).
//  3. Por cada clip: UpsertSourceClip (idempotente por platform+clip_id); los
//     clips ya conocidos no se modifican.
//  4. Encolar un job 'download' por cada clip NUEVO insertado (los ya vistos
//     no re-encolan: idempotencia del pipeline).
//  5. Actualizar sources.last_checked_at ANTES de encolar downloads: si el
//     worker muere a mitad, la próxima pasada re-encola solo lo que falte
//     (los source_clips ya insertados son no-op).
//
// Nota sobre el orden: last_checked_at se avanza con el timestamp de la CONSULTA
// (no de los clips): entre esa marca y la próxima pasada puede haber clips nuevos
// que caerán en la ventana siguiente.
func (w *Worker) executeDiscovery(ctx context.Context, job db.Job) error {
	if job.ReferenceType != "sources" {
		return fmt.Errorf("job discovery con reference_type inesperado: %s", job.ReferenceType)
	}

	source, err := db.GetSourceByID(w.db, job.ReferenceID)
	if err != nil {
		return fmt.Errorf("get source %d: %w", job.ReferenceID, err)
	}
	if source == nil {
		return fmt.Errorf("source %d no existe", job.ReferenceID)
	}
	if !source.Active {
		log.Printf("[worker] source %d (%s) inactivo, saltando discovery", source.ID, source.ChannelName)
		return nil
	}

	// resolver el Discoverer por la plataforma del source (twitch, kick, ...)
	// Un Discoverer clásico (cfg.Discoverer) queda registrado como "twitch" en
	// NewWorker para compatibilidad con configs y tests anteriores.
	discoverer, ok := w.discoverers[source.Platform]
	if !ok || discoverer == nil {
		return fmt.Errorf("no discoverer configurado para la plataforma %q", source.Platform)
	}

	// ventana temporal: clips creados desde la última revisión
	var after time.Time
	if source.LastCheckedAt != nil {
		after = *source.LastCheckedAt
	}

	// consultar la plataforma (1 página = 100 clips alcanza para MVP;
	// MaxClipsPerDiscovery permite limitar el backlog en canales muy activos)
	clips, err := discoverer.ListClips(ctx, source.ChannelID, after, 1)
	if err != nil {
		// rate limit de Twitch (429): NO es un fallo del canal — re-encolar el
		// job discovery con created_at futuro (el scheduler no lo ofrece hasta
		// que venza) y terminar el job 'done'. Sin esto, un 429 durante el
		// discovery dejaría el canal sin sincronizar hasta reinicio o CLI manual.
		var tle *twitch.RateLimitError
		if errors.As(err, &tle) {
			when := time.Now().UTC().Add(tle.RetryAfter)
			if enqueued := w.requeueDiscovery(job, source.ID, when); enqueued {
				log.Printf("[worker] discovery %s/%s rate-limited, reintentando a las %v (%v)",
					source.Platform, source.ChannelID, when.Format(time.RFC3339), tle.RetryAfter)
			}
			return nil // job 'done': el reintento ya quedó programado
		}
		return fmt.Errorf("list clips de %s/%s: %w", source.Platform, source.ChannelID, err)
	}
	log.Printf("[worker] discovery %s/%s: %d clips recibidos (after=%v)",
		source.Platform, source.ChannelID, len(clips), after)

	// avanzar last_checked_at ANTES de crear source_clips/downloads: si el proceso
	// muere después de esto, la próxima pasada no re-encola los ya insertados
	// (idempotencia por UNIQUE de source_clips)
	now := time.Now().UTC()
	if err := db.UpdateSourceLastChecked(w.db, source.ID, now); err != nil {
		return fmt.Errorf("update last_checked_at: %w", err)
	}

	newClips := 0
	for _, clip := range clips {
		// ¿ya lo conocíamos? Los existentes (downloaded/skipped/error/incluso
		// detected de una pasada anterior) NO re-encolan download: el pipeline
		// los retoma por su cuenta o quedan como están (idempotencia).
		exists, err := db.SourceClipExists(w.db, source.Platform, clip.ID)
		if err != nil {
			return fmt.Errorf("check source_clip %s: %w", clip.ID, err)
		}

		sc := &db.SourceClip{
			Platform:          source.Platform,
			PlatformClipID:    clip.ID,
			SourceID:          source.ID,
			Title:             clip.Title,
			DurationSeconds:   clip.DurationSec,
			CreatedAtPlatform: &clip.CreatedAt,
			Status:            "detected",
		}
		if err := db.UpsertSourceClip(w.db, sc); err != nil {
			return fmt.Errorf("upsert source_clip %s: %w", clip.ID, err)
		}

		if !exists {
			// check espacio libre en disco antes de encolar download
			if w.cfg.MinFreeDiskSpace > 0 {
				free, err := freeDiskSpace(w.cfg.DataDir)
				if err != nil {
					log.Printf("[worker] disk space check failed: %v", err)
				} else if free < w.cfg.MinFreeDiskSpace {
					log.Printf("[worker] espacio libre insuficiente (%d bytes < %d), saltando encolado de download para %s",
						free, w.cfg.MinFreeDiskSpace, clip.ID)
					continue
				}
			}

			newClips++
			downloadJob := &db.Job{
				Type:          "download",
				ReferenceID:   sc.ID,
				ReferenceType: "source_clips",
			}
			if err := db.EnqueueJob(w.db, downloadJob); err != nil {
				return fmt.Errorf("enqueue download de %s: %w", clip.ID, err)
			}
		}
	}

	log.Printf("[worker] discovery completo para source %d (%s/%s): %d clips, %d nuevos, %d downloads encolados",
		source.ID, source.Platform, source.ChannelID, len(clips), newClips, newClips)
	return nil
}

// executeDownload ejecuta el job de descarga de un clip.
//
// El job apunta a una fila de source_clips (reference_type='source_clips',
// reference_id=source_clips.id) con status='detected'. Pasos:
//
//  1. Cargar el source_clip y validar su estado.
//  2. Si ya existe un video para este clip (reintento tras crash), reusarlo:
//     idempotencia at-least-once — la descarga no se repite.
//  3. Descargar a <DataDir>/incoming/<platform_clip_id>.mp4 vía el Downloader.
//     El propio Downloader es atómico: escribe a .part y renombra al final.
//  4. Insertar la fila en videos (status='incoming').
//  5. Marcar source_clips.status='downloaded'.
//  6. Encolar un job 'process' para que ffmpeg lo recorte.
//
// Si algo falla: source_clips.status='error' con el motivo, y el job queda
// 'error' (reintentable manualmente re-encolándolo: al haber fila en videos,
// el paso 3 se salta).
func (w *Worker) executeDownload(ctx context.Context, job db.Job) error {
	if job.ReferenceType != "source_clips" {
		return fmt.Errorf("job download con reference_type inesperado: %s", job.ReferenceType)
	}

	sc, err := db.GetSourceClipByID(w.db, job.ReferenceID)
	if err != nil {
		return fmt.Errorf("get source_clip %d: %w", job.ReferenceID, err)
	}
	if sc == nil {
		return fmt.Errorf("source_clip %d no existe", job.ReferenceID)
	}

	// resolver el Downloader por la plataforma del clip (twitch usa
	// TwitchDownloaderCLI, kick descarga por HTTP directo del CDN)
	downloader, ok := w.downloaders[sc.Platform]
	if !ok || downloader == nil {
		return fmt.Errorf("no downloader configurado para la plataforma %q", sc.Platform)
	}
	if sc.Status == "downloaded" {
		// ya descargado en un intento anterior (el job se re-encoló tras un crash
		// entre el insert en videos y el update del source_clip): no-op idempotente
		log.Printf("[worker] source_clip %d ya está downloaded, no-op", sc.ID)
		return nil
	}

	dataDir := w.cfg.DataDir
	if dataDir == "" {
		dataDir = DefaultDataDir
	}
	// nombre de archivo determinista: el UNIQUE(filepath) de videos hace que un
	// reintento con el mismo clip choque aquí en vez de duplicar archivos
	destPath := filepath.Join(dataDir, "incoming", sc.PlatformClipID+".mp4")

	// ¿existe ya un video de un intento anterior? (job reintentado tras insert)
	existing, err := db.GetVideoByFilepath(w.db, destPath)
	if err != nil {
		return fmt.Errorf("lookup video por filepath: %w", err)
	}
	var videoID int64
	if existing != nil {
		videoID = existing.ID
		log.Printf("[worker] video %d ya existe para clip %s, saltando descarga", videoID, sc.PlatformClipID)
	} else {
		if err := downloader.DownloadClip(ctx, sc.PlatformClipID, destPath); err != nil {
			// apagado en curso: NO marcar el source_clip como error (la descarga
			// no falló, la interrumpió el shutdown). executeJob re-encolará el job;
			// al rearrancar, el clip sigue 'detected' y la descarga se reintenta.
			if ctx.Err() != nil {
				return fmt.Errorf("download clip %s: %w", sc.PlatformClipID, err)
			}
			if updErr := db.UpdateSourceClipStatus(w.db, sc.ID, "error", err.Error()); updErr != nil {
				log.Printf("[worker] error marcando source_clip %d como error: %v", sc.ID, updErr)
			}
			return fmt.Errorf("download clip %s: %w", sc.PlatformClipID, err)
		}

		v := &db.Video{
			SourceClipID: sc.ID,
			Filepath:     destPath,
			Status:       "incoming",
		}
		if err := db.InsertVideo(w.db, v); err != nil {
			//_filepath UNIQUE: si el archivo ya tenía fila (carrera), seguimos con la existente
			if dup, dupErr := db.GetVideoByFilepath(w.db, destPath); dupErr == nil && dup != nil {
				videoID = dup.ID
				log.Printf("[worker] video %d registrado por otro worker, continuando", videoID)
			} else {
				return fmt.Errorf("insert video: %w", err)
			}
		} else {
			videoID = v.ID
		}
	}

	// marcar el clip como descargado (paso 5)
	if err := db.UpdateSourceClipStatus(w.db, sc.ID, "downloaded", ""); err != nil {
		return fmt.Errorf("update source_clip status: %w", err)
	}

	// encolar el siguiente paso del pipeline (paso 6)
	processJob := &db.Job{
		Type:          "process",
		ReferenceID:   videoID,
		ReferenceType: "videos",
	}
	if err := db.EnqueueJob(w.db, processJob); err != nil {
		// el video ya está descargado y registrado: el process se puede re-encolar
		// a mano, así que esto no invalida el trabajo hecho
		return fmt.Errorf("enqueue process job: %w", err)
	}

	log.Printf("[worker] download completo: source_clip=%d video=%d → job process=%d", sc.ID, videoID, processJob.ID)
	return nil
}

// executeProcess ejecuta el job de procesamiento (recorte a 1080x1920 con ffmpeg).
//
// El job apunta a una fila de videos (reference_type='videos',
// reference_id=videos.id) con status='incoming'. Pasos:
//
//  1. Cargar el video y validar su estado.
//  2. Determinar la ruta de salida: <DataDir>/completed/<nombre-del-archivo>.
//     Mismo nombre que el incoming: el UNIQUE(filepath) de clips y el hecho de
//     estar en otro directorio evitan colisiones.
//  3. Si el clip ya existe en la DB (job re-encolado tras crash): no-op
//     idempotente, solo re-marca estados.
//  4. Procesar con el Processor (crop central 9:16 → scale 1080x1920, atómico).
//  5. Insertar la fila en clips (status='completed', 1080x1920).
//  6. Marcar videos.status='completed' y encolar job 'thumbnail'.
//
// Si algo falla: videos.status='failed' con el motivo y el job queda 'error'.
func (w *Worker) executeProcess(ctx context.Context, job db.Job) error {
	if w.processor == nil {
		return fmt.Errorf("no processor configurado (falta Processor en WorkerConfig)")
	}
	if job.ReferenceType != "videos" {
		return fmt.Errorf("job process con reference_type inesperado: %s", job.ReferenceType)
	}

	video, err := db.GetVideoByID(w.db, job.ReferenceID)
	if err != nil {
		return fmt.Errorf("get video %d: %w", job.ReferenceID, err)
	}
	if video == nil {
		return fmt.Errorf("video %d no existe", job.ReferenceID)
	}
	if video.Status == "completed" {
		// ya procesado en un intento anterior: no-op idempotente
		log.Printf("[worker] video %d ya está completed, no-op", video.ID)
		return nil
	}
	if video.Status == "failed" {
		// re-encolado manual de un video fallido: reintentar el procesamiento
		log.Printf("[worker] video %d estaba failed, reintentando", video.ID)
	}

	dataDir := w.cfg.DataDir
	if dataDir == "" {
		dataDir = DefaultDataDir
	}
	// salida: mismo nombre de archivo que el incoming, en completed/
	destPath := filepath.Join(dataDir, "completed", filepath.Base(video.Filepath))

	// ¿ya existe el clip de un intento anterior? (crash entre ffmpeg y el insert)
	existing, err := db.GetClipByFilepath(w.db, destPath)
	if err != nil {
		return fmt.Errorf("lookup clip por filepath: %w", err)
	}
	var clipID int64
	if existing != nil {
		clipID = existing.ID
		log.Printf("[worker] clip %d ya existe para video %d, saltando ffmpeg", clipID, video.ID)
	} else {
		// marcar processing ANTES del trabajo largo: un crash durante ffmpeg deja
		// el video en 'processing' (y el .part huérfano lo limpia el próximo run)
		if err := db.UpdateVideoStatus(w.db, video.ID, "processing", ""); err != nil {
			return fmt.Errorf("update video status: %w", err)
		}

		if err := w.processor.ProcessVideo(ctx, video.Filepath, destPath); err != nil {
			// apagado en curso: NO marcar el video como failed (el procesamiento
			// no falló, lo interrumpió el shutdown). El video queda 'processing' y
			// el job re-encolado lo retoma: ProcessVideo es idempotente y el .part
			// huérfano se sobreescribe.
			if ctx.Err() != nil {
				return fmt.Errorf("process video %d: %w", video.ID, err)
			}
			if updErr := db.UpdateVideoStatus(w.db, video.ID, "failed", err.Error()); updErr != nil {
				log.Printf("[worker] error marcando video %d como failed: %v", video.ID, updErr)
			}
			return fmt.Errorf("process video %d: %w", video.ID, err)
		}

		c := &db.Clip{
			VideoID:  video.ID,
			Filepath: destPath,
			Width:    ffmpegWidth,
			Height:   ffmpegHeight,
			Status:   "completed",
		}
		if err := db.InsertClip(w.db, c); err != nil {
			// UNIQUE(filepath): si otro worker lo insertó primero (carrera), seguir
			if dup, dupErr := db.GetClipByFilepath(w.db, destPath); dupErr == nil && dup != nil {
				clipID = dup.ID
				log.Printf("[worker] clip %d registrado por otro worker, continuando", clipID)
			} else {
				return fmt.Errorf("insert clip: %w", err)
			}
		} else {
			clipID = c.ID
		}
	}

	// marcar el video como completado
	if err := db.UpdateVideoStatus(w.db, video.ID, "completed", ""); err != nil {
		return fmt.Errorf("update video status: %w", err)
	}

	// encolar la generación de thumbnail (siguiente paso del pipeline)
	thumbJob := &db.Job{
		Type:          "thumbnail",
		ReferenceID:   clipID,
		ReferenceType: "clips",
	}
	if err := db.EnqueueJob(w.db, thumbJob); err != nil {
		// el clip ya está procesado y registrado: el thumbnail se puede re-encolar
		return fmt.Errorf("enqueue thumbnail job: %w", err)
	}

	log.Printf("[worker] process completo: video=%d → clip=%d → job thumbnail=%d", video.ID, clipID, thumbJob.ID)
	return nil
}

// executeThumbnail ejecuta el job de generación de thumbnail de un clip.
//
// El job apunta a una fila de clips (reference_type='clips',
// reference_id=clips.id). Pasos:
//
//  1. Cargar el clip; si ya tiene thumbnail_path apuntando a un archivo
//     existente, no-op idempotente.
//  2. Destino: <DataDir>/thumbnails/<nombre-del-clip>.jpg.
//  3. Extraer un frame con el Thumbnailer (frame 0.5s: el inicio de los clips
//     de Twitch suele ser negro; el Thumbnailer ya aplica ese default).
//  4. Guardar la ruta en clips.thumbnail_path.
//
// Nota: la generación del thumbnail NO cambia clips.status — el clip ya está
// 'completed' (procesado); el thumbnail es un adjunto de publicación.
// Si falla, el job queda 'error' (re-encolable) pero el clip sigue publicable.
func (w *Worker) executeThumbnail(ctx context.Context, job db.Job) error {
	if w.thumbnailer == nil {
		return fmt.Errorf("no thumbnailer configurado (falta Thumbnailer en WorkerConfig)")
	}
	if job.ReferenceType != "clips" {
		return fmt.Errorf("job thumbnail con reference_type inesperado: %s", job.ReferenceType)
	}

	clip, err := db.GetClipByID(w.db, job.ReferenceID)
	if err != nil {
		return fmt.Errorf("get clip %d: %w", job.ReferenceID, err)
	}
	if clip == nil {
		return fmt.Errorf("clip %d no existe", job.ReferenceID)
	}

	// idempotencia: ya tiene thumbnail y el archivo existe en disco → no-op
	if clip.ThumbnailPath != "" {
		if _, statErr := os.Stat(clip.ThumbnailPath); statErr == nil {
			log.Printf("[worker] clip %d ya tiene thumbnail (%s), no-op", clip.ID, clip.ThumbnailPath)
			return nil
		}
		// el registro apunta a un archivo borrado: regenerarlo con la misma ruta
		log.Printf("[worker] thumbnail de clip %d desapareció (%s), regenerando", clip.ID, clip.ThumbnailPath)
	}

	dataDir := w.cfg.DataDir
	if dataDir == "" {
		dataDir = DefaultDataDir
	}
	thumbPath := filepath.Join(dataDir, "thumbnails", filepath.Base(clip.Filepath)+".jpg")

	if err := w.thumbnailer.GenerateThumbnail(ctx, clip.Filepath, thumbPath, 0); err != nil {
		return fmt.Errorf("generate thumbnail del clip %d: %w", clip.ID, err)
	}

	if err := db.UpdateClipThumbnail(w.db, clip.ID, thumbPath); err != nil {
		return fmt.Errorf("update clip thumbnail_path: %w", err)
	}

	log.Printf("[worker] thumbnail completo: clip=%d → %s", clip.ID, thumbPath)
	return nil
}

// executePublish ejecuta el job de publicación de un clip en una plataforma
// externa (YouTube vía Data API v3 hoy; Meta/TikTok/Kick en el futuro).
//
// El job apunta a una fila de publications (reference_type='publications',
// reference_id=publications.id). Pasos:
//
//  1. Cargar la publicación + su clip (con filepath y thumbnail).
//  2. Idempotencia: si ya está 'published' con ExternalID, no-op.
//  3. Subir el clip vía el Publisher.
//  4. Éxito: status='published', ExternalID/ExternalURL, published_at=ahora.
//  5. RateLimitError: status='waiting_rate_limit' con next_retry_at=mañana
//     (la cuota de YouTube se resetea a medianoche PT). NO cuenta como intento
//     fallido: no se incrementa el backoff de errores.
//  6. Otro error: status='error' con backoff exponencial
//     next_retry_at = now * 2^attempts (cap 24h), attempts+1.
//  7. En los casos 5 y 6 se RE-ENCOLA automáticamente el siguiente intento:
//     un job 'publish' con created_at = next_retry_at. El scheduler solo ofrece
//     jobs con created_at <= now, así que el job "duerme" hasta que vence el
//     backoff/cuota. Complementa al job poll_publications (que re-encola
//     publications sin job futuro, p.ej. tras un crash del worker).
// Reconciler es la capacidad opcional de un Publisher de BUSCAR publicaciones
// ya hechas en la plataforma. La usa executePublish para reconciliar antes de
// reintentar un upload: con la clave determinista cf-<clip>-<video> en la
// descripción, si el intento anterior subió el video y murió antes de
// actualizar la DB, el reintento lo encuentra y NO vuelve a subir (el
// escenario de duplicados: "YouTube acepta → worker cae → retry sube otra
// vez").
//
// Nil-safe: los publishers que no implementan FindRecentByMarker (fakes de
// tests, adaptadores mínimos) saltean la reconciliación y van directo al
// upload — mismo comportamiento que antes de agregarla.
type Reconciler interface {
	// FindRecentByMarker busca entre los videos recientes de la cuenta/página
	// uno cuya descripción contenga alguno de los markers dados. Devuelve
	// (externalID, externalURL, nil) si lo encontró, ("", "", nil) si no hay
	// coincidencia, y error ante fallos de la API (transitorios: el caller
	// decide; acá se tratan como "no encontrado" para no bloquear el publish).
	FindRecentByMarker(ctx context.Context, markers []string) (string, string, error)
}

// executePublish publica el clip de una publication 'pending' vía el
// Publisher de su plataforma. LA POLITICA DE REINTENTOS vive acá:
//
//   - éxito                → 'published' (no reintenta)
//   - RateLimitError       → 'waiting_rate_limit', reintento a ~24h (reset cuota)
//   - PermanentError       → 'failed' en el PRIMER intento: credenciales/
//     parámetros/permisos no se arreglan reintentando; requiere intervención
//     del operador (renovar token, corregir metadata). DEAD-LETTER visible en
//     'status' (publications failed).
//   - error transitorio    → 'error' con backoff exponencial 2^attempts (cap
//     24h) RE-ENCOLADO para reintento automático... hasta MaxPublishAttempts;
//     alcanzado el techo → 'failed' (sin más reintentos).
func (w *Worker) executePublish(ctx context.Context, job db.Job) error {
	if job.ReferenceType != "publications" {
		return fmt.Errorf("job publish con reference_type inesperado: %s", job.ReferenceType)
	}

	pub, err := db.GetPublicationByID(w.db, job.ReferenceID)
	if err != nil {
		return fmt.Errorf("get publication %d: %w", job.ReferenceID, err)
	}
	if pub == nil {
		return fmt.Errorf("publication %d no existe", job.ReferenceID)
	}

	// resolver el Publisher por la plataforma de la publication (youtube, meta).
	// Un Publisher clásico (cfg.Publisher) queda registrado como "youtube" en
	// NewWorker para compatibilidad con configs y tests anteriores.
	publisher, ok := w.publishers[pub.Platform]
	if !ok || publisher == nil {
		return fmt.Errorf("no publisher configurado para la plataforma %q", pub.Platform)
	}

	// idempotencia: ya publicada (crash entre upload y update de DB) → reconciliar
	if pub.Status == "published" && pub.ExternalID != "" {
		log.Printf("[worker] publication %d ya está published (%s), no-op", pub.ID, pub.ExternalID)
		return nil
	}

	clip, err := db.GetClipByID(w.db, pub.ClipID)
	if err != nil {
		return fmt.Errorf("get clip %d: %w", pub.ClipID, err)
	}
	if clip == nil {
		return fmt.Errorf("clip %d no existe para publication %d", pub.ClipID, pub.ID)
	}
	if _, statErr := os.Stat(clip.Filepath); statErr != nil {
		return fmt.Errorf("clip %d: archivo no encontrado %s: %w", clip.ID, clip.Filepath, statErr)
	}

	// metadatos del video en YouTube: título del clip original si lo hay.
	// La descripción lleva el MARKER determinista cf-<clip>-<video>: es la
	// huella que FindRecentByMarker busca antes de reintentar un upload para
	// no duplicar (ver Reconciler).
	videoID, err := db.GetVideoIDByClipID(w.db, clip.ID)
	if err != nil {
		videoID = 0 // reconciliación degradada, no bloquea el publish
	}
	title := fmt.Sprintf("Clip %s", filepath.Base(clip.Filepath))
	tags := []string{"shorts", "clips"}
	description := fmt.Sprintf("Clip generado con ClipFactory\n%s", strings.Join(db.PublicationKeysForClip(clip.ID, videoID), " "))

	// RECONCILIACIÓN anti-duplicados: si la plataforma ya tiene el video con
	// este marker (upload previo cuyo update de DB falló/crasheó), registrarlo
	// y NO volver a subir. Solo aplica a publications con intentos previos:
	// un publish fresco (attempts=0, sin next_retry_at) no puede ser duplicado
	// y se ahorra la llamada a la API.
	if rec, ok := publisher.(Reconciler); ok && (pub.Attempts > 0 || pub.NextRetryAt != nil) {
		externalID, externalURL, ferr := rec.FindRecentByMarker(ctx, db.PublicationKeysForClip(clip.ID, videoID))
		if ferr != nil {
			log.Printf("[worker] publication %d: reconciliación no disponible (%v); continúa el upload", pub.ID, ferr)
		}
		if externalID != "" {
			now := time.Now().UTC()
			if updErr := db.UpdatePublicationStatus(w.db, pub.ID, "published", externalID, externalURL, "", &now, nil, false); updErr != nil {
				return fmt.Errorf("update publication tras reconciliar (¡video ya subido %s!): %w", externalID, updErr)
			}
			log.Printf("[worker] publication %d RECONCILIADA: ya estaba en %s (%s) de un intento previo; no se duplica", pub.ID, externalID, externalURL)
			return nil
		}
	}

	externalID, externalURL, upErr := publisher.UploadVideo(ctx, clip.Filepath, title, description, tags)
	switch {
	case upErr == nil:
		// éxito: registrar la publicación externa
		now := time.Now().UTC()
		if updErr := db.UpdatePublicationStatus(w.db, pub.ID, "published", externalID, externalURL, "", &now, nil, false); updErr != nil {
			// el video YA está en YouTube: registrar el ID localmente es crítico
			// para no subirlo dos veces; si falla el update, dejar el error
			return fmt.Errorf("update publication tras éxito (¡video ya subido %s!): %w", externalID, updErr)
		}
		log.Printf("[worker] publish completo: publication=%d → %s (%s)", pub.ID, externalID, externalURL)
		return nil

	default:
		// rate limit → waiting_rate_limit; permanente → failed SIN reintentos;
		// el resto → backoff con techo (más abajo).
		var rle *youtube.RateLimitError
		if errors.As(upErr, &rle) {
			// cuota agotada: esperar al reset diario (medianoche PT ≈ 08:00 UTC).
			// NO incrementa attempts (no fue un fallo del clip ni del pipeline)
			nextRetry := time.Now().UTC().Add(24 * time.Hour)
			if updErr := db.UpdatePublicationStatus(w.db, pub.ID, "waiting_rate_limit", "", "", "cuota diaria agotada", nil, &nextRetry, false); updErr != nil {
				return fmt.Errorf("update publication (rate limit): %w", updErr)
			}
			log.Printf("[worker] publication %d espera reset de cuota hasta %v", pub.ID, nextRetry)
			// el job queda 'done': el reintento queda programado acá y, como
			// red de seguridad, poll_publications lo re-encola si faltara
			w.requeuePublish(job, pub.ID, nextRetry)
			return nil
		}

		// permanente (credenciales inválidas/revocadas, parámetros rechazados,
		// permisos): reintentar no lo arregla — dead-letter en el PRIMER intento.
		if adapter.IsPermanent(upErr) {
			if updErr := db.UpdatePublicationStatus(w.db, pub.ID, "failed", "", "", upErr.Error(), nil, nil, true); updErr != nil {
				return fmt.Errorf("update publication (failed/permanent): %w", updErr)
			}
			log.Printf("[worker] publication %d FALLÓ (permanente, no se reintenta): %v", pub.ID, upErr)
			return fmt.Errorf("publish clip %d (permanente): %w", clip.ID, upErr)
		}

		// fallo transitorio: backoff exponencial 2^attempts, cap 24h, con techo
		// MaxPublishAttempts (al alcanzarlo la publication muere en 'failed':
		// reintentar para siempre inundaría la cola y los logs).
		if pub.Attempts+1 >= adapter.MaxPublishAttempts {
			if updErr := db.UpdatePublicationStatus(w.db, pub.ID, "failed", "", "",
					fmt.Sprintf("agotados %d intentos; última causa: %s", adapter.MaxPublishAttempts, upErr.Error()), nil, nil, true); updErr != nil {
				return fmt.Errorf("update publication (failed/max attempts): %w", updErr)
			}
			log.Printf("[worker] publication %d FALLÓ DEFINITIVAMENTE tras %d intentos: %v", pub.ID, adapter.MaxPublishAttempts, upErr)
			return fmt.Errorf("publish clip %d agotó %d intentos: %w", clip.ID, adapter.MaxPublishAttempts, upErr)
		}

		delay := time.Duration(1<<uint(pub.Attempts)) * time.Hour
		if delay > 24*time.Hour {
			delay = 24 * time.Hour
		}
		nextRetry := time.Now().UTC().Add(delay)
		if updErr := db.UpdatePublicationStatus(w.db, pub.ID, "error", "", "", upErr.Error(), nil, &nextRetry, true); updErr != nil {
			return fmt.Errorf("update publication (error): %w", updErr)
		}
		log.Printf("[worker] publication %d falló, reintento en %v", pub.ID, delay)
		w.requeuePublish(job, pub.ID, nextRetry) // reintento automático cuando venza el backoff
		return fmt.Errorf("publish clip %d: %w", clip.ID, upErr)
	}
}

// requeuePublish programa el SIGUIENTE intento de una publication encolando un
// job 'publish' con created_at = when (los jobs solo se ofrecen cuando
// created_at <= now, así que el job "duerme" hasta que vence el backoff/cuota).
//
// No toca la fila publications: el estado y next_retry_at ya los escribió el
// caller. Se excluye de la verificación el PROPIO job en ejecución (running):
// sin esa exclusión, HasActivePublishJob lo vería como "ya hay uno activo" y
// nunca re-encolaría. El chequeo evita duplicados frente a poll_publications;
// si aun así se duplicara, executePublish es idempotente (no-op sobre
// publications ya published) y el daño es nulo.
func (w *Worker) requeuePublish(job db.Job, publicationID int64, when time.Time) {
	w.pollMu.Lock()
	defer w.pollMu.Unlock()

	if active, err := db.HasActivePublishJob(w.db, publicationID, job.ID); err == nil && active {
		return // ya hay OTRO publish en vuelo o programado
	}
	now := db.NowUTC()
	if _, err := w.db.Exec(
		`INSERT INTO jobs (type, reference_id, reference_type, status, created_at, updated_at)
		 VALUES ('publish', ?, 'publications', 'queued', ?, ?)`,
		publicationID, when.UTC().Format(time.RFC3339), now,
	); err != nil {
		log.Printf("[worker] error re-encolando publish de publication %d: %v (poll_publications lo cubrirá)", publicationID, err)
	}
}

// requeueDiscovery re-encola un job 'discovery' con created_at = when, para
// reintentar cuando venza el rate limit (mismo mecanismo de created_at futuro
// que requeuePublish). El chequeo de duplicados EXCLUYE el propio job en
// ejecución (está 'running' durante el requeue: sin la exclusión se vería a
// sí mismo como "ya hay uno activo" y nunca re-encolaría).
func (w *Worker) requeueDiscovery(job db.Job, sourceID int64, when time.Time) bool {
	w.pollMu.Lock()
	defer w.pollMu.Unlock()

	// ¿ya hay OTRO discovery en vuelo o programado para este source?
	var active int
	err := w.db.QueryRow(
		`SELECT 1 FROM jobs WHERE type = 'discovery' AND reference_id = ? AND reference_type = 'sources' AND status IN ('queued', 'running') AND id != ? LIMIT 1`,
		sourceID, job.ID,
	).Scan(&active)
	if err == nil {
		return false // ya hay otro: no duplicar
	}
	if err != sql.ErrNoRows {
		log.Printf("[worker] error verificando discovery activo de source %d: %v (reintento manual: CLI discovery)", sourceID, err)
		return false
	}

	// re-encolar directamente con created_at futuro (el scheduler no ofrece el
	// job hasta que venza)
	res, err := w.db.Exec(
		`INSERT INTO jobs (type, reference_id, reference_type, status, created_at, updated_at)
		 VALUES ('discovery', ?, 'sources', 'queued', ?, ?)`,
		sourceID, when.UTC().Format(time.RFC3339), db.NowUTC(),
	)
	if err != nil {
		log.Printf("[worker] error re-encolando discovery de source %d: %v (reintento manual: CLI discovery)", sourceID, err)
		return false
	}
	if _, err := res.LastInsertId(); err != nil {
		log.Printf("[worker] discovery de source %d re-encolado sin id: %v", sourceID, err)
	}
	return true
}

// Stop detiene el worker de forma graceful: cierra stopCh (el loop y los handlers
// de señal lo detectan), espera con wg.Wait() a que todo termine y deja 'started'
// en false. Es idempotente: llamarlo dos veces (o sin Start previo) es un no-op,
// lo que evita el panic "close of closed channel".
func (w *Worker) Stop() {
	if !w.started.CompareAndSwap(true, false) {
		return
	}
	log.Println("[worker] stopping worker")
	close(w.stopCh)
	w.wg.Wait()
	log.Println("[worker] worker stopped")
}

// Close libera los recursos del worker: la conexión SQLite SI el worker la abrió
// él mismo (ownDB). Con DB inyectada (tests, main.go que comparte la conexión)
// es responsabilidad del dueño cerrarla.
//
// Llamar SIEMPRE después de Stop(): cerrar la DB con jobs en vuelo provocaría
// "database is closed" en los handlers. Es idempotente y seguro sobre nil.
func (w *Worker) Close() error {
	if w.ownDB && w.db != nil {
		if err := w.db.Close(); err != nil {
			return fmt.Errorf("close db: %w", err)
		}
		w.ownDB = false
	}
	return nil
}

// Status devuelve el estado actual del worker.
func (w *Worker) Status() map[string]interface{} {
	return map[string]interface{}{
		"worker_id":      w.cfg.WorkerID,
		"started":        w.started.Load(),
		"max_concurrent": w.cfg.MaxConcurrentJobs,
		"poll_interval":  w.cfg.PollInterval.String(),
	}
}

// freeDiskSpace retorna el espacio libre en bytes en el directorio dado.
// En Windows usa GetDiskFreeSpaceEx, en Unix usa statfs.
func freeDiskSpace(path string) (int64, error) {
	var free int64
	if runtime.GOOS == "windows" {
		// Windows: usar GetDiskFreeSpaceEx
		kernel32 := syscall.NewLazyDLL("kernel32.dll")
		getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")
		pathPtr, err := syscall.UTF16PtrFromString(path)
		if err != nil {
			return 0, err
		}
		var freeBytesAvailable, totalNumberOfBytes, totalNumberOfFreeBytes int64
		r1, _, err := getDiskFreeSpaceEx.Call(
			uintptr(unsafe.Pointer(pathPtr)),
			uintptr(unsafe.Pointer(&freeBytesAvailable)),
			uintptr(unsafe.Pointer(&totalNumberOfBytes)),
			uintptr(unsafe.Pointer(&totalNumberOfFreeBytes)),
		)
		if r1 == 0 {
			return 0, fmt.Errorf("GetDiskFreeSpaceEx failed: %v", err)
		}
		free = freeBytesAvailable
	} else {
		// Unix/Linux: usar statfs
		var stat syscall.Statfs_t
		if err := syscall.Statfs(path, &stat); err != nil {
			return 0, err
		}
		free = int64(stat.Bavail) * int64(stat.Bsize)
	}
	return free, nil
}
