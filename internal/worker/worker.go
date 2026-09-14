package worker

// Este paquete implementa el loop principal de ClipFactory: un worker que sondea
// la cola de jobs en SQLite y los ejecuta según su tipo.
//
// CICLO DE VIDA:
//
//   NewWorker(cfg)  → crea el worker (abre la DB o usa una inyectada, útil en tests)
//   w.Start()       → arranca goroutines: manejo de señales + loop de polling
//   w.loop()        → cada PollInterval: processJobs() → GetPendingJobs + LockJob
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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

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
	DB                *sql.DB     // nil = el worker abre su propia conexión con InitDB(DBPath)
	Discoverer        Discoverer  // job 'discovery' (nil = esos jobs fallan con mensaje claro)
	Downloader        Downloader  // job 'download'  (idem)
	Processor         Processor   // job 'process'   (idem)
	Thumbnailer       Thumbnailer // job 'thumbnail' (idem)
	Publisher         Publisher   // job 'publish'   (idem)
	DataDir           string      // raíz de archivos (incoming/, completed/, thumbnails/)
	DBPath            string
	WorkerID          string
	MaxConcurrentJobs int
	PollInterval      time.Duration
}

// DefaultWorkerConfig retorna la configuración por defecto para el hardware objetivo (i3-3220, 2 núcleos).
func DefaultWorkerConfig(dbPath string) WorkerConfig {
	return WorkerConfig{
		DBPath:            dbPath,
		WorkerID:          "worker-main",
		MaxConcurrentJobs: 2, // 1 ffmpeg + 1 download: adecuado para el i3-3220 (2 núcleos)
		PollInterval:      5 * time.Second,
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
	cfg         WorkerConfig
	db          *sql.DB        // conexión a SQLite (inyectada o propia)
	discoverer  Discoverer     // nil = jobs 'discovery' fallan con mensaje claro
	downloader  Downloader     // nil = jobs 'download' fallan con mensaje claro
	processor   Processor      // nil = jobs 'process' fallan con mensaje claro
	thumbnailer Thumbnailer    // nil = jobs 'thumbnail' fallan con mensaje claro
	publisher   Publisher      // nil = jobs 'publish' fallan con mensaje claro
	ownDB       bool           // true si el worker abrió su propia conexión (y debe cerrarla)
	stopCh      chan struct{}  // cerrado por Stop(): apaga el loop y las goroutines
	wg          sync.WaitGroup // espera a loop + jobs en curso al hacer Stop()
	started     atomic.Bool    // guarda Start/Stop idempotentes y sin carreras
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
		discoverer:  cfg.Discoverer,
		downloader:  cfg.Downloader,
		processor:   cfg.Processor,
		thumbnailer: cfg.Thumbnailer,
		publisher:   cfg.Publisher,
		ownDB:       ownDB,
		stopCh:      make(chan struct{}),
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
			w.processJobs(ctx)
		}
	}
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
	default:
		log.Printf("[worker] unknown job type: %s", job.Type)
		err = fmt.Errorf("unknown job type: %s", job.Type)
	}

	if err != nil {
		if failErr := db.FailJob(w.db, job.ID, err.Error()); failErr != nil {
			log.Printf("[worker] error marking job %d as failed: %v", job.ID, failErr)
		}
		return err
	}

	// marcar como completado
	if err := db.CompleteJob(w.db, job.ID); err != nil {
		log.Printf("[worker] error completing job %d: %v", job.ID, err)
	}
	return nil
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
	if w.discoverer == nil {
		return fmt.Errorf("no discoverer configurado (falta Discoverer en WorkerConfig)")
	}
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

	// ventana temporal: clips creados desde la última revisión
	var after time.Time
	if source.LastCheckedAt != nil {
		after = *source.LastCheckedAt
	}

	// consultar la plataforma (1 página = 100 clips alcanza para MVP;
	// MaxClipsPerDiscovery permite limitar el backlog en canales muy activos)
	clips, err := w.discoverer.ListClips(ctx, source.ChannelID, after, 1)
	if err != nil {
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
	if w.downloader == nil {
		return fmt.Errorf("no downloader configurado (falta Downloader en WorkerConfig)")
	}
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
		if err := w.downloader.DownloadClip(ctx, sc.PlatformClipID, destPath); err != nil {
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
//
// Los reintentos NATURALES los hace GetPendingPublications (status error/
// waiting_rate_limit con next_retry_at vencido): este job es el que encola la
// PRIMERA publicación por plataforma; quién lo re-encola tras un error es el
// calling code (en el MVP, el operador o un futuro job 'poll' diario).
func (w *Worker) executePublish(ctx context.Context, job db.Job) error {
	if w.publisher == nil {
		return fmt.Errorf("no publisher configurado (falta Publisher en WorkerConfig)")
	}
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

	// metadatos del video en YouTube: título del clip original si lo hay
	title := fmt.Sprintf("Clip %s", filepath.Base(clip.Filepath))
	description := "Clip generado con ClipFactory"
	tags := []string{"shorts", "clips"}

	externalID, externalURL, err := w.publisher.UploadVideo(ctx, clip.Filepath, title, description, tags)
	switch {
	case err == nil:
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
		var rle *youtube.RateLimitError
		if errors.As(err, &rle) {
			// cuota agotada: esperar al reset diario (medianoche PT ≈ 08:00 UTC).
			// NO incrementa attempts (no fue un fallo del clip ni del pipeline)
			nextRetry := time.Now().UTC().Add(24 * time.Hour)
			if updErr := db.UpdatePublicationStatus(w.db, pub.ID, "waiting_rate_limit", "", "", "cuota diaria agotada", nil, &nextRetry, false); updErr != nil {
				return fmt.Errorf("update publication (rate limit): %w", updErr)
			}
			log.Printf("[worker] publication %d espera reset de cuota hasta %v", pub.ID, nextRetry)
			// el job queda 'done': la publicación se reintentará por GetPendingPublications
			return nil
		}

		// error genérico: backoff exponencial 2^attempts, cap 24h
		delay := time.Duration(1<<uint(pub.Attempts)) * time.Hour
		if delay > 24*time.Hour {
			delay = 24 * time.Hour
		}
		nextRetry := time.Now().UTC().Add(delay)
		if updErr := db.UpdatePublicationStatus(w.db, pub.ID, "error", "", "", err.Error(), nil, &nextRetry, true); updErr != nil {
			return fmt.Errorf("update publication (error): %w", updErr)
		}
		log.Printf("[worker] publication %d falló, reintento en %v", pub.ID, delay)
		return fmt.Errorf("publish clip %d: %w", clip.ID, err)
	}
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

// Status devuelve el estado actual del worker.
func (w *Worker) Status() map[string]interface{} {
	return map[string]interface{}{
		"worker_id":      w.cfg.WorkerID,
		"started":        w.started.Load(),
		"max_concurrent": w.cfg.MaxConcurrentJobs,
		"poll_interval":  w.cfg.PollInterval.String(),
	}
}
