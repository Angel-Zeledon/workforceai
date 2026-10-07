# Roadmap empresarial y de ecosistema (priorizado)

Fecha: 2026-10-06. Estado: propuesta de priorización, sin implementación. Convierte dos lluvias de ideas (Bloque A: empresas grandes; Bloque B: producto y ecosistema) en un plan accionable contrastado con el código real.

Convenciones: prosa en español; identificadores, endpoints y claves en inglés. Escalas: Impacto (I), Esfuerzo (E), Riesgo (R) en A/M/B; E orientativo en semanas-persona (1-2 devs backend + 1 frontend, igual que `09-roadmap-fases.md`). Todas las cifras de esfuerzo son juicio mío, no medición.

Marcas de confianza:
- **[verificado en código]**: leí el archivo citado.
- **[verificado en web]**: leído en la URL citada (sección 9).
- **[no verificado]**: conocimiento general o inferencia mía; no hay fuente ni prueba.

---

## 0. Resumen ejecutivo

1. **Lo construido es más fuerte de lo que dicen los docs en controles y credenciales, y más débil en lo que una empresa grande revisa primero.** Existen bóveda cifrada, grants por capacidad, kill-switch, solo lectura, pausa de agente, envío diferido de 60 s, revisión de plan, tope de gasto con pausa, RLS por organización y auditoría append-only por rol de BD. No existen SSO, exportación de auditoría, cadena de hash, observabilidad, API pública, ni estado duradero de ejecuciones.
2. **Hallazgo crítico: el motor de políticas fino ya está escrito pero no está conectado.** `backend/internal/policy` (montos, límites por ventana, destinatario nuevo, categorías) y `backend/internal/tools/executor.go` solo los importan sus propios tests. El orquestador decide aprobación con `needsApproval` (riesgo `high`, lista `ApprovalActions`, autonomía `suggest`/`approve_each`) en `application/orchestrator.go`. El "RBAC/ABAC fino" de la lluvia de ideas es en gran parte **cablear lo que ya existe**, no construirlo.
3. **Hallazgo crítico 2: nada que esté en ejecución sobrevive a un reinicio.** Las aprobaciones esperan en canales en memoria (`application/approvals.go`), las ejecuciones son goroutines (`orchestrator.go`, `run` en memoria) y `07-seguridad-costos.md` sec. 5.4 admite "un reinicio corta las ejecuciones". El criterio de salida de F3 ("sobrevive a reinicio") no se cumple. Para un piloto serio esto pesa más que SSO.
4. **Hallazgo crítico 3: el proveedor de LLM de producción es DeepSeek.** `docs/DEPLOY.md` exige `DEEPSEEK_API_KEY`; `docker-compose.prod.yml` abre egress solo para él. Una empresa grande preguntará por residencia y destino de los datos; esa respuesta hoy es incómoda. Hay motor Anthropic en `agent-runtime/app/crewai_engine.py`, pero un solo proveedor por proceso, sin respaldo ni enrutamiento por tarea.
5. **Tensión estratégica**: `competitive-analysis.md` sec. 6 recomienda "evitar enterprise al inicio" (PYME hispanohablante). Este roadmap añade empresa grande. Son compatibles solo si se hace en orden: primero el caso de uso donde somos 10 veces mejores (sección 8), luego un piloto acotado, no un producto enterprise completo.
6. **Mi postura sobre la hipótesis del dueño** (SSO, auditoría exportable, RBAC fino, autoalojado): de acuerdo en 3 de 4 con matices, y falta lo más importante (sección 5): durabilidad, un conector real verificado, proveedor de modelo aceptable y paquete de confianza (DPA, cuestionario, pentest).

---

## 1. Método y límites

- Leí `README.md`, `docs/SPEC.md`, `docs/competitive-analysis.md`, `docs/DEPLOY.md` y `docs/architecture/` (`07`, `09`, `integrations-credentials.md`, `professions-catalog.md`; `06`, `02`, `08`, `agent-workspaces.md` y `workflow-visualization.md` solo por encabezados y búsquedas).
- Verifiqué en `backend/` (rutas del router, RBAC, políticas, migraciones, orquestador, controles, vault), `agent-runtime/app/` (motores, precios, redacción) y `frontend/src/` (componentes, mock, preferencias). **No ejecuté ningún test ni levanté el stack**: afirmaciones de "funciona" se basan en leer código y en lo que declaran los docs.
- Los agentes de otros equipos editan el frontend (rediseño visual); lo que digo del frontend corresponde al estado de hoy y puede cambiar.
- Lo de empresas grandes es conocimiento general respaldado con búsquedas web (sección 9). Las estadísticas de blogs de proveedores (p. ej. "SOC 2 es obligatorio en el 90% de los tratos") son marketing y las trato como orientativas.
- **No verificado** en general: cualquier cifra de costo de auditorías/certificaciones, plazos de compras de clientes concretos, y el comportamiento real de Gmail/Google OAuth (los propios docs lo declaran sin probar, `integrations-credentials.md` sec. 18.5).

---

## 2. Estado real y contradicciones docs vs código

### 2.1 Lo que existe (verificado en código)

| Capacidad | Evidencia | Estado |
|---|---|---|
| Auth local (email/contraseña), JWT con refresh, límites de login/registro | `backend/internal/auth/service.go`, `handlers.go`, `ratelimit.go` | Existe. Sin OIDC/SAML/SCIM/MFA (búsqueda de `saml|oidc|scim|mfa|totp` sin resultados en `internal/auth`) |
| Roles `owner/admin/member/viewer` y permisos `recurso:acción` | `auth/rbac.go` | Existe, roles fijos, sin roles personalizados |
| Multi-tenant con RLS forzada y rol de BD sin bypass | `migrations/202_rls_policies.sql`, `203_app_role.sql`, `store_rls_test.go` | Existe |
| Aprobaciones humanas con riesgo low/medium/high y timeout | `application/approvals.go`, `orchestrator.go` `handleTools` | Existe, simple (ver 2.2) |
| Credenciales fuera del modelo | `internal/vault` (AES-256-GCM, DEK por org envuelta por KEK), runtime sin campos de secreto, `sanitize`, `agent-runtime/app/redact.py` | Existe; KEK solo en variable de entorno (`cmd/server/connections.go` avisa que producción debería usar KMS) |
| Grants por capacidad, maker-checker de grants, taint, envío diferido 60 s, outbox | `internal/connections`, `internal/gateway` | Existe, solo para Gmail (simulado y real sin probar con cuenta real) |
| Kill-switch (`freeze`/`lockdown`), solo lectura, pausa de agente, kill-switch por herramienta | `internal/controls`, `api/controls.go`, `241_controls.sql` | Existe; no cancela llamadas en vuelo al runtime (`integrations-credentials.md` 18.4) |
| Presupuesto: reserva/concilia, tope por solicitud/agente/org, pausa y reanudar, estimación previa | `application/budget.go`, `costcontrol.go`, `pricing.go`, `210_cost_ledger.sql`, `GET /requests/{id}/estimate` | Existe; el tope de org es una sola variable `BUDGET_USD`, sin periodos |
| Desglose de costos | `GET /costs/breakdown`, `GET /requests/{id}/cost`, `frontend/src/components/cost/*` | Existe |
| Auditoría | tabla `audit_logs` (`001_init.sql`), `REVOKE UPDATE, DELETE` al rol de app (`203_app_role.sql`) | Solo escritura append-only |
| Plantillas de workflow y paquetes por negocio, onboarding, tono regional, agenda (briefing diario) | `internal/catalog/data/{workflows,packs,tax}`, `application/orgconfig.go`, `orgservice.go`, `GET /onboarding*`, `/schedules` | Existe: 5 workflows, 4 packs (`agency`, `commerce`, `general`, `professional_services`), 2 plantillas fiscales marcadas `"example": true` (MX, ES) |
| Despliegue autoalojado de un nodo | `docker-compose.prod.yml`, `Caddyfile`, `docs/DEPLOY.md`, CI con imágenes a GHCR (`.github/workflows/ci.yml`) | Existe (Docker Compose, un host, backups con `pg_dump` manual) |
| i18n es/en en UI | `frontend/src/lib/i18n/{es,en}.json` | Existe |
| Oficina 3D con estados, sala de reuniones, 3 luces | `components/office/*`, `lib/preferences.ts` | Existe (ver 2.3) |

