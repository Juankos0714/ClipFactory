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
aviso: Twitch.ClientID vacío — los jobs 'discovery' y 'download' fallarán hasta configurar credentials/twitch.conf
aviso: YouTube sin credenciales — los jobs 'publish' fallarán hasta configurar credentials/youtube.conf
worker corriendo... (presiona Ctrl+C para detener)
```

Eso es degradación controlada: el worker corre y la DB se crea con sus 7 tablas.
Detenelo con Ctrl+C y seguí configurando.

## 3. Comandos del CLI

```
clipfactory [command]

worker       arranca el worker: sondea la cola de jobs cada 5s y los ejecuta
status       resumen del sistema (MVP: sources configurados y WorkerID)
discovery    (stub, aún no implementado)
help         ayuda
```

El comando que importa es **worker**: hace todo el pipeline. Los otros dos son
auxiliares/informativos.

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

### 5.4 Publicar en YouTube

Hoy (MVP) el job `publish` no se encola automáticamente. Para publicar un clip
procesado, insertá la publication y su job:

```bash
# 1) crear la fila de publication para el clip (platform='youtube'):
sqlite3 database/clipfactory.db "INSERT INTO publications (clip_id, platform, status) VALUES (1, 'youtube', 'pending');"
# 2) encolar el job apuntando a esa fila:
sqlite3 database/clipfactory.db "INSERT INTO jobs (type, reference_id, reference_type, status) VALUES ('publish', 1, 'publications', 'queued');"
#  (reference_id = publications.id, será 1 si es la primera)
# 3) el worker en ejecución lo toma en el próximo poll (≤5s) y sube el video.
```

Si la cuota diaria de YouTube se agota (≈6 uploads/día con el default), la
publicación entra en `waiting_rate_limit` y se reintenta sola al día siguiente
(`GetPendingPublications`). Ver §8 para verificar.

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

```bash
# alta
sqlite3 database/clipfactory.db "INSERT INTO sources (platform, channel_id, channel_name, active) VALUES ('twitch', '4919', 'illojuan', 1);"

# pausar un canal sin borrarlo (el discovery lo salta)
sqlite3 database/clipfactory.db "UPDATE sources SET active = 0 WHERE id = 2;"

# reactivar
sqlite3 database/clipfactory.db "UPDATE sources SET active = 1 WHERE id = 2;"
```

El `channel_id` es el broadcaster ID numérico de Twitch (cómo conseguirlo:
[Guía de Twitch §6](guia-twitch.md#6-encontrar-el-channel-id-de-un-canal)).
Un `sources.yaml` para evitar tocar la DB está en el roadmap.

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
| `no publisher configurado` | `youtube.conf` falta o incompleto | Completar CLIENT_ID/SECRET/REFRESH_TOKEN (§4) |
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

1. **Compilar en Linux** (en el contenedor o en el propio servidor):

   ```bash
   CGO_ENABLED=0 go build -o clipfactory ./cmd/clipfactory
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

4. **Copiar credenciales** (`twitch.conf`, `youtube.conf`) a
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
   Restart=on-failure
   RestartSec=10

   [Install]
   WantedBy=multi-user.target
   ```

   ```bash
   sudo systemctl daemon-reload && sudo systemctl enable --now clipfactory
   journalctl -u clipfactory -f        # seguir el log
   ```

6. **Backups**: copiar `database/clipfactory.db` (y el `-wal`/`-shm`
   mientras corre) con periodicidad diaria. Los videos son regenerables; la
   DB no (tiene el historial de publications y sus `external_id`).

---

*Ver también: [Arquitectura y Decisiones](arquitectura-y-decisiones.md),
[Guía de Twitch](guia-twitch.md) y [Guía de YouTube](guia-youtube.md).*
