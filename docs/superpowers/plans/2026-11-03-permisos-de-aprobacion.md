# Permisos de aprobación — plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. Pasos con checkbox (`- [ ]`).

**Goal:** que el dueño decida de antemano qué necesita su OK por agente y por tipo de acción, con tres niveles en
palabras simples, excepciones por categoría, piso de seguridad y valores por defecto de la empresa, y que cada
aprobación diga qué regla la pidió.

**Architecture:** los permisos de cada agente se guardan como datos en `org_settings.rules.permissions` (JSONB, sin
migración) y `PolicyService` los traduce, para la llamada concreta, a *grants* y *limits* del motor existente
(`internal/policy`): no hay un segundo motor. El nivel sigue siendo `agents.autonomy` (única fuente de verdad) y solo
lo escribe `trust.Service` (nuevo `ChooseLevel`, respeta techo y controles). Un agente sin entrada de permisos se
comporta exactamente como hoy.

**Tech Stack:** Go (backend), Next.js 15 + React 19 (frontend), `node --test`.

**Spec:** `docs/superpowers/specs/2026-11-03-permisos-de-aprobacion-design.md`

## Global Constraints

- Sin segundo motor: todo se expresa con `policy.Grant`/`policy.Limit` evaluados por `policy.Engine`.
- Empresas existentes no cambian: sin entrada en `permissions.agents[id]` el agente decide como hoy.
- "Solo lo importante" con reglas de fábrica: agentes nuevos (evento `agent.created`) y quien lo elija.
- Piso de seguridad: `payments`, `contracts`, `delete` preguntan siempre (también en "Trabaja solo"); solo el
  dueño (`owner`) lo quita por agente y categoría con `understood: true`; auditado y visible.
- Presupuesto, límites de ritmo, riesgo `high` del runtime, monto de la organización, doble aprobación, `deny` y la
  aprobación propia del Gateway (correo) siguen activos siempre.
- Cambian niveles y excepciones: `admin`/`owner` o el responsable del equipo del agente (jefe del departamento del
  puesto o `reports_to`). Un agente nunca. Todo cambio en auditoría.
- Textos es/en/pt; sin servidores, Docker ni navegador; pruebas solo del paquete tocado.

## Review Focus

- Un agente `rules` antiguo sin entrada: misma decisión que hoy en todas las combinaciones (test de equivalencia).
- `draft_contract` es interno (borrador), `send_contract` es contratos: la clasificación lee primero lo de solo lectura.
- Quitar el piso no salta el riesgo `high` del runtime ni el monto de la organización (test).
- Excepción en un agente sin nivel elegido: `409 choose_level_first` (no activa reglas de fábrica por sorpresa).
- Acción por conexión (drive) con veredicto `perm.*` de aprobación: el Gateway la retiene (no se ejecuta sola).

---

### Task 1: clasificación y reglas de fábrica (`internal/policy/permissions.go`)

**Files:** Create `backend/internal/policy/permissions.go`, `backend/internal/policy/permissions_test.go`.

**Produces:**
- `ActionKind(tool, action string, args map[string]any) string` → `internal|messages|publish|payments|delete|contracts|spend|other`.
- `type Permissions{DefaultLevel string; Agents map[string]AgentPermissions}`, `AgentPermissions{Exceptions []Exception; FloorLifts []FloorLift; Since time.Time; UpdatedBy string; UpdatedAt time.Time}`,
  `Exception{Category, Mode ("ask"|"never"|"ask_if"), If *AskIf{PerDayOver int; AmountOver *float64; NewRecipient bool}, CreatedBy, CreatedAt, Source}`, `FloorLift{Category, By, At}`.
- `(p *Permissions) For(agentID) (AgentPermissions, bool)`, `(a AgentPermissions) Lifted(cat) bool`, `Exception(cat)`.
- `PermissionRules(agentID string, level Autonomy, a AgentPermissions, kind string) ([]Grant, []Limit)`.
- `IsFloor(cat) bool`, `ValidCategory(cat) bool`, `(e Exception) Validate() error`.

- [ ] Tests: `TestActionKind` (send_email→messages, send_proposal/send_contract→contracts, make_payment/send_invoice→payments,
  fs.delete→delete, publish_post→publish, draft_contract/read→internal, documents.create/fs.write→internal,
  calendar.schedule→other, create_purchase_order con `amount`→spend); `TestPermissionRulesThroughEngine` (con
  `NewEngine`: rules+internal→allow; rules+messages→require_approval `perm.important:messages`; never→allow;
  floor payments en autonomous→`perm.floor:payments`; lifted→allow; ask_if monto 500 con amount 800→`perm.if_amount:messages`,
  con 100→allow; per_day 2: tercera llamada→require_approval `limit:perm.per_day:messages`).
- [ ] Implementar; `go test ./internal/policy/`.

### Task 2: PolicyService aplica los permisos (`application/policyflow.go`, `application/permissions_policy.go`)

**Files:** Modify `catalog/catalog.go` (campo `Permissions *policy.Permissions` en `PackRules`), `application/policyflow.go`
(`configured` por agente, grants del agente en `engineConfig`, piso levantado salta `baseline.approval_action` y
`org.always_approve` solo para esa categoría), `application/orgservice.go` (reaplicar paquete conserva permissions),
`application/connections_flow.go` (veredicto `perm.*` de aprobación ⇒ `call.Autonomy="approve_each"` para el Gateway).
Test: `application/permissions_policy_test.go`.

