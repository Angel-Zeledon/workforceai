# Mientras no estabas (B3)

Cuando una persona vuelve a la oficina después de una ausencia larga (8 horas por omisión), ve una hoja descartable
sobre la oficina con lo que pasó mientras no estaba: primero lo que la espera (aprobaciones que **ella** puede decidir),
luego alertas, problemas, lo terminado, las sugerencias del equipo y las aprobaciones que se resolvieron; el gasto del
periodo, quién trabajó, y un **timelapse** opcional que repite en la oficina, a alta velocidad, los cambios de estado de
los agentes. Se lee en un minuto.

Código: `backend/internal/away` (ventana de la ausencia, armado y orden del resumen, servicio), `backend/internal/api/away.go`
(HTTP), `backend/cmd/server/away.go` (cableado), lectura por ventana en `infrastructure/memory/history.go` y
`infrastructure/postgres/history.go`, marcas en `infrastructure/postgres/away.go`, migración `792_away_marks.sql`
(+ `migrations/down/792_away_marks_down.sql`). UI: `frontend/src/components/away/AwayHost.tsx`, `src/lib/away*.ts`,
simulación en `src/lib/mock/away-mock.ts`. API: sección 27 de `08-api.md`.

## Cuándo estuvo fuera: marcas de última visita

La presencia (`internal/presence`) vive en memoria y muere con el socket, así que no sirve para saber cuándo se fue
alguien ayer. Lo nuevo es una fila por persona y organización (`away_marks`): `last_seen_at`, `visit_started_at`,
`away_since` (fin de la visita anterior) y `dismissed_at`.

- El frontend manda `POST /away-summary/heartbeat` al cargar, cada 5 minutos mientras la pestaña está visible y al volver
  a ella. Un latido después de **30 minutos** de silencio empieza una visita nueva: `away_since = last_seen_at` anterior.
- La ausencia que cubre el resumen es `[away_since, visit_started_at)`, o `[last_seen_at, ahora)` si el latido de la
  visita nueva todavía no llegó. Las dos dan la misma ventana, así que el orden entre `GET` y latido no importa y una
  recarga a los pocos minutos muestra el mismo resumen.
- `GET` no escribe. `dismiss` cierra el resumen solo para la visita en curso; la próxima ausencia lo vuelve a abrir.
- Una ausencia de más de 14 días se resume en sus últimos 14 (`clipped: true`); `away_minutes` sigue siendo la real.
- Primera visita (sin marca): `first_visit: true`, sin resumen.

## Qué cuenta y de dónde sale

Todo se calcula al leer, con datos que la plataforma ya guarda; no hay tabla de resúmenes.

| Dato | Fuente |
|---|---|
| Conteos (solicitudes terminadas, tareas hechas/fallidas/bloqueadas, aprobaciones resueltas, sugerencias, mensajes) | `events` de la ventana, contados por tipo (exacto) |
| Solicitudes fallidas | auditoría `request.status.failed` (el orquestador no emite un evento propio) |
| Ítems de cada sección | `events` de tipos notables, más nuevos primero (máx. 500 leídos, 5 mostrados por sección con `total` exacto) |
| Alertas | `security.alert`, `anomaly.detected`, `control.changed` (kill switch, solo lectura), `agent.control_changed` (pausa), `budget.exceeded`/`budget.warning`, `connection.status_changed` a `needs_reauth`/`error`/`suspended`/`revoked`/`expired` |
| Aprobaciones que esperan | `approvals` pendientes **ahora** (de cuando sean); `new` si se crearon durante la ausencia |
| Gasto | `usage_entries` de la ventana, sumado por agente |
| Timelapse | `events` `agent.state_changed` de la ventana (máx. 5000 leídos, muestreados a 400 conservando el último cambio de cada agente) |

