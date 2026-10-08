# 08 - API

Base: `/api/v1`, JSON. Todo lo definido en el SPEC se **mantiene identico** (marcado **[SPEC]**). Lo demas es extension aditiva, por fase.

## 1. Convenciones

| Tema | Regla |
|---|---|
| Auth | Fase 1: ninguna (org `demo`). Fase 2: `Authorization: Bearer <JWT>`; `org_id` del token, **nunca** del body/URL. WS con ticket (03). |
| IDs | uuid; agentes por `slug` (ej. `sales`) como en el SPEC. |
| Errores | `application/problem+json`: `{type, title, status, code, detail, request_id, errors?:[{field,message}]}`. Codigos estables: `validation_failed`, `not_found`, `forbidden`, `conflict`, `budget_exhausted`, `rate_limited`, `approval_required`, `invalid_state`. |
| Paginacion | Cursor: `?limit=50&cursor=<opaco>` -> `{items:[], next_cursor}`. **[SPEC]** `GET` de listas sin paginar siguen devolviendo array simple si no se pasa `limit`/`cursor` (compatibilidad). |
| Filtros | `?status=a,b&agent_id=&customer_id=&since=&until=&q=`; orden `?sort=-created_at`. |
| Idempotencia | `Idempotency-Key` en POST que crean (requests, runs, messages). Reutilizar clave+mismo body => misma respuesta. |
| Concurrencia | `ETag`/`If-Match` en PATCH de agentes, workflows, reglas. |
| Trazabilidad | `X-Request-Id` entrante/saliente; todo evento/audit lo incluye. |
| Permisos | Cada endpoint declara el permiso requerido (06); sin permiso => 403 (o 404 si revelar existencia es sensible). |
| Versionado | `/api/v1`; cambios incompatibles => `/api/v2`. Campos nuevos son aditivos. |

## 2. Fase 1 - SPEC (sin cambios)

| Metodo y ruta | Notas |
|---|---|
| `GET /agents`, `GET /agents/{id}`, `GET /agents/{id}/detail` | **[SPEC]** |
| `GET /tasks?agent_id=&status=`, `GET /tasks/{id}` | **[SPEC]** |
| `POST /requests` `{text}` -> `202 {request_id}` | **[SPEC]**; extension opcional: `{text, customer_id?, budget_cap_usd?}` |
| `GET /requests`, `GET /requests/{id}` | **[SPEC]** |
| `GET/POST /conversations/{id}/messages`, `GET /conversations` | **[SPEC]** |
| `GET /approvals?status=`, `POST /approvals/{id}/decision` `{decision, note?}` | **[SPEC]** |
| `GET /reports`, `GET /reports/{id}`, `GET /activity?limit=`, `GET /metrics`, `GET /healthz`, `POST /demo/reset` | **[SPEC]** |
| `GET /ws` | **[SPEC]** + extensiones (03) |

Extension compatible de `GET /metrics`: ademas de `{tasks_active, tasks_done, approvals_pending, cost_usd, budget_usd, errors, requests_total}` puede incluir `cost_by_agent`, `budget_period`, `budget_used_pct`.

## 3. Fase 1+ - Extensiones operativas (bajo riesgo)

### Requests y tareas
| Ruta | Descripcion | Permiso |
|---|---|---|
| `POST /requests/{id}/cancel` | Cancela pendientes/en curso; request `failed` con `reason=cancelled` | `tasks:cancel` |
| `POST /requests/{id}/clarify` `{answers:[{question,answer}]}` | Responde `clarifying_questions` del plan | `requests:create` |
| `GET /requests/{id}/timeline` | Eventos causales ordenados (`events` por `correlation_id`) | `tasks:read` |
| `GET /requests/{id}/cost` | Desglose por tarea/agente/modelo | `reports:read` |
| `POST /tasks/{id}/retry` | Reintento manual de `failed`/`blocked` | `tasks:cancel` |
| `POST /tasks/{id}/cancel` | Cancela una tarea | `tasks:cancel` |
| `GET /tasks/{id}/interactions` | Trazas (`agent_interactions`), con cuerpos solo para `audit:read` | `audit:read` |

### Conversaciones
| Ruta | Descripcion |
|---|---|
| `POST /conversations` `{title, participants, customer_id?}` | Crear conversacion usuario-agente |
| `GET /conversations?agent_id=&customer_id=&kind=` | Filtros |
| `GET /conversations/{id}/messages?since=` | Incremental |

### Aprobaciones
| Ruta | Descripcion |
|---|---|
| `GET /approvals/{id}` | Detalle con `args` exactos, cadena de delegacion, riesgo |
| `POST /approvals/{id}/decision` | **[SPEC]**; respuesta incluye la `Approval` resuelta; `409 invalid_state` si ya resuelta o `args_hash` cambio. Con gobierno (sec. 14.6): `403` si el actor es un agente, no alcanza el rol requerido o es el solicitante; doble aprobacion = la primera responde `pending` con `decisions` |
| `GET /approvals?status=&risk=&agent_id=` | Filtros |

### Eventos y actividad
| Ruta | Descripcion |
|---|---|
| `GET /events?since_seq=&types=&limit=` | Replay sin WS (03) |
| `GET /events/stream` | SSE equivalente al WS (solo servidor->cliente) |
| `GET /activity?limit=&agent_id=&kind=` | **[SPEC]** + filtros |

### Salud y descubrimiento
`GET /healthz` **[SPEC]** devuelve `{status}`; extension `GET /readyz` (PG, Redis, runtime con `mode`, `engine`, `contract_versions`) y `GET /version`.

## 4. Fase 2 - Identidad, multitenant y gobierno

### Auth y organizacion
| Ruta | Descripcion | Permiso |
|---|---|---|
| `POST /auth/login`, `POST /auth/refresh`, `POST /auth/logout` | Si hay login propio (alternativa: OIDC externo) | publico |
| `GET /auth/oidc/{provider}/start`, `.../callback` | SSO | publico |
| `GET /me` | Usuario, org, roles, permisos efectivos | autenticado |
| `POST /ws/ticket` | Ticket de un solo uso para WS | autenticado |
| `GET /org`, `PATCH /org` | Nombre, ajustes, presupuesto, `max_delegation_depth`, `max_autonomy` | `budget:manage` / admin |
| `GET/POST/PATCH/DELETE /users`, `POST /users/{id}/invite` | Gestion de usuarios | `roles:manage` |
| `GET/POST/PATCH/DELETE /roles`, `PUT /users/{id}/roles` | RBAC | `roles:manage` |
| `GET/POST/PATCH/DELETE /departments` | Estructura | `agents:configure` |

### Agentes (configuracion)
| Ruta | Descripcion | Permiso |
|---|---|---|
| `POST /agents` | Crear agente (slug, persona, rol, departamento) | `agents:configure` |
| `PATCH /agents/{id}` | Persona, responsabilidades, `runtime_profile`, `active`, presupuesto mensual (If-Match) | `agents:configure` |
| `PUT /agents/{id}/autonomy` `{autonomy, rules?}` | Nivel y reglas; auditado | `agents:set_autonomy` |
| `POST /agents/{id}/autonomy/simulate` `{rules, window:"30d"}` | Evalua reglas contra tool_requests historicos | `agents:set_autonomy` |
| `GET/PUT /agents/{id}/tools` | Herramientas concedidas (`max_risk`, `constraints`, override) | `tools:grant` |
| `GET /agents/{id}/memory?scope=&customer_id=` | Memoria visible | `memory:read` |
| `GET /agents/{id}/metrics?period=` | Tareas, tiempo, costo, tasa de aprobacion | `tasks:read` |

### Herramientas
`GET /tools`, `GET /tools/{key}` (schema de args, riesgo), `PATCH /tools/{key}` (habilitar/deshabilitar, `requires_approval`), `GET /tool-calls?task_id=&agent_id=&status=`.

### Clientes y contactos
| Ruta | Descripcion |
|---|---|
| `GET /customers?q=&status=`, `POST /customers`, `GET/PATCH/DELETE /customers/{id}` | CRUD (DELETE = derecho al olvido, async + audit) |
| `GET /customers/{id}/timeline` | Requests, conversaciones, documentos, aprobaciones del cliente |
| `GET/POST /customers/{id}/contacts`, `PATCH/DELETE /contacts/{id}` | Contactos |
| `GET /customers/{id}/memory`, `POST /customers/{id}/memory`, `PATCH/DELETE /memories/{id}` | Memoria de cliente (editable por usuarios) |
| `POST /customers/merge` `{source_id, target_id}` | Operacion explicita y auditada |

### Memoria
`GET /memories?scope=&agent_id=&customer_id=&key=` (siempre exige el filtro de scope; sin el => 400), `POST /memories`, `PATCH /memories/{id}` (`pinned`, `value`), `DELETE /memories/{id}`, `GET /memories/{id}/history`.

### Documentos y reportes
`GET /documents?customer_id=&kind=`, `POST /documents` (multipart o URL firmada), `GET /documents/{id}`, `GET /documents/{id}/content` (redirect firmado), `DELETE /documents/{id}`; `GET /reports?request_id=`, `GET /reports/{id}/export?format=md|pdf|json`.

### Presupuesto y costos
| Ruta | Descripcion | Permiso |
|---|---|---|
| `GET /budget` | Estado: limite, usado, reservado, proyeccion | `reports:read` |
| `GET /costs?group_by=agent|model|day|request&from=&to=` | Series para el dashboard | `reports:read` |
| `PUT /budget` `{budget_usd, period, per_request_cap}` | Cambio de limites | `budget:manage` |
| `POST /budget/override` `{amount_usd, expires_at, reason}` | Excepcion puntual | `budget:manage` |

### Notificaciones
`GET /notifications?status=unread`, `POST /notifications/{id}/read`, `POST /notifications/read-all`, `GET/PUT /notification-preferences`.

