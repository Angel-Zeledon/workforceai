# Aprobaciones fuera de la app, plan gratis con tope duro, meta del día y primeros pasos

Implementa los puntos 3 a 5 de `docs/competitive-review-2026-10.md` sobre las aprobaciones de
`06-permisos-autonomia.md`. Código: `backend/internal/notify`, `backend/internal/policy/spendcap.go`,
`backend/internal/rewards/daily.go`, migraciones 720 y 721.

## 1. La tarjeta de aprobación: una vez / sesión / proyecto / rechazar

`POST /approvals/{id}/decision` acepta `scope`:

| scope | efecto |
|---|---|
| `once` (o ausente) | aprueba solo esta acción |
| `session` | aprueba y crea un **permiso permanente** (`standing approval`) de 8 h (`SessionStandingTTL`) |
| `project` | aprueba y crea el permiso permanente de siempre (30 días, `always_allow`) |

No hay un concepto nuevo: `session` y `project` **reusan `StandingService.Grant`**, con las mismas reglas
(solo acciones internas y reversibles de un proyecto, riesgo bajo o medio, un solo aprobador, sin regla de la
organización, nunca `code.run`), la misma revocación (pestaña Aprobaciones del proyecto) y la misma auditoría
(`standing_approval.created/used/revoked`). `scope` con `reject` o con un valor desconocido es 400.

"Recordar para este destinatario" **no se implementó a propósito**: las acciones que tienen destinatario
(enviar, publicar, pagar) están excluidas de los permisos permanentes por diseño; un permiso por destinatario
ampliaría ese borde. Si algún día se quiere, el sitio es `StandingEligibility`, con una decisión de producto
explícita.

UI: `ApprovalsTray.tsx` (casilla "recordar" + elección sesión/proyecto, el botón cambia de texto) y
`approvals/ApprovalsMobile.tsx` (botones "para esta sesión" / "para este proyecto").

## 2. Aprobar desde fuera de la app

### Canales (por organización, `notify_settings`, migración 720)

- Correo (SMTP del servidor) a hasta 10 destinatarios.
- Webhook entrante de **Slack** y de **Microsoft Teams** (tarjeta adaptable). Solo `https`, sin IP, sin puerto
  y solo los hosts de cada producto (`hooks.slack.com`, `*.webhook.office.com`, `*.logic.azure.com`,
  `*.powerplatform.com`): el notificador no puede apuntarse a una dirección interna (SSRF). Se revalida al
  enviar. La URL es un secreto: la API nunca la devuelve (solo dónde apunta) y el export de datos la redacta.
  **Selladas con la bóveda** (migración 783): con clave de bóveda la URL se guarda cifrada con la clave de datos de la
  organización en `slack_webhook_enc` / `teams_webhook_enc` (`vault.Seal`, propósitos `notify-webhook-slack|teams`, así
  una URL de Slack no abre como Teams ni en otra organización) y las columnas en claro quedan en `''`. Las filas
  anteriores se leen una vez y se sellan solas (`UPDATE` solo si la columna sellada sigue vacía). Sin clave de bóveda
  todo sigue como antes (URL en la columna en claro). Si un valor sellado no se puede abrir (clave retirada) el canal
  queda apagado y se registra sin el secreto; un admin lo vuelve a guardar.
- `min_risk` (desde qué riesgo avisar), `links_enabled`, `locale` (es/en/pt) del aviso.

API (permiso `approvals:decide`): `GET/PUT /notify/settings`, `POST /notify/test`.
UI: tarjeta `NotifySettingsCard` en el Centro de seguridad.

### Enlaces firmados

`HMAC-SHA256(clave, payload)` con `{org, approval, decision, to, exp, jti}`. Reglas:

1. **De un solo uso**: el `jti` se consume en `notify_token_use` (clave primaria `(org_id, jti)`; en memoria
   sin base de datos). El consumo es atómico: con 20 clics simultáneos decide uno.
2. **Caducan** (24 h por defecto, `NOTIFY_LINK_TTL_HOURS`, máximo 7 días).
3. **Abrir no decide**: `GET /api/v1/notify/a?t=` solo muestra la confirmación (los escáneres de correo y
   las vistas previas de chat abren los enlaces). Decide el `POST` del botón "Confirmar". La página se sirve con
   `no-store`, `no-referrer`, CSP cerrada y sin iframes.
