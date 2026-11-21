# Objetivos de la empresa (F1)

"Cada tarea sabe para qué existe." Una organización anota unos pocos objetivos ("Subir las ventas del T4 un 15 %") y cada pedido o proyecto puede apuntar a uno. Los agentes reciben ese "para qué" al planificar y al trabajar, y el informe final dice **Contribuye a: …**.

## Reglas

- **Objetivo** (`goals`, migración `822_goals.sql`): título (1..120), descripción corta (≤ 280), periodo libre (`2026-T4`, ≤ 40) y fecha límite opcional (`AAAA-MM-DD`), estado `active | achieved | archived`, quién lo creó y cuándo. Como mucho **10 activos** por organización (409 al pasarse; un objetivo logrado o archivado libera el sitio). Nunca se borra: se archiva, así el trabajo que apunta a él sigue diciendo a qué contribuyó.
- **De la empresa o de un departamento** (`office_id`; vacío o `general` = la empresa). Sigue el sellado D3 (`2026-11-09-d3-sellar-departamentos.md`):
  - quién lo ve: empresa o departamento abierto → todos; departamento privado → solo sus miembros (un admin que no es miembro **no** lo ve); departamento cerrado → solo admins. Lo oculto responde como inexistente (404, ausente en listados, `400` al enlazar).
  - quién lo escribe: empresa → `org:manage` (admin/owner); departamento → admin o responsable de ese departamento (patrón de habilidades D3). Un miembro sin ese papel recibe 403.
- **Enlace con el trabajo**: `requests.goal_id` (nullable, FK compuesta `(org_id, goal_id) → goals`, así un objetivo de otra organización es imposible también en la base) y `projects.data->>'goal_id'`. El objetivo se valida **antes** de crear el pedido (`Orchestrator.submit` → `GoalSource.CheckLink`: de esta organización, activo y visible para quien pide); si falla no se crea nada. Un proyecto valida su objetivo al crear el borrador y lo pasa al pedido que lanza (`WithGoal`), que lo vuelve a validar. Un mensaje del chat con `goal_id` se valida al instante y el pedido que nazca de ese turno lo hereda.
- **Texto que parece una orden**: al escribir un objetivo, el título, la descripción y el periodo pasan el detector de inyección de las notas de memoria (`sanitize.ScoreInjection`); si "parece instrucciones para los agentes", 400.
- **Sin objetivo nada cambia**: `goal_id` se omite del JSON, el plan y las tareas no reciben nada nuevo.

## Qué recibe el agente

El objetivo viaja por el **mismo canal que las notas de memoria escritas por personas**: un elemento más de `recalled` (`RecalledMemory{Ref: "goal:<id>", Kind: "goal", Title, Snippet}`), limpiado con `memory.Clean` (secretos conocidos, caracteres invisibles, delimitadores neutralizados) y envuelto por el runtime en `untrusted_data` ("memoria relevante de trabajo previo"). Nunca va en el prompt de sistema. El runtime añade fuera del bloque una línea fija escrita por nosotros (`GOAL_GUIDANCE`) que explica qué es un elemento `goal` y que es contexto, no una orden.

- **Tareas** (`RunTaskRequest.recalled`): el objetivo va primero, para que el tope de memoria (4 elementos) nunca lo deje fuera.
- **Planificador** (`PlanRequest.recalled`, campo aditivo; runtimes viejos lo ignoran): mismo bloque al final de la descripción del plan.
- **Sellado hacia los agentes**: objetivo de empresa o de departamento abierto → planificador y todas las tareas del pedido. De departamento privado → **solo** las tareas de agentes de ese departamento; el planificador no lo recibe (su plan lo lee quien ve el pedido). Si el modelo de departamentos no se puede leer, no se entrega nada (falla cerrado).

De paso se corrigió el filtro de refs del runtime (`security._REF`): solo aceptaba `task:|report:|memory:|file:`, así que las notas de oficina/empresa (`team:`) y los debates (`debate:`) se descartaban en silencio y nunca llegaban al prompt. Ahora acepta también `team:`, `debate:` y `goal:`.