### Auditoria
**Implementado en el backend (contrato real en la sec. 14):** `GET /audit` (filtros `from`, `to`, `actor`, `type`, `entity`, `request_id`; cursor), `GET /audit/export?format=jsonl|csv` (streaming sincrono, con la cadena de hash para SIEM) y `GET /audit/verify` (verificacion de la cadena, ancla externa opcional), todos con `audit:read`. Lo de abajo era el diseno inicial; difiere en: el export es sincrono (no 202), no esta firmado (la cadena de hash permite verificarlo; la firma queda pendiente) y no hay filtro `decision` (usa `type=policy.decision` y mira `details.effect`).

| Ruta | Descripcion | Permiso |
|---|---|---|
| `GET /audit?actor=&action=&entity_type=&entity_id=&from=&to=&decision=` | Busqueda paginada | `audit:read` |
| `GET /audit/verify?from=&to=` | Verifica cadena de hash | `audit:read` |
| `GET /audit/export?format=csv|json` | Export firmado (async -> 202 + URL) | `audit:read` |

## 5. Fase 2 - Workflows

| Ruta | Descripcion | Permiso |
|---|---|---|
| `GET /workflows`, `GET /workflows/{key}?version=` | Definiciones | `workflows:manage` |
| `POST /workflows` `{key, definition}` | Crea version `draft` (valida DSL, devuelve errores por paso) | `workflows:manage` |
| `POST /workflows/{key}/versions/{v}/publish` | Pasa a `active` | `workflows:manage` |
| `POST /workflows/{key}/dry-run` `{inputs}` | Camino simulado sin efectos | `workflows:manage` |
| `POST /workflows/{key}/runs` `{inputs}` | Inicia run manual (202 `{run_id}`) | `workflow:operate` |
| `GET /workflow-runs?status=&workflow=&customer_id=` | Listado | `tasks:read` |
| `GET /workflow-runs/{id}` | Run + pasos + grafo con estado | `tasks:read` |
| `POST /workflow-runs/{id}/cancel` | Cancelar | `workflow:operate` |
| `POST /workflow-runs/{id}/steps/{key}/retry` | Reintentar paso | `workflow:operate` |
| `POST /workflow-runs/{id}/signals` `{signal, payload}` | Senal externa para pasos `wait` | `workflow:operate` |

## 6. Fase 3+ - Integraciones y herramientas reales

| Ruta | Descripcion |
|---|---|
| `GET /integrations` | Catalogo y estado por org (`gmail`, `slack`, `whatsapp`, `hubspot`, `calendar`, `drive`, `m365`, `erp`) |
| `POST /integrations/{provider}/connect` -> `{auth_url}`; `GET /integrations/{provider}/callback` | OAuth |
| `DELETE /integrations/{provider}` | Desconecta y revoca tokens |
| `GET /integrations/{provider}/status`, `POST /integrations/{provider}/test` | Salud |
| `POST /webhooks/{provider}` | Entrada de eventos (firma HMAC/anti-replay; publico pero verificado); persiste y emite `integration.inbound` |
| `GET/POST /secrets`, `DELETE /secrets/{name}` | Solo escritura (los valores nunca se devuelven) - permiso `integrations:manage` |
| `GET /inbox?channel=&customer_id=` | Mensajes entrantes (contenido marcado `external_untrusted`) |
| `POST /approvals/{id}/decision` con `{edit:{...}}` | Aprobar con edicion (genera nuevo `args_hash`, queda auditado) |

## 7. Contrato interno Go -> agent-runtime (extensiones aditivas)

Todo lo del SPEC se mantiene. Extensiones (campos opcionales, el runtime puede ignorarlas):

| Endpoint | Cambio |
|---|---|
| `POST /v1/run-task` | Request: `+contract_version`, `+limits:{max_tokens, max_cost_usd, timeout_s}`, `+untrusted:[{id,source,trust,text}]` (reemplaza progresivamente `external_content`), `+attempt`. Response: `+memory_writes:[{scope,key,value,kind,confidence}]`, `+delegations:[{to_agent_id,title,description}]`. |
| `POST /v1/plan` | Request: `+constraints:{max_tasks, allow_agents}`, `+customer_id`. Response: `+workflow_hint?:string` (clave de workflow sugerido). |
| `POST /v1/summarize` | Nuevo (resumen de conversacion/tarea para memoria) `{texts, max_tokens}` -> `{summary, usage}` |
| `GET /healthz` | `+engine`, `+contract_versions`, `+runtime_version` |
| Headers | `X-Request-Id`, `X-Runtime-Contract` |

Ninguna de estas lleva credenciales ni identidad completa del usuario: solo `agent` y `customer_id` opaco.

## 8. Ejemplos

**Crear request con cliente y tope**
```http
POST /api/v1/requests
Idempotency-Key: 6f1c...
{"text":"Prepara una propuesta de $50,000 para Acme","customer_id":"c0ffee00-...","budget_cap_usd":1.50}
-> 202 {"request_id":"..."}
```

**Decidir aprobacion con error de estado**
```http
POST /api/v1/approvals/9a2.../decision
{"decision":"approve","note":"ok, revisado con legal"}
-> 409 {"type":"about:blank","title":"Invalid state","status":409,"code":"invalid_state","detail":"approval already resolved"}
```

**Actualizar autonomia**
```http
PUT /api/v1/agents/assistant/autonomy
If-Match: "v7"
{"autonomy":"rules","rules":[{"id":"r2","effect":"allow","tool":"email.send","when":"args.template in ['meeting_confirmation']","limits":{"per_day":30}}]}
-> 200 {"agent":{...},"effective_autonomy":"rules"}
```

## 9. Generacion y pruebas del contrato

- Fuente de verdad: `backend/api/openapi.yaml` (OpenAPI 3.1); tipos TS del frontend y validadores de handlers se generan desde ahi (`oapi-codegen`, `openapi-typescript`).
- Tests de contrato contra el spec en CI; `breaking-change` check con `oasdiff` en cada PR.
- Colecciones de ejemplos como pruebas e2e (`httpyac`/Playwright API).


## 11. Plantillas de workflow, onboarding, tono regional y programaciones (mejoras #4, #9, #10, #14)

Extension **aditiva y retrocompatible** de `/api/v1` (migracion `230_org_config.sql`, down en `backend/migrations/down/230_org_config_down.sql`). Ningun endpoint ni campo del SPEC cambia. Si una organizacion no configura nada, el backend **no envia** `locale` ni `tone` al runtime y todo se comporta como antes. Las rutas solo existen si el servidor monta `OrgConfig` (siempre en `cmd/server`).

### 11.1 Datos, no codigo
Todo el catalogo vive en JSON embebido (`backend/internal/catalog/data/{workflows,packs,tax}/*.json`), versionado por `schema` (`aiw.workflow_template/1`, `aiw.onboarding_pack/1`, `aiw.tax_template/1`). Cada texto visible es una **clave i18n** resuelta contra el catalogo `i18n.{es,en}` de la propia plantilla (`es` por defecto). Al cargar, el catalogo se valida (agentes inexistentes, dependencias desconocidas, ciclos, profundidad > 5, claves sin traducir en es/en, placeholders sin parametro, campos desconocidos); un archivo invalido rompe el arranque y los tests. Anadir una plantilla = anadir un archivo.

- **Plantillas de workflow** (`new_client`, `collections`, `proposal`, `month_close`, `daily_briefing`): `params[]` (`{{param}}` por sustitucion literal, sin evaluacion; valores saneados: una linea, <= 200 caracteres, sin `{{ }}`), `steps[]` con `agent_role` (rol = id del agente), `depends_on` y trabajo en paralelo implicito: pasos sin dependencia entre si corren en paralelo (p. ej. en `month_close`, `balance_sheet` e `income_statement`). `stage` es el nivel de camino mas largo (los pasos con el mismo `stage` corren en paralelo).
- **Paquetes de onboarding** (`professional_services`, `agency`, `commerce`, `general`): autonomia por agente, memoria inicial por agente, reglas de aprobacion (`approval_amount_usd`, `always_approve`), plantillas recomendadas y hora sugerida del resumen. Hoy configuran los 7 agentes existentes; crear agentes nuevos por profesion depende del catalogo de profesiones (`professions-catalog.md`). Las reglas se guardan en `org_settings.rules`; **hoy solo la autonomia por agente se aplica de forma efectiva**, la aplicacion de `rules` en el motor de politicas queda pendiente.
- **Plantillas fiscales** (`mx_invoice_example`, `es_invoice_example`): solo estructura (campos, lista de verificacion). Deben llevar `example: true` y un aviso que contenga "ejemplo, no asesoria fiscal" / "example, not tax advice" (validado al cargar). No calculan impuestos.

### 11.2 Endpoints
Todos requieren autenticacion cuando `AUTH_ENABLED=true`; `?locale=es|en` opcional en las lecturas (por defecto el idioma de la organizacion, luego `es`).

| Metodo y ruta | Permiso | Descripcion |
|---|---|---|
| `GET /workflow-templates` | `agents:read` | Galeria: `[{key, version, category, name_key, name, description_key, description, params:[{key,label_key,label,required,default}], steps:[{key,title_key,title,description_key,description,agent_id,depends_on,stage}], parallel_steps}]` |
| `GET /workflow-templates/{key}` | `agents:read` | Una plantilla; `404` si no existe |
| `POST /workflow-templates/{key}/instantiate` `{params:{...}, locale?}` | `requests:create` | `202 {request_id}`. Crea una solicitud normal **sin pasar por el planificador del runtime** (mismas dependencias, paralelismo, aprobaciones, informe y estimacion de costo). `400` por parametro obligatorio faltante o desconocido, `404` plantilla inexistente |
| `GET /onboarding/packs` | `agents:read` | Paquetes por tipo de negocio resueltos |
| `GET /onboarding` y `GET /org/settings` | `agents:read` | `{configured, locale, tone, business_type, pack_version, onboarding_completed, onboarding_completed_at, rules, agent_tones, recommended_templates, available_tones, available_locales}` |
| `POST /onboarding` `{pack_key, locale?, tone?, briefing?:{enabled,hour,minute,timezone,weekdays}, force?}` | `org:manage` | Aplica el paquete (autonomia, memoria, reglas, resumen diario). `409` si ya se completo salvo `force:true`; `404` paquete; `400` tono/idioma/hora/zona invalidos. Respuesta `{settings, agents_configured, memory_seeded, schedule}`. Auditado (`onboarding.completed`) |
| `PUT /org/settings` `{locale?, tone?}` | `org:manage` | Idioma (`es`\|`en`) y tono de la organizacion |
| `PUT /agents/{id}/tone` `{tone}` | `agents:write` | Tono por agente; `""` quita el override. Devuelve los ajustes |
| `GET/POST /schedules`, `PUT/DELETE /schedules/{id}` | lectura `agents:read`, escritura `org:manage` | Tareas programadas simples: `{id, template_key, params, hour, minute, timezone (IANA), weekdays[0=domingo..6], enabled, next_run_at, last_run_at}`. `POST {hour, minute, timezone, weekdays?, enabled?}` crea el resumen diario (`template_key` por defecto `daily_briefing`; solo plantillas sin parametros obligatorios). Maximo 20 por organizacion |
| `GET /tax-templates?country=MX` | `agents:read` | Estructuras fiscales de ejemplo (`example:true`, `disclaimer`) |

