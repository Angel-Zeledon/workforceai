# Ascensos por confianza: autonomía ganada (B1)

Cada empleado empieza en **periodo de prueba** en su nivel de autonomía actual. Si su historial de aprobaciones es casi
siempre "sí", la plataforma **propone** subirlo un nivel y una persona con el rol adecuado decide; si falla, **baja**. La
autonomía se gana con evidencia, no se enciende con un interruptor. Se apoya en lo que ya existe: los niveles de
`agents.autonomy` (sec. 3 de `06-permisos-autonomia.md`), las aprobaciones, las denegaciones del motor de políticas, el
estado de las tareas y, si hay datos, el puntaje de calidad B11 (`quality-score.md`).

Código: `backend/internal/trust` (metodología pura `assess.go`, servicio `service.go`, observador `observer.go`, memoria
`store.go`, señales `signals.go`), `backend/internal/infrastructure/postgres/trust.go` (consultas), `backend/internal/api/trust.go`
(HTTP), `backend/cmd/server/trust.go` (cableado), migración `791_agent_trust.sql` (+ `migrations/down/791_agent_trust_down.sql`).
UI: `frontend/src/components/trust/TrustCard.tsx` (pestaña Perfil del agente, junto a la tarjeta de calidad).

## Las dos reglas

1. **Un ascenso nunca es automático.** El sistema solo crea una *propuesta*; el nivel cambia cuando un humano con rol
   `admin` u `owner` la acepta (`agents:write` **y** `approvals:decide`). Los principales de agente (`agent:<id>`, el id del
   agente), `system` y `orchestrator` nunca pueden decidir (misma regla que las aprobaciones). Un ascenso es siempre
   **un nivel** y **nunca supera el techo del rol**: el menor entre `autonomy.ceiling` de la plantilla y el techo de su nivel
   de riesgo (`green` → `autonomous`, `amber` → `rules`, `red` → `approve_each`; `roles.AutonomyCeiling`). Un rol que no es
   una plantilla conocida recibe el techo más restrictivo (`approve_each`).
2. **Un incidente baja un nivel al instante.** Bajar autonomía solo añade revisión humana (más acciones pasan por
   aprobación), así que no puede causar daño y esperar a que alguien lo acepte dejaría a un agente que acaba de fallar con
   más libertad de la que merece. Por eso los descensos por incidente son **automáticos**, de **un solo nivel**, auditados,
   con evento en tiempo real y **reversibles por una persona durante 7 días** (falsa alarma). Las señales débiles (tasa de
   aprobación baja, muchas tareas fallidas, calidad baja) solo **proponen** un descenso: decide una persona.

La única función que escribe `agents.autonomy` es `Service.setLevel`: rechaza subir sin decisión humana y subir por encima
del techo (tests `TestSetLevelGuards`, `TestNeverExceedsCeilingProperty`, `TestPromotionIsSuggestedNeverApplied`,
`TestForgedProposalAboveCeilingIsRefused`).

## Metodología (versión 1)

La evidencia cuenta solo lo ocurrido **desde que el agente llegó a su nivel actual** (estrictamente después), con un
máximo de **30 días** hacia atrás. Tras cualquier cambio de nivel la evidencia empieza de cero: el siguiente nivel se gana
otra vez, y la decisión que provocó un descenso pertenece al nivel anterior.

**Periodo de prueba**: desde que el agente se empieza a seguir (contratación, o la primera vez que la plataforma lo mira)
hasta que pasen **14 días** *y* tenga **10 decisiones** humanas. En prueba no se propone ningún ascenso; los descensos sí
se aplican.

**Ascenso propuesto** (se deben cumplir todos):

| Criterio | Umbral | Fuente |
|---|---|---|
| `decisions` (aprobadas + rechazadas por una persona) | ≥ 20 | `approvals` por `resolved_at` |
| `approval_rate` (aprobadas / decisiones) | ≥ 95 % | `approvals` |
| `high_risk_rejections` | ≤ 0 | `approvals.risk = 'high'` rechazadas |
| `policy_denials` | ≤ 0 | `audit_logs` `tool.denied` con `reason` `policy:*` |
| `task_failure_rate` (fallidas / terminadas) | ≤ 10 % (con ≥ 5 tareas; con menos, "sin datos") | `tasks` `done`/`failed` |
| `quality` (B11) | ≥ 75 y tendencia no `down` (sin puntaje: "sin datos") | `quality.Service` |

Además: el agente no está en su techo y no hubo un ascenso **rechazado** desde este nivel en los últimos **7 días**
(para no insistir). Un criterio "sin datos" no cuenta en contra (igual que en B11), pero `decisions` y `approval_rate`
deben cumplirse: sin evidencia no hay ascenso.