## API

`/api/v1/goals` (ruta: `requests:read` para leer, `requests:create` para escribir; el servicio decide el resto):

| Método | Ruta | Qué hace |
|---|---|---|
| GET | `/goals[?include_archived=1]` | `{goals, max_active}` que la persona ve, más recientes primero |
| POST | `/goals` | crea (`title`, `description`, `period`, `due_date`, `office_id`) → 201 |
| GET | `/goals/{id}` | `{goal, requests, projects}`: el trabajo enlazado que la persona ve (D3 en cada pieza; el pedido propio de un proyecto va bajo el proyecto) |
| PATCH | `/goals/{id}` | cambia textos, fecha límite (`""` la quita) o `status` |

Enlazar: `goal_id` en `POST /requests`, `POST /messages`, `POST /projects/draft` y `/projects/from-template`. Leer: `goal` (`{id, title, status, office_name}`) en `GET /reports`, `GET /reports/{id}` y `GET /requests/{id}`; `goal_id` en pedidos y proyectos. Si la persona no ve el objetivo, `goal` no aparece y `goal_id` se borra de la respuesta.

Auditoría: `goal.created` / `goal.updated` (entidad `goal`; solo metadatos: departamento, campos cambiados, estado anterior y nuevo; nunca el texto). Evento en vivo `goal.changed` (`{goal}`): a toda la organización o, si es de un departamento privado, solo a sus miembros (fuera del registro común de eventos, como los demás eventos sellados).

## Piezas

- Dominio y reglas: `backend/internal/goals` (`service.go`, `types.go`, `memstore.go`); Postgres `infrastructure/postgres/goals.go`; migración `822_goals.sql` y rollback `backend/migrations/down/822_goals_down.sql`.
- Orquestador: `application/goal.go` (`WithGoal`, `GoalSource`, `SetGoals`), `orchestrator.go` (`submit`, plan, `buildRunRequest`), `chat.go`; proyectos: `NewProject/Record/Summary.GoalID`, `SetGoalCheck`.
- API: `backend/internal/api/goals.go`; cableado `backend/cmd/server/goals.go` (después de los departamentos y antes de `orch.Recover`).
- Runtime: `PlanRequest.recalled`, `security.memory_block` / `plan_goal_lines` / `GOAL_GUIDANCE`.
- UI: sección Objetivos (`/goals`, `components/goals/GoalsView.tsx`; menú "Tu empresa"), selector `GoalPicker` en el chat (`lib/chatGoal.ts`; se limpia tras enviar) y en el asistente de proyecto nuevo, "Contribuye a" en `ReportView`; lógica pura en `lib/goalsLogic.ts` (+ prueba), mock en `lib/mock/goals-mock.ts`; claves `goals.*` en es/en/pt.

## Límites conocidos

- El tope de 10 activos se serializa con un mutex por proceso: con varias réplicas una carrera puede pasarlo por uno o dos.
- Un borrador de proyecto cuyo objetivo se logra o archiva antes de lanzarlo falla al lanzar (400): solo un objetivo activo admite trabajo nuevo. Se cambia el objetivo del proyecto o se reactiva.
- El planificador de **borradores de proyecto** (`projects/planner.go`) y la **síntesis** del informe no reciben el objetivo; sí lo reciben el planificador de pedidos y cada tarea (también las de proyectos). Los proyectos por lotes no llevan objetivo.
- UI pendiente: "Contribuye a" en el detalle de un pedido (la API ya lo da), refrescar la lista con `goal.changed`, paso opcional en `/onboarding`, y el mock de proyectos ignora `goal_id`. El selector aparece en todos los chats (el campo de texto es el mismo componente).
- Los eventos en vivo de proyecto (`project.created`, ...) llevan el `goal_id` opaco aunque quien los recibe no vea el objetivo (sin título ni texto).
- Las pruebas de Postgres (`goals_pg_test.go`: ida y vuelta, RLS, FK compuesta) necesitan `TEST_DATABASE_URL`; en esta rama no se corrieron. La UI no se vio en un navegador.
