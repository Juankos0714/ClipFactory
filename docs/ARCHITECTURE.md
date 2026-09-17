# ClipFactory — Análisis de arquitectura (FASE 1)

> Documento de trabajo de la **FASE 1** del plan maestro del frontend.
> Analiza el backend existente, identifica el gap de API y define la arquitectura
> del frontend. Complementa a `API_CONTRACT.md` y `FRONTEND_PLAN.md`.

---

## 1. Estado actual del sistema

ClipFactory es un **pipeline automatizado de clips cortos** (Twitch/Kick → YouTube
Shorts / Meta Reels) implementado como un **binario Go CLI** de un solo proceso.

### 1.1 Stack backend (verificado en el código)

| Área | Tecnología | Detalle |
|------|------------|---------|
| Lenguaje | Go 1.24 | módulo `github.com/juankos0714/clipfactory`, `CGO_ENABLED=0` |
| Persistencia | SQLite (WAL) | `modernc.org/sqlite`, DB única en `database/clipfactory.db` |
| Cola | En la DB | tabla `jobs`, locks `locked_at/locked_by`, semántica at-least-once |
| Config | Env vars + YAML + `.conf` | `CLIPFACTORY_*`, `config/sources.yaml`, `credentials/*.conf` |
| Origen clips | Twitch (Helix) / Kick | adaptadores en `internal/adapter/` |
| Procesamiento | ffmpeg | crop central 9:16 → 1080x1920, thumbnails |
| Publicación | YouTube (Data API v3) / Meta (Graph API) | uploads resumables, backoff, reconciliación anti-duplicados |
| Exposición de red | 🚫 **Ninguna** | **No existe API REST ni servidor HTTP** (salvo un `/metrics` Prometheus opcional y mínimo) |

### 1.2 Comandos del CLI

`cmd/clipfactory/main.go` define la superficie actual:

| Comando | Función |
|---------|---------|
| `clipfactory worker` | Loop principal: sondea `jobs`, ejecuta discovery/download/process/thumbnail/publish/poll_publications. Auto-discovery al arrancar. |
| `clipfactory status` | Resumen del sistema leyendo la DB (conteos por estado). |
| `clipfactory discovery` | Encola un job `discovery` por canal activo (idempotente). |
| `clipfactory doctor` | Verifica dependencias (ffmpeg, TwitchDownloaderCLI, credenciales, DB). |
| `clipfactory help` | Ayuda. |

**Conclusión:** el backend es una pieza *headless*. Todo el estado visible (canales,
clips, jobs, publicaciones) vive en SQLite y **no hay ningún mecanismo hoy para que
una interfaz web lea o orqueste el sistema**. Este es el gap central que el frontend
debe resolver junto con una capa de API (ver `API_CONTRACT.md`).

---

## 2. Modelo de datos y máquinas de estado

Fuente de verdad: `internal/db/models.go` y `internal/db/migrations.go`. Todas las
fechas son **texto RFC3339 UTC** (comparaciones lexicográficas en SQL).

### 2.1 Tablas

| Tabla | Propósito | UNIQUE |
|-------|-----------|--------|
| `schema_migrations` | Versionado de migraciones | `version` |
| `sources` | Canales de origen (Twitch/Kick) | `(platform, channel_id)` |
| `source_clips` | Clips detectados en el origen | `(platform, platform_clip_id)` |
| `videos` | Archivos descargados localmente | `filepath` |
| `clips` | Clips verticales 1080x1920 listos para publicar | `filepath` |
| `publications` | Intento de publicación por (clip, plataforma) | `(clip_id, platform)` |
| `jobs` | Cola de trabajos | — (locks en DB) |
| `logs` | Auditoría de eventos | — |

### 2.2 Entidades y estados

```
sources:        active (bool) + last_checked_at

source_clips:   detected ──► downloaded ──► (skipped | error)

videos:         incoming ──► processing ──► (completed | failed)

clips:          processing ──► (completed | failed)   + thumbnail_path

publications:   pending ──► published | error | waiting_rate_limit | failed
                (failed = dead-letter: permanente o techo de intentos)

jobs:           queued ──► running ──► (done | error)
                (tipos: discovery, download, process, thumbnail, publish,
                 poll_publications)
```

