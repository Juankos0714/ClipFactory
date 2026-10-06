# Plan: 4 agentes de QA/seguridad para ClipFactory

## Decisiones tomadas (confirmadas)

| Decisión | Valor |
|---|---|
| Tipo | **Subagentes** (invocables con `@`), no skills |
| Ubicación | **Proyecto**: `.opencode/agent/<nombre>.md` (versionado, compartido por pull) |
| Origen del contenido | Los 4 cuerpos que redactaste, usados como base literal |
| Herramientas | Instalar `hey` + `govulncheck` en WSL vía `go install` (sin sudo) |

**Resolución de la contradicción**: en la primera respuesta entregaste los 4 archivos en
formato `SKILL.md` para `.opencode/skills/<n>/SKILL.md`, pero en la 2ª y 3ª elegiste
subagentes en `.opencode/agent/`. Sigo lo explícito: **4 subagentes**, con tu contenido
convertido de frontmatter de skill a frontmatter de agente. Si además querés que sean
cargables con la herramienta `skill`, son 4 copias más (no lo hago salvo que lo pidas).

---

## 1. Estado verificado (hechos, no supuestos)

- **No existe `.opencode/`** en el repo, ni `opencode.json`. El global
  (`~/.config/opencode/opencode.jsonc`) tiene solo `$schema`. No hay agents/skills
  instalados en ningún ámbito, ni skills externas (`~/.claude/skills`, `~/.agents/skills`).
- **Toolchain**: Go 1.24.13 en `~/sdk/go` (WSL, instalado en esta sesión, sin root).
  Docker daemon **caído**. No hay `ab`, `wrk`, `hey`, `govulncheck` en Windows ni WSL.
  `curl` sí. `rg` **no** está instalado → los agentes usarán `grep`/`Select-String`.
- **Suite actual**: Go 11 paquetes `ok` (vet/gofmt/build limpios); web 108 Vitest +
  71 Playwright; CI con jobs `test` (vet/fmt/build/test/race), `docker`, `web`.
- **Superficie de ataque real** (relevada, para calibrar el audit):
  - `internal/db/list.go:105` — `"UPDATE sources SET " + strings.Join(sets, ...)`: los
    fragments son **literales** de un whitelist y los valores van con `?`. **No es SQLi**
    → archetypical falso positivo.
  - `internal/adapter/ffmpeg/ffmpeg.go:126,213,256` — `exec.CommandContext(ctx, path, args...)`
    con `args []string`, sin shell. **No hay command injection**; los paths vienen de la
    DB/config. Otro falso positivo frecuente.
  - `internal/api/handlers.go` + streaming byte-range con validación anti-path-traversal
    (superficie **a favor**, ya endurecida).
  - Frontend: 3 sitios renderizan `href={pub.external_url}` **sin allowlist de protocolo**
    (`publishing/publications-page.tsx:173`, `clips/clip-detail-page.tsx:132`,
    `production/components/publications-card.tsx:64`) → candidato real a `javascript:` URL.
  - Auth bearer **opcional**: sin `CLIPFACTORY_API_TOKEN` la API es pública (decisión
    documentada, pero el audit debe verificarla, no re-litigarla).
  - `credentials/` y `config/sources.yaml` están fuera de git; `sources.yaml` == `.example`.
- **Realidad del load test**: la API lee de la **misma SQLite con `SetMaxOpenConns(1)`**,
  así que la latencia mide serialización de SQLite, no el capa HTTP. Y los `POST`
  **encolan jobs reales** (`EnsureActiveJob`) → golpearlos inunda la DB de jobs.

## 2. Archivos a crear (4)

Todos en `.opencode/agent/`, con frontmatter de agente (sin `name:`; el nombre sale del
archivo), sin `model:` pinneado (hereda el del config global → no se rompe si cambia).

| Archivo | `mode` | `permission.edit` | `permission.bash` | Por qué |
|---|---|---|---|---|
| `test-suite-architect.md` | subagent | **deny** | allow | Los permisos imponen su propia regla "no modificar código para que pase una prueba": no puede editar. |
| `test-driven-dev.md` | subagent | allow | allow | Único que escribe código (RED→GREEN→REFACTOR). |
| `http-load-profiler.md` | subagent | **ask** | allow | Mide y escribe un baseline; `ask` para no crear archivos sin permiso. |
| `code-vuln-audit.md` | subagent | **deny** | ask | Regla del draft: "no modificar código automáticamente"; `bash: ask` para `govulncheck`/`npm audit`. |

