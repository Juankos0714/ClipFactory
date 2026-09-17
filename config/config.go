// Package config carga la configuración de ClipFactory.
//
// FUENTES DE CONFIGURACIÓN (en orden de uso):
//
//  1. Variables de entorno CLIPFACTORY_* (ver LoadConfig para la lista completa).
//     Son la vía principal: funcionan igual en bare metal y en Docker
//     (docker-compose.yml ya las define para el contenedor de desarrollo).
//
//  2. credentials/twitch.conf y credentials/youtube.conf: archivos clave=valor
//     estilo .env con las credenciales por plataforma. Formato documentado en
//     docs/guia-twitch.md y docs/guia-youtube.md.
//
//  3. config/sources.yaml: canales a monitorear (Twitch/Kick). El worker lo aplica
//     a la tabla `sources` en cada arranque (upsert idempotente), así que editar el
//     archivo y reiniciar es suficiente para dar de alta o pausar canales.
//
// Las credenciales viven separadas de la config para poder montar credentials/ con
// permisos restringidos (y para no commitear nunca secretos: credentials/ está
// en .gitignore).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config representa la configuración completa de ClipFactory.
//
// Es un struct plano y sin dependencias: los paquetes internos (worker, adapters)
// reciben solo los campos que necesitan, nunca este struct entero.
type Config struct {
	// --- Rutas de directorios y archivos (todas configurables por env var) ---
	DataDir        string // raíz de archivos de video: data/{incoming,completed,...}
	DBPath         string // ruta de la DB SQLite (database/clipfactory.db)
	LogDir         string // directorio de logs de la aplicación
	ConfigDir      string // directorio de config (sources.yaml, futuro)
	CredentialsDir string // directorio con twitch.conf / youtube.conf

	// --- Logging ---
	LogLevel string // "debug", "info", "warn", "error"

	// --- Worker ---
	MaxConcurrentJobs int           // jobs en paralelo (default: NumCPU)
	PollInterval      time.Duration // frecuencia de sondeo de la cola (default: 5s)
	WorkerID          string        // identidad en jobs.locked_by (default: hostname;
	//                                  CLIPFACTORY_WORKER_ID para override). Único
	//                                  por proceso: varios workers sobre la misma DB
	//                                  no deben compartir identidad de lock.

	// Sources: canales a monitorear, cargados de config/sources.yaml (vacío si
	// el archivo no existe: los canales también pueden administrarse por DB)
	Sources []SourceConfig

	// Intervalo entre corridas del job poll_publications, que re-encola los
	// publishes de publications pendientes (reintentos tras error/cuota).
	// Default: 5m (ver CLIPFACTORY_POLL_PUBLICATIONS_INTERVAL).
	PollPublicationsInterval time.Duration

	// DiscoverOnStart: auto-discovery al arrancar el worker — encola un job
	// 'discovery' por cada canal activo de la tabla sources en el primer tick
	// (idempotente vía EnsureActiveJob). Default true;
	// CLIPFACTORY_DISCOVER_ON_START=false para desactivarlo.
	DiscoverOnStart bool

	// RetentionInterval: cada cuánto ejecutar la limpieza de videos completados
	// antiguos. Default: 24h (ver CLIPFACTORY_RETENTION_INTERVAL).
	RetentionInterval time.Duration

	// RetentionMaxAge: máxima antigüedad de videos 'completed' para conservar.
	// Default: 720h (30 días, ver CLIPFACTORY_RETENTION_MAX_AGE).
	RetentionMaxAge time.Duration

	// MinFreeDiskSpace: espacio libre mínimo en bytes en el directorio de datos
	// antes de encolar nuevos downloads. Default: 1GB (ver CLIPFACTORY_MIN_FREE_DISK_SPACE).
	// Si el espacio libre es menor, no se encolan nuevos downloads (se reintenta en el siguiente tick).
	MinFreeDiskSpace int64

	// MetricsAddr: dirección para exponer métricas Prometheus (ej: ":9090").
	// Vacío = desactivado (default).
	MetricsAddr string

	// Platform credentials (cargadas desde archivos en credentials/)
	Twitch  TwitchConfig
	YouTube YouTubeConfig
	Meta    MetaConfig
	// TikTok y publicación en Kick se agregan en fases posteriores (Kick es
	// plataforma de ORIGEN de clips, como Twitch)

	// Ruta al binario TwitchDownloaderCLI (para el job 'download').
	// Default: "TwitchDownloaderCLI" (se asume en PATH; la imagen Docker lo incluye).
	TwitchDownloaderPath string

	// Ruta al binario ffmpeg (para los jobs 'process' y 'thumbnail').
	// Default: "ffmpeg" (se asume en PATH; la imagen Docker lo incluye).
	FFmpegPath string
}