Errores: los mismos codigos HTTP del resto de la API (`400` invalido, `404`, `409`).

### 11.3 Tono regional y `locale` hacia el runtime
`tone` ∈ `neutral|mx|co|ar|cl|es`. Prioridad: tono del agente > tono de la organizacion > ninguno. Go agrega a `/v1/plan`, `/v1/run-task`, `/v1/consult` y `/v1/synthesize` dos campos **opcionales** (`omitempty`): `locale` (`es|en`) y `tone`. Solo se envian si la organizacion guardo ajustes y el tono no es `neutral`; un runtime antiguo los ignora (`extra="ignore"`) y un runtime nuevo sin `tone` usa `neutral`. En el runtime el tono solo agrega una instruccion fija de registro y vocabulario al idioma `es`; no cambia reglas, esquema, enums ni nombres de herramientas.

### 11.4 Programaciones (resumen diario)
Un planificador en proceso revisa cada 30 s las organizaciones conocidas (la org por defecto y las de `memberships`) y ejecuta las programaciones vencidas como una solicitud normal con la plantilla `daily_briefing` (agente `assistant`, mismo presupuesto, aprobaciones y auditoria; actor `scheduler`). Es seguro con varias instancias: la ocurrencia se reclama con un compare-and-set de `next_run_at`, de modo que corre como maximo una vez. Una ocurrencia vencida hace mas de 6 horas (servidor caido) se salta y se avanza a la siguiente (`schedule.skipped_stale`). Sin mecanismo de reintento: un fallo queda en auditoria (`schedule.failed`).

### 11.5 Datos y RLS
Tablas `org_settings` (PK `org_id`), `agent_settings` (PK `org_id, agent_id`) y `schedules` (PK `id`, indice por `next_run_at` para las activas), todas con `org_id`, `ENABLE`/`FORCE ROW LEVEL SECURITY` y la politica `tenant_isolation` de `202_rls_policies.sql`, y `GRANT` a `app_user`. Sin fila en `org_settings` = "nada configurado". `POST /demo/reset` no borra esta configuracion.


## 12. Control de costos: estimacion previa, topes duros y desglose (mejoras #1, #2, #3)

Todo es **aditivo y retrocompatible**: sin topes configurados y con el umbral por defecto, el flujo existente no cambia (ningun cliente ve estados ni eventos nuevos en el escenario de demo). Valores monetarios en USD (`number`).

### 12.1 Estimacion previa (#1)
Tras crear el plan y las tareas, el backend pide al runtime un **rango** (`min_usd`-`max_usd`, nunca una cifra unica) por tarea y para el informe final, y lo publica. Si el maximo estimado supera el umbral de confirmacion o el tope de la solicitud, la solicitud pasa a `awaiting_confirmation` y **no ejecuta nada** hasta que el usuario responda.

| Ruta | Descripcion | Permiso |
|---|---|---|
| `GET /requests/{id}/estimate` | `CostEstimate` (guardado o recalculado desde las tareas persistidas). `404` si aun no hay plan | `requests:read` |
| `POST /requests/{id}/confirm` `{decision:"proceed"|"cancel", budget_cap_usd?}` | Responde la confirmacion. `proceed` con `budget_cap_usd > 0` fija el tope duro de la solicitud. `cancel` bloquea las tareas y marca la solicitud `failed` con explicacion. `409 conflict` si la solicitud no espera confirmacion; `400` si `decision` o el tope son invalidos | `requests:create` |
| `POST /requests` | Extension opcional del body: `budget_cap_usd` (tope duro de esa solicitud; `0`/ausente = valor por defecto de `REQUEST_BUDGET_CAP_USD`) | `requests:create` |
| `GET /requests/{id}` | Extension: `+budget_cap_usd`, `+estimate?` | `requests:read` |

`CostEstimate`:
```json
{"request_id":"...","currency":"USD","mode":"simulation|live","model":"...","basis":"simulation|token_heuristic|fallback",
 "tasks":[{"task_id":"...","title":"...","agent_id":"sales","min_usd":0.012,"max_usd":0.041}],
 "synthesis":{"min_usd":0.01,"max_usd":0.03},"total":{"min_usd":0.1,"max_usd":0.31},
 "threshold_usd":1.0,"budget_cap_usd":0,"requires_confirmation":false,"confirm_reason":"threshold|cap"}
```
- `basis=simulation`: el costo real cae **siempre** dentro del rango (el motor simulado usa esos mismos rangos; hay test).
- `basis=token_heuristic` (live): entrada aproximada por longitud de texto y dependencias; salida acotada por `max_tokens`; el maximo asume un reintento por JSON invalido y hasta 3 consultas. **Es una estimacion, puede variar.**
- `basis=fallback`: el runtime no tiene `/v1/estimate` (o fallo): constantes gruesas del backend. Una estimacion `fallback` **nunca** exige confirmacion.
- Confirmacion: `confirm_reason=cap` si `total.max_usd` > tope de la solicitud; `threshold` si `total.max_usd` > `COST_CONFIRM_THRESHOLD_USD`. Sin respuesta en `APPROVAL_TIMEOUT` la solicitud falla de forma visible.

### 12.2 Topes duros con pausa explicada (#2)
Antes de **cada** llamada al runtime el backend **reserva** `estimacion maxima` contra los tres alcances (solicitud, agente, organizacion) y la **concilia** despues con el costo real: se registra el **mayor** entre `usage.cost_usd` del runtime y el recalculo del backend con su propia tabla de precios (`07-seguridad-costos.md` 5.2). La reserva cuenta las llamadas en vuelo, asi que tareas paralelas no pueden rebasar el tope.

| Alcance | Tope | Al llegar al tope |
|---|---|---|
| `request` | `budget_cap_usd` de la solicitud | Solicitud `paused` (**pausa, no falla**); `budget.exceeded` con `resumable:true` |
| `agent` | Tope mensual por agente (UTC, desde el dia 1) | El agente pasa a `blocked`; sus tareas esperan, los demas agentes siguen; `resumable:true` |
| `org` | `BUDGET_USD` | La tarea falla con mensaje y `budget.exceeded` con `resumable:false` (no se sube desde aqui) |

| Ruta | Descripcion | Permiso |
|---|---|---|
| `GET /budget` | `{org:{budget_usd,used_usd,reserved_usd}, defaults:{request_cap_usd,agent_cap_usd,confirm_threshold_usd,period_start}, agents:[{agent_id,cap_usd,spent_usd,reserved_usd,paused}], pauses:[{scope,scope_id,request_id,since,cap_usd,spent_usd,needed_usd,waiters}]}` | `metrics:read` |
| `PUT /requests/{id}/budget` `{budget_cap_usd}` | **Subir tope y continuar**: cambia el tope (`0` lo quita) y reanuda las llamadas pausadas que ahora caben. Auditado (`budget.cap_changed`). `400` valor invalido, `404` solicitud desconocida | `approvals:decide` |
| `PUT /agents/{id}/budget` `{monthly_budget_usd}` | Igual para el tope mensual de un agente (`0` quita el tope explicito; aplica `AGENT_BUDGET_USD` si existe) | `approvals:decide` |

Si el nuevo tope aun no alcanza para la siguiente llamada, la pausa continua y se publica otro `budget.exceeded` con los numeros actualizados. Una pausa que nadie resuelve en `BUDGET_PAUSE_TIMEOUT` falla la tarea con un evento `error` visible. **Nunca hay un fallo silencioso**: siempre hay `budget.exceeded`, entrada de actividad y mensaje del sistema al usuario.

### 12.3 Desglose de gasto (#3)
Cada llamada conciliada escribe una fila en el ledger (`usage_entries`: solicitud, tarea, agente cobrado, operacion, modelo, tokens, costo, herramientas pedidas).

| Ruta | Descripcion | Permiso |
|---|---|---|
| `GET /costs/breakdown?request_id=` | Desglose de toda la org (o de una solicitud) | `metrics:read` |
| `GET /requests/{id}/cost` | Igual, limitado a la solicitud | `requests:read` |

Respuesta: `{total_usd, calls, untracked_usd, llm_only_usd, by_agent:[{agent_id,cost_usd,calls,input_tokens,output_tokens}], by_task:[{task_id,request_id,title,agent_id,cost_usd,calls}], by_tool:[{tool,calls,cost_usd}], by_operation:[{kind:"run_task|consult|synthesize",...}], by_model:[...]}`. `by_tool` reparte el costo de una llamada en partes iguales entre las herramientas que pidio (las herramientas no tienen costo propio en esta fase); `llm_only_usd` es el costo de llamadas sin herramientas; `untracked_usd` es gasto previo al ledger sin desglose.

### 12.4 Eventos WebSocket nuevos (aditivos; el envelope no cambia)
| Tipo | Payload |
|---|---|
| `cost.estimated` | `{request_id, estimate: CostEstimate}` (siempre tras `plan.created`) |
| `request.status_changed` | `{request_id, status}`: solo al entrar o salir de `awaiting_confirmation` / `paused` |
| `budget.warning` | `{scope, scope_id, request_id, agent_id, spent_usd, cap_usd, pct}` al cruzar el 80 % (una vez por alcance y tope) |
| `budget.exceeded` | `{scope, scope_id, request_id, agent_id, task_id, cap_usd, spent_usd, reserved_usd, needed_usd, resumable, message}` |
| `budget.resumed` | `{scope, scope_id, request_id, agent_id}` cuando termina la pausa |

