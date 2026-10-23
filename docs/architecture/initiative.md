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

## Disparadores

1. **Tarea fallida.** `Orchestrator.failTask` llama a `InitiativeRaiser.TaskFailed` (gancho opcional, `Orchestrator.SetInitiative`). Una tarea que falla (no las detenidas por presupuesto o saldo del proveedor, que quedan `blocked`) genera "La tarea X falló" con un texto de solicitud para revisarla. La clave de duplicado es agente + título de la tarea.
2. **Señales de métricas** (migración `800`, sección siguiente).

Cualquier otro código puede llamar `InitiativeService.Raise` con su propia señal. `RaiseInput.Dedupe` (opcional) cambia la ventana de duplicados de esa señal y `RaiseInput.Params` guarda los valores detrás del texto.

## Señales de métricas

`InitiativeScanner` (`backend/internal/application/initiative_signals.go`) mira datos que la plataforma ya guarda y, si un número cruza el umbral de la organización, crea una sugerencia con `InitiativeService.Raise`. La detección es **determinista**: aritmética sobre datos guardados, sin llamar a ningún modelo, así que no gasta tokens. Cada candidata pasa por el mismo camino que una tarea fallida: interruptor general, presupuesto diario / mensual / por agente, ventana de duplicados y, al aceptar, una solicitud normal. Nada se ejecuta solo.

Se eligieron cuatro señales por lo que el código realmente guarda:

| Señal (`signal`) | Qué mira | Por qué es fiable | Agente (por rol) | Clave de duplicado / ventana |
|---|---|---|---|---|
| `metric_change` | Hojas (`sheet`) y tablas (`table`) actualizadas en los últimos 30 días (máx. 50): una columna cuyo encabezado nombra margen, ventas o caja (`ClassifyMetric`, es/en/pt; "costo de ventas" no cuenta). Compara el último valor numérico con el anterior (fila = periodo). Margen: diferencia en **puntos** (valores entre ±1.5 se leen como fracción: 0.31 = 31 %); ventas y caja: cambio **relativo** en %. | El contenido canónico `aiw.sheet/1` / `aiw.table/1` guarda números reales (`v` de la celda). Solo cuentan números JSON (no se adivina texto con formato); se omiten filas "total" y fórmulas `SUM`. Extracción en `backend/internal/artifacts/metrics.go`. | margen: `accounting` → `finance_treasury` → `analyst` → `data_analyst`; caja: `finance_treasury` → `accounting` → `analyst`; ventas: `sales` → `analyst` → `data_analyst`; si no hay, el agente que creó el artefacto | `artefacto:celda o fila` del último valor (un periodo nuevo es otra clave) / 7 días. Como mucho 5 por revisión (los cambios más grandes primero) |
| `spend_pace` | Gasto del mes (libro de uso `usage` desde el día 1, UTC) proyectado al último día frente a lo **disponible del mes**: tope vigente (`Budget.OrgCap`: tope del operador, endurecido por el plan) menos lo gastado antes del día 1. Con plan mensual eso es la asignación del mes; con tope de por vida, lo que quedaba el día 1. | Usa el mismo libro y el mismo tope que los topes duros. Sin tope (0) no hay señal; con el tope ya agotado tampoco (el presupuesto ya bloquea y lo explica). No evalúa antes de `min_days`. | `finance_treasury` → `accounting` → `analyst` | `org:AAAA-MM` / 7 días |
| `approval_aging` | Aprobaciones `pending` con más de `min_age_hours` (al menos `min_count`). | `approvals.created_at` es fijo. Ojo: las aprobaciones normales se rechazan solas a los 30 min (`ApprovalTimeout`); las que envejecen son sobre todo las de proyectos (`PROJECT_APPROVAL_TIMEOUT=0` espera con recordatorios). | `assistant` → `operations` → `project_manager`; si no, el agente de la más antigua | id de la más antigua / 24 h |
| `quality_drop` | Puntaje de calidad B11 (`quality.Service.Changes`): ventana actual (30 días) frente a la anterior. | Solo agentes con puntaje en **ambas** ventanas (el B11 ya exige muestra mínima), así que nunca se infiere una caída de datos que faltan. | el propio agente | id del agente / 7 días |

**Descartada:** "proyecto recurrente que no llega a su cadencia". El código no guarda recurrencia ni cadencia de proyectos (no hay programación por proyecto); inferirla de fechas de lanzamiento por plantilla sería adivinar.

