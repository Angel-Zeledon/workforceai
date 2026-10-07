# AI Workforce OS — Spec y contrato (Fase 1: lo tangible)

Producto: oficina virtual 3D donde los empleados son agentes de IA persistentes. Backend Go (dueño de dominio, datos, permisos, eventos), CrewAI en un runtime Python reemplazable, frontend Next.js + React Three Fiber.

## Estructura del monorepo
```
/backend         Go (chi). Dominio, API REST, WebSocket, orquestador, workflows, aprobaciones, auditoría
/agent-runtime   Python FastAPI + CrewAI. Ejecuta tareas de agentes. NO llama sistemas externos
/frontend        Next.js (App Router, TS) + React Three Fiber. Modo oficina 3D y modo dashboard
/docker-compose.yml  postgres, redis, backend, agent-runtime, frontend
```
Fase 1: sin auth (org fija `demo`, id `00000000-0000-0000-0000-000000000001`). Auth/RBAC/multi-tenant real entra en Fase 2, pero TODA tabla ya lleva `org_id`.

## Principios (no negociables)
- Go es la fuente de verdad. El runtime Python solo recibe una tarea+contexto y devuelve un resultado estructurado y/o solicitudes de herramienta (`tool_requests`). Go decide si las ejecuta, pide aprobación o las rechaza.
- Contenido externo (emails, docs, mensajes) se pasa al runtime como datos delimitados, nunca como instrucciones del sistema.
- Cada acción relevante escribe `audit_logs` + emite un evento.
- Modo simulación: si no hay `ANTHROPIC_API_KEY` (o `SIMULATION=true`), el runtime devuelve resultados guionados realistas y con latencias simuladas, para que el demo funcione sin clave.
- Presupuesto: cada llamada al runtime registra tokens/costo; el backend aplica límites por org (`budget_usd`) y profundidad máxima de delegación (5).

## Agentes (seed)
| id | role | nombre | cargo |
|---|---|---|---|
| sales | sales | Valeria Ríos | Gerente de Ventas |
| hr | hr | Marcos Peña | Recursos Humanos |
| legal | legal | Elena Castro | Abogada |
| accounting | accounting | Tomás Vidal | Contador |
| analyst | analyst | Nadia Ortega | Analista |
| operations | operations | Iván Duarte | Operaciones |
| assistant | assistant | Sofía Lara | Secretaria / Asistente Ejecutivo |

Estados: `idle thinking working waiting talking reviewing blocked awaiting_approval completed error`.

## Modelos JSON (API)
```ts
Agent { id, name, role, title, description, state, activity, current_task_id|null, progress /*0-100*/,
        tools: string[], permissions: string[], autonomy: "suggest"|"approve_each"|"rules"|"autonomous",
        metrics: { tasks_completed, tasks_pending, avg_seconds, cost_usd } }
Task { id, request_id, workflow_id|null, title, description, agent_id, status: "pending"|"running"|"blocked"|"awaiting_approval"|"done"|"failed",
       depends_on: string[], parent_task_id|null, created_at, started_at|null, finished_at|null, output|null /*StructuredOutput*/ }
StructuredOutput { summary, findings: string[], metrics: Record<string,string|number>, hypotheses: string[], evidence: string[],
                   recommendations: string[], confidence: number /*0-1*/, suggested_tasks: string[] }
Message { id, conversation_id, from /*agent id|"user"|"system"*/, to /*agent id|"user"|"all"*/, kind: "chat"|"delegation"|"consult"|"answer", text, task_id|null, ts }
Conversation { id, title, participants: string[], request_id|null, last_message_at }
Approval { id, task_id, agent_id, action /*p.ej. "send_proposal"*/, title, details, risk: "low"|"medium"|"high", status: "pending"|"approved"|"rejected", created_at, resolved_at|null }
Report { id, request_id, title, summary, sections: {heading, body}[], contributors: string[], cost_usd, created_at }
ActivityItem { id, ts, agent_id|null, kind /*event type*/, text }
Request { id, text, status: "planning"|"running"|"awaiting_approval"|"done"|"failed", created_at, report_id|null, cost_usd }
```

## REST (`/api/v1`, JSON, CORS abierto en dev)
- `GET /agents`, `GET /agents/{id}` (incluye tareas recientes y conversaciones del agente en `GET /agents/{id}/detail`: `{agent, tasks, conversations, memory: {key,value,scope}[], recent_activity}`)
- `GET /tasks?agent_id=&status=`, `GET /tasks/{id}`
- `POST /requests` body `{text}` → `202 {request_id}`. Dispara el orquestador (planifica, crea tareas con dependencias, ejecuta en paralelo donde pueda).
- `GET /requests`, `GET /requests/{id}` (incluye tasks y plan)
- `GET /conversations`, `GET /conversations/{id}/messages`, `POST /conversations/{id}/messages` body `{text}` (el usuario interviene)
- `GET /approvals?status=`, `POST /approvals/{id}/decision` body `{decision:"approve"|"reject", note?}`
- `GET /reports`, `GET /reports/{id}`
- `GET /activity?limit=100`
- `GET /metrics` → `{tasks_active, tasks_done, approvals_pending, cost_usd, budget_usd, errors, requests_total}`
- `GET /healthz`
- `POST /demo/reset` limpia datos de ejecución y reseed.