`Request.status` gana dos valores (aditivos): `awaiting_confirmation` y `paused`. Los clientes que no los conozcan deben tratarlos como "en curso".

### 12.5 Contrato Go -> agent-runtime
`POST /v1/estimate` `{request_text, tasks:[{id,key?,title,description,agent_id,depends_on}], locale?}` -> `{mode, model, basis, currency, rates:{input_per_m,output_per_m}, tasks:[{id,title,agent_id,min_usd,max_usd,input_tokens_min,input_tokens_max,output_tokens_min,output_tokens_max}], synthesis:{min_usd,max_usd}, total:{min_usd,max_usd}}`. Usa las mismas tarifas que el costo real (`MODEL_PRICES`, `PRICE_IN_PER_M`/`PRICE_OUT_PER_M`) y **no llama al LLM**. Es opcional para el backend (capacidad `Estimator`).

### 12.6 Configuracion (variables de entorno del backend)
| Variable | Default | Descripcion |
|---|---|---|
| `COST_CONFIRM_THRESHOLD_USD` | `1.0` | Pide confirmacion si el maximo estimado la supera (`0` desactiva) |
| `REQUEST_BUDGET_CAP_USD` | `0` | Tope duro por defecto de cada solicitud (`0` = sin tope) |
| `AGENT_BUDGET_USD` | `0` | Tope mensual por defecto de cada agente (`0` = sin tope) |
| `BUDGET_PAUSE_TIMEOUT` | `30m` | Cuanto espera una pausa por tope antes de fallar visiblemente |

## 13. Conexiones, bóveda y controles de emergencia (backend implementado)

Aditivo y retrocompatible: sin `Deps.Conns/Controls/Gateway` (o sin conexiones) la API se comporta exactamente igual que antes y las herramientas siguen simuladas. Diseño completo en `integrations-credentials.md` (sec. 18 = estado de implementación). **Ningún endpoint, evento WS, log ni prompt devuelve un secreto**: `secret` y `oauth_client_secret` son de solo escritura; solo se devuelve `credential:{kind, hint, version, expires_at, rotated_at, created_at}` (`hint` = últimos 4 caracteres solo para claves >= 20 caracteres, nunca para tokens OAuth).

### 13.1 Convenciones

- Errores de estas rutas: `{"error": "<texto>", "code": "<código estable>"}` (no `problem+json`: el resto de la API tampoco lo usa todavía). Códigos: `not_found` 404; `invalid` 400, o un código más específico (`read_write_must_be_separate`, `agents_cannot_approve`, `secret_detected`, `confirmation_required`, `reason_required`); `forbidden` 403; `conflict` 409; `second_approver_required` 409; `version_mismatch` 409; al aprobar un envío que ya no se puede ejecutar, 409 con el motivo (`approval_mismatch`, `kill_switch_active`, ...); `precondition_failed` 412 (`If-Match` en `PATCH /connections/{id}`); `kek_missing` 503 (no hay `CONNECTIONS_KEK`: solo `mode:"simulated"`); `oauth_not_configured` 503; `internal` 500.
- Con `AUTH_ENABLED=false` el actor implícito es `owner` (puede todo, incluida la liberación). Los permisos son los de `integrations-credentials.md` sec. 10 (`connections:*`, `controls:*`); `controls:release` es **solo owner**.
- `ETag` en `GET /connections/{id}`; `If-Match` opcional en `PATCH`.
- El callback OAuth es público y se verifica con `state` de un solo uso (TTL 10 min, PKCE S256); responde siempre `302` a la UI (`UI_BASE_URL/connections/{id}?connected=1` o `?error=<código>`; códigos: `consent_denied`, `scope_overreach`, `account_mismatch`, `no_refresh_token`, `invalid_state`...). Nunca reenvía texto del proveedor.

### 13.2 Catálogo y conexiones

| Ruta | Descripción | Permiso |
|---|---|---|
| `GET /connection-providers` | `[{id, label_key, phase, available, auth:["oauth2_byo_app" o "api_key"], capabilities:[{id, profile:"read" o "write", risk, side_effects, reversibility?, default, always_approval, hold_seconds_default?, label_key, tools[]}], resource_filters:[{key,type}], never_offered[], default_limits, live_available, split_read_write}]`. `live_available=false` si falta `CONNECTIONS_KEK`: la UI ofrece solo simulado | `connections:read` |
| `GET /connections?provider=&status=` | Lista (metadatos) | `connections:read` |
| `POST /connections` `{provider, kind:"oauth2" o "api_key", label, capabilities[], resource_scope?, mode?:"live" o "simulated", expires_at?, secret? (api_key, solo escritura), oauth_client_id?, oauth_client_secret? (trae tu app, solo escritura; ambos o ninguno)}` | `201` + conexión. Gmail: **lectura y escritura en conexiones distintas** (`mail.read` solo; `mail.draft` y `mail.send` juntas); mezclar da `400 read_write_must_be_separate`. `simulated` no guarda secretos y queda `active`. `live` sin KEK da `503 kek_missing` | `connections:manage` |
| `GET /connections/{id}` | Detalle + `credential` + `grants_count` | `connections:read` |
| `PATCH /connections/{id}` `{label?, limits?, resource_scope?, read_only?}` | `If-Match` opcional | `connections:manage` |
| `POST /connections/{id}/oauth/start` | `{auth_url, expires_at}` | `connections:manage` |
| `GET /connections/oauth/callback?code&state` | Público; `302` a la UI | verificado por `state` |
| `POST /connections/{id}/scopes` `{add:[cap]}` | Consentimiento incremental (devuelve `auth_url` si es live). Añadir capacidades de escritura exige `connections:grant:write`; una conexión de lectura no puede ganar escritura | `connections:manage` |
| `PUT /connections/{id}/credential` `{secret}` | Rota una API key (`?destroy_previous=true`); las conexiones OAuth se reautorizan | `connections:manage` |
| `POST /connections/{id}/test` | `{status, ok, code, latency_ms, account_label, granted_capabilities}` | `connections:manage` |
| `POST /connections/{id}/suspend`, `/resume`, `/revoke {confirm_name}` | Reanudar tras un kill switch exige `controls:release`. Revocar: suspende, cancela envíos en espera, revoca en el proveedor (si falla: `pending_provider_revocation:true` y se reintenta), destruye el secreto, revoca grants; es idempotente y el historial de uso se conserva | `connections:revoke` |
| `GET /connections/{id}/limits`, `PUT .../limits` | `{per_minute, per_hour, per_day, write_per_day, max_items_per_call, max_bytes_per_day, monthly_budget_usd, on_exceed:"deny"}` | read / manage |
| `GET /connections/{id}/usage?agent_id=&result=&from=&to=&cursor=&limit=` | `{items:[Usage], next_cursor}`; `Usage` = `{id, connection_id, grant_id, agent_id, task_id, approval_id, on_behalf_of, tool, action, capability, decision:"allowed" o "needs_approval" o "denied", deny_reason, status:"succeeded" o "failed" o "skipped" o "held" o "scheduled" o "sent", resource_ref, items_count, bytes_in, bytes_out, latency_ms, provider_status, error_code, cost_usd, tainted, created_at}`. **Solo metadatos, nunca contenido** | `connections:usage:read` |
| `GET /connections/{id}/usage/summary?period=day, week o month` | `{reads, writes, denied, needs_approval, cost_usd, period}` | `connections:usage:read` |

`Connection` = `{id, provider, kind, label, account_label, mode, status, status_reason, profile:"read" o "write", requested_capabilities, granted_capabilities, provider_scopes, resource_scope:{labels?, exclude_labels?, max_age_days?}, limits, read_only, last_tested_at, last_test_status, last_used_at, last_error_code, expires_at, created_by, created_at, updated_at, revoked_at, pending_provider_revocation, oauth_client_id?, credential, grants_count}`. Estados: `pending`, `active`, `needs_reauth`, `error`, `suspended`, `revoked`, `expired`. `status_reason`, `deny_reason` y `error_code` son **códigos estables** (`kill_switch`, `user_revoked`, `reauth_required`, `scope_not_granted`, `connection_unavailable`, `read_only_mode`, `kill_switch_active`, `agent_paused`, `tool_disabled`, `rate_limit`, `budget_exceeded`, `risk_exceeds_grant`, `resource_out_of_scope`, `secret_detected`, `approval_mismatch`, `controls_unavailable`, `provider_error`): la UI los traduce y nunca muestra texto del proveedor.

### 13.3 Grants (qué agente puede qué)

| Ruta | Descripción | Permiso |
|---|---|---|
| `GET /connections/{id}/grants`, `GET /agents/{id}/connections` | Grants vivos / vista inversa `[{connection, grant}]` | `connections:read` |
| `PUT /connections/{id}/grants/{agent_id}` `{capabilities[], resource_scope?, constraints?:{allowed_recipient_domains?}, max_risk?, autonomy_override?, limits?, redaction_profile?:"strict", "standard" o "none_admin_only", valid_until?, alias?, is_default?, confirm_name?}` | Conectar no es conceder (D1). Reglas: capacidades dentro de las concedidas por el proveedor; un grant solo estrecha el alcance y los límites de la conexión; perfil por defecto **`strict`**; `none_admin_only` solo owner; las capacidades `approve*`, `connections*`, `controls*` y `admin*` se rechazan (`400 agents_cannot_approve`). Con escritura: exige `connections:grant:write` y `confirm_name` igual al nombre de la conexión (`400 confirmation_required`); si la org tiene **más de un admin** (owner + admin) responde `202` y el grant queda `status:"pending_second_approval"` (no se puede usar) hasta que **otro** humano lo apruebe | `connections:grant` |
| `POST /connections/{id}/grants/{agent_id}/approve` o `POST /connections/{id}/grants/{agent_id}` `{approve:true}` | Segunda aprobación; `409 second_approver_required` si es el mismo humano; `409 conflict` si nada está pendiente | `connections:grant` y `connections:grant:write` |
| `DELETE /connections/{id}/grants/{agent_id}` | `204` | `connections:revoke` |

