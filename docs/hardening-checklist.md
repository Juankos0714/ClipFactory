# Hardening — checklist de cambios concretos

Segunda pasada del repositorio, archivo por archivo, que convierte la auditoría
en tareas accionables. Cada ítem: **archivo/zona → problema verificado en el
código actual → solución → prioridad**.

> Verificado el 2026-09-16 sobre `main` + work-in-progress local.
> Los ítems marcados ✅ ya fueron corregidos en esta sesión; los demás están
> pendientes. Prioridades: **P0** = riesgo de datos/secrets, **P1** = bug
> funcional, **P2** = producción, **P3** = arquitectura, **P4** = tests.

---

## Fase 1 — Críticos (P0)

### 1. `.gitignore` no existe ✅ IMPLEMENTADO

- **Estado verificado**: no hay `.gitignore` en la raíz. El código y la docs
  afirman que `credentials/` está ignorado, pero no hay mecanismo real.
- **Trackeado hoy**: `database/clipfactory.db` (127KB de estado operativo).
  `credentials/` no tiene archivos commiteados (bien, pero por suerte).
- **Implementado** (2026-09-16):
  - `.gitignore` en la raíz: credentials/ (con excepción `*.conf.example`),
    `database/*.db|db-shm|db-wal`, data/, logs/, tmp/, coverage, OS junk.
  - `git rm --cached database/clipfactory.db` + `database/.gitkeep` para
    mantener la estructura (la DB local del repo quedó ignorada, no borrada
    del disco).
  - Ejemplos de credenciales creados: `credentials/{twitch,youtube,meta}.conf
    .example` (onboarding sin secretos; youtube y meta añadidos aquí, no
    existían).
  - Historial revisado: `git log --all -- credentials/` está vacío — nunca
    hubo secretos commiteados, no hace falta rotar nada.
- **Prioridad**: P0 → hecha.

### 2. Docker prod: usuario root, credenciales rw, compose compila al arrancar — ✅ IMPLEMENTADO

- **Estado verificado** (`Dockerfile`, `docker-compose.yml`):
  - ✅ Multi-stage y compilar-en-build: el stage `prod` quedó reparado en esta
    sesión (`COPY . .` + build con `-trimpath -ldflags "-s -w"`), el binario se
    compila en `docker build` y el `ENTRYPOINT` es el CLI (worker = PID 1,
    SIGTERM directo). El compose de **dev** sigue compilando al arrancar, que
    es lo correcto para desarrollo.
  - ✅ `USER clipfactory` (uid fijo 10001) en el stage prod + chown de
    /opt/clipfactory. Nota de ownership de volúmenes en guia-de-uso §12.1
    (chown 10001 en data/database/logs del host).
  - ✅ `:ro` en credentials: ya documentado en guia-de-uso §12.1.
  - ✅ `-buildvcs=false` en el build del stage prod: COPY trae el .git del
    host y el stamping VCS de go falla dentro del contenedor (exit 128);
    el versionado real vive en el tag de imagen (sha-<sha> en CI).
- **Prioridad**: P0 → hecha. (Verificado: imagen corre como uid 10001, worker
  arranca, DB escribible en volumen, status OK, docker stop → exit 0.)

### 3. Streaming en publishers: el video completo va a RAM

- **Estado verificado**:
  - `internal/adapter/meta/meta.go:176` — `os.ReadFile(videoPath)` + build del
    multipart en un `bytes.Buffer`: **hasta 2-3× el tamaño del video en RAM**.
  - `internal/adapter/youtube/youtube.go:203` — `os.ReadFile(videoPath)`, con
    comentario que lo asume aceptable ("clips <60s ≈ 5-15MB"). Hoy cierto,
    pero frágil si cambia el preset de ffmpeg o la duración.
