// Command clipfactory es el punto de entrada (CLI) de ClipFactory.
//
// Es un binario con 4 comandos:
//
//	clipfactory worker     → arranca el worker (proceso principal, corre para siempre)
//	clipfactory status     → muestra un resumen del estado del sistema
//	clipfactory discovery  → (stub) pasada manual de discovery
//	clipfactory help       → ayuda
//
// PAPER DE ESTE ARCHIVO (inyección de dependencias):
//
//	main.go es el ÚNICO lugar donde se "cablea" el sistema: carga la configuración,
//	crea los adaptadores concretos (Twitch, ffmpeg, YouTube) y los conecta al worker
//	a través de las interfaces de internal/worker (Discoverer, Downloader, Processor,
//	Thumbnailer, Publisher). El resto del código solo conoce interfaces, no
//	implementaciones — eso permite testear cada pieza con fakes y cambiar de
//	plataforma sin tocar el worker.
//
// Flujo de arranque de 'worker':
//
//	LoadConfig() → Validate() → crear adaptadores → NewWorker() → Start() → esperar señal
package main

import (
	"fmt"
	"os"

	"github.com/juankos0714/clipfactory/config"
	"github.com/juankos0714/clipfactory/internal/adapter/ffmpeg"
	"github.com/juankos0714/clipfactory/internal/adapter/twitch"
	"github.com/juankos0714/clipfactory/internal/adapter/youtube"
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
  discovery    Ejecuta una pasada de discovery manual
  help         Muestra este mensaje

Flujo del pipeline:
  1. Discovery: consulta periódica de clips nuevos en canales configurados
  2. Download: descarga los clips encontrados a data/incoming/
  3. Process: recorta a 1080x1920 con ffmpeg (VAAPI → fallback libx264)
  4. Review: revisión manual (MVP) o automática (fases posteriores)
  5. Publish: publica en YouTube/Meta/TikTok/Kick según configuración

La base de datos SQLite (data/clipfactory.db) mantiene el estado de todos los clips,
jobs, y publicaciones para idempotencia y reintentos con backoff.`)
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
//  1. Cargar y validar la configuración (env vars + credentials/*.conf).
//  2. Crear los adaptadores concretos y conectarlos al WorkerConfig SI hay
//     credenciales. Si no las hay, el worker arranca igual pero los jobs de esa
//     fase fallan con un mensaje claro (degradación controlada, no crash):
//     así se puede desarrollar/probar el pipeline por partes.
//  3. Crear el worker y arrancarlo.
//  4. Bloquearse para siempre con `select {}` — el shutdown graceful lo maneja
//     el worker internamente capturando SIGINT/SIGTERM.
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

	// --- Adaptador de Twitch (jobs 'discovery' y 'download') ---
	// Solo se conecta si hay ClientID en credentials/twitch.conf. Sin credenciales,
	// el campo Discoverer/Downloader queda nil y esos jobs fallarán con un mensaje
	// claro ("no discoverer configurado") en vez de tumbar el proceso al arrancar.
	if cfg.Twitch.ClientID != "" {
		twitchAdapter := twitch.NewTwitchAdapter(cfg.Twitch.ClientID, cfg.Twitch.AuthToken)
		twitchAdapter.SetDownloaderPath(cfg.TwitchDownloaderPath)
		wcfg.Discoverer = twitchAdapter // lista clips (API Helix)
		wcfg.Downloader = twitchAdapter // descarga clips (TwitchDownloaderCLI)
	} else {
		fmt.Fprintln(os.Stderr, "aviso: Twitch.ClientID vacío — los jobs 'discovery' y 'download' fallarán hasta configurar credentials/twitch.conf")
	}

	// --- Procesador ffmpeg (jobs 'process' y 'thumbnail') ---
	// No necesita credenciales, siempre se conecta. La ruta al binario es
	// sobrescribible vía CLIPFACTORY_FFMPEG_PATH (default: "ffmpeg" en PATH).
	proc := ffmpeg.NewProcessor()
	if cfg.FFmpegPath != "" {
		proc.SetFFmpegPath(cfg.FFmpegPath)
	}
	wcfg.Processor = proc   // recorta a 1080x1920
	wcfg.Thumbnailer = proc // extrae el frame de vista previa

	// --- Publicador de YouTube (job 'publish') ---
	// Requiere las 3 credenciales de credentials/youtube.conf (ClientID,
	// ClientSecret, RefreshToken). Sin ellas, los jobs publish fallan con
	// mensaje claro y el resto del pipeline sigue funcionando.
	if cfg.YouTube.ClientID != "" && cfg.YouTube.RefreshToken != "" {
		ytPub := youtube.NewPublisher(cfg.YouTube.ClientID, cfg.YouTube.ClientSecret, cfg.YouTube.RefreshToken)
		ytPub.SetPrivacyStatus(cfg.YouTube.PrivacyStatus) // public | unlisted | private
		ytPub.SetCategoryID(cfg.YouTube.CategoryID)       // 20 = Gaming
		wcfg.Publisher = ytPub
	} else {
		fmt.Fprintln(os.Stderr, "aviso: YouTube sin credenciales — los jobs 'publish' fallarán hasta configurar credentials/youtube.conf")
	}

	// Paso 3: crear y arrancar el worker.
	// NewWorker abre su propia conexión SQLite (wcfg.DB == nil) y aplica migraciones.
	w, err := worker.NewWorker(wcfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creando worker: %v\n", err)
		os.Exit(1)
	}

	if err := w.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error iniciando worker: %v\n", err)
		os.Exit(1)
	}

	// Paso 4: bloquear el proceso para siempre. El worker registra sus propios
	// handlers de SIGINT/SIGTERM (ver worker.Start): Ctrl+C detiene el proceso
	// y no hay nada más que hacer en la goroutine principal.
	// (Mejora futura: en vez de select {}, esperar aquí a w.Stop() tras la señal.)
	fmt.Println("worker corriendo... (presiona Ctrl+C para detener)")
	select {}
}

// runStatus ejecuta el comando 'status': resumen rápido del sistema.
//
// MVP: muestra cuántos canales hay configurados y el WorkerID que se usaría.
// TODO: leer la DB real y reportar conteos por estado (jobs queued, clips
// pendientes, publications en error, etc.).
func runStatus() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error cargando config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("estado del sistema:")
	fmt.Println("- sources:", len(cfg.Sources))
	fmt.Println("- worker:", worker.DefaultWorkerConfig("./data/clipfactory.db").WorkerID)
}

// runDiscovery ejecuta el comando 'discovery': pasada manual de discovery.
//
// TODO no implementado: hoy la forma manual de disparar discovery es insertar
// un job directamente en la DB (ver docs/guia-twitch.md §7).
func runDiscovery() {
	fmt.Println("ejecutando discovery... (no implementado aún)")
}
