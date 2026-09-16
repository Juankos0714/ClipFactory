// Command clipfactory es el punto de entrada (CLI) de ClipFactory.
//
// Es un binario con 4 comandos:
//
//	clipfactory worker     → arranca el worker: sondea la cola, ejecuta los jobs
//	                          y encola discovery de los canales activos al arrancar.
//	                          Sale limpio (code 0) con SIGINT/SIGTERM.
//	clipfactory status     → muestra un resumen del estado del sistema
//	clipfactory discovery  → encola discovery para los canales activos
//	clipfactory help       → ayuda
//
// PAPER DE ESTE ARCHIVO (inyección de dependencias):
//
//	main.go es el ÚNICO lugar donde se "cablea" el sistema: carga la configuración,
//	aplica sources.yaml a la DB, crea los adaptadores concretos (Twitch, Kick,
//	ffmpeg, YouTube, Meta) y los conecta al worker a través de los mapas
//	Discoverers/Downloaders/Publishers de internal/worker. El resto del código solo
//	conoce interfaces, no implementaciones — eso permite testear cada pieza con
//	fakes y cambiar de plataforma sin tocar el worker.
//
// Flujo de arranque de 'worker':
//
//	LoadConfig() → Validate() → syncSources(sources.yaml → DB) → crear adaptadores
//	→ NewWorker() → Start() → esperar señal
package main

import (
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/juankos0714/clipfactory/config"
	"github.com/juankos0714/clipfactory/internal/adapter/ffmpeg"
	"github.com/juankos0714/clipfactory/internal/adapter/kick"
	"github.com/juankos0714/clipfactory/internal/adapter/meta"
	"github.com/juankos0714/clipfactory/internal/adapter/twitch"
	"github.com/juankos0714/clipfactory/internal/adapter/youtube"
	"github.com/juankos0714/clipfactory/internal/db"
	"github.com/juankos0714/clipfactory/internal/worker"
)

// printUsage imprime la ayuda del CLI: comandos disponibles y resumen del pipeline.
func printUsage() {
	fmt.Println(`ClipFactory — pipeline de clips para YouTube Shorts y más

Uso:
  clipfactory [command]

Comandos:
  worker       Ejecuta el worker de la cola de jobs
  status       Muestra el estado del sistema
  discovery    encola un job de discovery por cada canal activo (el worker
               los procesa: busca clips nuevos y encola sus descargas)
  help         Muestra este mensaje

Flujo del pipeline:
  1. Discovery: consulta periódica de clips nuevos en canales configurados
     (config/sources.yaml: Twitch y Kick)
  2. Download: descarga los clips encontrados a data/incoming/
  3. Process: recorta a 1080x1920 con ffmpeg (VAAPI → fallback libx264)
  4. Review: revisión manual (MVP) o automática (fases posteriores)
  5. Publish: publica en YouTube y Meta/Facebook según configuración; los
     reintentos tras error o cuota agotada se re-encolan solos.

La base de datos SQLite (database/clipfactory.db) mantiene el estado de todos los
clips, jobs y publicaciones para idempotencia y reintentos con backoff.`)
}

