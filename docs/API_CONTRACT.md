# ClipFactory — Contrato de API (FASE 1)

> Contrato `frontend ↔ backend` definido antes de implementar. Todos los endpoints
> listados son **nuevos** para el backend (que hoy solo es CLI). Cada sección indica
> claramente su estado. Ningún contrato inventa lógica que el backend ya tenga: la
> API expone la DB y encola jobs, no duplica ejecutores.

---

## 1. Estado global

> **FRONTEND REQUIRES BACKEND ENDPOINT** — hoy NO existe ninguna API HTTP.
> Cada endpoint de este documento requiere implementación backend (un `internal/api`
> aditivo + comando `clipfactory server`; ver `ARCHITECTURE.md §4`).

Leyenda de estados:

| Marca | Significado |
|-------|-------------|
| 🆕 **REQUIRED** | Endpoint nuevo que el frontend necesita; requiere implementación backend. |
| ✅ **EXISTS** | Ya implementado en el backend (hoy: ninguno como HTTP). |
| 🧭 **BACKLOG** | Depende de una feature de negocio aún inexistente (métricas, revisión, scheduling). |

---

## 2. Convenciones

### 2.1 Transporte y contenido

- Base URL: `VITE_API_URL` (ej. `http://localhost:8080/api`). Nunca hardcodear.
- JSON estricto. Timestamps **RFC3339 UTC** (misma convención que la DB).
- Errores con el formato estándar:
  ```json
  { "error": { "code": "VALIDATION_ERROR", "message": "…", "details": null } }
  ```

### 2.2 Envelope de listas (paginación)

```json
{
  "data": [ … ],
  "pagination": {
    "page": 1, "pageSize": 50,
    "total": 1234, "pageCount": 25,
    "hasNext": true, "hasPrev": false
  }
}
```

Query comunes: `page`, `pageSize` (1–100, default 50), filtros por dominio y `sort`.

### 2.3 Filtros genéricos de fecha

Similar al CLI (que usa texto RFC3339): `from`/`to` en RFC3339 para acotar ventanas.

### 2.4 Autenticación

El backend no tiene auth. En producción opcional se admite `Authorization: Bearer
<CLIPFACTORY_API_TOKEN>` (token de operador vía env, no en VITE). El frontend prepara
auth **como arquitectura** (login/logout/sesión/guards) y evoluciona a OAuth2/OIDC.

### 2.5 Endpoints de fallo → códigos HTTP

| HTTP | Significado en el contrato |
|------|----------------------------|
| 400 | entrada inválida (`VALIDATION_ERROR`) |
| 401 | sin autenticar (`AUTHENTICATION_REQUIRED`) |
| 403 | sin permiso (`AUTHORIZATION_DENIED`) |
| 404 | recurso inexistente (`NOT_FOUND`) |
| 409 | conflicto de estado/UNIQUE (`CONFLICT`) |
| 429 | demasiadas peticiones (`RATE_LIMITED`) |
| 500 | error interno (`INTERNAL_ERROR`) |
| 503 | worker/DB no disponible (`UNAVAILABLE`) |

---

## 3. Endpoints por dominio

### 3.1 System & Health

| Endpoint | Método | Estado | Descripción |
|----------|--------|--------|-------------|
| `/api/health` | GET | 🆕 **REQUIRED** | Aliveness + versión de schema + estado del worker. |
| `/api/system/overview` | GET | 🆕 **REQUIRED** | Dashboard: conteos por estado de cada tabla + flags de workers. |
| `/api/system/config` | GET | 🆕 **REQUIRED** | Config **no sensible** (poll, concurrency, rutas, credenciales *presente/ausente*). |

`GET /api/health` → `200`:
```json
{ "status": "ok", "schema_version": 12, "worker": "stopped" }
```

`GET /api/system/overview` → `200`: es la forma HTTP del `clipfactory status`.
```json
{
  "sources":    { "twitch": { "active": 1, "inactive": 0 } },
  "source_clips": { "twitch": { "detected": 3, "downloaded": 12, "skipped": 1, "error": 1 } },
  "videos":     { "incoming": 0, "processing": 1, "completed": 8, "failed": 2 },
  "clips":      { "processing": 0, "completed": 8, "failed": 1 },
  "publications": { "youtube": { "pending": 2, "published": 6, "error": 1, "waiting_rate_limit": 1, "failed": 0 } },
  "jobs":       { "discovery": { "queued": 1, "running": 0, "done": 42, "error": 1 }, "…": "…" }
}
```