- **Solución**: `os.Open()` + `io.Reader` directo al body del request:

  - Meta multipart: reemplazar `mw.CreateFormFile` + `fw.Write(data)` por un
    multipart que streamee el file (usar `multipart.Writer.CreateFormFile` con
    un pipe, o armar el body con `io.Pipe`: writer en goroutine leyendo el
    archivo, reader como `RequestBody`).
  - YouTube resumable: ya usa sesión resumible (init + PUT); pasar el
    `*os.File` como body del PUT en vez del `[]byte`.
  - Guarda de tamaño: si `os.Stat(videoPath).Size()` > umbral configurable
    (ej. 100MB), rechazar y marcar el clip `error` — no cargarlo.
- **Prioridad**: P0 en Meta, P1 en YouTube (comportamiento actual correcto
  pero sin cinturón).

### 4. Config del worker: env vars ignoradas por `DefaultWorkerConfig` ✅ IMPLEMENTADO

- **Estado verificado** (`cmd/clipfactory/main.go`): `runWorker()` hace
  `wcfg := worker.DefaultWorkerConfig(cfg.DBPath)` y **nunca** copia
  `cfg.MaxConcurrentJobs`, `cfg.PollInterval` ni asigna `WorkerID` desde env.
  `CLIPFACTORY_MAX_CONCURRENT_JOBS` y `CLIPFACTORY_POLL_INTERVAL` están
  documentadas y parseadas en config, pero **no tienen efecto**: el worker
  siempre corre con 2 jobs / 5s.
- **Implementado** (2026-09-16): `runWorker()` propaga `wcfg.MaxConcurrentJobs
  = cfg.MaxConcurrentJobs`, `wcfg.PollInterval = cfg.PollInterval` y
  `wcfg.WorkerID = cfg.WorkerID`. Nuevo `cfg.WorkerID` con default
  `os.Hostname()` (`defaultWorkerID()`, fallback `worker-local`) y override
  `CLIPFACTORY_WORKER_ID` — identidad única por contenedor en `jobs.locked_by`.
  README: fila nueva de env var en la tabla.
- **Verificado en vivo**: contenedor prod arranca con
  `starting worker <hostname> (max concurrent jobs: 12, poll interval: 5s)`
  (antes: `worker-main` fijo, 2 jobs fijo).
- **Prioridad**: P1 → hecha.
- **NOTA aparte**: el default de `CLIPFACTORY_MAX_CONCURRENT_JOBS` es NumCPU
  (12 en nico-server de 8 vías... el hardware objetivo i3-3220 tiene 4 hilos
  y el diseño objetivo era 2 concurrentes: 1 ffmpeg + 1 descarga). Si
  operarás con el default tal cual, fijá `CLIPFACTORY_MAX_CONCURRENT_JOBS=2`
  en el .env del server o vigilar el uso de CPU.

---

## Fase 2 — Fiabilidad (P1)

### 5. Reintentos sin techo ni clasificación ✅ IMPLEMENTADO

- **Estado verificado** (`internal/worker/worker.go executePublish`): el error
  genérico usa backoff `2^attempts` horas cap 24h, **sin `MAX_ATTEMPTS`** —
  un clip cuyo upload falla para siempre reintenta cada 24h eternamente y
  ocupa la cola. No existe distinción retryable/permanent en jobs.
- **Implementado** (2026-09-16):
  - `internal/adapter/retry.go`: `MaxPublishAttempts = 10`, tipo
    `PermanentError`, `ClassifyTokenError` (invalid_grant/invalid_client/
    unauthorized_client → permanentes) e `IsPermanent` (errors.As sobre la
    cadena). Lo no clasificado queda TRANSITORIO (conservador; el techo acota).
  - Publishers: 401/403 → `PermanentError` en youtube y meta; el error del
    token endpoint de YouTube clasifica vía `ClassifyTokenError`.
  - Worker: permanente → publication `failed` en el 1er intento SIN
    re-encolar (dead-letter, visible en `status`); transitorio → backoff
    2^attempts cap 24h hasta `MaxPublishAttempts`, luego `failed`.
  - Tests: `retry_test.go` (adapter), `publish_retry_test.go` (worker),
    `TestAPIErrorPermanent` (meta), `TestUploadPermanentUnauthorized`+
    ampliación de `TestTokenEndpointError` (youtube).
  - NOTA: `failed` aún no está en CHECK constraints (no existen) y
    `GetPendingPublications` ya lo excluye al pedir `status IN ('pending',
    'error', 'waiting_rate_limit')`.