## WebSocket `GET /ws`
Cada frame: `{ "id", "type", "ts" /*RFC3339*/, "org_id", "agent_id"?: string, "payload": {...} }`. Al conectar, el servidor envía `{"type":"hello","payload":{"agents":[Agent...]}}`.
Tipos y payload:
- `request.received` `{request_id,text}` · `request.completed` `{request_id,report_id}`
- `plan.created` `{request_id, tasks:[{id,title,agent_id,depends_on}]}`
- `agent.state_changed` `{agent_id,state,activity,task_id|null,progress}` (agent_id también en el envelope)
- `task.created|started|completed|failed|blocked` `{task: Task}`
- `message.sent` `{message: Message}` (incluye comunicación agente→agente: la UI dibuja un “bubble/línea” entre personajes)
- `approval.requested` `{approval}` · `approval.resolved` `{approval}`
- `report.created` `{report: Report}`
- `activity.logged` `{item: ActivityItem}`
- `error` `{agent_id?, message}`
- `metrics.updated` `{metrics}`

## Contrato Go → agent-runtime (HTTP/JSON)
(gRPC queda para después; el contrato de mensajes es el mismo.)
- `POST /v1/plan` `{request_text, agents:[{id,role,title,responsibilities}], budget_usd}` → `{objectives:string[], tasks:[{key,title,description,agent_id,depends_on:[key]}], clarifying_questions:string[]}`
- `POST /v1/run-task` `{task:{id,title,description,agent_id}, agent:{id,role,title,persona,responsibilities,tools}, context:{request_text, dependency_outputs:[{task_id,agent_id,output}], memory:[{scope,key,value}]}, external_content?:string[] }`
  → `{output: StructuredOutput, consults:[{to_agent_id,question}], tool_requests:[{tool,action,args,risk}], usage:{model,input_tokens,output_tokens,cost_usd,duration_ms}}`
- `POST /v1/consult` `{from_agent_id,to_agent_id,question,context}` → `{answer, usage}`
- `POST /v1/synthesize` `{request_text, outputs:[{task_id,agent_id,title,output}]}` → `{title, summary, sections:[{heading,body}]}`
- `GET /healthz` → `{status, mode:"simulation"|"live"}`

## Orquestación (Go)
1. `POST /requests` → evento `request.received`; Sofía (assistant) pasa a `thinking`.
2. `/v1/plan` → tareas con dependencias → `plan.created`, `task.created` por cada una.
3. Scheduler: ejecuta tareas cuyo `depends_on` esté `done`, en paralelo (goroutines + límite de concurrencia). Cada ejecución: `agent.state_changed`→`working`, `/v1/run-task`, guarda output, `task.completed`.
4. `consults` del resultado → mensajes agente→agente (`message.sent`, estado `talking` en ambos) y `/v1/consult`.
5. `tool_requests` con riesgo alto o acción en lista de aprobación (p.ej. `send_proposal`, `send_contract`) → `Approval` pendiente, tarea `awaiting_approval`, agente `awaiting_approval`. Al aprobar, continúa; al rechazar, tarea `blocked`.
6. Al terminar todas: `/v1/synthesize` → Report → `report.created`, `request.completed`.
7. Todo escribe en `audit_logs` y en `activity`.

Escenario guionado para el modo simulación (propuesta de $50,000): Ventas analiza cliente + prepara propuesta (necesita aprobación para enviarla), Legal revisa contrato, Contador margen (31% → 24% real), Analista rentabilidad, Operaciones capacidad (necesita contratar 2 personas), Asistente consolida. Para solicitudes distintas, el plan simulado elige agentes por palabras clave (ventas bajaron → Analista+Ventas+Contador; contratar → HR+Contador+Legal, etc.).

## Frontend
- Next.js App Router + TS, React Three Fiber + drei, Zustand para estado, conexión WS con reconexión; `NEXT_PUBLIC_API_URL`, `NEXT_PUBLIC_WS_URL`.
- Toggle “Modo oficina / Modo dashboard”.
- Oficina 3D: planta de oficina estilizada y profesional (no infantil): escritorios por rol, sala de reuniones, zona de aprobación del usuario. Personajes 3D (low-poly estilizados, construidos proceduralmente con primitivas o GLB si hay) con rig simple y animaciones por estado: idle (respira, mira alrededor), thinking (mano en la barbilla, burbuja), working (sentado, teclea, monitor encendido), talking (gesticula, línea/burbuja hacia el interlocutor; el personaje camina a la mesa de otro si es consulta), reviewing (documento en mano), blocked/error (indicador rojo), awaiting_approval (alza la mano, icono ámbar), completed (check). Etiqueta flotante con nombre, cargo, estado, actividad.
- Click en personaje → panel lateral: estado, tarea actual+progreso, conversaciones, memoria, tareas, reportes, actividad, métricas.
- Barra inferior: caja de comando “Dile a tu empresa…” (POST /requests), feed de actividad, bandeja de aprobaciones.
- Dashboard: KPIs, tareas, workflows/plan, agentes, conversaciones (con intervención del usuario), aprobaciones, reportes, activity feed, costos, errores.
- Estética: videojuego empresarial moderno, oscuro y sobrio, acentos por rol. Toda animación debe derivar de eventos reales.

## Fuera de alcance de la Fase 1
Auth/RBAC real, multi-tenant efectivo, herramientas reales (email/WhatsApp…), memoria vectorial. Todo diseñado para entrar sin romper el contrato.