El texto de la solicitud que se envía al aceptar pide **solo análisis** ("no modifiques el archivo ni apruebes nada", "no apruebes ni rechaces nada", "no cambies topes ni configuraciones"). Los nombres de columnas, artefactos y aprobaciones van entre comillas como datos. El título y el cuerpo se guardan en el idioma de la organización (`OrgConfig.Locale`: es/en/pt) y además en `params`, para que la UI los muestre en el idioma de quien mira (`initiative.signal.*`).

**Controles.** Con el kill-switch activo o el agente en pausa (`controls.Check` sin efectos), esa candidata se omite (`reason: "controls"`) sin gastar presupuesto. Si la iniciativa está apagada, no se evalúa nada.

**Cuándo corre.** Un barrido cada `INITIATIVE_SCAN_INTERVAL` (duración Go, por defecto `15m`, mínimo `1m`; `0`/`off` lo apaga) para cada organización; el primero espera un intervalo completo después de arrancar. `POST /initiatives/scan` (`org:manage`) lo corre al momento y devuelve qué pasó con cada candidata (`raised`, o `reason`: `disabled | duplicate | daily_limit | monthly_limit | agent_limit | controls | no_agent | error`); queda auditado (`initiative.scanned`).

**Configuración** (`signals` dentro de `GET/PUT /settings/initiative`, solo `org:manage` para cambiar, auditado en `initiative.settings_changed`). Un `PUT` puede mandar solo lo que cambia dentro de `signals` (se combina con lo guardado); sin `signals` se conserva lo guardado (clientes anteriores). Los umbrales se validan aunque la señal esté apagada (`400` fuera de rango):

| Campo | Defecto | Rango |
|---|---|---|
| `metric_change.enabled` / `change_pct` / `margin_points` | `true` / 10 / 3 | — / 1..1000 / 0.5..100 |
| `spend_pace.enabled` / `pace_pct` / `min_days` | `true` / 100 / 3 | — / 50..500 / 1..27 |
| `approval_aging.enabled` / `min_age_hours` / `min_count` | `true` / 24 / 1 | — / 1..720 / 1..100 |
| `quality_drop.enabled` / `drop_points` | `true` / 10 | — / 1..100 |

**Límites conocidos.** Las series en horizontal (periodos en columnas) no se leen. Un margen guardado como `1.2` (por 1.2 %) se leería como 120 %. El gasto del mes sale del libro de uso y el de por vida de `requests.cost_usd`; si difieren, "lo disponible" puede desviarse un poco. Con varias réplicas cada una barre por su cuenta: los duplicados se evitan por clave, pero el presupuesto tiene la misma carrera descrita arriba.

**Modo mock** (`NEXT_PUBLIC_MOCK=true`): `frontend/src/lib/mock/initiative-mock.ts` muestra una sugerencia pendiente por señal; aceptar inicia una solicitud simulada normal; los ajustes validan los mismos rangos.

## Piezas

- Dominio y servicio: `backend/internal/application/initiative.go`; señales: `initiative_signals.go`; extracción de series: `backend/internal/artifacts/metrics.go`; calidad: `quality.Service.Changes`; cableado y barrido: `backend/cmd/server/initiative.go`.
- Almacenes: `infrastructure/memory/initiative.go`, `infrastructure/postgres/initiative.go` (migración `787_initiatives.sql`, rollback en `backend/migrations/down/787_initiatives_down.sql`, RLS por organización; la app no puede borrar filas de `initiatives`). Migración `800_initiative_signals.sql` (rollback `backend/migrations/down/800_initiative_signals_down.sql`): columnas `initiative_settings.signals` y `initiatives.params` (JSONB, con defecto; las políticas RLS de 787 las cubren).
- API: `backend/internal/api/initiative.go` (ver `08-api.md` sección 25).
- UI: `InitiativePanel` (Dashboard) e `InitiativeBudgetCard` (Seguridad: presupuesto, señales y "Revisar ahora"), helpers en `frontend/src/lib/initiativeLogic.ts`, claves `initiative.*` en es/en/pt.

## Qué no se verificó

Las pruebas de Postgres (`initiative_pg_test.go`, `initiative_signals_pg_test.go`, incluida la migración 800 abajo y arriba) necesitan `TEST_DATABASE_URL`; sin él se omiten y en esta rama no se corrieron. La UI de señales compila (`tsc`) y sus helpers y el mock tienen pruebas unitarias, pero no se vio en un navegador. El barrido periódico no se observó en un servidor real; se probó `Scan` directamente y por `POST /initiatives/scan`.
