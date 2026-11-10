# D3. Sellar el resto del departamento — plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** lo que hacen los agentes de un departamento privado (pedidos, tareas, informes, chats, actividad, memoria, eventos en vivo, aprobaciones) solo lo ven sus miembros; habilidades y conexiones pueden ser de un departamento; las aprobaciones las decide el responsable del departamento.

**Architecture:** una sola regla en `offices.Model` decide si una persona ve el trabajo de un agente (`CanSeeWork`). La API la aplica en cada listado y detalle; el registrador de eventos la usa para fijar la audiencia; la memoria guarda con alcance de oficina; habilidades y conexiones ganan `office_id`; las aprobaciones usan `IsLead` para decidir. Sin Docker; migraciones nuevas numeradas tras la más alta.

**Tech Stack:** Go (chi, pgx, almacenes en memoria), Next.js 15 / TS, i18n es/en/pt.

**Spec:** `docs/superpowers/specs/2026-11-09-empresa-departamentos-design.md` (fase D3, §4.2).

## Global Constraints

- **Regla de trabajo visible** (`CanSeeWork(v Viewer, agentID string) bool`): el agente es de General o de una oficina abierta → `true`; de una oficina privada → solo miembros de esa oficina (`IsMember`), **también si la persona es admin** (spec §4.1, decisión 2). Agente desconocido → General (regla existente de `Model.Agent`). Sin servicio de oficinas (demo sin cuentas) → todo visible como hoy.
- Lo oculto responde igual que lo inexistente (404 en detalle; ausente en listados).
- **Aprobaciones:** decide quien ya podía (rol `approvals:decide` + reglas actuales) **y además**: si el agente es de una oficina con responsables (no General), solo sus responsables (`IsLead`) deciden; si no tiene responsables, los admin. Quien no ve el trabajo (`CanSeeWork`) no ve la aprobación.
- **Memoria:** documentos de tareas e informes de un agente de oficina (no General) se guardan con `scope = 'office'` y `office_id`; los recuerda cualquier agente de esa oficina; los de alcance `org` siguen para General.
- **Habilidades y conexiones:** `office_id` opcional (`""` = de toda la empresa). Una de oficina solo se asigna/otorga a agentes de esa oficina, y el gateway rechaza su uso por otro agente. La crean/gestionan los admin o el responsable de esa oficina.
- Copia nueva en es/en/pt; `check-i18n` en verde. Nunca números de ahorro.
- Sin Docker: `go test ./...`, `go vet ./...`; frontend `npm run test:unit`, `npx tsc --noEmit -p .`.

## Review Focus

1. **Admin no miembro** de un departamento privado: no ve sus pedidos, tareas, informes, actividad, chats, memoria del agente, aprobaciones ni eventos en vivo, por ninguna ruta.
2. **Evento en vivo** de un agente privado (mensaje, tarea terminada, informe, aprobación pedida): no llega por websocket a no miembros ni queda en el registro común.
3. **Aprobación** de un agente de Legal (con responsable): un admin no responsable recibe 403 al decidir; el responsable decide; sin responsables, decide el admin.
4. **Habilidad o conexión de Contabilidad** asignada por API a un agente de Legal → rechazada; uso por el gateway desde un agente de otra oficina → rechazado.
5. **Datos viejos** (memoria `org`, habilidades y conexiones sin oficina): siguen funcionando igual.

---

### Task 1: La regla `CanSeeWork` y los listados de trabajo

**Files:** `backend/internal/offices/model.go` (+ test), `backend/internal/api/` (rutas de `router.go:283-305`: `/tasks`, `/tasks/{id}`, `/requests`, `/requests/{id}`, `/requests/{id}/audit`, `/reports`, `/activity`, `/agents/{id}/memory`, `/agents/{id}/skills`, `/agents/{id}/connections`, `AgentDetail` en `application/queries.go:155-181`, `chat_history.go:228-237` hilos, `chat.go:1146-1175` mensajes), `postMessage` (`router.go:571`) con `checkAgentUse`.
**Interfaces:** Produces `func (m *Model) CanSeeWork(v Viewer, agentID string) bool`; en api `func (s *server) seesWork(r *http.Request, agentID string) bool` (true sin oficinas).

- [ ] **Step 1: pruebas que fallan** — `TestCanSeeWork` (General/abierta/privada miembro/privada admin no miembro/desconocido); `TestPrivateWorkHiddenFromNonMembers` (API: Legal privada con agente `legal`; `beto` admin no miembro: `/requests` sin los pedidos de `legal`, `/requests/{id}` 404, `/tasks` sin sus tareas, `/reports`, `/activity`, `/agents/legal/memory` 404, hilos de chat sin el de `legal`); `TestConversationPostChecksAgentUse` (`POST /conversations/{id}/messages` a una conversación de `legal` por `beto` → igual que inexistente).
- [ ] **Step 2:** FAIL. **Step 3:** implementar; los listados filtran con el modelo cargado una vez por petición. **Step 4:** `go test ./internal/offices/ ./internal/api/ ./internal/application/` PASS.
- [ ] **Step 5:** commit `feat(offices): private departments' work is visible only to their members`.

### Task 2: Eventos en vivo con audiencia de oficina

**Files:** `backend/internal/application/recorder.go:32-62`, cableado en `backend/cmd/server`, tests.
**Interfaces:** Consumes `CanSeeWork`/`Office(id).Members`. Produces un `AudienceFor func(ctx, org string, ev Event) (users []string, scoped bool)` instalado en el registrador: si el evento lleva un agente (campo de agente del evento, o el de la tarea/pedido/aprobación que lleva) de una oficina privada, `Audience` = miembros de esa oficina y el evento no va al registro común (como los de chat privado, `chat.go:551`).