`Grant` = `{id, connection_id, agent_id, capabilities, resource_scope, constraints, max_risk, autonomy_override, limits, redaction_profile, is_default, alias, status:"active", "pending_second_approval", "suspended", "revoked" o "expired", valid_from, valid_until, granted_by, requested_by, approved_by, created_at, revoked_at}` (`requested_by` = quien lo pidió; `approved_by` = el segundo humano).

### 13.4 Controles (kill switch, solo lectura, pausa, por herramienta)

| Ruta | Descripción | Permiso |
|---|---|---|
| `GET /org/controls` | `{mode:"normal" o "read_only", kill_switch_level:"none", "freeze" o "lockdown", reason, set_by, set_at, settings:{plan_review:"always", "touches_writes" o "never"}, disabled_tools:[], agents:[AgentControl], admin_count, viewer:{id, role, can_release, can_pause}}`. El nivel incluye la variable `KILL_SWITCH` del entorno | `connections:read` |
| `PUT /org/controls` `{mode?, reason?, settings?:{plan_review}}` | Activar solo lectura = `controls:pause` (admin); **desactivarlo = owner**. `plan_review:"never"` = owner. Por defecto `touches_writes` (decisión 7) | `controls:pause` |
| `POST /org/controls/kill-switch` `{level:"freeze" o "lockdown", reason}` | Activa. `freeze`: nada corre, el Gateway deniega **todo** uso de conexiones (lecturas incluidas) y los envíos en espera **vuelven a la cola de aprobación**. `lockdown`: además suspende todas las conexiones | `controls:killswitch` (admin) |
| `POST /org/controls/release` `{reason (obligatorio), resume_connections:"all" o "none"}` | Levanta. **Solo owner**. Los envíos devueltos a la cola NO se ejecutan solos: hay que aprobarlos de nuevo | `controls:release` (owner) |
| `POST /org/controls/tools` `{tool, disabled, reason?}` o `PUT /org/controls/tools/{tool}` `{disabled, reason?}` | Kill switch **por herramienta** (`"email"` o `"email.send"`); deny `tool_disabled` también para las herramientas simuladas. Apagar = admin; **reactivar = owner** con motivo | `controls:killswitch` |
| `GET /agent-controls` | `{items:[{agent_id, control:"active" o "paused", drain?:"graceful" o "immediate", reason, paused_by, paused_at, read_only}]}` (solo agentes con estado no por defecto) | `connections:read` |
| `POST /agents/{id}/control` `{action:"pause", "resume", "read_only" o "read_write", drain?, reason?}` | `pause graceful`: no empiezan tareas nuevas y la llamada en curso termina; `immediate`: tampoco herramientas. Un agente pausado no avanza (la tarea queda visible con el agente en estado `blocked` y actividad "En pausa (agent_paused)") y reanuda sola. `read_write` (quitar solo lectura de un agente) = owner | `controls:pause` |
| `GET /spend-limits/status` | Solo lectura: `{items:[{id, scope_type, scope_id, period, limit_usd, used_usd, on_hit}]}` derivado de los topes de `GET /budget`. La edición sigue en `PUT /agents/{id}/budget` y `PUT /requests/{id}/budget` (sec. 12). Los topes unificados por conexión/herramienta/dinero movido (C4) no están implementados | `metrics:read` |

Efectos: `read_only` (org, agente, conexión o la solicitud marcada "sin acciones externas") deniega con `read_only_mode` toda acción con efectos (conexiones, y herramientas simuladas cuya acción sea de escritura o desconocida); las lecturas siguen. Los controles **fallan cerrado**: si el almacén de controles no responde, el Gateway deniega (`controls_unavailable`) y las tareas esperan.

### 13.5 Bandeja de salida (outbox), envío diferido y revisión de plan

El envío de correo **siempre** pide aprobación (`always_approval`, invariante: ni la autonomía `autonomous` lo evita) y, una vez aprobado, espera una **ventana de cancelación obligatoria de 60 s** (`EMAIL_HOLD_SECONDS` solo puede subirla, máximo 300). El mensaje sale únicamente cuando vence la ventana y todo se revalida (kill switch, conexión, grant, solo lectura, pausa).

| Ruta | Descripción | Permiso |
|---|---|---|
| `GET /outbox?status=` | `{items:[EmailDraft]}`. `EmailDraft` = `{id, approval_id, agent_id, connection_id, account_label, tool, action, to[], cc[], subject, body, status:"pending_approval", "held", "sent", "cancelled", "rejected" o "blocked", block_reason, origin_external, new_recipient, tainted, reversibility:"none", hold_seconds, hold_until, flags[], version, args_hash, created_at, updated_at}`. El **mismo `id`** vive todo el ciclo (pendiente, en espera, enviado). `body` solo mientras es pendiente o está en espera | `approvals:read` |
| `PATCH /outbox/{id}` `{to?, cc?, subject?, body?}` | Edita un pendiente: `version` y `args_hash` cambian (invalida cualquier aprobación del contenido anterior). `400 secret_detected` si el texto parece una credencial | `approvals:decide` |
| `POST /outbox/{id}/approve` `{version}` | `409 version_mismatch` si cambió. Programa el envío (`status:"held"`, `hold_until` = ahora + 60 s) y resuelve la aprobación que esperaba la tarea. Se ejecuta el contenido **editado** | `approvals:decide` |
| `POST /outbox/{id}/reject` | Rechaza (resuelve la aprobación) | `approvals:decide` |
| `POST /tool-calls/{id}/cancel-hold` | Cancela un envío en espera, con el `id` del item; devuelve el `EmailDraft` | `requests:create` |
| `GET /connection-holds?status=` | Holds crudos (sin contenido) | `connections:read` |

Una aprobación normal (`POST /approvals/{id}/decision`) de una acción de conexión lleva `context` (solo mientras el proceso que la creó siga vivo): `{account, recipients[], reversibility, tainted, external_origin, hold_seconds, flags[], args_hash, outbox_id}`, también en el evento `approval.requested` y en `GET /approvals` (campo opcional nuevo `Approval.context`). Si el item se editó, aprobar por la ruta normal da `approval_mismatch` (no se envía el contenido viejo).

**Revisión de plan (decisión 7)**: si el plan alcanza conexiones con escritura (o `plan_review:"always"`), la solicitud queda en `planning` y **ninguna tarea corre** hasta aprobar.

| Ruta | Descripción | Permiso |
|---|---|---|
| `GET /plan-reviews`, `GET /requests/{id}/plan` | `{id, request_id, request_text, status:"plan_ready", "approved" o "rejected", tasks:[{id,title,agent_id,depends_on}], reachable_connections:[{connection_id,label,agent_id,capabilities,writes}], approvals_expected:{min, reason_keys}, est_cost_usd:{p50,p90}, est_duration_s:{p50}, review_required, touches_writes, no_external_actions, removed_task_ids, note}`. `reachable_connections` es una **cota superior** (lo que podría tocar según los grants), determinista (no la inventa el LLM) | `requests:read` |
| `PATCH /requests/{id}/plan` `{remove_task_ids?, note?, no_external_actions?}` | Quita tareas (sus dependientes se omiten) o marca "sin acciones externas" (equivale a solo lectura para esa solicitud) | `requests:create` |
| `POST /requests/{id}/plan/approve`, `/reject` | Lanza / cancela. La revisión vive en memoria del proceso (si el backend reinicia, la solicitud en curso se pierde igual que sus aprobaciones) | `requests:create` |

### 13.6 Eventos WebSocket nuevos (aditivos; mismo envelope)

Ningún payload contiene secretos ni contenido de mensajes (salvo `tool_call` en `tool_call.hold_changed`, que es el `EmailDraft` que el aprobador ya ve).

| Evento | Payload |
|---|---|
| `connection.created` / `connection.updated` | `{connection:{id, provider, label, status, granted_capabilities}}` |
| `connection.status_changed` | `{connection_id, from?, to, reason}` |
| `connection.tested` | `{connection_id, status, latency_ms}` |
| `connection.rotated` | `{connection_id, version}` |
| `connection.revoked` | `{connection_id, pending_provider_revocation}` |
| `connection.grant_changed` | `{connection_id, agent_id, before[], after[], by, status?}` |
| `connection.usage` | `{connection_id, agent_id, tool, action, decision, status}` (las lecturas permitidas se coalescen a 250 ms) |
| `connection.rate_limited` | `{connection_id, agent_id, limit}` |
| `control.changed` | `{scope:"org", mode, kill_switch_level, reason, by, controls:{mode, kill_switch_level, reason, set_by, set_at, settings, disabled_tools}, tool?, disabled?, released_level?}` |
| `agent.control_changed` | `{agent_id, control, read_only?, by, reason?}` |
| `tool_call.hold_changed` | `{hold_id, status, reason?, hold_until?, tool_call: EmailDraft}` |
| `plan.review_requested`, `plan.approved`, `plan.rejected` | `{request_id, preflight}` |
| `security.alert` | `{kind:"prompt_injection_suspected" o "secret_detected", agent_id, tool, ...}` |

### 13.7 Contrato Go -> runtime (extensión aditiva)

`POST /v1/run-task` ya admitía `external_content[]`; ahora también acepta `untrusted:[{id, source:"tool:<name>", trust, text}]`, `tools_available:[{name, actions}]` y `tainted:bool` (todos opcionales). El runtime **no recibe** `connection_id`, cuentas, URLs de API ni tokens (hay un test de contrato). Todo `untrusted` y `external_content` va en bloques `DATOS NO CONFIABLES` con secretos redactados (segunda barrera) y la respuesta se escanea antes de salir. Las lecturas por conexión se ejecutan **entre turnos** (hasta 2 rondas): Go ejecuta la herramienta con la credencial y reenvía el resultado sanitizado y delimitado (`<untrusted_data id source from trust="external_untrusted">`) como `external_content`.

### 13.8 Variables de entorno

