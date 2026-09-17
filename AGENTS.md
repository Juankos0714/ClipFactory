# AGENTS.md — Reglas maestras para trabajar en ClipFactory (con IA)

> Estas reglas gobiernan a CUALQUIER agente (Claude Code, Cursor, Codex, opencode…)
> que toque este repositorio. Son una adaptación de la filosofía **Ponytail**
> («lazy senior dev») al caso concreto de ClipFactory.
>
> La regla maestra, por encima de todas:
>
> > **No escribas código hasta demostrar que necesitas escribirlo.**
> > **No agregues nada hasta que exista evidencia de que hace falta.**
>
> Y la del repo:
>
> > Deletion over addition. Boring over clever.

---

## 1. Por qué existe este documento

ClipFactory es un backend Go **de producción, testeado y desplegado** que hoy es 100%
CLI (sin API REST). El trabajo actual es **agregar: (1) un server Go aditivo**
(`clipfactory server`, comando nuevo + `internal/api`, sin tocar el worker) y
**(2) un frontend React + Vite + TS** que consume ese contrato, desplegado en Vercel.

Todo el contexto técnico ya está documentado y es la fuente de verdad:

| Documento | Qué contiene |
|-----------|--------------|
| `docs/ARCHITECTURE.md` | Análisis del backend + arquitectura frontend propuesta |
| `docs/API_CONTRACT.md` | Contrato REST completo (markers `FRONTEND REQUIRES BACKEND ENDPOINT`) |
| `docs/FRONTEND_PLAN.md` | Plan de implementación por fases |
| `docs/MODELO_DE_DATOS.md` | Schema SQLite, máquinas de estado, DTOs |
| `docs/guia-de-uso.md` | Guía operativa del pipeline |

**ANTES de escribir cualquier endpoint o página: lee esos docs.** Son aditivos al
código, nunca decorativos.

---

## 2. La escalera de decisión (de Ponytail)

Cuando el frontend necesite algo, recorre esta escalera **en orden, y quédate en el
primer peldaño que funcione**. No te saltes peldaños hacia abajo por «hacer las
cosas bien por adelantado».

```
1. ¿Ya existe?            → reutilízalo (busca en el repo ANTES de escribir)
2. ¿Reutilizable?         → factoriza lo existente, no crees uno paralelo
3. ¿El estándar?          → usa JS/TS nativo / navegador / plataforma
4. ¿Dependencia ya instalada? → úsala antes de agregar una nueva
5. ¿Simplificar?          → ¿puedo resolverlo con MENOS código del que pensaba?
6. ¿Código mínimo?       → escribe SOLO lo necesario para el comportamiento
```

Regla concreta para **dependencias**: si una dependencia nueva no se puede justificar
con una frase corta («esto lo resuelve React Query, no necesito fetch manual»), **NO
se instala**. Y si se instala, se registra el `por qué` en `FRONTEND_PLAN.md` §5.1.

---

## 3. Escalera anti-sobreingeniería (cuándo es Herm... el backend VS el frontend)

Antes de implementar, pregunta siempre:

> ¿Esta responsabilidad pertenece al componente, al hook, al servicio, al dominio
> o al backend?

| Responsabilidad | Vive en |
|-----------------|---------|
| Lógica de negocio, persistencia, jobs, discovery, publicación, métricas definitivas | **Backend Go** (worker + `internal/db` + futura API). NUNCA en React. |
| Presentación, interacción, estado de UI, validación de entrada, cache de datos, navegación | **Frontend React** |
| Composición de datos que el backend ya sabe hacer (JOINs, conteos, DTOs agregados) | **Backend** vía endpoint (`GET /api/clips`, etc.) — NO reensamblar en el frontend |

**Regla de oro**: si el backend ya tiene una query/función equivalente, el frontend la
consume por API; NO la reimplementa. Si un endpoint no existe, el frontend lo marca
con el marker exacto `FRONTEND REQUIRES BACKEND ENDPOINT` (ver `API_CONTRACT.md`) y
la UI lo muestra **bloqueado**, nunca simulado.

---

## 4. Principios de implementación (adaptados de Ponytail)

### 4.1 YAGNI aplicado a ClipFactory

- **No** crees `ViralService`, `ViralRepository`, `ViralStrategy`, `ViralFactory`,
  `ViralProvider`… para un ranking. Si el ranking sale de `SELECT … ORDER BY
  views DESC` del backend, la UI muestra una lista ordenada **y ya está**.
- **No** crees abstracciones del tipo `BaseRepository`, `BaseService`, factories de
  HTTP, ni un sistema de «capas» genérico. Crea lo mínimo que cada feature necesite.
