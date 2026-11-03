# Lotes para varios clientes y "Necesita tu ayuda" — plan de implementación

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** que un agente pueda pedir ayuda a mitad de una tarea y la persona responda desde "Pendientes" (A); lanzar una
plantilla para muchos clientes como un lote (B); y ver en qué va cada proyecto con sus archivos (C).

**Architecture:** el runtime devuelve un `help_request` estructurado junto al resultado; el orquestador lo convierte en
una *pregunta* persistida (tabla `task_questions`), pone la tarea en `needs_input`, cede su hueco del planificador
(`YieldSlot`) y espera la respuesta como espera una aprobación de proyecto (recordatorios, nunca decide solo). Responder
vuelve a llamar al runtime con las respuestas (`help_answers`); reasignar cambia de agente; omitir calcula el impacto
sobre los objetivos y deja el proyecto "terminado en parte" si hace falta. El lote (B) es un registro nuevo que agrupa
N proyectos normales (uno por cliente) y los lanza a ritmo K, reutilizando `Launch` de proyectos.

**Tech Stack:** Go 1.2x (chi, pgx), Python 3.12 (FastAPI, pydantic, CrewAI), Next.js 15 / React 19 / zustand / Tailwind.

**Spec:** `docs/superpowers/specs/2026-11-03-proyectos-por-cliente-y-ayuda-design.md`

## Global Constraints

- Migraciones aditivas e idempotentes desde `810_…` en `backend/internal/infrastructure/postgres/migrations/` con su
  `backend/migrations/down/<n>_<name>_down.sql`; tablas con `org_id` + RLS `tenant_isolation` (patrón de `540_standing_approvals.sql`).
- Ninguna llamada real a proveedores de IA en pruebas; el runtime se prueba con el motor de simulación y dobles.
- Textos de la UI en `frontend/src/lib/i18n/{es,en,pt}.json`; `node scripts/check-i18n.mjs` pasa.
- Pruebas `.test.mjs` nuevas se suman a `test:unit` de `frontend/package.json`.
- No tocar `frontend/src/components/office/**`, `docs/architecture/office-editor.md`, `.env`, cobros, landing.
- Un lote cuenta como **un** proyecto activo del plan. Máximo de clientes por lote y K por plan (defaults: 50 / 3; Pro
  200 / 5; Empresa 1000 / 10), configurables.
- El agente nunca decide por falta de respuesta; recordatorios a las 24 h, luego cada día durante 3 días, luego semanal.
- Varias preguntas del mismo grupo (lote, si no proyecto, si no solicitud) dentro de 3 minutos se juntan en un aviso.

## Review Focus

- Respuesta llega mientras el backend se reinicia: la tarea en `needs_input` debe volver a esperar la misma pregunta
  (no preguntar de nuevo ni correr sin respuesta). Prueba: `TestQuestionSurvivesRestart` (Task A3).
- El agente pregunta en bucle: a la 4.ª pregunta de la misma tarea, la tarea falla con explicación (recuperable) en vez
  de esperar para siempre. Prueba: `TestFourthQuestionFailsTask` (Task A2).
- Responder dos veces / responder una pregunta ya cerrada (doble clic, respuesta en lote solapada): 409, una sola
  continuación. Prueba: `TestAnswerTwiceConflicts` (Task A1).
- Omitir lo único que lleva al resultado: rechazado con 409 y la explicación, aunque el cliente mande `confirm=true`.
  Prueba: `TestSkipRefusedWhenAllObjectivesLost` (Task A4).
- Cancelar el proyecto mientras una tarea espera respuesta: la pregunta se cierra y la tarea se detiene sin llamar al
  modelo. Prueba: `TestProjectCancelClosesQuestions` (Task A5).

---

## Parte A — "Necesita tu ayuda" de punta a punta

### Task A0: runtime — el agente puede pedir ayuda