4. Un enlace es de **una** aprobación y **una** decisión. Cada destinatario recibe sus propios enlaces; el
   actor queda como `link:<destinatario>` en la auditoría.
5. **Riesgo alto, doble aprobación, rol requerido, maker-checker y `code.run` nunca aceptan enlace**: el aviso
   se envía sin botones y apunta a `/approvals/<id>` en la app (con sesión). Se comprueba al enviar **y** al usar
   (`LinkAllowed`), así que un enlace firmado para algo que luego se volvió pesado también se rechaza.
6. La clave sale de `NOTIFY_LINK_SECRET` (>= 32 bytes) o se deriva de `JWT_SECRET` con una etiqueta propia.
   Sin clave o sin URL pública (`NOTIFY_PUBLIC_URL`, por defecto `UI_BASE_URL`) no se ofrecen enlaces; los avisos
   siguen saliendo.
7. Un enlace no concede rol: la decisión cuenta como de un admin (los destinatarios los eligió un admin) y solo
   dentro de lo que `LinkAllowed` permite. Nunca crea permisos permanentes.

Los avisos salen de un decorador del publicador de eventos (`notify.Observer`, como `push.Notifier`) en segundo
plano: `approval.requested` y, para el plan, `budget.exceeded` de la organización. Un fallo del canal se registra
y nunca bloquea la aprobación.

### Cola de salida con reintentos (`notify_outbox`, migración 784)

Los avisos (correo, Slack, Teams) ya no se envían en línea: `Service` escribe una fila por mensaje y destino en
`notify_outbox`, hace el primer intento enseguida y un trabajador (`Service.StartOutbox`, cada 10 s por las
organizaciones con filas pendientes y un barrido cada 2 min que recoge lo que dejó un reinicio u otra réplica) reintenta.
- Arrendamiento con `FOR UPDATE SKIP LOCKED` (mueve `next_attempt_at` 2 min mientras un trabajador la tiene): dos réplicas
  nunca toman la misma fila; un trabajador que muere la deja para el siguiente intento.
- Espera exponencial con jitter (30 s, 2 min, 10 min, 1 h, 6 h) y hasta 6 intentos; después `status = 'dead'` (carta
  muerta, con `last_error`, sin la URL). Es definitivo de inmediato (`dead`) lo que no puede mejorar con reintentos:
  dirección no permitida, canal o destinatario quitados, o respuesta 4xx (salvo 408, 425 y 429).
- La URL del webhook **no** se guarda en la fila: se lee de los ajustes al enviar, así que quitar un canal o un
  destinatario detiene lo que quedaba en cola. El `payload` (puede llevar enlaces firmados) se sella con la bóveda
  (`sealed = true`, propósito `notify-outbox`) y, si el sellado falla, no se encola en claro: se envía en línea una vez.
  Al enviarse bien la fila suelta el payload; sent y dead se borran a los 30 días.
- Cartas muertas: `GET /notify/outbox/dead` (últimas 100, sin payload) y `POST /notify/outbox/dead/{id}/retry` (`approvals:decide`,
  como el resto de `/notify/*`; bajo RLS de la organización). Reenviar devuelve la fila a `pending` con `attempts = 0`, vence ya y se audita
  `notify.outbox_retry`; el `UPDATE ... WHERE status = 'dead'` es la reclamación (un doble clic reenvía una vez; otra fila o tenant = 404).
  La consola las muestra en la pestaña "Cola de notificaciones" (`AdminNotifyQueue`).
- `POST /notify/test` sigue siendo síncrono (el admin necesita la respuesta). `NOTIFY_OUTBOX=off` vuelve al envío en
  línea de un solo intento. Sin Postgres se usa una cola en memoria (reintenta, no sobrevive a un reinicio).

## 3. Plan gratis con tope DURO

- `policy/spendcap.go`: funciones puras (`CapAllows`, `EffectiveCap`, `EvaluateCap`). El tope es duro: si
  `gastado + reservado + estimación` lo pasa, la llamada **no empieza**. Un tope <= 0 en un plan duro no permite
  nada (falla cerrado). El plan gratis se informa como `gastado / estimado (gastado + reservado) / tope`.
- Se conecta al presupuesto que ya existe: `Budget.OrgCapFor` (hook, 0 = nada) estrecha `Config.BudgetUSD`; gana
  el más estricto. Lo aplican `Reserve` (todas las llamadas, el chat incluido), las advertencias al 80 % y
  `/budget`. Al pasar el tope el flujo existente **pausa y avisa** (`budget.exceeded`, no reanudable por ser
  de la organización); `notify.Observer` además manda un aviso por los canales (una vez al día).