`CONNECTIONS_KEK`, `CONNECTIONS_KEK_PREVIOUS`, `GOOGLE_OAUTH_CLIENT_ID`, `GOOGLE_OAUTH_CLIENT_SECRET`, `OAUTH_REDIRECT_URL`, `UI_BASE_URL`, `EMAIL_HOLD_SECONDS`, `KILL_SWITCH`, `APP_ENV` (tabla en `backend/README.md`).

## 14. Auditoría encadenada, aprobaciones con gobierno y reglas de aprobación (backend implementado)

Aditivo y retrocompatible. Cierra los hallazgos "Políticas sin conectar" y "Auditoría incompleta" de `enterprise-and-ecosystem-roadmap.md` (jugada 1). Las decisiones de diseño y su razón están en `06-permisos-autonomia.md` sec. 8; el modelo de amenazas, en `07-seguridad-costos.md` sec. 6.

### 14.1 Convenciones

- Errores: `{"error": "<texto>"}` (igual que el resto de la API). `403` = autenticado pero sin permiso para **esa** decisión (rol insuficiente, aprobar la propia solicitud, agente); `409` = ya resuelta o ya aprobaste; `400` = filtro o regla inválidos.
- Sin `AUTH_ENABLED` el actor implícito es `user` con rol `owner` (demo). La API de auditoría solo **lee**: no existe endpoint que escriba, cambie o borre una entrada.
- Permisos: `audit:read` (admin, owner) para `/audit*`; `approvals:read` para leer reglas; `policy:manage` (**solo owner**) para cambiarlas. Ningún rol de agente tiene `approvals:decide`, `audit:read` ni `policy:manage`.

### 14.2 `GET /audit`

Filtros (todos opcionales, combinables): `from` (inclusive) y `to` (exclusivo) en RFC 3339 o `YYYY-MM-DD` (UTC); `actor` (exacto: id de usuario, id de agente o `system`); `type` (acción: exacta, o prefijo con `*`: `approval.*`, `policy.decision`; alias `action`); `entity` (`task`, `approval`, `request`, `connection`, `org`...); `request_id`. Paginación por cursor: `limit` (1-1000, def. 100), `cursor` (opaco), `order=asc|desc` (def. `desc` en la API; el export es siempre `asc`).

```json
{
  "items": [{
    "seq": 41, "id": "uuid", "ts": "2026-10-06T14:03:22.123456Z", "org_id": "uuid",
    "actor": "sales", "action": "policy.decision", "entity": "task", "entity_id": "uuid", "request_id": "uuid",
    "details": {"tool":"email","action":"send_proposal","effect":"require_approval","rule_id":"dual_approval.action:send_proposal",
                "required_approvals":2,"required_role":"admin","args_hash":"3f…","on_behalf_of":"user-id"},
    "prev_hash": "ab…", "hash": "cd…"
  }],
  "next_cursor": "…"
}
```

`next_cursor` solo aparece si hay más. `seq` es 0 en filas anteriores a la cadena (migración 250): se listan, pero no entran en la verificación.

### 14.3 `GET /audit/export?format=jsonl|csv` (+ los mismos filtros)

Descarga **síncrona en streaming** (no 202): `application/x-ndjson` (`.jsonl`, una entrada por línea, el objeto de 14.2) o `text/csv` (columnas `seq,id,ts,org_id,actor,action,entity,entity_id,request_id,details,prev_hash,hash`; `details` es JSON; las celdas que empiezan por `= + - @` llevan un `'` delante contra inyección de fórmulas). Orden: más antigua primero (el orden de la cadena). Es una **instantánea**: solo entradas hasta el `seq` vigente al empezar; la entrada `audit.exported` que registra la descarga no entra en ella. Tope de 500 000 entradas por descarga (si hay más, acota por fechas). Cada export se audita (formato y filtros, nunca el contenido). `Content-Disposition: attachment`, `Cache-Control: no-store`.

Un export **sin filtrar** se verifica sin base de datos: `server ctl audit-verify -file export.jsonl -strict`; uno filtrado se verifica sin `-strict` (hay huecos por construcción, pero cada hash y cada par adyacente se comprueban). Para un SIEM: ingerir JSONL tal cual; `hash`/`prev_hash` permiten al SIEM detectar huecos por su cuenta.

### 14.4 Cadena de hash y `GET /audit/verify`

Cada entrada de una organización incluye el hash de la anterior: `hash = sha256(JSON compacto {v:1, org_id, seq, prev_hash, ts, actor, action, entity, entity_id, request_id, details})` con los campos en ese orden, claves de mapas ordenadas, `ts` en UTC con microsegundos (`2006-01-02T15:04:05.000000Z`), `details` normalizado (viaje JSON de ida y vuelta) y `prev_hash=""` en `seq=1`. Los `ts` crecen estrictamente a lo largo de la cadena (orden por tiempo = orden por `seq`). Cada organización tiene su cadena.

`GET /audit/verify[?anchor_seq=N&anchor_hash=H]` recorre toda la cadena y responde **200** con el informe (200 aunque la cadena no verifique: la verificación se ejecutó; el que falla es el registro):

```json
{"ok": false, "checked": 3, "first_seq": 1, "last_seq": 3, "head_seq": 3, "head_hash": "…",
 "broken_at_seq": 4, "reason": "hash_mismatch", "detail": "entry content was modified", "anchor_checked": false}
```

`reason`: `hash_mismatch` (entrada modificada), `prev_hash_mismatch` (no enlaza con la anterior: p. ej. se rehízo el hash de una entrada), `gap` (se borró, insertó o reordenó), `head_mismatch` (la cola se truncó: la cadena termina antes de la cabeza registrada), `anchor_mismatch` (el hash difiere del ancla externa o el ancla queda más allá del final), `org_mismatch`. Cada verificación se audita (`audit.verified` / `audit.integrity_failed`).

**Límite honesto:** quien controle por completo la base puede reescribir **toda** la cadena de forma coherente (incluida la cabeza). Se detecta con un **ancla externa**: guardar fuera de la base (WORM/S3 Object Lock, ticket, SIEM) el último `seq`+`hash` de cada export o verificación, y pasarlo como `anchor_seq`/`anchor_hash` (o `-anchor-seq/-anchor-hash` en el comando). El job diario que publica el ancla **no está implementado** (ver 06 sec. 8.7).

Comando (sin API, útil si la UI no responde): `server ctl audit-verify [-org ID | -all] [-anchor-seq N -anchor-hash H]` (usa `DATABASE_URL`; sale con error si no verifica) y `server ctl audit-verify -file export.jsonl [-strict]` (sin base de datos).

### 14.5 Qué se registra (solo metadatos)

Toda entrada pasa por un **depurador central** (`audit.Scrub`, en `Recorder.Audit`): se sustituyen por `[redacted]` las claves de contenido (`args`, `body`, `text`, `subject`, `output`, `summary`, `description`, `details`, `context`...) y de credenciales (`*secret*`, `*password*`, `*api_key*`, `token`, `*_token`, `authorization`...); los secretos dentro de textos (JWT, claves de API, tokens de Google, `password=`...) se enmascaran con `sanitize.RedactSecrets`; los textos se truncan a 300 caracteres. Contadores como `input_tokens` se conservan. En su lugar se guardan `arg_keys` (qué campos se enviaron) y `args_hash` (huella de la acción, no reversible).

| `action` | Cuándo | Metadatos clave |
|---|---|---|
| `policy.decision` | cada tool request que no es una lectura | `tool`, `action`, `risk`, `effect` (`allow`/`require_approval`/`deny`), `rule_id`, `reason`, `source` (`baseline`/`engine`), `trace` (todas las reglas que se activaron), `required_role`, `required_approvals`, `autonomy`, `args_hash`, `amount`, `on_behalf_of` |
| `tool.executed` / `tool.denied` | ejecución o rechazo (`reason`: `policy:<regla>`, `read_only_mode`, `tool_disabled`...) | `tool`, `action`, `arg_keys`, `approval_id`, `approvers` |
| `approval.requested` | se pide una aprobación | (evento WS, sin contenido) |
| `approval.partial` | llega la primera de dos aprobaciones | `received`, `required`, `required_role`, `policy_rule` |
| `approval.approved` / `approval.rejected` | decisión final (también `rejected` por tiempo, actor `system`) | `approvers[]`, `required_approvals`, `required_role`, `requested_by`, `policy_rule`, `note` |
| `approval.decision_refused` | alguien intentó decidir y no podía | `reason`: `agents_cannot_decide`, `role_too_low`, `self_approval` |
| `policy.rules_updated` | cambio de reglas (owner) | umbral, `always_approve`, y solo **conteos** de lo demás |
| `connection.*`, `control.changed`, `agent.control_changed`, `tool_call.*` | uso de conexiones y controles (Tool Gateway, kill switch, solo lectura, pausa) | ver `integrations-credentials.md` sec. 9 |
| `audit.exported`, `audit.verified`, `audit.integrity_failed` | uso de la propia auditoría | formato, filtros, resultado |

Todas llevan `request_id` cuando la acción ocurre dentro de una solicitud (las de aprobaciones lo obtienen de la tarea). Las direcciones de destinatarios pueden aparecer como metadato en eventos de conexiones (identifican a quién se escribió, no qué se escribió).

### 14.6 Aprobaciones con gobierno

`Approval` (en `GET /approvals` y en el payload de `approval.requested|resolved|progress`) gana campos aditivos:

| Campo | Significado |
|---|---|
| `required_approvals` | `1`, o `2` (doble aprobación: dos humanos distintos) |
| `required_role` | rol mínimo de **cada** aprobador (`admin`/`owner`; `""` = cualquiera con `approvals:decide`) |
| `requested_by` | el humano en cuyo nombre actúan los agentes (`""` sin autenticación) |
| `no_self_approval` | `true` con doble aprobación o `forbid_self_approval` |
| `policy_rule` | regla que exigió la aprobación (`org.amount`, `dual_approval.action:make_payment`, `baseline.risk_high`...) |
| `decisions` | `[{by, role, note, ts}]` aprobaciones recibidas hasta ahora |