// SourceConfig representa un canal de origen a monitorear.
type SourceConfig struct {
	Platform    string // "twitch", "kick"
	ChannelID   string // ID de canal en la plataforma
	ChannelName string // nombre legible (para logs y display)
	Active      bool   // monitorear o no
}

// MetaConfig representa las credenciales de Meta/Facebook
// (credentials/meta.conf) para publicar los clips como Reels en una página.
type MetaConfig struct {
	PageID       string // ID de la página de Facebook donde se publican los Reels
	AccessToken  string // Page Access Token (no el token de usuario: ver docs)
	GraphVersion string // versión de la Graph API (default "v21.0")
}

// TwitchConfig representa las credenciales de Twitch (credentials/twitch.conf).
type TwitchConfig struct {
	ClientID  string // Client-ID de la app registrada en Twitch (obligatorio)
	AuthToken string // App Access Token (opcional para /helix/clips, sube el rate limit)
}

// YouTubeConfig representa las credenciales de la YouTube Data API v3
// (credentials/youtube.conf). El RefreshToken se genera UNA VEZ fuera del
// pipeline (docs/guia-youtube.md §4).
type YouTubeConfig struct {
	ClientID      string
	ClientSecret  string
	RefreshToken  string
	PrivacyStatus string // "public", "unlisted", "private"
	CategoryID    string // categoryId de YouTube ("20" = Gaming)
}

// LoadConfig carga la configuración desde variables de entorno y archivos de credentials.
//
// Tolerancia a fallos deliberada: los archivos OPCIONALES (sources.yaml,
// twitch.conf, youtube.conf) no existen en una instalación fresca y su ausencia NO
// es error (cada fase del pipeline valida sus propias credenciales cuando las
// necesita). Pero si el archivo existe y no se puede leer (permisos, es un
// directorio...), eso SÍ es error: mejor fallar temprano que descubrirlo en producción.
func LoadConfig() (*Config, error) {
	// 1) variables de entorno con defaults para todo (ver getEnv* más abajo)
	cfg := &Config{
		DataDir:              getEnv("CLIPFACTORY_DATA_DIR", "./data"),
		DBPath:               getEnv("CLIPFACTORY_DB_PATH", "./database/clipfactory.db"),
		LogDir:               getEnv("CLIPFACTORY_LOG_DIR", "./logs"),
		ConfigDir:            getEnv("CLIPFACTORY_CONFIG_DIR", "./config"),
		CredentialsDir:       getEnv("CLIPFACTORY_CREDENTIALS_DIR", "./credentials"),
		LogLevel:             getEnv("CLIPFACTORY_LOG_LEVEL", "info"),
		MaxConcurrentJobs:    getEnvInt("CLIPFACTORY_MAX_CONCURRENT_JOBS", runtime.NumCPU()),
		PollInterval:         getEnvDuration("CLIPFACTORY_POLL_INTERVAL", "5s"),
		WorkerID:             getEnv("CLIPFACTORY_WORKER_ID", defaultWorkerID()),
		TwitchDownloaderPath: getEnv("CLIPFACTORY_TWITCH_DOWNLOADER_PATH", "TwitchDownloaderCLI"),
		FFmpegPath:           getEnv("CLIPFACTORY_FFMPEG_PATH", "ffmpeg"),
	}
	// intervalo del poll de publications (re-encolado automático de publishes)
	cfg.PollPublicationsInterval = getEnvDuration("CLIPFACTORY_POLL_PUBLICATIONS_INTERVAL", "5m")

	// auto-discovery al arrancar el worker ("true"/"1"/"yes"; default true)
	cfg.DiscoverOnStart = getEnvBool("CLIPFACTORY_DISCOVER_ON_START", true)

	// retención de videos completados
	cfg.RetentionInterval = getEnvDuration("CLIPFACTORY_RETENTION_INTERVAL", "24h")
	cfg.RetentionMaxAge = getEnvDuration("CLIPFACTORY_RETENTION_MAX_AGE", "720h")

	// espacio libre mínimo en disco (bytes)
	cfg.MinFreeDiskSpace = getEnvInt64("CLIPFACTORY_MIN_FREE_DISK_SPACE", 1024*1024*1024) // 1GB default

	// métricas Prometheus
	cfg.MetricsAddr = getEnv("CLIPFACTORY_METRICS_ADDR", "")

	// 2) sources.yaml (canales a monitorear) — opcional, stub hoy
	sources, err := loadSources(filepath.Join(cfg.ConfigDir, "sources.yaml"))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load sources config: %w", err)
	}
	cfg.Sources = sources

	// 3) credentials/twitch.conf — opcional
	twitchCfg, err := loadTwitchConfig(filepath.Join(cfg.CredentialsDir, "twitch.conf"))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load twitch config: %w", err)
	}
	cfg.Twitch = twitchCfg

	// 4) credentials/youtube.conf — opcional
	ytCfg, err := loadYouTubeConfig(filepath.Join(cfg.CredentialsDir, "youtube.conf"))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load youtube config: %w", err)
	}
	cfg.YouTube = ytCfg

	// 5) credentials/meta.conf — opcional (publicación en Facebook Reels)
	metaCfg, err := loadMetaConfig(filepath.Join(cfg.CredentialsDir, "meta.conf"))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load meta config: %w", err)
	}
	cfg.Meta = metaCfg

	return cfg, nil
}