- Qué organización es gratis: `org_plans` (migración 721) por organización; sin fila, el valor de despliegue
  `FREE_PLAN_CAP_USD` (> 0 = toda organización sin plan es gratis con ese tope; sin definir = ningún plan con
  tope y nada cambia). El tope se reinicia cada mes natural UTC (migración 785): `org_plans.spend_base_usd` es el gasto
  de toda la vida al empezar el periodo (`period_start`) y el plan solo cuenta `vida - base`. Al presupuesto
  (`OrgCapFor`, que compara con el gasto de toda la vida) se le da `base + tope`; `GET /plan/usage` muestra el mes
  (gastado y tope menos la base). `notify.PlanResetter` (cada hora, `PLAN_RESET_JOB=off` lo apaga) recorre las
  organizaciones con fila en `org_plans`; el `UPDATE ... WHERE period_start IS NULL OR period_start < <día 1>` es la
  reclamación, así que réplicas y reinicios nunca reinician dos veces. La primera pasada solo sella `period_start`
  (no cambia la base: nadie se desbloquea de golpe). Al reiniciar se borra `cap_notified_at` (el aviso de tope se puede
  volver a mandar) y se audita `plan.period_reset`. Una organización sin fila (valor por defecto
  `FREE_PLAN_CAP_USD`) recibe una en la primera pasada (`PlanStamper.StampDefaultPlan`: `INSERT ... ON CONFLICT DO NOTHING`, plan `free`,
  `cap_usd` = el valor por defecto de ese momento, `period_start` = día 1, base 0, `updated_by = plan-resetter`): sin cupo extra, igual que el
  sellado de un plan explícito, y desde el mes siguiente se reinicia como cualquier otro. Esa fila fija el tope vigente al crearla (cambiar
  `FREE_PLAN_CAP_USD` después no la cambia; se edita en `org_plans`). Sin `FREE_PLAN_CAP_USD` no se crea nada. `provider` / `provider_ref`
  (migración 785) son reservas para un proveedor de pagos; nada los lee ni cobra a nadie.
- `GET /plan/usage` (`metrics:read`) alimenta `PlanCapBanner` (en `TrialBanner.tsx`): barra, texto
  `gastado · estimado · tope`, aviso al 80 % y estado "en pausa".

Nunca hay sobreconsumo silencioso: el tope se comprueba antes de gastar, la pausa es visible y la causa se
notifica. Lo que sí puede pasar es que el costo real de una llamada ya iniciada supere su estimación; el excedente
queda visible (`percent_used` 100, `spent_usd` real) y bloquea todo lo siguiente.

### 3.1 Planes: Free, Pro y Max

El catálogo vive en `policy/plans.go` (`policy.Catalog`) y se sirve tal cual en `GET /plans` (público, para las páginas
de precios). Un plan es un precio más límites DUROS:

| Plan | Mensual | Anual | IA al mes (tope duro) | Proyectos activos | Tareas programadas | Personas | Almacenamiento | Agentes |
|------|--------:|------:|----------------------:|------------------:|-------------------:|---------:|---------------:|--------:|
| Free | 0       | 0     | 1 USD                 | 1                 | 2                  | 2        | 500 MB         | 20      |
| Pro  | 29 USD  | 290   | 12 USD                | 5                 | 10                 | 10       | 10 GB          | 20      |
| Max  | 99 USD  | 990   | 45 USD                | 20                | 20                 | 50       | 50 GB          | 20      |

- El cupo de IA es el `cap_usd` de `org_plans` y se aplica igual que el tope del plan gratis de arriba: los tres planes
  del catálogo son duros (`policy.HardCapped`), con el mismo reinicio mensual. `paid` queda como el "sin plan" de un
  despliegue sin planes: sin tope y sin límites de plan (solo los máximos del sistema). Migración 805: `org_plans.plan`
  acepta `free`, `pro`, `max` y `paid`.
