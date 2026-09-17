package config

// Tests de la carga de configuración: defaults por env var, parsing de
// credentials/twitch.conf y tolerancia a archivos faltantes.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setEnv establece una variable de entorno y devuelve una función para restaurarla.
func setEnv(t *testing.T, key, value string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("setenv %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

func TestLoadConfigDefaults(t *testing.T) {
	// limpiar todas las variables relevantes para probar los defaults
	keys := []string{
		"CLIPFACTORY_DATA_DIR", "CLIPFACTORY_DB_PATH", "CLIPFACTORY_LOG_DIR",
		"CLIPFACTORY_CONFIG_DIR", "CLIPFACTORY_CREDENTIALS_DIR", "CLIPFACTORY_LOG_LEVEL",
		"CLIPFACTORY_MAX_CONCURRENT_JOBS", "CLIPFACTORY_POLL_INTERVAL",
	}
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		os.Unsetenv(k)
		if had {
			defer os.Setenv(k, old)
		}
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.DataDir != "./data" {
		t.Errorf("expected DataDir './data', got '%s'", cfg.DataDir)
	}
	if cfg.DBPath != "./database/clipfactory.db" {
		t.Errorf("expected DBPath './database/clipfactory.db', got '%s'", cfg.DBPath)
	}
	if cfg.LogDir != "./logs" {
		t.Errorf("expected LogDir './logs', got '%s'", cfg.LogDir)
	}
	if cfg.ConfigDir != "./config" {
		t.Errorf("expected ConfigDir './config', got '%s'", cfg.ConfigDir)
	}
	if cfg.CredentialsDir != "./credentials" {
		t.Errorf("expected CredentialsDir './credentials', got '%s'", cfg.CredentialsDir)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected LogLevel 'info', got '%s'", cfg.LogLevel)
	}
	if cfg.PollInterval != 5*time.Second {
		t.Errorf("expected PollInterval 5s, got %v", cfg.PollInterval)
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	setEnv(t, "CLIPFACTORY_DATA_DIR", "/tmp/cf-data")
	setEnv(t, "CLIPFACTORY_DB_PATH", "/tmp/cf/db.sqlite")
	setEnv(t, "CLIPFACTORY_LOG_LEVEL", "debug")
	setEnv(t, "CLIPFACTORY_MAX_CONCURRENT_JOBS", "4")
	setEnv(t, "CLIPFACTORY_POLL_INTERVAL", "10s")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.DataDir != "/tmp/cf-data" {
		t.Errorf("expected DataDir '/tmp/cf-data', got '%s'", cfg.DataDir)
	}
	if cfg.DBPath != "/tmp/cf/db.sqlite" {
		t.Errorf("expected DBPath '/tmp/cf/db.sqlite', got '%s'", cfg.DBPath)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel 'debug', got '%s'", cfg.LogLevel)
	}
	if cfg.MaxConcurrentJobs != 4 {
		t.Errorf("expected MaxConcurrentJobs 4, got %d", cfg.MaxConcurrentJobs)
	}
	if cfg.PollInterval != 10*time.Second {
		t.Errorf("expected PollInterval 10s, got %v", cfg.PollInterval)
	}
}

func TestLoadConfigInvalidInt(t *testing.T) {
	// valor inválido debe caer al default (runtime.NumCPU())
	setEnv(t, "CLIPFACTORY_MAX_CONCURRENT_JOBS", "not-a-number")
	setEnv(t, "CLIPFACTORY_POLL_INTERVAL", "not-a-duration")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.MaxConcurrentJobs < 1 {
		t.Errorf("expected MaxConcurrentJobs >= 1 (NumCPU default), got %d", cfg.MaxConcurrentJobs)
	}
	if cfg.PollInterval != 5*time.Second {
		t.Errorf("expected PollInterval fallback 5s, got %v", cfg.PollInterval)
	}
}

func TestLoadTwitchConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "twitch.conf")
	content := "# comentario\nCLIENT_ID = abc123\nAUTH_TOKEN=oauth:xyz789\n\nlinea_invalida_sin_igual\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write twitch.conf: %v", err)
	}

	cfg, err := loadTwitchConfig(path)
	if err != nil {
		t.Fatalf("load twitch config: %v", err)
	}

	if cfg.ClientID != "abc123" {
		t.Errorf("expected ClientID 'abc123', got '%s'", cfg.ClientID)
	}
	if cfg.AuthToken != "oauth:xyz789" {
		t.Errorf("expected AuthToken 'oauth:xyz789', got '%s'", cfg.AuthToken)
	}
}

