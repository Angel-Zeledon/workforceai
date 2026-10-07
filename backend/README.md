# Backend (Go)

Dueño del dominio, datos, permisos, eventos y orquestación. Contrato en `../docs/SPEC.md`.

## Estructura

```
cmd/server/main.go            wiring, arranque, apagado ordenado
internal/domain               entidades y nombres de eventos (contrato JSON)
internal/application          orchestrator, scheduler, approvals, queries, puertos
internal/infrastructure
  postgres/                   pgx + migraciones embebidas (migrations/*.sql) + seed
  redis/                      pub/sub de eventos y lock por tarea
  runtime/                    cliente HTTP del agent-runtime
  memory/                     store/lock en memoria (tests y modo demo sin DB)
internal/events               hub WebSocket (escucha Redis)
internal/api                  REST (chi) + WS /ws
```

## Configuración (env)

| Variable | Default | Descripción |
|---|---|---|
| `PORT` | `8080` | Puerto HTTP |
| `DATABASE_URL` | vacío | Postgres. Vacío = store en memoria (sin persistencia) |
| `REDIS_URL` | vacío | Redis. Vacío o caído = eventos y locks en proceso |
| `RUNTIME_URL` | `http://localhost:8000` | agent-runtime |
| `BUDGET_USD` | `50` | Presupuesto por org |
| `APPROVAL_ACTIONS` | `send_proposal,send_contract` | Acciones que siempre piden aprobación |
| `MAX_PARALLEL` / `TASK_TIMEOUT` / `TASK_RETRIES` / `APPROVAL_TIMEOUT` | `4` / `120s` / `3` / `30m` | Opcionales |
| `COST_CONFIRM_THRESHOLD_USD` / `REQUEST_BUDGET_CAP_USD` / `AGENT_BUDGET_USD` / `BUDGET_PAUSE_TIMEOUT` | `1.0` / `0` / `0` / `30m` | Control de costos: umbral de confirmacion de la estimacion, tope duro por solicitud y por agente (mensual), espera de una pausa por tope. `0` desactiva. Ver `docs/architecture/08-api.md` sec. 12 |
| `CONNECTIONS_KEK` / `CONNECTIONS_KEK_PREVIOUS` | vacío | Clave que envuelve las claves de datos de la bóveda (base64 de 32 bytes, `openssl rand -base64 32`). **Sin ella no se guarda ninguna credencial real** (falla cerrado); las conexiones `simulated` siguen funcionando. `..._PREVIOUS` permite rotar la KEK. Respaldarla aparte de la BD |
| `GOOGLE_OAUTH_CLIENT_ID` / `GOOGLE_OAUTH_CLIENT_SECRET` / `OAUTH_REDIRECT_URL` / `UI_BASE_URL` | vacío / vacío / `http://localhost:8080/api/v1/connections/oauth/callback` / `/` | "Trae tu app OAuth" (autoalojado) para Gmail. También se puede dar por conexión (`oauth_client_id` + `oauth_client_secret`). Ver `docs/architecture/integrations-credentials.md` sec. 18 |
| `EMAIL_HOLD_SECONDS` | `60` | Ventana de cancelación de un envío. El mínimo obligatorio es 60 s: valores menores se suben a 60, máximo 300 |
| `KILL_SWITCH` | vacío | `freeze` o `lockdown` al arrancar (y con `SIGHUP`): nada corre hasta quitarla y recargar. Ningún endpoint la levanta. También: `server ctl freeze\|lockdown\|release -org ID -reason TEXT` contra la BD |
| `APP_ENV` | vacío | `production` advierte si la KEK viene del entorno |

Las migraciones se aplican al arrancar y el seed (org `demo` + 7 agentes) es idempotente.

## Correr

```bash
# local (sin Postgres/Redis: memoria)
RUNTIME_URL=http://localhost:8000 go run ./cmd/server

# con dependencias
DATABASE_URL=postgres://user:pass@localhost:5432/aiw?sslmode=disable \
REDIS_URL=redis://localhost:6379 go run ./cmd/server

go test ./...

docker build -t aiw-backend . && docker run -p 8080:8080 aiw-backend
```

API en `http://localhost:8080/api/v1`, WebSocket en `ws://localhost:8080/ws`.

### Seguridad y multi-tenant (producción)

| Variable | Default | Descripción |
|---|---|---|
| `AUTH_ENABLED` | `false` | `true`: JWT obligatorio en `/api/v1/*` (salvo `/healthz` y `/api/v1/auth/*`) y en `/ws`; el `org_id` sale del token |
| `JWT_SECRET` | vacío | HS256, **>= 32 bytes**; obligatorio con `AUTH_ENABLED=true` (el arranque falla si falta) |
| `ALLOWED_ORIGINS` | vacío | Lista por comas para CORS y WS. Vacío + auth off = `*` (dev); vacío + auth on = solo same-origin. `*` prohibido con auth on |
| `ENABLE_DEMO_RESET` | `false` | Registra `POST /api/v1/demo/reset` (con auth: rol admin). **Omitir en producción** |
| `MIGRATE_DATABASE_URL` | = `DATABASE_URL` | Credenciales de owner solo para migraciones |
| `DB_APP_ROLE` | `app_user` | Rol sin BYPASSRLS al que cada conexión hace `SET ROLE`; vacío = no cambiar |
| `TRUSTED_PROXIES` | vacío | CIDRs/IPs cuyo `X-Forwarded-For` se respeta (rate limit) |
| `READ_HEADER_TIMEOUT` / `READ_TIMEOUT` / `WRITE_TIMEOUT` / `IDLE_TIMEOUT` | `10s` / `30s` / `60s` / `120s` | Timeouts del servidor |
| `MAX_BODY_BYTES` | `1048576` | Límite del body JSON (413 si se excede) |

`docker-compose.yml` (desarrollo) fija `AUTH_ENABLED=false` y `ENABLE_DEMO_RESET=true` para que smoke/e2e funcionen sin login.

Auth: `POST /api/v1/auth/{register,login,refresh,logout}`, `GET /api/v1/auth/me`, gestión de miembros en `/api/v1/auth/members`. WebSocket: `ws://host/ws?access_token=<jwt>` (o cabecera `Authorization`); el servidor lo cierra con código 4401 a los 15 min para forzar reconexión con token nuevo. Aprobar/rechazar (`approvals/{id}/decision`) exige rol admin u owner.

RLS: las migraciones 201-203 se aplican al arrancar. Cada llamada del store corre en una transacción con `set_config('app.org_id', ..., true)` (`postgres.Store.WithOrgTx`) y el pool usa el rol `app_user` (no owner, sin BYPASSRLS); `audit_logs` es append-only para la app. Rollbacks en `migrations/down/`.

Compromisos conocidos: el pool se conecta con las credenciales owner y hace `SET ROLE` (en producción preferible un login role miembro de `app_user` en `DATABASE_URL` + `MIGRATE_DATABASE_URL`); el presupuesto (`BUDGET_USD`) y `Reset` cancelan/aplican a nivel de proceso, no por organización; `register` es público.

Tests con Postgres real: `TEST_DATABASE_URL=postgres://postgres:pw@localhost:55432/postgres?sslmode=disable go test ./internal/...` (ver `store_rls_test.go`).
