# D1. Carpetas de departamento — plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** cada departamento (oficina) tiene su carpeta `/Offices/<oficina>/` con plantilla por profesión, y ninguna persona ni agente de fuera puede listar, leer, buscar ni escribir ahí.

**Architecture:** el drive gana una raíz `Offices` cuyas carpetas de primer nivel llevan `RefID` = id de la oficina. El acceso de una persona a esa raíz lo decide un adaptador `drive.PeopleAccess` (implementado en `cmd/server` sobre `offices.Model`); la API del drive lo aplica en cada ruta. Para agentes, `AgentFS.baseZone` clasifica `/Offices/<su oficina>` como zona propia y lo demás como fuera, y `zoneAll` recorta con el acceso de la persona por la que trabaja (`requesterFrom`).

**Tech Stack:** Go (chi, pgx, almacenes en memoria para pruebas), Next.js 15 / React 19 / TypeScript, i18n JSON es/en/pt.

**Spec:** `docs/superpowers/specs/2026-11-09-empresa-departamentos-design.md` (fase D1, §4.1 y §4.4).

## Global Constraints

- Nombre de la raíz en el drive: `Offices` (constante `drive.RootOffices`); en pantalla se muestra "Oficinas" / "Offices" / "Escritórios".
- Niveles: `none` · `read` · `edit` (las constantes `drive.AccessNone/AccessRead/AccessEdit` que ya existen).
- Acceso de una persona a `/Offices/<o>`: miembro → `edit`; no miembro de oficina abierta → `read`; no miembro de oficina privada → `none`, **también si es admin** (spec §4.1, decisión 2 del equipo). Fuera de `/Offices` nada cambia: siguen mandando los roles.
- `none` responde **404** (no se revela que existe); `read` sin permiso de escritura responde **403**.
- Sin `PeopleAccess` configurado (demo sin cuentas, pruebas antiguas) el drive se comporta exactamente como hoy.
- Agente: `edit` sin aprobación dentro de la carpeta de su oficina; nada en otras oficinas; nunca más que la persona por la que trabaja.
- Departamentos creados en cuentas `company` nacen **privados**; en `team` siguen abiertos.
- Toda copia visible en es/en/pt (`frontend/src/lib/i18n/*.json`, `node scripts/check-i18n.mjs` en verde).
- Pruebas sin Docker: `go test ./...` en `backend/`, `npm run test:unit` y `npx tsc --noEmit -p .` en `frontend/`.

## Review Focus

1. **Mover o copiar fuera de una oficina:** mover un archivo de `/Offices/Legal` a `/Shared` exige `edit` en el origen Y en el destino; si no, 403/404 y nada se mueve.
2. **Búsquedas y vistas indirectas:** `/drive/search`, `/drive/passages`, `/drive/trash` y `fs.search` / `fs.recent` / memoria no devuelven nada de una oficina sin acceso, ni siquiera el nombre del archivo.
3. **Archivo por id:** `GET /drive/files/{id}` (y content, text, preview, versions, diff) de un archivo en una oficina ajena da 404 aunque se conozca el id.
4. **Agente compartido con otra persona:** un agente de Contabilidad usado por alguien que no es miembro de Contabilidad no puede leer `/Offices/Contabilidad` en esa tarea.
5. **Oficina que pasa de abierta a privada:** quien leía deja de ver en la siguiente petición (nada cacheado en el cliente lo mantiene).

---

### Task 1: Raíz `Offices` y carpeta de oficina con plantilla

**Files:**
- Modify: `backend/internal/drive/types.go` (constante y `Roots`)
- Modify: `backend/internal/drive/service.go` (nuevo método, junto a `EnsureOwnedFolder`)
- Modify: `backend/internal/roles/professions.go` (carpetas por profesión + profesión `education`)
- Test: `backend/internal/drive/offices_test.go`, `backend/internal/roles/professions_test.go`

**Interfaces:**
- Produces: `drive.RootOffices = "Offices"`; `func (s *Service) EnsureOfficeFolder(ctx context.Context, officeID, displayName string, subfolders []string) (Folder, error)` (idempotente; crea `/Offices/<nombre seguro>` con `RefID=officeID` y cada subcarpeta si falta, como `System()`); `func (s *Service) OfficeFolder(ctx context.Context, officeID string) (Folder, error)` (`domain.ErrNotFound` si no existe); `func (p Profession) Folders(loc string) []string`; profesión `"education"`.

