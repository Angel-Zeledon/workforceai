# Tests E2E (Playwright)

Pruebas contra el stack completo (`docker compose up --build`): frontend en `localhost:3000`, backend en `localhost:8080`.

```bash
cd e2e
npm install
npx playwright install chromium
npm test                # todo
npm run test:contract   # solo contrato WebSocket (no requiere navegador)
npm run typecheck       # tsc --noEmit
```

Variables opcionales: `FRONTEND_URL`, `API_URL` (default `http://localhost:8080/api/v1`), `WS_URL` (default `ws://localhost:8080/ws`).

## Convención de `data-testid` (el frontend debe adoptarla)

| data-testid | Elemento | Notas |
|---|---|---|
| `agent-<id>` | Personaje/tarjeta de cada agente | `<id>` en `sales`, `hr`, `legal`, `accounting`, `analyst`, `operations`, `assistant`. Debe existir exactamente 1 por agente en la vista de oficina (puede ser un nodo DOM superpuesto/overlay sobre el canvas 3D, p. ej. la etiqueta flotante de drei `Html`). Recomendado: atributo `data-state` con el estado actual. No usar el prefijo `agent-` para ningún otro elemento. |
| `command-input` | Input del chat de la Oficina (canal general) | Visible en el modo oficina. |
| `command-submit` | Botón de enviar al chat de la Oficina | Dispara `POST /messages` con `conversation: "office"`; el backend decide quién responde y, si es una tarea, crea la solicitud (si el backend aún no tiene `/messages` el frontend cae a `POST /requests`). |
| `approval-<id>` | Una aprobación pendiente en la bandeja | `<id>` = id de la `Approval`. Debe contener un botón con texto "Aprobar" (y "Rechazar"). Solo aprobaciones `pending`; desaparece al resolverse. |
| `mode-toggle` | Botón de cambio oficina/dashboard | Debe exponer `data-mode="office"` o `data-mode="dashboard"` (modo activo). |
| `activity-feed` | Contenedor del feed de actividad | Muestra los `activity.logged` en texto. |

Los tests usan `[data-testid^="agent-"]` y `[data-testid^="approval-"]`: no reutilices esos prefijos.

## Tests

- `tests/office.spec.ts`: carga de la oficina, UI base, 7 personajes.
- `tests/request-flow.spec.ts`: solicitud de $50,000 -> aprobación desde la UI -> reporte.
- `tests/dashboard-mode.spec.ts`: cambio de modo.
- `tests/ws-contract.spec.ts`: conecta a `/ws`, ejecuta el escenario y valida envelope y payload de cada evento según `docs/SPEC.md`.

Cada test llama `POST /demo/reset` antes de empezar.

## Convención de `data-testid` del workspace (agent-workspaces.md)

Prefijos `ws-`, `art-`, `dash-`, `files-` (nunca `agent-` ni `approval-`). Implementados en el frontend v1:

