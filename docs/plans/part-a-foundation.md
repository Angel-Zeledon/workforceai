# Plan Parte A: la base

Fecha: 2026-10-07. Fuente: `docs/prompts/next-features.md`. Prosa en español; identificadores, endpoints y claves en inglés.
Esfuerzo en días-persona, juicio propio (no medición). Cada afirmación sobre el estado actual cita código leído en esta sesión.

## Orden propuesto

1. **A8 CI** (hecho en parte, ver abajo): sin CI fiable no se puede verificar nada de lo demás.
2. **A1 Ejecución duradera**: prerrequisito de pilotos reales, de A6 (aprobar desde el móvil horas después) y de B1/B2.
3. **A2 Cuentas y login (frontend)**: el backend ya tiene casi todo; falta la UI y las invitaciones.
4. **A3 IA real** y **A4 Gmail real**: necesitan claves/cuentas del dueño para verificarse.
5. **A5 Profesiones como datos**, **A7 pendientes**, **A6 móvil y canales**.

---

## A8 CI — estado: arreglado `go test`, `-race` añadido; falta verificar en GitHub

- 2026-10-08: smoke del escenario de $50,000 y 7/7 e2e de Playwright en verde contra el stack completo (compose en puertos alternos), con A1 integrado.

- Causa reproducida en `golang:1.26` (CI usa `go-version-file`): `TestProjectsLifecycleOverHTTP` fallaba con "cancel of a finished project = 200".
  El snapshot deriva `done` (`projects/view.go` `deriveStatus`) pero el estado terminal se persiste en el siguiente tick del motor (`projects/engine.go` `publish`); en ese hueco `Control` veía `running` y aceptaba cancelar.
  Arreglo: `Control` consulta el estado derivado antes de `pause`/`cancel` (comportamiento real corregido, no sólo el test).
- Segundo test intermitente encontrado con `-race`: `TestApprovalApprovedContinuesAndProducesReport` leía el estado del agente antes de que se escribiera (la fila de aprobación se crea antes, `orchestrator.go` `handleTools`). Arreglo en el test: helper `awaitApproval`.
- Verificado: `-race -count=10` (2 CPU) y `-count=5` (1 CPU) en verde; `pytest` 526 ok; `tsc`, `next build` y `check:i18n` ok.
- **No verificado**: el run real en GitHub Actions (no hay `gh` en esta máquina). Publicación de imágenes a GHCR ya existe en `ci.yml` (job `publish`); despliegue en `deploy/vps`.
- Pendiente opcional: añadir `-race` al job de backend (≈ +1 min) para atrapar estos casos antes.

---

## A1 Ejecución duradera — implementado salvo el paso 6 (A1b en `feat/a1b-resume`)

- Verificado en stack real (Postgres + Redis + runtime): aprobación pendiente → `docker compose restart backend` → log "resumed in-progress requests count=1" → aprobar → solicitud `done` con informe.

### Hecho (2026-10-08)
- Pasos 1-5 del alcance: `RunStore` (memoria + Postgres, migración `290_durable_runs.sql` con down y RLS), checkpoint por tarea antes de esperar una aprobación de herramienta simulada, `Approvals.Attach/WaitUntil/Supersede` con plazo original, `Orchestrator.Recover` al arrancar (`cmd/server/main.go`), evento `request.resumed`, ejecución aprobada a lo sumo una vez. Decisiones tomadas: D-A1a reintento automático una vez; D-A1b plazo original. Detalle en `07-seguridad-costos.md` 5.4.
- Tests: reinicios simulados con el store en memoria (aprobar, rechazar, decidido durante la caída, plazo vencido, sin checkpoint, doble `Recover`, interrumpida antes de empezar) y Postgres real (ida y vuelta, RLS, reset).