### 2.2 Contradicciones y huecos entre lo documentado y lo construido

| # | Documentado | Realidad en código | Impacto en este roadmap |
|---|---|---|---|
| C1 | `09` F2: "Auditoría: hash chain, `GET /audit`, export"; `07` sec. 6: cadena de hash y export firmado | No hay endpoint `/audit`; `PermAuditRead` solo se define en `rbac.go` y se prueba, ningún handler lo usa; sin `prev_hash` | "Auditoría exportable" (hipótesis del dueño) es trabajo nuevo real, no un ajuste |
| C2 | `06`/`09` F2: autonomía real, reglas, `args_hash`, maker-checker de aprobaciones | `internal/policy` y `internal/tools` no se importan fuera de sí mismos; orquestador usa `needsApproval`. `args_hash` y aprobación atada a argumentos existen solo en `gateway` (Gmail). Maker-checker solo en grants, no en aprobaciones de acciones | RBAC/ABAC por monto y por aprobador: cablear el motor existente (pocas semanas) |
| C3 | `09` F3: workflows que sobreviven al reinicio | Estado de ejecución en memoria; aprobaciones con canales en memoria; plan review, outbox pendiente y contexto de tarjeta en memoria (`18.4`) | Bloquea pilotos con tareas largas; prerrequisito de "workflows duraderos" |
| C4 | `09` F7: WS con `seq`/replay/outbox con `LISTEN/NOTIFY` (F2 en `03`) | `internal/events/hub.go` sin secuencia ni replay (búsqueda de `seq|replay` sin resultados) | Los "replays compartibles" necesitan registro de eventos con orden; `events` existe como tabla pero sin garantía de orden de cliente [parcialmente no verificado: no leí `store.go` para eventos] |
| C5 | `README.md`/`SPEC.md`: "Fase 1 sin auth, org fija demo" | Auth, RLS, controles y conexiones ya existen; README desactualizado | Higiene documental; confunde a quien evalúe el producto |
| C6 | `README.md`: modo live con `ANTHROPIC_API_KEY`; `DEPLOY.md`: requiere `DEEPSEEK_API_KEY` | Ambos soportados por el runtime; prod documentada solo con DeepSeek | Decisión abierta D3 (sección 8) |
| C7 | `workflow-visualization.md` y `agent-workspaces.md`: proyectos, espacios de trabajo, artefactos | Backend no tiene rutas `/projects` ni `/artifacts`; `frontend/src/lib/projects/api.ts` lo declara ("The backend does not implement them yet"); funcionan con `NEXT_PUBLIC_MOCK=true` | "Proyectos" y "espacios de trabajo" son hoy interfaz con mock, no capacidad de producto |
| C8 | `professions-catalog.md`: roles como datos en `internal/roles` | No existe `internal/roles`; los 7 agentes son seed fijo (`domain/seed.go`); lo más cercano son los packs de `catalog` | Marketplace de profesiones requiere el motor de plantillas de rol primero |
| C9 | `competitive-analysis.md` sec. 6: evitar enterprise al inicio | Este roadmap persigue empresas grandes | Decisión estratégica abierta D1 |
| C10 | `07` sec. 4: rate limits por API, org, WS (token bucket) | Solo hay límites en login/registro/refresh (`auth/ratelimit.go`); `api/*.go` sin limitador por org | Faltante para "límites por cliente" y para SLA |
| C11 | `07`: retención configurable (30 días para `agent_interactions`) | No hay tabla `agent_interactions` ni jobs de retención/borrado (migraciones listadas: sin ellas) | Retención y borrado son trabajo nuevo |
| C12 | `04`: aislamiento de memoria por `customer_id` | `customer_id` no aparece en `domain`/`application` | "Despacho con varios clientes sin mezclar datos" aún no existe; es el corazón del caso de uso recomendado (sección 8) |

### 2.3 Estado de las ideas del Bloque B ya parcialmente cubiertas

| Idea | Estado verificado |
|---|---|
| Sala de reuniones | Existe solo como mobiliario/decoración (`OfficeEnv.tsx` `MeetingRoom`); `MEETING_CENTER` solo se usa ahí y en `lib/meta.ts`, los personajes no caminan a ella [verificado en código por búsqueda] |
| Gestos | Hay poses por estado (`Character.tsx`: `seat:working/thinking/idle/waiting/reviewing/blocked/awaiting_approval`, `stand:talking/completed/error`); no hay gestos sociales ni de delegación |
| Luz según hora | Solo preferencia manual `lighting: ambient|day|night` (`OfficeSettings.tsx`); no usa la hora real (`getHours` no aparece en componentes) |
| Resumen de supervisor | `daily_briefing.json` + `schedules` con `weekdays` permiten un semanal reutilizando el mecanismo; no hay plantilla semanal |
| Deshacer/rebobinar | No implementado (`integrations-credentials.md` 18.4); solo envío diferido de 60 s y versiones de artefactos en mock |
| Reputación/puntaje | No existe (`score|reputation` sin resultados útiles) |
| Memoria que aprende | Memoria clave/valor por agente (`memories`, `orchestrator.go` ~línea 442 y 748); sin aprendizaje del estilo del usuario |
| Móvil, voz, PWA | No existen (sin manifest, service worker ni `SpeechRecognition`) |
| Clips/replays, referidos, marketplace, cosméticos, API pública, white-label | No existen |
| Facturación electrónica por país | Solo 2 estructuras "de ejemplo" con aviso (`catalog/data/tax/*.json`); sin integración con PAC/DIAN/etc. |

---

## 3. Bloque A: empresas grandes