| data-testid | Elemento |
|---|---|
| `ws-host` (`data-size` dock/expanded/full, `data-desk`) | Contenedor del escritorio (también en el dashboard, tamaño `full`) |
| `ws-open-desk` | Botón "Abrir escritorio" en la pestaña Archivos del `AgentPanel` |
| `panel-tab-files` | Pestaña "Archivos" del `AgentPanel` (sigue el patrón `panel-tab-*`) |
| `files-item-<artifactId>` | Fila de archivo en esa pestaña |
| `ws-desk` (`data-desk`) | Cuerpo del escritorio (pestañas + paneles) |
| `ws-size-full`, `ws-size-dock`, `ws-close` | Riel del escritorio: pantalla completa, contraer, cerrar agente |
| `ws-tab-<artifactId>` (`data-kind`, `data-active`), `ws-tab-close-<id>` | Pestaña de artefacto y su cierre |
| `ws-tab-project-<projectId>` | Pestaña fija del resumen de proyecto |
| `ws-tab-progress-<artifactId>` (`data-progress`, `data-build-state`) | Anillo de progreso (solo artefactos con `deliverable_id`) |
| `ws-tab-new`, `ws-new-kind-<kind>` (`data-suggested`), `ws-new-template-<templateId>`, `ws-import-input` | Menú "+" |
| `ws-split-toggle`, `ws-pane-<artifactId>`, `ws-locale-select`, `ws-proposals-count` | Cabecera del escritorio y paneles |
| `ws-project-summary` (`data-progress`), `ws-deliverable-<deliverableId>` (`data-progress`, `data-build-state`) | Panel del proyecto |
| `ws-deliverable-nav`, `ws-deliverable-select`, `ws-deliverable-prev`, `ws-deliverable-next` | Navegación entre entregables |
| `ws-dependency-badge-<artifactId>`, `ws-linked-artifacts`, `art-link-<linkId>` | Referencias entre artefactos |
| `art-sheet`, `art-doc`, `art-table`, `art-board`, `art-chart`, `art-pdf`, `art-form`, `art-inbox`, `art-agenda` | Raíz de cada editor/visor |
| `art-sheet-namebox`, `art-sheet-formulabar`, `art-sheet-ref-<cell>` | Hoja: caja de nombre, barra de fórmulas, chip de celda con `AIW_REF` (las celdas llevan `data-cell="B3"`) |
| `art-doclink-<bid>-<linkId>`, `art-embed-<bid>` (`data-target-id`, `data-target-kind`) | Documento: enlace a artefacto e incrustación |
| `art-title`, `art-status` (`data-status`), `art-version`, `art-save-state` (`data-state`), `art-agent-badge` (`data-agent`) | Cabecera del artefacto |
| `art-approve` | Aprobar (solo humano) |
| `art-attach-<agentId>` | Selector de adjunto none/read/propose/edit |
| `art-ask-agent`, `art-ask-submit` | Pedir revisión al agente |
| `art-history-toggle`, `art-version-<n>`, `art-restore-<n>` | Historial |
| `art-export-json`, `art-export-csv` | Exportar |
| `art-conflict`, `art-conflict-mine`, `art-conflict-theirs` | Conflicto de guardado |
| `dash-nav-<section>` (incluye `dash-nav-files`), `dash-file-<id>`, `dash-project-<projectId>` (`data-progress`) | Dashboard: navegación genérica y sección Archivos |

Pendientes del spec (no implementados aún): `ws-chip-<agentId>`, `art-proposal-*`, `art-suggestion-*`, `art-comment-*`, `art-import-input`, `art-export-docx|xlsx|pdf`.

## `data-testid` de Proyectos (frontend/src/components/projects, ver docs/architecture/workflow-visualization.md)

Siguen el catalogo de la sec. 10 del spec. Nuevos o con matices respecto al spec (no usan los prefijos `agent-` ni `approval-`, salvo `approval-group-<action>` que es del spec):

| data-testid | Elemento | Notas |
|---|---|---|
| `nav-projects` | Boton "Proyectos" del header | `data-active`. Se cierra al usar `mode-toggle`. |
| `projects-view` | Pantalla de Proyectos | |
| `project-card-<id>` | Tarjeta de proyecto | `data-status`. Su progreso: `project-card-progress-<id>` (`data-done`, `data-total`). |
| `project-back`, `project-new` | Volver a la lista / abrir el asistente | |
| `new-project-wizard`, `wizard-template`, `wizard-goal`, `wizard-param-<key>`, `wizard-budget`, `wizard-create` | Asistente "Nuevo proyecto" | |
| `project-status` | Estado del proyecto | `data-status`. |
| `project-progress` | Progreso (solo en detalle no borrador) | `data-done`, `data-total`. |
| `project-pause`, `project-resume`, `project-cancel`, `save-as-template` | Control / plantilla | |
| `pv-tab-<health|map|timeline|kanban|lanes|delegation|approvals|costs>`, `pv-hint` | Pestanas | |
| `draft-editor`, `draft-objective-<id>`, `draft-node-count`, `draft-node-title-input`, `draft-node-agent-select`, `estimate-p50`, `estimate-p90`, `validate-ok`, `budget-input`, `underbudget-warning`, `launch-ack-underbudget`, `launch-confirm` | Borrador y lanzamiento | `estimate-*` llevan `data-usd`; `draft-node-count` lleva `data-count`. Hay un input por nodo (usar `.first()`/`nth`). |
| `health-light` (`data-light`), `health-eta-p50`, `health-budget-forecast`, `health-waiting-human` (`data-count`), `critical-path-strip`, `cp-node-<id>` | Salud | |
| `map-canvas` (`data-lod`), `map-node-<id>` (`data-state`, `data-selected`), `map-node-count`, `project-heat-strip`, `map-search-input`, `map-breadcrumb` | Mapa (version simple) | |
| `timeline-row-<id>`, `timeline-now-line`, `timeline-forecast-<id>`, `timeline-zoom-in`, `timeline-zoom-out` | Linea de tiempo | Sustituyen a `timeline-zoom-day/week` (el mock dura segundos). |
| `kanban-scope`, `kanban-scope-required`, `kanban-col-<ready|running|awaiting|waiting|retrying|failed|done>` (`data-count`), `kanban-card-<id>` | Kanban | Alcance obligatorio si hay mas de 60 tareas. |
| `lane-<agentId|human>`, `lane-load-<agentId|human>` (`data-active`, `data-queued`), `lanes-now-line` | Carriles | |
| `depth-badge` (`data-depth`), `depth-limit-badge` | Delegacion | |
| `approval-group-<action>` (`data-count`), `approvals-batch-approve`, `approvals-batch-reject`, `approvals-batch-confirm`, `approvals-batch-typed-confirm`, `approvals-batch-confirm-go`, `project-approval-<id>` | Aprobaciones del proyecto | `project-approval-<id>` no usa el prefijo `approval-`. |
| `costs-budget-input`, `costs-budget-save` | Costos | |
| `node-drawer`, `node-drawer-close`, `explain-headline`, `explain-root-cause-<id>` | Panel de nodo | |
| `project-pill` | Pastilla en la oficina | `data-progress`. |
| `template-card-<id>`, `project-from-template` | Plantillas | |


