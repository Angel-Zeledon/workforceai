# Archivos y accesos por agente: plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. Pasos con casillas (`- [ ]`).

**Goal:** accesos Ninguno/Leer/Editar por carpeta para cada agente (con pantalla), un agente que encuentra solo los
archivos que la persona menciona (archivos recientes), y facturas → estado de resultados calculado en código.

**Architecture:** los accesos reutilizan `drive_grants` (columna `mode` pasa de `read` a `none|read|edit`); la regla más
específica gana sobre las zonas por defecto (mesa y proyectos = Editar, `/Shared` = Leer). Las búsquedas reciben el
alcance como `Under` con exclusiones `!/ruta` aplicadas en SQL antes de ordenar. Dos acciones nuevas del tool `fs`:
`recent` (archivos recientes por persona) e `income_statement` (filas de facturas → Excel calculado por
`internal/invoicepnl`, puro y determinista).

**Tech Stack:** Go (chi, pgx, excelize), Python (prompt del runtime), Next.js 15 + React 19.

**Spec:** `docs/superpowers/specs/2026-11-03-agentes-ubican-archivos-y-accesos-design.md`

## Global Constraints

- Textos en es/en/pt (`frontend/src/lib/i18n/*.json`), estilo blanco + azul, IBM Plex Sans, esquinas ~10px.
- Migración aditiva siguiente libre (810 si sigue libre) con su `down` en `backend/migrations/down/`.
- Sin llamadas reales a proveedores; sin tocar `components/office/**`, `.env`, cobros ni landing.
- Archivos del otro agente (`agent-runtime/app/{crewai_engine,main,models,security,simulation}.py`, `domain.go`,
  orquestador, `projects`, `notify`, `components/projects/**`, `ApprovalsTray.tsx`): solo cambios mínimos y releídos.
- "Recientes" = 14 días por defecto (máx. 90). Skill nueva en es/en, roles `accounting` y `finance_treasury`, ≤ 8 por rol.

## Review Focus

- Un "Ninguno" dentro de una carpeta legible (p. ej. `/Shared/RRHH`) no debe filtrarse por búsqueda por palabras, por
  significado, memoria ni recientes → prueba `TestAccessNoneHidesSubtreeEverywhere`.
- Un agente con "Editar" en una carpeta ajena escribe sin aprobación; con "Leer" sigue pidiendo aprobación →
  `TestAccessEditWritesFreely`.
- Una tarea de un cliente (B12) nunca gana acceso por un "Editar" o "Leer" → `TestAccessDoesNotBreakCustomerIsolation`.
- Facturas en otra moneda sin tipo de cambio, duplicadas o ilegibles nunca desaparecen ni se suman → `TestComputeReview*`.
- Quien no es admin ni responsable del equipo del agente no cambia accesos (403) → `TestFolderAccessAuthorization`.

---

### Task 1: Niveles de acceso en el drive (Parte 3, backend)

**Files:** Create `backend/internal/drive/access.go`, `backend/internal/drive/access_test.go`,
`backend/internal/infrastructure/postgres/migrations/810_agent_folder_access.sql`, `backend/migrations/down/810_agent_folder_access_down.sql`.
Modify `drive/agent.go` (zone, readableRoots, runList), `drive/service.go` (AddGrant acepta mode), `drive/memstore.go`,
`docindex/memstore.go`, `postgres/drive.go`, `postgres/docindex.go` (exclusiones `!`).

**Interfaces (Produces):**
- `const AccessNone, AccessRead, AccessEdit = "none","read","edit"`; `ZoneDenied = "denied"`, `ZoneGrantedEdit = "granted_edit"`.
- `func InScope(under []string, path string) bool` (nil = todo; entradas `!p` excluyen el subárbol).
- `func (s *Service) SetAgentAccess(ctx, by Principal, agentID, path, level string) (prev string, err error)` (`level` = none|read|edit|default; audita `drive.access.changed`).
- `func (a *AgentFS) AgentAccess(ctx, agentID string) ([]AccessEntry, error)`; `func (a *AgentFS) AccessAt(ctx, agentID, path string) (level, source string, err error)`.
- `type AccessEntry struct{ Path, Level, Source string; GrantID string }` (Source = default|manual).