- [ ] **Step 1: pruebas que fallan**

```go
func TestEnsureOfficeFolderCreatesTemplateOnce(t *testing.T) {
	// EnsureOfficeFolder(ctx, "dep_1", "Contabilidad", []string{"Clientes", "Impuestos"}) dos veces:
	// la segunda no falla ni duplica; List("/Offices/Contabilidad") tiene exactamente Clientes e Impuestos;
	// OfficeFolder(ctx, "dep_1").Path == "/Offices/Contabilidad" y .RefID == "dep_1".
}
func TestOfficeFolderUnknownIsNotFound(t *testing.T) { /* errors.Is(err, domain.ErrNotFound) */ }
```

```go
func TestProfessionFolders(t *testing.T) {
	// accounting es: Clientes, Proveedores, Bancos y conciliaciones, Impuestos, Cierres mensuales, Políticas
	// legal es: Casos, Contratos, Plantillas de escritos, Plazos, Políticas
	// hr es: Colaboradores, Reclutamiento, Planillas, Políticas, Capacitación
	// education es: Cursos, Exámenes, Rúbricas, Calificaciones
	// cualquier otra: Documentos, Políticas
	// en: Clients, Suppliers, Banks and reconciliations, Taxes, Monthly closings, Policies | Cases, Contracts,
	//     Filing templates, Deadlines, Policies | People, Recruiting, Payroll, Policies, Training |
	//     Courses, Exams, Rubrics, Grades | Documents, Policies
	// pt: Clientes, Fornecedores, Bancos e conciliações, Impostos, Fechamentos mensais, Políticas | Casos, Contratos,
	//     Modelos de petições, Prazos, Políticas | Colaboradores, Recrutamento, Folha de pagamento, Políticas,
	//     Treinamento | Cursos, Provas, Rubricas, Notas | Documentos, Políticas
	// locale desconocido usa es.
}
```

Actualizar `TestProfessionsAreTheEightOfTheDesign` a nueve: `education` (Roles nil, equipo es "Educación", en "Education", pt "Educação"), insertada antes de `other`.

- [ ] **Step 2:** `go test ./internal/drive/ ./internal/roles/` → FAIL (símbolos no definidos).
- [ ] **Step 3:** implementar. `EnsureOfficeFolder` reutiliza `EnsureOwnedFolder(ctx, RootOffices, officeID, displayName)` y `Mkdir` con `System()` por subcarpeta (ignorar `ErrConflict`). Añadir `"offices"` donde se mapea `RootKind` si hay un switch por raíz.
- [ ] **Step 4:** `go test ./internal/drive/ ./internal/roles/` → PASS. También `go test ./internal/drive/drivetest/...` y `go vet ./internal/drive/`.
- [ ] **Step 5:** commit `feat(drive): /Offices root with per-profession folder templates; education profession`.

### Task 2: Acceso de una persona a una carpeta

**Files:**
- Create: `backend/internal/drive/people.go`
- Modify: `backend/internal/drive/service.go` (campo en `Config`)
- Create: `backend/cmd/server/drive_people.go` (adaptador sobre `offices`)
- Test: `backend/internal/drive/people_test.go`, `backend/cmd/server/drive_people_test.go`

**Interfaces:**
- Consumes: `OfficeFolder`, `RootOffices` (Task 1); `offices.Model.Office/IsMember` (existentes).
- Produces:
  - `type PeopleAccess interface { OfficeAccess(ctx context.Context, org, userID, officeID string) (string, error) }` y `Config.People PeopleAccess`.
  - `func (s *Service) PersonAccess(ctx context.Context, userID, path string) string`: fuera de `/Offices/<x>` → `AccessEdit`; `/Offices` mismo → `AccessRead`; dentro → `OfficeAccess` de la carpeta de primer nivel (por su `RefID`); `People == nil` → `AccessEdit`; error u oficina desconocida → `AccessNone` (falla cerrado).
  - `func (s *Service) VisibleTo(ctx context.Context, userID string, paths []string) []bool` (una consulta de oficinas por llamada; lo usan listados y búsquedas).
  - Adaptador `officePeople{svc *offices.Service}` con la regla de Global Constraints.

