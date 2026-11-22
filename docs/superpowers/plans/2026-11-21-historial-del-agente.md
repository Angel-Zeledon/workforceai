# F4. Historial de la configuración del agente, con "volver a como estaba" — plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans. Pasos con checkbox (`- [ ]`).

**Goal:** cada cambio de configuración de un agente (por cualquier ruta) deja una versión inmutable con quién, cuándo
y qué cambió; la persona ve la línea de tiempo en palabras simples y puede "volver a esta versión" sin que eso suba la
autonomía, relaje aprobaciones, reasigne habilidades que ya no existen o pase un tope de su departamento.

**Architecture:** paquete nuevo `internal/agenthistory`. La versión es una **instantánea completa** de la
configuración (no un diff que cada handler tenga que acordarse de escribir): `Service.Record(ctx, org, agent, cause)`
la compone leyendo las fuentes (perfil, tono, autonomía, permisos, habilidades, tope, controles, ensayo, puesto,
accesos), la compara con la última y solo añade una versión si algo cambió. Dos ganchos comunes, sin tocar los
handlers:

1. **Middleware HTTP** en el grupo autenticado de `/api/v1`: tras cualquier petición que modifica (no GET) y termina en
   2xx cuya ruta lleva `/agents/{id}` → `Record` con el actor de la petición. Unas pocas rutas que cambian agentes sin
   llevar `{id}` (aceptar un ascenso, "aprobar y no volver a preguntar", aplicar un paquete o industria, nivel por
   defecto, departamentos) → `RecordAll` (todos los agentes de la organización; sin cambio no hay versión).
2. **Observador de eventos** (envuelve el publicador, como `trust.Observer`): `agent.created`,
   `agent.autonomy_changed`, `agent.control_changed`, `agent.profile_changed` fuera de una petición ya cubierta →
   `Record` en segundo plano. Así entra el descenso automático por incidente (actor `system`, origen
   `trust.auto_demotion`).

Una prueba recorre el router (`chi.Walk`) y falla si aparece una ruta que modifica `/agents/{id}/…` sin clasificar.

**Restaurar** (`POST /agents/{id}/versions/{v}/restore`) calcula un plan puro (`agenthistory.PlanRestore`) campo a
campo y lo ejecuta a través de los servicios de siempre (perfil, permisos → `trust.Service`, habilidades, presupuesto,
puesto), con el contexto de la persona: mismas validaciones y permisos. Lo que no se puede restaurar se omite y la
respuesta lo explica. El resultado es una versión nueva (`restored_from`), nunca se reescribe la historia.

**Tech Stack:** Go (chi), Postgres (migración 827, RLS por `org_id`), Next.js 15 / React 19, i18n es/en/pt.

## Global Constraints

- Append-only: la tabla no tiene UPDATE; la retención (100 por agente) borra solo las más viejas, nunca la última.
- Sin secretos: un campo marcado secreto guarda solo una huella (sha256 corta) y el diff dice "cambió".
- Autonomía: restaurar solo puede **bajar**, y lo hace por `PermissionsService.ChooseLevel` → `trust.Service`.
- Permisos de aprobación: solo se restaura lo que es **igual o más estricto** por categoría (piso puesto, preguntar);
  quitar el piso, "nunca preguntar" o condiciones más laxas se omiten (`permissions_freer`).
- Habilidades: una que ya no existe o no está activa no se reasigna (`skill_unavailable`).
- Tope de gasto: no puede superar el límite del departamento ni el de la organización (`budget_above_ceiling`).
- Un agente nunca restaura (403). Departamento privado: listar/ver/restaurar → 404 para no miembros.
- Auditoría `agent.config_restored` con versión origen, campos aplicados y omitidos.
- Pausa, solo lectura, ensayo, departamento y accesos se muestran en el historial pero no se restauran (se cambian
  desde sus controles; mover de departamento cambia quién ve el trabajo).

## Tasks

- [x] 1. `agenthistory`: tipos, instantánea, diff, `MemStore`, `Service.Record/RecordAll/List/Get`, retención,
  secretos. Pruebas unitarias.
- [x] 2. `PlanRestore` (puro) + `Service.Restore` con `Executor`. Pruebas: no sube autonomía, no relaja permisos,
  habilidad inexistente, tope sobre el techo, agente no restaura, versión nueva.
- [x] 3. Postgres: migración 827 (`agent_config_versions`, RLS, sin UPDATE) + down + `PgStore`.
- [x] 4. API: rutas `GET /agents/{id}/versions`, `GET …/versions/{v}`, `POST …/versions/{v}/restore`; middleware;
  fuentes y ejecutor desde `Deps`; observador cableado en `cmd/server`. Pruebas: recorrido de rutas, sellado,
  permisos, aislamiento entre organizaciones, descenso automático.
- [x] 5. Frontend: `agentHistoryApi.ts`, `agentHistoryLogic.ts` (+ test), sección "Historial" en el Perfil del agente
  con línea de tiempo, antes/después y confirmación; mock; i18n es/en/pt.
- [x] 6. Docs: `docs/architecture/agent-history.md`, `08-api.md` §51.