Cada cuerpo = **el texto que redactaste** (objetivo, proceso, tablas, severidades, formato
de reporte) + una sección nueva **"Calibración ClipFactory"**.

### 2.1 `test-suite-architect.md`
Su cuerpo tal cual (objetivo, principios Google Testing, proceso de 5 pasos, plantilla de
caso con ID/precondiciones/datos/pasos/esperado/prioridad/tipo, ejecución en 4andover,
reporte, severidad P0-P3, "no modificar código para hacer pasar una prueba").
Agregar:
- Comandos **reales** del repo, no genéricos: `npm run typecheck|lint|test|test:e2e`
  (en `web/`), `go vet/gofmt/build/test ./...` vía `~/sdk/go/bin/go`, `scripts/test.ps1`.
- **Ancla de requisitos**: todo caso de prueba nuevo se deriva de `docs/API_CONTRACT.md`,
  `docs/MODELO_DE_DATOS.md` o del backlog real `docs/hardening-checklist.md` §20
  (429 Twitch, timeout de upload, ffmpeg fallando, crash antes del UPDATE, disco lleno).
  Sin fuente documental → el caso se marca `origen: hipótesis` y no se reporta como gap.
- **Anti-falso-positivo**: antes de decir "X no está cubierto", ejecutar la suite y contar;
  afirmar cobertura solo con salida real. Prohibido declarar "falta test" sobre algo que
  `grep` muestre ya cubierto.
- El dev-mock de `web/` es stateful: pedirle que elija filas que otra suite no mutea.

### 2.2 `test-driven-dev.md`
Su cuerpo tal cual (ciclo RED/GREEN/REFACTOR, antes-de-empezar, nuevas features, bugs,
integración, reglas, reporte final).
Agregar:
- Cycles **verificables**: nombrar el comando del test y pegar su salida en cada fase
  (RED debe fallar *por la razón correcta* — si falla por error de compilación no cuenta).
- Stack real: `testing` + `httptest` (Go), Vitest + Testing Library + Playwright (web);
  proibido introducir otro framework (ya hay dos, ambos en `AGENTS.md` §4.4).
- Bug → primero test que reproduza, y `git stash`-free: no tocar el código hasta ver el RED.

### 2.3 `http-load-profiler.md`
Su cuerpo tal cual (objetivo, checklist previo, escalonado 1→500, métricas rps/p50/p90/
p95/p99/errores, tabla de resultado, punto de inflexión, seguridad).
**Desviación documentada**: el draft prefiere `wrk`/`ab`; ninguno se puede instalar sin
sudo en esta máquina (`ab`=apache2-utils, `wrk`=C+build-essential, y no hay gcc).
Se usa **`hey`** (`~/go/bin/hey`), que da las mismas métricas con reporte por percentile.
Agregar:
- **Guardarraíles duros** (por los POST encolan jobs): solo `GET` de lectura
  (`/api/health`, `/api/system/overview`, `/api/clips`, `/api/publications`, `/api/jobs/stats`).
  Prohibido `POST`/`DELETE` sin autorización explícita punto por punto.
- **Caveat obligatorio en el reporte**: `SetMaxOpenConns(1)` → las cifras miden la
  serialización de SQLite; p99 alto no es "el HTTP server es malo". Declarar baseline, no veredicto.
- Warm-up antes de medir; 3 repeticiones por nivel; reportar el **mejor** y el **mediano**
  (el peor es ruido de la VM); reportar si el server corría en la misma máquina (sí) → disclaimer.

### 2.4 `code-vuln-audit.md`
Su cuerpo tal cual (13 áreas, secretos redactados, SQLi/XSS/cmd-injection, dependencias,
priorización CRITICAL/HIGH/MEDIUM/LOW, proceso de 10 pasos, formato de reporte, reglas).
Agregar:
- **Gate de confirmación por hallazgo** (esto es lo que mate los falsos positivos):
  1. `archivo:línea` + **flujo de datos** desde el input no confiable hasta el sink;
  2. verificar el sink leyendo el código (¿parametrizado? ¿whitelist? ¿shell?);
  3. **repro** (curl o test) o etiqueta explícita `NO VERIFICADO`;
  4. severidad ajustada al contexto real (API localhost, auth opcional, 1 operador);
  5. si el fragmento interpolado es literal → **no es hallazgo**, se anota como benigno.