Clave de columnas: **Estado** = Existe / Parcial / Falta / Contradice. **Fase** = fase de `09-roadmap-fases.md` (F2..F7) o fase nueva (sección 7: N1..N4). **H** = horizonte: a (30 días), b (3-6 meses), c (largo plazo).

### 3.1 Seguridad y cumplimiento

| Idea | Estado y evidencia | I | E | R | Dependencias | Fase | H |
|---|---|---|---|---|---|---|---|
| SSO OIDC/SAML | Falta. Solo email/contraseña (`auth/service.go`). `09` F2 menciona OIDC y F7 SAML, ninguno hecho. Camino: broker (WorkOS, Auth0 Organizations) en lugar de implementar SAML a mano. Ejemplo de precio de WorkOS: $125/conexión a nivel inicial por SSO y otro tanto por directorio [verificado en web, sec. 9] | A | 2-3 sem con broker; 6-8 sem a mano | M (SAML casero es fuente clásica de vulnerabilidades) | Modelo de membresía existente | F2 (completar) | a-b |
| SCIM | Falta. Sin tabla de identidades externas. Mismo broker lo cubre | M | 2 sem con broker | B | SSO | F7 → N1 | b |
| MFA | Falta (sin resultados). Con SSO la empresa lo aplica en su IdP; para cuentas locales es necesario | M | 1-2 sem | B | - | N1 | b |
| RBAC fino por rol personalizado | Parcial. 4 roles fijos y permisos por endpoint (`rbac.go`); `CanAssign` evita escalada | A | 3-4 sem | M | - | F2 | b |
| ABAC: quién aprueba qué y hasta qué monto | Parcial y desconectado (C2): `policy` soporta `AmountAbove/AtMost`, destinatario, categorías y límites por ventana (`policy/types.go`, `config.go` con tope de pagos diarios 50.000); orquestador no lo usa. Falta: aprobador por nivel de monto (`required_role` en `Approval`), maker-checker de aprobaciones | A | 3-5 sem (cablear + modelo de aprobador + UI) | M (cambia el camino central de aprobación; requiere tests de regresión) | C2; `approvals.go` | F2 (cierre) | a-b |
| Auditoría inmutable y exportable a SIEM | Parcial: append-only por permisos de BD, sin cadena de hash, sin `GET /audit`, sin export. Expectativa típica de compradores: exportación JSON/CSV por admin/API, integración Splunk/Datadog/Elastic, retención mínima 90 días [orientativo; fuentes de proveedores, sec. 9] | A | Endpoint + CSV/JSON: 1-2 sem. Hash chain + verificación diaria: 2 sem. Streaming a SIEM (webhook/syslog): 2-3 sem | B | RBAC `audit:read` ya definido | F2 (cierre) | a (endpoint/export), b (cadena, streaming) |
| BYOK / KMS | Parcial: `KeyWrapper` es interfaz con `EnvKeyWrapper`; falta implementación KMS (AWS/GCP/Azure) (`vault/keywrap.go`, `cmd/server/connections.go`). BYOK real (clave del cliente) = KEK por org; hoy KEK global | M (decide tratos en banca/salud, [orientativo]) | 3-4 sem para KMS; 4-6 más para KEK por org | M | Vault | N2 | b-c |
| Residencia de datos por región | Falta. Un solo despliegue; el LLM va a DeepSeek (C6). Solución realista: despliegue por región (instancia dedicada) antes que multirregión en una sola | A en LatAm/UE | Despliegue regional por Terraform/Compose: 3-4 sem; selector de proveedor por región: ver modelos | A | Proveedor de modelo por org | N2 | b-c |
| Retención y borrado configurable | Falta (C11). Hay `audit_logs`, `events`, `activity`, `messages` sin TTL | M | 3-4 sem (políticas por tabla, job, borrado por cliente con registro, crypto-shred ya existe para credenciales) | M (conflicto con auditoría inmutable: separar retención de auditoría de la de contenido) | Auditoría | N2 | b |
| SOC 2 / ISO 27001 / GDPR / leyes locales | Falta certificación. Para SOC 2: Tipo 1 en ~2-4 meses; Tipo 2 con observación de 3-12 meses, 6-15 meses totales [verificado en web, sec. 9]. ISO/IEC 42001 (gestión de IA): 6-12 meses típicos, menos de 500 organizaciones certificadas a mayo 2026 [verificado en web]. México: nueva LFPDPPP en vigor desde 2025-03-21, multas hasta 320.000 UMA, autoridad transferida a la Secretaría Anticorrupción y Buen Gobierno [verificado en web]. Otros países (Colombia Ley 1581, Brasil LGPD, GDPR): no verificado por mí en esta sesión | A para grandes | SOC 2 Tipo 1: 8-12 sem de trabajo del equipo + auditor [no verificado costo]; pentest independiente: previo | M (certificar un producto que cambia rápido) | Auditoría exportable, retención, RBAC, SSO | N2 | b-c |
| Redacción de datos sensibles antes del modelo | Parcial y buena base: `sanitize` (secretos, perfil `strict` que enmascara teléfonos, cuentas, IBAN, RFC) y `redact.py` como segunda barrera, solo sobre contenido que viene de conexiones | A | PII general (nombres, direcciones, IDs por país), reversible (tokenizar y restaurar): 3-5 sem | M (falsos negativos; no vender como garantía) | `sanitize` | N2 | b |

### 3.2 Despliegue y control

| Idea | Estado y evidencia | I | E | R | Dependencias | Fase | H |
|---|---|---|---|---|---|---|---|
| Autoalojado / VPC | Parcial: Compose de producción de un nodo, Caddy, imágenes GHCR, `DEPLOY.md`. Faltan: Helm/Kubernetes, backups automáticos con PITR (hoy `pg_dump` manual), guía air-gapped, proveedor de modelo no externo | A (el dueño lo pide) pero ver sección 5 | Helm + backups + guía: 4-6 sem | M (soporte de entornos ajenos) | Modelos locales | N3 | b |
| Modelos locales | Falta. `engine.py`/`crewai_engine.py` soportan solo `anthropic` y `deepseek` vía `LLM(model=...)` de CrewAI; CrewAI soporta otros proveedores por LiteLLM [no verificado en esta sesión], pero precios (`MODEL_PRICES`) y `pricing.go` no conocen modelos locales (costo = 0, el ledger lo trataría con la tarifa por defecto de Sonnet si el nombre no está en tabla) | A (autoalojado serio) | 2-3 sem para soporte OpenAI-compatible (vLLM/Ollama) + precios | M (calidad de planes/JSON con modelos pequeños) | - | N3 | b |
| Ambientes dev/test/prod con promoción | Falta (no hay concepto de ambiente por organización). Se logra con instancias separadas; promoción = exportar/importar plantillas versionadas | M | 4-6 sem (depende de versionado de agentes) | M | Versionado | N3 | c |
| Multiempresa con subunidades, presupuestos y datos aislados | Parcial: aislamiento por org (RLS) existe; no hay jerarquía (sin `parent_org`/departamento en migraciones; `team|department` sin resultados), y el presupuesto de org es una constante de entorno | A | Subunidades (jerarquía + presupuesto por nodo + herencia de permisos): 6-8 sem | A (toca RLS: la política es `org_id = app_current_org()`, jerarquía exige cuidado) | RBAC fino | N2 | b-c |
| Proveedor de modelo por tarea con respaldo | Falta: un motor y modelo por proceso del runtime (`MODEL` + `PROVIDER`), sin respaldo. El backend ya recalcula costo con su tabla (`pricing.go`) | A | 3-4 sem (enrutamiento por agente/tarea, circuito y respaldo automático; `07` sec. 5.3 ya describe circuit breaker) | M | Precios por modelo | N3 | b |
| Versionado de agentes y prompts con revisión, diff y reversión | Falta. Agente = fila con persona fija en seed; `C14` en `integrations-credentials.md` lo lista como P2. Frontend de workspaces tiene versiones de artefactos (mock) | A para gobierno | 4-5 sem (tablas de versión, diff, restauración, aprobación de cambios) | B-M | Roles como datos (C8) | N3 | b-c |