- [ ] **Step 1: pruebas que fallan** (`people_test.go` con un `PeopleAccess` falso):
  `TestPersonAccessOutsideOfficesIsEdit` (`/Shared/a.md` → edit), `TestPersonAccessOfficeRoot` (`/Offices` → read), `TestPersonAccessDelegatesByRefID` (`/Offices/Legal/Casos/x.md` pide `OfficeAccess(..., "dep_legal")`), `TestPersonAccessFailsClosed` (error → none), `TestPersonAccessWithoutAdapterIsEdit`.
  `drive_people_test.go`: miembro → edit; no miembro abierta → read; no miembro privada → none; admin no miembro privada → none; admin no miembro abierta → read.
- [ ] **Step 2:** `go test ./internal/drive/ ./cmd/server/ -run 'PersonAccess|OfficePeople'` → FAIL.
- [ ] **Step 3:** implementar y conectar `Config.People` en la construcción del drive en `cmd/server` solo cuando hay `offices` (auth activada).
- [ ] **Step 4:** las mismas pruebas → PASS.
- [ ] **Step 5:** commit `feat(drive): a person's access to office folders (member edit, open read, private none)`.

### Task 3: La API del drive aplica el acceso

**Files:**
- Modify: `backend/internal/api/drive.go`, `backend/internal/api/drive_upload.go`, `backend/internal/api/docsearch.go`, `backend/internal/api/folder_access.go`
- Test: `backend/internal/api/drive_offices_api_test.go`

**Interfaces:**
- Consumes: `PersonAccess`, `VisibleTo` (Task 2); `application.PersonFrom(ctx)` para la persona.
- Produces: `func (s *server) driveNeed(r *http.Request, path, level string) error` → `nil`, `domain.ErrNotFound` (nivel `none`) o `domain.ErrForbidden` (tiene `read` y pidió `edit`).

Reglas por ruta: `list` → `read` sobre la carpeta y filtrar hijos con `VisibleTo`; `search`, `trash`, `passages`, `folder-access` → filtrar resultados; `files/{id}`, `content`, `text`, `preview`, `versions`, `diff` → `read` sobre la ruta del archivo; `folders` (mkdir), `upload` (PUT y POST, cada archivo del multipart), `files/{id}/content`, `restore`, `tags`, `versions/{n}/restore`, `delete` → `edit`; `move` (archivo y carpeta) → `edit` en origen y destino; `grants` (POST) → quien da a un agente acceso a una ruta necesita `edit` ahí.

- [ ] **Step 1: pruebas que fallan** con servidor de prueba, oficina privada "Legal" (miembro `ana`) y abierta "Ventas" (miembro `ana`), usuario `beto` sin oficinas, ambos admin:
  - `TestOfficePrivateHiddenFromNonMember`: `beto` GET `/drive/list?path=/Offices` no ve "Legal" y sí "Ventas"; `GET /drive/list?path=/Offices/Legal` → 404; `GET /drive/files/{id}` de un archivo de Legal → 404; `/drive/search?q=<palabra del archivo>` → 0 resultados.
  - `TestOfficeOpenIsReadOnlyForNonMember`: `beto` lee un archivo de Ventas (200) y `PUT /drive/upload?path=/Offices/Ventas/x.txt` → 403.
  - `TestMoveOutOfOfficeNeedsBothEnds`: `ana` mueve de `/Offices/Legal` a `/Shared` (200); `beto` mueve de `/Shared` a `/Offices/Legal` → 404 y el archivo sigue en `/Shared`.
  - `TestMemberWorksNormally`: `ana` crea carpeta, sube, edita, borra y restaura en `/Offices/Legal`.
  - `TestBecomingPrivateTakesEffectNextRequest`: Ventas pasa a privada; la siguiente lectura de `beto` → 404.
- [ ] **Step 2:** `go test ./internal/api/ -run 'Office.*(Hidden|ReadOnly|Move|Member|Private)'` → FAIL.
- [ ] **Step 3:** implementar `driveNeed` y aplicarlo según la tabla; los filtros usan `VisibleTo` en bloque.
- [ ] **Step 4:** `go test ./internal/api/` completo → PASS (las pruebas viejas del drive no cambian: sin `/Offices` todo sigue igual).
- [ ] **Step 5:** commit `feat(api): the drive API enforces office folder access on every route`.

### Task 4: Los agentes respetan las carpetas de oficina

