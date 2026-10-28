# Distrito de clientes (B12)

Un despacho contable trabaja para muchos clientes. Cada **cliente** (`Customer`) es un edificio del barrio; el
despacho los ve todos juntos y entra a cada uno para ver su trabajo. Los empleados se **clonan por cliente**
("Contador · Ferretería López") con **memoria aislada**: lo que se hizo para un cliente nunca llega a otro.

Código: `backend/internal/customers` (entidad, clones, vínculos, estado del barrio), `backend/internal/application/customer.go`
(alcance de una solicitud), `backend/internal/drive/customer_scope.go` (aislamiento de la unidad), filtros en
`backend/internal/memory` y `backend/internal/infrastructure/postgres/memory.go`, `backend/internal/api/customers.go`
(HTTP), `backend/cmd/server/customers.go` (cableado). Migración `797_customers.sql` (+ `migrations/down/797_customers_down.sql`).
UI: `frontend/src/components/customers/` (sección "Clientes" del panel), mock en `frontend/src/lib/mock/customers-mock.ts`.

## Modelo

| Objeto | Dónde | Notas |
|---|---|---|
| Cliente | tabla `customers` | nombre (único entre los activos de la organización, sin distinguir mayúsculas), RFC/NIF opcional, color `#rrggbb` (uno por defecto estable si no se da), notas. Se **archiva**, nunca se borra: sus etiquetas siguen aislando sus datos |
| Carpeta del cliente | unidad `/Clients/<nombre>` | se crea al registrar el cliente con `RefID` = id del cliente (si hay unidad) |
| Clon | tabla `customer_assignments` | un agente existente trabajando para un cliente con un nombre visible. Es el MISMO agente: plantilla de rol, herramientas, autonomía, pausa, topes de gasto y puntaje de calidad son los del agente base. Uno por (cliente, agente). Escritorio propio: `/Agents/<agente>/Clients/<cliente>` |
| Solicitud del cliente | tabla `customer_links` (`kind=request`) | se guarda **antes** de crear la solicitud: ninguna tarea corre sin su alcance |
| Proyecto del cliente | `projects.data->>'customer_id'` | se fija al crear el borrador; al lanzarlo, su solicitud queda vinculada al cliente |
| Carpeta vinculada | tabla `customer_links` (`kind=drive_folder`) | cualquier carpeta (no de primer nivel) que una persona asigna a un cliente; todo lo que cuelga de ella es del cliente |
| Artefacto | `artifacts.customer_id` (migración 280) | ya existía; lo que se produce para una solicitud de un cliente (entregables) se etiqueta con ese cliente |
| Memoria | `memory_docs.customer_id` | el cliente para el que se hizo el trabajo recordado |

Solo personas crean clientes, clones y vínculos; todo queda auditado (`customer.created`, `customer.updated`,
`customer.archived`, `customer.clone.created|deleted`, `customer.folder.linked|unlinked`, `customer.request.scoped`).
Los agentes no tienen ninguna herramienta para esto y nunca aprueban nada. En modo solo lectura las escrituras se rechazan.

## Aislamiento (lo aplica el backend, no la UI)

El alcance se resuelve **en el servidor** a partir del id de la solicitud (`customer_links`), en cada lugar donde un agente
recibe datos guardados. Si el alcance no se puede determinar (error de base de datos), se **cierra**: no hay recuerdos ni
archivos.

**Memoria** (`memory.VisibleToCustomer`, mismo filtro en SQL y en memoria, en la búsqueda por palabras y en la semántica):

| Tarea | Ve |
|---|---|
| sin cliente | todo lo que veía antes (compatibilidad) |
| del cliente A | solo documentos con `customer_id = A`, más las notas que una **persona** escribió al agente sin cliente (`/recordar`: instrucciones del despacho). Nunca documentos de B ni trabajo sin cliente (podría contener datos de cualquiera) |

Aplica a la memoria inyectada al empezar la tarea (`Recall`), a `memory.search` (búsqueda y apertura por `ref`) y a los
archivos que la memoria trae de la unidad. El trabajo terminado se etiqueta con el cliente de su solicitud al indexarse.

