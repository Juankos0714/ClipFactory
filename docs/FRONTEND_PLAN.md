# ClipFactory Frontend — Plan de implementación (FASE 1)

> Documento definitivo de planificación. Complementa a `ARCHITECTURE.md` (análisis
> y arquitectura) y a `API_CONTRACT.md` (contratos REST). Aquí: **qué se construye,
> en qué orden, con qué criterio de "terminado"**.

---

## 1. Enfoque global

Se construye un **frontend de administración completa** (el maestro pide un "centro de
control", no un wrapper de comandos). El backend hoy es CLI sin API REST, así que todo
el contrato vive en `API_CONTRACT.md` con markers:

- 🆕 **REQUIRED** → la UI queda **bloqueada** si no existe; se implementa el endpoint
  backend como parte de esta iniciativa (aditivo, sin romper el worker).
- 🧭 **BACKLOG** → la UI lo muestra deshabilitado con "no disponible", nunca simulado.

> El frontend NO duplica lógica de negocio. Solo presenta el DTO que la API
> expone, encola acciones (que el worker ya sabe ejecutar) y gestiona estado de UI.

---

## 2. Principios de trabajo

1. **Fases verificables**: cada fase termina con typecheck + lint + tests verdes y
   una captura visual (screenshot manual o a11y smoke).
2. **Backend primero para lo REQUIRED**: cada fase arranca implementando los
   endpoints 🆕 que su página necesita (ver `API_CONTRACT.md`), en un servidor Go
   aditivo que reusa `internal/db`/`internal/worker` — jamás duplicando lógica.
3. **Contrato como fuente de verdad única**: `types/` y los services del frontend
   se generan DA partir del contrato, no al revés.
4. **Sin inventar features**: lo que el backend no soporta (métricas, ranking por
   views, pausa de jobs) se marca explícito en la UI.
5. **Accesible + responsive + testable** desde la primera página, no al final.

---

## 3. Fases

### FASE 2 — Scaffold y capa base

**Objetivo**: app arrancable con rutas, providers, API client, errores y layout.

| Entregable | Descripción | Verificación |
|------------|-------------|--------------|
| Scaffold Vite (React+TS) | `npm create vite` configurado, tsconfig estricto, ESLint+Prettier | `pnpm build` + `pnpm lint` verdes |
| Router | 8 rutas del sitemap + `NotFound` | navegación manual |
| Providers | QueryClientProvider, AuthProvider (contrato), RealtimeProvider (SSE optional) | sin errores de consola |
| API client | `apiClient` (axios, base URL, timeout, interceptores, clasificación de errores) | tests unitarios del client |
| Errores | Mapa amigable de errores backend (mapeo del contrato) | tests |
| ConfirmDialog/Toast/Dialog | Base del mini-design-system | snapshot |
| Sidebar + Topbar + responsive shell | Drawer móvil, navegación por teclado | a11y smoke |

**Criterio "terminado"**: `pnpm dev` en 320px y 1440px, sin errores; typecheck y lint
limpios; los 5 estados UI (idle/loading/success/empty/error) implementados en un
componente de ejemplo (`MetricCard` + `EmptyState`).

---

### FASE 3 — Dashboard + Channels + Clips (MVP principal)

**Objetivo**: las 3 pantallas del núcleo con datos reales del contrato.

| Página | Endpoints que consume (REQUIRED) | Verificación |
|--------|----------------------------------|--------------|
| Dashboard (`/`) | `GET /api/system/overview`, `GET /api/clips` (top), `GET /api/workers` | KPIs reales, ranking transparente, workers online/offline |
| Channels (`/channels`) | `CRUD /api/sources/*`, `GET /api/source-clips`, triggers discovery | alta/edición/activa/desactiva/borra/sincroniza, prueba de flujo |
| Clips (`/clips`) | `GET /api/clips` (filtros+sort+paginación), `GET /api/clips/:id` | filtros combo, orden, paginación, preview, selección múltiple |
| + `GET /api/jobs` (para cola en clips) | — | estados de pipeline visibles |

**Criterio "terminado"**: flujo E2E `Channels → Add channel → Clips → Filter → Select`
funcional contra la API (o el stub si el backend aún no publica), con estados vacíos.

---

### FASE 4 — Production + Queue (pipeline operativo)

| Página | Endpoints | Verificación |
|--------|-----------|--------------|
| Production (`/production`) | `GET /api/clips?status`, `GET /api/jobs`, `GET /api/publications` | pipeline Detected→…→Published, retry de errores |
| Queue (`/production/queue`) | `GET /api/jobs`, `POST /:id/retry`, `cancel` | acciones reales, sin simular |

**Criterio "terminado"**: ver un clip pasar de detected→published (stub) y reintentar
un job 'error'.

---

### FASE 5 — Automation (constructor CUANDO/ENTONCES)

| Página | Endpoints | Verificación |
|--------|-----------|--------------|
| Automation (`/automation`) | 🧭 **BACKLOG** — la UI usa contrato mínimo + marcas "pendiente de backend" | constructor visual funciona contra template local |

Solo se habilita "ejecutar" cuando el backend lo soporte. Nunca se finge ejecución.

---

### FASE 6 — Publishing + Analytics

| Página | Endpoints | Verificación |
|--------|-----------|--------------|
| Publications (`/publications`) | `GET /api/publications`, retry/cancel | estados por plataforma, errores legibles |
| Analytics (`/analytics`) | 🧭 **BACKLOG** — usa DTO derivado + aviso "requiere métricas backend" | gráficos Recharts con datos presentes |

---

### FASE 7 — Auth + Realtime (si el backend lo soporta)

- Login/logout/sesión + protected routes (estructura lista; activación condicional).
- SSE/WebSocket para progreso de jobs si existe `poll_publications`/SSE en backend.

---

### FASE 8 — Hardening

- Testing (tests por página + flujos E2E Playwright del maestro).
- Performance: virtualización de lista de clips (10k+), code splitting por ruta,
  lazy de Recharts.
- Accesibilidad final (WCAG AA), contraste, foco, navegación teclado.
- `FRONTEND_BACKEND_CONTRACT.md` final = snapshot del contrato implementado vs
  backlog, con el checklist completo verificado.

---

## 4. Orden de dependencias y decisiones de la fase actual

Ya que el backend **no tiene API**, la FASE 1 entrega (todo escrito en
`ARCHITECTURE.md`, `API_CONTRACT.md`, y este archivo):
la decisión de **construir el servidor Go aditivo** (comando `clipfactory server`)
para que la UI sea funcional de verdad. Sin esa pieza, cualquier UI quedaría
"mockeada". Por eso este plan asume:

1. Se amplía `cmd/clipfactory` con `server` (aditivo; no rompe worker/status/CLI).
2. Se implementa `internal/api` (mux REST + serving de archivos + SSE/streaming)
   reutilizando `internal/db` — ver `API_CONTRACT.md`.
3. El frontend se construye contra ese contrato.

Si el usuario prefiere **NO tocar el backend todavía**, se procede en modo
"frontend-first" contra el contrato con un **mock server de desarrollo** (Vite
middleware / msw) que implementa el contrato — y todos los read de métricas
muestran "requiere backend".

---

## 5. Riesgos y mitigación

| Riesgo | Impacto | Mitigación |
|--------|---------|------------|
| Backend sin API → UI sin datos | Alto | Servidor Go aditivo (`server`) reutilizando `db`; contrato cerrado primero. |
| Métricas/views inexistentes | Medio | Secciones marcadas 🧭 BACKLOG, UI honesta ("sin datos de métricas"). |
| Listas 10k clips | Alto | Paginación server-side + virtualización (TanStack Virtual). |
| Estado de jobs sin realtime | Bajo | TanStack Query + refetchInterval; progreso sin simular. |
| Accesibilidad descuidada | Medio | Checklist WCAG en `FRONTEND_BACKEND_CONTRACT.md`, a11y smoke por fase. |

---

## 6. Estructura final del documento

La implementación JAMÁS avanza a FASE 2 sin que la FASE 1 esté completa y
**los tres documentos** (ARCHITECTURE, API_CONTRACT, FRONTEND_PLAN) estén
revisados por el usuario. Cualquier cambio al contrato se registra en
`API_CONTRACT.md` ANTES de tocar código.