Orden: aprobaciones por riesgo (alto primero), doble aprobación, antigüedad; las secciones van en orden fijo y dentro de
cada una por severidad y luego lo más reciente. Repeticiones del mismo hecho (la misma tarea bloqueada dos veces, la
misma alerta) se pliegan en un ítem con `repeats`. `quiet` es "no pasó nada y nada te espera"; entonces la hoja no se
abre sola. `show = !dismissed && !quiet && ausencia >= min_hours`.

**"Te esperan a ti"** aplica las mismas reglas que `Approvals.Decide`: permiso `approvals:decide`, rango de rol suficiente,
sin autoaprobación cuando la política lo prohíbe, y una persona distinta por cada aprobación de una doble aprobación. Las
demás pendientes se cuentan como `approvals_waiting_other`. Sin `approvals:read` las aprobaciones no aparecen.

## Reglas de la plataforma

- **Sin IA y sin costo.** El resumen es determinista (`method: "deterministic"`) y el párrafo de cabecera lo arma el
  frontend con i18n a partir de los conteos. No se implementó el párrafo opcional generado por el runtime: costaría
  presupuesto por algo que los conteos ya dicen y añadiría el riesgo de cifras inventadas. Si se agrega, debe pasar por la
  misma reserva de presupuesto que el abogado del diablo (B8) y respetar el kill switch.
- **Los agentes nunca deciden.** El resumen solo enlaza; la aprobación se decide con la tarjeta de siempre
  (`POST /approvals/{id}/decision`), con todas sus comprobaciones.
- **Kill switch / solo lectura / pausa:** el resumen no ejecuta nada. Latido y descarte solo escriben la marca personal
  de quien llama, también en modo solo lectura (como un sello de sesión). No se auditan: un latido cada 5 minutos
  inundaría la cadena de auditoría y no son acciones sobre el negocio.
- **Tenencia:** `away_marks` con RLS `tenant_isolation`; todas las lecturas van por organización (`WithOrgTx`).
- **Sin secretos ni contenido:** títulos de tareas/solicitudes/aprobaciones (ya visibles en la app) y un error truncado
  a 200 caracteres; nada de cuerpos de correo ni salidas.

## Frontend

- `AppShell` monta el latido; la oficina monta `AwayHost` (carga el resumen al llegar y al volver a la pestaña) y un botón
  "Mientras no estabas" en el HUD para abrirlo cuando se quiera.
- Se abre solo si `show` y la preferencia de la persona lo permiten: 4, 8 (por omisión, o `NEXT_PUBLIC_AWAY_MIN_HOURS`),
  12, 24 horas o nunca, guardada en este navegador. Nunca encima de la bienvenida del primer uso.
- "Revisar" despliega la tarjeta de aprobación dentro de la hoja; "Ver reporte" muestra el reporte si ya está cargado;
  "Ver empleado" abre su panel; el resto lleva al dashboard.
- Timelapse: aplica los cuadros al estado local de los agentes (los mismos campos que cambia un evento en vivo), así que la
  oficina 3D reacciona como siempre; al terminar o detenerlo recarga los agentes reales del servidor. No envía nada.
- Simulación (`NEXT_PUBLIC_MOCK=true`): una noche verosímil con los agentes del demo y una aprobación pendiente real del
  motor simulado (se puede aprobar o rechazar). Cerrarla se recuerda 12 horas en el navegador.

## Límites conocidos

- Con varias pestañas o dispositivos, la visita es una sola por persona y organización (correcto en la práctica).
- Las solicitudes fallidas salen de la auditoría; si la retención la purgó, ya no cuentan.
- El timelapse muestra estados, no recorridos exactos: los personajes caminan entre zonas según el estado, a alta
  velocidad, y el reloj del cuadro dice cuándo pasó cada cambio.
- La memoria del demo sin Postgres pierde las marcas al reiniciar (la siguiente visita empieza sin resumen).
- Las pruebas de Postgres (`away_pg_test.go`, migración 792 abajo/arriba) están escritas pero **no se ejecutaron** en este
  equipo (sin `TEST_DATABASE_URL`).
