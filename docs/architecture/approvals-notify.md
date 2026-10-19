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
  tope y nada cambia). El tope cuenta el gasto de toda la vida de la organización.
- `GET /plan/usage` (`metrics:read`) alimenta `PlanCapBanner` (en `TrialBanner.tsx`): barra, texto
  `gastado · estimado · tope`, aviso al 80 % y estado "en pausa".

Nunca hay sobreconsumo silencioso: el tope se comprueba antes de gastar, la pausa es visible y la causa se
notifica. Lo que sí puede pasar es que el costo real de una llamada ya iniciada supere su estimación; el excedente
queda visible (`percent_used` 100, `spent_usd` real) y bloquea todo lo siguiente.

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

`NOTIFY_PUBLIC_URL`, `NOTIFY_LINK_SECRET`, `NOTIFY_LINK_TTL_HOURS`, `FREE_PLAN_CAP_USD`, `REWARDS_DAILY_GOAL`.

## Pendiente

- Cifrar las URLs de webhook con la bóveda (hoy: texto en una columna con RLS, redactada en el export y
  enmascarada en la API).
- Reinicio mensual del tope y un checkout real para pasar de gratis a pago (hoy solo se informa).
- Reintentos con cola para los webhooks y el correo (hoy: un intento, error en el log).
- Pantalla para cambiar el plan de una organización (hoy: tabla `org_plans` o `FREE_PLAN_CAP_USD`).
