# Vigilante de trabajo atascado (F6)

Hasta ahora solo había un tiempo máximo por llamada al runtime (`TaskTimeout`). Nada veía trabajo que **dejó de avanzar**: una tarea en `running` sin señales, un nodo de proyecto esperando algo que nunca llega, un pedido que quedó a medias tras un reinicio que no lo reanudó. El vigilante lo detecta, el empleado dueño avisa con una sugerencia y una persona decide: **reintentar, reasignar o cancelar**.

Detectar no llama a ningún modelo ni gasta tokens. Recuperar pasa por el camino normal (presupuesto, aprobaciones, kill-switch, pausa, solo lectura).

## Qué es "progreso"

Datos que ya existían, sin migración:

| Fuente | Qué aporta |
|---|---|
| `tasks.started_at` | cada intento de una tarea lo escribe al pasar a `running` |
| libro de uso (`usage`) | cada llamada al runtime que termina deja una entrada con hora y tarea |
| la ejecución viva en este proceso | cuándo empezó y cuándo terminó su última espera legítima |
| `tasks.created_at` y `finished_at` de las dependencias | para una tarea lista que nadie ejecuta |

Cada tarea que el planificador ejecuta corre bajo un **supervisor** (`Orchestrator.supervise`, `backend/internal/application/watchdog_exec.go`) que registra su ejecución. Las esperas legítimas la marcan como "esperando": todo lo que pasa por `YieldSlot` (aprobación, pregunta a una persona, pausa del agente o kill-switch, pausa u horario del proyecto, regla de delegación, tope de gasto) y la espera de un turno en la cola de la organización (`acquireSlot`). Al terminar la espera cuenta como progreso.

## Qué es "atascado"

`Orchestrator.Stalled` (determinista, solo lecturas):

- **Sin avanzar** (`kind: no_progress`): la tarea está en `running`, este proceso la ejecuta, no está esperando nada legítimo y lo último que avanzó es más viejo que el umbral.
- **Sin trabajador** (`kind: no_worker`): ningún proceso ejecuta el pedido (un reinicio que no lo reanudó, una instancia que murió). Cuenta una tarea en `running`, o la primera tarea lista (`pending` con todas sus dependencias hechas). Un pedido así aporta **una** candidata: reanudarlo cubre a todas.

**Nunca** es atasco: `awaiting_approval`, `needs_input` (ya se ven en Pendientes y en "Te necesitan"), un pedido `paused` (tope de gasto esperando a una persona), un pedido en `planning` o `awaiting_confirmation`, ni una tarea pendiente de un pedido que este proceso sí está ejecutando (la cola del planificador es suya).

## Umbral por organización

Dentro de las señales de iniciativa (`GET/PUT /settings/initiative`, campo `signals.stalled_work`, solo `org:manage` para cambiarlo, auditado en `initiative.settings_changed`). Vive en la columna JSONB `initiative_settings.signals` que ya existía: **no hace falta migración** (el número 824 queda libre). Una fila guardada antes lee los valores por defecto.

| Campo | Defecto | Rango | Por qué |
|---|---|---|---|
| `enabled` | `true` | — | apagado: el barrido no evalúa nada; el detalle de la tarea sigue ofreciendo la recuperación |
| `task_minutes` | 30 | 5..1440 | una tarea normal hace llamadas de 2 min como máximo (`TaskTimeout`) con reintentos acotados |
| `project_minutes` | 120 | 15..4320 | las tareas de proyecto van detrás del trabajo interactivo (prioridad de cola) y un proyecto grande encadena muchas; la espera de turno ya no cuenta, pero sus tareas son más largas |

## Al detectar

`Watchdog.Scan` (`backend/internal/application/watchdog.go`), cada `WATCHDOG_INTERVAL` (duración Go, por defecto `5m`, mínimo `1m`; `0`/`off` lo apaga; el primero espera un intervalo entero tras arrancar) y a pedido con `POST /stalled-work/scan` (`org:manage`, auditado `watchdog.scanned`):

1. Una vez por episodio y proceso: evento en vivo `task.stalled` (`{task_id, request_id, agent_id, title, kind, minutes, project, last_progress}`) y auditoría `task.stalled` (actor `system`). El **episodio** es la tarea más el inicio del intento que se atascó: si se reintenta y se vuelve a atascar, es otro episodio.
2. Una sugerencia `stalled_task` del agente dueño por `InitiativeService.Raise`: el interruptor general, el presupuesto diario, mensual y por agente, y la ventana de duplicados de 24 h (clave = episodio) aplican igual. Texto en el idioma de la organización: *"La tarea "X" lleva 40 min sin avanzar"* / *"¿La reintento, se la paso a otro o la cancelo?"*; `params` (`task_id`, `request_id`, `task`, `minutes`, `kind`, `project`) para que la UI la muestre en el idioma de quien mira.
3. Las sugerencias `stalled_task` pendientes cuya tarea ya no está atascada se cierran solas (`dismissed`, por `system`, auditoría `initiative.closed`); las que una persona recuperó quedan `accepted` por ella.

**Aceptar** una sugerencia `stalled_task` (la bandeja "Te necesitan", el panel de sugerencias) es **reintentar**, no un pedido nuevo: `InitiativeService.SetAction` registra esa acción para la señal.

## Recuperar

`Orchestrator.RecoverStalled` (`watchdog_recover.go`). Solo personas: un actor de agente, `system` u `orchestrator` recibe `403` (auditado `task.stall_*_refused`). Solo si la tarea **sigue** atascada (si no, `409 not_stalled`).