- **Catálogo de benignos ya revisados** (con file:line, para no re-litigar):
  `internal/db/list.go:105` (SET por whitelist + `?`), `internal/adapter/ffmpeg/ffmpeg.go:126,213,256`
  (`exec.CommandContext` con `args []string`, sin shell), streaming con anti-path-traversal,
  `credentials/` y `config/sources.yaml` fuera de git.
- **Apalancamiento de herramientas**: `govulncheck ./...` (CVE de Go, a instalar),
  `npm audit` en `web/` (npm), `go vet`, y **revisión manual dirigida** con
  `grep`/`Select-String` (no hay `rg` instalado). Secretos: buscar en `.gitignore`,
  `.env*`, `config/`, y verificar que lo no commiteado siga ignorado.
- **Prioridad para este repo**: (a) `href={pub.external_url}` sin allowlist de protocolo
  (3 archivos), (b) auth opcional → API pública si falta el token, (c) CORS configurable,
  (d) dependencias Go/npm con CVE.

## 3. Herramientas (WSL, sin sudo)

```bash
~/sdk/go/bin/go install github.com/rakyll/hey@latest              # -> ~/go/bin/hey
~/sdk/go/bin/go install golang.org/x/vuln/cmd/govulncheck@latest  # -> ~/go/bin/govulncheck
```
Fallback si falla la red: `~/sdk/go/bin/go run github.com/rakyll/hey@latest ...`.
Los cuerpos de los agentes usan **rutas absolutas** (`~/go/bin/hey`) para no depender del PATH.

## 4. Uso y arranque

- Invocación: `@test-suite-architect`, `@test-driven-dev`, `@http-load-profiler`, `@code-vuln-audit`
  (o vía `task` con `subagent_type`).
- **Hay que cerrar y reabrir opencode**: la config se carga al inicio, no hace hot-reload.
- `.opencode/agent/` se commitea (lo hacés vos); nada de esto toca `web/package.json`
  ni agrega dependencias al repo.

## 5. Orden de ejecución

1. `go install` de `hey` + `govulncheck` en WSL; verificar `--version` de ambos.
2. Crear los 4 `.opencode/agent/*.md` con el contenido de §2.
3. Reiniciar opencode; confirmar que los 4 aparecen en el autocomplete de `@`.
4. Smoke test de permisos: pedirle a `@code-vuln-audit` un hallazgo sobre
   `internal/db/list.go:105` y verificar que lo descarte como benigno (si lo reporta
   como SQLi, el prompt necesita ajuste → ese es el criterio de aceptación).
5. Corrida real de los 4 en este repo, en este orden: `code-vuln-audit` (rápido, sin
   side effects) → `test-suite-architect` (diseño) → `test-driven-dev` (el único que
   escribe) → `http-load-profiler` (el más caro: levanta el server Go con
   `CLIPFACTORY_API_ADDR=:8080`, necesita DB; Docker está caído así que va con el binario
   local o con Go de WSL).

## 6. Criterios de aceptación

- [ ] Los 4 aparecen en `@` tras reiniciar; ninguno rompe el arranque de opencode.
- [ ] `@code-vuln-audit` **no** reporta SQLi en `list.go:105` ni command injection en
      `ffmpeg.go`, y **sí** levanta el `href` sin allowlist de protocolo.
- [ ] `@test-suite-architect` no declara "falta cobertura" sin haber corrido la suite y
      sin citar `docs/hardening-checklist.md` §20.
- [ ] `@http-load-profiler` no ejecuta ningún `POST` y su reporte incluye el disclaimer
      de SQLite `SetMaxOpenConns(1)`.
- [ ] `@test-driven-dev` muestra RED con fallo por la razón correcta antes de editar.
- [ ] Cero dependencias nuevas en `web/package.json`; cero secretos en los archivos.

## 7. Lo que NO voy a hacer

- No agregar `ab`/`wrk` (requieren sudo/compilador) ni cambiar a `ab`/`wrk` en el draft.
- No meter `hey`/`govulncheck` como dependencias del repo (viven en el home del usuario).
- No tocar `AGENTS.md`, ni el código de la app, salvo que pidas un fix puntual.
- No commitear nada (lo hacés vos).