### 3.3 Confiabilidad y escala

| Idea | Estado y evidencia | I | E | R | Dependencias | Fase | H |
|---|---|---|---|---|---|---|---|
| Workflows duraderos que sobreviven reinicios | Falta (C3). Opciones: Temporal (reanuda desde el historial de eventos) o durabilidad sobre Postgres (checkpoints por paso, otro servidor recupera) [ambas verificado en web, sec. 9]. Recomiendo Postgres primero: ya es dependencia obligatoria, evita operar un clúster Temporal y encaja en autoalojado; reevaluar Temporal si crece la complejidad | A (el más subestimado) | Postgres: 5-7 sem (tabla de ejecuciones/pasos, reanudación al arrancar, aprobaciones como espera persistente, `ClaimSchedule` ya da el patrón de reclamo atómico en `orgconfig.go`). Temporal: 6-10 sem + operación | A (reescribe el núcleo del orquestador) | - | F3 (criterio incumplido) | a (diseño y aprobaciones persistentes), b (resto) |
| Colas con prioridades y límites por cliente | Falta: concurrencia fija (4 por org según `07`), sin prioridad; sin rate limit por org (C10) | M | 3-4 sem | B | Workflows duraderos | N1 | b |
| Observabilidad (trazas, costo por llamada, alertas) | Parcial: costo por llamada ya se guarda (`usage_entries`); logs JSON con `slog`; sin OpenTelemetry/Prometheus (sin resultados de `otel|prometheus`); `/metrics` es JSON de negocio, no métricas de operación | A | OTel trazas request→task→runtime→tool: 2-3 sem; métricas Prometheus + alertas: 2 sem | B | - | F7 (adelantar) | a-b |
| Pruebas automáticas con casos dorados y puerta de calidad | Parcial: tests unitarios Go/pytest, e2e Playwright (`e2e/tests`), corpus hostil en tests de gateway; sin casos dorados por agente ni evaluación de calidad con LLM; `ci.yml` no tiene puerta de calidad de agentes | A | Marco de evals + 10-20 casos por agente + puerta en CI: 3-4 sem (más costo de LLM por corrida, no verificado) | M | Modo live para evaluar | F7 (adelantar parcial) | a (arranque con simulación), b |
| SLA, alta disponibilidad, recuperación ante desastres | Falta: un nodo, Redis y Postgres únicos, backup manual (`DEPLOY.md`). Un SLA solo es creíble con HA o con restauración probada | M (grandes lo piden en contrato) | HA gestionada (Postgres/Redis administrados) + PITR + simulacro DR: 4-6 sem + costo de infraestructura | M | Autoalojado/Helm | F7 / N3 | b-c |

### 3.4 Integraciones

El patrón de conectores ya está probado con Gmail: manifiesto JSON de proveedor + grants + gateway (`connections/providers/*.json`, `gateway`). Solo hay dos manifiestos (`google_gmail`, `custom_api`); Calendar, Drive, GitHub, CRM, ERP, Slack **no** están (`integrations-credentials.md` 18.4).

| Idea | Estado | I | E | R | Fase | H |
|---|---|---|---|---|---|---|
| ERP/finanzas (SAP, Oracle, NetSuite, Dynamics) | Falta. `09` F6 lista QuickBooks/SAP/Odoo. Para grandes: SAP/Oracle/Dynamics son proyectos de integración por cliente (a menudo vía un middleware del cliente) [no verificado], no un conector genérico | A pero de alto costo | 6-10 sem por ERP, solo lectura primero | A | F6 | c (salvo uno de lectura en piloto) |
| Facturación electrónica por país | Falta (solo 2 ejemplos con aviso). México exige emitir CFDI 4.0 a través de un PAC autorizado por el SAT [verificado en web]; esto implica partner PAC, no construirlo. Riesgo alto: dinero y fiscal | A en LatAm | 6-8 sem por país con partner [no verificado] | A (legal/fiscal) | F6 → N4 | c |
| CRM/soporte (Salesforce, HubSpot, Zendesk, ServiceNow) | Falta | A | 3-5 sem por conector con el patrón existente | M | F6 | b (uno) |
| Colaboración (Slack, Teams, Jira, Confluence, SharePoint) | Falta; `09` F5/F6. Slack con aprobaciones firmadas es el de mayor valor porque cierra el problema de "el aprobador no está en la oficina" | A (Slack) | Slack 3-4 sem; Teams/Graph 4-6 | M | F5/F6 | b |
| Datos (Snowflake, BigQuery) solo lectura | Falta; `professions-catalog.md` lista SQL de solo lectura en C5 | M | 3-4 sem por motor con consulta parametrizada y límites | M (exfiltración por consultas amplias) | F6 | b-c |
| Puente MCP/Zapier/n8n | Falta. `competitive-analysis.md` sec. 2 recomienda MCP como cliente. La especificación de autorización de MCP se basa en OAuth 2.1 y la versión más reciente que pude verificar es 2025-06-18 [verificado en web; si hay una versión posterior: no verificado] | A (cola larga) | Cliente MCP detrás del gateway con grants: 4-6 sem | A (cada servidor MCP externo es superficie de inyección; todo resultado = contenido no confiable) | F6/F7 | b-c |
| API pública y webhooks | Falta (sin claves de API ni webhooks en `api/`, `auth/`). Webhooks salientes: 2 sem; API pública con claves por org, alcances y versionado: 4-5 sem | A (habilita integradores y SIEM) | M | F7 → N1 | a (webhooks de eventos de auditoría), b |

### 3.5 Gobierno de IA