**Unidad** (`drive/customer_scope.go`, aplica a `fs.list|read|search|write|write_document|edit|move|delete`, a la búsqueda por
pasajes y a la capa de archivos de la memoria):

| Ruta, en una tarea del cliente A | Lectura | Escritura |
|---|---|---|
| `/Agents/<yo>/Clients/A/...` (escritorio del clon) | sí | libre |
| `/Projects/<proyecto de A>` asignado | sí | libre |
| `/Clients/<carpeta de A>`, carpetas vinculadas a A, proyectos de A no asignados | sí | con aprobación humana |
| `/Shared` (material del despacho curado por personas) | sí | rechazada |
| cualquier cosa de B (carpeta, proyecto, carpeta vinculada, escritorio de clon) | no | rechazada (`customer_isolation`) |
| escritorio general del agente, proyectos sin cliente, carpetas de `/Clients` sin cliente | no | rechazada |

Gana el dueño más profundo: una carpeta de B dentro de `/Shared` o de una carpeta de A sigue siendo de B, y los resultados de
búsqueda bajo una raíz legible se filtran otra vez por ruta. `fs.list` no nombra carpetas de otros clientes. Una tarea sin
cliente conserva la política anterior (`docs/architecture/drive.md`). Como la escritura fuera del cliente se rechaza, un clon
no puede copiar datos de A a un lugar que B lea.

**Artefactos referenciados en el plan** (W3, `artifact:<id>`) y **celdas citadas** (B6): un proyecto de A solo puede
referenciar artefactos etiquetados A. Uno de B, o uno sin cliente (puede contener datos de cualquiera; no existe una marca de
"compartible" para artefactos del despacho), se rechaza **al escribir el plan** con `400 customer_isolation`: al crear el
borrador (objetivo y textos de la plantilla), al editarlo (`PATCH` del plan: un título llega a la descripción de la tarea), al
proponer un cambio de plan y otra vez al lanzarlo (`projects/customer_refs.go`). Una referencia a un artefacto que no existe se
deja al filtro de la tarea. Sin el lector de artefactos, un plan de cliente con referencias se rechaza. **Al correr la tarea**
se filtra de nuevo (`artifacts/context_source.go`): el alcance sale del id de la solicitud (y si la ejecución trae un cliente en
contexto, nunca se amplía); si no se puede resolver, no se entrega ningún artefacto. Las salidas de la misma solicitud siempre
pasan (mismo cliente por construcción). El mismo filtro aplica a `ResolveCell`, así que una cita B6 `cell:` de una tarea de A
nunca se verifica contra una hoja de B; las citas `file:` y `memory:` solo se verifican contra lo que la tarea leyó o recordó, que
ya pasó por el aislamiento de la unidad y de la memoria.

**Chat**: un mensaje puede ir "para un cliente" (`POST /messages` con `customer_id`). El cliente se valida antes de guardar el
mensaje (`404`/`409`, no se guarda nada) y viaja con el turno; si el turno crea una solicitud, el orquestador guarda su vínculo
**antes** de crearla, igual que `POST /requests`. En la UI, mientras la vista de un cliente está abierta, la caja del chat muestra
"Para el cliente: X" (con "Quitar") y envía sus mensajes con ese cliente; al salir de la vista se quita. También el comando
`/cliente <nombre>: <tarea>` (`/customer`, `/client`; el nombre por id, nombre exacto sin acentos, el nombre más largo con que
empiezan las palabras si no hay dos puntos, o un prefijo único). Las respuestas conversacionales del chat no recuerdan memoria,
así que no hay otra vía por la que un turno vea datos de un cliente.

**Planificación**: si el cliente tiene clones, el planificador de una solicitud del cliente solo ve a esos agentes (más la
asistente); sin clones, a todos.