- **Prioridad**: P1 → hecha.

### 6. Twitch no maneja 429/Retry-After ✅ IMPLEMENTADO

- **Estado verificado** (`internal/adapter/twitch/twitch.go`): no hay mención
  a 429 ni `RateLimitError`. Un rate limit de Helix durante el discovery
  falla el job como error genérico (y hoy, con backoff de publication, que
  ni siquiera aplica a discovery).
- **Implementado** (2026-09-16):
  - `internal/adapter/twitch/ratelimit.go`: `RateLimitError{RetryAfter}`;
    429 → `RateLimitError` con header `Retry-After` parseado (segundos o
    fecha HTTP) o default 60s (`DefaultTwitchRetryAfter` — Helix no envía el
    header hoy). `errors.As` atraviesa el wrap `"page N:"` de la paginación.
  - Worker `executeDiscovery`: 429 → re-encola el discovery vía
    `requeueDiscovery` (excluye el job propio en running; no duplica si ya
    hay otro activo) con `created_at = now + RetryAfter` y termina 'done'.
    `last_checked_at` NO avanza (la pasada no vio clips).
  - Tests: `ratelimit_test.go` (adapter), `discovery_ratelimit_test.go`
    (worker: requeue con created_at futuro, job done, no-duplicación, ciclo
    completo cuando vence el plazo, y que otros errores sigan fallando).
- **Prioridad**: P1 → hecha.

### 7. Duplicación de publicaciones externas (el riesgo grande) ✅ IMPLEMENTADO

- **Estado verificado**: la ventana es real — `executePublish` sube, y el
  `UpdatePublicationStatus` ocurre después. Crash entre ambos = publication
  sigue `pending` → el poll la re-encola → **segundo upload a YouTube/Meta**.
- **Implementado** (2026-09-16): MARKER DETERMINISTA + RECONCILIACIÓN.
  - `db.PublicationKeysForClip(clipID, videoID)` → markers `cf-<clip>-<video>`
    y `cf-<clip>`, escritos en la descripción/detalle de cada video publicado
    (`executePublish` los agrega a la description). Determinista: no depende
    del reloj; el reintento encuentra lo que subió el intento anterior.
  - `worker.Reconciler` (interfaz opcional, nil-safe): `FindRecentByMarker
    (ctx, markers)`. YouTube: channels.list (playlist de uploads) →
    playlistItems.list (últimos 50, descripciones) — llamadas de LECTURA que
    no consumen cuota de upload. Meta: GET /{page}/videos?fields=id,
    description&limit=50 (una llamada).
  - `executePublish`: antes de reintentar un upload (solo si
    `pub.Attempts > 0 || pub.NextRetryAt != nil` — un publish fresco no puede
    ser duplicado y se ahorra la llamada), consulta al reconciliador; si hay
    match → registra external_id/URL y NO sube. Si la API de reconciliación
    falla, sigue con el upload (fallar abierto: el techo de intentos acota).
  - Ventana residual: el poll re-encola y hay maxResults=50 de historia —
    suficiente para el cap de backoff de 24h.
  - Tests: reconcile_test.go (youtube: hit/miss/sin-markers/error de API y
    flujo de 2 llamadas), reconcile_test.go (meta: hit/miss/sin-markers/
    error), reconcile_test.go (worker: crash-simulado → reconciliado sin
    re-sub, miss → sube, fresco → ni consulta, marker presente en la
    descripción enviada al publisher).
