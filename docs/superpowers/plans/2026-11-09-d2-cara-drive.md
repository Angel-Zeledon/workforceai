# D2. Cara Drive del departamento — plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** en cuentas de empresa, la pantalla de inicio de cada persona es el Drive de su departamento (`/d/<oficina>`): árbol, lista con autor y "Para revisar", panel del archivo con lo que espera decisión, y "Pedir algo sobre esta carpeta".

**Architecture:** el backend gana dos datos pequeños: el pedido de chat acepta una carpeta de trabajo (validada con la compuerta de D1) y las aprobaciones de `fs` llevan la ruta en su contexto. El frontend arma la pantalla con piezas existentes (`lib/drive.ts`, `FilePane` de `DriveView`, `PendingCard`, `officesApi`) más lógica pura probada (`lib/deptLogic.ts`), y cambia el menú lateral según el `audience` de la organización.

**Tech Stack:** Go; Next.js 15 / React 19 / TypeScript; i18n es/en/pt; `node --test`.

**Spec:** `docs/superpowers/specs/2026-11-09-empresa-departamentos-design.md` (fase D2, §4.3).

## Global Constraints

- Ruta `/d` (elige departamento) y `/d/<officeId>` (el Drive del departamento). Solo carpetas de oficinas donde la persona es miembro (`is_member`); General nunca tiene carpeta.
- Menú en cuentas `company`: grupo principal con **Departamento** (`/d`), **Pendientes**, **Agentes** (la pestaña de agentes existente), **Ajustes**; todo lo demás bajo un grupo "Más" plegado. Cuentas `solo` y `team`: menú igual que hoy.
- Ninguna pantalla de departamento muestra números de ahorro ni "equivale a N personas" (decisión del dueño).
- Estado por archivo en esta fase: solo **Para revisar** (hay una aprobación pendiente de `fs` sobre esa ruta). Filtros: **Todo**, **Para revisar**, **Cambiado hoy** (`updated_at` desde las 00:00 locales).
- Pedido sobre una carpeta: va al chat de un agente del departamento elegido por la persona (por defecto el primero de `office.agents`), con la carpeta como contexto; la carpeta pasa la compuerta `driveNeed(read)` de D1; fuera de `/Offices` no se acepta como carpeta de trabajo.
- Estilo: blanco, azul `#0A66C2`, IBM Plex Sans, esquinas ~10px; sin colores nuevos; patrón Drive (árbol | lista | panel).
- Copia en es/en/pt; `node scripts/check-i18n.mjs` en verde; cada `*.test.mjs` nuevo se añade a la lista de `test:unit` en `frontend/package.json`.
- Sin Docker: `go test ./...`, `npm run test:unit`, `npx tsc --noEmit -p .`; recorrido visual con `NEXT_PUBLIC_MOCK=true npx next dev -p 3100` y detener el proceso al terminar.

## Review Focus

1. **Persona que no es miembro de ningún departamento** entra a `/d`: ve un aviso claro (y, si es admin, un enlace para crear departamentos), nunca una pantalla vacía rota.
2. **`/d/<officeId>` de un departamento ajeno o privado:** el mismo aviso de "no disponible", sin listar nada (el backend ya da 404).
3. **Carpeta de trabajo maliciosa** en el pedido (`../`, otra oficina, `/offices/x` en minúsculas, archivo en vez de carpeta): rechazada igual que inexistente.
4. **Aprobación de `fs` sin ruta** (tipos viejos o acciones sin path) no rompe la pantalla ni marca archivos al azar.
5. **Muchos archivos (500+) en una carpeta:** la lista se pinta sin bloquear (sin cálculos O(n²) en el render).

---

### Task 1: Backend — carpeta de trabajo en el chat y ruta en las aprobaciones de fs

**Files:**
- Modify: `backend/internal/api/chat.go` (body `folder`), `backend/internal/application/chat_files.go` (contexto y bloque del prompt), `backend/internal/drive/gwtool.go` (ruta en el pendiente), y el punto donde `GatewayPending` se convierte en `Approval.Context` (búscalo en `internal/application`)
- Test: `backend/internal/api/chat_folder_api_test.go`, `backend/internal/application/chat_folder_test.go`, `backend/internal/drive/gwtool_path_test.go`

**Interfaces:**
- Produces: `POST /messages` acepta `"folder": "<ruta>"` opcional; `application.WithWorkFolder(ctx, path string) context.Context`; el prompt del turno incluye un bloque `[work folder]\n<ruta>\n[/work folder]` con una línea traducida "chat.workFolderNote" (es: "Trabaja dentro de esta carpeta: lee lo que necesites de ahí y guarda ahí lo que produzcas."); cada aprobación nacida de `fs` lleva `context.path` (ruta normalizada) y `context.action` (`write`, `move`, `delete`…).