`POST /approvals/{id}/decision {decision: "approve"|"reject", note}` (permiso `approvals:decide`: admin, owner) ahora responde:

| Caso | Respuesta |
|---|---|
| El actor es un agente o `system` (nunca pueden decidir) | `403` |
| `approve` con rol por debajo de `required_role` | `403` |
| `approve` del solicitante (`requested_by`) con `no_self_approval` | `403` |
| `approve` de quien ya aprobó esta solicitud | `409` |
| Primera de dos aprobaciones | `200`, `status:"pending"`, `decisions` con 1; evento `approval.progress`; la tarea sigue esperando |
| Aprobación que completa el quórum | `200`, `status:"approved"` |
| `reject` de cualquier humano autorizado (incluso tras una aprobación) | `200`, `status:"rejected"`, **definitivo** |
| Ya resuelta | `409` |

Con `AUTH_ENABLED=false` hay un solo actor anónimo: una doble aprobación no se puede completar (falla cerrado: caduca como cualquier aprobación pendiente). Las aprobaciones de acciones de conexiones (Gmail) llevan los mismos campos. Evento WS nuevo: `approval.progress` `{approval}` (primera de dos aprobaciones).

### 14.7 Reglas de aprobación como datos: `GET|PUT /policy/rules`

`GET` (permiso `approvals:read`) devuelve `{approval_amount_usd, always_approve[], governance{…}, enforced, baseline[]}`. `PUT` (permiso `policy:manage`, owner) reemplaza solo lo que se envía (`approval_amount_usd`, `always_approve`, `governance`; `governance:{}` la borra), valida y audita (`policy.rules_updated`). Las reglas viven en `org_settings.rules` junto a las que siembra el paquete de onboarding (reaplicar un paquete no borra `governance`). `enforced=false` = la organización no tiene reglas: rige solo el `baseline` (ver 06 sec. 8.1).

```json
{
  "approval_amount_usd": 3000,
  "always_approve": ["send_proposal", "send_contract"],
  "governance": {
    "deny_actions": ["crm.delete_*"],
    "new_recipient_requires_approval": true,
    "known_domains": ["acme.com"], "known_contacts": ["socio@otra.com"],
    "approval_categories": ["legal", "financial"],
    "amount_tiers": [{"amount_above": 5000, "role": "admin"}, {"amount_above": 50000, "role": "owner", "action": "make_payment"}],
    "action_roles": [{"action": "send_contract", "role": "owner"}],
    "dual_approval": {"actions": ["make_payment"], "amount_above": 20000, "high_risk": true},
    "limits": [{"id": "mail.hourly", "tool": "email", "action": "send*", "window_seconds": 3600, "max_calls": 30, "on_exceed": "deny"},
               {"id": "pay.daily", "action": "make_payment", "window_seconds": 86400, "max_amount": 50000, "on_exceed": "require_approval"}],
    "forbid_self_approval": false
  }
}
```

Semántica (detalle y decisiones en 06 sec. 8): `deny_actions` nunca se ejecutan; un destinatario no conocido (ni dominio ni dirección listados; sin destinatario reconocible en una acción de salida) exige aprobación; un monto por encima de un `amount_tier` exige aprobación **de ese rol o superior**; `action_roles` solo sube el rol; `dual_approval` exige dos humanos distintos si coincide la acción, el monto o el riesgo `high` declarado por el runtime, **sea cual sea la autonomía del agente**; `limits` son ventanas deslizantes por agente (o por organización con `per_org`). `role` solo admite `admin` u `owner` (solo ellos pueden decidir).

### 14.8 Datos y RLS

Migración `250_audit_chain.sql` (+ `backend/migrations/down/250_audit_chain_down.sql`): `audit_logs` gana `seq`, `prev_hash`, `hash`, `request_id`; tabla `audit_chain_heads(org_id, seq, hash, last_ts)` (serializa los apéndices con `SELECT … FOR UPDATE` y permite detectar el truncado de la cola); `approvals` gana `required_approvals`, `required_role`, `requested_by`, `no_self_approval`, `policy_rule`, `decisions`. Ambas tablas de auditoría con RLS por organización. **Append-only por dos vías**: el rol de la aplicación no tiene `UPDATE/DELETE/TRUNCATE`, y triggers impiden `UPDATE/DELETE/TRUNCATE` de `audit_logs` incluso al dueño de la tabla (hay que desactivar el trigger, un acto de DDL visible); un trigger solo deja avanzar la cabeza de `audit_chain_heads` de uno en uno.

### 14.9 Variables de entorno

Ninguna nueva. `APPROVAL_ACTIONS` (def. `send_proposal,send_contract`) sigue siendo parte del suelo inalterable (06 sec. 8.1).

## 15. Chat conversacional (backend implementado)

Contrato completo y reglas en [chat-routing.md](chat-routing.md). Resumen:

### 15.1 `POST /messages`

`{"conversation": "office" | "agent:<id>", "text": "..."}` -> `202 {"message_id", "turn_id"}`. Permiso `conversations:write`. Guarda el mensaje, enruta (`POST /v1/route` del runtime, con respaldo a reglas locales) y responde por WebSocket (`chat.message`, `chat.typing`, `route.decided`). `smalltalk` y `question` no crean solicitud, plan ni tareas; `task` sigue el flujo existente (plan, aprobación humana). `400` texto vacío / conversación inválida, `404` agente desconocido.

### 15.2 `GET /conversations/{id}/messages`

Sin parámetros devuelve todo (como antes). `?limit=N` (máx. 200) devuelve los N más recientes; `?before=<message_id>` los N anteriores. Cuerpo = array; headers `X-Has-More` y `X-Next-Before`. Los ids `office` y `agent:<id>` son conversaciones por organización creadas al primer mensaje (lista vacía si aún no hay).

### 15.3 Cambios aditivos

`Message` gana `turn_id`, `reply_to`, `request_id`; `Task` gana `assigned_reason`; `POST /v1/plan` admite `reason` por tarea. Migración `260_chat.sql`.

### 15.4 Runtime

`POST /v1/route` y `POST /v1/chat-reply` (ver chat-routing.md sec. 5). Opcionales: sin ellos el backend usa reglas locales.

## 16. Proyectos (backend implementado)

Un proyecto es un workflow entero con trabajo en paralelo: objetivos -> flujos -> nodos. Lanzarlo envía sus hojas como **una solicitud** del orquestador (tareas reales con dependencias), por lo que el tope de presupuesto, las aprobaciones, el kill-switch / solo lectura / pausa de agente, la política y la auditoría son los de siempre. Diseño y decisiones: [workflow-visualization.md](workflow-visualization.md) (Apéndice A, "Estado del backend"). Código: `backend/internal/projects`, `backend/internal/api/projects.go`. Migración `270_projects.sql` (down: `migrations/down/270_projects_down.sql`). Los ids de proyecto son `prj-<12 hex>`; los de nodo `<proyecto>:<clave>` (la subtarea delegada, `<nodo>~s`).

### 16.1 Endpoints (`/api/v1`)

| Ruta | Permiso | Respuesta |
|---|---|---|
| `GET /projects` | `tasks:read` | array de `ProjectSummary` (más recientes primero) |
| `POST /projects/draft` `{goal, template_id?, params?, budget_usd?, locale?}` | `requests:create` | `201 {project_id}`. Plantilla: id/clave (`tpl-financial-close`, `tpl-branch-opening`, `tpl-generic`), `wf:<clave>` (flujo del catálogo, p. ej. `wf:month_close` con `params:{month}`) o una guardada. Sin `template_id`: "cierre financiero/contable" -> `financial_close`; si no, planificador del runtime con respaldo `generic` |
| `POST /projects/from-template` | `requests:create` | igual que `draft` (exige `template_id`) |
| `GET /projects/{id}` y `GET /projects/{id}/snapshot?depth=full` | `tasks:read` | `{project, objectives, nodes, approvals, budget, estimate, structure_version, max_parallel, planning:null}` |
| `PATCH /projects/{id}/plan` `{ops:[{op:"update", id, fields:{title?, agent_id?}}]}` | `requests:create` | `{structure_version, issues}`; solo borradores (`409` si no); agente inexistente `400` |
| `POST /projects/{id}/estimate` | `requests:create` | `{estimate:{basis:"priors", total{p50_usd,p90_usd,calls}, duration, by_objective, by_agent, warnings}}` |
| `POST /projects/{id}/validate` | `requests:create` | `{ok, issues:[{severity,code,node_id?}]}`; códigos `cycle`, `no_nodes`, `empty_title`, `no_agent`, `unknown_agent`, `dangling_dependency` |
| `POST /projects/{id}/launch` `{approved_budget_usd, acknowledge_underbudget?, max_parallel?}` | `requests:create` | `202 {ok, project}`. `409` si no es borrador o `underbudget` (presupuesto < P50 sin reconocimiento); `400 invalid_plan (...)` |
| `POST /projects/{id}/control` `{action: "pause"\|"resume"\|"cancel"}` y atajos `POST /projects/{id}/pause\|resume\|cancel` | `tasks:manage` | `{ok, status, control, project}`; `409` en transiciones no válidas |
| `PUT /projects/{id}/budget` `{budget_usd}` | `tasks:manage` | `{ok, project}`; en un proyecto lanzado cambia el tope de su solicitud y despierta lo pausado (debe ser > 0) |
| `GET /projects/{id}/health` | `tasks:read` | semáforo, progreso, ETA P50/P90, camino crítico, presupuesto y pronóstico, esperando humano, riesgos, por objetivo |
| `GET /projects/{id}/approvals` | `approvals:read` | aprobaciones del proyecto: compuertas, de herramientas de sus tareas y `extend_budget` |
| `POST /approvals/batch` `{filter:{project_id, action}, decision:"approve"\|"reject", expected_count, include_high?}` | `approvals:decide` | `{ok, count, decided}`. `409 count_mismatch` si hay otro número pendiente; `409 high_risk_needs_confirmation` si alguna es de riesgo alto sin `include_high`. Cada decisión pasa por `Approvals.Decide` (rol, "los agentes nunca aprueban", doble aprobación, auditoría); la primera que falle corta y lo ya decidido queda decidido (`decided`) |
| `GET /project-templates` | `tasks:read` | plantillas incorporadas + flujos del catálogo (`wf:*`) + las guardadas por la organización |
| `POST /projects/{id}/save-as-template` | `requests:create` | `201 Template` (guardada por organización, `builtin:false`) |

