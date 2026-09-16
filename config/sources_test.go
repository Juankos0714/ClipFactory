package config

// Tests del parser de sources.yaml (subconjunto YAML parseado a mano) y de
// credentials/meta.conf.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemp escribe un archivo temporal con el contenido dado.
func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestLoadSourcesOK(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "sources.yaml", `
# canales
sources:
  - platform: twitch
    channel_id: "4919"
    channel_name: illojuan
    active: true
  - platform: kick
    channel_id: xokas
    channel_name: xokas (kick)
`)

	sources, err := loadSources(path)
	if err != nil {
		t.Fatalf("loadSources: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sources))
	}

	if sources[0].Platform != "twitch" || sources[0].ChannelID != "4919" ||
		sources[0].ChannelName != "illojuan" || !sources[0].Active {
		t.Errorf("unexpected source[0]: %+v", sources[0])
	}
	if sources[1].Platform != "kick" || sources[1].ChannelID != "xokas" || !sources[1].Active {
		t.Errorf("unexpected source[1]: %+v", sources[1])
	}
}

func TestLoadSourcesActiveFalse(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "sources.yaml", "sources:\n  - platform: twitch\n    channel_id: 1\n    active: false\n")

	sources, err := loadSources(path)
	if err != nil {
		t.Fatalf("loadSources: %v", err)
	}
	if len(sources) != 1 || sources[0].Active {
		t.Errorf("expected active=false, got %+v", sources[0])
	}
}

func TestLoadSourcesDefaultChannelName(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "sources.yaml", "sources:\n  - platform: kick\n    channel_id: xokas\n")

	sources, err := loadSources(path)
	if err != nil {
		t.Fatalf("loadSources: %v", err)
	}
	if sources[0].ChannelName != "xokas" {
		t.Errorf("expected channel_name fallback to channel_id, got %q", sources[0].ChannelName)
	}
}

func TestLoadSourcesFirstKeyOnDashLine(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "sources.yaml", "sources:\n  - platform: twitch\n    channel_id: 42\n")

	sources, err := loadSources(path)
	if err != nil {
		t.Fatalf("loadSources: %v", err)
	}
	if len(sources) != 1 || sources[0].Platform != "twitch" || sources[0].ChannelID != "42" {
		t.Errorf("unexpected sources: %+v", sources)
	}
}

func TestLoadSourcesUnknownPlatform(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "sources.yaml", "sources:\n  - platform: tiktik\n    channel_id: x\n")

	if _, err := loadSources(path); err == nil {
		t.Error("expected error for unknown platform")
	} else if !strings.Contains(err.Error(), "plataforma desconocida") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoadSourcesMissingRequiredFields(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "sources.yaml", "sources:\n  - platform: twitch\n")

	if _, err := loadSources(path); err == nil {
		t.Error("expected error for missing channel_id")
	}
}

func TestLoadSourcesUnknownKey(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "sources.yaml", "sources:\n  - platform: twitch\n    channel_id: 1\n    invalid_key: x\n")

	if _, err := loadSources(path); err == nil {
		t.Error("expected error for unknown key")
	}
}

func TestLoadSourcesContentOutsideSources(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "sources.yaml", "otra_clave:\n  - x\n")

	if _, err := loadSources(path); err == nil {
		t.Error("expected error for content outside 'sources:'")
	}
}

func TestLoadSourcesInvalidActive(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "sources.yaml", "sources:\n  - platform: twitch\n    channel_id: 1\n    active: a veces\n")

	if _, err := loadSources(path); err == nil {
		t.Error("expected error for invalid active value")
	}
}

