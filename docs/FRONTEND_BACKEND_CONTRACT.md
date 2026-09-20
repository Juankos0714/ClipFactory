# Frontend ↔ Backend — Contrato implementado vs backlog (snapshot FASE 8)

> Snapshot final (FASE 8) sobre el working tree local. La fuente de verdad
> operacional sigue siendo `API_CONTRACT.md`; este archivo es el **inventario de
> cumplimiento**: qué registra el server aditivo (`internal/api/handlers.go`,
> `routes()`) y qué consume realmente el frontend (`web/src/lib/api/resources.ts`).
> No define endpoints nuevos; solo documenta el estado real.

---

## 1. Resumen

| Bloque | Estado |
|--------|--------|
| Endpoints 🆕 REQUIRED del contrato | **Implementado en backend + consumido en frontend** (ver §2). |
| Endpoints 🧭 BACKLOG | No expuestos por el backend; la UI no los simula (ver §3). |
| Auth (token Bearer) | Implementada en frontend (FASE 7), condicional al `CLIPFACTORY_API_TOKEN` del backend. |
| Polling vs realtime | Polling controlado (5–10s). SSE no existe → no hay realtime (BACKLOG). |

---

## 2. Contrato REQUIRED — cumplimiento por endpoint

| Endpoint | Backend (`routes()`) | Frontend (`resources.ts`) | Nota |
|----------|:---:|:---:|-------|
| `GET /api/health` | ✅ | ✅ | público siempre (auth lo exime) |
| `GET /api/system/overview` | ✅ | ✅ | = `clipfactory status` |
| `GET /api/system/config` | ✅ | ✅ | no sensible |
| `GET /api/workers` | ✅ | ✅ | `locked_by` derivado de jobs |
| `GET/POST/PATCH/DELETE /api/sources` | ✅ | ✅ | alta→201, duplicado→409 |
| `POST /api/sources/:id/discovery` | ✅ | ✅ | idempotente |
| `GET /api/sources/:id/clips` | ✅ | ✅ | paginado + filtros |
| `GET /api/source-clips/:id` | ✅ | ✅ | |
| `POST /api/source-clips/:id/download` | ✅ | ✅ | |
| `POST /api/source-clips/:id/skip` | ✅ | ✅ | |
| `GET /api/clips` (+ DTO agregado) | ✅ | ✅ | paginado server-side, filtros, sort, **vista por views es BACKLOG** |
| `GET /api/clips/:id` | ✅ | ✅ | árbol completo |
| `GET /api/clips/:id/video|thumbnail|processed` | ✅ | ✅ | byte-range |
| `POST /api/clips/:id/queue-for-download` | ✅ | ✅ | |
| `POST /api/clips/:id/queue-for-process` | ✅ | ✅ | |
| `POST /api/clips/:id/regenerate-thumbnail` | ✅ | ✅ | |
| `GET/POST /api/publications` | ✅ | ✅ | POST con UNIQUE→409 |
| `GET /api/publications/:id` | ✅ | ✅ | |
| `POST /api/publications/:id/retry|cancel` | ✅ | ✅ | |
| `GET /api/jobs` | ✅ | ✅ | default excluye `done` |
| `GET /api/jobs/:id` | ✅ | ✅ | |
| `GET /api/jobs/stats` | ✅ | ✅ | |
| `POST /api/jobs/:id/retry|cancel` | ✅ | ✅ | |
| `GET /api/logs` | ✅ | ✅ | sin página propia en el nav (queda accesible por API) |

**Ningún endpoint REQUIRED marcado como pendiente.** El marker
`FRONTEND REQUIRES BACKEND ENDPOINT` se mantiene solo para los errores 404/ausencia
de recurso (`errors.ts`) y para BACKLOG.

---

## 3. Backlog del backend — tratamiento en la UI (nunca simulado)

| Feature | Estado backend | En la UI |
|---------|----------------|----------|
| Métricas de views / ranking (`/api/analytics/*`, `sort=views`, `min_views`) | 🧭 | Analytics muestra gráficos **derivados de la DB** (jobs/publishings) con banner explícito; vista por views bloqueada. |
| Realtime / progreso de jobs (`/api/events` SSE) | 🧭 | Polling (5–10s). No hay websockets. |
| Automations (`/api/automations*`) | 🧭 | Constructor `CUÁNDO → ENTONCES` guarda **borradores locales** ("pendiente de backend"); no finge ejecución. |
| Revisión manual, priority, pause/resume de jobs | 🧭 | No expuestos en la UI. |
| `sources/sync-file`, `publications/:id/mark-published|stats` | 🧭 | No expuestos. |

---

## 4. Decisiones de FASE 8

- **Clips list (10k+): sin virtualización**. El backend ya pagina server-side
  (`page/pageSize` + envelope) con filtros — la lista real por pantalla es de 20
  filas. Virtualizar contradiría `AGENTS.md §4.3` (paginación server-side > virtualización).
- **Code splitting por ruta + lazy de Recharts**: cada página es un chunk propio;
  Recharts queda fuera del bundle principal y se carga solo en `/analytics`
  (ver `web/src/App.tsx`, `React.lazy`).
- **Testing**: unit/componentes con Vitest + Testing Library (jsdom, por archivo).
  Los 5 estados UI, Button, Dialog (a11y) y LoginPage cubiertos.
- **E2E Playwright**: **backlog/opcional** — requiere instalar navegadores y el
  backend (o el mock) en CI. No se instaló: los flujos críticos están cubiertos
  por tests de componentes + el contrato backend (`internal/api/api_test.go`).
- **A11y**: focus-visible global, skip-link, `prefers-reduced-motion`, focus-trap
  + restauración en Dialog, `color-scheme: dark` (UI dark-only).

---

## 5. Checklist «terminado» verificado (AGENTS.md §8)

Comandos ejecutados sobre `web/` en FASE 8:

```text
[✓] TypeScript sin errores                  tsc -b --noEmit   (0 errores)
[✓] ESLint sin errores                       eslint .          (0 errores; 1 warning preexistente react-refresh en AuthProvider)
[✓] Los 5 estados UI                        StateView (loading/error/empty/success) + idle; cubierto por tests
[✓] Responsive (320→1920) + a11y smoke       shell: sidebar desktop ↔ drawer móvil; skip-link; focus-visible; reduced-motion
[✓] Sin lógica de negocio duplicada         las queries SQL viven en el backend (DTOs); el frontend consume API
[✓] Sin secretos expuestos                  token de operador en sessionStorage; jamás en VITE_*
[✓] Endpoints REQUIRED del contrato         todos implementados (backend) y consumidos (frontend); BACKLOG bloqueado/no simulado
[✓] Sin dependencias injustificadas         set fijo de AGENTS §4.4 + Recharts (justificado en ARCHITECTURE.md §5.1)
[✓] Sin console.log de debug                no hay logging de debug en el bundle
```

Pendiente claramente identificado: E2E con navegador real (Playwright) y métricas de
views (requieren job de recolección en backend).