- [ ] Pruebas: Ninguno oculta en list/read/search/ReadableRoots; Editar escribe sin aprobación; Leer pide aprobación; la
  mesa propia no se puede cambiar; B12 intacto; SetAgentAccess audita y reemplaza.
- [ ] Implementar, `go test ./internal/drive/ ./internal/docindex/`.

### Task 2: API y quién puede cambiar (Parte 3)

**Files:** Create `backend/internal/api/folder_access.go`, `backend/internal/api/folder_access_test.go`. Modify
`api/router.go` (Deps.AgentFolders, mount), `cmd/server/main.go` (una línea).

- `GET /agents/{id}/folder-access` → `{entries[], can_manage}`; `PUT /agents/{id}/folder-access {path, level}`;
  `GET /drive/folder-access?path=` → `{agents:[{agent_id,name,level,source}], can_manage}`.
- Pueden cambiar: `files:grant` (admin/owner) o el jefe (`Head`) del departamento del agente (orgchart). 403 si no.

### Task 3: Pantallas (Parte 3)

**Files:** Create `frontend/src/lib/folderAccess.ts`, `frontend/src/lib/folderAccessLogic.ts`,
`frontend/src/lib/folderAccessLogic.test.mjs`, `frontend/src/components/agent/FolderAccessSection.tsx`,
`frontend/src/components/drive/FolderAgentsPanel.tsx`. Modify `AgentPanel.tsx`, `DriveView.tsx`, i18n, `package.json`.

### Task 4: Archivos recientes y el agente se ubica solo (Parte 1)

**Files:** Create `backend/internal/drive/recent.go`, `recent_test.go`, `agent-runtime/app/file_locate.py`,
`agent-runtime/tests/test_file_locate.py`. Modify `drive/store.go` (`RecentFiles`), memstore, `postgres/drive.go`,
`drive/gwtool.go` (persona en ctx, SideEffect, Specs), `agent.go` (switch), `security.py` (una línea).

- `fs.recent {days?=14 (1..90), by? me|anyone, kind? pdf|xlsx|docx|image|text|any, path?, limit?=50 (≤200)}`.
- Respuesta: archivos agrupados por carpeta con fecha, y "sin acceso: N archivos en /X" (sin nombres).
- Prompt: orden adjuntos → recientes → búsqueda; confirmar solo si hay duda (help_request si `help_allowed`, si no
  pregunta normal); sin acceso → decirlo y ofrecer "Darle acceso"/"Pedir acceso".

### Task 5: Facturas → estado de resultados (Parte 2)

**Files:** Create `backend/internal/invoicepnl/{pnl.go,pnl_test.go}`, `backend/internal/drive/pnl.go`, `pnl_test.go`,
`backend/internal/skills/builtin/{es,en}/income-statement-from-invoices/SKILL.md`. Modify roles `accounting.json`,
`finance_treasury.json`, `skills/service_test.go`, `file_locate.py` (reglas de facturas).

- `invoicepnl.Compute(in Input) Result` en centavos (int64); duplicados (número+emisor), cuadre subtotal+impuestos=total
  (tolerancia 0,01 o 0,05 %), monedas sin tipo de cambio fuera del total, ilegibles/escaneadas a Revisar, clasificación
  por la empresa (emisor = ingreso, receptor = gasto) o dudosa.
- `fs.income_statement {path .xlsx, company, invoices[], currency?, rates?, locale?}` → Excel Detalle / Estado de
  resultados / Revisar, mismas zonas que write_document; resumen: leídas, resultado, a revisar.

### Task 6: Documentación y verificación

`archivos-agentes.md`, `agent-search.md`, `08-api.md`; `go build/vet/test`, pytest, tsc, test:unit, check-i18n.