- [ ] **Step 1: pruebas que fallan**
  - `TestChatFolderMustBeReadableOfficeFolder`: miembro de Legal con `folder: "/Offices/Legal/Casos"` → 2xx y el turno lleva la carpeta; `beto` (no miembro, Legal privada) → mismo error que una carpeta inexistente; `folder: "/Shared"` → 400 `folder must be a department folder`; `folder` que es un archivo → mismo error que inexistente; `"/Offices/Legal/../Contabilidad"` → normalizado y validado contra la ruta final.
  - `TestWorkFolderBlockInPrompt`: con `WithWorkFolder(ctx, "/Offices/Legal/Casos")` el texto enviado al agente contiene el bloque y la nota; sin carpeta, no hay bloque.
  - `TestFsApprovalCarriesPath`: una escritura de `fs` que pide aprobación crea una aprobación con `context["path"] == "/Offices/Legal/Casos/x.md"` y `context["action"] == "write"`.
- [ ] **Step 2:** `go test ./internal/api/ ./internal/application/ ./internal/drive/ -run 'ChatFolder|WorkFolder|FsApprovalCarriesPath'` → FAIL.
- [ ] **Step 3:** implementar. La validación de la carpeta usa `driveNeed(r, folder, drive.AccessRead)` y `FolderByPath`; la ruta se limpia con `drive.CleanPath`. El bloque se arma junto al de adjuntos (`chat_files.go`).
- [ ] **Step 4:** `go test ./internal/api/ ./internal/application/ ./internal/drive/...` → PASS; `go vet` de esos paquetes.
- [ ] **Step 5:** commit `feat(chat): a request can work inside a department folder; fs approvals carry their path`.

### Task 2: Menú según el tipo de cuenta

**Files:**
- Modify: `frontend/src/lib/navBridge.ts` (`navGroups`, `sectionOf`), `frontend/src/components/nav/AppShell.tsx` (grupo "Más" plegable), `frontend/src/lib/teamsApi.ts` o un archivo nuevo `frontend/src/lib/audience.ts` (hook)
- Modify: `frontend/src/lib/i18n/{es,en,pt}.json`
- Test: `frontend/src/lib/navBridge.test.mjs` (crear si no existe; añadir a `test:unit`)

**Interfaces:**
- Produces: `useAudience(): "solo" | "team" | "company" | null` (carga `teamsApi.welcome()` una vez por sesión y la guarda en un store; `null` mientras carga o si falla → menú de hoy); `navGroups({ shopAvailable, company }: { shopAvailable: boolean; company: boolean })`; sección nueva `{ id: "department", href: "/d", label: "nav.department", icon: <ícono de carpeta existente> }`; `sectionOf("/d")` y `sectionOf("/d/x")` → `"department"`.

- [ ] **Step 1: pruebas que fallan** en `navBridge.test.mjs`: `navGroups({shopAvailable:false, company:true})` tiene como primer grupo `["department","pending","agents","settings"]` (ids reales de las secciones existentes de Pendientes, Agentes y Ajustes — usa los que ya estén en `SECTIONS`) y un grupo `"more"` con el resto sin repetir ninguno; `navGroups({shopAvailable:true, company:false})` es idéntico al resultado de hoy; `sectionOf("/d/dep_1") === "department"`.
- [ ] **Step 2:** `npm run test:unit` → FAIL.
- [ ] **Step 3:** implementar; en `AppShell` el grupo "more" se pinta plegado con un botón "Más" (`nav.group.more`: "Más" / "More" / "Mais"), recordando abierto/cerrado en `localStorage` (con try/catch). `nav.department`: "Departamento" / "Department" / "Departamento".
- [ ] **Step 4:** `npm run test:unit`, `npx tsc --noEmit -p .`, `node scripts/check-i18n.mjs` → verdes.
- [ ] **Step 5:** commit `feat(nav): company accounts get a short menu led by their department`.

### Task 3: Lógica pura del Drive del departamento

**Files:**
- Create: `frontend/src/lib/deptLogic.ts`
- Test: `frontend/src/lib/deptLogic.test.mjs` (añadir a `test:unit`)

