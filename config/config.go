package config

// Carga de configuración de ClipFactory.
//
// FUENTES DE CONFIGURACIÓN (en orden de uso):
//
//  1. Variables de entorno CLIPFACTORY_* (ver LoadConfig para la lista completa).
//     Son la vía principal: funcionan igual en bare metal y en Docker
//     (docker-compose.yml ya las define para el contenedor de desarrollo).
//
//  2. credentials/twitch.conf: archivo clave=valor estilo .env con las credenciales
//     de Twitch (CLIENT_ID, AUTH_TOKEN). Formato documentado en docs/guia-twitch.md.
//
//  3. config/sources.yaml (FUTURO): canales a monitorear. loadSources es un stub.
//
// Las credenciales viven separadas de la config para poder montar credentials/ con
// permisos restringidos (y para no commitear nunca secretos).

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config representa la configuración completa de ClipFactory.
type Config struct {
	// Paths
	DataDir        string
	DBPath         string
	LogDir         string
	ConfigDir      string
	CredentialsDir string

	// Logging
	LogLevel       string

	// Worker
	MaxConcurrentJobs int
	PollInterval      time.Duration

	// Sources: canales a monitorear
	Sources []SourceConfig

	// Platform credentials (cargadas desde archivos en credentials/)
	Twitch  TwitchConfig
	YouTube YouTubeConfig
	// Meta, TikTok, Kick se agregan en fases posteriores

	// Ruta al binario TwitchDownloaderCLI (para el job 'download').
	// Default: "TwitchDownloaderCLI" (se asume en PATH).
	TwitchDownloaderPath string

	// Ruta al binario ffmpeg (para el job 'process').
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

// TwitchConfig representa las credenciales de Twitch.
type TwitchConfig struct {
	ClientID  string // Client-ID de la app registrada en Twitch
	AuthToken string // OAuth token (opcional para algunos endpoints)
}

// YouTubeConfig representa las credenciales de la YouTube Data API v3.
// El RefreshToken se genera UNA VEZ fuera del pipeline (docs/guia-youtube.md §4).
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
// twitch.conf) no existen en una instalación fresca y su ausencia NO es error.
// Pero si el archivo existe y no se puede leer (permisos, es un directorio...),
// eso SÍ es error: mejor fallar temprano que descubrirlo en producción.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		DataDir:        getEnv("CLIPFACTORY_DATA_DIR", "./data"),
		DBPath:         getEnv("CLIPFACTORY_DB_PATH", "./database/clipfactory.db"),
		LogDir:         getEnv("CLIPFACTORY_LOG_DIR", "./logs"),
		ConfigDir:      getEnv("CLIPFACTORY_CONFIG_DIR", "./config"),
		CredentialsDir: getEnv("CLIPFACTORY_CREDENTIALS_DIR", "./credentials"),
		LogLevel:       getEnv("CLIPFACTORY_LOG_LEVEL", "info"),
		MaxConcurrentJobs: getEnvInt("CLIPFACTORY_MAX_CONCURRENT_JOBS", runtime.NumCPU()),
		PollInterval:      getEnvDuration("CLIPFACTORY_POLL_INTERVAL", "5s"),
		TwitchDownloaderPath: getEnv("CLIPFACTORY_TWITCH_DOWNLOADER_PATH", "TwitchDownloaderCLI"),
		FFmpegPath:           getEnv("CLIPFACTORY_FFMPEG_PATH", "ffmpeg"),
	}

	// cargar fuentes desde archivo de config (si existe)
	sources, err := loadSources(filepath.Join(cfg.ConfigDir, "sources.yaml"))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load sources config: %w", err)
	}
	cfg.Sources = sources

	// cargar credenciales de Twitch desde archivo (si existe)
	twitchCfg, err := loadTwitchConfig(filepath.Join(cfg.CredentialsDir, "twitch.conf"))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load twitch config: %w", err)
	}
	cfg.Twitch = twitchCfg

	// cargar credenciales de YouTube desde archivo (si existe)
	ytCfg, err := loadYouTubeConfig(filepath.Join(cfg.CredentialsDir, "youtube.conf"))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load youtube config: %w", err)
	}
	cfg.YouTube = ytCfg

	return cfg, nil
}

func loadSources(path string) ([]SourceConfig, error) {
	// TODO: implementar lectura de YAML/JSON de fuentes
	// Por ahora retornamos un slice vacío
	return nil, nil
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
	// leer archivo de credenciales (formato clave=valor, similar a .env)
	data, err := os.ReadFile(path)
	if err != nil {
		return TwitchConfig{}, err
	}

	cfg := TwitchConfig{}
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
		value := strings.TrimSpace(parts[1])
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
//   CLIENT_ID, CLIENT_SECRET, REFRESH_TOKEN (obligatorios para publicar)
//   PRIVACY_STATUS (default "public"), CATEGORY_ID (default "20" = Gaming)
func loadYouTubeConfig(path string) (YouTubeConfig, error) {
	// mismo formato que twitch.conf: leer con el mismo parser genérico
	// (se redeclara la lectura porque loadTwitchConfig mapea claves de Twitch)
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
		value := strings.TrimSpace(parts[1])
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

	// defaults opcionales (los obligatorios los valida Validate/Publisher)
	if cfg.PrivacyStatus == "" {
		cfg.PrivacyStatus = "public"
	}
	if cfg.CategoryID == "" {
		cfg.CategoryID = "20"
	}
	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}

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

// Validate verifica que la configuración sea consistente antes de arrancar el worker.
//
// Lo mínimo indispensable: si no hay DataDir o DBPath el pipeline no puede correr.
// Un Twitch.ClientID vacío NO es error fatal (permite desarrollar el pipeline sin
// credenciales), pero el discovery no podrá llamar a Helix — se valida en su lugar.
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