| Idea | Estado y evidencia | I | E | R | Fase | H |
|---|---|---|---|---|---|---|
| Inventario de agentes (dueño, datos, herramientas) | Parcial: existen `agents`, grants por agente (`GET /agents/{id}/connections`), `agent-controls`; falta dueño, clasificación de datos y vista | M | 2-3 sem | B | N2 | a-b |
| Políticas centralizadas | Parcial: `org_controls`, reglas del pack (`catalog.PackRules.AlwaysApprove`), políticas `policy` sin cablear | A | Va con ABAC (3.1) | M | F2 | b |
| Doble aprobación en acciones de alto riesgo | Parcial: solo en grants de escritura (`connections/grants.go`, solo si hay más de un admin). `06` lo prevé para aprobaciones `high`; no implementado (C2) | A | 1-2 sem tras cablear políticas | B | F2 | a-b |
| Informes de uso y riesgo para cumplimiento | Parcial: datos existen (`connection_usage`, `usage_entries`, `audit_logs`); falta reporte | M | 2 sem tras exportación | B | N2 | b |
| Detección de fugas e inyección | Parcial fuerte: `sanitize` puntúa inyección y envuelve en `<untrusted_data>`, taint, allowlists; tests con runtime malicioso. Falta DLP en salidas hacia herramientas de escritura más allá de Gmail y corpus de 200+ casos que promete `07` | A | Corpus y DLP en gateway: 3 sem | M (nunca es garantía; el diseño ya lo dice, `07` sec. 2.3) | F4 (cierre) | a-b |
| Evaluación de calidad y sesgo | Falta | M | Con evals (3.3); sesgo requiere definir qué medir por profesión: 3+ sem | M | N2 | c |

### 3.6 Costos y administración

| Idea | Estado y evidencia | I | E | R | Fase | H |
|---|---|---|---|---|---|---|
| Presupuestos por departamento con alertas | Parcial: topes por org/solicitud/agente con pausa, avisos 80/100 % (`budget.go`); sin departamento ni periodos; `GET /spend-limits/status` solo lectura | A | 3-4 sem (tabla `spend_limits` de `integrations-credentials.md` 14.3, periodos, jerarquía) | M | F2 → N2 | b |
| Reparto de costos (chargeback) | Falta; `usage_entries` por agente y solicitud, no por centro de costo | M | 2 sem tras departamentos | B | N2 | c |
| Facturación por uso con exportación contable | Falta (sin código de facturación; `billing:manage` es solo un permiso). Exportación CSV del ledger: 1 semana | M | Export: 1 sem; facturación con pasarela: 4-6 sem | M | N4 | b (export), c |
| Panel de administración | Parcial: Centro de seguridad, Conexiones, Controles, Costos en frontend; falta administración de miembros/roles en UI y reportes (permisos `members:*` existen en backend) | A | 3-4 sem de frontend | B | F2 | b |

---

## 4. Bloque B: producto y ecosistema

Fases: F# del roadmap existente; **N#** nuevas (sección 7).

### 4.1 Mejorar lo existente

| Idea | Estado | I | E | R | Dependencias | Fase | H |
|---|---|---|---|---|---|---|---|
| Oficina más viva (gestos, reuniones con delegación, luz por hora real) | Parcial (2.3). Todo derivado de eventos reales (principio del SPEC); la reunión puede dispararse con `message.sent` de tipo `delegation`/`consult` ya emitido | M (retención y marketing) | 2-3 sem frontend. Luz por hora real: 2-3 días | B (el rediseño visual en curso puede chocar: coordinar) | Rediseño frontend | N1 | a (luz), b |
| Reputación y puntaje de calidad por empleado | Falta; solo hay métricas básicas por agente (`tasks_completed`, `avg_seconds`, `cost_usd`) | M | 2 sem con señales objetivas (aprobada sin editar, rechazada, fallida). Sin evals reales sería un número vacío | M (puntaje sin base = engaña) | Aprobaciones con edición registrada; evals | N1 | b |
| Reunión de equipo con debate previo a decidir | Parcial: existe `consult` agente→agente (límite 3 por tarea en estimación, no aplicado en backend según `07` 5.4); el debate multi-turno es nuevo y multiplica costo de LLM | M | 3-4 sem | A (costo y bucles; usar tope por reunión) | Presupuesto por solicitud (existe) | N1 | c |
| Memoria que aprende cómo trabaja el usuario | Parcial (memoria clave/valor). Aprendizaje de preferencias a partir de ediciones de aprobaciones: 3-5 sem | A (reduce fatiga de aprobar, C13) | 3-5 sem | M (memoria como vector de persistencia de inyección, `07` 2.2.8) | Registro de ediciones | F3 / N1 | b |
| Resumen semanal de supervisor | Parcial: `schedules` + plantilla semanal | M | 1 sem | B | Briefing existente | N1 | a |
| Deshacer/rebobinar acciones | Falta salvo envío diferido. `undo_spec` por herramienta (`integrations-credentials.md` 14.4). Honesto: la mayoría de efectos externos no son reversibles; vender "ventana de cancelación" y "compensar", no "rebobinar" | A (confianza) | 3-5 sem para borrador/evento/archivo | M | Conectores con escritura | F4/N1 | b |

### 4.2 Atraer gente