Pruebas de fuga cruzada: `backend/internal/customers/isolation_test.go` (recuerdo, `memory.search` y apertura, `fs.*` por la
pasarela, capa de archivos de la memoria, alcance desconocido), `artifact_isolation_test.go` (artefactos referenciados y celdas
B6 al correr la tarea, alcance desconocido), `history_test.go` (asignación explícita de trabajo anterior), en
`internal/projects/customer_test.go` el rechazo al escribir el plan, en `internal/application/customer_test.go` las solicitudes
nacidas en el chat, en `internal/api/customers_chat_api_test.go` `/cliente` y `POST /messages` y, en Postgres,
`customers_pg_test.go` (RLS, filtro SQL y cambio de etiqueta). Las pruebas de artefactos, plan y chat se comprobaron desactivando
el filtro: fallan.

## Trabajo anterior al distrito

Lo hecho antes de B12 no tiene cliente y una tarea de cliente no lo recuerda. **No se reetiqueta en silencio**: una persona con
`files:grant` lo asigna explícitamente (`POST /customers/history`, sección "Trabajo anterior sin cliente" de la vista del
cliente, con la lista de `GET /customers/history/candidates`), y puede deshacerlo con `customer_id: ""` (botón "Quitar del
cliente" en los proyectos y solicitudes terminados del cliente).

| Qué | Efecto |
|---|---|
| Solicitud terminada | se guarda su vínculo (`customer_links`) y sus documentos de memoria pasan de `''` al cliente; deshacer hace lo contrario |
| Proyecto en borrador o terminado | `projects.data->>'customer_id'` y, si se lanzó, su solicitud como arriba; un borrador debe cumplir la regla de artefactos |
| Un documento de memoria | solo si su solicitud no tiene cliente (si la tiene, el documento sigue a su solicitud) |

Nunca se mueve trabajo en curso (sus tareas resuelven el alcance mientras corren) y no se pasa directamente de un cliente a otro:
hay que quitar el primero. Auditoría `customer.history.assigned|unassigned` con `from`, `to`, `memories` y `request_id`, más
`project.customer_changed` en el proyecto. Código: `customers/history.go`, `cmd/server/customers.go` (`customerHistory`),
`memory.CustomerRetagger` (memoria y Postgres).

## Barrio

`GET /customers/district` da cada cliente activo con su estado, derivado de sus proyectos y de sus 20 solicitudes más
recientes (`customers.HealthOf`):

| Estado | Cuándo |
|---|---|
| `blocked` | algo falló en los últimos 7 días (proyecto o solicitud fallidos, tareas fallidas en un proyecto activo) o hay un proyecto en pausa |
| `waiting_approval` | un proyecto espera a una persona (compuerta o aprobaciones pendientes) o una solicitud espera aprobación |
| `on_track` | hay trabajo en curso |
| `idle` | nada activo |

La vista es un mapa isométrico en SVG (sin recursos 3D nuevos): cada edificio lleva el color del cliente, una luz de estado y
pisos según clones y proyectos activos; cada edificio es un botón accesible y abajo hay una lista equivalente. Al entrar se ve
la vista del cliente: clones, proyectos, solicitudes recientes, un cuadro para pedir trabajo **para ese cliente**
(`POST /requests` con `customer_id`), alta y baja de clones, edición y archivo.

## Límites conocidos

- La etiqueta de la memoria se fija al indexar; cambiarla después solo es posible con la asignación explícita de trabajo
  anterior (arriba), nunca automática.
- `/Shared` es legible por todos los clientes: lo que una persona ponga ahí es material del despacho.
- La carga de trabajo del clon (puntaje, gasto, pausa) es la del agente base: no hay topes por cliente.
- Los artefactos sin cliente (creados a mano sin `customer_id`, o antes de B12) no llegan a tareas de cliente; no hay una marca
  de "compartible" ni una acción para etiquetar artefactos existentes (asignar un proyecto no reetiqueta sus artefactos).
- Los comandos de chat distintos de `/cliente` (`/encargar`, `/reunion`, entregables...) no heredan el cliente de la caja del chat.
- En el modo de demostración (mock) el chat valida el cliente pero no vincula la solicitud, y solo las solicitudes se pueden
  asignar como trabajo anterior.
- El cambio de etiqueta en Postgres tiene prueba (`customers_pg_test.go`) que requiere `TEST_DATABASE_URL`.
- El modo de código "escritorio" usa el escritorio del clon y la carpeta del proyecto del cliente, igual que `fs.*`.
