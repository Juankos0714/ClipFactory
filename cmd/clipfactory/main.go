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

func main() {
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

func runWorker() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error cargando config: %v\n", err)
		os.Exit(1)
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "config invalida: %v\n", err)
		os.Exit(1)
	}

	wcfg := worker.DefaultWorkerConfig(cfg.DBPath)
	wcfg.DataDir = cfg.DataDir

	// el adaptador de Twitch provee la capacidad de descarga (job 'download').
	// Solo se conecta si hay ClientID: sin credenciales, los jobs download fallan
	// con un mensaje claro en vez de morir al arrancar.
	if cfg.Twitch.ClientID != "" {
		twitchAdapter := twitch.NewTwitchAdapter(cfg.Twitch.ClientID, cfg.Twitch.AuthToken)
		twitchAdapter.SetDownloaderPath(cfg.TwitchDownloaderPath)
		wcfg.Discoverer = twitchAdapter
		wcfg.Downloader = twitchAdapter
	} else {
		fmt.Fprintln(os.Stderr, "aviso: Twitch.ClientID vacío — los jobs 'discovery' y 'download' fallarán hasta configurar credentials/twitch.conf")
	}

	// el procesador ffmpeg recorta a 1080x1920 (job 'process') y extrae los
	// thumbnails (job 'thumbnail'). La ruta del binario es sobrescribible.
	proc := ffmpeg.NewProcessor()
	if cfg.FFmpegPath != "" {
		proc.SetFFmpegPath(cfg.FFmpegPath)
	}
	wcfg.Processor = proc
	wcfg.Thumbnailer = proc

	// el publicador de YouTube sube los clips procesados (job 'publish'). Solo
	// se conecta si hay credenciales completas en credentials/youtube.conf;
	// sin ellas, los jobs publish fallan con un mensaje claro.
	if cfg.YouTube.ClientID != "" && cfg.YouTube.RefreshToken != "" {
		ytPub := youtube.NewPublisher(cfg.YouTube.ClientID, cfg.YouTube.ClientSecret, cfg.YouTube.RefreshToken)
		ytPub.SetPrivacyStatus(cfg.YouTube.PrivacyStatus)
		ytPub.SetCategoryID(cfg.YouTube.CategoryID)
		wcfg.Publisher = ytPub
	} else {
		fmt.Fprintln(os.Stderr, "aviso: YouTube sin credenciales — los jobs 'publish' fallarán hasta configurar credentials/youtube.conf")
	}

	w, err := worker.NewWorker(wcfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creando worker: %v\n", err)
		os.Exit(1)
	}

	if err := w.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error iniciando worker: %v\n", err)
		os.Exit(1)
	}

	// esperar señal para shutdown
	fmt.Println("worker corriendo... (presiona Ctrl+C para detener)")
	select {}
}

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

func runDiscovery() {
	fmt.Println("ejecutando discovery... (no implementado aún)")
}