// main es el dispatcher del CLI: parsea os.Args[1] y llama al comando correspondiente.
//
// Sin argumentos muestra la ayuda y sale con código 0; un comando desconocido
// imprime la ayuda en stderr y sale con código 1 (útil para scripts que chequean
// el exit code).
func main() {
	// Log de diagnóstico: imprime los argumentos recibidos (útil al depurar
	// invocaciones desde Docker/scripts donde es fácil perder un flag).
	fmt.Println("args:", os.Args)

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]

	switch command {
	case "worker":
		runWorker()
	case "status":
		runStatus()
	case "discovery":
		runDiscovery()
	case "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "command not found: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

// runWorker ejecuta el comando 'worker': el proceso principal de ClipFactory.
//
// Pasos:
//  1. Cargar y validar la configuración (env vars + sources.yaml + credentials/).
//  2. Aplicar sources.yaml a la tabla sources (upsert idempotente).
//  3. Crear los adaptadores concretos y conectarlos al WorkerConfig SI hay
//     credenciales. Si no las hay, el worker arranca igual pero los jobs de esa
//     fase fallan con un mensaje claro (degradación controlada, no crash).
//  4. Crear el worker y arrancarlo.
//  5. Bloquearse esperando SIGINT/SIGTERM: al llegar una señal, Stop() espera
//     a que los jobs en curso terminen (los re-encola en vez de marcarlos
//     error) y Close() libera la DB. Una segunda señal fuerza la salida.
func runWorker() {
	// Paso 1: configuración. Cualquier error aquí es fatal: sin config no hay pipeline.
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error cargando config: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config invalida: %v\n", err)
		os.Exit(1)
	}

	// Paso 2: armar el WorkerConfig con defaults sensatos para el hardware
	// objetivo (2 jobs concurrentes: 1 ffmpeg + 1 descarga, poll cada 5s).
	wcfg := worker.DefaultWorkerConfig(cfg.DBPath)
	wcfg.DataDir = cfg.DataDir

	// intervalo del re-encolado automático de publications (default 5m,
	// CLIPFACTORY_POLL_PUBLICATIONS_INTERVAL para cambiarlo)
	wcfg.PollPublicationsInterval = cfg.PollPublicationsInterval

	// auto-discovery al arrancar (default true, CLIPFACTORY_DISCOVER_ON_START=false
	// para desactivar): el worker encola un job discovery por cada canal activo
	// en su primer tick, así la primera pasada no requiere el CLI 'discovery'
	wcfg.DiscoverOnStart = cfg.DiscoverOnStart

	// --- Adaptadores de ORIGEN (jobs 'discovery' y 'download') ---
	// Mapas por plataforma: el worker resuelve por source.platform /
	// source_clip.platform (twitch y kick hoy).
	wcfg.Discoverers = map[string]worker.Discoverer{}
	wcfg.Downloaders = map[string]worker.Downloader{}

	// Twitch: requiere ClientID en credentials/twitch.conf; descarga con
	// TwitchDownloaderCLI. Sin credenciales, sus jobs fallarán con mensaje claro.
	if cfg.Twitch.ClientID != "" {
		twitchAdapter := twitch.NewTwitchAdapter(cfg.Twitch.ClientID, cfg.Twitch.AuthToken)
		twitchAdapter.SetDownloaderPath(cfg.TwitchDownloaderPath)
		wcfg.Discoverers["twitch"] = twitchAdapter // lista clips (API Helix)
		wcfg.Downloaders["twitch"] = twitchAdapter // descarga clips (TwitchDownloaderCLI)
	} else {
		fmt.Fprintln(os.Stderr, "aviso: Twitch.ClientID vacío — los jobs de twitch fallarán hasta configurar credentials/twitch.conf")
	}

	// Kick: no requiere credenciales (endpoints públicos de kick.com). Descarga
	// por HTTP directo del CDN, sin herramientas externas. Siempre disponible.
	kickAdapter := kick.NewKickAdapter()
	wcfg.Discoverers["kick"] = kickAdapter
	wcfg.Downloaders["kick"] = kickAdapter

	// --- Procesador ffmpeg (jobs 'process' y 'thumbnail') ---
	// No necesita credenciales, siempre se conecta. La ruta al binario es
	// sobrescribible vía CLIPFACTORY_FFMPEG_PATH (default: "ffmpeg" en PATH).
	proc := ffmpeg.NewProcessor()
	if cfg.FFmpegPath != "" {
		proc.SetFFmpegPath(cfg.FFmpegPath)
	}
	wcfg.Processor = proc   // recorta a 1080x1920
	wcfg.Thumbnailer = proc // extrae el frame de vista previa

	// --- Publicadores (job 'publish', por plataforma) ---
	wcfg.Publishers = map[string]worker.Publisher{}

	// YouTube: requiere las 3 credenciales de credentials/youtube.conf (ClientID,
	// ClientSecret, RefreshToken). Sin ellas, los jobs publish de platform='youtube'
	// fallan con mensaje claro y el resto del pipeline sigue funcionando.
	if cfg.YouTube.ClientID != "" && cfg.YouTube.RefreshToken != "" {
		ytPub := youtube.NewPublisher(cfg.YouTube.ClientID, cfg.YouTube.ClientSecret, cfg.YouTube.RefreshToken)
		ytPub.SetPrivacyStatus(cfg.YouTube.PrivacyStatus) // public | unlisted | private
		ytPub.SetCategoryID(cfg.YouTube.CategoryID)       // 20 = Gaming
		wcfg.Publishers["youtube"] = ytPub
	} else {
		fmt.Fprintln(os.Stderr, "aviso: YouTube sin credenciales — los jobs 'publish' de youtube fallarán hasta configurar credentials/youtube.conf")
	}

	// Meta/Facebook: requiere PAGE_ID y ACCESS_TOKEN (Page Access Token) en
	// credentials/meta.conf. Publica los clips como Reels de página.
	if cfg.Meta.PageID != "" && cfg.Meta.AccessToken != "" {
		metaPub := meta.NewPublisher(cfg.Meta.PageID, cfg.Meta.AccessToken, cfg.Meta.GraphVersion)
		wcfg.Publishers["meta"] = metaPub
	} else {
		fmt.Fprintln(os.Stderr, "aviso: Meta sin credenciales — los jobs 'publish' de meta fallarán hasta configurar credentials/meta.conf")
	}

	// Paso 3: aplicar sources.yaml a la tabla sources (upsert idempotente).
	// Si el archivo tiene contenido inválido ya falló en LoadConfig; acá solo
	// puede fallar la DB.
	if err := syncSources(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error aplicando sources.yaml: %v\n", err)
		os.Exit(1)
	}

	// Paso 4: crear y arrancar el worker.
	// NewWorker abre su propia conexión SQLite (wcfg.DB == nil) y aplica migraciones
	// versionadas (tabla schema_migrations).
	w, err := worker.NewWorker(wcfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creando worker: %v\n", err)
		os.Exit(1)
	}

	if err := w.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error iniciando worker: %v\n", err)
		os.Exit(1)
	}

	// Paso 5: bloquear hasta recibir SIGINT/SIGTERM. El worker también captura
	// las señales (ver worker.Start): al llegar una, cancela su contexto interno,
	// lo que aborta HTTP/ffmpeg en vuelo. Acá esperamos la MISMA señal para hacer
	// el cierre ordenado:
	//
	//   w.Stop()  → espera (wg.Wait) a que los jobs en curso terminen; como el
	//               ctx ya está cancelado, executeJob los RE-ENCOLA a la cola
	//               en vez de marcarlos 'error' (se retoman al rearrancar).
	//   w.Close() → cierra la conexión SQLite que el worker abrió.
	//
	// Antes de esto el proceso hacía select {} y Docker/mataba con SIGKILL tras
	// el timeout: los jobs en curso quedaban 'running' huérfanos hasta el
	// stale-lock de 30s. Ahora un `docker stop` sale limpio en code 0.
	fmt.Println("worker corriendo... (Ctrl+C para detener)")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("[main] señal recibida, apagando gracefully...")

	// una SEGUNDA señal = el operador quiere salir ya: no esperar jobs largos
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "[main] segunda señal: salida forzada")
		os.Exit(130)
	}()

	w.Stop()
	if err := w.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "error cerrando el worker: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[main] apagado completo")
}