- Los demás límites son cuentas que se comprueban donde se crea la cosa: agentes (`POST /agents/from-template` y aplicar
  una industria en el organigrama), proyectos activos (al lanzar), tareas programadas (`OrgConfig.CreateSchedule`),
  personas = miembros + invitaciones pendientes (`auth.CreateInvitation`; reinvitar a la misma dirección no suma) y
  almacenamiento (la cuota de Drive, cuando la del plan es menor que la del despliegue). Pasar un límite nunca borra
  nada: solo rechaza lo siguiente con `409` y `{"code":"plan_limit","limit_kind","limit","plan"}`
  (`domain.PlanLimitError`; el servicio de auth responde `{"error":{"code":"plan_limit","message"}}`). El
  almacenamiento sigue siendo `ErrQuota` de Drive (`413`), pero cuando la cuota alcanzada es la del plan el cuerpo
  lleva `code: "plan_limit"` con `limit_kind: "storage"`, `limit` (bytes) y `plan`; la cuota del despliegue sigue
  respondiendo `quota_exceeded`.
- Los agentes no se venden: todos los planes traen la oficina completa (20, el máximo del sistema).
- `GET /plan` (`metrics:read`): plan, límites, lo usado de cada uno (`-1` = desconocido en este despliegue), el cupo
  de IA del mes y el catálogo. `PUT /plan {"plan":"pro"}` (`billing:manage`, auditado `plan.changed`) solo con
  `PLAN_SELF_SERVE=on`; no hay proveedor de pagos, así que no cobra nada. Al cambiar, el mes en curso conserva su base:
  subir de plan sube el tope al momento; bajar puede dejar a la organización por encima del nuevo tope hasta el día 1.
- Front: Ajustes → Plan y uso (`PlanBilling.tsx`) muestra el plan, una barra por límite y el catálogo (mensual / anual);
  `PlanCapBanner` nombra el plan y enlaza ahí (también sin login, `AUTH_ENABLED=false`). La fila de Ajustes solo aparece
  si el despliegue usa planes. `authFetch` detecta cualquier `409`/`413` con `plan_limit` (de cualquier pantalla) y
  `TrialBanner` muestra qué límite fue y el enlace para mejorar el plan.
- Cobro: con `ONVO_SECRET_KEY` los planes se pagan con ONVO Pay (formulario de ONVO dentro de la pantalla de plan); el
  plan cambia cuando el backend ve el pago en ONVO, y `org_plans.provider` / `provider_ref` / `provider_sub` /
  `provider_pending` guardan cliente y suscripciones (`billing-onvo.md`). Sin ONVO, `PUT /plan` (solo con
  `PLAN_SELF_SERVE=on`) cambia el plan sin cobrar.
- Encendido: `PLANS=on` hace Free a toda organización sin plan, con el cupo de IA de Free del catálogo
  (`FREE_PLAN_CAP_USD` > 0 también lo enciende y fija ese cupo).

## 4. Meta del día

`rewards/daily.go`. Una meta por día UTC, elegida por la fecha (rotación fija: 1, 2 o 3 tareas, o una aprobada a
la primera). El progreso se **deriva del libro de monedas** (solo entradas `earn` de tareas terminadas de hoy),
la recompensa es una entrada `daily_goal` con clave idempotente `earn:daily:<fecha>`: paga como máximo una vez
al día, aunque haya reintentos, reinicios o réplicas. No hay endpoint que dé monedas; `GET /rewards/daily-goal`
solo lee (y paga si el objetivo ya se cumplió). Se activa con `Service.Daily` (`REWARDS_DAILY_GOAL=false` lo
apaga; apagado no cambia nada de la economía).

## 5. Primeros pasos (4)

`ActivationChecklist.tsx`: contexto del negocio, conectar una herramienta, elegir autonomía, primera tarea.
Cada paso se detecta con datos reales (ajustes de la organización / una conexión activa / un agente en
`rules` o `autonomous` o la oficina configurada / una solicitud), y una vez hecho queda hecho. La meta del día
se muestra al lado como una pastilla.

## Variables

`NOTIFY_PUBLIC_URL`, `NOTIFY_LINK_SECRET`, `NOTIFY_LINK_TTL_HOURS`, `FREE_PLAN_CAP_USD`, `REWARDS_DAILY_GOAL`,
`NOTIFY_OUTBOX` (`off` = sin cola), `PLAN_RESET_JOB` (`off` = sin reinicio mensual), `PLANS` (`on` = catálogo de planes,
toda organización sin plan en Free), `PLAN_SELF_SERVE` (`on` = la persona dueña cambia el plan desde la app).

## Pendiente

- Sellar con la bóveda también la columna en claro de filas antiguas que nadie vuelva a leer (se sellan al leerlas).
- Publicar precios en la landing (hoy dice "todavía no hay precios").