## `data-testid` de plantillas de workflow y onboarding

Nuevos (no cambian ni reutilizan los de arriba; sin prefijos `agent-` ni `approval-`). Componentes en `frontend/src/components/templates/*` y `frontend/src/components/onboarding/*`.

| data-testid | Elemento | Notas |
|---|---|---|
| `templates-open` | Boton del header que abre la galeria | |
| `wf-template-gallery` (`-close`) | Dialogo de la galeria | `role="dialog"`; `wf-template-gallery-close` lo cierra |
| `wf-template-category-<all\|categoria>` | Pestanas de categoria | `aria-selected` |
| `wf-template-card-<key>` | Tarjeta de una plantilla | `<key>` = `new_client`, `collections`, `proposal`, `month_close`, `daily_briefing` |
| `wf-template-run-<key>` | Boton "Usar plantilla" de la tarjeta | Abre el dialogo de parametros |
| `wf-template-run` (`-close`) | Dialogo de parametros y orden de ejecucion | |
| `wf-template-param-<param>` | Input de un parametro (`client`, `service`, `scope`, `amount`, `month`) | |
| `wf-template-flow`, `wf-template-stage-<n>`, `wf-template-step-<key>` | Vista de etapas; `wf-template-stage-<n>` expone `data-parallel="true"` si hay pasos en paralelo | |
| `wf-template-submit` | Inicia el flujo (`POST /workflow-templates/{key}/instantiate`) | |
| `wf-template-started`, `wf-template-error` | Mensaje de exito / error | |
| `onboarding-open` | Boton del header "Configura tu oficina" (solo si el onboarding no esta completo) | No se abre solo: `NEXT_PUBLIC_ONBOARDING_AUTOOPEN=true` activa la apertura automatica en la primera visita |
| `onb-wizard` (`-close`) | Dialogo del asistente | |
| `onb-step` | Paso actual (`data-step`) | |
| `onb-pack-<key>` | Opcion de tipo de negocio (`professional_services`, `agency`, `commerce`, `general`) | `role="radio"` |
| `onb-locale`, `onb-tone` | Idioma y tono regional (paso 2) | `<select>` |
| `onb-brief-enabled`, `onb-brief-time`, `onb-brief-timezone`, `onb-brief-day-<0-6>` | Resumen diario (paso 3) | |
| `onb-back`, `onb-next`, `onb-finish` | Navegacion | |
| `onb-done`, `onb-done-close`, `onb-error` | Resultado | |
| `org-config-open` | Boton del header "Ajustes del equipo" (onboarding completo) | |
| `cfg-panel` (`-close`), `cfg-message` | Panel de ajustes | |
| `cfg-tone-org`, `cfg-tone-agent-<id>` | Tono de la oficina y por agente | `<select>` |
| `cfg-brief-time`, `cfg-brief-timezone`, `cfg-brief-day-<0-6>`, `cfg-brief-enabled`, `cfg-brief-save`, `cfg-brief-create`, `cfg-brief-delete` | Resumen diario | |
| `cfg-tax`, `cfg-tax-<key>` | Plantillas fiscales de ejemplo (con aviso "ejemplo, no asesoria fiscal") | |

