# Historial de la configuración del agente, con "volver a como estaba" (F4)

"Ayer cambiaste las instrucciones de Valeria. ¿Volver a como estaba?" Cada cambio en la configuración de un agente,
por cualquier ruta, deja una **versión inmutable**: una instantánea completa de lo configurable, quién la hizo (una
persona o el sistema), cuándo, cómo llegó y qué campos cambiaron. Cualquier versión se puede restaurar; restaurar crea
una versión **nueva** (nunca reescribe la historia) y pasa por los mismos servicios, validaciones y permisos que cambiar
esos campos a mano, sin volver nunca a algo más libre.

Código: `backend/internal/agenthistory` (tipos, instantánea y diff `snapshot.go`, servicio `service.go`, plan de
restauración puro `restore.go`, observador de eventos `observer.go`, `MemStore`),
`backend/internal/infrastructure/postgres/agent_history.go` (Postgres), `backend/internal/api/agent_history.go`
(fuentes, ejecutor, rutas y el gancho HTTP), `backend/cmd/server/agent_history.go` (cableado), migración
`827_agent_config_versions.sql` (+ `migrations/down/827_agent_config_versions_down.sql`). UI:
`frontend/src/components/agent/HistorySection.tsx` (sección "Historial" en la pestaña Perfil del agente),
`frontend/src/lib/agentHistoryLogic.ts` (palabras simples, probado), `agentHistoryApi.ts`, `mock/agent-history-mock.ts`.
API: sección 51 de `08-api.md`.

## Qué se guarda

La instantánea (`agenthistory.Config`) reúne, desde el servicio o almacén dueño de cada parte:

| Campo | Fuente | ¿Se restaura? |
|---|---|---|
| `instructions`, `creativity`, `tags` | perfil de trabajo (V2) | sí, con `ProfileService.Update` |
| `tone` | tono por agente | sí, en la misma llamada del perfil |
| `autonomy` | `agents.autonomy` | solo **hacia abajo**, por `PermissionsService.ChooseLevel` → `trust.Service` |
| `permissions` | `org_settings.rules.permissions.agents[id]` (excepciones y pisos levantados, sin fechas) | solo lo **igual o más estricto**, por `PermissionsService` |
| `skills` | asignaciones directas de la biblioteca | sí, salvo una habilidad que ya no existe o no está activa |
| `budget_usd` | tope mensual propio (`budget_caps`) | sí, si no supera el límite del departamento ni el de la organización |
| `tools`, `job` | herramientas y puesto (organigrama: responsabilidades, a quién reporta, escalamiento, horario) | el puesto sí (`org:manage`); las herramientas solo si la versión vieja tiene **menos** |
| `paused`, `read_only`, `rehearsal` | controles y ensayo | no: se cambian desde sus botones |
| `name`, `title`, `role`, `department` | agente | no: estructurales (mover de departamento cambia quién ve el trabajo) |
| `access`, `folders` | dueño/visibilidad (oficinas) y reglas manuales de carpetas | no: los accesos se conceden a mano |

Las listas se ordenan y se quitan fechas y "quién tocó por última vez" para que guardar lo mismo no parezca un cambio.
**Sin secretos**: un campo secreto se guarda solo como huella (`Config.SetSecret`, sha256 de 16 hex) y el diff dice
`secret: true`, sin valores; un secreto nunca se restaura (`secret`). Hoy ningún campo configurable de un agente es un
secreto (las instrucciones ya rechazan credenciales al guardarse); el mecanismo existe para cuando lo haya.

## Imposible olvidarlo: un solo punto de escritura

Los handlers no escriben historia. `Service.Record` compone la instantánea, la compara con la última versión y solo
añade una si algo cambió (sin cambio no hay versión; una ruta que no cambia la configuración no deja rastro). Lo llaman
dos ganchos comunes:

1. **Middleware HTTP** (`historyHook`, en el grupo autenticado de `/api/v1`): tras cualquier petición que modifica
   (no GET) y termina en 2xx cuya ruta lleva `/agents/{id}` (también `governance/agents/{id}/department`), registra ese
   agente con el actor y el nombre de la persona; `source` es la ruta (`PUT /agents/{id}/tone`). Las rutas que cambian
   agentes sin nombrarlos (onboarding, ajustes de la organización, departamentos, industrias, oficinas, habilidades,
   "aprobar y no volver a preguntar", propuestas de confianza, equipos, reset del demo) registran a **todos** los agentes
   de la organización. Antes del primer cambio escribe la versión `baseline` ("así estaba al empezar el historial"),
   así el primer cambio tras el despliegue también se puede deshacer.
2. **Observador de eventos** (`agenthistory.Observer`, envuelve el publicador como `trust.Observer`): `agent.created`,
   `agent.autonomy_changed`, `agent.control_changed` y `agent.profile_changed` que no pertenecen a una petición ya
   cubierta registran al agente en segundo plano. Así entra el **descenso automático por incidente**: actor `system`,
   `source` `trust.auto_demotion` ("El sistema bajó la autonomía tras un incidente"), aunque el contexto lleve a la
   persona cuya decisión lo desencadenó. Mientras una petición está en curso, el observador le **entrega** al
   middleware los agentes que nombran sus eventos (con la causa de cada evento) en vez de registrarlos él: una
   petición deja una versión por agente, una contratación (`POST /agents/from-template`, que no lleva `{id}`) empieza
   el historial del agente nuevo con `source: agent.created`, y si dentro de la petición el sistema actúa solo (evaluar
   y descender) la versión se atribuye al sistema. Entregar y cerrar comparten un candado: un evento que llega después
   del cierre (trabajo en segundo plano que sobrevive a la petición) lo registra el observador.

