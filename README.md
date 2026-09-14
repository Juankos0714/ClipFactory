# ClipFactory

Pipeline automatizado de clips para YouTube Shorts y más: descarga clips de canales de
Twitch/Kick, los recorta a formato vertical **1080x1920** con ffmpeg y los publica en
YouTube/Meta/TikTok/Kick.

Diseñado para correr en hardware modesto (i3-3220, 2 núcleos) sobre un servidor Linux
(`nico-server`), con desarrollo en Windows + Docker.

---

## Índice

1. [Arquitectura](#arquitectura)
2. [Estructura del proyecto](#estructura-del-proyecto)
3. [Flujo del pipeline](#flujo-del-pipeline)
4. [La base de datos](#la-base-de-datos)
5. [Configuración](#configuración)
6. [Cómo ejecutar](#cómo-ejecutar)
7. [Tests](#tests)
8. [Guía de Twitch](docs/guia-twitch.md) ← guía paso a paso para clips de Twitch
9. [Guía de YouTube](docs/guia-youtube.md) ← OAuth, cuotas y publicación
9. [Decisiones de diseño](#decisiones-de-diseño)
10. [Roadmap](#roadmap)

---

## Arquitectura

```
                    ┌─────────────────────────────────────────────────────┐
                    │                    clipfactory (binario)            │
                    │                                                     │
 Twitch Helix API   │  ┌──────────┐   ┌──────────┐   ┌───────────────┐    │
 (clips de canal) ──┼─►│ discovery│──►│ download │──►│ process (ffmpeg)│   │
                    │  └──────────┘   └──────────┘   └───────┬───────┘    │
                    │                                        │            │
                    │  ┌─────────────────────────────────────▼─────────┐  │
                    │  │              cola de jobs (SQLite)            │  │
                    │  │  jobs: queued → running → done / error        │  │
                    │  └─────────────────────────────────────┬─────────┘  │
                    │                                        │            │
                    │  ┌──────────┐   ┌──────────┐   ┌───────▼───────┐    │
 YouTube / Meta /   │  │ thumbnail│   │  review  │──►│   publish     │────┼──►
 TikTok / Kick ◄────┼──└──────────┘   └──────────┘   └───────────────┘    │
                    └─────────────────────────────────────────────────────┘
```

**Conceptos clave:**

- **Worker único con cola interna**: un solo proceso sondea la tabla `jobs` de SQLite
  cada `CLIPFACTORY_POLL_INTERVAL` (5s por defecto) y ejecuta hasta
  `CLIPFACTORY_MAX_CONCURRENT_JOBS` (2 por defecto: 1 ffmpeg + 1 descarga) trabajos
  en paralelo.
- **Locks en la DB**: cada job se "lockea" (`locked_at`, `locked_by`) al tomarlo.
  Si el worker muere, el job vuelve a estar disponible tras 30s (lock obsoleto).
  Esto permite en el futuro escalar a varios workers sin cambiar la lógica.
- **Idempotencia**: todos los registros tienen índices UNIQUE
  (`(platform, channel_id)`, `(platform, platform_clip_id)`, `filepath`,
  `(clip_id, platform)`) para que reintentos no dupliquen datos.
- **Estados explícitos**: cada entidad (`source_clips`, `videos`, `clips`,
  `publications`) tiene una columna `status` con máquina de estados documentada.

## Estructura del proyecto

```
clipfactory/
├── cmd/clipfactory/         # punto de entrada (CLI: worker, status, discovery, help)
├── config/                  # carga de configuración (env vars + archivos)
├── internal/
│   ├── adapter/twitch/      # adaptador de Twitch (API Helix + descarga)
│   ├── db/                  # esquema SQLite, migraciones y CRUD tipado
│   └── worker/              # loop del worker, cola de jobs, ejecutores por tipo
├── data/                    # archivos: incoming/, processing/, completed/, failed/, thumbnails/
├── database/                # clipfactory.db (SQLite en modo WAL)
├── credentials/             # credenciales por plataforma (twitch.conf, ...)
├── logs/                    # logs de la aplicación
├── scripts/                 # scripts de PowerShell para Docker (dev y tests)
├── docs/                    # documentación adicional (guías por plataforma)
├── Dockerfile               # multi-stage: deps → dev → prod
└── docker-compose.yml       # entorno de desarrollo con código montado
```

**Regla de visibilidad**: `internal/` es privado del módulo Go — nada externo puede
importarlo. Los adaptadores de plataforma viven ahí para que las credenciales y la
lógica de API no se filtren a otros proyectos.

## Flujo del pipeline

| Fase | Qué hace | Tablas afectadas | Job type |
|------|----------|------------------|----------|
| 1. **Discovery** ✅ | Consulta clips nuevos vía API Helix (`/helix/clips` con paginación) y los registra | `source_clips` (status `detected`) | `discovery` |
| 2. **Download** ✅ | Descarga el clip con TwitchDownloaderCLI a `data/incoming/` (átomico: `.part` → rename) | `videos` (status `incoming`) | `download` |
| 3. **Process** ✅ | ffmpeg recorta a 1080x1920 (crop central 9:16 + libx264, salida atómica) y encola thumbnail | `clips` (status `completed`) | `process` |
| 4. **Thumbnail** ✅ | ffmpeg extrae un frame (0.5s) del clip como JPEG 1080x1920 | `clips.thumbnail_path` | `thumbnail` |
| 5. **Review** | (MVP: manual) el operador aprueba el clip | — | — |
| 6. **Publish** ✅ | Sube a YouTube Data API v3 (upload resumable) con reintentos y backoff | `publications` (status `pending` → `published`/`error`) | `publish` |

✅ = implementado y con tests. Los primeros pasos del pipeline ya funcionan
encadenados: `discovery` inserta clips nuevos y encola `download`; `download`
descarga y encola `process`; `process` recorta a 1080x1920 y encola `thumbnail`;
`thumbnail` extrae el frame de vista previa; `publish` sube el clip a YouTube
(vía OAuth refresh token + upload resumable) y registra el `external_id`.

**Idempotencia del publish**: si el worker muere después de subir el video pero
antes de actualizar la DB, el reintento detecta `status='published'` con
`external_id` y NO vuelve a subirlo (no-op). Los errores de cuota diaria
(`quotaExceeded`) NO cuentan como intento fallido: la publicación pasa a
`waiting_rate_limit` con `next_retry_at` a ~24h y `GetPendingPublications` la
reintenta sola tras el reset. Los demás errores usan backoff exponencial
(`next_retry_at = now · 2^attempts`, cap 24h).

**Idempotencia del discovery**: re-listar los mismos clips no duplica nada —
`UpsertSourceClip` es no-op para clips existentes y **no regresa** estados avanzados
(un clip ya `downloaded` nunca vuelve a `detected`), y solo los clips genuinamente
nuevos encolan download.

**Máquinas de estado:**

```
source_clips:  detected ──► downloaded ──► (skipped | error)
videos:        incoming ──► processing ──► (completed | failed)
clips:         processing ──► (completed | failed)
publications:  pending ──► (published | error | waiting_rate_limit)
jobs:          queued ──► running ──► (done | error)
```

## La base de datos

SQLite único en `database/clipfactory.db` con **WAL** (Write-Ahead Logging) para
permitir lecturas concurrentes con la escritura del worker.

**Tablas (7):**

| Tabla | Propósito | Claves UNIQUE |
|-------|-----------|---------------|
| `sources` | Canales a monitorear (Twitch/Kick) | `(platform, channel_id)` |
| `source_clips` | Clips detectados antes de descargar | `(platform, platform_clip_id)` |
| `videos` | Archivos descargados localmente | `filepath` |
| `clips` | Clips recortados a 1080x1920 | `filepath` |
| `publications` | Un registro por (clip, plataforma) para reintentos independientes | `(clip_id, platform)` |
| `jobs` | Cola de trabajos interna | — (usa `locked_at`/`locked_by`) |
| `logs` | Auditoría de eventos | — |

**Timestamps**: todas las columnas de tiempo se guardan como **texto RFC3339 UTC**
(`2026-09-13T10:00:00Z`). Esto permite comparaciones lexicográficas correctas en SQL
(sin depender de `datetime()` de SQLite, que usa otro formato y rompe las comparaciones
mixtas — ver comentario en `internal/db/models.go`).

**Migraciones**: `MigrateDB()` en `internal/db/migrations.go` ejecuta
`CREATE TABLE IF NOT EXISTS` idempotentes. No hay versionado de esquema todavía; cuando
haga falta, se agregará una tabla `schema_migrations`.

## Configuración

Todo se configura con variables de entorno (ver `config/config.go`):

| Variable | Default | Descripción |
|----------|---------|-------------|
| `CLIPFACTORY_DATA_DIR` | `./data` | raíz de los archivos de video |
| `CLIPFACTORY_DB_PATH` | `./database/clipfactory.db` | ruta de la DB SQLite |
| `CLIPFACTORY_LOG_DIR` | `./logs` | directorio de logs |
| `CLIPFACTORY_CONFIG_DIR` | `./config` | directorio de config (sources.yaml, futuro) |
| `CLIPFACTORY_CREDENTIALS_DIR` | `./credentials` | credenciales por plataforma |
| `CLIPFACTORY_LOG_LEVEL` | `info` | nivel de log |
| `CLIPFACTORY_MAX_CONCURRENT_JOBS` | `NumCPU()` | jobs en paralelo |
| `CLIPFACTORY_POLL_INTERVAL` | `5s` | intervalo de sondeo de la cola |
| `CLIPFACTORY_TWITCH_DOWNLOADER_PATH` | `TwitchDownloaderCLI` | ruta al binario de descarga de clips (ya instalado en la imagen Docker) |
| `CLIPFACTORY_FFMPEG_PATH` | `ffmpeg` | ruta al binario ffmpeg (ya instalado en la imagen Docker) |

**Credenciales de Twitch** (`credentials/twitch.conf`, formato clave=valor):

```
CLIENT_ID = tu_client_id
AUTH_TOKEN = oauth:tu_token
```

Ver la [Guía de Twitch](docs/guia-twitch.md) para obtenerlos paso a paso.

**Credenciales de YouTube** (`credentials/youtube.conf`, mismo formato):

```
CLIENT_ID = tu_client_id.apps.googleusercontent.com
CLIENT_SECRET = tu_client_secret
REFRESH_TOKEN = 1//tu_refresh_token
PRIVACY_STATUS = public   # opcional: public | unlisted | private (default public)
CATEGORY_ID = 20          # opcional: 20 = Gaming (default)
```

El `REFRESH_TOKEN` se genera UNA VEZ fuera del pipeline (flujo OAuth offline).
Ver la [Guía de YouTube](docs/guia-youtube.md) para el paso a paso completo,
incluyendo cómo generar el refresh token con `oauth2l` o `curl`.

## Cómo ejecutar

### Desarrollo (Docker, recomendado en Windows)

```powershell
# 1. levantar el contenedor de desarrollo (duerme con 'sleep infinity')
docker compose up -d

# 2. ejecutar comandos dentro del contenedor
docker compose exec clipfactory-dev /opt/clipfactory/bin/clipfactory --help
docker compose exec clipfactory-dev bash   # shell interactivo

# o usar los scripts de PowerShell
.\scripts\run-cli.ps1 -CliArgs status
.\scripts\test.ps1                          # tests en Docker
```

El `Dockerfile` tiene 3 stages:
- **deps**: solo descarga dependencias Go (capa cacheable).
- **dev**: agrega ffmpeg, mediainfo, sqlite3, curl, jq y compila el binario a
  `/opt/clipfactory/bin/clipfactory`. El código se monta por volumen.
- **prod**: imagen final con lo mínimo para correr.

### Binario nativo

```bash
CGO_ENABLED=0 go build -o clipfactory ./cmd/clipfactory
./clipfactory worker      # correr el worker
./clipfactory status      # estado del sistema
./clipfactory discovery   # pasada manual de discovery
```

## Tests

Los tests corren **dentro de Docker** (Linux, Go 1.24) para reproducir el entorno de
producción:

```powershell
.\scripts\test.ps1
```

o manualmente:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/app" -w /app \
  golang:1.24-bookworm go test -v ./...
```

Con detector de carreras de datos (race detector):

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/app" -w /app \
  golang:1.24-bookworm go test -race ./...
```

**Cobertura actual** (todos los paquetes con lógica tienen tests):

| Paquete | Qué se prueba |
|---------|---------------|
| `config` | defaults, overrides por env, parsing de twitch.conf, Validate() |
| `internal/db` | migraciones, CRUD completo, constraints UNIQUE/FK, cascadas, NULLs, comparaciones de timestamps, cleanup con retención |
| `internal/worker` | ciclo de vida (start/stop/restart), locks, procesamiento de jobs, concurrencia, tipos desconocidos |
| `internal/adapter/twitch` | validación de credenciales, request HTTP (headers, query), errores de API, cancelación de contexto |
| `internal/adapter/youtube` | OAuth refresh (token cache con expiración), upload resumable (init + PUT), clasificación de errores de cuota (`RateLimitError`), límites de metadata |
| `internal/worker` (publish) | éxito (status `published` + external_id), cuota (`waiting_rate_limit` sin consumir attempts), backoff exponencial de errores, idempotencia (no-op si ya published), sin Publisher, publication/clip inexistentes, archivo faltante, respeto de `next_retry_at` |

**Notas técnicas de los tests** (leer antes de tocar `internal/db`):

- Las DBs de prueba son `:memory:` con `SetMaxOpenConns(1)`: con SQLite en memoria,
  cada conexión nueva del pool sería una base **distinta** (perderías las tablas).
- `PRAGMA foreign_keys = ON` se ejecuta explícitamente: los parámetros DSN
  `_foreign_keys=on` no siempre son honrados por el driver `modernc.org/sqlite`.
- Las comparaciones de timestamps usan texto RFC3339, no `datetime()` de SQLite.

## Decisiones de diseño

1. **SQLite + WAL en vez de Postgres**: hardware modesto, un solo servidor, cero
   operaciones. WAL da concurrencia lector/escritor suficiente para el pipeline.
2. **Cola en la DB en vez de Redis/RabbitMQ**: misma razón. Los jobs con
   `locked_at/locked_by` dan la semántica de "at least once" con recuperación por
   timeouts.
3. **Driver `modernc.org/sqlite`** (SQLite puro en Go, sin CGO): el build es
   `CGO_ENABLED=0`, cross-compile trivial Windows→Linux, sin dependencias nativas.
4. **`CGO_ENABLED=0`**: binario estático, fácil de copiar a `nico-server`.
5. **ffmpeg con VAAPI y fallback a libx264**: en el i3-3220 no hay GPU útil, así que
   el fallback software es el camino normal; VAAPI queda para servidores con iGPU.
6. **Una fila de `publications` por plataforma**: un fallo en YouTube no bloquea a
   TikTok; cada una tiene su propio backoff (`next_retry_at`, `attempts`).
7. **Timestamps RFC3339 en texto**: comparaciones lexicográficas correctas en SQL sin
   funciones de fecha de SQLite (ver [La base de datos](#la-base-de-datos)).

## Roadmap

- [x] Discovery real con API Helix (parseo de respuesta JSON de clips + paginación)
      — ver [guía de Twitch](docs/guia-twitch.md)
- [x] Descarga con TwitchDownloaderCLI (job `download` completo con idempotencia)
- [x] Procesamiento ffmpeg (job `process`: crop central 9:16 → 1080x1920, libx264,
      salida atómica; VAAPI pendiente)
- [x] Thumbnails con ffmpeg (job `thumbnail`: frame a 0.5s como JPEG, atómico e
      idempotente)
- [x] Publicación en YouTube Data API v3 (job `publish`: OAuth refresh token,
      upload resumable, backoff exponencial, cuota diaria → `waiting_rate_limit`)
      — ver [guía de YouTube](docs/guia-youtube.md)
- [ ] Re-encolado automático de publications tras error/cuota (hoy: el job es
      de un solo tiro; el reintento natural lo hace `GetPendingPublications`)
- [ ] Publicación en Meta/TikTok/Kick
- [ ] `sources.yaml` para configurar canales (hoy: INSERT directo en `sources`)
- [ ] Versionado de migraciones (tabla `schema_migrations`)