## `data-testid` de Conexiones y Control (prefijos `conn-` y `ctl-`)

Frontend: `frontend/src/components/connections/*` y `components/security/*`. Contrato en `docs/architecture/integrations-credentials.md` sec. 11.6. Los ids siguen el spec; ademas:

- Navegacion: `nav-connections`, `nav-security`, `conn-close`, `conn-screen`, `ctl-screen`, `conn-tab-{connections|matrix|capabilities|usage|limits}`.
- Asistente: `conn-label-input`, `conn-profile-{read|write}` (Gmail: lectura y escritura son conexiones separadas), `conn-oauth-client-id`, `conn-secret-input` (password, `data-sensitive`; en OAuth es el secreto de cliente de "trae tu app"), `conn-assign-{agentId}`, `conn-wizard-done`, `conn-sim-deny-scope` (solo mock).
- Detalle: `conn-drawer`, `conn-drawer-status`, `conn-readonly-toggle`, `conn-revoke-submit`, `conn-sim-agent`, `conn-sim-use-{capability}` (solo mock).
- Permisos: `conn-grant-remove`, `conn-grant-approve` (segunda persona), `conn-grant-second-note`, `conn-grant-no-approve-note`.
- Empleados: `ctl-can-do-{agentId}` (`data-paused`, `data-can-send`), `ctl-can-do-{agentId}-send-rule`.
- Seguridad: `ctl-killswitch` (`data-level`), `ctl-killswitch-dialog`, `ctl-ks-level-{freeze|lockdown}`, `ctl-killswitch-reason`, `ctl-killswitch-confirm`, `ctl-banner` (`data-level`: freeze|lockdown|read_only), `ctl-global-state`, `ctl-release`, `ctl-release-reason`, `ctl-release-resume-all`, `ctl-readonly-toggle`, `ctl-tool-kill-{capability}` (`data-disabled`), `ctl-agent-pause-{id}`, `ctl-agent-resume-{id}`, `ctl-agent-row-{id}`, `ctl-limits-table`.
- Plan: `ctl-plan-review` (seccion), `ctl-plan-review-{id}` (`data-status`, `data-required`), `ctl-plan-required`, `ctl-plan-task-{taskId}-remove`, `ctl-plan-no-external`, `ctl-plan-note`, `ctl-plan-approve`, `ctl-plan-reject`.
- Borrador de correo: `ctl-outbox`, `ctl-draft-{id}` (`data-status`), `ctl-draft-to|subject|body|save|approve|reject`, `ctl-hold-countdown` (`data-seconds`), `ctl-hold-cancel`, y dentro de la tarjeta `conn-approval-account|reversibility|taint|hold`.
- Solo mock: `ctl-sim-viewer-{userId}`, `ctl-sim-admins-{1|2}`, `ctl-plan-simulate`.

Ninguno empieza con `agent-` ni `approval-`. Los secretos nunca aparecen en el DOM ni en el store.

## `data-testid` de Control de costos (prefijo `cost-`)

Frontend: `frontend/src/components/cost/*`. Contrato en `docs/architecture/08-api.md` sec. 12 y `docs/architecture/07-seguridad-costos.md` sec. 5. Ninguno empieza con `agent-` ni `approval-`.