**Files:**
- Modify: `backend/internal/drive/agent.go` (`AgentFS`, `baseZone`, `zoneAll`, `readableRoots`, `canRead`, `autoWrite`)
- Modify: `backend/cmd/server/` donde se construye `drive.AgentFS` (inyectar la oficina del agente)
- Test: `backend/internal/drive/agent_offices_test.go`

**Interfaces:**
- Consumes: `OfficeFolder`, `PersonAccess` (Tasks 1-2); `requesterFrom(ctx)` (recent.go); `offices.Model.Agent(id).OfficeID`.
- Produces: `type AgentOffices interface { AgentOffice(ctx context.Context, org, agentID string) (string, error) }` y campo `AgentFS.Offices AgentOffices`; constante `ZoneOffice = "office"` (lee y escribe sin aprobación); `readableRoots` incluye `/Offices/<carpeta de su oficina>` cuando existe. Agente de "General" o sin `Offices` → sin carpeta de oficina.

Regla en `zoneAll`, después de lo actual: si la ruta está bajo `/Offices` y `requesterFrom(ctx) != ""`, con `pa := Svc.PersonAccess(ctx, requester, path)`: `none` → `ZoneDenied`; `read` y la zona era de escritura → `ZoneGranted` (solo lectura). Una regla de `drive_grants` puede dar a un agente acceso de lectura a otra oficina solo si quien la crea tiene ese acceso (Task 3 lo valida en la API); aun así el recorte por persona aplica.

- [ ] **Step 1: pruebas que fallan** (entorno como `newAgentEnv`, oficinas "Contabilidad" (agente `acct`) y "Legal" (agente `legal`), archivos en ambas):
  - `TestAgentWritesOwnOfficeWithoutApproval`: `Plan(acct, "write", /Offices/Contabilidad/Clientes/a.md)` sin `Approval` ni `Deny`.
  - `TestAgentCannotReadOtherOffice`: `read`, `list` y `search` de `acct` sobre Legal → denegado / sin resultados.
  - `TestAgentLimitedByRequester`: con `WithRequester(ctx, "beto")` y `PeopleAccess` que da `none` a beto en Contabilidad, `acct` no puede leer su propia oficina; con `read`, leer sí y escribir pide aprobación.
  - `TestReadableRootsIncludeOwnOffice`: `ReadableRoots(acct)` contiene `/Offices/Contabilidad` y no `/Offices/Legal`.
- [ ] **Step 2:** `go test ./internal/drive/ -run 'Agent.*Office|Requester|ReadableRootsInclude'` → FAIL.
- [ ] **Step 3:** implementar; conectar `AgentOffices` en `cmd/server` con `offices.Model(ctx, org).Agent(id).OfficeID` (ignorar `GeneralID`).
- [ ] **Step 4:** `go test ./internal/drive/... ./cmd/server/` → PASS.
- [ ] **Step 5:** commit `feat(drive): agents work inside their office folder and never beyond the person they work for`.

### Task 5: Crear departamentos con su carpeta

**Files:**
- Modify: `backend/internal/api/teams.go` (`createTeam`), `backend/internal/api/offices.go` (`enableOffice`), `backend/internal/api/router.go` (JSON de oficina)
- Test: `backend/internal/api/teams_offices_api_test.go`

**Interfaces:**
- Consumes: `EnsureOfficeFolder`, `Profession.Folders` (Task 1); `s.Auth.OrgSetup(ctx, org)`, `auth.AudienceCompany`; `s.Offices.Enable`.
- Produces: el JSON de cada oficina (`writeOffice` y la lista `GET /offices`) gana `folder_path` (`""` si no tiene).

- [ ] **Step 1: pruebas que fallan:**
  - `TestCompanyTeamIsPrivateOfficeWithTemplate`: org con audience `company`, `POST /teams {"profession":"accounting"}` → existe la oficina, `visibility == "private"`, y `/Offices/Contabilidad` tiene las 6 carpetas de Task 1; la respuesta trae `folder_path`.
  - `TestTeamAudienceStaysPlainDepartment`: audience `team` → comportamiento actual (sin oficina nueva).
  - `TestEnableOfficeCreatesGenericFolder`: `POST /offices` sobre un departamento → `/Offices/<nombre>` con Documentos y Políticas.
