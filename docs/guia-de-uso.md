# Guía de Uso de ClipFactory

Guía práctica paso a paso: desde cero (instalación y configuración) hasta
operar el pipeline en producción, diagnosticar problemas y mantener la base de
datos. Para el **por qué** de cada decisión técnica, ver
[Arquitectura y Decisiones](arquitectura-y-decisiones.md).

---

## Índice

1. [Requisitos](#1-requisitos)
2. [Primer arranque (desde cero)](#2-primer-arranque-desde-cero)
3. [Comandos del CLI](#3-comandos-del-cli)
4. [Configurar credenciales](#4-configurar-credenciales)
5. [Ejecutar el pipeline completo](#5-ejecutar-el-pipeline-completo)
6. [Operación diaria](#6-operación-diaria)
7. [Dar de alta canales a monitorear](#7-dar-de-alta-canales-a-monitorear)
8. [Consultas útiles de la DB](#8-consultas-útiles-de-la-db)
9. [Re-encolar y reparar jobs](#9-re-encolar-y-reparar-jobs)
10. [Solución de problemas](#10-solución-de-problemas)
11. [Tests y desarrollo](#11-tests-y-desarrollo)
12. [Deploy en el servidor (nico-server)](#12-deploy-en-el-servidor-nico-server)

---

## 1. Requisitos

**Para desarrollo (Windows, recomendado):**

- [Docker Desktop](https://www.docker.com/products/docker-desktop/) — todo el
  pipeline (Go, ffmpeg, TwitchDownloaderCLI) vive en el contenedor; no
  instalás nada más en tu máquina.
- PowerShell (viene con Windows).

**Para producción (Linux):**

- Servidor Linux x64 con ffmpeg instalado (`apt install ffmpeg`) y acceso a
  internet.
- El binario `clipfactory` compilado (ver §12) y `TwitchDownloaderCLI`
  ([releases de lay295/TwitchDownloader](https://github.com/lay295/TwitchDownloader/releases))
  en el PATH o referenciado por ruta.

## 2. Primer arranque (desde cero)

### Paso 1: clonar y levantar el contenedor de desarrollo

El compose define DOS servicios:

- **clipfactory-dev**: el playground de desarrollo (`sleep infinity`); entrás
  con `docker compose exec` a compilar y mirar la DB.
- **clipfactory**: el worker REAL, que compila el código montado, corre el
  binario como PID 1 y se mantiene con `restart: unless-stopped`. Tiene
  healthcheck (`clipfactory status` cada 30s) y `stop_grace_period: 90s`:
  `docker stop` le llega directo al worker, que apaga gracefully (jobs en
  curso re-encolados, DB cerrada limpia, exit 0) — ver §3.

Para trabajar solo con desarrollo:

```powershell
docker compose up -d clipfactory-dev
```

Para correr el pipeline (sin credenciales arranca igual, con avisos):

```powershell
docker compose up -d clipfactory
docker compose ps             # STATUS debe mostrar "Up ... (healthy)"
docker compose logs -f clipfactory
```

```powershell
cd clipfactory
docker compose up -d          # construye la imagen dev la primera vez (unos minutos)
docker compose ps             # debe mostrar clipfactory-dev "running"
```

La imagen `dev` ya incluye: Go 1.24, ffmpeg, mediainfo, sqlite3, curl, jq,
.NET 8 runtime y TwitchDownloaderCLI. El código fuente se monta por volumen:
editás en Windows y compilás dentro del contenedor.

### Paso 2: verificar el binario

```powershell
docker compose exec clipfactory-dev /opt/clipfactory/bin/clipfactory --help
# o reconstruir con tu código actual:
docker compose exec clipfactory-dev bash -c "cd /opt/clipfactory/app && CGO_ENABLED=0 go build -o /opt/clipfactory/bin/clipfactory ./cmd/clipfactory"
```

### Paso 3: primer arranque del worker (sin credenciales)

```powershell
docker compose exec clipfactory-dev /opt/clipfactory/bin/clipfactory worker
```

Debe arrancar con dos avisos esperados:

```
aviso: Twitch.ClientID vacío — los jobs de twitch fallarán hasta configurar credentials/twitch.conf
aviso: YouTube sin credenciales — los jobs 'publish' de youtube fallarán hasta configurar credentials/youtube.conf
aviso: Meta sin credenciales — los jobs 'publish' de meta fallarán hasta configurar credentials/meta.conf
worker corriendo... (presiona Ctrl+C para detener)
```

(Kick no genera aviso: no requiere credenciales.)

Eso es degradación controlada: el worker corre y la DB se crea con sus 8 tablas
(7 de negocio + `schema_migrations`, el registro de migraciones).
Detenelo con Ctrl+C y seguí configurando.

## 3. Comandos del CLI

```
clipfactory [command]

worker       arranca el worker: sondea la cola de jobs cada 5s, los ejecuta y
             al arrancar encola discovery para los canales activos
             (CLIPFACTORY_DISCOVER_ON_START=false para desactivarlo).
             SIGINT/SIGTERM (Ctrl+C, docker stop) apagan gracefully: se
             espera a los jobs en curso y se re-encolan para el próximo
             arranque; una segunda señal fuerza la salida inmediata.
             En compose, el servicio 'clipfactory' tiene stop_grace_period
             de 90s (más margen puntual: docker stop -t 300 clipfactory).
status       resumen del sistema leyendo la DB (jobs, videos, clips,
             publications por estado, versión de esquema)
discovery    encola un job de discovery por cada canal activo (idempotente:
             no duplica si ya hay uno en vuelo). Útil para re-disparar una
             pasada sin reiniciar el worker.
help         ayuda
```

El comando que importa es **worker**: hace todo el pipeline, incluida la
primera pasada de discovery (auto-discovery al arrancar). Los otros dos son
auxiliares: discovery re-dispara pasadas a mano y status muestra el estado.

## 4. Configurar credenciales

Los archivos viven en `credentials/` (nunca se commitean). Formato común
`CLAVE = valor`, `#` para comentarios.

### Twitch (obligatorio para discovery + download)

Creá `credentials/twitch.conf`:

```
CLIENT_ID = gp762nuuoqcoxypju8c569th9wzunuq
AUTH_TOKEN = 73d0f9mk0nxlt0ons4vbs0x0r259uc
```

Cómo obtenerlos paso a paso: **[Guía de Twitch](guia-twitch.md)** (crear app en
dev.twitch.tv, generar App Access Token, encontrar el channel ID).

### YouTube (obligatorio solo para publish)

Creá `credentials/youtube.conf`:

```
CLIENT_ID = 1234567890-abc.apps.googleusercontent.com
CLIENT_SECRET = GOCSPX-xxxxxxxx
REFRESH_TOKEN = 1//0gXXXXXXXX
PRIVACY_STATUS = unlisted   # recomendado al principio; public cuando confíes
CATEGORY_ID = 20            # 20 = Gaming
```

Cómo generar el REFRESH_TOKEN (una sola vez): **[Guía de YouTube](guia-youtube.md)**.

### Meta / Facebook (obligatorio solo para publish en Facebook)

Creá `credentials/meta.conf` para publicar los clips como **Reels de una página**:

```
PAGE_ID = 123456789012345          # ID de tu página de Facebook
ACCESS_TOKEN = EAAG...             # Page Access Token (no el token de usuario)
GRAPH_API_VERSION = v21.0          # opcional (default v21.0)
```

El Page Access Token se genera en developers.facebook.com (app → Messenger/
Graph API Explorer → extender permisos `pages_manage_posts` +
`pages_read_engagement` → canjear por token de página). El upload usa la Graph
API (`POST /{page-id}/videos` con `upload_type=reel`): sube el archivo directo
(multipart) o, si configurás una base pública de archivos en el adaptador, por
`file_url`.

### Verificar que los lee

```powershell
docker compose run --rm clipfactory-dev /opt/clipfactory/bin/clipfactory worker
# sin avisos = credenciales cargadas
```

## 5. Ejecutar el pipeline completo

### 5.1 Dar de alta el canal (una vez)

Dentro del contenedor (el `channel_id` es el ID numérico de Twitch — Guía de
Twitch §6):

```powershell
docker compose exec clipfactory-dev sqlite3 /opt/clipfactory/database/clipfactory.db "INSERT INTO sources (platform, channel_id, channel_name, active) VALUES ('twitch', '1337', 'auronplay', 1);"
```

### 5.2 Encolar la primera pasada de discovery

**Normalmente no hace falta hacer nada**: al arrancar, el worker encola solo un
job de discovery por cada canal activo (auto-discovery, una vez por proceso).
Con el canal dado de alta en sources.yaml (o en la tabla sources),

```powershell
docker compose up -d
docker compose logs -f clipfactory
```

muestra en el log `[worker] auto-discovery: 1 encolados, 0 ya en vuelo` y la
cadena del pipeline arranca sola.

Si querés disparar una pasada SIN reiniciar el worker (o con el auto-discovery
desactivado), usá el comando `discovery`, que encola una pasada por cada canal
activo (aplicando antes el sources.yaml):

```powershell
docker compose run --rm clipfactory-dev /opt/clipfactory/bin/clipfactory discovery
```

Salida esperada:

```
discovery encolado: twitch/1337 (source 1) → job 1

listo: 1 discovery encolados, 0 ya en vuelo. Arrancá el worker para procesarlos.
```

Si preferís hacerlo a mano (o querés re-descubrir UN canal puntual), el método
directo sigue siendo insertar el job por sqlite3:

```powershell
# averiguá el id de tu source (será 1 si es el primero):
docker compose exec clipfactory-dev sqlite3 /opt/clipfactory/database/clipfactory.db "SELECT id, channel_name FROM sources;"
docker compose exec clipfactory-dev sqlite3 /opt/clipfactory/database/clipfactory.db "INSERT INTO jobs (type, reference_id, reference_type, status) VALUES ('discovery', 1, 'sources', 'queued');"
```

### 5.3 Arrancar el worker y mirar la cadena

```powershell
docker compose exec clipfactory-dev /opt/clipfactory/bin/clipfactory worker
```

En el log verás la cadena completa ejecutarse sola:

```
[worker] locked job 1 (type=discovery, ...)
[worker] discovery completo para source 1 (...): N clips, M nuevos, M downloads encolados
[worker] locked job 2 (type=download, ref_type=source_clips, ref_id=1)
[worker] download completo: source_clip=1 video=1 → job process=3
[worker] locked job 3 (type=process, ref_type=videos, ref_id=1)
[worker] process completo: video=1 → clip=1 → job thumbnail=4
[worker] locked job 4 (type=thumbnail, ref_type=clips, ref_id=1)
[worker] thumbnail completo: clip=1 → /opt/clipfactory/data/thumbnails/xxx.mp4.jpg
```

Resultado: un clip vertical 1080x1920 en `data/completed/` y su JPEG en
`data/thumbnails/`.

### 5.4 Publicar en YouTube o Facebook

Creá la fila de `publications` para el clip; el **re-encolado automático** hace
el resto: cada 5 minutos (`CLIPFACTORY_POLL_PUBLICATIONS_INTERVAL`) el job
`poll_publications` encola un `publish` por cada publication pendiente, y si un
upload falla o se queda sin cuota, el propio worker programa el reintento solo
(backoff exponencial con cap 24h; cuota de YouTube → ~24h).

```bash
# publication para YouTube (platform='youtube'):
sqlite3 database/clipfactory.db "INSERT INTO publications (clip_id, platform, status) VALUES (1, 'youtube', 'pending');"
# publication para Facebook Reels (platform='meta'):
sqlite3 database/clipfactory.db "INSERT INTO publications (clip_id, platform, status) VALUES (1, 'meta', 'pending');"
# dentro de ≤5 min el worker lo toma solo. Para publicar YA, encolá el job a mano:
sqlite3 database/clipfactory.db "INSERT INTO jobs (type, reference_id, reference_type, status) VALUES ('publish', 1, 'publications', 'queued');"
#  (reference_id = publications.id, será 1 si es la primera)
```

Si la cuota diaria de YouTube se agota (≈6 uploads/día con el default), la
publicación entra en `waiting_rate_limit` y se re-encola sola al día siguiente.
Ver §8 para verificar.

## 6. Operación diaria

Rutina típica en el servidor:

1. **Worker corriendo** (systemd, tmux o screen): `./clipfactory worker`.
2. **Nueva pasada de discovery** (manual o cron):
   ```bash
   sqlite3 database/clipfactory.db "INSERT INTO jobs (type, reference_id, reference_type, status) VALUES ('discovery', 1, 'sources', 'queued');"
   ```
   El discovery guarda `last_checked_at`: cada pasada trae solo clips nuevos.
3. **Revisar estados** (§8): todo en `done`/`completed` y las publications
   `published` o `pending`.
4. **Publicar** los clips aprobados (§5.4).

Ejecutar el discovery cada 5-15 minutos es más que suficiente (los clips
nuevos de un canal caben holgado en una página de 100).

## 7. Dar de alta canales a monitorear

**Forma recomendada: `config/sources.yaml`** (copiá `sources.yaml.example`).
Se aplica a la tabla `sources` en cada arranque del worker (upsert idempotente):

```yaml
sources:
  - platform: twitch
    channel_id: "4919"          # broadcaster ID numérico (Guía de Twitch §6)
    channel_name: illojuan
    active: true
  - platform: kick              # Kick como ORIGEN de clips (igual que Twitch)
    channel_id: xokas           # slug del canal en kick.com
    channel_name: xokas (kick)
```

Para pausar un canal: `active: false` en el yaml (o `UPDATE sources SET active = 0
WHERE id = 2;` en la DB). Los canales que están en la DB pero no en el yaml no
se tocan: el archivo da de alta, no excluye.

**Alternativa: SQL directo** (sigue funcionando):

```bash
sqlite3 database/clipfactory.db "INSERT INTO sources (platform, channel_id, channel_name, active) VALUES ('twitch', '4919', 'illojuan', 1);"
```

Para forzar el re-encolado de discovery de un canal (por ejemplo tras darlo de
alta con el worker ya corriendo), encolá un job:

```bash
sqlite3 database/clipfactory.db "INSERT INTO jobs (type, reference_id, reference_type, status) VALUES ('discovery', 1, 'sources', 'queued');"
```

## 8. Consultas útiles de la DB

Todas con `sqlite3 database/clipfactory.db` (en Docker:
`docker compose exec clipfactory-dev sqlite3 /opt/clipfactory/database/clipfactory.db`).

**Estado general del pipeline:**

```sql
-- jobs por estado
SELECT type, status, COUNT(*) FROM jobs GROUP BY type, status;

-- últimos jobs con error (el porqué de cada fallo)
SELECT id, type, reference_type, reference_id, substr(error_message,1,80) AS err
FROM jobs WHERE status = 'error' ORDER BY updated_at DESC LIMIT 10;

-- clips listos para publicar
SELECT c.id, c.filepath, c.thumbnail_path IS NOT NULL AS tiene_thumb, v.status
FROM clips c JOIN videos v ON v.id = c.video_id
WHERE c.status = 'completed' ORDER BY c.created_at DESC LIMIT 20;
```

**Publicaciones (el flujo de YouTube):**

```sql
SELECT id, clip_id, status, attempts, next_retry_at, external_id,
       substr(error_message,1,60) AS err
FROM publications ORDER BY updated_at DESC LIMIT 20;
```

- `status='pending'` → esperando su job publish.
- `status='published'` → subido: `external_id`/`external_url` tienen el videoId.
- `status='error'` → falló; `next_retry_at` indica cuándo será reintentada.
- `status='waiting_rate_limit'` → cuota diaria agotada; reintento ~24h.

**Verificar un clip procesado:**

```bash
ffprobe -v error -select_streams v:0 -show_entries stream=width,height \
  -of csv=p=0:s=x data/completed/<clip>.mp4
# → 1080x1920
```

## 9. Re-encolar y reparar jobs

La cola es idempotente: insertar un job que ya se ejecutó es seguro (los
handlers detectan el estado avanzado y salen con no-op).

```sql
-- reintentar un download que falló (ej: Twitch caído a mitad)
INSERT INTO jobs (type, reference_id, reference_type, status)
VALUES ('download', <source_clip_id>, 'source_clips', 'queued');

-- reintentar el recorte de un video 'failed'
INSERT INTO jobs (type, reference_id, reference_type, status)
VALUES ('process', <video_id>, 'videos', 'queued');

-- regenerar el thumbnail de un clip
INSERT INTO jobs (type, reference_id, reference_type, status)
VALUES ('thumbnail', <clip_id>, 'clips', 'queued');

-- reintentar una publicación que quedó en error
INSERT INTO jobs (type, reference_id, reference_type, status)
VALUES ('publish', <publication_id>, 'publications', 'queued');
```

Notas:

- Las publications NO necesitan re-encolado manual: el worker solo reintenta
  (job futuro tras error/cuota + `poll_publications` cada 5m). El INSERT de
  publish de arriba es solo para adelantar el reintento.
- Un video en `processing` (worker muerto a mitad de ffmpeg): el próximo run
  retoma el trabajo solo (lock obsoleto tras 30s) o re-encolá `process`.
- Un `source_clip` en `error` re-encola download normalmente: el handler
  limpia el estado al tener éxito.
- Si un clip quedó publicado en YouTube pero la fila no refleja el
  `external_id` (crash crítico), corregí la fila a mano ANTES de re-encolar
  publish — el no-op de idempotencia lee `status='published' AND external_id`.

## 10. Solución de problemas

| Síntoma | Causa probable | Solución |
|---|---|---|
| `no discoverer configurado` | `twitch.conf` falta o vacío | Completar `credentials/twitch.conf` (§4) y reiniciar el worker |
| `no publisher configurado para la plataforma "youtube"` | `youtube.conf` falta o incompleto | Completar CLIENT_ID/SECRET/REFRESH_TOKEN (§4) |
| `api returned status 401` (Twitch) | Token vencido (~60 días) | Regenerar App Access Token (Guía de Twitch §4) |
| `api returned status 429` (Twitch) | Rate limit | Mandar `AUTH_TOKEN` (800 vs 30 pts/min) o bajar frecuencia de discovery |
| discovery no encuentra clips | `broadcaster_id` mal (usaste el nombre) | Usar el ID numérico (Guía de Twitch §6) |
| `twitch: downloader falló` | TwitchDownloaderCLI no está en PATH / Twitch caído | `CLIPFACTORY_TWITCH_DOWNLOADER_PATH` apuntando al binario; reintentar luego |
| `ffmpeg: proceso falló` en el log | ffmpeg no está en PATH o el mp4 de entrada está corrupto | `CLIPFACTORY_FFMPEG_PATH`; re-encolar `download` para re-bajar el clip |
| `youtube: auth: 401 invalid_grant` | Refresh token revocado o app en Testing >7 días | Regenerar token (Guía de YouTube §4) |
| `youtube: api devolvió 403: quotaExceeded` | Cuota diaria agotada | Nada: espera al reset (medianoche PT); queda `waiting_rate_limit` |
| `clip N: archivo no encontrado` (publish) | El mp4 de `completed/` se movió/borró | Verificar `data/completed/` y `clips.filepath`; re-procesar si falta |
| Jobs `queued` que nunca corren | Worker no está corriendo | `./clipfactory worker` (o revisar el proceso en el servidor) |
| Job en `running` "colgado" | Worker murió a mitad | Se auto-recupera tras 30s (stale lock); o re-encolar |
| `database is locked` | Otra herramienta mantiene la DB | Cerrar sqlite3/DB browser; WAL permite leer, no escribir en paralelo con otra escritura |

## 11. Tests y desarrollo

**Correr todos los tests** (en Docker, igual que producción):

```powershell
.\scripts\test.ps1
```

o manualmente:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/app" -w /app golang:1.24-bookworm go test -v ./...
```

**Con race detector** (recomendado al tocar concurrencia del worker):

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/app" -w /app golang:1.24-bookworm go test -race ./...
```

**Un solo paquete**, p. ej. durante un refactor de la DB:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W):/app" -w /app golang:1.24-bookworm go test -v ./internal/db/...
```

Notas para desarrolladores:

- Los tests usan DBs `:memory:` con `SetMaxOpenConns(1)`: con SQLite en memoria
  cada conexión nueva sería una DB distinta y perderías las tablas.
- `PRAGMA foreign_keys = ON` se ejecuta explícitamente en cada test (el DSN
  `_foreign_keys` no es confiable en `modernc.org/sqlite`).
- Los adaptadores se testean con `httptest.Server` (sin red real) y el worker
  con fakes (sin ffmpeg): ver [Arquitectura §3.7](arquitectura-y-decisiones.md#37-inyección-de-dependencias-con-interfaces).
- Tests reales de ffmpeg se saltan solos si ffmpeg no está en PATH.

**Compilar el binario:**

```powershell
.\scripts\build.ps1
# o manualmente:
docker compose exec clipfactory-dev bash -c "cd /opt/clipfactory/app && CGO_ENABLED=0 go build -o /opt/clipfactory/bin/clipfactory ./cmd/clipfactory"
```

## 12. Deploy en el servidor (nico-server)

Dos caminos posibles, elige UNO:

- **12.1 Docker (stage `prod`)** — recomendado: imagen autocontenida con
  ffmpeg, .NET y TwitchDownloaderCLI ya instalados dentro. Mismo entorno que
  testaste en desarrollo.
- **12.2 Binario + systemd** — sin Docker: compila el binario estático y lo
  corre systemd directamente.

En ambos casos, en el servidor se necesita:

```bash
# estructura de directorios (una vez)
ssh usuario@nico-server
sudo mkdir -p /opt/clipfactory/{config,credentials,database,logs,data/{incoming,processing,completed,failed,thumbnails}}

# credenciales (desde tu máquina Windows)
scp credentials/*.conf usuario@nico-server:/opt/clipfactory/credentials/
ssh usuario@nico-server "chmod 600 /opt/clipfactory/credentials/*.conf"

# sources.yaml (canales a monitorear)
scp config/sources.yaml usuario@nico-server:/opt/clipfactory/config/
```

### 12.1 Vía Docker (stage `prod` del Dockerfile)

#### a) Construir la imagen

Desde tu máquina Windows (o en el servidor, si clona el repo):

```bash
docker build --target prod -t clipfactory:prod .
```

La imagen incluye: binario compilado (CGO_ENABLED=0, `-trimpath -ldflags
"-s -w"`), ffmpeg, .NET 8 runtime + TwitchDownloaderCLI. El `ENTRYPOINT` es
directamente el CLI con `CMD ["worker"]`.

#### b) Transferir la imagen al servidor

Si nico-server no tiene acceso al registry/BuildKit:

```bash
docker save clipfactory:prod | gzip > clipfactory-prod.tar.gz
scp clipfactory-prod.tar.gz usuario@nico-server:/tmp/
ssh usuario@nico-server "docker load < /tmp/clipfactory-prod.tar.gz && rm /tmp/clipfactory-prod.tar.gz"
```

#### c) Correr el worker

```bash
ssh usuario@nico-server

docker run -d \
  --name clipfactory \
  --restart unless-stopped \
  --stop-timeout 90 \
  -v /opt/clipfactory/config:/opt/clipfactory/config \
  -v /opt/clipfactory/credentials:/opt/clipfactory/credentials:ro \
  -v /opt/clipfactory/data:/opt/clipfactory/data \
  -v /opt/clipfactory/database:/opt/clipfactory/database \
  -v /opt/clipfactory/logs:/opt/clipfactory/logs \
  clipfactory:prod
```

Detalles importantes:

- **`--stop-timeout 90`**: `docker stop` espera hasta 90s al apagado graceful
  (jobs en curso re-encolados, DB cerrada limpia, exit 0). Para más margen
  puntual: `docker stop -t 300 clipfactory`.
- **`:ro` en credentials**: el worker solo las lee. Si el volumen está
  read-only, un bug no puede sobrescribir secretos.
- **no-root**: el contenedor corre como el usuario `clipfactory` (uid
  10001, fijado en el Dockerfile). Los volúmenes del host deben ser
  escribibles por ese uid: `chown -R 10001:10001
  /opt/clipfactory/{data,database,logs}` una vez antes del primer
  `docker run` (config y credentials pueden quedarse root-owned: solo se
  leen). Si el contenedor arranca y muere con `attempt to write a readonly
  database`, el ownership de los volúmenes es lo primero que hay que mirar.
- **auto-discovery**: al arrancar encola discovery de los canales activos
  (`CLIPFACTORY_DISCOVER_ON_START=false` para desactivarlo, igual que en dev).
- **logs**: `docker logs -f clipfactory` (también quedan en
  `/opt/clipfactory/logs/`).

#### d) Healthcheck (opcional pero recomendado)

```bash
docker run -d \
  ... (igual que arriba) ... \
  --health-cmd "/opt/clipfactory/bin/clipfactory status" \
  --health-interval 30s \
  --health-timeout 15s \
  --health-retries 3 \
  --health-start-period 15s \
  clipfactory:prod
```

`docker ps` mostrará `(healthy)`/`(unhealthy)`: el check es una lectura
SQLite barata que no toca la cola.

#### e) Upgrade a una nueva versión

```bash
# en tu máquina Windows, con el código nuevo:
docker build --target prod -t clipfactory:prod .
docker save clipfactory:prod | gzip > clipfactory-prod.tar.gz
scp clipfactory-prod.tar.gz usuario@nico-server:/tmp/

# en el servidor:
docker load < /tmp/clipfactory-prod.tar.gz
docker stop clipfactory        # graceful: jobs en curso se re-encolan
docker rm clipfactory
docker run -d ... clipfactory:prod   # (mismas flags que c)
```

Al rearrancar, el worker aplica migraciones pendientes y re-encola los jobs
que quedaron en vuelo del apagado (y los huérfanos de un kill -9, por el
stale-lock de 30s).

#### f) Rollback

Conservá la imagen anterior con tag:

```bash
docker tag clipfactory:prod clipfactory:prod-2026-09-16   # antes del upgrade
docker run -d ... clipfactory:prod-2026-09-16             # si hay que volver
```

La DB es compatible hacia adelante (migraciones versionadas): si la versión
nueva migró el esquema, una versión vieja puede rechazarla — por eso el
rollback de código con DB migrada es "última opción"; primero intentá
arreglar hacia adelante.

### 12.2 Vía binario + systemd (sin Docker)

1. **Compilar en Linux** (en el contenedor o en el propio servidor):

   ```bash
   CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o clipfactory ./cmd/clipfactory
   ```

   El binario es estático: se copia tal cual.

2. **Copiar al servidor** y preparar estructura:

   ```bash
   scp clipfactory usuario@nico-server:/opt/clipfactory/bin/
   ssh usuario@nico-server
   mkdir -p /opt/clipfactory/{data/{incoming,processing,completed,failed,thumbnails},database,logs,credentials}
   ```

3. **Instalar TwitchDownloaderCLI** (ffmpeg con apt):

   ```bash
   wget https://github.com/lay295/TwitchDownloader/releases/latest/download/TwitchDownloaderCLI-linux-x64.zip
   unzip TwitchDownloaderCLI-linux-x64.zip -d tdl && chmod +x tdl/TwitchDownloaderCLI
   sudo mv tdl/TwitchDownloaderCLI /usr/local/bin/
   ```

4. **Copiar credenciales** (`twitch.conf`, `youtube.conf`, `meta.conf`) a
   `/opt/clipfactory/credentials/` con permisos restringidos:

   ```bash
   chmod 600 /opt/clipfactory/credentials/*.conf
   ```

5. **Correr el worker** con systemd (ejemplo de unit):

   ```ini
   # /etc/systemd/system/clipfactory.service
   [Unit]
   Description=ClipFactory worker
   After=network-online.target

   [Service]
   WorkingDirectory=/opt/clipfactory/app
   ExecStart=/opt/clipfactory/bin/clipfactory worker
   # SIGTERM → apagado graceful (jobs re-encolados); tras el timeout, SIGKILL
   TimeoutStopSec=90
   Restart=on-failure
   RestartSec=10

   [Install]
   WantedBy=multi-user.target
   ```

   ```bash
   sudo systemctl daemon-reload && sudo systemctl enable --now clipfactory
   journalctl -u clipfactory -f        # seguir el log
   ```

### Backups (ambas vías)

Copiar `database/clipfactory.db` (y el `-wal`/`-shm` mientras corre) con
periodicidad diaria. Los videos son regenerables; la DB no (tiene el historial
de publications y sus `external_id`). Si el worker corre en Docker, detenerlo
un momento antes del backup garantiza consistencia total; si no, el
`-wal` captura las últimas transacciones.

---

*Ver también: [Arquitectura y Decisiones](arquitectura-y-decisiones.md),
[Guía de Twitch](guia-twitch.md) y [Guía de YouTube](guia-youtube.md).*