| data-testid | Elemento | Notas |
|---|---|---|
| `cost-estimate-dialog` | Dialogo modal de confirmacion de costo | Aparece solo cuando la solicitud esta en `awaiting_confirmation` (el maximo estimado supera `COST_CONFIRM_THRESHOLD_USD` o el tope de la solicitud). Nada se ejecuta hasta responder. |
| `cost-gate-reason` | Motivo de la confirmacion | |
| `cost-confirm-cap-input` | Input del tope duro opcional (USD) | |
| `cost-confirm-proceed`, `cost-confirm-cancel` | Continuar / cancelar solicitud | `POST /requests/{id}/confirm` |
| `cost-estimate` | Tarjeta de estimacion | `data-basis` = `simulation`/`token_heuristic`/`fallback` |
| `cost-estimate-total` | Rango total "min - max" | Siempre un rango, nunca una cifra |
| `cost-estimate-task-<taskId>` | Fila de rango por tarea | Solo en la tarjeta completa (dialogo) |
| `cost-request-cap` | Tope de la solicitud | Solo si hay tope |
| `cost-alerts` | Contenedor de avisos de presupuesto | |
| `cost-alert-request`, `cost-alert-agent`, `cost-alert-org` | Pausa explicada por tope | `data-scope`; `role="alert"` |
| `cost-raise-input`, `cost-raise-submit` | "Subir tope y continuar" | `PUT /requests/{id}/budget` o `PUT /agents/{id}/budget` |
| `cost-warning` | Aviso al 80% del tope | |
| `cost-breakdown` | Panel de desglose del dashboard (seccion Costos) | |
| `cost-breakdown-total`, `cost-breakdown-scope` | Total y selector de alcance (toda la org / una solicitud) | `<select>` |
| `cost-tab-<agent|task|tool|operation|model>` | Pestana del desglose | `role="tab"`, `aria-selected` |
| `cost-row-<tab>-<id>` | Fila del desglose | |
| `cost-untracked` | Aviso de gasto previo sin desglose | Solo si hay |
| `cost-limits` | Panel de limites de presupuesto | |
| `cost-limit-row-<agentId>`, `cost-limit-input-<agentId>`, `cost-limit-save-<agentId>` | Tope mensual editable por agente | |

Nota: los `data-testid` y `data-mode` existentes no cambian. Con el umbral por defecto (1 USD) el escenario de simulacion no pide confirmacion, asi que los tests existentes no ven el dialogo.



## `data-testid` del chat (prefijos `chat-`, `panel-`, `turn-`, `route-`, `reason-`)

Contrato en `docs/architecture/chat-routing.md`. Frontend: `frontend/src/components/chat/*`, `CommandBar.tsx` (chat de la Oficina) y `AgentPanel.tsx`. Ninguno empieza con `agent-` ni `approval-`.

**Cambio importante: al tocar a un agente (oficina o dashboard) el panel abre PRIMERO en la pestaña `chat` (chat 1:1), ya no en `state`.** Los tests que miraban el estado/tareas/archivos deben hacer clic antes en `panel-tab-state` / `panel-tab-tasks` / `panel-tab-files`. Pestañas del panel: `panel-tab-chat` (por defecto), `panel-tab-state`, `panel-tab-tasks`, `panel-tab-chats` ("Con el equipo": conversaciones entre agentes), `panel-tab-memory`, `panel-tab-reports`, `panel-tab-files`, `panel-tab-activity`, `panel-tab-profile`.

| data-testid | Elemento | Notas |
|---|---|---|
| `chat-pane-agent` | Panel del chat 1:1 (`conversation: "agent:<id>"`) | Dentro de `AgentPanel`, pestaña Chat |
| `chat-input`, `chat-submit` | Input y botón de enviar del chat 1:1 | `POST /messages` con `agent:<id>`; responde solo ese agente |
| `chat-log` | Lista de mensajes (`role="log"`, `aria-live="polite"`) | En el 1:1 y en el chat de la Oficina |
| `chat-msg` (`data-from`, `data-kind`) | Mensaje (burbuja) | `data-kind`: chat/consult/answer/delegation/system |
| `chat-typing` (`data-count`) | Indicador "escribiendo…" (`chat.typing`) | `data-count` = agentes escribiendo |
| `office-chat` (`data-open`) | Panel del chat de la Oficina sobre la barra de comandos | Colapsable |
| `chat-collapse`, `chat-unread` | Botón colapsar/expandir; contador de mensajes nuevos si está colapsado | |
| `turn-chip` (`data-intent`) | Tipo de turno: `smalltalk` (conversación), `question` (pregunta), `task` (tarea), de `route.decided` | En la cabecera del chat de la Oficina y en cada nota de ruta |
| `route-note` (`data-intent`) | Quién responde/aporta y por qué, bajo el mensaje del usuario | Solo en el chat de la Oficina |
| `reason-chip` | "Asignado a X porque …" (`assigned_reason` de la tarea) | Panel del agente (estado/tareas/chat), tarjetas del dashboard y plan de solicitud |

- `tests/chat.spec.ts`: el chat 1:1 es la pestaña por defecto del panel y solo responde ese agente; un saludo en la oficina es `smalltalk` y no crea solicitud.