### 3.2 Channels / Sources

Modelo: `sources` (`id`, `platform`, `channel_id`, `channel_name`, `active`,
`last_checked_at`, `created_at`, `updated_at`).

| Endpoint | Método | Estado | Descripción |
|----------|--------|--------|-------------|
| `/api/sources` | GET | 🆕 **REQUIRED** | Lista canales (filtro `platform`, `active`; sort/offset). |
| `/api/sources` | POST | 🆕 **REQUIRED** | Alta de canal (valida `platform ∈ {twitch, kick}`, `channel_id` no vacío). |
| `/api/sources/:id` | GET | 🆕 **REQUIRED** | Detalle (con conteos derivados: clips detectados, etc.). |
| `/api/sources/:id` | PATCH | 🆕 **REQUIRED** | Actualizar `channel_name`, `active`, `channel_id`. |
| `/api/sources/:id` | DELETE | 🆕 **REQUIRED** | Baja lógica del canal (o física; decidir en implementación). |
| `/api/sources/:id/discovery` | POST | 🆕 **REQUIRED** | Encola job `discovery` (equivalente HTTP del CLI `discovery`). Idempotente. |
| `/api/sources/:id/clips` | GET | 🆕 **REQUIRED** | SourceClips de ese canal (paginado, filtros). |
| `/api/sources/sync-file` | GET | 🧭 **BACKLOG** | Re-aplicar `sources.yaml` (requiere lectura del archivo en servidor). |

Request `POST /api/sources`:
```json
{ "platform": "twitch", "channel_id": "4919", "channel_name": "illojuan", "active": true }
```
`201` → el recurso creado con `id`. `409` si `(platform, channel_id)` ya existe.

### 3.3 Source clips (detectados)

Modelo: `source_clips` (`id`, `platform`, `platform_clip_id`, `source_id`, `title`,
`duration_seconds`, `created_at_platform`, `status`, `error_message`, `created_at`,
`updated_at`).

| Endpoint | Método | Estado | Descripción |
|----------|--------|--------|-------------|
| `/api/clips` | GET | 🆕 **REQUIRED** | **Listado principal.** Ver 3.4. |
| `/api/source-clips/:id` | GET | 🆕 **REQUIRED** | Detalle de un clip detectado. |
| `/api/source-clips/:id/download` | POST | 🆕 **REQUIRED** | Encola job `download` si `status=detected`. |
| `/api/source-clips/:id/skip` | POST | 🆕 **REQUIRED** | Marca `skipped` (acción de operador). |

### 3.4 Clips — listado unificado (pantalla principal)

**Problema de dominio**: el frontend muestra el *árbol* source_clip→video→clip→
publications. Se define un **DTO agregado** `ClipListItem` que el backend compone por
JOIN (no duplica lógica de negocio: solo una lectura).

`GET /api/clips?page=&pageSize=&channel_id=&platform=&status=&from=&to=&min_views=&max_duration=&sort=&order=`

| Query | Valores |
|-------|---------|
| `channel_id` | filtrar por canal |
| `platform` | `twitch`/`kick` (origen) |
| `status` | `detected`/`downloaded`/`downloaded_processing`/`completed`/`processing`/`failed`/`skipped`/`error` |
| `from`/`to` | ventana `created_at_platform` |
| `min_views` | 🧭 BACKLOG (requiere métricas) |
| `sort` | `newest`/`views`/`engagement`/`growth`/`duration` (default `newest`) |
| `order` | `asc`/`desc` |

```json
{
  "data": [{
    "source_clip": {
      "id": 5, "platform": "twitch", "platform_clip_id": "ClipAbC123",
      "source_id": 1, "title": "imposible...", "duration_seconds": 42.7,
      "created_at_platform": "2026-09-15T20:10:00Z", "status": "downloaded"
    },
    "video": { "id": 7, "filepath": "../data/incoming/ClipAbC123.mp4",
               "status": "incoming", "width": 1920, "height": 1080,
               "duration_seconds": 42.7 },
    "clip": { "id": 9, "filepath": "../data/completed/ClipAbC123.mp4",
              "thumbnail_path": "../data/thumbnails/ClipAbC123.mp4.jpg",
              "status": "completed", "duration_sec": 42.0 },
    "channel": { "id": 1, "channel_name": "illojuan", "platform": "twitch",
                 "channel_id": "4919" },
    "publications": [
      { "id": 3, "platform": "youtube", "status": "published",
        "external_url": "https://youtu.be/dQw4w9WgXcQ", "attempts": 0,
        "published_at": "2026-09-16T08:00:00Z", "views": null }
    ],
    "metrics": { "views": null, "views_per_hour": null, "growth": null,
                  "engagement": null, "available": false }
  }],
  "pagination": { "page": 1, "pageSize": 50, "total": 1234, "pageCount": 25,
                  "hasNext": true, "hasPrev": false }
}
```