func TestLoadTwitchConfigMissingFile(t *testing.T) {
	_, err := loadTwitchConfig(filepath.Join(t.TempDir(), "noexiste.conf"))
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected not-exist error, got: %v", err)
	}
}

func TestLoadConfigMissingCredentialsFileIsNotFatal(t *testing.T) {
	setEnv(t, "CLIPFACTORY_CREDENTIALS_DIR", filepath.Join(t.TempDir(), "credentials"))
	setEnv(t, "CLIPFACTORY_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("expected missing optional files to be non-fatal, got: %v", err)
	}
	if cfg.Twitch.ClientID != "" {
		t.Errorf("expected empty ClientID, got '%s'", cfg.Twitch.ClientID)
	}
}

func TestLoadConfigInvalidCredentialsFileIsFatal(t *testing.T) {
	// crear un directorio donde se espera un archivo: ReadFile devuelve error que no es IsNotExist
	dir := t.TempDir()
	setEnv(t, "CLIPFACTORY_CREDENTIALS_DIR", dir)
	setEnv(t, "CLIPFACTORY_CONFIG_DIR", filepath.Join(dir, "noexiste"))

	if err := os.Mkdir(filepath.Join(dir, "twitch.conf"), 0o755); err != nil {
		t.Fatalf("mkdir twitch.conf: %v", err)
	}

	_, err := LoadConfig()
	if err == nil {
		t.Error("expected error for unreadable credentials file, got nil")
	}
}

func TestLoadConfigAPIFields(t *testing.T) {
	setEnv(t, "CLIPFACTORY_API_ADDR", ":9099")
	setEnv(t, "CLIPFACTORY_API_TOKEN", "secreto-operador")
	setEnv(t, "CLIPFACTORY_CORS_ORIGINS", "http://localhost:5173, https://app.vercel.app ,")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.APIAddr != ":9099" {
		t.Errorf("expected APIAddr ':9099', got '%s'", cfg.APIAddr)
	}
	if cfg.APIToken != "secreto-operador" {
		t.Errorf("expected APIToken 'secreto-operador', got '%s'", cfg.APIToken)
	}
	want := []string{"http://localhost:5173", "https://app.vercel.app"}
	if len(cfg.CORSOrigins) != len(want) {
		t.Fatalf("expected %d CORS origins, got %v", len(want), cfg.CORSOrigins)
	}
	for i := range want {
		if cfg.CORSOrigins[i] != want[i] {
			t.Errorf("CORSOrigins[%d]: expected %q, got %q", i, want[i], cfg.CORSOrigins[i])
		}
	}
}

func TestGetEnvListFallbacks(t *testing.T) {
	defaultList := []string{"http://localhost:5173"}

	// variable ausente → default
	os.Unsetenv("CLIPFACTORY_TEST_LIST")
	if got := getEnvList("CLIPFACTORY_TEST_LIST", defaultList); len(got) != 1 || got[0] != defaultList[0] {
		t.Errorf("expected default list, got %v", got)
	}

	// solo comas/espacios → default
	setEnv(t, "CLIPFACTORY_TEST_LIST", " , , ")
	if got := getEnvList("CLIPFACTORY_TEST_LIST", defaultList); len(got) != 1 || got[0] != defaultList[0] {
		t.Errorf("expected default list for blank value, got %v", got)
	}

	// valor único sin espacios extra
	setEnv(t, "CLIPFACTORY_TEST_LIST", "*")
	if got := getEnvList("CLIPFACTORY_TEST_LIST", defaultList); len(got) != 1 || got[0] != "*" {
		t.Errorf("expected ['*'], got %v", got)
	}
}

