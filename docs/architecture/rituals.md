# Rituales: stand-up diario y retrospectiva semanal (B4)

Cada agente cuenta en la sala de reuniones qué hizo, qué hará y qué lo bloquea; una vez por semana, en la
retrospectiva, propone hasta dos mejoras a **su propio** trabajo. Las propuestas son sugerencias que resuelve una persona.

Código: `backend/internal/rituals` (construcción determinista, servicio, horario, almacén en memoria),
`backend/internal/infrastructure/postgres/rituals.go`, `backend/internal/api/rituals.go`, `backend/cmd/server/rituals.go`.
Migración `793_rituals.sql` (+ `backend/migrations/down/793_rituals_down.sql`). UI: pestaña **Rituales** en `/meetings`
(`frontend/src/components/rituals/RitualsPanel.tsx`, enlace directo `/meetings?view=rituals`), claves `rituals.*` en es/en/pt.

## Qué dice cada agente (stand-up)

Todo sale de datos que ya existen, con la misma función pura para los mismos datos (probado):

| Lista | Fuente |
|---|---|
| `done` (Hice) | tareas `done` o `failed` del agente terminadas desde el último stand-up (como máximo una semana atrás; si no hubo, últimas 24 h), la más reciente primero |
| `next` (Haré) | tareas `running` y luego `pending` (la más antigua primero) |
| `blockers` | tareas `blocked` y `awaiting_approval`, y aprobaciones pendientes del agente que no estén ya representadas por su tarea |

Cada lista guarda hasta 3 elementos y un marcador `+N`. `lines` son 2 frases (3 si hay bloqueos) en el idioma de la
organización para el chat; la UI pinta las listas con sus propias traducciones. Un agente sin nada que contar aparece con
`quiet: true`. Un agente en pausa no participa (`absent`).

## Retrospectiva

Ventana de 7 días. Por agente: tareas hechas y fallidas, aprobaciones rechazadas **por una persona** (las rechazadas por el
sistema, `sin respuesta…` / `superseded:`, no cuentan: misma regla que B11), retrabajos de la revisión Q1
(`output.review.reworks > 0` o veredicto `rework`/`fail`) y el puntaje de calidad de B11 (ventana de 30 días).

Propuestas (máximo 2 por agente, la señal más repetida primero):

| Señal | Umbral | Tipo |
|---|---|---|
| `rejected_approvals` | 2+ en la semana | `instruction_note` |
| `reworks` | 2+ | `instruction_note` |
| `failed_tasks` | 2+ | `instruction_note` |
| `quality_score` | puntaje `fair`/`needs_attention` o tendencia a la baja (solo si queda hueco) | `skill_suggestion` (guardar como skill la tarea que salió bien) o, sin tarea buena, `instruction_note` |

El texto de una propuesta lo escribe la plataforma (fijo por señal, solo varían cifras): **nunca** lleva títulos de tareas
ni otro texto de terceros, porque una nota aceptada pasa a las instrucciones del agente. Los títulos van en `evidence`, que
solo se muestra.

## Aceptar una propuesta

- Solo una persona con `agents:write`; el servicio rechaza agentes como actor (`403`).
- `pending -> accepted` es compare-and-set: dos personas no la aplican dos veces.
- `instruction_note`: se añade al final de las instrucciones por `ProfileService.Update` como quien aceptó (mismo camino que
  editar el perfil a mano: nueva versión, auditada, reversible con "restaurar"; creatividad, skills y tono no cambian). Si la
  edición falla (p. ej. instrucciones demasiado largas), la propuesta vuelve a `pending` y el error llega a quien aceptó.
- `skill_suggestion`: solo se registra; la persona crea la skill (Skills o `/aprender`), que un administrador revisa.
- Nada toca herramientas, permisos, conexiones ni autonomía.

## Horario y controles

`ritual_settings` por organización (`GET/PUT /settings/rituals`): zona horaria, `standup {enabled, time, weekdays}`,
`retro {enabled, time, weekdays: [uno]}`, `use_model`.