> `metrics` es `null` hasta que el backend almacene métricas de plataforma
> (🧭 BACKLOG). `views` en `publications` y `metrics` comparten origen.

### 3.5 Detalle de clip + archivos

| Endpoint | Método | Estado | Descripción |
|----------|--------|--------|-------------|
| `/api/clips/:id` | GET | 🆕 **REQUIRED** | DTO completo de un clip (árbol completo). Aquí `id` = `source_clips.id`. |
| `/api/clips/:id/video` | GET | 🆕 **REQUIRED** | Redirige/stream del archivo de origen (byte-range). |
| `/api/clips/:id/thumbnail` | GET | 🆕 **REQUIRED** | Servir `clips.thumbnail_path` (cacheable). |
| `/api/clips/:id/processed` | GET | 🆕 **REQUIRED** | Stream del clip vertical final (byte-range, si el operador lo permite). |
| `/api/clips/:id/queue-for-download` | POST | 🆕 **REQUIRED** | Encola `download` (selectión manual). |
| `/api/clips/:id/queue-for-process` | POST | 🆕 **REQUIRED** | Encola `process` (re-procesar). |
| `/api/clips/:id/regenerate-thumbnail` | POST | 🆕 **REQUIRED** | Encola `thumbnail`. |
| `/api/clips/:id/review` | POST | 🧭 **BACKLOG** | Estado de revisión manual (require estado en DB). |

### 3.6 Previews (video)

- El VideoPlayer usa las URLs de 3.5 (thumbnail = poster). El backend debe servir con
  `Accept-Ranges: bytes` y `Content-Type: video/mp4` para scrubbing.
- No descargar el archivo entero al navegador. Si las rutas `data/*` no son accesibles
  por HTTP, estos endpoints son quien las expone (configuración `CLIPFACTORY_API_*`).

### 3.7 Publications

Modelo: `publications` (`id`, `clip_id`, `platform`, `status`, `attempts`,
`next_retry_at`, `external_id`, `external_url`, `error_message`, `published_at`,
`created_at`, `updated_at`).

| Endpoint | Método | Estado | Descripción |
|----------|--------|--------|-------------|
| `/api/publications` | GET | 🆕 **REQUIRED** | Lista filtrable (`platform`, `status`, `from`, `to`) con JOIN a clip. |
| `/api/publications/:id` | GET | 🆕 **REQUIRED** | Detalle. |
| `/api/publications` | POST | 🆕 **REQUIRED** | Crea publication `pending` para `(clip_id, platform)` (UNIQUE → 409). |
| `/api/publications/:id/retry` | POST | 🆕 **REQUIRED** | Encola job `publish` para esa publication (`EnqueueJob`). |
| `/api/publications/:id/cancel` | POST | 🆕 **REQUIRED** | Marca dead-letter `failed` (operador lo decide). |
| `/api/publications/:id/mark-published` | POST | 🧭 **BACKLOG** | Corrección manual de estado. |
| `/api/publications/:id/stats` | GET | 🧭 **BACKLOG** | Métricas de la plataforma para ese video. |

### 3.8 Jobs / Cola

Modelo: `jobs` (`id`, `type`, `reference_id`, `reference_type`, `status`, `attempts`,
`locked_at`, `locked_by`, `error_message`, `created_at`, `updated_at`).