- [ ] **Step 1:** `TestPrivateAgentEventsReachOnlyMembers` para `EvMessageSent`, `EvTaskCompleted`, `EvReportCreated`, `EvApprovalRequest`, `EvRequestReceived` (un suscriptor no miembro no los recibe; un miembro sí; un evento de agente de General llega a todos). **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** PASS. **Step 5:** commit `feat(events): live events of a private department reach only its members`.

### Task 3: Memoria con alcance de oficina

**Files:** migración nueva (`memory_docs.office_id TEXT NOT NULL DEFAULT ''`, CHECK de scope con `'office'`), `backend/internal/memory/{types.go,service.go,memstore.go}`, `backend/internal/infrastructure/postgres/memory.go:81,162`, cableado de la oficina del agente.
**Interfaces:** `memory.ScopeOffice = "office"`; `Doc.OfficeID`; `SearchParams.OfficeID`/`VecQuery.OfficeID`; el servicio recibe `AgentOffice func(ctx, org, agentID) string` ("" para General).

- [ ] **Step 1:** `TestTaskMemoryStaysInOffice` (resultado de tarea de `acct` → lo recuerda otro agente de Contabilidad, no uno de Legal ni de General); `TestGeneralAgentsKeepOrgMemory`; `TestOldOrgDocsStillVisible`. **Step 2:** FAIL. **Step 3:** implementar (memstore y SQL con `OR (scope='office' AND office_id=$n)`). **Step 4:** PASS. **Step 5:** commit `feat(memory): what a department's agents learn stays in the department`.

### Task 4: Habilidades de departamento

**Files:** migración (`skills.office_id`), `backend/internal/skills/{service.go,assign.go}`, `backend/internal/api/skills.go`, frontend `components/skills/SkillsView.tsx` (selector "Para: Toda la empresa / <departamento>" al crear; etiqueta del departamento en la lista), i18n.
**Interfaces:** `Skill.OfficeID`; `Assign` rechaza (`domain.ErrInvalid`, "skill belongs to another department") si `skill.OfficeID != "" && agentOffice != skill.OfficeID`; `List` para una persona = empresa + oficinas que puede ver con `CanSeeWork`-equivalente por oficina (privada → miembro); crear/editar una de oficina: admin o responsable de esa oficina.

- [ ] **Step 1:** pruebas de servicio y API (asignar a agente de otra oficina → 400; listar como no miembro → no aparece la de Legal privada; responsable crea la de su oficina; miembro no responsable no). **Step 2:** FAIL. **Step 3:** implementar backend y pantalla. **Step 4:** Go y frontend verdes. **Step 5:** commit `feat(skills): skills can belong to a department`.

### Task 5: Conexiones de departamento

**Files:** migración (`connections.office_id`), `backend/internal/connections/{service.go,grants.go}`, `backend/internal/gateway/gateway.go:220-258` (`resolve`), `plan.go:55`, `sources.go:21`, `backend/internal/api/connections.go`, frontend `components/connections/ConnectionsView.tsx`/`GrantMatrix.tsx`, i18n.
**Interfaces:** `Connection.OfficeID`; `PutGrant` rechaza agente de otra oficina; `resolve` rechaza (como si no hubiera grant) si la conexión es de una oficina y el agente no es de ella; listar para no miembros oculta las de oficinas privadas; gestionar una de oficina: admin o su responsable.

- [ ] **Step 1:** pruebas (otorgar a otra oficina → 400; gateway con grant viejo inconsistente → rechazo; listar como no miembro → oculta). **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** verdes. **Step 5:** commit `feat(connections): connections can belong to a department`.

### Task 6: Aprobaciones al responsable del departamento

**Files:** `backend/internal/application/{approvals.go:248-290,approval_inbox.go:47-80}`, `backend/internal/api/router.go:587-599` (`GET /approvals`), `backend/internal/notify/service.go:168-190` (avisos), cableado de oficinas, frontend (Pendientes: texto "Decide: <responsable>" cuando la persona no puede decidir), i18n.
**Interfaces:** `application.ApproverPolicy interface { CanDecide(ctx, org, userID string, a domain.Approval) (bool, error); Sees(ctx, org, userID string, a domain.Approval) bool; Approvers(ctx, org string, a domain.Approval) []string }` instalada en el orquestador; `Decide` y `CanDecideApproval` la consultan además de las reglas actuales; `GET /approvals` e inbox filtran con `Sees`; el aviso por correo va también a los responsables (si el servicio de avisos tiene correos de personas; si no, solo el evento con audiencia de la Tarea 2).

- [ ] **Step 1:** `TestLeadDecidesDepartmentApprovals` (responsable sí; admin no responsable 403; sin responsables, admin sí; General: reglas de hoy); `TestApprovalsListHidesPrivateDepartments`; inbox "decidable" del responsable incluye las de su oficina. **Step 2:** FAIL. **Step 3:** implementar. **Step 4:** verdes. **Step 5:** commit `feat(approvals): a department's approvals go to its responsible`.

### Task 7: Documentación, guía y verificación

**Files:** `docs/architecture/equipos-oficinas.md` (D3 hecho, qué falta), `docs/architecture/08-api.md`, `landing/guide-src/departments.json` (sección "Lo que queda dentro del departamento": pedidos, chats, memoria, habilidades, conexiones, aprobaciones del responsable), regenerar la landing.

- [ ] **Step 1:** docs y guía (lenguaje simple). **Step 2:** `go test ./...`, `go vet ./...`, frontend tests/tsc/i18n. **Step 3:** commit `docs: sealed departments (D3) in the docs and the guide`.