func TestValidate(t *testing.T) {
	cfg := &Config{DataDir: "./data", DBPath: "./db.sqlite"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid config, got: %v", err)
	}

	if err := (&Config{DBPath: "./db.sqlite"}).Validate(); err == nil {
		t.Error("expected error when DataDir is empty")
	}

	if err := (&Config{DataDir: "./data"}).Validate(); err == nil {
		t.Error("expected error when DBPath is empty")
	}
}

// ---- youtube.conf ----

func TestLoadYouTubeConfigFull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "youtube.conf")
	content := "# credenciales de YouTube\nCLIENT_ID=abc123.apps.googleusercontent.com\nCLIENT_SECRET=secreto\nREFRESH_TOKEN=1//token\nPRIVACY_STATUS=unlisted\nCATEGORY_ID=42\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write youtube.conf: %v", err)
	}

	cfg, err := loadYouTubeConfig(path)
	if err != nil {
		t.Fatalf("loadYouTubeConfig: %v", err)
	}
	if cfg.ClientID != "abc123.apps.googleusercontent.com" {
		t.Errorf("ClientID: %q", cfg.ClientID)
	}
	if cfg.ClientSecret != "secreto" {
		t.Errorf("ClientSecret: %q", cfg.ClientSecret)
	}
	if cfg.RefreshToken != "1//token" {
		t.Errorf("RefreshToken: %q", cfg.RefreshToken)
	}
	if cfg.PrivacyStatus != "unlisted" {
		t.Errorf("PrivacyStatus: %q", cfg.PrivacyStatus)
	}
	if cfg.CategoryID != "42" {
		t.Errorf("CategoryID: %q", cfg.CategoryID)
	}
}

func TestLoadYouTubeConfigDefaults(t *testing.T) {
	// solo obligatorios: PRIVACY_STATUS y CATEGORY_ID toman defaults
	dir := t.TempDir()
	path := filepath.Join(dir, "youtube.conf")
	content := "CLIENT_ID=id\nCLIENT_SECRET=sec\nREFRESH_TOKEN=tok\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write youtube.conf: %v", err)
	}

	cfg, err := loadYouTubeConfig(path)
	if err != nil {
		t.Fatalf("loadYouTubeConfig: %v", err)
	}
	if cfg.PrivacyStatus != "public" {
		t.Errorf("expected default PrivacyStatus 'public', got %q", cfg.PrivacyStatus)
	}
	if cfg.CategoryID != "20" {
		t.Errorf("expected default CategoryID '20' (Gaming), got %q", cfg.CategoryID)
	}
}

func TestLoadYouTubeConfigMissingIsOK(t *testing.T) {
	// el archivo es OPCIONAL: si no existe se devuelve el error y LoadConfig
	// lo ignora cuando es os.IsNotExist (ver LoadConfig)
	_, err := loadYouTubeConfig(filepath.Join(t.TempDir(), "noexiste.conf"))
	if err == nil {
		t.Error("expected error for missing file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist, got: %v", err)
	}
}

func TestLoadConfigYouTubeIntegration(t *testing.T) {
	// flujo completo: LoadConfig lee credentials/youtube.conf si existe
	dir := t.TempDir()
	setEnv(t, "CLIPFACTORY_CREDENTIALS_DIR", dir)
	setEnv(t, "CLIPFACTORY_CONFIG_DIR", filepath.Join(dir, "noexiste"))

	content := "CLIENT_ID=int-id\nCLIENT_SECRET=int-sec\nREFRESH_TOKEN=int-tok\n"
	if err := os.WriteFile(filepath.Join(dir, "youtube.conf"), []byte(content), 0o600); err != nil {
		t.Fatalf("write youtube.conf: %v", err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.YouTube.ClientID != "int-id" || cfg.YouTube.RefreshToken != "int-tok" {
		t.Errorf("expected YouTube credentials loaded, got %+v", cfg.YouTube)
	}
	if cfg.YouTube.PrivacyStatus != "public" || cfg.YouTube.CategoryID != "20" {
		t.Errorf("expected YouTube defaults, got %+v", cfg.YouTube)
	}
}