// stripInlineComment corta un comentario inline de un valor de configuración:
// todo desde ` #` (espacio + numeral) hasta el fin de línea. Misma semántica
// que YAML: un `#` PEGADO al texto no es comentario (p.ej. "C#", "#1"), y un
// valor entre comillas se respeta íntegro (las comillas se quitan aparte).
//
// Sin esto, `active: false # pausado` fallaba con "active inválido" — un error
// que el sistema-test en vivo encontró en el primer walkthrough.
func stripInlineComment(value string) string {
	v := strings.TrimSpace(value)
	if strings.HasPrefix(v, "\"") || strings.HasPrefix(v, "'") {
		return v // valor citado: se respeta completo
	}
	// cortar en el primer '#' precedido de espacio o tab (espacio en blanco
	// antes del # = comentario; pegado al texto = parte del valor)
	if idx := strings.IndexAny(v, " \t"); idx >= 0 {
		if rest := strings.TrimSpace(v[idx+1:]); strings.HasPrefix(rest, "#") {
			v = strings.TrimSpace(v[:idx])
		}
	}
	return v
}

// sourcesYAML define la estructura del archivo sources.yaml para yaml.v3
type sourcesYAML struct {
	Sources []SourceConfig `yaml:"sources"`
}

// loadSources lee los canales a monitorear desde config/sources.yaml usando yaml.v3.
func loadSources(path string) ([]SourceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var sy sourcesYAML
	if err := yaml.Unmarshal(data, &sy); err != nil {
		return nil, fmt.Errorf("%s: error parseando YAML: %w", path, err)
	}

	if len(sy.Sources) == 0 {
		return nil, fmt.Errorf("%s: la lista 'sources' está vacía", path)
	}

	// validación por ítem: lo mínimo para que el discovery funcione
	for i, s := range sy.Sources {
		if s.Platform == "" || s.ChannelID == "" {
			return nil, fmt.Errorf("%s: sources[%d] necesita 'platform' y 'channel_id'", path, i)
		}
		switch s.Platform {
		case "twitch", "kick":
			// ok
		default:
			return nil, fmt.Errorf("%s: sources[%d] plataforma desconocida %q (soportadas: twitch, kick)", path, i, s.Platform)
		}
		if s.ChannelName == "" {
			sy.Sources[i].ChannelName = s.ChannelID // nombre legible por defecto
		}
		// active default true ya se maneja en SourceConfig
	}
	return sy.Sources, nil
}

// loadMetaConfig lee credentials/meta.conf (mismo formato clave=valor que
// twitch.conf/youtube.conf).
//
// Claves reconocidas:
//
//	PAGE_ID, ACCESS_TOKEN (obligatorios para publicar)
//	GRAPH_API_VERSION (default "v21.0")
func loadMetaConfig(path string) (MetaConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return MetaConfig{}, err
	}

	cfg := MetaConfig{}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := stripInlineComment(parts[1])
		switch key {
		case "PAGE_ID":
			cfg.PageID = value
		case "ACCESS_TOKEN":
			cfg.AccessToken = value
		case "GRAPH_API_VERSION":
			cfg.GraphVersion = value
		}
	}

	if cfg.GraphVersion == "" {
		cfg.GraphVersion = "v21.0"
	}
	return cfg, nil
}