### 2.3 Relación entre entidades (clave para "un clip")

Una misma pieza de contenido avanza por **4 tablas encadenadas por FK**:

```
sources (canal)
  └─► source_clips (detectado: título, duración, created_at_platform)
       └─► videos (descargado: archivo local, resolución, duración)
            └─► clips (procesado vertical: filepath+thumbnail_path)
                 └─► publications (por plataforma: estado, external_id/url, attempts,
                                   next_retry_at, published_at)
                       └─► jobs (el trabajo del pipeline apunta a cada una vía
                                 reference_type/reference_id)
```

**Implicación para el frontend:** *no existe una fila "clip" única* que reúna
origen+archivo+procesado+publicación. La UI debe componer este **árbol de datos**
(típicamente vía JOIN en el backend y devolver un DTO agregado) en lugar de asumir
una tabla `clips` con todo.

### 2.4 Métricas de rendimiento (views)

🚫 **La DB no almacena métricas de rendimiento externas** (views, likes, comments)
de los clips publicados. `publications` solo tiene `external_id/url` y estado. La
reconciliación de YouTube (`internal/adapter/youtube/reconcile.go`) consulta uploads
recientes **solo para evitar duplicados**, no para extraer estadísticas.

> Consecuencia directa para el plan maestro: el **Dashboard, el ranking y la sección
> de Analítica dependen de un backend nuevo que almacene y consulte métricas de
> plataforma** (`GET /analytics/...`, `GET /clips?...&sort=views`). Hasta entonces,
> esos módulos se implementan contra el contrato y con datos limitados a lo que la DB
> sí contiene (estados, fechas, duraciones, intentos).

---

## 3. Flujos del pipeline (cómo orquesta realmente el sistema)

1. **Discovery**: job `discovery` → consulta la API de la plataforma con
   `sources.last_checked_at` como ventana; `UpsertSourceClip` (idempotente) y encola
   `download` solo para clips genuinamente nuevos. Respeta cuota de disco y rate limit
   (429 → re-encola el discovery con retardo).
2. **Download**: job `download` → descarga atómica a `data/incoming/`; inserta
   `videos` (status `incoming`), marca `source_clips=downloaded`, encola `process`.
3. **Process**: job `process` → ffmpeg crop central 9:16 → 1080x1920 a
   `data/completed/`; inserta `clips` (status `completed`), marca `videos=completed`,
   encola `thumbnail`.
4. **Thumbnail**: job `thumbnail` → ffmpeg extrae frame 0.5s a `data/thumbnails/`,
   guarda en `clips.thumbnail_path`.
5. **Review**: 🚫 **No implementado en backend.** Es manual/humano en el MVP. El
   frontend representa este paso como UX (aprobar "manualmente"), no como un estado
   real de la DB.
6. **Publish**: job `publish` apuntando a `publications`. Reintentos: error transitorio
   → backoff exponencial `2^attempts` (cap 24h); cuota diaria →
   `waiting_rate_limit` + `next_retry_at ~24h`; error permanente →
   `failed` (dead-letter). Re-encolado automático vía `poll_publications` y jobs
   futuros (`created_at = next_retry_at`). Reconciliación anti-duplicados con markers
   `cf-<clipID>-<videoID>` en la descripción.

**Capacidades aún inexistentes en el backend** (relevantes para la UI):
- Revisión/desaprobación con estado persistente.
- Edición de title/description por clip previa a la publicación.
- Estados "Scheduled" de publicación (el backend solo conoce timestamps de retry).
- Métricas de views.
- Multi-canal de Meta además de una página única.

---

## 4. El gap: sin API REST

**FRONTEND REQUIRES BACKEND ENDPOINT.** El backend no expone ninguna API HTTP.
Todo endpoint que el frontend consuma es nuevo. Para no romper el backend existente,
la capa de API debe ser **aditiva**:

