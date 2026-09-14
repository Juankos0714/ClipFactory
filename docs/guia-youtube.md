# Guía de YouTube para ClipFactory

Guía completa para publicar los clips procesados en YouTube con ClipFactory:
crear el proyecto en Google Cloud, activar la YouTube Data API v3, generar el
`REFRESH_TOKEN` una sola vez, configurar `youtube.conf` y entender cómo funciona
el job `publish` (upload resumable, cuotas y reintentos).

---

## Índice

1. [Cómo funciona la integración](#1-cómo-funciona-la-integración)
2. [Crear el proyecto en Google Cloud y activar la API](#2-crear-el-proyecto-en-google-cloud-y-activar-la-api)
3. [Obtener Client ID y Client Secret](#3-obtener-client-id-y-client-secret)
4. [Generar el REFRESH_TOKEN (una sola vez)](#4-generar-el-refresh_token-una-sola-vez)
5. [Configurar credentials/youtube.conf](#5-configurar-credentialsyoutubeconf)
6. [Categorías de YouTube](#6-categorías-de-youtube)
7. [Cómo funciona el job publish](#7-cómo-funciona-el-job-publish)
8. [Cuotas y límites de la API](#8-cuotas-y-límites-de-la-api)
9. [Monitoreo y solución de problemas](#9-monitoreo-y-solución-de-problemas)

---

## 1. Cómo funciona la integración

ClipFactory publica con la **YouTube Data API v3** (`videos.insert` con
**upload resumable**). La autenticación usa el flujo **offline** de OAuth 2.0:

```
┌────────────────────────────────────────────────────────────────────┐
│  UNA SOLA VEZ (fuera del pipeline, tu máquina):                    │
│                                                                    │
│  navegador → consentimiento de Google → authorization_code         │
│  → canje por { access_token, refresh_token }                       │
│  → guardas el REFRESH_TOKEN en credentials/youtube.conf            │
└────────────────────────────────────────────────────────────────────┘
                              │
                              ▼ (solo el refresh_token viaja al servidor)
┌────────────────────────────────────────────────────────────────────┐
│  EN CADA PUBLISH (automático, dentro de ClipFactory):              │
│                                                                    │
│  POST https://oauth2.googleapis.com/token                          │
│       (refresh_token + client_id + client_secret)                  │
│  → access_token (válido ~1 hora, se cachea en memoria)             │
│                                                                    │
│  PASO 1: POST /upload/youtube/v3/videos?uploadType=resumable       │
│          (solo metadatos JSON: título, descripción, tags,          │
│           privacyStatus, categoryId)                               │
│  → 200 OK + header Location (session URL)                          │
│                                                                    │
│  PASO 2: PUT <session URL> (los bytes del video mp4)               │
│  → 200/201 + { id: "dQw4w9WgXcQ", ... }                            │
│                                                                    │
│  → publications.external_id = "dQw4w9WgXcQ"                        │
│    publications.external_url = "https://youtu.be/dQw4w9WgXcQ"      │
└────────────────────────────────────────────────────────────────────┘
```

Puntos clave:

- **El pipeline nunca pide consentimiento en runtime**: solo canjea el
  refresh token por access tokens. Por eso el paso 4 se hace UNA vez.
- **No usa la librería cliente de Google** (`google.golang.org/api`): la
  implementación es HTTP directo (2 requests), lo que mantiene el binario
  estático `CGO_ENABLED=0` y las dependencias mínimas.
- **Idempotencia garantizada**: si el proceso muere entre el PASO 2 y el
  update de la DB, el reintento ve `status='published'` con `external_id`
  y NO vuelve a subir el video (evita duplicados en el canal).

## 2. Crear el proyecto en Google Cloud y activar la API

1. Ve a [console.cloud.google.com](https://console.cloud.google.com/) e inicia
   sesión con la cuenta de Google dueña del canal de YouTube.
2. Arriba a la izquierda: **Seleccionar proyecto → Nuevo proyecto**.
   - Nombre: `clipfactory` (o el que prefieras).
   - No necesitas facturación para la Data API v3 (la cuota gratuita basta,
     ver [§8](#8-cuotas-y-límites-de-la-api)).
3. Con el proyecto seleccionado, ve a **APIs y servicios → Biblioteca**.
4. Busca **YouTube Data API v3** y pulsa **Activar**.
5. Ve a **Pantalla de consentimiento de OAuth** (OAuth consent screen):
   - User type: **Externo** (las cuentas internas requieren Workspace).
   - App name: `clipfactory`, email de soporte: el tuyo.
   - **Alcances (scopes)**: agrega
     `https://www.googleapis.com/auth/youtube.upload`.
   - **Test users**: agrega tu propia cuenta de Google (mientras la app esté
     en modo "Testing", solo los test users pueden dar consentimiento).
   - Nota: en modo Testing el refresh token expira en **7 días**. Para un
     token de larga duración, pulsa **Publicar aplicación** (no requiere
     verificación si solo usas scopes no sensibles con datos propios).

## 3. Obtener Client ID y Client Secret

1. Ve a **APIs y servicios → Credenciales → Crear credenciales → ID de cliente
   de OAuth**.
2. Tipo de aplicación: **Aplicación de escritorio** (desktop app). El nombre
   es informativo: `clipfactory-cli`.
3. Pulsa **Crear**: te muestra el **Client ID** (termina en
   `.apps.googleusercontent.com`) y el **Client Secret** (empieza con
   `GOCSPX-`). Cópialos: los usarás en los pasos 4 y 5.

## 4. Generar el REFRESH_TOKEN (una sola vez)

### Opción A: con oauth2l (recomendada, menos pasos)

```bash
# instala oauth2l (binario único de Google)
#   Windows:  choco install oauth2l   (o descarga el .zip de releases)
#   Linux:    snap install oauth2l    (o descarga de github.com/google/oauth2l)

oauth2l fetch --credentials client_secret_xxx.json \
  --scope https://www.googleapis.com/auth/youtube.upload \
  --output_format json
```

- `client_secret_xxx.json` es el archivo que descargas desde la pantalla de
  credenciales (botón ⬇ del client creado).
- El comando abre el navegador: inicia sesión con la cuenta del canal,
  acepta la advertencia "app no verificada" (→ Avanzado → Ir a clipfactory)
  y concede el permiso.
- La salida JSON incluye `"refresh_token": "1//0g..."`. **Cópialo.**

### Opción B: con curl (dos pasos manuales)

```bash
# 1. authorization code: abre esta URL en el navegador (todo en una línea)
#    (CLIENT_ID reemplazando espacios por nada; redirect_uri es el loopback
#    genérico de desktop apps)
https://accounts.google.com/o/oauth2/v2/auth?
  client_id=TU_CLIENT_ID.apps.googleusercontent.com
  &redirect_uri=urn:ietf:wg:oauth:2.0:oob
  &response_type=code
  &scope=https://www.googleapis.com/auth/youtube.upload
  &access_type=offline
  &prompt=consent

#    ← el `prompt=consent` es OBLIGATORIO: sin él Google no devuelve
#      refresh_token si ya se concedió uno antes.
#    ← Google muestra el authorization code en pantalla: cópialo.

# 2. canjear el code por tokens
curl -s https://oauth2.googleapis.com/token \
  -d client_id=TU_CLIENT_ID.apps.googleusercontent.com \
  -d client_secret=TU_CLIENT_SECRET \
  -d code=EL_CODIGO_DEL_PASO_1 \
  -d grant_type=authorization_code \
  -d redirect_uri=urn:ietf:wg:oauth:2.0:oob
```

La respuesta incluye `refresh_token` y `access_token` (este último se ignora:
el pipeline genera los suyos). **Guarda el `refresh_token`** — no vuelve a
mostrarse sin repetir el flujo con `prompt=consent`.

> ⚠️ **Seguridad**: el refresh token permite subir videos a tu canal con el
> scope otorgado. Trátalo como una contraseña: no lo commitees, no lo pegues
> en logs. `credentials/` ya está en `.gitignore`.

## 5. Configurar credentials/youtube.conf

Crea `credentials/youtube.conf` (formato clave=valor, igual que `twitch.conf`):

```
CLIENT_ID = 1234567890-abc123.apps.googleusercontent.com
CLIENT_SECRET = GOCSPX-xxxxxxxxxxxxxxxx
REFRESH_TOKEN = 1//0gXXXXXXXXXXXXXXXXXXXXX
PRIVACY_STATUS = unlisted
CATEGORY_ID = 20
```

| Clave | Obligatoria | Default | Descripción |
|-------|:-----------:|---------|-------------|
| `CLIENT_ID` | ✅ | — | Client ID de OAuth (paso 3) |
| `CLIENT_SECRET` | ✅ | — | Client Secret de OAuth (paso 3) |
| `REFRESH_TOKEN` | ✅ | — | Token de larga duración (paso 4) |
| `PRIVACY_STATUS` | — | `public` | `public` \| `unlisted` \| `private` |
| `CATEGORY_ID` | — | `20` | Categoría del video (ver [§6](#6-categorías-de-youtube)) |

Recomendación para empezar: `PRIVACY_STATUS = unlisted` — puedes verificar los
videos subidos sin que aparezcan públicamente en el canal. Cuando confíes en el
pipeline, cambia a `public`.

Si falta alguna obligatoria (o el archivo no existe), el worker arranca igual
pero los jobs `publish` fallan con el mensaje claro
`no publisher configurado (falta Publisher en WorkerConfig)` y el resto del
pipeline (discovery → download → process → thumbnail) sigue funcionando.

## 6. Categorías de YouTube

`CATEGORY_ID` es el identificador numérico de la categoría de video de YouTube
(`snippet.categoryId`). Las más usadas:

| ID | Categoría |
|----|-----------|
| `20` | **Gaming** (default) — clips de streamers |
| `24` | Entertainment |
| `22` | People & Blogs |
| `23` | Comedy |
| `17` | Sports |
| `10` | Music |
| `28` | Science & Technology |
| `1` | Film & Animation |

Lista completa: `https://developers.google.com/youtube/v3/docs/videoCategories/list`
(con `regionCode=ES` o `US`).

## 7. Cómo funciona el job publish

El job `publish` apunta a una fila de `publications`
(`reference_type='publications'`, `reference_id=publications.id`). Hay **una
fila por (clip, plataforma)**: un fallo con YouTube no bloquea a TikTok/Kick.

### Máquina de estados de `publications`

```
                    ┌──────────────────────────────────────────┐
                    │  GetPendingPublications (reintento       │
                    │  natural cuando next_retry_at vence)     │
                    ▼                                          │
  pending ──► [worker ejecuta UploadVideo] ──► published      ─┘
                    │            │                ▲
       quotaExceeded│            │ error genérico │ (backoff vence)
                    ▼            ▼                │
        waiting_rate_limit      error ────────────┘
        (next_retry_at = +24h)  (next_retry_at = now · 2^attempts, cap 24h)
```

### Detalle de cada camino

1. **Éxito** → `status='published'`, `external_id`, `external_url`,
   `published_at=ahora`. `attempts` NO se incrementa (no hubo fallo).
2. **Cuota agotada** (`quotaExceeded` / `rateLimitExceeded`, HTTP 403) →
   `status='waiting_rate_limit'`, `next_retry_at = ahora + 24h`,
   `attempts` intacto. El job queda `done`: **el reintento es natural** —
   `GetPendingPublications` la recoge cuando `next_retry_at` vence (tras el
   reset de cuota de medianoche PT).
3. **Error genérico** (red, 5xx, archivo, API) → `status='error'`,
   `error_message`, backoff exponencial: `next_retry_at = ahora · 2^attempts`
   (1h, 2h, 4h, ... cap 24h), `attempts+1`. El job queda `error`.
4. **Publicación ya `published` con `external_id`** → **no-op**: protege
   contra re-entregas del job y crashes entre upload y update de DB.
5. **Clip o archivo faltante** → error antes de llamar a la API (no gasta
   cuota).

### Metadatos del video subido

Hoy (MVP): título `Clip <nombre-de-archivo>`, descripción fija, tags
`["shorts","clips"]`, y recorte defensivo a los límites de la API
(título ≤100 chars, descripción ≤5000). Enriquecer el título con el nombre del
streamer/juego está en el roadmap (requiere guardar más campos en `clips`).

## 8. Cuotas y límites de la API

La cuota de la YouTube Data API v3 es **diaria, por proyecto** (se resetea a
**medianoche hora del Pacífico**):

| Operación | Costo en cuota |
|-----------|---------------:|
| `videos.insert` (subir un video) | **~1600 unidades** |
| `videos.list` (consultar) | 1 unidad |
| Default del proyecto | **10,000 unidades/día** ≈ **6 uploads/día** |

Consecuencias prácticas para ClipFactory:

- Con el default puedes subir **~6 clips por día**. Si necesitas más,
  solicita incremento de cuota en el console de Google Cloud (formulario de
  "YouTube API Services - Audit and Quota Extension"; se aprueba para casos
  legítimos).
- El job `publish` consume cuota **solo cuando sube**: errores previos
  (archivo faltante, metadata inválida) y reintentos que no llegan a la API
  son gratis.
- Cuando la cuota se agota a mitad de día, los siguientes publishes reciben
  `quotaExceeded` y entran en `waiting_rate_limit` hasta el reset — el
  pipeline sigue procesando clips nuevos sin bloquearse.

## 9. Monitoreo y solución de problemas

### Ver el estado de las publicaciones

```bash
sqlite3 database/clipfactory.db "
  SELECT id, clip_id, status, attempts, next_retry_at, external_id,
         substr(error_message,1,60) AS err
  FROM publications WHERE platform='youtube' ORDER BY updated_at DESC LIMIT 20;"
```

### Errores comunes

| `error_message` | Causa | Solución |
|-----------------|-------|----------|
| `no publisher configurado` | `youtube.conf` falta o tiene claves vacías | Completar paso 5 y reiniciar el worker |
| `youtube: auth: 401` / `invalid_grant` | Refresh token revocado o expirado (app en Testing >7 días) | Regenerar token (paso 4, con `prompt=consent`); o publicar la app en el consent screen |
| `youtube: auth: 400 invalid_client` | Client ID/Secret incorrectos | Revisar `youtube.conf` contra el console |
| `youtube: api devolvió 403: quotaExceeded` | Cuota diaria agotada | Esperar reset (medianoche PT) o pedir más cuota (§8) |
| `youtube: api devolvió 400: invalidCategoryId` | `CATEGORY_ID` no existe | Usar un ID de la tabla §6 |
| `youtube: api devolvió 400: invalidTitle` | Título vacío o >100 chars | Ya se recorta defensivamente; reportar si ocurre |
| `clip N: archivo no encontrado` | El archivo `completed/` fue movido/borrado | Verificar `data/completed/` y `clips.filepath` |
| `youtube: init sin header Location` | Proxy intermedio elimina headers | Desactivar proxy para `googleapis.com` |

### Logs del worker

Cada publish deja líneas en el log del worker:

```
[worker] locked job 42 (type=publish, ref_type=publications, ref_id=7)
[worker] publish completo: publication=7 → dQw4w9WgXcQ (https://youtu.be/dQw4w9WgXcQ)
[worker] publication 7 espera reset de cuota hasta 2026-09-14 08:00:00 +0000 UTC
[worker] publication 7 falló, reintento en 1h0m0s
```

---

*Ver también: [Guía de Twitch](guia-twitch.md) para la parte de descarga, y el
README para la arquitectura general del pipeline.*