`TestEveryAgentChangeRouteLeavesAVersion` recorre las rutas reales (perfil, restaurar perfil, tono, tope, puesto,
habilidades, nivel, excepciones, piso, pausa, solo lectura, ensayo, acceso, carpetas) y comprueba que cada una deja una
versión con el campo correcto, y además recorre el router (`chi.Walk`): una ruta nueva que modifica `/agents/{id}/…`
sin clasificar hace fallar la prueba.

Concurrencia: un candado por agente en el proceso; entre réplicas, la clave primaria `(org_id, agent_id, version)`
convierte una colisión en `ErrConflict` y `Record` vuelve a leer y reintenta.

## Restaurar

`agenthistory.PlanRestore(old, cur, env)` es puro y decide campo por campo; `Service.Restore` ejecuta el plan con un
`Executor` (en la API, los servicios de siempre con el contexto de la persona) y después registra la versión nueva con
`source: restore` y `restored_from`. `GET …/versions/{v}/restore` devuelve el mismo plan sin aplicar nada: la UI lo
muestra en la confirmación.

Reglas de seguridad:

- **Autonomía**: nunca sube por esta vía (`autonomy_earned`: "la autonomía se gana con evidencia; pide un ascenso").
  Bajar sí, y por `PermissionsService.ChooseLevel` → `trust.Service.ChooseLevel` (techo del rol, controles,
  auditoría `agent.autonomy_changed`), la única ruta que escribe `agents.autonomy`. Como elegir el nivel en Permisos,
  bajar el nivel así también enciende las reglas de fábrica del agente (solo añade supervisión).
- **Permisos de aprobación**: por categoría, se compara lo que pregunta cada versión al nivel resultante (debajo de
  `rules` se compara como `rules`, donde las excepciones importan): preguntar siempre > preguntar si… > nunca; dos
  "preguntar si…" se comparan condición por condición. Solo se aplica lo igual o más estricto; quitar el piso, "nunca
  preguntar" o condiciones más laxas se omiten (`permissions_freer`). Volver a poner el piso sí se aplica. Las reglas de
  fábrica nunca se apagan por esta vía; encenderlas (si la versión vieja las tenía) sí.
- **Habilidades**: una que ya no existe, está desactivada o sin versión aprobada no se reasigna
  (`skill_unavailable`, con su nombre); las asignadas después se quitan.
- **Tope de gasto**: no puede superar el menor de los límites por encima del agente (departamento u organización,
  `Budget.AgentCeiling`): `budget_above_ceiling`.
- **Herramientas**: solo se restauran si la versión vieja tiene un subconjunto de las actuales (`tools_freer`).
- **Quién**: un agente o el sistema nunca restaura (`403`). La ruta pide `agents:write`; además cada campo exige el
  permiso de su ruta propia (tope `approvals:decide`, puesto `org:manage`, nivel y excepciones admin/owner/responsable).
  Lo que la persona no puede cambiar vuelve en `skipped` con `not_allowed` y lo demás se aplica.
- **Sellado (D3)**: listar, ver, previsualizar y restaurar responden `404` a quien no es miembro de un departamento
  privado, como si el agente no existiera. Aislamiento entre organizaciones: RLS por `org_id` y filtros en cada consulta.
- **Auditoría**: `agent.config_restored` con la versión origen, la nueva, los campos aplicados y los omitidos con su
  motivo.

El perfil (instrucciones, creatividad, especialidades, tono) se restaura en una sola llamada; si esa llamada se rechaza,
se rechazan todos sus campos. Lo mismo para puesto y herramientas.

## Retención y datos (migración 827)

`agent_config_versions` (`org_id, agent_id, version, created_at, actor, actor_kind, actor_name, source, fields,
restored_from, config`), RLS como `202_rls_policies.sql`. Append-only: el rol de la aplicación puede leer, insertar y
borrar (retención), nunca actualizar, y un disparador rechaza cualquier `UPDATE`. Se guardan las **últimas 100
versiones por agente** (`DefaultKeep`); la retención borra las más viejas en la misma transacción del alta, nunca la
última. El `down` borra la tabla y la función; los agentes conservan su configuración actual. Sin base de datos (demo)
se usa `MemStore`.

## UI

Perfil del agente → **Historial** (también `?section=historial`): línea de tiempo con frases simples ("Carla cambió
las instrucciones", "El sistema bajó la autonomía tras un incidente", "Así estaba al empezar el historial"), fecha y
número de versión. Al abrir una versión se ve el antes y el después de cada campo en palabras. "Volver a esta versión"
abre una confirmación con lo que se restaurará y lo que no, con su motivo; al terminar, un resumen de lo aplicado.
Sin métricas de ahorro.

## No verificado

- La prueba de Postgres (`agent_history_pg_test.go`: numeración, retención, RLS, disparador append-only) se salta sin
  `TEST_DATABASE_URL`; el SQL no se ejecutó contra un Postgres real en esta entrega.
- `PUT /governance/agents/{id}/department` solo se monta con Postgres: lo cubre el mismo gancho por patrón, pero no hay
  prueba HTTP de esa ruta.
- Si una fuente no se puede leer en el momento (p. ej. el organigrama sin directorio de personas), no se escribe
  versión para no guardar una instantánea parcial; queda un aviso en el log y el cambio entra en la siguiente versión.
- La UI se verificó con `tsc`, pruebas unitarias de la lógica y `check:i18n`; no se probó en el navegador.