- **Prioridad**: P1 → hecha.

### 8. Auto-discovery no reintenta (decisión documentada, ventana operativa)

- **Estado verificado**: `maybeDiscoverOnStart` corre una vez por proceso
  (deliberado, anti-spam). Si la API está caída en ese momento, no hay
  discovery hasta reinicio o CLI manual.
- **Solución**: reintento con backoff + jitter SOLO mientras el fallo sea
  transitorio: `nextAttempt = 1m, 2m, 5m, 10m, 30m` (cap 30m, jitter ±20%),
  reiniciado la serie tras un éxito. Implementarlo con un `discoveryBackoff`
  en el worker y reuso de `EnsureActiveJob` (ya idempotente). El fallo
  permanente (canal inexistente) sigue sin reintentar automático.
- **Prioridad**: P2 (la ventana existe pero el CLI `discovery` ya la cubre
  manualmente).

---

## Fase 3 — Producción (P2)

### 9. CI/CD inexistente ✅ IMPLEMENTADO

- **Estado verificado**: no hay `.github/`.
- **Implementado** (2026-09-16), 3 workflows:
  - `.github/workflows/ci.yml` — en push a main y PRs: `go vet`, `gofmt -l`
    (falla si hay archivos sin formatear), `go build`, `go test` y
    **`go test -race`** (crítico: goroutines + atomic + SQLite compartida;
    verificado local: 9/9 paquetes limpios con -race). Además un job
    `docker build --target prod` en CADA PR/push: valida que el stage prod
    compile (ya estuvo roto sin que nadie lo ejecutara). Cache GHA.
  - `.github/workflows/docker.yml` — en push a main: build+push del stage
    prod a **GHCR** con tags `sha-<sha>` (rollback exacto) y `latest`
    (metadata-action baja el nombre a minúsculas; usa `GITHUB_TOKEN`).
  - `.github/workflows/security.yml` — semanal (lunes 06:00 UTC) y manual:
    `gosec` (sin fallar, SARIF al tab Security para triage) y
    `govulncheck` (SÍ falla: CVE alcanzable en dependencias es accionable).
- **Prioridad**: P2 → hecha. (El push inicial de estos archivos será su
  primera ejecución real: revisar el tab Actions tras el push.)

### 10. Observabilidad: solo stdout + tabla logs sin usar

- **Estado verificado**: no hay métricas; la tabla `logs` existe pero el
  worker no escribe en ella (`InsertLog` sin llamadores en runtime).
- **Solución incremental**:
  1. Exponer `/metrics` (Prometheus) en un listener opcional
     (`CLIPFACTORY_METRICS_ADDR=:9090`, desactivado por default). Métricas
     mínimas: `clipfactory_jobs_total{type,status}`,
     `clipfactory_job_duration_seconds{type}` (histograma),
     `clipfactory_queue_depth{type}`,
     `clipfactory_publications_total{platform,status}`.
  2. Hacer que los handlers escriban eventos clave en la tabla `logs` (ya
     tiene índices y FKs lógicas).
- **Prioridad**: P2.

### 11. Retención de video: función existe, nadie la llama

- **Estado verificado**: `db.CleanupOldCompletedVideos(olderThan)` está
  implementada y testeada, pero **no tiene llamador** en `runWorker` — el
  disco crece indefinidamente. Además no cubre `incoming/` ni `failed/`.
- **Solución**:
  - Llamarla desde el loop con intervalo propio
    (`CLIPFACTORY_RETENTION=720h`, default 30 días).
  - Extender a `videos.status='failed'` y a `source_clips` con error.
  - Umbral de disco: antes de encolar download, chequear espacio libre
    (`syscall.Statfs` en Linux); `<10%` → no encolar nuevas descargas (log
    + reintentar después); `<5%` → además no procesar.
- **Prioridad**: P2 (en un pipeline de video es cuando-no-si).

