# Empleados proactivos (B2)

Un empleado puede avisar por su cuenta ("la tarea *Enviar propuesta* falló, ¿la reviso?"). Esto es una **sugerencia**, no una acción.

## Reglas

- Una sugerencia solo es un registro (`initiatives`) que una persona acepta o descarta. No ejecuta nada.
- Aceptar llama a `Orchestrator.Submit` con el texto guardado: es el mismo camino que `POST /requests`. Planificación, aprobaciones (incluida la doble), topes de gasto, kill-switch, modo solo lectura, pausa del agente, reglas de delegación y auditoría se aplican igual. Si ese camino rechaza la solicitud, la sugerencia vuelve a `pending` y el error llega a quien aceptó.
- Los agentes no reciben ninguna capacidad de aceptar ni de aprobar: las sugerencias las crea la plataforma a partir de una señal y solo la API con permiso `requests:create` las resuelve.
- Aceptar es atómico (`pending -> accepted` como compare-and-set): dos personas no pueden crear dos solicitudes de la misma sugerencia.

## Presupuesto de iniciativa

Por organización (`initiative_settings`, `GET/PUT /settings/initiative`, solo `org:manage` para cambiarlo):

| Campo | Defecto | Significado |
|---|---|---|
| `enabled` | `true` | apagado: no se crea ninguna sugerencia |
| `daily_limit` | 3 | sugerencias por día UTC (cualquier estado cuenta, también las descartadas) |
| `monthly_limit` | 30 | sugerencias por mes UTC |
| `agent_daily` | vacío | tope diario por agente (0 silencia a ese agente) |

Una sugerencia no llama a ningún modelo: se construye con datos de una señal que ya existe, así que no gasta tokens. El gasto aparece cuando una persona acepta: la solicitud creada se cobra contra los topes de gasto como cualquier otra. El presupuesto de iniciativa limita **cuántas veces** se molesta a la gente; los topes de gasto siguen siendo la barrera del dinero.

Duplicados: la misma señal (`signal` + `signal_key`) no se vuelve a sugerir dentro de 24 h (`InitiativeService.Dedupe`). Un rechazo por presupuesto, duplicado o desactivado no es un error para quien llama: `Raise` devuelve `RaiseOutcome{Raised:false, Reason}` (`disabled | duplicate | daily_limit | monthly_limit | agent_limit`).

Límite conocido: la comprobación de presupuesto y la inserción se serializan con un mutex por proceso; con varias réplicas el límite puede excederse por unas pocas sugerencias en una carrera.

## Disparador

`Orchestrator.failTask` llama a `InitiativeRaiser.TaskFailed` (gancho opcional, `Orchestrator.SetInitiative`). Una tarea que falla (no las detenidas por presupuesto o saldo del proveedor, que quedan `blocked`) genera "La tarea X falló" con un texto de solicitud para revisarla. La clave de duplicado es agente + título de la tarea. Cualquier otro código puede llamar `InitiativeService.Raise` con su propia señal; no hay detección de señales compleja.

## Piezas

- Dominio y servicio: `backend/internal/application/initiative.go`.
- Almacenes: `infrastructure/memory/initiative.go`, `infrastructure/postgres/initiative.go` (migración `787_initiatives.sql`, rollback en `backend/migrations/down/787_initiatives_down.sql`, RLS por organización; la app no puede borrar filas de `initiatives`).
- API: `backend/internal/api/initiative.go` (ver `08-api.md` sección 25).
- UI: `InitiativePanel` (Dashboard) e `InitiativeBudgetCard` (Seguridad), claves `initiative.*` en es/en/pt.

## Qué no se verificó

La prueba de Postgres (`initiative_pg_test.go`) necesita `TEST_DATABASE_URL`; sin él se omite. La UI no se compiló ni se vio en un navegador en esta rama (sin `node_modules`).
