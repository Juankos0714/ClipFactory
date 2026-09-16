# Arquitectura y Decisiones Técnicas de ClipFactory

Documento de referencia que explica **por qué** el sistema es como es: cada
decisión técnica, qué alternativas se consideraron y qué consecuencias tiene.
Para el **cómo** paso a paso, ver la [Guía de Uso](guia-de-uso.md).

---

## Índice

1. [Visión general del sistema](#1-visión-general-del-sistema)
2. [El pipeline: una cola de jobs en SQLite](#2-el-pipeline-una-cola-de-jobs-en-sqlite)
3. [Decisiones por capa](#3-decisiones-por-capa)
   - 3.1 [Lenguaje y runtime: Go](#31-lenguaje-y-runtime-go)
   - 3.2 [Base de datos: SQLite + WAL](#32-base-de-datos-sqlite--wal)
   - 3.3 [Driver: modernc.org/sqlite sin CGO](#33-driver-moderncorgsqlite-sin-cgo)
   - 3.4 [Cola de jobs dentro de la DB](#34-cola-de-jobs-dentro-de-la-db)
   - 3.5 [Timestamps como texto RFC3339](#35-timestamps-como-texto-rfc3339)
   - 3.6 [Idempotencia por índices UNIQUE](#36-idempotencia-por-índices-unique)
   - 3.7 [Inyección de dependencias con interfaces](#37-inyección-de-dependencias-con-interfaces)
   - 3.8 [Procesos externos: TwitchDownloaderCLI y ffmpeg](#38-procesos-externos-twitchdownloadercli-y-ffmpeg)
   - 3.9 [YouTube: HTTP directo sin SDK](#39-youtube-http-directo-sin-sdk)
   - 3.10 [Docker multi-stage y scripts de PowerShell](#310-docker-multi-stage-y-scripts-de-powershell)
4. [Máquinas de estado](#4-máquinas-de-estado)
5. [Manejo de errores y reintentos](#5-manejo-de-errores-y-reintentos)
6. [Seguridad y secretos](#6-seguridad-y-secretos)
7. [Rendimiento en hardware modesto](#7-rendimiento-en-hardware-modesto)
8. [Limitaciones conocidas y roadmap](#8-limitaciones-conocidas-y-roadmap)

---

## 1. Visión general del sistema

ClipFactory automatiza el ciclo completo de publicación de clips:

```
Twitch (API Helix)          TwitchDownloaderCLI         ffmpeg                     YouTube Data API v3
┌─────────────────┐   ┌────────────────────────┐   ┌──────────────────────┐   ┌─────────────────────┐
│ discovery:      │   │ download:              │   │ process:             │   │ publish:            │
│ lista clips     │──►│ baja el mp4 a          │──►│ recorta a vertical   │──►│ sube el clip con    │
│ nuevos de un    │   │ data/incoming/         │   │ 1080x1920 y extrae   │   │ OAuth refresh token │
│ canal           │   │                        │   │ el thumbnail         │   │ (upload resumable)  │
└─────────────────┘   └────────────────────────┘   └──────────────────────┘   └─────────────────────┘
        └──────────────────────── TODOS pasan por la cola de jobs (SQLite) ────────────────────────┘
```

Cada flecha es un **job type** (`discovery`, `download`, `process`, `thumbnail`,
`publish`) encolado en la tabla `jobs` de una SQLite local. Un único proceso
worker sondea esa cola y ejecuta los jobs. Nada más: no hay Redis, ni colas
externas, ni servicios en la nube — una decisión deliberada explicada en §3.2 y §3.4.

**El problema que resuelve:** publicar clips de streamers en YouTube Shorts a
ritmo constante es trabajo repetitivo (buscar el clip, descargarlo, recortarlo a
vertical, subirlo con la metadata correcta). El pipeline lo convierte en un
proceso con estado, auditable y reanudable tras fallos.

**Restricciones de diseño** (dadas, no negociables):

| Restricción | Consecuencia en el diseño |
|---|---|
| Hardware modesto (i3-3220, 2 núcleos, sin GPU útil) | ffmpeg software con `preset veryfast`, 2 jobs concurrentes, SQLite en vez de un DBMS |
| Un solo servidor Linux (`nico-server`) | binario estático, sin orquestadores ni service mesh |
| Desarrollo en Windows | Docker como entorno reproducible; cross-compile trivial |
| Operador único (proyecto personal) | revisión manual del publish; UI mínima; DB visible con sqlite3 |

## 2. El pipeline: una cola de jobs en SQLite

### Por qué cada fase es un job y no una función que llama a la siguiente

1. **Recuperabilidad**: si el proceso muere durante el recorte de ffmpeg, al
   reiniciar el job `process` queda en `running` con lock obsoleto y se retoma.
   Con llamadas directas en cadena, un crash a mitad significaría re-hacer
   desde cero o implementar checkpoints a mano — que es justo lo que la cola
   ya da gratis.
2. **Observabilidad**: el estado del sistema entero se lee con un `SELECT` a
   `jobs`. Sabés exactamente qué clip está en qué fase y por qué falló.
3. **Desacoplamiento temporal**: discovery encola 50 downloads; el worker los
   procesa al ritmo que el hardware aguanta. No hay presión de memoria ni
   descargas paralelas descontroladas.

### El encadenado concreto

| Job | Se encola cuando | Apunta a (`reference_type`) | Al terminar encola |
|---|---|---|---|
| `discovery` | operador o job periódico | `sources` | N jobs `download` (solo clips nuevos) |
| `download` | discovery | `source_clips` | 1 job `process` |
| `process` | download | `videos` | 1 job `thumbnail` |
| `thumbnail` | process | `clips` | — (fin de cadena) |
| `publish` | operador, poll_publications, o el propio publish (reintento) | `publications` | job futuro en error/cuota (created_at = next_retry_at) |
| `poll_publications` | el worker cada `PollPublicationsInterval` (5m) | `system` | N jobs `publish` (publications pendientes sin job en vuelo) |

El `reference_id` + `reference_type` del job es un puntero polimórfico a la
tabla que contiene los datos del trabajo. Es más simple que una tabla de
payload JSON y mantiene la integridad referencial (la fila apuntada existe o
el job falla con mensaje claro).

### Concurrencia: qué protege a qué

- **Dos jobs a la vez** (`MaxConcurrentJobs=2`): uno suele ser ffmpeg (CPU)
  y otro una descarga (red). No compiten por el mismo recurso.
- **Lock de jobs a nivel SQL**: `LockJob` hace `UPDATE jobs SET status='running'
  ... WHERE id=? AND status='queued'`. Si dos workers compiten, solo uno gana
  (RowsAffected=1); el otro recibe error y sigue. La DB es el mutex.
- **Locks obsoletos (stale locks)**: si un worker muere con un job en
  `running`, `GetPendingJobs` lo vuelve a ofrecer cuando `locked_at` tiene más
  de 30 segundos. Semántica *at-least-once*: un job puede ejecutarse dos veces
  tras un crash, y por eso TODO handler es idempotente (§3.6).

## 3. Decisiones por capa

### 3.1 Lenguaje y runtime: Go

- **Binario único estático** (`CGO_ENABLED=0`): copiar a `nico-server` y
  correr. Sin instalar runtime, sin virtualenvs, sin conflictos de versión.
- **Goroutines para concurrencia barata**: el worker lanza cada job en su
  goroutine; el coste de las 2 concurrentes es despreciable.
- **`database/sql` estándar**: la capa DB es SQL plano con helpers tipados
  (ver `internal/db/models.go`), sin ORM. Con 8 tablas y ~20 queries, un ORM
  (GORM, ent) añadiría dependencias y reflection sin ganar nada.
- Alternativa descartada: **Python** (ecosistema de video YouTube API más
  maduro, pero distribución mucho más frágil en un servidor personal y sin
  binario único). **Node**: idem. **Rust**: máximo control, ciclo de edición
  más lento para un proyecto personal.

### 3.2 Base de datos: SQLite + WAL

- **Por qué no Postgres/MySQL**: un servidor DBMS para un pipeline de un solo
  proceso es una operativa extra (servicio, backups, actualizaciones, memoria)
  sin beneficio. SQLite corre embebida, se respalda copiando un archivo, y el
  modo WAL permite que lecturas concurrentes (status, queries del operador) no
  bloqueen la escritura del worker.
- **WAL (Write-Ahead Logging)**: los escritores van a un log y los lectores leen
  el snapshot consistente. Para este patrón de acceso (1 escritor, lecturas
  esporádicas) es el modo ideal. Contra: el archivo `clipfactory.db-wal` debe
  considerarse parte de los backups.
- **`PRAGMA foreign_keys = ON` explícito**: SQLite viene con FK desactivadas por
  defecto y el parámetro DSN `_foreign_keys=on` del driver `modernc.org/sqlite`
  no siempre se honra según versión. Por eso `InitDB` y todos los tests lo
  ejecutan como statement aparte después de abrir.

### 3.3 Driver: modernc.org/sqlite sin CGO

- `modernc.org/sqlite` es SQLite **traducido a Go puro** (sin CGO). Consecuencias:
  - `CGO_ENABLED=0` posible → cross-compile Windows→Linux en un comando.
  - Sin gcc/mingw en el toolchain de build → el Docker build es más simple.
  - Performance algo menor que `mattn/go-sqlite3` (C): irrelevante aquí, la DB
    hace cientos de writes/minuto como máximo.
- Alternativa descartada: `mattn/go-sqlite3` — requiere CGO, rompe el binario
  estático y complica el build multiplataforma.

### 3.4 Cola de jobs dentro de la DB

- **Por qué no Redis/RabbitMQ/NATS**: otro servicio que instalar, monitorear y
  respaldar. La tabla `jobs` con `status/locked_at/locked_by` da todo lo
  necesario para un solo servidor: FIFO, concurrencia limitada, recuperación
  por timeout.
- **Por qué no un canal Go (`chan`)**: la cola en memoria desaparece con el
  proceso. La persistencia del trabajo pendiente es EL punto del diseño:
  después de un crash, nada se pierde y nada se re-hace de más (con la
  idempotencia de §3.6).
- **Trade-off aceptado**: la DB puede crecer con jobs `done` históricos.
  Cleanup de jobs viejos está en el roadmap (§8).

### 3.5 Timestamps como texto RFC3339

Todas las columnas de tiempo se guardan como `TEXT` en formato RFC3339 UTC
(`2026-09-13T10:00:00Z`), escritas SIEMPRE con `db.NowUTC()`.

- **Por qué**: las cadenas RFC3339 UTC ordenan lexicográficamente igual que
  cronológicamente. Entonces `WHERE locked_at < '2026-...'` funciona en SQL
  puro sin funciones de fecha — y sin depender de `datetime()` de SQLite, que
  usa otro formato (`YYYY-MM-DD HH:MM:SS`) y **rompe las comparaciones** si se
  mezclan formatos en la misma columna.
- **El bug que esto evita**: `datetime('now')` como DEFAULT de columna produce
  el otro formato. El esquema lo trae por defecto, pero el código NUNCA inserta
  sin `NowUTC()`, y `parseTimeOrNull` acepta ambos formatos al leer para
  tolerar filas escritas por herramientas externas.
- Al leer, los strings NO se escanean a `time.Time` directo (el driver
  falla con "unsupported Scan"); se escanean a `sql.NullString` y se
  convierten con `parseTime`/`parseTimeOrNull`. Esa es también la razón de
  escanear NULLables en `sql.NullString` (un `NULL → string` directo paniquea).

### 3.6 Idempotencia por índices UNIQUE

Semántica *at-least-once* de la cola ⇒ todo handler debe ser re-ejecutable sin
efectos duplicados. La primera línea de defensa son los UNIQUE del esquema:

| Tabla | Constraint | Qué idempotencia garantiza |
|---|---|---|
| `sources` | `(platform, channel_id)` | no duplicar canales |
| `source_clips` | `(platform, platform_clip_id)` | re-listar clips no duplica ni re-encola |
| `videos` | `filepath` | re-descargar el mismo clip no crea dos filas |
| `clips` | `filepath` | re-procesar no crea dos clips |
| `publications` | `(clip_id, platform)` | una sola publicación por clip y plataforma |

Y cada handler además verifica antes de trabajar:

- `download`: si el source_clip ya está `downloaded` o ya hay fila en `videos`
  con ese filepath → no-op.
- `process`: si el video ya está `completed` o ya hay clip con ese filepath →
  no-op.
- `thumbnail`: si `clips.thumbnail_path` apunta a un archivo que existe → no-op;
  si el archivo desapareció → regenera en la misma ruta.
- `publish`: si la publicación ya está `published` con `external_id` → no-op.
  Esto protege el caso crítico: crash entre subir el video a YouTube y
  actualizar la DB. El reintento ve `published` y no vuelve a subir.

**Nombres de archivo deterministas**: `<platform_clip_id>.mp4` para incoming,
mismo nombre en `completed/` y `thumbnails/<nombre>.jpg`. Así el UNIQUE
(filepath) es suficiente: un reintento produce la misma ruta y colisiona
limpiamente en la DB en vez de acumular archivos.

**Atomicidad de archivos**: descarga, recorte y thumbnail escriben a
`<destino>.part` y hacen `os.Rename` al final. Si el proceso muere a mitad,
queda un `.part` huérfano (limpiado en el próximo intento) pero JAMÁS un
`finished.mp4` corrupto con nombre final que la DB crea válido.

### 3.7 Inyección de dependencias con interfaces

`internal/worker` define las capacidades como interfaces mínimas:

```
Discoverer   ListClips(channelID, after, maxPages) → []ClipInfo
Downloader   DownloadClip(clipID, destPath) error
Processor    ProcessVideo(src, dest) error
Thumbnailer  GenerateThumbnail(videoPath, thumbPath, atSec) error
Publisher    UploadVideo(videoPath, title, desc, tags) → (externalID, externalURL, err)
```

- El worker NO importa los adaptadores concretos (Twitch, ffmpeg, YouTube);
  importa solo `twitch.ClipInfo` y `youtube.RateLimitError` por razones de
  tipado. Es `cmd/clipfactory/main.go` quien construye los adaptadores y los
  conecta. Beneficios: tests con fakes (todos los tests del worker corren sin
  red ni ffmpeg), y agregar Kick/TikTok mañana es escribir un adaptador que
  satisfaga esas interfaces — cero cambios en el worker.
- **Degradación controlada**: si una capacidad es `nil`, los jobs de ese tipo
  fallan con `"no X configurado (falta X en WorkerConfig)"` y el resto del
  pipeline sigue. Permite operar el pipeline sin credenciales de YouTube, por
  ejemplo.
- **`ClipInfo` neutro**: la metadata de clip es un struct sin menciones a
  Twitch. Agregar otra plataforma = mapear su API a `ClipInfo`.
- **`osRemove` inyectable en `internal/db`**: `CleanupOldCompletedVideos`
  borra archivos vía la variable `osRemove`, reasignada en tests para no
  tocar disco. Un patrón pequeño pero que evita interfaces dummy.

### 3.8 Procesos externos: TwitchDownloaderCLI y ffmpeg

- **La API de Twitch no da descargas de clips.** La solución de la comunidad es
  `TwitchDownloaderCLI` (.NET). ClipFactory lo ejecuta como subprocess con
  `exec.CommandContext(ctx, ...)` — integrado al contexto, entonces un
  shutdown del worker cancela la descarga en curso.
- **ffmpeg igual**: subprocess con contexto. El filtro es
  `crop='min(iw,ih*9/16)':ih,scale=1080:1920,setsar=1` con
  `-c:v libx264 -preset veryfast -crf 23 -c:a copy -movflags +faststart`.
  - `crop min(...)` recorta al centro 9:16 (y tolera entradas ya verticales).
  - `veryfast` + `crf 23`: calidad visualmente transparente a coste de CPU
    viable en el i3-3220 (un clip de 60s tarda segundos, no minutos).
  - `-c:a copy`: el audio no se re-encodea (el crop no lo afecta) → más rápido.
  - `-movflags +faststart`: mueve el índice moov al inicio → YouTube puede
    empezar a procesar el video sin descargarlo entero.
  - `-f mp4`/`-f image2` explícitos: el `.part` no tiene extensión
    reconocible y ffmpeg no puede inferir el formato del nombre.
- **VAAPI queda como mejora futura**: en el i3-3220 no hay iGPU útil, así que
  el fallback software es el camino normal hoy.
- **stderr capturado y recortado**: los CLI imprimen barras de progreso
  enormes; en error se conservan los últimos 500-800 chars, que es donde está
  el mensaje real, y van al `error_message` de la fila correspondiente.
- **binario por PATH, ruta configurable**: `CLIPFACTORY_TWITCH_DOWNLOADER_PATH`
  y `CLIPFACTORY_FFMPEG_PATH`. En Docker ambos ya están instalados.

### 3.9 YouTube: HTTP directo sin SDK

`internal/adapter/youtube` implementa el upload a mano (2 requests HTTP):
resumable init (metadatos) → `Location` header (session URL) → PUT con bytes.

- **Por qué no `google.golang.org/api`** (el SDK oficial): arrastraría el
  árbol de dependencias de Google (decenas de módulos) y su transporte
  interno, por algo que con OAuth refresh + 2 requests queda en ~300 líneas.
  Mantiene el binario estático pequeño y el build trivial.
- **Flujo OAuth offline**: el REFRESH_TOKEN se genera UNA VEZ fuera del
  pipeline (consentimiento en navegador). En runtime, el adapter canjea el
  refresh por access tokens (~1h de vida), con cache thread-safe
  (`sync.Mutex`) y margen de 60s para no usar tokens al borde de expirar.
- **Errores de cuota como tipo**: `quotaExceeded`/`rateLimitExceeded` (HTTP
  403) se clasifican en `RateLimitError`. El worker lo detecta con
  `errors.As` y NO lo cuenta como intento fallido: pasa la publicación a
  `waiting_rate_limit` con `next_retry_at` a ~24h, porque la cuota diaria se
  resetea a medianoche PT y un reintento antes no puede funcionar.
- **Metadatos con límites duros**: título ≤100 chars y descripción ≤5000 se
  recortan defensivamente antes de llamar a la API. `selfDeclaredMadeForKids:
  false` explícito porque YouTube lo exige.
- **Video completo en memoria**: los clips son <60s a 1080x1920 CRF23
  (~5-15 MB) — acceptable. Streaming por chunks con reintentos quedaría para
  videos largos (roadmap).

### 3.10 Docker multi-stage y scripts de PowerShell

- **3 stages** (`deps` → `dev` → `prod`):
  - `deps`: solo `go mod download` — capa cacheable; si `go.mod` no cambia, no
    se re-descargan dependencias jamás.
  - `dev`: agrega ffmpeg, mediainfo, sqlite3, curl, jq, .NET runtime +
    TwitchDownloaderCLI. El código se monta por volumen (docker-compose) y el
    binario se compila a `/opt/clipfactory/bin` (fuera del volumen) para que
    editar código no requiera rebuild de imagen.
  - `prod`: lo mínimo para correr. (Hoy el deploy real copia el binario
    compilado a nico-server; el stage prod queda preparado para un futuro
    imagen de producción.)
- **PowerShell en `scripts/`**: el desarrollador trabaja en Windows; los
  scripts (`run-cli.ps1`, `test.ps1`, `build.ps1`...) envuelven los comandos
  docker con las rutas y flags correctos (`MSYS_NO_PATHCONV=1` en bash para
  evitar el mangling de rutas de Git Bash).

## 4. Máquinas de estado

Cada entidad tiene una columna `status` con transiciones documentadas. Son el
contrato operativo del pipeline: cualquier estado raro es visible y explicable.

```
source_clips:  detected ──► downloaded ──► (skipped | error)
videos:        incoming ──► processing ──► (completed | failed)
clips:         processing ──► (completed | failed)   [thumbnail es un adjunto, no cambia status]
publications:  pending ──► (published | error | waiting_rate_limit)
jobs:          queued ──► running ──► (done | error)
```

Notas finas:

- `source_clips.status` nunca retrocede: `UpsertSourceClip` no baja un clip
  `downloaded` a `detected` aunque el discovery lo re-liste (protege contra
  re-encolar descargas).
- `videos: processing` se setea ANTES de invocar ffmpeg: si crashea, el estado
  refleja la realidad (trabajo interrumpido) y el próximo run lo retoma.
- `publications: waiting_rate_limit` NO incrementa `attempts` — la cuota de
  YouTube agotada no es culpa del clip; el backoff exponencial queda reservado
  para fallos reales del pipeline.
- `jobs` en `error` NO se re-encolan automáticamente: el reintento de las
  publications lo hacen (1) el job futuro que `executePublish` encola con
  `created_at = next_retry_at` y (2) el `poll_publications` periódico como red
  de seguridad. Los demás jobs se re-encolan a mano insertando un job nuevo
  (idempotente por diseño).

## 5. Manejo de errores y reintentos

Principio: **fallar temprano en arranque, fallar suave en runtime**.

- Arranque: config inválida, DB inaccesible → el proceso sale con código != 0
  (no tiene sentido seguir).
- Runtime: un job que falla NO tira al worker. `executeJob` marca el job como
  `error` con el mensaje, recupera panics (un panic en un handler se convierte
  en job `error`, no en proceso muerto) y sigue con la cola.
- Backoff de publicaciones: `next_retry_at = now · 2^attempts` con cap de 24h
  (1h, 2h, 4h...). `GetPendingPublications` solo devuelve filas cuyo
  `next_retry_at` ya venció → los reintentos son naturales, sin timers. Además,
  `executePublish` re-encola el job con `created_at = next_retry_at`: el job
  no se ofrece hasta vencer el backoff (scheduling por tiempo de creación).
- Errores clasificados: cuota (`RateLimitError`, YouTube y Meta) → esperar
  reset diario (YouTube) / backoff (Meta); el resto → backoff exponencial.
- `error_message` en cada tabla guarda el detalle (con stderr recortado de los
  subprocess) para diagnosticar sin logs adicionales.

## 6. Seguridad y secretos

- `credentials/` está en `.gitignore`: nunca se commitean tokens.
- Los archivos de credenciales usan formato `CLAVE = valor` con comentarios;
  el parser tolera espacios y valores con `=`.
- El REFRESH_TOKEN de YouTube equivale a acceso de subida al canal: se trata
  como contraseña. El access token vive solo en memoria y expira en ~1h.
- Los subprocess se invocan con rutas configuradas por el operador (no input
  de usuarios externos); los flags `#nosec G204` documentan esa decisión.
- La única superficie de red saliente es a APIs conocidas (api.twitch.tv,
  googleapis.com) con `http.Client` con timeout — no hay servidor HTTP
  entrante que asegurar.

## 7. Rendimiento en hardware modesto

- `MaxConcurrentJobs=2` (1 ffmpeg + 1 descarga) saturan el i3-3220 sin swap.
- libx264 `veryfast`/CRF 23: recorte de un clip de 60s en segundos.
- SQLite WAL: cero contención entre el worker y consultas del operador.
- Upload en memoria (5-15 MB por clip): sin archivos temporales extra.
- El discovery pide 1 página (100 clips) por pasada: el rate limit de Helix
  (800 pts/min con token) permite monitorear decenas de canales sin acercarse
  al límite.

## 8. Limitaciones conocidas y roadmap

- **`sources.yaml` implementado** ✅: los canales se configuran en
  `config/sources.yaml` y se aplican a la tabla `sources` en cada arranque
  (upsert idempotente por `UpsertSource`). Los canales existentes en la DB que
  no están en el archivo no se tocan (el archivo da de alta, no excluye).
- **Auto-discovery al arrancar** ✅: el worker encola un job `discovery` por
  cada canal activo en su primer tick (`DiscoverOnStart`, default true;
  `CLIPFACTORY_DISCOVER_ON_START=false` para desactivar). UNA vez por proceso:
  si un discovery falla, el reintento lo maneja el operador (CLI `discovery`) o
  el próximo arranque, no hay spam automático de jobs en error.
- **Shutdown graceful** ✅: SIGINT/SIGTERM (Ctrl+C, `docker stop`) cancelan el
  contexto del worker: los jobs en curso abortan sus HTTP/ffmpeg (todos los
  adaptadores son context-aware) y se RE-ENCOLAN ('queued', sin error) para el
  próximo arranque en vez de marcarse fallidos. `docker stop` sale con code 0.
  Los jobs 'running' huérfanos (kill -9, corte de luz) se recuperan por el
  stale-lock de 30s, que ahora también aplica sobre jobs en 'running'
  (`GetPendingJobs` + `LockJob`). Una segunda señal fuerza la salida (exit 130).
- **GetChannelIDByName incompleto**: hace el request pero no parsea el JSON
  todavía; se usa el curl documentado en la Guía de Twitch §6.
- **`status` CLI implementado** ✅: lee la DB real y reporta versión de
  esquema, jobs por tipo/estado, videos/clips por estado, source_clips y
  publications por plataforma (backend: `db.GetJobStats` y compañía en
  `internal/db/stats.go`).
- **`discovery` CLI implementado** ✅: encola un job discovery por cada canal
  activo usando `EnsureActiveJob` (idempotente: no duplica si ya hay uno en
  vuelo) tras aplicar sources.yaml. El worker es quien ejecuta la pasada; de
  hecho, con el auto-discovery al arrancar (ver arriba) normalmente ni hace
  falta invocarlo.
- **Re-encolado de publications implementado** ✅: dos mecanismos
  complementarios: (1) `executePublish` programa el siguiente intento encolando
  un job `publish` con `created_at = next_retry_at` (el scheduler solo ofrece
  jobs con `created_at <= now`, así que el job "duerme" hasta vencer el backoff
  o el reset de cuota); (2) el job `poll_publications` (encolado cada
  `CLIPFACTORY_POLL_PUBLICATIONS_INTERVAL`, default 5m) re-encola publishes de
  publications pendientes que quedaron sin job futuro (p.ej. tras un crash).
- **Versionado de migraciones implementado** ✅: tabla `schema_migrations`
  (version, name, applied_at). Cada paso vive en `migrationSteps` con versión
  consecutiva; `MigrateDB` ejecuta solo las faltantes, cada una en una
  transacción. Las DBs creadas antes del versionado se adoptan: la v1 se
  registra como aplicada sin re-ejecutar el DDL (ver `adoptLegacySchema`).
- **Meta (Facebook) implementado** ✅: Reels de página vía Graph API
  (`internal/adapter/meta`), con upload multipart o `file_url` (si se configura
  `SetFilesBaseURL`) y clasificación de rate limit (códigos 4/17/32/613) para
  el mismo backoff del worker. Kick es plataforma de ORIGEN (como Twitch), no
  de publicación.
- **Título de video genérico**: `Clip <archivo>`; enriquecerlo con streamer/
  juego requiere guardar más campos de Helix en `source_clips`.
- **TikTok**: publicación pendiente (la arquitectura lo espera: es agregar un
  Publisher al mapa `Publishers` del worker).
- **`logs` table infrautilizada**: el esquema la define; hoy el logging real
  va a stdout/logs de archivos.
- **API de Kick no oficial**: los endpoints de clips de kick.com funcionan pero
  no están documentados ni soportados; pueden cambiar sin aviso (todos los
  endpoints del adapter son sobrescribibles para absorber cambios).
- **`GetChannelIDByName` incompleto**: hace el request pero no parsea el JSON
  todavía; se usa el curl documentado en la Guía de Twitch §6.
- **`status` CLI implementado** ✅: lee la DB real y reporta versión de
  esquema, jobs por tipo/estado, videos/clips por estado, source_clips y
  publications por plataforma (backend: `db.GetJobStats` y compañía en
  `internal/db/stats.go`).
- **`discovery` CLI implementado** ✅: encola un job discovery por cada canal
  activo usando `EnsureActiveJob` (idempotente: no duplica si ya hay uno en
  vuelo) tras aplicar sources.yaml. El worker es quien ejecuta la pasada; de
  hecho, con el auto-discovery al arrancar (ver arriba) normalmente ni hace
  falta invocarlo.

---

*Ver también: [Guía de Uso](guia-de-uso.md), [Guía de Twitch](guia-twitch.md),
[Guía de YouTube](guia-youtube.md) y el [README](../README.md).*