```
cmd/clipfactory (CLI existente) ──► se extiende con el comando `server`
                                        │
internal/api (NUEVO, aditivo)          │
  ├── http.Handler con rutas REST      │
  ├── lee de la MISMA SQLite (db pkg)  │
  └── encola jobs con db.EnqueueJob    │  ← reutiliza la lógica de negocio,
                                        │     NO la duplica
internal/worker (existente)            │
  └── los jobs encolados por la API     │
      los procesa el worker normal      ▼
```

### 4.1 Principios de la capa de API (a respetar en el contrato)

- **No duplicar lógica de negocio**: la API solo expone operaciones sobre la DB
  (`db.*`) y encolado de jobs (`db.EnqueueJob`). La máquina de estados y los
  ejecutores siguen viviendo en `internal/worker`.
- **Acciones = encolar, no ejecutar**: `POST /clips/:id/download` encola un job
  `download`; no ejecuta la descarga en la goroutine del HTTP handler.
- **Simetría con el CLI**: los endpoints replican lo que `status`/`discovery` hacen
  por consola, de modo que el contrato refleja capacidades reales.
- **Autenticación**: el backend no tiene auth. El frontend debe preparar la
  arquitectura (login/logout/sesión) pero **no inventar un sistema incompatible**.
  Recomendación: token de API simple servido por env (`CLIPFACTORY_API_TOKEN`) para
  producción, y la UI construida para evolucionar a OAuth2/OIDC.

---

## 5. Arquitectura frontend propuesta

React + TypeScript (estricto) + Vite, modular por feature. El backend solo sirve JSON;
**todo el estado de server vive en TanStack Query**; el estado global de cliente es
excepcional (Zustand solo para UI transversal).

```
src/
├── app/
│   ├── router/            # rutas + guards (ProtectedRoute, role-based)
│   ├── providers/         # QueryClientProvider, AuthProvider, RealtimeProvider
│   └── config/            # env (VITE_API_URL, VITE_APP_NAME)
│
├── components/
│   ├── ui/                # Button, Input, Select, Dialog, Table, Badge, Tabs,
│   │                      # Skeleton, EmptyState, ErrorState, Tooltip, Toast...
│   ├── layout/            # AppShell, Sidebar, Topbar, ResponsiveContainer
│   └── shared/            # VideoPlayer, MetricCard, StatusBadge, RankingCard
│
├── features/
│   ├── dashboard/         # SystemStatus, MetricsOverview, TopClips, ProductionQueue
│   ├── channels/          # lista, formulario alta/edición, sincronizar
│   ├── clips/             # tabla, filtros, preview, ranking, selección
│   ├── production/        # pipeline por clip, cola, jobs, retry
│   ├── publishing/        # publications por plataforma, aprobar/publicar
│   ├── analytics/         # Recharts: views, publicaciones, por canal/periodo
│   ├── automation/        # constructor visual de reglas (CUANDO/ENTONCES)
│   └── settings/          # workers, sistema, config
│
├── entities/
│   ├── channel/           # Channel types + repo
│   ├── clip/              # Clip/ClipAggregate types + repo (ranking domestico)
│   ├── job/               # Job types + repo
│   ├── publication/       # Publication types + repo
│   └── automation/        # AutomationRule types + repo
│
├── services/
│   ├── api/               # apiClient (axios) + interceptores + endpoints
│   ├── auth/              # sesión, token storage seguro
│   └── realtime/          # SSE/WebSocket si el backend lo provee
│
├── hooks/                 # useClips, useChannels, useJobs, usePublications...
├── lib/                   # ranking, format, dates, validations (Zod schemas)
├── types/                 # tipos compartidos de dominio y API
└── utils/                 # helpers puros
```

### 5.1 Dependencias y su justificación