// syncSources aplica config/sources.yaml a la tabla sources de la DB.
//
// Upsert idempotente por (platform, channel_id): canales nuevos se dan de alta,
// existentes actualizan nombre y active. Los canales que están en la DB pero NO
// en el yaml se dejan intactos (el archivo es fuente de ALTA, no de exclusión:
// nadie pierde el monitoreo por omitir una línea).
//
// Abre su propia conexión (y la cierra) para no acoplar el arranque del worker
// a la conexión interna de NewWorker.
func syncSources(cfg *config.Config) error {
	if len(cfg.Sources) == 0 {
		return nil // sin sources.yaml (o vacío): los canales viven solo en la DB
	}

	conn, err := db.InitDB(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("abrir DB para syncSources: %w", err)
	}
	defer conn.Close()

	for _, s := range cfg.Sources {
		src := &db.Source{
			Platform:    s.Platform,
			ChannelID:   s.ChannelID,
			ChannelName: s.ChannelName,
			Active:      s.Active,
		}
		if err := db.UpsertSource(conn, src); err != nil {
			return fmt.Errorf("upsert source %s/%s: %w", s.Platform, s.ChannelID, err)
		}
		fmt.Printf("source aplicado desde sources.yaml: %s/%s (active=%v)\n", s.Platform, s.ChannelID, s.Active)
	}
	return nil
}