**Files:** Modify `agent-runtime/app/models.py`, `app/security.py` (`build_task_prompt`), `app/crewai_engine.py`
(`run_task`), `app/simulation.py` (`run_task`), `app/main.py` (`scrub_run_task`). Test: `agent-runtime/tests/test_help_request.py`.

**Interfaces — Produces (contrato JSON con el backend):**
- `HelpRequest{kind: "data"|"file"|"decision"|"contradiction"|"other", what_happened: str, need: str, options: list[str] (≤5)}`
- `HelpAnswer{question: str, answer: str, by: str = ""}`
- `RunTaskRequest.help_allowed: bool = False`, `RunTaskRequest.help_answers: list[HelpAnswer] | None`
- `TaskResult.help_request` / `RunTaskResponse.help_request: HelpRequest | None`

- [ ] Tests: `test_prompt_offers_help_only_when_allowed`, `test_prompt_includes_answers_as_guidance`,
  `test_sim_marker_asks_then_continues_with_answer` (`[sim:help]` en el título: sin respuestas → `help_request` con
  opciones; con `help_answers` → sin `help_request`), `test_help_request_dropped_when_not_allowed`,
  `test_help_request_is_scrubbed` (un secreto en `need` sale redactado).
- [ ] Implementar; `help_request` se descarta si `help_allowed` es falso. Las respuestas van al prompt como datos de una
  persona autorizada, nunca como autoridad para herramientas.
- [ ] `agent-runtime/.venv/Scripts/python -m pytest -q tests/test_help_request.py` → PASS.

### Task A1: backend — preguntas (dominio, almacén, servicio)

**Files:** Create `backend/internal/application/questions.go`, `questions_test.go`; Create
`backend/internal/infrastructure/memory/questions.go`, `postgres/questions.go`, migración `810_task_questions.sql` + down.
Modify `backend/internal/domain/domain.go` (`TaskNeedsInput = "needs_input"`, eventos `task.needs_input`,
`question.answered`, `question.reminder`, `help.notice`).

**Interfaces — Produces:**
- `type Question struct{ID, TaskID, RequestID, AgentID, ProjectID, Kind, WhatHappened, Need string; Options []string; Status string; AskedFor string; CreatedAt time.Time; AnsweredAt *time.Time; AnsweredBy, AnswerKind, Answer, FilePath, ReassignTo string; Reminders int}`
- estados `open|answered|reassigned|skipped|cancelled`; `AnswerKind` `text|option|file|reassign|skip`
- `type QuestionStore interface{ CreateQuestion; GetQuestion; UpdateQuestion; ListQuestions(ctx, org, status string); ListQuestionsByTask(ctx, org, taskID) }`
- `type Questions struct` con `Ask(ctx, q) (Question, <-chan Question, error)`, `Attach(ctx, id)`, `Resolve(ctx, id, Resolution) (Question, error)`, `Wait(ctx, ch, q) (Question, error)`, `CancelRequest(ctx, requestID, note)`
- `NextReminder(created time.Time, n int) time.Time` — n=1: +24 h; n=2..4: +24 h cada uno; después +7 días.
- Agrupador de avisos `helpCoalescer` (ventana 3 min, clave grupo+persona) que emite `help.notice{asked_for, group, count, question_ids, reminder}`.

- [ ] Tests: `TestNextReminderCadence`, `TestAnswerWakesWaiter`, `TestAnswerTwiceConflicts`, `TestAttachAnsweredReturnsAtOnce`,
  `TestHelpNoticesCoalesceWithinWindow` (3 preguntas del mismo grupo → 1 aviso inmediato + 1 resumen "2 más"; otro grupo → aviso propio).
- [ ] Implementar memoria + Postgres (+ migración con RLS) y el servicio. `go test ./internal/application/ -run Question` → PASS.

### Task A2: orquestador — la tarea se detiene, espera y sigue