| Endpoint | Método | Estado | Descripción |
|----------|--------|--------|-------------|
| `/api/jobs` | GET | 🆕 **REQUIRED** | Lista/paginación por `type`, `status` (default excluye `done`). |
| `/api/jobs/:id` | GET | 🆕 **REQUIRED** | Detalle. |
| `/api/jobs/stats` | GET | 🆕 **REQUIRED** | Igual que `GetJobStats` (conteos por tipo/estado). |
| `/api/jobs/:id/retry` | POST | 🆕 **REQUIRED** | Re-encola job `error` (`EnqueueJob` nuevo con mismo ref). |
| `/api/jobs/:id/cancel` | POST | 🆕 **REQUIRED** | Marca `error` un job `queued/running` (best-effort). |
| `/api/jobs/:id/priority` | PATCH | 🧭 **BACKLOG** | Requiere soporte de prioridad en la cola (no existe). |
| `/api/jobs/:id/pause` / `resume` | POST | 🧭 **BACKLOG** | No existe en el backend. NO simular. |

> **No simular acciones inexistentes**: pause/resume/prioritize solo aparecen en la UI
> cuando el backend las soporte (estado 🧭 → ocultas/deshabilitadas).

### 3.9 Analytics

> Todos los de este bloque son 🧭 **BACKLOG**: dependen de que el backend almacene
> métricas de plataforma (views por publication) vía un nuevo job de recolección.

| Endpoint | Método | Estado | Descripción |
|----------|--------|--------|-------------|
| `/api/analytics/overview` | GET | 🧭 **BACKLOG** | KPI: views totales, clips, publicadas, pendientes. |
| `/api/analytics/timeseries` | GET | 🧭 **BACKLOG** | Parámetros `granularity=day&from=&to=&platform=`. |
| `/api/analytics/channels` | GET | 🧭 **BACKLOG** | Rendimiento por canal. |
| `/api/analytics/platforms` | GET | 🧭 **BACKLOG** | Publicaciones por plataforma. |
| `/api/clips?sort=views` | GET | 🧭 **BACKLOG** | Ranking por vistas (misma query de 3.4). |

Mientras tanto, el frontend puede construir un **overview derivado de la DB**
(conteos por estado) usando 3.1 + 3.8 — sin views.

### 3.10 Automations

> No existe ningún modelo de automatización en el backend. La UI construirá reglas
> client-side contra un contrato mínimo que el backend deberá persistir/ejecutar.

| Endpoint | Método | Estado | Descripción |
|----------|--------|--------|-------------|
| `/api/automations` | GET | 🧭 **BACKLOG** | Lista de reglas. |
| `/api/automations` | POST | 🧭 **BACKLOG** | Crear regla `CUANDO…ENTONCES…`. |
| `/api/automations/:id` | PUT / DELETE | 🧭 **BACKLOG** | Editar/eliminar/activar. |
| `/api/automations/:id/evaluate` | POST | 🧭 **BACKLOG** | Ejecutar contra el backlog (discovery). |

Hasta su implementación, el frontend guarda las reglas como **templates configurados
en la UI** y las expone como borrador (marcado "pendiente de backend"), sin fingir
ejecución.

### 3.11 Workers / Realtime

| Endpoint | Método | Estado | Descripción |
|----------|--------|--------|-------------|
| `/api/workers` | GET | 🆕 **REQUIRED** (mínimo) | `locked_by` activos + últimos `jobs.locked_at` (derivado de la DB). |
| `/api/events` (SSE) o `/ws` | GET | 🧭 **BACKLOG** | Stream de progreso de jobs (server-sent events). |

La UI **no simula progreso**: si no hay SSE, el progreso se obtiene con polling
controlado (refetchInterval 5–10s) de `/api/jobs`.

### 3.12 Logs

| Endpoint | Método | Estado | Descripción |
|----------|--------|--------|-------------|
| `/api/logs` | GET | 🆕 **REQUIRED** | Últimos `logs` paginados (filtro nivel/módulo). |

---

## 4. Checklist de dependencias backend (resumen)

El **incremento mínimo irrenunciable** para que el frontend sea funcional:

1. `internal/api`: router HTTP aditivo + comando `clipfactory server`.
2. Endpoints listados como 🆕 **REQUIRED** de las secciones 3.1–3.4, 3.5 (streams),
   3.7, 3.8 y 3.11.
3. Servir archivos (video/thumbnail) con byte-range.
4. (Opcional, ya visible) `GET /api/system/overview` = `status` del CLI.

Postergados (🧭): métricas de views (bloquea full analytics/ranking por views),
revisión persistente, scheduling, prioridades, pausa/resume, automations, SSE.
Mientras no existan, la UI los muestra **deshabilitados o en estado "no disponible"**,
nunca simulados.