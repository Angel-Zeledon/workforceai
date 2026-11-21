# F1. Objetivos de la empresa — plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:test-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** cada pedido o proyecto puede apuntar a un objetivo de la empresa ("Subir las ventas del T4 un 15 %"). El agente recibe ese "para qué" como contexto al planificar y al ejecutar, y el informe final dice "Contribuye a: …".

**Architecture:** paquete nuevo `backend/internal/goals` (tipos, reglas de visibilidad, servicio, almacén en memoria) + `postgres.GoalStore` (migración `822_goals.sql`). El vínculo vive en el trabajo: `requests.goal_id` (columna nullable con FK compuesta `(org_id, goal_id)` → `goals`) y `projects.data->>'goal_id'` (el documento JSONB del proyecto, como `customer_id`). El orquestador sigue el patrón del cliente (B12): `application.WithGoal(ctx, id)` + un puerto `GoalSource` que valida el objetivo **antes** de crear el pedido y entrega la nota para el agente. Un proyecto pasa su objetivo al pedido que lanza.

**Canal hacia el agente:** el objetivo llega como un elemento más de `recalled` (el mismo canal que las notas de memoria escritas por personas, `RecalledMemory`, `Kind: "goal"`, `Ref: "goal:<id>"`), limpiado con `memory.Clean` (mismos sospechosos y neutralización) y envuelto por el runtime en `untrusted_data`. Nunca en el prompt de sistema. Al planificar viaja en `PlanRequest.recalled` (campo aditivo). Una línea fija (escrita por nosotros) explica al modelo qué es un elemento `goal`.

**Tech stack:** Go (chi, pgx, memstores), Python FastAPI (pydantic), Next.js 15 / TS, i18n es/en/pt.

## Restricciones globales

- **Sellado D3.** Un objetivo con `office_id` es de ese departamento. Si el departamento es privado, solo sus miembros lo ven (también para admins no miembros); lo oculto responde como inexistente (404 en detalle, ausente en listados, `400 goal_invalid` al enlazar). Un departamento cerrado (sin fila en `offices`): solo admins. Sin servicio de oficinas (demo abierta): todo visible y no hay objetivos de departamento.
- **Al agente:** objetivo de empresa o de departamento abierto → a todas las tareas del trabajo y al planificador. De departamento privado → solo a las tareas de agentes de ese departamento; el planificador no lo recibe (su plan lo puede leer quien ve el pedido).
- **Permisos:** leer `requests:read` + visibilidad. Crear/editar `requests:create` en la ruta y, en el servicio: objetivo de empresa → `org:manage` (admin/owner); de departamento → admin o responsable de ese departamento (patrón de habilidades D3).
- **Límites:** 10 objetivos activos por organización; título 1..120, descripción ≤ 280, periodo ≤ 40 caracteres; fecha límite opcional `AAAA-MM-DD`. Estados `active | achieved | archived`. Solo un objetivo activo admite trabajo nuevo.
- **Sin objetivo = idéntico a hoy:** `goal_id` se omite del JSON (`omitempty`), no se añade nada a `recalled` ni al plan.
- Auditoría `goal.created | goal.updated` (metadatos, sin texto). Evento WS `goal.changed`: a toda la org o, si es de un departamento privado, solo a sus miembros (fuera del registro común).
- Copia en es/en/pt; nunca métricas de ahorro. Sin Docker ni servidores.

## Foco de revisión

1. Admin no miembro de Legal privada: no ve el objetivo de Legal en `/goals`, `/goals/{id}` (404), ni el "Contribuye a" de un informe; no puede enlazarlo (400).
2. Objetivo de otra organización en `POST /requests` o `POST /projects` → 400 y no se crea nada (además la FK compuesta lo impide en Postgres).
3. Un agente de General en un pedido enlazado a un objetivo privado de Legal **no** recibe el texto del objetivo.
4. Sin objetivo: `RunTaskRequest.recalled` y `PlanRequest` idénticos a antes.
5. El runtime acepta refs `goal:` (y, de paso, `team:`/`debate:`, que hoy se descartaban).

---

### Task 1: dominio y servicio `goals`
- [ ] Pruebas: validación de textos/estado/límite de 10 activos; visibilidad (empresa, abierta, privada miembro/no miembro, cerrada); permisos de escritura (admin, responsable, miembro no responsable); `CheckLink` (inexistente, otra org, archivado, oculto); `AgentNote` (privado solo a su departamento; planificador sin privados). Commit `feat(goals): company goals with department sealing`.

### Task 2: almacén Postgres y migración 822
- [ ] `822_goals.sql` (tabla + RLS + `requests.goal_id` + FK compuesta + índice), down `822_goals_down.sql`, `postgres.GoalStore`, `goal_id` en `reqCols`/`CreateRequest`. Prueba `goals_pg_test.go` (solo con `TEST_DATABASE_URL`). Commit `feat(goals): postgres store and migration 822`.

### Task 3: orquestador
- [ ] `WithGoal/GoalFrom`, `GoalSource`, `SetGoals`; `submit` valida y guarda `req.GoalID`; `PlanRequest.Recalled`; `buildRunRequest` antepone la nota. Pruebas en `application` con runtime falso: con objetivo llega a plan y tarea; objetivo inválido → no se crea pedido; sin objetivo → sin cambios. Commit `feat(goals): the agents know which goal their work serves`.

### Task 4: proyectos
- [ ] `NewProject.GoalID`, `Record.GoalID`, `Summary.GoalID`; validación en `CreateDraft` (puerto `GoalCheck`); `Launch` pasa `WithGoal`. Commit `feat(goals): projects carry their goal to the request they launch`.

### Task 5: API
- [ ] `/api/v1/goals` (GET lista, POST, GET detalle con trabajo enlazado visible, PATCH), `goal_id` en `POST /requests`, `goal` en `GET /requests/{id}`, `/reports`, `/reports/{id}`; cableado en `cmd/server`. Pruebas API: aislamiento entre orgs, sellado, enlace rechazado, "Contribuye a". Commit `feat(goals): REST API, audit and live event`.

### Task 6: runtime
- [ ] `PlanRequest.recalled`; `_REF` acepta `goal|team|debate`; línea guía del objetivo; bloque en el prompt del planificador. Pruebas pytest. Commit `feat(runtime): goals reach the planner and the tasks as data`.

### Task 7: UI
- [ ] Sección "Objetivos" (lista, crear, logrado, archivar, trabajo enlazado), selector opcional al crear pedido y proyecto, "Contribuye a: …" en el informe; i18n es/en/pt; mock. Commit `feat(ui): company goals`.

### Task 8: docs y verificación
- [ ] `docs/architecture/goals.md`, `08-api.md`; `go test`/`go vet` de los paquetes tocados, pytest, `node --test`, `npm run build`. Commit `docs: company goals (F1)`.