**Interfaces:**
- Consumes: tipos `DriveFile`, `DriveFolder` (o el nombre real) y `Principal` de `lib/drive.ts`; `Approval` de `lib/types.ts`; `OfficeView` de `lib/officesLogic.ts`.
- Produces:
  - `myDepartments(offices: OfficeView[]): OfficeView[]` → solo `is_member && !general && folder_path`, en el orden recibido.
  - `pendingByPath(approvals: Approval[], under: string): Map<string, Approval[]>` → aprobaciones pendientes con `context.path` bajo `under` (sin distinguir mayúsculas), agrupadas por ruta exacta; ignora las que no tienen `context.path`.
  - `filterRows<T extends { path: string; updated_at?: string }>(rows: T[], filter: "all" | "review" | "today", pending: Map<string, unknown>, now: Date): T[]`.
  - `proposedRows(pending: Map<string, Approval[]>, folder: string, existing: Set<string>): { path: string; name: string; approval: Approval }[]` → archivos propuestos por un agente (aprobación de escritura) en esa carpeta exacta que todavía no existen.
  - `defaultAgent(office: OfficeView): string | undefined` → primer id de `office.agents`.
  - `crumbs(path: string, root: string): { name: string; path: string }[]` → migas desde la raíz del departamento.

- [ ] **Step 1: pruebas que fallan** (una por función, con: General excluido y no miembro excluido; aprobación sin `context.path` ignorada; mayúsculas distintas agrupan igual; "today" usa 00:00 local de `now`; "review" solo con pendiente; propuesto en subcarpeta NO aparece en la carpeta padre; `crumbs("/Offices/Legal/Casos/2026", "/Offices/Legal")` → `[Legal, Casos, 2026]`; 1000 filas filtradas en < 50 ms).
- [ ] **Step 2:** `npm run test:unit` → FAIL.
- [ ] **Step 3:** implementar (Map por ruta en minúsculas; nada O(n²)).
- [ ] **Step 4:** `npm run test:unit` → PASS.
- [ ] **Step 5:** commit `feat(dept): pure logic for the department drive`.

### Task 4: La pantalla del departamento

**Files:**
- Create: `frontend/src/app/(app)/d/page.tsx`, `frontend/src/app/(app)/d/[office]/page.tsx`, `frontend/src/components/dept/DepartmentDrive.tsx`, `frontend/src/components/dept/DeptTree.tsx`, `frontend/src/components/dept/DeptComposer.tsx`
- Modify: `frontend/src/components/drive/DriveView.tsx` (exportar `FilePane` y `Author` sin cambiar su comportamiento), `frontend/src/lib/api.ts` (`postMessage` acepta `folder?: string`), `frontend/src/lib/drive.ts` y `frontend/src/lib/officesApi.ts` (datos demo: Contabilidad con su plantilla completa de la Tarea 1 de D1, archivos de ejemplo, un agente y una aprobación pendiente de escritura con `context.path`), `frontend/src/lib/i18n/{es,en,pt}.json`

**Interfaces:**
- Consumes: Task 1 (`folder` en `POST /messages`, `context.path`), Task 2 (`useAudience`, sección `department`), Task 3 (todas las funciones de `deptLogic`), `PendingCard`/`ApprovalPending`, `DetailPanel`/`useIsPhone`.

Comportamiento:
- `/d`: si `myDepartments` tiene uno o más → redirige a `/d/<primero>`; si ninguno → `EmptyState` "Todavía no estás en un departamento" (+ enlace "Crear departamentos" a la pantalla de oficinas si `me.admin`).
- `/d/<office>`: si no es de `myDepartments` → el mismo `EmptyState` con "Este departamento no está disponible para ti". Si lo es: encabezado con nombre del departamento, migas (`crumbs`), selector de departamento si hay más de uno, buscador (usa `drive.search` limitado a la carpeta del departamento).
- Izquierda (`DeptTree`): carpetas del departamento, se cargan al expandir (`drive.list`), la actual resaltada en azul suave.
- Centro: filtros Todo / Para revisar (con contador) / Cambiado hoy; tabla Nombre · Último cambio (`Author` + `timeAgo`) · Estado (chip "Para revisar" si hay pendiente); filas propuestas (`proposedRows`) arriba con chip "Propuesto"; clic en fila → panel derecho; menú "⋯" por fila: Abrir, Pedir a un agente…, Descargar.
- Derecha: en escritorio columna fija; en teléfono `DetailPanel` como hoja inferior. Muestra, si hay pendientes para ese archivo, cada uno con `ApprovalPending` (aprobar/rechazar como en Pendientes) y debajo `FilePane` (vista previa y versiones).
- Abajo (`DeptComposer`): selector de agente (agentes de la oficina, por defecto `defaultAgent`) + campo "Pedir algo sobre esta carpeta…" + botón Pedir → `api.postMessage(<conversación del agente>, texto, adjuntos, undefined, carpetaActual)`; "Pedir a un agente…" de una fila rellena el campo y adjunta ese archivo. Tras enviar, aviso "Enviado a <agente>" con enlace a la conversación.
- Copia (es): "Pedir algo sobre esta carpeta…", "Pedir", "Para revisar", "Propuesto", "Cambiado hoy", "Todo", "Abrir", "Pedir a un agente…", "Descargar", "Enviado a {agent}", "Todavía no estás en un departamento", "Este departamento no está disponible para ti", "Crear departamentos"; en/pt equivalentes.