| Acción | Permiso | Qué hace |
|---|---|---|
| Reintentar | `requests:create` | la tarea vuelve a `pending` y corre otra vez por `runTask` |
| Reasignar | `tasks:manage` | igual, con otro agente del **mismo departamento** y el **mismo rol**, que pueda tomarla (`canTake`: el auditor interno no toma trabajo, el agente no está en pausa) y que respete las reglas de delegación |
| Cancelar | `tasks:manage` | la tarea y sus dependientes activos (transitivos: `pending`, `running`, `awaiting_approval`, `needs_input`) quedan `blocked` con una nota; sus aprobaciones pendientes se retiran y sus preguntas abiertas se cierran (no se decide nada por ellas). El resto del pedido sigue y cierra con su informe |

Reintentar y reasignar rechazan con `409 execution_blocked` si el kill-switch, la pausa o el modo solo lectura frenan al agente; pasado eso, el camino normal vuelve a mirar todo (presupuesto, aprobaciones, guardas).

**Cancelar pide confirmación de la lista exacta.** `GET /tasks/{id}/stall` devuelve `cancel_cascade`; `POST …/cancel` debe mandar `confirm_task_ids` con esos mismos ids. Si la lista cambió, `409 cascade_changed` con la lista nueva y no se toca nada. Si algún dependiente es de un departamento privado que la persona no ve, el detalle lo cuenta en `hidden_cascade` (sin nombrarlo) y cancelar responde `409 cascade_hidden`.

### Por qué no hay doble ejecución

- **Ejecución viva en este proceso:** la acción deja una orden en el supervisor de la tarea y cancela su contexto (causa propia, `errStallRecover`; `failTask` la ignora). **La misma goroutine** aplica la orden: vuelve a correr la tarea por `runTask` o la bloquea. Nunca se arranca una segunda ejecución. Si el paso actual no respeta la cancelación, la respuesta es `requested` y la orden se aplica cuando ese paso suelte; si la tarea terminó sola mientras tanto, `finished` y no se repite.
- **Sin trabajador:** se actualiza la tarea guardada y el pedido se reanuda con `track` + `recoverRequest`, el mismo cerrojo y el mismo camino de la reanudación durable (`durable.go`). Si `Recover` ya tomó el pedido, la acción responde `409`; si la acción lo tomó primero, `Recover` lo salta. Lo terminado no se repite; las aprobaciones esperando continúan desde su punto de control. Un pedido sin datos para reanudar (sin `RunMeta`) se cierra con una explicación, como tras un reinicio.
- **Dos personas a la vez:** cerrojo `stall:<org>:<tarea>` del `Locker` (Redis en producción) mientras dura la acción, y una marca "recuperando" en el proceso hasta que la nueva ejecución se registra (como mucho 2 min). La segunda persona recibe `409`.

Una tarea en `running` de un pedido vivo pero sin ejecución registrada (un intento que terminó sin escribir su estado) se trata con la recuperación manual existente (`RetryTask`) dentro del pedido vivo.

## API

`backend/internal/api/watchdog.go` (ver `08-api.md` sección 46). Todo respeta D3: el trabajo de un departamento privado responde `404` a quien no es miembro, la lista lo omite, la sugerencia la ve solo quien ve el trabajo de su agente (filtro ya existente de `/initiatives`) y el evento en vivo llega solo a sus miembros (lleva el agente).

## UI

- `StallActions` (`frontend/src/components/watchdog/StallActions.tsx`): Reintentar / Reasignar (elige entre los candidatos) / Cancelar, con un diálogo que lista los dependientes que también se cancelan. Aparece en la sugerencia `stalled_task` (en lugar de "Aceptar"), en la tarea del panel del agente y en el nodo del proyecto (`NodeDrawer`). Si la tarea no está atascada no muestra nada.
- Umbrales: la tarjeta de presupuesto de iniciativa (Seguridad) muestra "Trabajo atascado (vigilante)" con sus dos minutos.
- Helpers puros y pruebas: `frontend/src/lib/watchdogLogic.ts` (+ `.test.mjs`); claves `stall.*` e `initiative.signal(s).stalled_*` en es/en/pt.
- Modo mock: `frontend/src/lib/mock/watchdog-mock.ts` muestra una tarea de Contabilidad atascada hace 40 min (con un dependiente) y su sugerencia.

## Límites conocidos

- Como la reanudación durable, supone **un proceso que ejecuta pedidos**. Con varias réplicas que ejecutan, una tarea viva en otra réplica no tiene supervisor aquí: solo cuentan `started_at` y el libro de uso, y una espera legítima larga allí (cola, pausa) podría verse como atasco. En ese caso deja `WATCHDOG_INTERVAL=off` en las réplicas que no ejecutan. El cerrojo del `Locker` sigue evitando dos recuperaciones a la vez.
- El libro de uso se lee entero (una vez por barrido y solo si hay candidatas).
- Reintentar repite la llamada al runtime. Una acción ya aprobada nunca se ejecuta dos veces (registro de ejecución por aprobación), pero una acción interna de bajo riesgo sin aprobación que el intento cortado ya hubiera hecho podría pedirse de nuevo: la misma regla que la reanudación tras un reinicio.
- La memoria de "episodio ya anunciado" es del proceso: tras un reinicio, un atasco que sigue se anuncia una vez más (la sugerencia no se repite: la deduplica la clave).

## Qué no se verificó

Sin Docker ni navegador. Las pruebas de Go usan los almacenes en memoria; con Postgres el aislamiento por organización lo da RLS y aquí se probó con un almacén que lo imita. La UI compila y sus helpers y el mock tienen pruebas, pero no se vio en un navegador. El barrido periódico no se observó en un servidor real; se probó `Scan` directamente.