**Files:** Modify `backend/internal/application/orchestrator.go` (`runTask` → bucle con `runTaskOnce`), `ports.go`
(`RunTaskRequest.HelpAllowed/HelpAnswers`, `RunTaskResponse.HelpRequest`), `durable.go` (reanudar `needs_input`),
`queries.go` (needs_input cuenta como activa). Create `backend/internal/application/help_flow.go`, test `help_flow_test.go`.

**Interfaces — Consumes:** A1. **Produces:** `(*Orchestrator).SetQuestions(*Questions)`; `MaxQuestionsPerTask = 3`.

- [ ] Tests: `TestAgentAsksAndContinuesWithAnswer` (la tarea pasa a `needs_input`, no hay más llamadas al runtime hasta
  responder, la segunda llamada lleva `help_answers`, la tarea termina y el historial guarda quién respondió),
  `TestIndependentBranchKeepsRunning` (con MaxParallel=1 la rama independiente termina mientras se espera),
  `TestReassignAnswerRunsWithOtherAgent`, `TestFourthQuestionFailsTask`, `TestNoQuestionsServiceKeepsOldBehavior`.
- [ ] Implementar. `go test ./internal/application/ -run 'Help|Question'` → PASS.

### Task A3: reanudar tras reinicio

**Files:** `durable.go`; test en `help_flow_test.go`.
- [ ] `TestQuestionSurvivesRestart`: tarea en `needs_input` + pregunta abierta → `Recover` → espera sin llamar al runtime;
  al responder, sigue y termina.

### Task A4: omitir con sentido (impacto por objetivos) y "terminado en parte"

**Files:** Create `backend/internal/application/help_skip.go` (+ test). Modify `projects/view.go`, `projects/types.go`
(`StateNeedsInput`, `StatusPartial = "partial"`, `Summary.NeedsInput`), `projects/engine.go` (resolver de objetivos).

**Interfaces — Produces:**
- `type SkipImpact struct{ Skipped []TaskRef; ObjectivesLost []ObjectiveRef; AllLost, Allowed bool }`
- `type ObjectiveResolver interface{ ObjectivesOfRequest(ctx, org, requestID string) (map[string]ObjectiveRef, bool) }` (projects lo implementa; sin él la solicitud entera es un objetivo)
- `(*Orchestrator).QuestionImpact(ctx, questionID) (SkipImpact, error)`
- Un objetivo se pierde cuando **todas** sus tareas finales (las que ninguna otra del mismo objetivo usa) quedan omitidas.

- [ ] Tests: `TestSkipImpactNothingDepends`, `TestSkipImpactSomeObjectivesLost`, `TestSkipRefusedWhenAllObjectivesLost`,
  `TestSkipMarksDependentsOmitted` (dependientes `blocked` con `metrics.omitted_by`), projects:
  `TestProjectPartialWhenObjectiveOmitted`, `TestNodeNeedsInputState`.
- [ ] Implementar. `go test ./internal/application/ ./internal/projects/` → PASS.

### Task A5: API, avisos y cancelación

**Files:** Create `backend/internal/api/questions.go` (+ `questions_api_test.go`). Modify `api/router.go` (montaje),
`push/service.go` (`help.notice` → push a `asked_for`, o a owners/admins si vacío), `notify/service.go` + `messages.go`
(correo/Slack/Teams con texto "Un agente necesita tu ayuda"), `projects/engine.go` (cancelar → `CancelRequest`),
`cmd/server/main.go` (cableado), `docs/architecture/08-api.md`, `approvals-inbox.md`, `workflow-visualization.md`.

**Endpoints:** `GET /questions?status=open` (tasks:read) · `GET /questions/{id}` (+`impact`) · `POST /questions/{id}/answer`
`{kind, text?, option?, file_path?, agent_id?, confirm?}` (tasks:manage) · `POST /questions/answer-batch` `{ids, kind, text|option}`.

- [ ] Tests: `TestQuestionsAPIListAndAnswer`, `TestAnswerBatch`, `TestSkipNeedsConfirm` (sin `confirm` → 409 con impacto),
  `TestProjectCancelClosesQuestions`, `TestHelpNoticePushedToAsker`.
