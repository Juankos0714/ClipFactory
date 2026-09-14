# Guía de Twitch para ClipFactory

Guía completa para obtener clips de canales de Twitch con ClipFactory: crear la
aplicación, conseguir credenciales, entender la API Helix, descargar los clips y
configurar los canales a monitorear.

---

## Índice

1. [Cómo funciona la integración](#1-cómo-funciona-la-integración)
2. [Crear la aplicación en Twitch Developer Console](#2-crear-la-aplicación-en-twitch-developer-console)
3. [Obtener el Client ID y Client Secret](#3-obtener-el-client-id-y-client-secret)
4. [Generar el App Access Token](#4-generar-el-app-access-token)
5. [Configurar ClipFactory](#5-configurar-clipfactory)
6. [Encontrar el Channel ID de un canal](#6-encontrar-el-channel-id-de-un-canal)
7. [La API de clips (Helix /clips)](#7-la-api-de-clips-helix-clips)
8. [Descargar los clips (TwitchDownloaderCLI)](#8-descargar-los-clips-twitchdownloadercli)
9. [Dar de alta un canal a monitorear](#9-dar-de-alta-un-canal-a-monitorear)
10. [Límites, cuotas y buenas prácticas](#10-límites-cuotas-y-buenas-prácticas)
11. [Solución de problemas](#11-solución-de-problemas)

---

## 1. Cómo funciona la integración

Twitch **no ofrece descargas oficiales de clips por API**. La integración tiene dos
piezas:

```
┌─────────────────────┐         ┌──────────────────────────┐
│  API Helix (oficial)│         │  TwitchDownloaderCLI     │
│                     │         │  (herramienta externa)   │
│  • Listar clips     │         │  • Descargar el video    │
│    por canal        │         │    del clip (mp4)        │
│  • Metadata: ID,    │         │                          │
│    título, duración,│         │                          │
│    fecha, thumbnail │         │                          │
└──────────┬──────────┘         └────────────┬─────────────┘
           │                                 │
           ▼                                 ▼
   discovery (job type)              download (job type)
   → tabla source_clips              → data/incoming/*.mp4
                                       → tabla videos
```

- **Descubrir** clips nuevos: API oficial Helix (`GET /helix/clips`), documentada y
  estable. La implementa `internal/adapter/twitch/twitch.go`.
- **Descargar** el video del clip: `TwitchDownloaderCLI`, la herramienta de la
  comunidad estándar para esto. La invocará el job `download` (aún no implementado).

## 2. Crear la aplicación en Twitch Developer Console

1. Iniciá sesión en <https://dev.twitch.tv/console> con tu cuenta de Twitch
   (no hace falta ser Partner/Affiliate; cualquier cuenta vale).
2. **Applications → Your Console → Register Your Application**.
3. Completá:
   - **Name**: `clipfactory` (el nombre debe ser único globalmente; si está tomado,
     usá `clipfactory-tunombre`).
   - **OAuth Redirect URLs**: `http://localhost:3000` (no lo vamos a usar, pero es
     obligatorio poner algo válido).
   - **Category**: `Chat Bot` o `Application Integration` (cualquiera sirve).
4. **Create**. Después entrá a la app → **Manage**.

## 3. Obtener el Client ID y Client Secret

En la página **Manage** de tu aplicación:

- **Client ID**: visible directamente. Ej: `gp762nuuoqcoxypju8c569th9wzunuq`.
- **Client Secret**: botón **New Secret** → se muestra **una sola vez**. Guardalo
  inmediatamente en tu gestor de contraseñas.

> ⚠️ **El Client Secret es sensible**: no lo commitees ni lo pegues en logs. Si se
> filtra, usá **New Secret** para invalidarlo.

Para ClipFactory necesitás el **Client ID** (obligatorio). El **Client Secret** solo
se usa para generar tokens (paso siguiente) y no se guarda en `twitch.conf`.

## 4. Generar el App Access Token

La API Helix acepta dos tipos de token. Para **listar clips públicos** alcanza el
**App Access Token** (flujo *client credentials*), que no requiere que ningún usuario
inicie sesión:

```bash
curl -X POST 'https://id.twitch.tv/oauth2/token' \
  -d 'client_id=TU_CLIENT_ID' \
  -d 'client_secret=TU_CLIENT_SECRET' \
  -d 'grant_type=client_credentials'
```

Respuesta:

```json
{
  "access_token": "73d0f9mk0nxlt0ons4vbs0x0r259uc",
  "expires_in": 5186162,     // ~60 días
  "token_type": "bearer"
}
```

**Notas importantes:**

- El token **expira** (`expires_in` segundos, ~60 días). Guardá la fecha de expiración
  y regeneralo antes de que venza (futuro: el worker lo renovará solo; hoy se pega
  manualmente en `twitch.conf`).
- Es un token de **aplicación**: puede listar clips de cualquier canal público, pero
  no accede a datos privados.
- Si el token se filtra, invalidalo con:
  `curl -X POST 'https://id.twitch.tv/oauth2/revoke' -d 'client_id=...' -d 'token=...'`

## 5. Configurar ClipFactory

Creá el archivo `credentials/twitch.conf` (formato clave=valor, `#` para comentarios):

```
# Credenciales de Twitch para ClipFactory
# Client ID de la app registrada en dev.twitch.tv/console
CLIENT_ID = gp762nuuoqcoxypju8c569th9wzunuq

# App Access Token del paso 4 (opcional para /helix/clips,
# pero recomendado: aumenta el rate limit)
AUTH_TOKEN = 73d0f9mk0nxlt0ons4vbs0x0r259uc
```

Verificá que ClipFactory lo lea correctamente:

```powershell
docker compose up -d
docker compose exec clipfactory-dev ls /opt/clipfactory/credentials/
docker compose run --rm clipfactory-dev /opt/clipfactory/bin/clipfactory status
```

El test `TestLoadTwitchConfig` en `config/config_test.go` documenta el formato exacto
que se parsea (soporta espacios alrededor del `=`, comentarios y líneas en blanco).

## 6. Encontrar el Channel ID de un canal

La API Helix no acepta nombres de canal para `/helix/clips`: necesita el
**broadcaster ID** numérico. Con tu Client ID:

```bash
curl -s 'https://api.twitch.tv/helix/users?login=auronplay' \
  -H 'Client-ID: TU_CLIENT_ID' | jq '.data[0].id'
```

Respuesta: `"1337"` (ejemplo). Ese número es el `channel_id` que va en la tabla
`sources`.

Alternativa sin curl: <https://www.streamweasels.com/tools/convert-twitch-username-to-user-id/>

El adaptador ya tiene el método `GetChannelIDByName(ctx, username)` para esto
(pendiente de parsear la respuesta; hoy podés usar el curl de arriba).

## 7. La API de clips (Helix /clips)

Endpoint: `GET https://api.twitch.tv/helix/clips`

**Headers obligatorios:**

```
Client-Id: TU_CLIENT_ID
Authorization: Bearer TU_TOKEN   (opcional para clips, recomendado)
```

**Parámetros útiles:**

| Parámetro | Descripción |
|-----------|-------------|
| `broadcaster_id` | ID del canal (obligatorio si no usás `game_id`/`id`) |
| `first` | Resultados por página, máx **100** |
| `after` | Cursor de paginación (viene en `_pagination.cursor`) |
| `started_at` / `ended_at` | Filtro por fecha de creación del clip (RFC3339). **Ojo**: la ventana máxima entre ambos es de **7 días** |

**Ejemplo** (clips de las últimas 24h de un canal):

```bash
SINCE=$(date -u -d '1 day ago' +%Y-%m-%dT%H:%M:%SZ)
curl -s -G 'https://api.twitch.tv/helix/clips' \
  -H 'Client-ID: TU_CLIENT_ID' \
  -H 'Authorization: Bearer TU_TOKEN' \
  --data-urlencode "broadcaster_id=1337" \
  --data-urlencode "first=100" \
  --data-urlencode "started_at=$SINCE" | jq .
```

**Respuesta (recortada):**

```json
{
  "data": [
    {
      "id": "AwkwardHelplessSalamanderSwiftRage",
      "url": "https://www.twitch.tv/auronplay/clip/AwkwardHelplessSalamanderSwiftRage",
      "embed_url": "https://clips.twitch.tv/embed?clip=AwkwardHelplessSalamanderSwiftRage",
      "broadcaster_id": "1337",
      "broadcaster_name": "auronplay",
      "creator_id": "99631238",
      "creator_name": "esl_csgo",
      "video_id": "460123456",
      "game_id": "32982",
      "language": "es",
      "title": "JUGANDO CON MI ABUELA",
      "view_count": 50321,
      "created_at": "2026-09-12T18:03:22Z",
      "thumbnail_url": "https://clips-media-assets2.twitch.tv/...-preview-480x272.jpg",
      "duration": 32.5
    }
  ],
  "pagination": { "cursor": "eyJiIjp7IkN1cnNvciI6..." }
}
```

**Mapeo a `ClipInfo`** (implementado en `internal/adapter/twitch/twitch.go` →
`helixClip.toClipInfo()`; la paginación con `pagination.cursor` es automática):

| JSON Helix | Campo ClipFactory | Tabla destino |
|------------|-------------------|---------------|
| `id` | `ID` → `platform_clip_id` | `source_clips` |
| `title` | `Title` | `source_clips` |
| `duration` | `DurationSec` (⚠️ en Helix es `duration`, no `duration_seconds`) | `source_clips.duration_seconds` |
| `created_at` | `CreatedAt` | `source_clips.created_at_platform` |
| `thumbnail_url` | `ThumbnailURL` | `source_clips` (futuro) |
| `broadcaster_id` | `ChannelID` | `sources` |
| `broadcaster_name` | `ChannelName` | `sources` |

**Paginación**: `ListClips` sigue `pagination.cursor` automáticamente hasta agotar
páginas (o hasta `maxPages` si se pasa; el discovery usa 1 página = 100 clips).

**Cómo se conecta con el job discovery** (`internal/worker/worker.go` →
`executeDiscovery`):

1. Lee el canal de `sources` (debe estar `active=1`).
2. Llama `ListClips(channelID, last_checked_at)`: clips nuevos desde la última
   revisión (sin filtro en la primera pasada).
3. Por cada clip: `UpsertSourceClip` (idempotente; **no regresa** estados avanzados:
   un clip ya `downloaded` queda como está).
4. Encola un job `download` SOLO por cada clip genuinamente nuevo.
5. Actualiza `sources.last_checked_at` (la próxima pasada parte de ahí).

**Probar el discovery manualmente** (con el canal ya dado de alta):

```bash
sqlite3 database/clipfactory.db "INSERT INTO jobs (type, reference_id, reference_type, status) \
  VALUES ('discovery', <SOURCE_ID>, 'sources', 'queued');"
./clipfactory worker   # tomará discovery → download en cadena
```

**Idempotencia**: la tabla `source_clips` tiene UNIQUE `(platform, platform_clip_id)`,
así que re-listar los mismos clips no duplica filas ni re-encola descargas.

## 8. Descargar los clips (TwitchDownloaderCLI)

[TwitchDownloaderCLI](https://github.com/lay295/TwitchDownloader) es una herramienta
.NET de la comunidad que puede descargar el video de un clip a mp4.

**Instalación en el servidor (Linux x64):**

```bash
# descargar el binario más reciente
wget https://github.com/lay295/TwitchDownloader/releases/latest/download/TwitchDownloaderCLI-linux-x64.zip
unzip TwitchDownloaderCLI-linux-x64.zip -d twitchdownloader
chmod +x twitchdownloader/TwitchDownloaderCLI
sudo mv twitchdownloader/TwitchDownloaderCLI /usr/local/bin/
TwitchDownloaderCLI --help
```

**En Docker** (agregar al Dockerfile en el stage `dev`/`prod`):

```dockerfile
RUN wget -q https://github.com/lay295/TwitchDownloader/releases/latest/download/TwitchDownloaderCLI-linux-x64.zip \
    && unzip TwitchDownloaderCLI-linux-x64.zip -d /tmp/tdl \
    && chmod +x /tmp/tdl/TwitchDownloaderCLI \
    && mv /tmp/tdl/TwitchDownloaderCLI /usr/local/bin/
```

**Descargar un clip** (ojo: el CLI 1.56+ prefiere verbos estilo git y el ID pelado;
la URL completa da "Unable to parse Clip ID/URL"):

```bash
TwitchDownloaderCLI clipdownload \
  -u AwkwardHelplessSalamanderSwiftRage \
  -o /opt/clipfactory/data/incoming/AwkwardHelplessSalamanderSwiftRage.mp4

# el modo viejo -m clipdownload sigue funcionando pero está deprecado:
# TwitchDownloaderCLI -m clipdownload -u <ID> -o <salida>
```

**Cómo lo llama ClipFactory** (job `download`, implementado en
`internal/worker/worker.go` → `executeDownload` + `internal/adapter/twitch/twitch.go`
→ `DownloadClip`):

1. Lee de `source_clips` el clip apuntado por el job (reference_id).
2. Si ya está `downloaded` o ya existe fila en `videos`: no-op idempotente.
3. Construye la URL: `https://www.twitch.tv/clip/<platform_clip_id>`.
4. Ejecuta `TwitchDownloaderCLI -m clipdownload -u <url> -o <dest>.part`
   (la ruta al binario es configurable: env var `CLIPFACTORY_TWITCH_DOWNLOADER_PATH`,
   default `TwitchDownloaderCLI` en PATH).
5. Verifica que el archivo exista y no esté vacío, y lo renombra a `<dest>`
   (rename atómico: nunca queda un mp4 corrupto con nombre final).
6. Inserta fila en `videos` con `status='incoming'` y marca
   `source_clips.status='downloaded'`.
7. Encola un job `process` para ffmpeg.

Si el CLI falla, `source_clips.status='error'` con el stderr del proceso
(últimos 500 chars) y el job queda `error` con el motivo.

**Probar manualmente el job download** (con un clip ya insertado en `source_clips`):

```bash
sqlite3 database/clipfactory.db "INSERT INTO jobs (type, reference_id, reference_type, status) \
  VALUES ('download', <SOURCE_CLIP_ID>, 'source_clips', 'queued');"
./clipfactory worker   # el worker lo tomará en el próximo poll
```

## 9. Dar de alta un canal a monitorear

Hoy (MVP) los canales se insertan directo en la DB:

```bash
sqlite3 database/clipfactory.db "
INSERT INTO sources (platform, channel_id, channel_name, active)
VALUES ('twitch', '1337', 'auronplay', 1);"
```

En el contenedor:

```powershell
docker compose exec clipfactory-dev sqlite3 /opt/clipfactory/database/clipfactory.db `
  "INSERT INTO sources (platform, channel_id, channel_name, active) VALUES ('twitch', '1337', 'auronplay', 1);"
```

El valor de `channel_id` es el broadcaster ID del paso 6. `active=1` significa que el
discovery lo consultará; ponelo en `0` para pausar un canal sin borrarlo.

> 📌 **Futuro**: archivo `config/sources.yaml` para declarar canales sin tocar la DB
> (ver `loadSources()` en `config/config.go`).

## 10. Límites, cuotas y buenas prácticas

- **Descarga atómica**: el CLI escribe a `<dest>.part` y ClipFactory renombra al final.
  Si el proceso muere a mitad, no queda un mp4 corrupto con nombre final (y el
  `.part` huérfano se limpia en el próximo intento).

- **Rate limit de Helix**: 800 puntos/minuto con App Access Token (30/min si no
  mandás token). Cada request a `/helix/clips` cuesta 1 punto. Con polling cada 5 min
  estás sobradísimo; el límite te da para monitorear decenas de canales.
- **Ventana de fechas**: `started_at`/`ended_at` acepta máximo **7 días** entre ambos.
  Para "clips desde la última revisión" conviene guardar `sources.last_checked_at` y
  pedir desde ahí (si fue hace más de 7 días, capar a 7).
- **Duración del clip**: los clips de Twitch duran entre 5 y 60 segundos. El recorte
  a 1080x1920 recorta los laterales: los clips con la acción en el centro quedan bien;
  para clips "wide" considerá un blur de fondo en el proceso ffmpeg.
- **Retención**: ClipFactory borra videos `completed` viejos con
  `CleanupOldCompletedVideos` (política configurable, ver `internal/db/models.go`).
  Los clips de Twitch quedan ~90 días online; descargarlos pronto evita links rotos.
- **ToS de Twitch**: automatizar descargas está en zona gris. Usalo con canales que te
  corresponden o con permiso, y no reintentes agresivamente.

## 11. Solución de problemas

| Síntoma | Causa probable | Solución |
|---------|----------------|----------|
| `twitch: ClientID es requerido` | `twitch.conf` no existe o no se parsea | Verificá `CLIPFACTORY_CREDENTIALS_DIR` y el formato `CLAVE=valor` |
| `api returned status 401` | Token vencido o inválido | Regenerá el App Access Token (paso 4) |
| `api returned status 403` | Falta scope o la app está en desarrollo sin usuarios permitidos | Para endpoints públicos no aplica; si pasa, revisá que el token sea de app y no de usuario |
| `api returned status 429` | Rate limit excedido | Bajá la frecuencia de discovery; verificá que mandás `Authorization` (800 vs 30 puntos/min) |
| `/helix/clips` devuelve `data: []` siempre | `broadcaster_id` equivocado (pusiste el nombre) | Conseguí el ID numérico (paso 6) |
| Descarga falla con TwitchDownloaderCLI | ffmpeg no está en PATH (lo necesita para remuxear) | En Docker ya está instalado; en bare metal: `apt install ffmpeg` |
| Los timestamps en SQL no comparan bien | Mezcla de formatos RFC3339 y `YYYY-MM-DD HH:MM:SS` | Guardá todo como RFC3339 UTC (ver README → Decisiones de diseño) |

---

## Checklist rápido

```bash
# 1. App en dev.twitch.tv → Client ID (+ Secret para el token)
# 2. Token de app:
curl -X POST 'https://id.twitch.tv/oauth2/token' \
  -d 'client_id=...' -d 'client_secret=...' -d 'grant_type=client_credentials'
# 3. credentials/twitch.conf con CLIENT_ID y AUTH_TOKEN
# 4. ID numérico del canal:
curl -s 'https://api.twitch.tv/helix/users?login=NOMBRE' -H 'Client-ID: ...' | jq '.data[0].id'
# 5. INSERT en sources
# 6. Probar listado manual:
curl -s -G 'https://api.twitch.tv/helix/clips' \
  -H 'Client-ID: ...' -H 'Authorization: Bearer ...' \
  --data-urlencode 'broadcaster_id=...' --data-urlencode 'first=5' | jq '.data[].title'
```

---

*Ver también: [Guía de YouTube](guia-youtube.md) para publicar los clips
procesados, y el README para la arquitectura general del pipeline.*