- **No** desarrolles el constructor visual de automatizaciones completo de golpe:
  primero un formulario/diálogo mínimo que capture `CUÁNDO → ENTONCES` y lo envíe al
  backend; el diagrama visual es un BACKLOG si el backend no lo ejecuta.

### 4.2 Reutilización real

- El frontend NO duplica queries SQL ni lógica de negocio.
- El frontend SÍ centraliza lo que se repite de verdad: un `apiClient` (axios),
  los componentes ui/ (`Button`, `Input`, `Dialog`…), los providers (query, auth,
  errores) y los DTOs de tipo. Esa es la única capa compartida que se permite.

### 4.3 «Boring over clever»

- Prefiere: RHF + Zod para formularios; TanStack Query para server-state;
  `refetchInterval` sencillo en vez de un sistema de realtime inventado; filas de
  tabla paginadas server-side en vez de virtualización si el backend ya pagina.
- Si una solución «inteligente» (websockets, virtualización, optimistic updates,
  mutation reactors) añade archivos y no resuelve un problema medible, no se hace.

### 4.4 Cada dependencia debe justificar su existencia

Dependencias aceptadas hoy: React, React Router, TanStack Query, React Hook Form,
Zod, Tailwind CSS, Lucide React, axios (o fetch abstraído), Recharts, Zustand
(solo si hay estado global de cliente genuino). Todo lo demás: justificarlo o no usarlo.

### 4.5 Los 5 estados UI

Todo request/fetch importante contempla: `idle | loading | success | empty | error`.
No se implementa solo el caso feliz. Estados vacíos y errores claros son obligatorios.

---

## 5. Reglas de código

| Regla | Detalle |
|-------|---------|
| TypeScript estricto | `strict`, `noImplicitAny`, `strictNullChecks`, `noUnusedLocals`, `noUnusedParameters` |
| `any` | Casi prohibido; usar `unknown` + validación (Zod). Nunca `as any` como parche |
| Sin secretos | Jamás `VITE_*` con secretos; los secretos viven en `credentials/` y el backend |
| Errores centralizados | Mapa `NetworkError / AuthenticationError / ValidationError / NotFoundError / ServerError` — nunca mostrar `AxiosError: ERR_NETWORK` crudo |
| A11y | HTML semántico; `<button>` no `<div onClick>`; labels; focus visible; WCAG AA |
| Responsive | Mobile-first; sidebar colapsable; tablas→cards en móvil; filtros en drawer |
| Sin `console.log` en prod | Logging controlado; limpiar debug antes de terminar |
| Idempotencia | Las acciones que encolan jobs deben ser idempotentes (el backend ya lo garantiza con UNIQUEs y `EnsureActiveJob`; el frontend no duplica encolados) |

---

## 6. Flujo de trabajo obligatorio

1. **Analiza** (lee `docs/` + código relevante) antes de tocar nada.
2. **Diseña el cambio mínimo** (¿endpoint nuevo? ¿componente? ¿solo styles?).
3. **Implementa** con el menor diff posible — nunca refactors de más.
4. **Verifica**: `tsc --noEmit`, `eslint`, `vitest run` (si aplica), build.
5. **Documenta** cualquier cambio de contrato en `API_CONTRACT.md` ANTES del código.

Si un cambio es grande (refactor, nueva feature), **primero pregúntate si se puede
hacer más pequeño**; si el backend no soporta algo, márcalo y NO lo simules.

---

## 7. Qué NO hacer jamás

- ❌ Inventar endpoints que el backend no puede atender.
- ❌ Simular métricas/estados que el backend no recolecta («views», «viralidad»).
- ❌ Reescribir el backend «para que sea más limpio» — es aditivo, no destructivo.
- ❌ Instalar librerías «por si acaso».
- ❌ Crear capas/abstracciones para satisfacer SOLID en vez de para resolver un problema.
- ❌ Dejar `any`/`as any`/`@ts-ignore` como muletas.
- ❌ Commitear secretos o `credentials/`.

---

## 8. Checklist de «terminado» (reducido a lo comprobable)

```text
[ ] TypeScript sin errores (tsc --noEmit)
[ ] ESLint sin errores
[ ] Los 5 estados UI implementados (idle/loading/success/empty/error)
[ ] Responsive (320→1920) + a11y smoke
[ ] Sin lógica de negocio duplicada del backend
[ ] Sin secretos expuestos
[ ] Endpoints REQUIRED del contrato marcados/bloqueados correctamente
[ ] Sin dependencias injustificadas
[ ] Sin console.log de debug
```