### Hecho (A1b, 2026-10-08, rama `feat/a1b-resume`)
- Proyectos: se reanudan tras un reinicio (`RequestOwner.ResumeRequest` + `projects.Service.Recover`); las puertas pendientes siguen siendo la misma aprobación y ya no se marcan `interrupted` (salvo sin recuperación).
- Revisión del plan y confirmación de costo pendientes: siguen esperando tras el reinicio con el plazo original (D-A1b); la puerta se guarda en `request_runs.gate`.
- Aprobaciones del Tool Gateway (Gmail): se reanudan desde su punto exacto (misma aprobación, mismos argumentos, elemento de la bandeja restaurado con el mismo id), sin reemplazarlas.
- A lo sumo una vez: registro `approval_executions` (clave `(org_id, approval_id)` + `args_hash`), compartido por la tarea y la bandeja de salida; falla cerrado si no está disponible.
- El chat de origen recibe el eco del informe de una solicitud reanudada (`request_runs.chat`).
- Migración `350_durable_resume.sql` (up y down, RLS por organización).
- Corrección de paso: la bandeja de salida se liga a su aprobación antes de que la aprobación sea visible (aprobar desde la bandeja al instante podía dejar la tarea esperando; el test `TestApprovingFromTheOutboxContinuesTheWaitingTaskWithEditedContent` fallaba ~1 de cada 10 con `-race`).
- Tests: reinicios simulados en memoria (revisión de plan reanudada y con plazo vencido, confirmación de costo reanudada y cancelada, aprobación de Gmail reanudada, aprobada desde la bandeja restaurada, rechazada durante la caída, ejecutada antes del reinicio sin repetirse, registro de ejecuciones simuladas, eco en el chat, proyecto reanudado en su puerta y puerta rechazada tras el reinicio) y Postgres real (columnas nuevas, reclamos concurrentes, RLS, reset, down y up).

### Hecho: paso 6 (rama `feat/a1c-queue`)
- Cola por organización con prioridades (`application/orgqueue.go`): `MAX_PARALLEL_PER_ORG` (por defecto 8) llamadas al runtime en vuelo por organización; interactivo > proyecto > programado; eventos aditivos `request.queued` / `request.dequeued`, auditoría y una línea de actividad por solicitud encolada. Lo que ocupa turno es la llamada al runtime, no la espera humana: kill-switch, pausa, horario, topes y aprobaciones se comprueban antes, así que nada se los salta y una tarea en pausa no bloquea a la organización. Cambio en `orchestrator.go` acotado a `submit` (propaga la prioridad) y `call` (pide turno).
- Contadores de ventana persistidos (`internal/counters`, migración `360_window_counters.sql` con down y RLS): uso de los límites de ventana del motor de políticas (scope `policy.limits`) y ventanas de la detección de anomalías (scope `anomaly`). Postgres o memoria sin `DATABASE_URL`. Detalle en `07-seguridad-costos.md` 5.6.
- Tests: orden por prioridad, tope bajo estrés con cancelaciones, sin fuga de turnos, tope entre solicitudes con eventos visibles, tarea en pausa sin turno, límite de ventana y anomalías que sobreviven a un reinicio simulado, Postgres real (ida y vuelta, RLS, down/up).
- Limitaciones: semáforo por proceso (no global entre réplicas); prioridad estricta sin envejecimiento; el chat conversacional no pasa por la cola; instantáneas "último escritor gana" entre réplicas. No verificado: e2e con carga real en el stack completo.

### Pendiente
- Varias instancias recuperando a la vez (documentado en `07-seguridad-costos.md` 5.4): hoy una sola instancia debe recuperar; el registro de ejecuciones sí es compartido.
- Ediciones de la revisión del plan hechas antes del reinicio (se pierden: la revisión empieza limpia); esperas de nodos `wait` de proyecto (vuelven a empezar); planificación interrumpida a mitad (la solicitud falla).
- No verificado: e2e con `docker compose restart backend` a mitad del escenario de $50,000, ni la reanudación con una cuenta real de Gmail (sólo buzón simulado).