- [ ] Tests: equivalencia sin entrada (todas autonomía×riesgo×acción igual que antes); agente con entrada en rules:
  `email.send`→approval `perm.important:messages`, `documents.create`→allow; piso levantado de contracts:
  `send_proposal` riesgo medium→allow, riesgo high→approval `baseline.risk_high`.
- [ ] Implementar; `go test ./internal/application/ -run 'Permission|Policy|Governance|Standing'`.

### Task 3: servicio de permisos y quién puede cambiar qué (`application/permissions.go`, `trust/manual.go`)

**Produces:** `PermissionsService{Store ConfigStore; Agents Store; SetLevel func(ctx, org, agentID, level, actor string) error; IsTeamLead func(ctx, org, userID, agentID string) bool; Rec *Recorder; OrgID string; Now func() time.Time}` con
`View(ctx, agentID) (AgentPermissionsView, error)`, `Overview(ctx) (PermissionsOverview, error)`,
`ChooseLevel(ctx, agentID, plain string) (AgentPermissionsView, error)`, `SetException(ctx, agentID, cat string, in ExceptionInput) (…)`,
`RemoveException(ctx, agentID, cat)`, `LiftFloor(ctx, agentID, cat string, understood bool)`, `RestoreFloor(ctx, agentID, cat)`,
`SetDefault(ctx, plain string) (PermissionsOverview, error)`, `AdoptNew(ctx, agentID) error`; `PermissionsObserver` (agent.created ⇒ `AdoptNew`).
`trust.(*Service).ChooseLevel(ctx, agentID, to, actor string) error` (techo del rol, controles al subir, auditoría `agent.autonomy_changed` con `source=permissions.manual`).
Niveles en palabras: `ask_all`=approve_each, `important`=rules, `alone`=autonomous, `suggest`=suggest.

- [ ] Tests (`permissions_test.go`): member sin ser responsable→`ErrForbidden`; responsable→ok; admin quita piso→`ErrForbidden`;
  owner sin `understood`→`ErrInvalid`; owner con understood→lifted y auditado; excepción sin nivel elegido→`ErrConflict`;
  `alone` sobre techo `rules`→opción deshabilitada en la vista con motivo `role_ceiling`; `AdoptNew` con default `important`
  y techo `approve_each`→nivel approve_each, entrada creada; `trust.ChooseLevel` respeta techo y controles.
- [ ] Implementar; `go test ./internal/application/ -run Permission` y `go test ./internal/trust/`.

### Task 4: API (`api/permissions.go`) y cableado (`cmd/server/permissions.go`, una línea en `router.go` y `main.go`)

Rutas: `GET /agents/{id}/permissions`, `PUT /agents/{id}/permissions/level`, `PUT|DELETE /agents/{id}/permissions/exceptions/{category}`,
`POST|DELETE /agents/{id}/permissions/floor/{category}`, `GET /permissions`, `PUT /permissions/default`. Lectura `agents:read`;
escritura `agents:read` + comprobación del servicio; default `org:manage`.

- [ ] Test API (`permissions_api_test.go`): viewer no cambia (403), owner cambia nivel y excepción (200), quita piso (200).
- [ ] `go build ./... && go vet ./internal/api/ ./internal/application/ ./internal/policy/ ./internal/trust/ ./cmd/server/`.

### Task 5: frontend — Permisos del agente, Ajustes → Aprobaciones, motivo de cada aprobación

**Files:** Create `frontend/src/lib/permissionsApi.ts`, `lib/permissionsLogic.ts` (+ `.test.mjs` en `test:unit`),
`lib/mock/permissions-mock.ts`, `components/agent/PermissionsSection.tsx`, `components/settings/ApprovalSettings.tsx`,
`components/approvals/ApprovalReason.tsx`. Modify (mínimo): `AgentPanel.tsx` (pestaña Perfil), `SettingsView.tsx`
(sub `approvals`), `lib/mock/engine.ts` (rutas), tarjetas de aprobación (una línea), i18n es/en/pt (`perm.*`).

- [ ] Tests (`permissionsLogic.test.mjs`): `levelOptions` deshabilita sobre el techo con motivo; `approvalReason`
  mapea `perm.important:messages`, `perm.floor:payments`, `baseline.autonomy:approve_each`, `org.amount`, flags del Gateway.
- [ ] `node --test src/lib/permissionsLogic.test.mjs`, `npx tsc --noEmit`, `node scripts/check-i18n.mjs`.

### Task 6 (al final, depende del agente de Proyectos): por proyecto/lote y "Aprobar y no volver a preguntar"

Releer `backend/internal/projects` (lotes) y `ApprovalsTray.tsx` justo antes. Si hay lotes y Pendientes:
`POST /approvals/{id}/remember` (excepción `never` del agente y la categoría, o permiso permanente del proyecto si la
aprobación viene de uno) con resumen y deshacer; si no, se deja para después y se dice en el reporte.

### Task 7: documentación

`docs/architecture/06-permisos-autonomia.md` (§11 nueva) y `docs/architecture/08-api.md` (rutas nuevas).

## Estado (2026-11-03)

Tareas 1–7 hechas. Task 6 sí se hizo (los lotes y Pendientes ya estaban): alcances `project`/`batch` en
`permissions.scopes`, `POST /approvals/{id}/remember`, lista y revocar en proyecto, lote y Ajustes. Sin prueba
automática del escalado en el Gateway (no hay en los arneses una escritura por conexión sin `always_approval` que no
sea interna).