### 12. Healthcheck: liveness vs readiness

- **Estado verificado**: el healthcheck (`status`) chequea proceso+DB, que es
  liveness+readiness mezclados pero razonables para el MVP. Falta validar
  dependencias externas.
- **Solución**: subcomando `clipfactory doctor`: DB accesible+esquema
  actual, ffmpeg en PATH (ejecutar `-version`), TwitchDownloaderCLI en PATH
  (si hay canales twitch), credenciales presentes y parseables. El
  healthcheck del contenedor puede seguir siendo `status` (barato), y
  `doctor` queda para el operador y para CI.
- **Prioridad**: P3.

---

## Fase 4 — Arquitectura (P3)

### 13. `main.go` acumula demasiado (473 líneas)

- **Estado verificado**: config+DI+señales+status+discovery+syncSources en un
  archivo. Funciona, pero cada feature lo agranda.
- **Solución**: extraer a `internal/app`: `app.RunWorker(cfg)`,
  `app.RunStatus(cfg)`, `app.RunDiscovery(cfg)`; `main.go` queda con dispatch
  puro. Los helpers (`sortedKeys`, `formatCounts`, `syncSources`) viajan con
  su paquete.
- **Prioridad**: P3 (no urgente; hacerlo junto con la Fase 4 de la auditoría).

### 14. Campos legacy `Discoverer/Downloader/Publisher` (singulares)

- **Estado verificado**: los tres existen solo por compatibilidad con tests
  viejos; `NewWorker` los mapea a "twitch"/"youtube". Deuda conocida.
- **Solución**: migrar los tests que usan el campo singular a los mapas
  (`w.downloaders = map[string]Downloader{...}` ya es el patrón en los tests
  nuevos) y borrar los campos singulares de `WorkerConfig`+`Worker` en una
  sola pasada (es mecánico y el compilador guía).
- **Prioridad**: P3.

### 15. Parser YAML manual → `yaml.v3`

- **Estado verificado**: `loadSources` ya creció a manejar comentarios inline,
  comillas, `active` con defaults, validaciones — señales clásicas de que el
  subconjunto manual llegó a su límite. Acaba de arreglarse un bug acá
  (comentarios inline).
- **Solución**: migrar `sources.yaml` a `gopkg.in/yaml.v3` con structs
  tipados (`yaml:"channel_id"` etc.); conservar los tests existentes (todos
  deberían seguir pasando salvo los de mensajes de error exactos, que se
  ajustan). Los `.conf` key=value **quedan como están**: ese formato simple
  no justifica la dependencia.
- **Prioridad**: P3.

### 16. Timestamps: defaults `datetime('now')` vs RFC3339

- **Estado verificado**: el DDL tiene `DEFAULT (datetime('now'))` pero todo
  el código escribe con `NowUTC()` (RFC3339). El default solo aplicaría en
  INSERTs que omitan la columna — hoy ningún insert lo hace. Riesgo bajo
  pero latente (un INSERT manual de operador crea formato mixto;
  `parseTimeOrNull` ya tolera ambos al leer).
- **Solución**: en la próxima migración (v2), recrear tablas con
  `DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))` o sin default + NOT NULL
  con escritura explícita. Bajo urgencia: solo documentar la regla
  "nunca confiar en el default".
- **Prioridad**: P3.

### 17. CHECK constraints para estados

- **Estado verificado**: 0 constraints CHECK en el esquema. `status` de
  jobs/videos/clips/publications acepta cualquier string (los tests mismos
  insertan 'raro' para probar).
- **Solución**: migración v2 con recreate-table añadiendo CHECKs:
  `jobs.status IN ('queued','running','done','error')`,
  `videos.status IN ('incoming','processing','completed','failed')`,
  `clips.status IN ('processing','completed','failed')`,
  `publications.status IN ('pending','published','error','waiting_rate_limit','failed')`,
  `sources.platform IN ('twitch','kick')` (o lista abierta si se quiere
  extensibilidad).
  Ojo con el dato 'raro' de los tests: ajustar fixture.