- [ ] **Step 1:** prueba de componente mínima no disponible (no hay runner de React): en su lugar, añadir a `deptLogic.test.mjs` cualquier decisión nueva que tome la pantalla, y verificar la pantalla en el navegador (Step 3).
- [ ] **Step 2:** implementar.
- [ ] **Step 3:** `npm run test:unit`, `npx tsc --noEmit -p .`, `node scripts/check-i18n.mjs` → verdes; con `NEXT_PUBLIC_MOCK=true npx next dev -p 3100` abrir `/d` en headless 1366×900 y 390×844, capturas en `docs/screenshots/d2-2026-11-09/` (`01-departamento.png`, `02-para-revisar.png`, `03-telefono.png`); revisar las capturas; detener el servidor y el `node` hijo del puerto 3100.
- [ ] **Step 4:** commit `feat(dept): the department's Drive as the home of company accounts`.

### Task 5: Documentación y guía

**Files:**
- Modify: `docs/architecture/equipos-oficinas.md` (D2 hecho), `landing/guide-src/files-and-chats.json` o una guía nueva `landing/guide-src/departments.json` si el esquema lo permite (es/en: "El Drive de tu departamento": dónde está, qué es "Para revisar", cómo pedir algo sobre una carpeta), regenerar con `node landing/scripts/build-guide.mjs`.

- [ ] **Step 1:** escribir docs y guía (lenguaje simple, "departamento", sin jerga).
- [ ] **Step 2:** regenerar la landing; `cd backend && go test ./...` y en frontend `npm run test:unit` una última vez.
- [ ] **Step 3:** commit `docs: the department Drive (D2) in the team docs and the user guide`.

## Decisiones tomadas durante la ejecución (2026-11-09)

- Ruling: statuses Borrador/Enviado from spec deferred; only Para revisar (+ Propuesto) — no data source for draft/sent yet — cost if wrong: owner may want those chips; add with tags later.
- Ruling: "Compartido conmigo" omitted until D4 (no fake placeholder) — cost: the tree lacks that section for now.
- Task 2: implemented 5617456. Ruling: "Agentes" = section org (/org, Agentes y oficinas), not office (3D) — spec lists "Agentes del departamento" — cost if wrong: one id swap.
- Ruling: audience null/failed → today's menu; only MOCK demo is explicitly company — protects the open demo and network failures from a menu switch — cost if wrong: none.
- Ruling: accept the residual one-frame flash on full page load — AppShell renders behind AuthGate (client session) and the stored audience applies in the first effect; removing it needs server-known audience (cookie) — cost if wrong: a single-frame menu switch on hard reload for company accounts.
- Task 3: fix round 1/5 (1 addressed; commits 2b9510f..0ecb5a0). Ruling: controller verified the 2-line diff directly instead of a scoped re-review (CREATES set + test) — cost if wrong: none beyond the final review catching it.
- Task 5: fix round 1/5 (6 addressed; commit a3a785b). Ruling: controller verified the doc-only fix directly (menu entry present in generated /guia/, API doc codes correct) instead of a scoped re-review — cost if wrong: copy nit in the guide.
- Ruling: company accounts land on /d once per app entry (sessionStorage mark), "/" keeps the 3D office reachable from "Más" — cost if wrong: one redirect to undo.
- Ruling: add "Del agente, sin revisar": a file whose last version was written by an agent inside /Offices and not yet marked reviewed shows "Para revisar"; "Marcar como revisado" sets tag "revisado" (existing tags API, needs edit); any new agent version clears the tag server-side — this makes the department screen the supervisor's review queue the spec intends without forcing approve_each — cost if wrong: one extra chip/button, easy to remove.
- Ruling: folder only for agent:<id> conversations (400 otherwise).
- Ruling: fix-now minors: stale stored audience cleared when welcome is empty; review counter counts only what the list can show; person author names; single chip on proposed rows; tree ~240px and no wrap in "Último cambio".

Resultado: 5 tareas + revisión final (1 crítico: marcas perdidas al recargar; inicio en /d; "Para revisar" incluye trabajo de agentes sin revisar con "Marcar como revisado"). Integrado en main.