// runStatus ejecuta el comando 'status': resumen del estado del sistema leyendo
// la DB real (database/clipfactory.db). Muestra:
//
//   - versión del esquema (schema_migrations)
//   - canales configurados (sources.yaml + DB, por plataforma)
//   - cola de jobs por tipo y estado (queued/running/done/error)
//   - pipeline de videos/clips por estado
//   - publications por plataforma y estado (published/error/backoff)
//
// No requiere credenciales ni fuentes: la DB es la única fuente de verdad.
// Si la DB no existe todavía (instalación fresca sin arranque previo del
// worker), lo informa y sale con código 0 en vez de crearla vacía.
func runStatus() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error cargando config: %v\n", err)
		os.Exit(1)
	}

	if _, err := os.Stat(cfg.DBPath); os.IsNotExist(err) {
		fmt.Println("sin DB en", cfg.DBPath, "— arrancá el worker una vez para crearla")
		fmt.Println("sources (sources.yaml):", len(cfg.Sources))
		fmt.Println("poll publications cada:", cfg.PollPublicationsInterval)
		return
	}

	conn, err := db.InitDB(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error abriendo DB %s: %v\n", cfg.DBPath, err)
		os.Exit(1)
	}
	defer conn.Close()

	fmt.Println("estado del sistema")
	fmt.Println("──────────────────")
	fmt.Println("db:", cfg.DBPath)

	if version, err := db.SchemaVersion(conn); err == nil {
		fmt.Println("schema:", version)
	}
	fmt.Println("sources (sources.yaml):", len(cfg.Sources))
	fmt.Println("poll publications cada:", cfg.PollPublicationsInterval)

	// --- cola de jobs (lo más operativo: qué está haciendo/pendiente el worker) ---
	if jobStats, err := db.GetJobStats(conn); err == nil {
		fmt.Println("\njobs (por tipo):")
		if len(jobStats) == 0 {
			fmt.Println("  (cola vacía)")
		}
		for _, jtype := range sortedKeys(jobStats) {
			fmt.Printf("  %-20s %s\n", jtype+":", formatCounts(jobStats[jtype]))
		}
	} else {
		fmt.Fprintf(os.Stderr, "aviso: no se pudieron leer jobs: %v\n", err)
	}

	// --- pipeline de videos y clips ---
	if videoStats, err := db.GetVideoStats(conn); err == nil && len(videoStats) > 0 {
		fmt.Println("\nvideos:", formatCounts(videoStats))
	}
	if clipStats, err := db.GetClipStats(conn); err == nil && len(clipStats) > 0 {
		fmt.Println("clips: ", formatCounts(clipStats))
	}

	// --- source_clips por plataforma (origen: twitch/kick) ---
	if scStats, err := db.GetSourceClipStats(conn); err == nil && len(scStats) > 0 {
		fmt.Println("\nsource_clips (por plataforma):")
		for _, platform := range sortedKeys(scStats) {
			fmt.Printf("  %-10s %s\n", platform+":", formatCounts(scStats[platform]))
		}
	}

	// --- sources por plataforma (yaml + alta manual) ---
	if srcStats, err := db.GetSourceStats(conn); err == nil && len(srcStats) > 0 {
		fmt.Println("\nsources (por plataforma):")
		for _, platform := range sortedKeys(srcStats) {
			c := srcStats[platform]
			fmt.Printf("  %-10s %d activos, %d pausados\n", platform+":", c.Active, c.Inactive)
		}
	}

	// --- publications por plataforma (destino: youtube/meta) ---
	if pubStats, err := db.GetPublicationStats(conn); err == nil && len(pubStats) > 0 {
		fmt.Println("\npublications (por plataforma):")
		for _, platform := range sortedKeys(pubStats) {
			fmt.Printf("  %-10s %s\n", platform+":", formatCounts(pubStats[platform]))
		}
	}
}