**Por defecto ambos están apagados.** Motivo: al actualizar, ninguna organización debe empezar a recibir mensajes en el
chat de la oficina cada mañana sin haberlo pedido (retrocompatible), y el botón "Hacer stand-up ahora" sirve para la demo
sin activar nada. Activarlo es una casilla.

El planificador revisa cada minuto. Una ocurrencia se celebra entre su hora y 2 h después (si el servidor estuvo caído más
tiempo no se celebra tarde). Se reclama por su clave de periodo (fecha local para el stand-up, semana ISO `2026-W43` para la
retro) con un índice único parcial: con varias réplicas cada ocurrencia se guarda una sola vez.

| Control | Programado | Manual ("ahora") |
|---|---|---|
| Kill-switch / controles no disponibles | se guarda como `skipped` (sin nota ni evento) | `409 blocked` |
| Fuera del horario de operación | `skipped` | se celebra (lo pidió una persona; no ejecuta nada) |
| Modo solo lectura | se celebra (no hay efectos externos) | se celebra |
| Agente en pausa | no participa (`absent`) | igual |
| Topes de gasto | solo afectan al resumen con modelo | igual |

Además, como máximo 10 rituales manuales por tipo y día UTC (`429 limit`).

## Costo

Sin `use_model` (por defecto) un ritual **no llama a ningún modelo**: costo cero, también en simulación. Con `use_model`
se hace **una** llamada por ritual a `Orchestrator.Summarize` (`mode: "meeting"`, las frases deterministas como mensajes no
confiables), que reserva presupuesto, apunta en el libro de uso y cae al resumen local si el runtime falla. Si un tope de
gasto no lo permite, queda el resumen determinista y se audita `ritual.summary_skipped`. El modelo nunca escribe lo que
dice cada agente, solo el párrafo del equipo. En simulación el runtime devuelve su resumen determinista.

## Oficina y chat

Al celebrarse se emite `ritual.held {ritual_id, kind, origin, agent_ids, proposals}`. El frontend (`useRituals`) lleva a esos
agentes a la sala de reuniones durante 45 s (`officeSignals`, mismo mecanismo que la planificación; caben 6). La nota completa
se publica en el chat de la oficina como mensaje del sistema (`Orchestrator.PostSystemNotice`).

## Modo demo

`NEXT_PUBLIC_MOCK=true`: `frontend/src/lib/mock/rituals-mock.ts` responde el mismo contrato. El stand-up usa las tareas y
aprobaciones reales de la demo (el mismo constructor que prueban los tests de UI); la retro cuenta honestamente la semana, así
que con poco historial no propone nada. Ninguna llamada a modelo.

## Datos y seguridad

- `rituals` y `ritual_proposals`: la app no puede borrar filas (historial). `ritual_settings`: un JSON por organización.
- RLS `tenant_isolation` en las tres tablas; `tenantdata` las descubre solo (exportación y purga).
- Auditoría: `ritual.held`, `ritual.skipped`, `ritual.blocked`, `ritual.summary_skipped`, `ritual.proposal_accepted`,
  `ritual.proposal_dismissed`, `ritual.settings_changed` (metadatos, sin textos de tareas).

## Límites conocidos y qué no se verificó

- El horario de cada agente (organigrama, `orgchart.Schedule`) no se consulta: un agente fuera de su turno participa igual.
- Las frases del servidor y el texto de las propuestas están en es/en (idioma de la organización); en portugués la UI traduce
  etiquetas y títulos, pero el texto exacto que se añadiría a las instrucciones se muestra tal cual.
- `ListTasks`/`ListApprovals` leen todo el historial de la organización en cada ritual (como `/resumen`); con mucho historial
  convendría filtrar por fecha en SQL.
- Las pruebas de Postgres (`rituals_pg_test.go`, incluida la bajada y subida de la migración) necesitan `TEST_DATABASE_URL`;
  en esta rama se omitieron (sin base de datos). La UI no se vio en un navegador ni se tomaron capturas; no se corrieron e2e
  de Playwright.