func TestLoadSourcesInlineComment(t *testing.T) {
	// regresión del system-test: `active: false # pausado` fallaba con
	// "active inválido" porque el comentario inline no se cortaba
	dir := t.TempDir()
	path := writeTemp(t, dir, "sources.yaml", "sources:\n"+
		"  - platform: twitch\n"+
		"    channel_id: 1\n"+
		"    active: false   # pausado temporalmente\n"+
		"  - platform: kick\n"+
		"    channel_id: xokas # sin espacio antes del # NO es comentario\n"+
		"    channel_name: kanal #1\n")

	sources, err := loadSources(path)
	if err != nil {
		t.Fatalf("loadSources con comentarios inline: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(sources))
	}
	if sources[0].Active {
		t.Errorf("expected active=false (comentario inline cortado), got %+v", sources[0])
	}
	if sources[1].ChannelID != "xokas" {
		t.Errorf("channel_id con # pegado: %q", sources[1].ChannelID)
	}
	if sources[1].ChannelName != "kanal" {
		t.Errorf("channel_name: esperaba 'kanal' (comentario cortado), got %q", sources[1].ChannelName)
	}
}

func TestStripInlineCommentQuotedValue(t *testing.T) {
	// un valor entre comillas se respeta íntegro (los # adentro no comentan)
	if got := stripInlineComment(`"token # con hash"`); got != `"token # con hash"` {
		t.Errorf("valor citado modificado: %q", got)
	}
	if got := stripInlineComment(`abc#pegado`); got != `abc#pegado` {
		t.Errorf("# pegado al texto no debe cortar: %q", got)
	}
	if got := stripInlineComment(`valor # comentario`); got != "valor" {
		t.Errorf("comentario no cortado: %q", got)
	}
	if got := stripInlineComment("valor\t# con tab"); got != "valor" {
		t.Errorf("comentario tras tab no cortado: %q", got)
	}
}

func TestLoadSourcesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "sources.yaml", "# solo comentarios\n\n")

	sources, err := loadSources(path)
	if err != nil {
		t.Fatalf("loadSources: %v", err)
	}
	if len(sources) != 0 {
		t.Errorf("expected 0 sources, got %d", len(sources))
	}
}

func TestLoadConfigSourcesIntegration(t *testing.T) {
	dir := t.TempDir()
	setEnv(t, "CLIPFACTORY_CONFIG_DIR", dir)
	setEnv(t, "CLIPFACTORY_CREDENTIALS_DIR", filepath.Join(dir, "no-credentials"))

	writeTemp(t, dir, "sources.yaml", "sources:\n  - platform: kick\n    channel_id: xokas\n")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0].Platform != "kick" {
		t.Errorf("expected kick source loaded, got %+v", cfg.Sources)
	}
}

// ---- credentials/meta.conf ----

func TestLoadMetaConfigFull(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "meta.conf", "# meta\nPAGE_ID=123456789\nACCESS_TOKEN=EAAG...token\nGRAPH_API_VERSION=v20.0\n")

	cfg, err := loadMetaConfig(path)
	if err != nil {
		t.Fatalf("loadMetaConfig: %v", err)
	}
	if cfg.PageID != "123456789" {
		t.Errorf("PageID: %q", cfg.PageID)
	}
	if cfg.AccessToken != "EAAG...token" {
		t.Errorf("AccessToken: %q", cfg.AccessToken)
	}
	if cfg.GraphVersion != "v20.0" {
		t.Errorf("GraphVersion: %q", cfg.GraphVersion)
	}
}

func TestLoadMetaConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "meta.conf", "PAGE_ID=42\nACCESS_TOKEN=tok\n")

	cfg, err := loadMetaConfig(path)
	if err != nil {
		t.Fatalf("loadMetaConfig: %v", err)
	}
	if cfg.GraphVersion != "v21.0" {
		t.Errorf("expected default GraphVersion v21.0, got %q", cfg.GraphVersion)
	}
}

func TestLoadMetaConfigMissingIsOK(t *testing.T) {
	// archivo opcional: LoadConfig ignora el error si es os.IsNotExist
	_, err := loadMetaConfig(filepath.Join(t.TempDir(), "noexiste.conf"))
	if err == nil {
		t.Error("expected error for missing file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist, got: %v", err)
	}
}

func TestLoadConfigMetaIntegration(t *testing.T) {
	dir := t.TempDir()
	setEnv(t, "CLIPFACTORY_CREDENTIALS_DIR", dir)
	setEnv(t, "CLIPFACTORY_CONFIG_DIR", filepath.Join(dir, "noexiste"))

	writeTemp(t, dir, "meta.conf", "PAGE_ID=777\nACCESS_TOKEN=mtok\n")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Meta.PageID != "777" || cfg.Meta.AccessToken != "mtok" {
		t.Errorf("expected Meta credentials loaded, got %+v", cfg.Meta)
	}
}