`GET /projects/{id}/workspace` (entregables como artefactos) está en la sec. 17. Aislamiento: un proyecto de otra organización responde `404` en todas las rutas.

### 16.2 Estados

`project.status`: `draft`, `running`, `paused`, `waiting_human` (esperando una aprobación o ampliación de presupuesto), `done`, `failed`, `cancelled`; `control`: `active|paused|cancelled`. Nodo: `draft`, `pending`, `ready`, `running`, `awaiting_approval`, `waiting` (temporizador), `paused` (proyecto o agente en pausa / kill-switch), `blocked`, `done`, `failed`, `cancelled`. Todo se deriva de las tareas del orquestador al leer.

### 16.3 Eventos WS (aditivos)

`project.created`, `project.status_changed`, `project.delta`: `payload = {project: ProjectSummary, nodes?: Node[] (solo los que cambiaron, con rev), approvals?, estimate?, structure_version?}`. Los publica el monitor del proyecto (cada 250 ms si algo cambió). Sin entrada de auditoría por delta; sí `project.created|launched|paused|resumed|cancelled|budget_changed|plan_edited|saved_as_template|approvals_batch|done|failed`.

### 16.4 Notas de contrato

- Las tareas de un proyecto aparecen también en `/tasks` y `/requests/{request_id}` (el `request_id` está en `project.request_id`). Su descripción termina con `[project-node:<id>]`.
- La estimación usa priors (`basis:"priors"`, modelo `deepseek-chat`), no el estimador del runtime; el gasto real puede diferir.
- `max_parallel` se guarda pero no se impone por proyecto (manda `MAX_PARALLEL`). Pausar no interrumpe llamadas en curso. Las consultas entre agentes de nodos con más de `MAX_DEPTH` niveles de dependencia se omiten (se amplía el límite de cadena solo para planes de proyecto).
- Un proyecto en curso cuando el servidor se reinicia se marca `failed` ("interrupted") al leerlo.

## 17. Artefactos y espacios de trabajo (backend implementado)

Artefactos tipados (`sheet`, `doc`, `table`, `board`, `chart`, `pdf`, `form`, `inbox`, `agenda`) con contenido canónico `aiw.<kind>/1`, versiones inmutables, adjuntos por agente y vínculos entre artefactos. Diseño: [agent-workspaces.md](agent-workspaces.md) (sec. 5, 7 y 11). Código: `backend/internal/artifacts`, `backend/internal/api/artifacts.go`. Migración `280_artifacts.sql` (down: `migrations/down/280_artifacts_down.sql`). Permisos nuevos (`auth/rbac.go`): `artifacts:read` (viewer+), `artifacts:create|write|comment|export` (member+), `artifacts:approve|delete` (admin+). Los agentes nunca reciben `approve`/`delete`.

### 17.1 Endpoints (`/api/v1`)

| Ruta | Permiso | Respuesta |
|---|---|---|
| `GET /artifact-kinds?agent_id=` | `artifacts:read` | `{items:[{kind, schema_version, suggested}]}` (los sugeridos para el rol primero, sin restringir) |
| `GET /artifact-templates?agent_id=&kind=` | `artifacts:read` | `{items:[{id, kind, title:{es,en}, suggested_for}]}` |
| `GET /artifacts?kind=&agent_id=&task_id=&status=&q=&project_id=` | `artifacts:read` | `{items: ArtifactMeta[]}` (sin contenido; archivados solo con `status=archived`) |
| `POST /artifacts` `{kind, title, content? \| template_id?, agent_id?, customer_id?, task_id?, locale?}` | `artifacts:create` | `201 Artifact` (meta + `content` + `version`) |
| `GET /artifacts/{id}?version=` | `artifacts:read` | `Artifact`; con `version` el contenido de esa versión |
| `PATCH /artifacts/{id}` `{title?, status?, project_id?, deliverable_id?}` | `artifacts:write`; `approved`/`sent` (o salir de ellos) exigen `artifacts:approve`; archivar un artefacto ajeno, `artifacts:delete` | `ArtifactMeta`; `403` si falta el permiso; `409` si está bloqueado (`approved`/`sent`) |
| `DELETE /artifacts/{id}` | `artifacts:write` (+ creador o `artifacts:delete`) | `204`; archiva (las versiones se conservan) |
| `POST /artifacts/{id}/versions` `{base_version, content, summary?}` | `artifacts:write` | `201 {version, merged, content?}`; `409 {code:"conflict", head_version, merged:false, conflicts:[{unit, mine, theirs, theirs_author}]}`; `400` contenido inválido o `base_version` fuera de rango; `409` si está bloqueado |
| `GET /artifacts/{id}/versions`, `GET /artifacts/{id}/versions/{n}` | `artifacts:read` | `{items: VersionInfo[]}` (más reciente primero) / `Artifact` de esa versión |
| `POST /artifacts/{id}/restore` `{version}` | `artifacts:write` | `ArtifactMeta` (crea una versión nueva `source:"restore"`) |
| `PUT /artifacts/{id}/attachments/{agent_id}` `{mode: "none"\|"read"\|"propose"\|"edit"}` | `artifacts:write` | `ArtifactMeta`; `approve` -> `400`; agente inexistente `404` |
| `GET /artifacts/{id}/links?direction=in\|out\|both` | `artifacts:read` | `{items:[{id, from, to, relation, anchor?, alias?, stale?}]}` |
| `POST /artifacts/{id}/links` `{to, relation, anchor?}` | `artifacts:write` | `201 Link`; duplicado `409`; a sí mismo `400`; destino inexistente `404` |
| `POST /artifacts/{id}/refresh-dependencies` | `artifacts:write` | `{refreshed, version, changed_units, unresolved}` |
| `POST /artifacts/{id}/ask` `{agent_id, text?, mode:"review_my_changes"\|"free"}` | `requests:create` | `202 {request_id}` (solicitud normal del orquestador; concede lectura al agente si no la tenía) |
| `GET /projects/{id}/workspace` | `artifacts:read` | `{project_id, name, status, deliverables:[{deliverable_id, title, agent_id, status, progress, build_state, artifacts}], links}` |

Cuerpo de hasta 6 MB al crear y al guardar versiones (el resto de rutas, `MAX_BODY_BYTES`). Aislamiento: un artefacto de otra organización responde `404` en todas las rutas; los vínculos a artefactos de otra organización no se crean.

### 17.2 Eventos WS (aditivos; `agent_id` en el envelope si lo originó un agente)

`artifact.created {artifact, open_in_workspace}`, `artifact.updated {artifact}`, `artifact.status_changed {artifact_id, from, to, by}`, `artifact.version_created {artifact_id, version, base_version, author, source, summary, changed_units}`, `artifact.progress_changed {artifact_id, project_id, deliverable_id, task_id, progress, build_state}`, `artifact.dependency_changed {artifact_id, source_id, source_version?, stale}`, `artifact.recalculated {artifact_id, version, source_id, changed_units}`, `artifact.agent_focus {artifact_id, agent_id, region, ttl_ms}` (efímero), `artifact.link_added {from, to, relation}` y `artifact.deleted {artifact_id}`. Auditoría (`entity:"artifact"`): `artifact.created|version_created|status_changed|attachment_changed|link_added|ask` con autor, versión y `content_hash`, nunca el contenido.

### 17.3 Notas de contrato

- Contenido rechazado (`400`): esquema distinto de `aiw.<kind>/1`, fórmulas con funciones fuera de la lista blanca o referencias externas, `AIW_REF` con alias no declarado, nodos/marcas de documento desconocidos, enlaces que no sean `http|https|mailto`, imágenes remotas, filas/tarjetas con ids repetidos, tamaños sobre los límites (doc 1 MB; resto 5 MB).
- Guardado concurrente: merge de 3 vías por unidad; la misma unidad en ambos lados es conflicto (`409`) y no se escribe nada. Un guardado idéntico al contenido actual no crea versión.
- Los agentes escriben a través de `artifacts.Service.ApplyAgent` (solo con modo `edit`); no hay endpoint HTTP que acepte un `agent_id` de un cliente.
- No implementado todavía: propuestas y comentarios, `diff`, `export`, blobs/`import`, `GET|PUT /workspaces/{desk}` y la tool `artifacts` del runtime.

## 18. Notificaciones push (Web Push)

Solo notifican: la notificación lleva **título, riesgo e id de la aprobación** (nunca detalles, argumentos ni contenido, cifrados además con RFC 8291) y decidir sigue exigiendo `POST /approvals/{id}/decision` autenticado con el permiso `approvals:decide`. Se activa con `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY` y `VAPID_SUBJECT` (`mailto:` o `https:`); sin ellas `GET /push/config` responde `enabled:false` y las otras rutas no existen. `PUSH_ALLOWED_HOSTS` (lista separada por comas) amplía los servicios push permitidos.

### 18.1 Endpoints (`/api/v1`)

| Ruta | Permiso | Respuesta |
|---|---|---|
| `GET /push/config` | `approvals:read` | `{enabled, public_key?}` (clave pública VAPID para `applicationServerKey`) |
| `POST /push/subscriptions` `{endpoint, keys:{p256dh, auth}}` | `approvals:read` | `201 {id, created_at}`; el `endpoint` no se devuelve. Endpoint no `https`, IP, puerto distinto de 443 o fuera de la lista de servicios push -> `400`. Repetir el mismo endpoint lo actualiza |
| `DELETE /push/subscriptions` `{endpoint}` | `approvals:read` | `204`; solo borra suscripciones propias (misma organización y usuario) |

Auditoría (`entity:"push_subscription"`): `push.subscribed` y `push.unsubscribed`, sin endpoint ni claves. Tabla `push_subscriptions` (migración 330) con RLS por organización; las suscripciones que el servicio push declara caducadas (`404/410`) se eliminan solas.