### Estado anterior (verificado antes de implementar)
- Cada solicitud corre en una goroutine con estado en memoria (`application/orchestrator.go` `submit`/`process`, struct `run`).
- Las aprobaciones se persisten (`001_init.sql`), pero quien espera es un canal en memoria (`application/approvals.go` `waiters`); tras reiniciar, `Decide` actualiza la fila y nadie continúa la tarea. El timeout es un `time.Timer` del proceso.
- Lo que la tarea iba a hacer tras la aprobación (lista de `ToolRequest` restantes, argumentos) sólo vive en la pila de `handleTools`.
- No hay barrido al arrancar (`cmd/server/main.go`); los proyectos se marcan `failed` de forma perezosa al leerlos (`projects/service.go` `get`).
- Reservas de presupuesto y pausas por tope en mapas del proceso (`application/budget.go`); gasto y topes sí persistidos (`210_cost_ledger.sql`).
- Sin cola ni prioridades; paralelismo sólo dentro de una solicitud (`Scheduler.MaxParallel`) (resuelto en el paso 6). Límites por ventana sólo en auth (`auth/ratelimit.go`). Idempotencia sólo en `POST /messages` y en memoria.
- Sobrevive hoy: envíos diferidos de Gmail (`connection_holds`) y agendas.

### Alcance
1. **Checkpoint de tarea** (migración `290_durable_execution.sql`, up/down, RLS por `org_id`):
   tabla `task_checkpoints(org_id, task_id PK, request_id, phase, output jsonb, pending_tools jsonb, next_tool int, approval_id, args_hash, updated_at)`.
   Se escribe antes de pedir la aprobación y se borra al terminar la tarea. `pending_tools` guarda los `ToolRequest` restantes **ya sanitizados** (nunca secretos: los argumentos ya pasan hoy por la auditoría sólo como `arg_keys`; aquí se guardan completos porque hacen falta para ejecutar, igual que `connection_holds`).
2. **Aprobación sin canal**: `Approvals.Decide` deja de depender del waiter. Si hay waiter (proceso vivo) se usa; si no, publica un "resume" para el orquestador. El timeout pasa a ser `expires_at` en la fila más un barrido periódico (como `gw.Run` cada 5 s).
3. **Recuperación al arrancar** (`Orchestrator.Recover`): para cada solicitud no terminal, reconstruir `run` desde BD (request, tasks, agentes, conversación, `requestedBy`, estilo) y reentrar al scheduler:
   tareas `done` → `rs.done`; `awaiting_approval` con checkpoint → esperar/reanudar desde `next_tool`; `running` sin checkpoint → reintentar la llamada al runtime (idempotente: el runtime no tiene efectos externos) ; `planning` → replanificar.
   Proyectos: reanudar el motor en vez de marcar `failed` en `get`.
4. **Idempotencia de efectos**: al reanudar una acción aprobada se verifica `args_hash` contra la aprobación (la aprobación se ata a los argumentos exactos) y se registra `tool.executed` con `approval_id` una sola vez (clave única por `approval_id`).
5. **Reservas de presupuesto**: al reanudar se rehace la reserva; las reservas huérfanas no existen en BD, así que no hay fugas (verificar en `budget.go`).
6. **Cola y límites por cliente** (segunda entrega dentro de A1): `MAX_PARALLEL_PER_ORG` y prioridad (`interactive` > `project` > `schedule`) con un semáforo por org; contadores de ventana en Redis/Postgres para que sobrevivan reinicios.

### Contratos
- Sin cambios de endpoints. Nuevo evento `request.resumed` (aditivo). `approval.resolved` igual.
- Cambio de comportamiento visible: tras reiniciar, lo que antes quedaba colgado continúa; los proyectos dejan de marcarse `interrupted`.

### Riesgos
- **Doble ejecución** de una acción aprobada si dos instancias recuperan a la vez → lock `task:<id>` existente (Redis `SET NX`) + clave única por `approval_id`.
- Reanudar con políticas cambiadas → ya existe `policy.Recheck` tras la espera; se aplica igual al reanudar.
- Modo memoria (sin `DATABASE_URL`): nada que recuperar; se documenta.

### Tests
- Unitarios con store en memoria simulando reinicio (nuevo `Orchestrator` sobre el mismo store): aprobación pendiente → reinicio → aprobar → la tarea termina y el informe se genera; rechazo tras reinicio → bloqueada; timeout tras reinicio.
- Doble recuperación concurrente → una sola ejecución.
- Postgres: test de la migración up/down y de RLS (patrón de `store_rls_test.go`).
- e2e: `docker compose restart backend` a mitad del escenario de $50,000 y aprobar después.