| Dependencia | Justifica su existencia |
|-------------|------------------------|
| `react`, `react-dom` | UI |
| `react-router` | navegación |
| `@tanstack/react-query` | estado de servidor, cache, invalidation |
| `react-hook-form` + `zod` (+ `@hookform/resolvers`) | formularios + validación |
| `axios` | HTTP con interceptores (uniforme) |
| `tailwindcss` | estilado consistente y responsive (no hay tema previo que pisar) |
| `lucide-react` | iconografía del dashboard |
| `recharts` | analítica (gráficos) |
| `zustand` | SOLO si surge estado global de cliente real (UI transversal) |
| `@tanstack/react-virtual` | listas grandes (10k+ clips) SI el backend no paginara |

No se agrega: Redux, Context pesados, bibliotecas de tabla pesadas, dayjs si
`Intl`/`@internationalized/date` alcanza (decidir en FASE 2), ni una lib de
componentes completa (construimos el mini-design-system propio).

### 5.2 Separación de responsabilidades (SOLID aplicado)

```
Page (presentación)
  └─► useClips(useQuery)                    Hook composición (estados/errores del query)
        └─► clipsEntity.getList(params)     Servicio/repo del feature
              └─► apiClient.get('/clips')   Cliente HTTP (única capa con axios)
```

- **Page** → presentación y eventos; no conoce axios ni endpoints.
- **Hook** → orquesta queries, derivación y estados (loading/error/empty/success).
- **Entity/Repo** → define el "repository" del feature (interfaz segregada:
  `ChannelRepository`, `ClipRepository`, `PublicationRepository`, `AnalyticsRepository`).
- **apiClient** → URL base desde `VITE_API_URL`, timeout, interceptor de errores que
  clasifica (`NetworkError`, `AuthError`, `ValidationError`, `ServerError`, ...).

### 5.3 Reglas de estado

- Server state → TanStack Query (`staleTime`/`gcTime`/`retries`/invalidation; polling
  controlado si no hubiera SSE; optimista solo en acciones de bajo riesgo).
- UI state → `useState` local.
- Global client state → Zustand solo si es imprescindible (p.ej. drawer mobile, modo
  manual/semi/auto si fuera preferencia del operador).

### 5.4 Ranking transparente (sin "IA")

El ranking es un **score determinista y documentado**, calculado en el frontend sobre
métricas que el backend provea (`views`, `views/hour`, recencia, duración, engagement
si existiera). Se muestra el desglose ("Razones: alta recencia, alta velocidad...") y
se deja claro que no es una predicción. La fórmula vive en `lib/ranking.ts`, es
configurable por constantes y testeable por unidad.

---

## 6. Derechos y publicación de terceros (§43–44 del plan maestro)

El sistema gestiona clips de canales de **terceros** (Twitch/Kick). La UI representa
claramente plataforma/canal/origen y no debe asumir `channel == operador`. El modelo
de datos actual **no tiene** columna `rights_status`; el contrato la incluye como
evolutiva:

```
rights_status: UNKNOWN | AUTHORIZED | RESTRICTED
```

La UI mostrará avisos antes de pedir publicación de contenido de terceros y
diferenciará `downloadable` de `commercially reusable`. Mientras el backend no exponga
`rights_status`, el frontend lo deriva por default (`UNKNOWN`) con aviso genérico.

---

## 7. Contexto de ejecución y despliegue

- **Dev**: Vite en `http://localhost:5173` con proxy a la API (`VITE_API_URL`).
- **Prod**: **Vercel** sirviendo el build estático del frontend + `rewrite` de la
  SPA (fallback a `index.html`). La API la servirá `clipfactory server` en el
  servidor Linux (nico-server); el frontend apunta a ella con `VITE_API_URL` pública
  (https) y CORS configurado en el server aditivo. El frontend **nunca** debe
  contener secretos (variables `VITE_*` van al bundle).
- **Realtime**: si el backend expone SSE/WS para progreso de jobs y estado de workers,
  el frontend se suscribe; de lo contrario, polling controlado con TanStack Query
  (refetchInterval por vista: dashboard 10s → ok).