- **Prioridad**: P3.

### 18. Asociación polimórfica `jobs.reference_id/reference_type`

- **Estado verificado**: sin FK posible; un job puede apuntar a una fila que
  no existe (los handlers ya fallan con mensaje claro, pero el job queda en
  error).
- **Solución pragmática (sin cambiar esquema)**: validación al encolar —
  `EnqueueJob` valida con un SELECT a la tabla destino según
  `reference_type` (los handlers ya lo hacen; traerlo al enqueue evita jobs
  muertos). Separar en 4 tablas NO vale la complejidad para single-node.
- **Prioridad**: P3.

---

## Fase 5 — Tests de fallos (P4)

### 19. Tests ya existentes que cubren parte de la auditoría ✅

La auditoría pedía "integration test del pipeline completo" y "crash
recovery": ya existen tras esta sesión:

- `internal/worker/system_test.go`: E2E discovery→download→process→thumbnail
  →publish con ffmpeg real y verificación con ffprobe.
- `internal/worker/shutdown_test.go`: ctx cancelado → requeue; Stop espera al
  job en curso; Close y ownership de DB.
- `internal/db/requeue_test.go`: RequeueJob con guard; stale-running
  recuperable por GetPendingJobs/LockJob.

### 20. Tests que faltan (escenarios de la auditoría)

- **429 de Twitch** → discovery re-encolado con Retry-After (requiere el fix
  #6).
- **HTTP 500/timeout de upload** → publication `error` con backoff, job done
  (parcialmente cubierto en poll_test.go con fakePublisher; agregar timeout).
- **ffmpeg fallando** → video `failed`, clip no creado, job error (existe un
  caso en process_test.go; verificar cobertura del mensaje en DB).
- **Publicación externa exitosa + crash antes del UPDATE** → simular: publisher
  fake que sube y luego fuerza un error de DB; verificar que el reintento no
  re-publica (requiere fix #7; hasta entonces documentar el riesgo).
- **Credenciales inválidas** → 401 de cada API → permanent, `failed` (fix #5).
- **Disco lleno** → skip de descargas (fix #11, umbral).
- **`go test -race ./...` en CI** (fix #9) — correrlo localmente ya: hay
  goroutines y atomics en el core; si pasa limpio, es una garantía real.
- **Prioridad**: P4, después de los fixes correspondientes.

---

## Orden de ejecución sugerido

```text
1. ✅ .gitignore + git rm --cached database/clipfactory.db + *.conf.example
2. ✅ wcfg.MaxConcurrentJobs/PollInterval/WorkerID desde config
3. ✅ fmt.Println("args:", os.Args) fuera de main()
4. ✅ USER no-root (uid 10001) en Dockerfile prod + nota de chown en docs
5. Meta streaming (os.Open + io.Pipe) + guard de tamaño                     (P0, 2-3 h)
6. ✅ MAX_ATTEMPTS + clasificación retryable/permanent
7. ✅ Twitch 429/Retry-After → requeue discovery
8. ✅ CI: ci.yml (vet/test/race/build) + docker.yml + security.yml
9. Retención de disco (llamar Cleanup + umbral statfs)                      (P2, 3 h)
10. Métricas /metrics + logs table                                          (P2, 4 h)
11. ✅ Reconciliación de publicaciones duplicadas (marker + FindRecentByMarker)
12. Refactors Fase 4 (app pkg, yaml.v3, CHECK constraints, legacy fields)   (P3, 2-3 días)
13. Tests de fallos de la Fase 5                                            (P4, continuo)
```

Los ítems 1-4 son media mañana y eliminan los riesgos P0/P1 de repositorio y
config. El 11 es el más costoso y el que más valor da antes de habilitar más
plataformas.