### Esfuerzo
8-12 días (1-6), +3-4 días cola y límites.

### Decisiones que necesito
- **D-A1a**: ¿Reintentar automáticamente las llamadas al runtime que estaban en curso al reiniciar (coste duplicado posible, acotado por el tope) o marcarlas `failed` y pedir reintento manual? Recomiendo reintento automático una vez.
- **D-A1b**: ¿Las aprobaciones mantienen su plazo original tras un reinicio (recomendado) o se reinicia el reloj?

---

## A2 Cuentas y login — hecho (integrado en `next-features`)

**Hecho:** invitaciones (migración 300, token hasheado, un solo uso, sin escalar rol, RLS), `GET /auth/config`, lista de organizaciones en `/auth/me` y `POST /auth/switch-org`; frontend `/login`, `/register`, `/invite`, sesión, miembros e invitaciones en el menú de administración, UI según permisos; con auth apagado la demo abierta sigue igual. e2e `auth.spec.ts` (sólo con `E2E_AUTH=1`) verificado contra stack con auth.
**Pendiente:** envío del enlace por correo (hoy se copia), nombre de la organización en la cabecera, SSO/SCIM, y la decisión del dueño sobre mover el refresh token a cookie `HttpOnly` (hoy en `localStorage`).
**No verificado:** cambio de organización en e2e (sólo test de servicio).

Plan original:

### Estado actual (verificado)
- `auth`: registro (crea usuario, org y owner), login, refresh, logout, `me`, miembros con roles `owner/admin/member/viewer` y ~40 permisos (`auth/service.go`, `handlers.go`, `rbac.go`). RLS por org (`202_rls_policies.sql`, `203_app_role.sql`).
- Apagado por defecto (`AUTH_ENABLED`, `config/config.go`). Sin auth todo usa `domain.DemoOrgID` y `can()` no restringe (`api/middleware.go`).
- **El frontend no tiene login ni envía tokens** (`frontend/src/app/` sólo `page.tsx`/`layout.tsx`).
- No hay invitaciones: `AddMember` sólo añade usuarios ya registrados.

### Alcance
1. Frontend: páginas `/login`, `/register`, selector de organización, cierre de sesión, cliente HTTP/WS con bearer + refresh, UI condicionada por permisos, gestión de miembros en el menú de administración. i18n es/en.
2. Invitaciones: tabla `invitations` (token hasheado, rol, expira, org), `POST /invitations`, `POST /invitations/accept`; envío del enlace por correo **no** en esta entrega (se copia el enlace) salvo que haya SMTP.
3. Demo abierta como modo aparte: `AUTH_ENABLED=false` sigue igual; con auth, `/demo` usa una org demo de sólo simulación.
4. SSO/SCIM: después, con broker externo (WorkOS u otro) — **decisión del dueño** (coste por conexión).

### Riesgos
Seguridad del token en el navegador (recomiendo access token en memoria + refresh en cookie `HttpOnly`, `SameSite=Lax`; hoy el refresh se devuelve en el cuerpo — confirmar en `handlers.go` antes de cambiar). Cambia contrato de `/auth/refresh` si se mueve a cookie → **requiere visto bueno**.

### Esfuerzo
6-8 días (UI + invitaciones), sin SSO.

---

## A3 IA real probada — hecho salvo la medición real (integrado en `next-features`)

**Hecho:** tabla de precios única (`model_prices.json`, idéntica en Go y Python por test) con los modelos Claude actuales; política de modelo por organización (`GET/PUT /settings/model-policy`, migración 310, tope del servidor `ALLOWED_PROVIDERS`, auditada; nunca amplía lo permitido); `RUNTIME_TOKEN` entre backend y runtime (opcional, aviso al arrancar si falta); `scripts/eval-live` con 5 casos dorados (exige `--live` y clave); modelo Anthropic por defecto `claude-sonnet-5-5`.
**Pendiente:** BYOK (decisión de seguridad), UI de administración de la política.
**No verificado:** `eval-live` contra un modelo real (no hay claves en este entorno).

Plan original:

### Estado actual (verificado)
- Runtime con proveedores deepseek/anthropic/custom y respaldo en orden (`agent-runtime/app/providers.py`, `crewai_engine.py`); orden por rol vía `MODEL_PROVIDER_ORDER_<ROLE>`.
- El runtime acepta `allowed_providers`/`preferred_providers` (`app/models.py`), **pero el backend nunca los envía**: no hay política por organización.
- Precios: sólo modelos DeepSeek en las tablas; cualquier otro (incluido Claude) cae al valor por defecto de Sonnet 3/15 por millón (`application/pricing.go`, `engine.py`).
- Las llamadas backend → runtime no llevan autenticación (`infrastructure/runtime/client.go`).

### Alcance
1. Política por org (`org_settings.model_policy`: proveedores permitidos, preferido, modelo por rol/tarea) enviada en cada llamada; el runtime ya la aplica.
2. Tabla de precios completa y compartida (generada de un único JSON para Go y Python) con los modelos actuales.
3. Token compartido backend → runtime (`RUNTIME_TOKEN`).
4. Medición: script `scripts/eval-live` que corre N casos dorados en live y registra calidad (rúbrica), latencia y coste real del proveedor vs estimado.
5. Clave del usuario por org (BYOK) guardada en la bóveda existente → **toca secretos, requiere visto bueno**.

### Esfuerzo
5-7 días sin BYOK; +3 con BYOK. **No verificable sin una clave real** (decisión del dueño: qué proveedores se permiten).

---

## A4 Conectores reales — hecho con dobles locales; sin probar con cuentas reales (integrado en `next-features`)

**Hecho:** Google Calendar (leer; crear evento con aprobación y retención de 60 s), Google Drive (sólo lectura y sólo en carpetas elegidas; sin carpeta no lee nada), GitHub (token fino en la bóveda; repos en lista blanca; issues/comentarios con aprobación + retención), Slack (canales en lista blanca; publicar con aprobación + retención, cancelable, una sola vez). El contenido externo llega como datos delimitados y marcados, incluida la muestra de inyección. UI de conexiones con los cuatro proveedores. Lista de verificación manual de Gmail real en `docs/runbooks/gmail-live-checklist.md`.
**No verificado:** ninguno de los cinco conectores (Gmail incluido) contra una cuenta real; sólo contra servidores falsos locales.

Plan original:

- Gmail ya tiene lectura, borrador y envío con OAuth PKCE, bóveda, envío diferido de 60 s y buzón simulado con un correo de inyección (`connections/gmail/*`, `providers/google_gmail.json`). Falta probarlo con cuenta real: requiere `GOOGLE_OAUTH_CLIENT_ID/SECRET` y una cuenta de prueba del dueño.
- Orden: 1) Gmail real lectura + borrador (checklist manual documentada), 2) Calendar (lectura, crear evento con aprobación), 3) Drive (lectura), 4) GitHub (lectura, issues con aprobación), 5) Slack (lectura de canales elegidos, publicar con aprobación).
- Cada conector = manifiesto JSON + proveedor Go con la misma interfaz que Gmail, con fake para tests.
- Esfuerzo: Gmail real 2 días (si hay credenciales); cada conector nuevo 4-6 días.

## A5 Más profesiones — hecho, ola A (integrado en `next-features`)

**Hecho:** `backend/internal/roles` con plantillas JSON embebidas; los 7 agentes del demo salen de ellas sin cambiar ids ni comportamiento (test contra la lista anterior); `GET /role-templates`, `POST /agents/from-template` (auditado); el runtime enruta y planifica con los perfiles que manda el backend; ola A (`project_manager`, `education`, `data_analyst`, `software_engineer`) disponible para contratar, no añadida al demo; "Contratar desde plantilla" en el menú de administración; los roles nuevos toman color/aspecto de la plantilla y un escritorio libre. Diálogo verificado en captura.
**Pendiente:** ola B; preguntas abiertas del catálogo (¿mantener las 8? ¿`data_analyst` separado de `analyst`?).
**No verificado:** aspecto 3D de un agente contratado en el navegador.

Plan original:

- Hoy 7 agentes fijos en código, duplicados en `domain/seed.go`, `agent-runtime/app/routing.py` y `frontend/src/lib/meta.ts`. No existe `internal/roles` ni `POST /agents/from-template` (diseñado en `professions-catalog.md`).
- Alcance: `backend/internal/roles/templates/*.json` embebidos, endpoint `GET /role-templates` y `POST /agents/from-template`, el runtime y el frontend leen la definición del agente (no listas fijas). Luego las 8 priorizadas, ola A primero (`project_manager`, `education`, `data_analyst`, `software_engineer`).
- Preguntas abiertas del catálogo (`professions-catalog.md`): ¿se mantienen esas 8? ¿`data_analyst` separado de `analyst`?
- Esfuerzo: motor 5-6 días; cada profesión 1-2 días con tests de enrutamiento.

## A6 Móvil y canales — PWA y push hechos; Slack/WhatsApp sólo diseño (integrado en `next-features`)

**Hecho:** manifest, iconos, service worker (sólo el armazón; nunca cachea `/api`), `/approvals` y `/approvals/[id]` pensados para móvil (riesgo, rol requerido, doble aprobación); Web Push con VAPID (RFC 8291, sin librería), migración 330, `GET /push/config`, `POST/DELETE /push/subscriptions`; el aviso lleva sólo título, riesgo e id (test que descifra el cuerpo). Diseño de Slack y WhatsApp en `docs/architecture/channels.md`.
**Pendiente:** Slack y WhatsApp (necesitan cuentas del dueño); con auth activo el frontend no envía el token en estas pantallas fuera del flujo de sesión — revisar al activar auth.
**No verificado:** entrega de push a un navegador real.

Plan original:

- No existe manifest, service worker ni Web Push. Alcance: PWA con bandeja de aprobaciones, Web Push (VAPID), luego Slack (botones interactivos) y WhatsApp (plantillas y consentimiento de WhatsApp Business: requiere cuenta del dueño).
- Depende de A1 (aprobar horas después de un reinicio) y A2 (identidad del que aprueba).
- Esfuerzo: PWA + push 5-7 días; Slack 5 días; WhatsApp 6-8 días + trámite de Meta.

## A7 Terminar lo a medias — en gran parte hecho (integrado en `next-features`)

**Hecho (A7a):** exportar docx/xlsx (`GET /artifacts/{id}/export`, auditado, sin inyección de fórmulas), visor PDF real con `pdfjs-dist` local, comentarios y propuestas (migración 340; los agentes proponen, sólo humanos aceptan), edición de tablero y agenda con arrastrar y teclado.
**Hecho (A7b):** horario de operación por organización (fuera de horario el trabajo nuevo se pausa visiblemente; por defecto siempre abierto), detección de anomalías v1 con reglas explicables y congelado automático opcional (apagado por defecto), notas honestas de qué se puede deshacer, guías de tono regional ampliadas. Los límites por conexión ya existían y persisten en `connection_usage`.
**Hecho (A7, rama `feat/a1c-queue`):** contadores de anomalías persistentes (scope `anomaly` en `window_counters`, migración 360; ver `07-seguridad-costos.md` 5.6 y 9.2).
**Pendiente:** subida/descarga de blobs PDF (el visor muestra marcadores hasta entonces), propuestas granulares, mapa grande de proyectos con LOD.
**No verificado:** docx/xlsx abiertos en Word/Excel; Postgres de la migración 340 contra una base real; arrastrar y soltar en navegador.

Plan original:

Lista del prompt; se verifica pieza a pieza al empezar (no auditado en esta sesión). Propuesta de orden por valor/riesgo: export docx/xlsx, visor de PDF real, comentarios/propuestas en artefactos, edición de tablero y agenda, horario de operación, límite por conexión, detección de anomalías, deshacer, tonos regionales mx/co/cl/es, mapa de proyectos con LOD.

---

## Qué necesito del dueño para seguir

1. Visto bueno al diseño de A1 y respuestas a D-A1a / D-A1b.
2. Para A2: ¿refresh token en cookie `HttpOnly` (cambia contrato de `/auth/refresh`)?
3. Para A3/A4: qué proveedores de modelo se permiten, una clave de prueba y credenciales OAuth de Google con una cuenta de prueba.
4. Para A5: confirmar las 8 profesiones.