- [ ] `go build ./... && go vet ./...`; pruebas por paquete.

### Task A6: frontend — "Pendientes"

**Files:** Create `frontend/src/lib/questionsLogic.ts` + `questionsLogic.test.mjs`, `frontend/src/components/approvals/QuestionCard.tsx`.
Modify `lib/types.ts`, `lib/api.ts`, `lib/store.ts` (eventos), `components/ApprovalsTray.tsx` (título "Pendientes",
preguntas arriba), `components/approvals/ApprovalsInbox.tsx` (sección de preguntas agrupadas por proyecto con
selección múltiple y respuesta en grupo), `components/projects/shared.tsx` (estado `needs_input`, estado `partial`), i18n.

**Interfaces — Produces:** `groupQuestions(qs) → [{key, label, items}]`, `waitingLabel(seconds)`, `skipVerdict(impact)` → `"confirm"|"refuse"`.
- [ ] Tests node: agrupación por proyecto/lote, etiqueta de espera, veredicto de omitir.
- [ ] `npx tsc --noEmit`, `npm run test:unit`, `node scripts/check-i18n.mjs`.

## Parte B — Lotes para varios clientes

### Task B1: campos de contacto del cliente — migración `813_customer_contact.sql` (`contact_name, email, phone`),
`customers` tipos/servicio/API/UI. Tests: `TestCustomerContactFields`.
### Task B2: importar con vista previa — `customers/import.go`: `PreviewImport(rows) → [{row, match: existing|new|error, customer_id, reason, missing_fields}]`
(coincide por correo, luego número fiscal, luego nombre normalizado). Tests: `TestImportMatchByEmailThenTaxThenName`, `TestImportRowErrors`.
### Task B3: lote — `projects/batch.go` + migración `814_project_batches.sql`: `CreateBatch(template, params, customer_ids, k)`
crea un proyecto por cliente (con `customer_id`, `{{cliente.*}}` sustituido), estimación total, máximo por plan, cuenta
como un activo; `batchPump` lanza hasta K a la vez. Tests: `TestBatchCountsAsOneActive`, `TestBatchRunsAtMostK`, `TestBatchMissingCustomerField`.
### Task B4: vista del lote — `GET /project-batches/{id}` con filas (cliente, avance, paso actual, agente, estado, costo, espera)
y acciones pausar/reanudar/cancelar cliente/reintentar fallidos; UI `BatchView.tsx`, botón "Lanzar para varios clientes".

## Parte C — Seguimiento simple y archivos por nodo

### Task C1: archivos por nodo — `Node.Files []FileRef` desde el resultado de la tarea; `NodeDrawer` los lista.
### Task C2: vista "Seguimiento" por defecto en `ProjectDetail`: lista de tareas (agente, estado, desde cuándo, archivos)
con "Necesita tu ayuda" arriba; las 9 vistas avanzadas siguen.

---

## Estado de la entrega (2026-11-03)

- **A (hecho):** A0–A6. Además: la tarea guarda en `output.files` lo que escribió (C1) y el panel del nodo muestra la
  pregunta abierta. Pendiente: aviso semanal como resumen con lista (hoy es un aviso por grupo y día); los canales de
  correo/Slack/Teams van a los destinos de la organización, no a la persona.
- **B (hecho):** B1–B4 (contacto del cliente, importación con vista previa y correcciones, lote con ritmo K y un solo
  activo, tabla del lote con filtros y acciones, tarjeta en Proyectos, botón en las plantillas). Pendiente: "Repetir con
  otros clientes" desde un proyecto terminado; el indicador de proyectos de la oficina (no se tocó la oficina); leer
  .xlsx directamente (se acepta CSV o pegar desde Excel).
- **C (hecho):** C1 y C2 (pestaña "Seguimiento" por defecto).