// sortedKeys devuelve las claves de un mapa[string]V ordenadas, para que el
// output de 'status' sea determinístico (los mapas de Go no garantizan orden).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// formatCounts imprime un mapa de conteos como "queued=2 done=10 error=1",
// con claves ordenadas para output estable.
func formatCounts(counts map[string]int) string {
	parts := make([]string, 0, len(counts))
	for _, status := range sortedKeys(counts) {
		parts = append(parts, fmt.Sprintf("%s=%d", status, counts[status]))
	}
	if len(parts) == 0 {
		return "(vacío)"
	}
	return strings.Join(parts, " ")
}

// runDiscovery ejecuta el comando 'discovery': pasada manual de discovery.
//
// Encola un job 'discovery' por cada canal ACTIVO en la DB, sin ejecutarlo: el
// worker (o el que arranches después) es quien los procesa. Pasos:
//
//  1. Cargar config y aplicar sources.yaml a la tabla sources (mismo
//     syncSources que el worker: alta/actualización idempotente de canales).
//  2. Listar los sources activos (GetSources).
//  3. Encolar un job discovery → sources por canal con EnsureActiveJob:
//     idempotente, si ya hay un discovery en vuelo (queued/running) para ese
//     canal NO lo duplica.
//
// Sin canales activos avisa y sale 0 (no es error: puede que solo tengas
// canales pausados o el sources.yaml vacío a propósito). Con canales, la
// salida lista qué canales quedaron encolados para que el worker los tome.
func runDiscovery() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error cargando config: %v\n", err)
		os.Exit(1)
	}

	// aplicar sources.yaml a la DB antes de listar (así un canal recién agregado
	// al yaml entra aunque el worker no haya arrancado todavía)
	if err := syncSources(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error aplicando sources.yaml: %v\n", err)
		os.Exit(1)
	}

	conn, err := db.InitDB(cfg.DBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error abriendo DB %s: %v\n", cfg.DBPath, err)
		os.Exit(1)
	}
	defer conn.Close()

	sources, err := db.GetSources(conn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error listando sources: %v\n", err)
		os.Exit(1)
	}
	if len(sources) == 0 {
		fmt.Println("no hay canales activos para descubrir")
		fmt.Println("dá de alta canales en config/sources.yaml o insertando en la tabla sources")
		return
	}

	enqueued := 0
	skipped := 0
	for _, src := range sources {
		job := &db.Job{Type: "discovery", ReferenceID: src.ID, ReferenceType: "sources"}
		added, err := db.EnsureActiveJob(conn, job)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error encolando discovery de %s/%s: %v\n", src.Platform, src.ChannelID, err)
			os.Exit(1)
		}
		if added {
			fmt.Printf("discovery encolado: %s/%s (source %d) → job %d\n", src.Platform, src.ChannelID, src.ID, job.ID)
			enqueued++
		} else {
			fmt.Printf("discovery ya en vuelo: %s/%s (source %d), no duplicado\n", src.Platform, src.ChannelID, src.ID)
			skipped++
		}
	}

	fmt.Printf("\nlisto: %d discovery encolados, %d ya en vuelo. Arrancá el worker para procesarlos.\n", enqueued, skipped)
}