// loadTwitchConfig lee un archivo clave=valor (estilo .env).
//
// Formato aceptado:
//   - Ignora líneas vacías y las que empiezan con '#' (comentarios)
//   - Toma SOLO la primera parte antes de '=' como clave (SplitN 2), de modo que
//     un valor con '=' adentro (p.ej. un token base64) se preserva intacto
//   - Recorta espacios de ambos lados: "CLIENT_ID = abc" es válido
//   - Claves desconocidas se ignoran silenciosamente (forward-compatible)
//
// El test TestLoadTwitchConfig documenta este comportamiento con ejemplos.
func loadTwitchConfig(path string) (TwitchConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return TwitchConfig{}, err
	}

	cfg := TwitchConfig{}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// saltar líneas vacías y comentarios
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// SplitN(2): clave = todo antes del primer '=', valor = el resto
		// (así un token con '=' adentro no se corrompe)
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue // línea sin '=' → ignorar
		}
		key := strings.TrimSpace(parts[0])
		value := stripInlineComment(parts[1])
		switch key {
		case "CLIENT_ID":
			cfg.ClientID = value
		case "AUTH_TOKEN":
			cfg.AuthToken = value
		}
	}
	return cfg, nil
}

// loadYouTubeConfig lee credentials/youtube.conf (mismo formato clave=valor que
// twitch.conf) y aplica defaults para los campos opcionales.
//
// Claves reconocidas:
//
//	CLIENT_ID, CLIENT_SECRET, REFRESH_TOKEN (obligatorios para publicar)
//	PRIVACY_STATUS (default "public"), CATEGORY_ID (default "20" = Gaming)
func loadYouTubeConfig(path string) (YouTubeConfig, error) {
	// mismo formato que twitch.conf: se redeclara la lectura porque
	// loadTwitchConfig mapea claves de Twitch (refactor futuro: parser genérico)
	data, err := os.ReadFile(path)
	if err != nil {
		return YouTubeConfig{}, err
	}

	cfg := YouTubeConfig{}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := stripInlineComment(parts[1])
		switch key {
		case "CLIENT_ID":
			cfg.ClientID = value
		case "CLIENT_SECRET":
			cfg.ClientSecret = value
		case "REFRESH_TOKEN":
			cfg.RefreshToken = value
		case "PRIVACY_STATUS":
			cfg.PrivacyStatus = value
		case "CATEGORY_ID":
			cfg.CategoryID = value
		}
	}

	// defaults para los campos opcionales (los obligatorios los valida el Publisher)
	if cfg.PrivacyStatus == "" {
		cfg.PrivacyStatus = "public"
	}
	if cfg.CategoryID == "" {
		cfg.CategoryID = "20"
	}
	return cfg, nil
}

// getEnv lee una variable de entorno y devuelve el default si está vacía o no existe.
func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// defaultWorkerID genera la identidad default del worker: hostname, con
// fallback a "worker-local" si el SO no lo reporta (contenedores con hostname
// raro tampoco es problema: cada contenedor tiene el suyo). Si algún día
// corren N workers en la MISMA máquina, el operador fija CLIPFACTORY_WORKER_ID
// distinto por proceso.
func defaultWorkerID() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "worker-local"
	}
	return h
}

// getEnvBool lee una variable de entorno como bool: "true", "1" y "yes" (en
// cualquier combinación de mayúsculas/minúsculas) son true; "false", "0" y
// "no" son false; cualquier otra cosa (incluido vacío/no existir) devuelve el
// default. Un valor no reconocido NO es error: se usa el default (misma
// tolerancia que getEnvInt/getEnvDuration).
func getEnvBool(key string, defaultValue bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	return defaultValue
}

// getEnvInt lee una variable de entorno como int; si falta o no es numérica,
// devuelve el default (los valores inválidos NO son error: fallar por una
// env var mal escrita mataría el worker entero).
func getEnvInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}

// getEnvDuration lee una variable de entorno como time.Duration (ej: "5s", "1m");
// si falta o es inválida, usa el default (y como último recurso 5s fijo).
func getEnvDuration(key string, defaultValue string) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	if d, err := time.ParseDuration(defaultValue); err == nil {
		return d
	}
	return 5 * time.Second
}

// getEnvInt64 lee una variable de entorno como int64; si falta o no es numérica,
// devuelve el default.
func getEnvInt64(key string, defaultValue int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return defaultValue
}

// Validate verifica que la configuración sea consistente antes de arrancar el worker.
//
// Lo mínimo indispensable: si no hay DataDir o DBPath el pipeline no puede correr.
// Un Twitch.ClientID vacío NO es error fatal (permite desarrollar el pipeline sin
// credenciales — los jobs que las necesitan fallan con mensaje claro), pero el
// discovery no podrá llamar a Helix.
func (c *Config) Validate() error {
	if c.DataDir == "" {
		return fmt.Errorf("data dir no configurado")
	}
	if c.DBPath == "" {
		return fmt.Errorf("db path no configurado")
	}
	if c.Twitch.ClientID == "" {
		// no es un error fatal, pero hay que tenerlo en cuenta para discovery
	}
	return nil
}