- [ ] **Step 2:** `go test ./internal/api/ -run 'CompanyTeam|TeamAudience|EnableOfficeCreates'` → FAIL.
- [ ] **Step 3:** implementar: en `createTeam`, si la org es `company` y `s.Offices != nil`, `Enable(..., d.ID, "private")` con un `Viewer` admin del sistema y luego `EnsureOfficeFolder(d.ID, d.Name, prof.Folders(loc))`; en `enableOffice`, `EnsureOfficeFolder` con `Folders` de `ProfessionOther`. Fallar la carpeta no deshace la oficina: se registra en el log y `folder_path` sale vacío.
- [ ] **Step 4:** `go test ./internal/api/` → PASS.
- [ ] **Step 5:** commit `feat(teams): company departments are born private with their profession's folders`.

### Task 6: Pantalla de Archivos y adjuntos del chat

**Files:**
- Modify: `frontend/src/components/drive/DriveView.tsx`, `frontend/src/lib/drive.ts` (demo), `frontend/src/lib/officesApi.ts`, `frontend/src/lib/officesLogic.ts`, `frontend/src/components/chat/attachments.tsx` y su llamador
- Modify: `frontend/src/lib/i18n/{es,en,pt}.json`
- Test: `frontend/src/lib/officeFoldersLogic.test.mjs` (+ `frontend/src/lib/officeFoldersLogic.ts`)

**Interfaces:**
- Consumes: `folder_path` de Task 5.
- Produces: `rootLabel(name: string, t): string` (para `Offices` → `t("drive.root.offices")`); `attachDest(officeFolder: string | undefined, today: string): string` → `${officeFolder}/Adjuntos/${today}` o `/Shared/Chat/${today}` si no hay carpeta (la palabra "Adjuntos" sale de i18n `drive.attachFolder`: "Adjuntos" / "Attachments" / "Anexos").

- [ ] **Step 1: pruebas que fallan** en `officeFoldersLogic.test.mjs`: `attachDest(undefined, "2026-11-09") === "/Shared/Chat/2026-11-09"`; `attachDest("/Offices/Legal", "2026-11-09")` termina en `/Adjuntos/2026-11-09` con la palabra pasada; `rootLabel("Offices", t)` usa la clave i18n y `rootLabel("Shared", t)` lo que ya se usa hoy.
- [ ] **Step 2:** `npm run test:unit` → FAIL.
- [ ] **Step 3:** implementar. En la pantalla de Archivos la raíz `Offices` aparece con su etiqueta e ícono de candado en las carpetas de oficinas privadas (el dato de visibilidad sale de `officesApi`). El composer del chat usa como destino por defecto la carpeta de la oficina del agente de la conversación si tiene `folder_path` (y la persona sigue pudiendo cambiarlo). El modo demo (`lib/drive.ts`) añade `/Offices/Contabilidad/Clientes/…` con dos archivos de ejemplo para que la pantalla se vea poblada.
- [ ] **Step 4:** `npm run test:unit`, `npx tsc --noEmit -p .`, `node scripts/check-i18n.mjs` → verdes.
- [ ] **Step 5:** commit `feat(files): office folders in Files and chat attachments saved in the agent's office`.

### Task 7: Verificar sin Docker, documentar y cerrar

**Files:**
- Modify: `docs/architecture/drive.md` (tabla Layout: fila `/Offices/<office>`), `docs/architecture/equipos-oficinas.md` (§11: E3 parcial, qué se hizo), `landing/guide-src/files-and-chats.json` (una sección "Carpetas de cada departamento", es/en) y regenerar con `node landing/scripts/build-guide.mjs`

- [ ] **Step 1:** `cd backend && go test ./... && go vet ./...` → todo verde.
- [ ] **Step 2:** recorrido manual sin Docker: compilar `go build -o $TEMP/wf-server.exe ./cmd/server`, correrlo con `AUTH_ENABLED=false SIMULATION=true PORT=8080 ALLOWED_ORIGINS=http://localhost:3000`, y el frontend con `NEXT_PUBLIC_MOCK=true npx next dev -p 3100` para la pantalla de Archivos; captura en `docs/screenshots/d1-2026-11-09/`. Detener ambos procesos al terminar (y el `node` hijo si queda en el puerto).
- [ ] **Step 3:** actualizar los docs y la guía; regenerar la landing.
- [ ] **Step 4:** commit `docs: office folders (D1) in the drive and team docs and the user guide`.