**Descenso automático** (basta uno): una acción de alto riesgo rechazada por una persona; **3 rechazos en 72 horas**;
**3 denegaciones de política** en la ventana.
**Descenso propuesto** (basta uno, decide una persona): tasa de aprobación < 80 % con ≥ 10 decisiones; tareas fallidas
> 30 % con ≥ 5 tareas; calidad B11 < 50.

No cuentan contra el agente: los rechazos del sistema (nota `sin respuesta…` o `superseded:`, como en B11), las
denegaciones por kill-switch, modo solo lectura, pausa o falta de permiso de conexión (solo las del motor de políticas
`policy:*`), ni las tareas `blocked` (se muestran, no se juzgan: también las bloquean presupuestos y pausas). Las
aprobaciones de esta plataforma no se editan (se aprueban o rechazan), así que "aprobada sin cambios" equivale a aprobada;
el día que exista edición, una aprobación editada debería contar como no aprobada sin cambios.

**Progreso** hacia el siguiente nivel: fracción de criterios cumplidos, con `decisions` proporcional (12 de 20 = 0.6 de
ese criterio); 100 % solo si se cumple todo.

Todas las respuestas llevan el bloque `methodology` con estos umbrales, y cada propuesta guarda la **evidencia** que vio
(conteos, criterios, disparadores, techo, versión de la metodología).

## Cuándo se evalúa

- `trust.Observer` envuelve el publicador de eventos: tras `approval.resolved`, `task.completed`, `task.failed` y
  `agent.created`, reevalúa a ese agente en segundo plano. Las denegaciones de política solo quedan en auditoría (no hay
  evento), así que se recogen en la siguiente evaluación del agente (la tarea que las sufrió termina justo después).
- `POST /agents/{id}/trust/evaluate` (con `agents:write`) evalúa a demanda.
- La evaluación no llama al modelo: no gasta presupuesto.

La evaluación aplica un descenso automático, crea una propuesta pendiente (como mucho una por agente) o marca como
`superseded` una propuesta que la evidencia ya no respalda o cuyo nivel cambió por otra vía (p. ej. un paquete de
onboarding). Aceptar una propuesta vuelve a comprobar la evidencia, el nivel actual y el techo; si algo cambió responde
`409 evidence_changed`.

## Controles

Subir autonomía (aceptar un ascenso o revertir un descenso) se **rechaza** con `409` y el código del control mientras haya
kill-switch (`kill_switch_active`), modo solo lectura de la organización o del agente (`read_only_mode`) o el agente esté
pausado (`agent_paused`): es la misma comprobación que pasa cualquier acción con efectos (`controls.Service.Check`). Bajar
nunca se bloquea. El reset del demo (`POST /demo/reset`) devuelve a su nivel inicial a los agentes que la escalera movió y
olvida registros y propuestas.

## Auditoría y tiempo real

- Auditoría: `trust.promotion_suggested`, `trust.demotion_suggested` (actor `system`), `agent.autonomy_changed` (con
  `from`, `to`, `direction`, `source` = `trust.auto_demotion` | `trust.promotion_accepted` | `trust.demotion_accepted` |
  `trust.demotion_reverted`, `proposal_id`, motivos y conteos), `trust.proposal_declined`, `trust.decision_refused`
  (quién intentó decidir sin poder y por qué), `trust.reset`.
- Eventos WS (aditivos): `agent.autonomy_changed` `{agent_id, from, to, direction, source, proposal_id, reasons}` y
  `trust.proposal` `{proposal_id, agent_id, kind, status, from?, to?}`. El frontend actualiza el nivel del agente y recarga
  la tarjeta.

## Datos (migración 791)

`agent_trust` (por agente: nivel inicial, nivel visto, desde cuándo lo tiene, desde cuándo se sigue) y `trust_proposals`
(tipo, de/a, estado `pending|accepted|declined|applied|reverted|superseded`, `auto`, motivos, evidencia JSON, quién y
cuándo resolvió). Índice único parcial: una sola propuesta `pending` por agente. RLS por organización como
`202_rls_policies.sql`. `agents.autonomy` sigue siendo la única fuente de verdad del nivel. El `down` borra las dos
tablas; los agentes conservan su nivel.

## No verificado

- Las pruebas de Postgres (`trust_pg_test.go`: consultas de evidencia, RLS, `down` y `up` otra vez) se saltan sin
  `TEST_DATABASE_URL`; en esta entrega no había base de datos disponible, así que el SQL no se ejecutó contra Postgres real.
- No se probó el flujo en el navegador ni con e2e de Playwright, y no se revisaron capturas de la tarjeta (la página en
  modo simulación compila y responde, pero no se pudo automatizar el navegador). Verificado: `tsc`, lint, pruebas unitarias
  del frontend (`trustLogic.test.mjs`) y `check:i18n`. En modo simulación todos los agentes están honestamente en prueba.
- Los umbrales son una primera calibración razonable, no un resultado medido: conviene revisarlos con datos reales.