| Idea | Estado | I | E | R | Fase | H |
|---|---|---|---|---|---|---|
| Clips y replays compartibles | Falta; requiere log de eventos ordenado (C4) y render determinista de la escena. Clip de vídeo: captura en cliente; replay interactivo: visor de solo lectura con datos redactados | A (marketing; `competitive-analysis.md` backlog #20) | 3-4 sem | A (privacidad: un replay filtra datos de negocio; redactar por defecto) | N1 | b |
| "Contrata tu primer empleado en 2 minutos" sin tarjeta | Parcial: simulación sin clave, onboarding con packs y workflow templates existen. Falta registro sin fricción, empleado real con conector y presupuesto de prueba controlado | A | 3-4 sem | M (abuso del crédito gratis; hay topes y límite de registro 5/h/IP) | N1 | a-b |
| Plantillas por oficio con vocabulario del sector | Parcial: 4 packs con i18n y tono regional (6 tonos). Más oficios = contenido (`professions-catalog.md` oleadas A/B) tras motor de roles (C8) | A | Motor de roles 3-4 sem; 4 oficios más: 6-8 sem | M (calidad desigual de personas) | F3 / N1 | b |
| Referidos pagados en crédito de IA | Falta (sin facturación). El crédito es costo real; requiere topes por cuenta y antifraude | M | 3-4 sem tras facturación | M (abuso) | N4 | c |
| Comunidad hispana y casos en español | No es código. Ya existe es/en en UI y tono regional; requiere contenido y casos reales | A | Continuo | B | negocio | a-b |
| Pruebas de valor concretas ("tu primer cierre de mes en 10 minutos") | Parcial: existe `month_close.json` en simulación. El valor real exige datos reales (CSV/Sheets/ERP) y verificación del resultado | A | 2-3 sem sobre carga de archivos + plantilla | M (prometer tiempo que el modelo no cumple; medir antes) | N1 | a-b |

### 4.3 Ecosistema

| Idea | Estado | I | E | R | Fase | H |
|---|---|---|---|---|---|---|
| Marketplace de profesiones y workflows con comisión | Falta. Prerrequisitos: roles/workflows como datos versionados y firmados, lint "solo endurece" (`professions-catalog.md` 3.5), sandbox de ejecución, moderación, pagos | M ahora, A después | 10-14 sem | A (código/prompts de terceros con acceso a dinero) | F7 → N4 | c |
| Red de profesionales humanos freelance | Falta | M | Modelo operativo, no solo software | A (responsabilidad profesional, laboral, pagos) | N4 | c |
| API/SDK públicos | Falta | A (ver 3.4) | 4-5 sem API + 2-3 sem SDK | M | N1 / N4 | b-c |
| Oficinas que se comunican entre empresas | Falta; hereda la identidad, contratos de datos y consentimiento entre organizaciones | B ahora | 12+ sem | A | N4 | c (largo) |
| Tienda de cosméticos | Falta; ya hay personalización (`AgentCustomizer.tsx`) | B | 3-4 sem + pagos | B | N4 | c |

### 4.4 Formatos nuevos

| Idea | Estado | I | E | R | Fase | H |
|---|---|---|---|---|---|---|
| App móvil de aprobaciones y voz | Falta (sin manifest/service worker/voz). Mejor primero PWA con push y aprobar/rechazar, y aprobación desde Slack/WhatsApp (F5). Las aprobaciones ya tienen API (`POST /approvals/{id}/decision`, `GET /outbox`) | A (cuello de botella real: el humano aprueba lejos del escritorio) | PWA 3-4 sem; nativa 8-12 sem | M (seguridad del canal de aprobación: sesión corta, args mostrados completos, `args_hash`) | F5 / N1 | a-b |
| Recepcionista telefónica en español | Falta; backlog #19 de `competitive-analysis.md` ("validar demanda antes") | M | 6-10 sem + costo por minuto [no verificado] | A (llamadas con terceros, consentimiento de grabación por país) | N4 | c |
| VR/AR | Falta | B | - | A (distracción) | - | c, posponer |
| Oficina compartida entre varios humanos con roles | Parcial: RBAC multiusuario y WS por org existen; falta presencia, atribución y asignación de aprobaciones por persona en la UI | A (necesario para empresa) | 3-4 sem | B | N1 | b |
| Modo educación | Falta; `education` es rol puntuado alto en `professions-catalog.md` (4.45) pero es un rol, no un modo | B | 2-4 sem como pack | B | N1 | c |

### 4.5 Negocio

| Idea | Estado | I | E | R | Fase | H |
|---|---|---|---|---|---|---|
| Precio por oficina con presupuesto de IA incluido, paquetes por industria | Propuesto en `competitive-analysis.md` sec. 6; sin implementación de planes ni cobro; el margen está **no verificado** (falta medir costo real por solicitud en live) | A | Medición 1-2 sem; cobro 4-6 sem | M | N4 | a (medir), b |
| White-label para despachos y consultoras | Falta (sin marca por org). Con subunidades y RBAC fino es natural (despacho = organización con clientes) | A | 4-6 sem (marca, dominio, correos) | M | N2 | b-c |
| Plan autoalojado | Parcial (3.2) | A (si el piloto grande lo exige) | Ver 3.2 | M | N3 | b |
| Certificaciones como argumento de venta | Falta (3.1) | A (grandes) | Ver 3.1 | M | N2 | b-c |

---

## 5. Camino mínimo para pasar un piloto en una empresa grande

### 5.1 Qué piensa el dueño y qué pienso yo

Hipótesis: SSO, auditoría exportable, RBAC fino y autoalojado.

| Elemento | Acuerdo | Argumento |
|---|---|---|
| SSO | De acuerdo, vía broker | Es un requisito de entrada casi universal en cuestionarios de seguridad [orientativo, sec. 9]; con un broker son 2-3 semanas y desbloquea SCIM después. No lo construiría a mano |
| Auditoría exportable | De acuerdo, y es de lo más barato | Hoy la tabla existe pero no hay endpoint (C1). Exportar JSON/CSV por API en 1-2 semanas cubre el 80 % de la pregunta; cadena de hash y SIEM pueden venir después |
| RBAC fino | De acuerdo, pero lo fino es el aprobador por monto, no más roles | El motor `policy` ya cubre montos y límites; el trabajo es conectarlo y modelar quién aprueba qué (C2). Roles personalizados pueden esperar |
| Autoalojado | **En desacuerdo como requisito del primer piloto** | Un piloto puede correr en SaaS o en una instancia dedicada nuestra en la región del cliente (más rápido). Autoalojado en la infraestructura del cliente multiplica el soporte, exige HA/Helm/backups y modelo local, y todo eso hoy no está. Ofrecerlo solo si el cliente lo condiciona por escrito |

### 5.2 Lo que falta en la hipótesis (a mi juicio)

1. **Durabilidad** (3.3): un piloto con aprobaciones que se pierden al reiniciar el backend se ve como defecto grave, no como limitación.
2. **Un caso real extremo a extremo y verificado**: hoy Gmail está implementado pero **sin probar contra Google real** (`integrations-credentials.md` 18.5) y el resto de conectores no existe. Sin valor real, no hay piloto.
3. **Proveedor de modelo aceptable** (D3): sin Anthropic/Azure/regional documentado como opción de producción y sin salida a un modelo local, la primera pregunta de seguridad (a dónde van mis datos) se complica.
4. **Paquete de confianza**: DPA, política de retención, descripción de subprocesadores (incluido el proveedor de LLM), resultado de un pentest independiente (la bóveda y el gateway lo piden en `integrations-credentials.md` C5), cuestionario respondido. SOC 2 Tipo 1 en curso ayuda; Tipo 2 no llegará a tiempo para un piloto de 30 días.
5. **Rate limiting y observabilidad básicos** (C10): para operar un piloto y responder incidentes.

### 5.3 Camino mínimo propuesto (orden)

| Paso | Entregable | E | Cierra |
|---|---|---|---|
| 1 | Cablear `policy` al orquestador, `required_role`/monto en aprobaciones, doble aprobación `high` | 3-5 sem | C2, ABAC, gobierno |
| 2 | `GET /audit` con filtros + export JSON/CSV + permiso `audit:read` + webhook de eventos de auditoría | 2-3 sem | C1 parcial |
| 3 | Ejecución duradera (Postgres): aprobaciones y tareas reanudan tras reinicio | 5-7 sem | C3 |
| 4 | SSO (OIDC/SAML por broker) + MFA delegado; SCIM después | 2-3 sem | identidad |
| 5 | Un conector real verificado de ida y vuelta (Gmail con cuenta real + uno de colaboración, preferible Slack para aprobar) | 4-6 sem | valor |
| 6 | Selector de proveedor/modelo por organización (Anthropic y DeepSeek documentados; endpoint OpenAI-compatible para local) con respaldo | 3-4 sem | D3, residencia |
| 7 | OTel + alertas básicas + rate limit por org | 3-4 sem | operación |
| 8 | Paquete de confianza (DPA, subprocesadores, pentest, SOC 2 Tipo 1 iniciado) | paralelo | cuestionario |

Total serial aproximado: 4-6 meses con el equipo supuesto; los pasos 2, 4, 7 y 8 se paralelizan. Un piloto restringido (un departamento, sin escritura irreversible, SaaS dedicado) puede arrancar tras los pasos 1-4 y un conector (≈ 3-4 meses) [estimación mía, no verificada].

---

## 6. Horizontes

### (a) Próximos 30 días

Criterio: bajo costo, alto efecto en confianza, no requiere rediseños profundos y no choca con el rediseño visual en curso (backend, docs y datos primero).

1. Actualizar `README.md`/`SPEC.md` (C5) y corregir la contradicción de proveedor en docs (C6). 0,5 sem.
2. `GET /audit` + export JSON/CSV y permiso `audit:read` usado (1-2 sem). Webhook saliente de auditoría (opcional, 1 sem).
3. Diseño y spike de ejecución duradera sobre Postgres y de aprobaciones persistentes; decidir Postgres vs Temporal con un prototipo (2-3 sem).
4. Cablear `policy` al orquestador detrás de una bandera de configuración, con tests de regresión (2-3 sem).
5. Medir costo real por solicitud en modo live con 20-50 solicitudes (la base de precios y de cualquier presupuesto incluido; hoy no verificado).
6. Probar Gmail con una cuenta real (el propio doc lo exige antes de usarlo).
7. Resumen semanal de supervisor (1 sem) y luz por hora real (días): victorias visibles sin riesgo.
8. Iniciar trámite de SOC 2 (elegir plataforma/auditor) y encargar pentest [costos: no verificado].
9. Definir con el dueño las decisiones D1-D6 (sección 8).

### (b) 3-6 meses

Camino mínimo (5.3): ejecución duradera completa, SSO por broker, conector Slack/aprobación y un CRM, selector de proveedor y respaldo, OTel, rate limit por org, PWA de aprobaciones, plantillas por oficio (oleada A de `professions-catalog.md`), reputación basada en señales, panel de administración, presupuestos por periodo y departamento (primera versión), evals con casos dorados, retención y borrado, replay/clips, registro sin tarjeta con empleado real, SOC 2 Tipo 1.

### (c) Visión a largo plazo

Subunidades y white-label completos, BYOK por organización, residencia multirregión, versionado/ambientes con promoción, ERP y facturación electrónica por país con partners, marketplace con comisión, red de freelancers humanos, API/SDK maduros, oficinas entre empresas, recepcionista telefónica, cosméticos, SOC 2 Tipo 2 e ISO 27001/42001. VR/AR: posponer sin fecha.

---

## 7. Fases nuevas propuestas (aditivas a `09-roadmap-fases.md`)

El roadmap existente asigna casi todo lo empresarial a F7 (6 semanas para observabilidad, evals, escala, plataforma y gobierno), lo cual es irreal y llega tarde para un piloto. Propongo **adelantar** partes de F2/F7 y añadir cuatro fases:

| Fase | Nombre | Contenido | Depende de |
|---|---|---|---|
| **F2-cierre** | Gobierno base (completar F2) | Políticas cableadas, aprobador por monto, doble aprobación, `GET /audit` y export, panel de miembros/roles, rate limit por org | Existente |
| **F3-cierre** | Durabilidad | Ejecución y aprobaciones persistentes, reanudación al arrancar, colas con prioridad | F2-cierre |
| **N1 Piloto** | Camino mínimo y activación | SSO, observabilidad, conector Slack + CRM, selector de proveedor, PWA de aprobaciones, API pública mínima y webhooks, oficina compartida con roles, onboarding sin tarjeta, resumen semanal, clips | F2/F3-cierre, F4 |
| **N2 Cumplimiento y escala organizacional** | Subunidades, presupuestos por departamento, retención/borrado, redacción de PII general, BYOK/KMS, inventario y reportes de gobierno, SOC 2 Tipo 2, white-label | N1 |
| **N3 Despliegue empresarial** | Helm/Kubernetes, HA, PITR y simulacro DR, modelos locales, enrutamiento y respaldo de modelos, versionado de agentes/prompts, ambientes | N1 |
| **N4 Ecosistema y negocio** | Facturación y planes, referidos, marketplace, freelancers, SDK, facturación electrónica con partners, voz telefónica, cosméticos, entre oficinas | N2, evidencia del caso de uso (sección 8) |

F4-F6 (herramientas reales, canales, CRM/ERP) siguen como están; N1 consume las que den valor al piloto.

---

## 8. Riesgos y decisiones abiertas para el dueño del producto

### D1. ¿PYME hispanohablante o empresa grande primero?
`competitive-analysis.md` recomienda lo primero; este roadmap lo segundo. Hacer ambos a la vez dispersa a un equipo de 1-2 devs backend. Mi recomendación: un solo piloto grande como prueba de hipótesis (con el camino mínimo) sin cambiar el posicionamiento, y decidir tras sus resultados.

### D2. Responsabilidad legal cuando un agente se equivoca con dinero real
Está sin resolver y sin documento. Hoy: aprobación humana obligatoria en acciones con efecto externo, avisos (`professions-catalog.md` 2.3), techo de autonomía por riesgo y simulación como punto de partida. No hay términos de servicio, límite de responsabilidad ni seguro (no verificado que existan fuera del repo). Decidir antes de operar con dinero real: (a) quién firma lo aprobado (el humano que aprueba), (b) cláusula de limitación de responsabilidad y su tope, (c) si se ofrece reembolso por fallos atribuibles al sistema (`competitive-analysis.md` lo menciona como decisión de negocio), (d) si se contrata seguro de responsabilidad por errores y omisiones tecnológicos [no verificado costo ni cobertura para IA]. Requiere abogado, no ingeniería. Para facturación fiscal, el aviso "no es asesoría fiscal" de `catalog/data/tax/*.json` no basta como política.

### D3. Proveedor de modelo y destino de los datos
Producción documentada con DeepSeek (C6). Decidir qué proveedores se ofrecen por defecto, cuáles por región y si se ofrece modelo local. Esta decisión condiciona residencia, BYOK, DPA y la conversación con compras de cualquier empresa grande.

### D4. Que lo 3D no opaque la utilidad
`competitive-analysis.md` sec. 3 ya lo reconoce como hipótesis a validar. Propuesta de métricas antes de invertir más en la oficina: porcentaje de usuarios que usan 3D vs dashboard después de la semana 2, y tiempo hasta aprobar desde móvil/dashboard vs oficina. Regla mía: toda mejora de la oficina debe justificarse por una de tres razones: sirve para supervisar (estado, bloqueos, aprobaciones), sirve para adquirir (clips), o es barata. Gestos y reuniones decorativas van después de móvil y aprobaciones. Además, las empresas grandes evaluarán la accesibilidad y el rendimiento en equipos modestos; el modo dashboard debe ser de primera clase [no verificado: no medí rendimiento].

### D5. El UN caso de uso en el que somos 10 veces mejores
No hay evidencia en el repo que lo demuestre; **no verificado** y debe decidirlo el dueño con datos de clientes. Mi candidato por lo construido y por lo investigado:

**Cierre mensual / cobranza para despachos contables y consultoras hispanohablantes con varios clientes** (workflow `month_close.json` y `collections.json`, pack `professional_services`), porque combina: aislamiento por cliente (la ventaja única nº 4 de `competitive-analysis.md`, aún sin implementar: C12), español y formatos fiscales locales, dinero con aprobación (nuestra ventaja nº 2) y una métrica comprobable ("tu primer cierre de mes en 10 minutos"). Condiciones para llamarlo "10 veces mejor": medir tiempo y errores frente a la hoja de cálculo actual en 3-5 despachos reales; sin esa medición es una hipótesis, no un hecho. Hasta tener un caso probado, **no abrir marketplace, freelancers, entre-oficinas ni cosméticos**: el ecosistema multiplica lo que ya funciona, no lo crea.

### D6. Alcance de lo regulado
Salud (`clinic_admin`), nómina, pagos y facturación electrónica están marcados `red` o pospuestos en `professions-catalog.md`. Decidir países y asesoría legal local antes de activarlos (México tiene nueva LFPDPPP con multas significativas [verificado en web]; los demás países: no verificado).

### Riesgos técnicos adicionales

| Riesgo | Mitigación |
|---|---|
| Reescribir el orquestador para durabilidad rompe el contrato WS/REST | Mantener contrato; migrar por detrás de una interfaz y probar con los e2e existentes (`e2e/tests`) y `smoke.sh` |
| Cablear `policy` cambia qué se aprueba hoy | Bandera por organización, modo "sombra" que compare decisiones de ambos motores antes de conmutar |
| Certificar un producto que cambia semanalmente | Congelar controles críticos como código con tests (`vault`, `gateway`, `controls` ya los tienen) y automatizar evidencias |
| Costo de LLM de evals y de pilotos | Presupuesto por solicitud y por org (existe) y topes específicos de evals |
| Dependencia de CrewAI (motor reemplazable) y de proveedores | El contrato Go→runtime ya aísla; añadir proveedor no cambia contrato |
| Rediseño visual simultáneo | Priorizar backend y docs en los primeros 30 días (sección 6a) |

---

## 9. Fuentes web (verificadas hoy, 2026-10-06)

Búsquedas realizadas con resultados citados; no abrí cada página completa, usé los resúmenes de la búsqueda. Donde son blogs de proveedores, el dato es orientativo.

- EU AI Act, aplazamiento de obligaciones de alto riesgo (acuerdo provisional del Digital Omnibus: Anexo III hasta 2027-12-02, Anexo I hasta 2028-08-02; etiquetado de contenido generado 2026-12-02): https://datamatters.sidley.com/2026/06/22/eu-lawmakers-reach-provisional-agreement-to-delay-key-eu-ai-act-obligations/ y https://www.gibsondunn.com/eu-ai-act-omnibus-agreement. "Provisional" significa que debe confirmarse su adopción final: no verificado.
- ISO/IEC 42001 en compras (6-12 meses típicos; menos de 500 organizaciones a mayo 2026): https://blog.orolabs.ai/what-is-iso-42001-and-why-does-it-matter-for-procurement y https://siliconcanals.com/?p=65477
- SOC 2 Tipo 1 vs Tipo 2 (2-4 meses vs 6-15 meses; observación 3-12 meses): https://drata.com/learn/soc-2/type-1-vs-type-2 y https://trustnetinc.com/resources/how-long-does-a-soc-2-audit-take/
- OWASP Top 10 para aplicaciones LLM (prompt injection LLM01, excessive agency LLM06): https://opensourcesecurity.substack.com/p/a-deep-dive-into-the-owasp-top-10 y https://promptfoo.dev/docs/red-team/owasp-llm-top-10/ (la fuente primaria es OWASP; no la abrí).
- Precio de SSO y directorio (SCIM) con WorkOS por conexión y SIEM $125/mes: https://ssojet.com/blog/workos-pricing-at-scale-50-100-200-connections (blog de un competidor del proveedor; verificar en la página oficial antes de decidir).
- Requisitos habituales de cuestionarios de seguridad (SSO/SAML, MFA, logs exportables y retención, SIEM, BYOK, residencia): https://securityboulevard.com/2026/04/9-critical-security-questionnaire-items-that-stall-enterprise-saas-deals/ y https://ssojet.com/blog/enterprise-security-questionnaire-saas. Las cifras porcentuales ("70 %", "90 %") son de blogs comerciales: **no verificadas**.
- Ejecución duradera: Temporal (reanuda desde historial de eventos) y alternativa sobre Postgres: https://www.dbos.dev/blog/postgres-is-all-you-need-for-durable-execution y https://jacar.es/en/durable-agent-execution-with-temporal/ (DBOS vende la alternativa Postgres: sesgo posible).
- MCP, autorización basada en OAuth 2.1, versión 2025-06-18 como la última que encontré: https://modelcontextprotocol.io/specification/2025-06-18/changelog (puede existir una versión posterior: no verificado).
- México, CFDI 4.0 y PAC autorizado por el SAT: https://www.edifact.com.mx/masinfo/pac-cfdi (sitio de un PAC, interesado) y https://www.ey.com/content/dam/ey-unified-site/ey-com/es-mx/alliances/documents/ey-innovacion-documentos-electronicos-mexico.pdf
- México, nueva LFPDPPP (DOF 2025-03-20, vigente 2025-03-21): https://www.ey.com/es_mx/technical/tax/boletines-fiscales/nueva-ley-federal-proteccion-datos-personal-posesion-particulares y https://www.littler.com/es/news-analysis/asap/mexico-tiene-nueva-ley-en-materia-de-proteccion-de-datos-personales

**No verificado en absoluto** (sin búsqueda ni lectura): GDPR operativo, Colombia Ley 1581, Brasil LGPD, DIAN/otros esquemas de facturación, requisitos concretos de SAP/Oracle/NetSuite/Dynamics, costos de auditorías y pentest, costos por minuto de voz, comportamiento de CrewAI con proveedores adicionales y de Google OAuth (PKCE en cliente web), y cualquier plazo de ciclo de ventas de una empresa grande.

---

## 10. Las 5 jugadas recomendadas, en orden

1. **Cablear el motor de políticas y cerrar gobierno** (aprobador por monto, doble aprobación, `GET /audit` con export): máximo efecto por esfuerzo, cierra C1 y C2, y es la parte de la hipótesis del dueño que más pesa.
2. **Ejecución duradera** (aprobaciones y tareas reanudan tras reinicio): prerrequisito silencioso de cualquier piloto y criterio ya prometido en F3.
3. **Un caso real extremo a extremo y el proveedor de modelo**: verificar Gmail real, sumar Slack (aprobar donde está la gente) y habilitar Anthropic/regional y respaldo; sin esto no hay valor que auditar.
4. **SSO por broker + observabilidad + paquete de confianza** (DPA, pentest, SOC 2 Tipo 1 en curso): lo que abre la puerta de compras; autoalojado solo si el cliente lo exige por escrito.
5. **Probar el caso de uso 10x (cierre mensual multicliente para despachos) con 3-5 despachos reales** antes de abrir marketplace, clips y ecosistema; implementar `customer_id` para ello.
