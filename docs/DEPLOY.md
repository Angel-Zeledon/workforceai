# Despliegue en producción

Stack: Caddy (TLS Let's Encrypt) -> frontend (Next.js) / backend (Go) -> runtime (Python), Postgres y Redis.
Se usa `docker-compose.prod.yml` (compose **independiente**, no se combina con el `docker-compose.yml` de dev).

## Arquitectura

- Un solo dominio. Caddy envía `/api/*` y `/ws` a `backend:8080`; el resto a `frontend:3000`.
- Solo Caddy publica puertos (80, 443 TCP/UDP). Postgres, Redis, runtime, backend y frontend viven en la red `internal` (sin salida a internet).
- El runtime también se une a la red `egress` para llamar a la API del/los proveedor(es) de modelo configurado(s) (DeepSeek, Anthropic y/o un endpoint compatible; ver "Proveedores de modelo").
- El frontend se construye con URLs relativas (`/api/v1`) y deriva el WebSocket de `window.location` (`wss://` bajo HTTPS), por lo que **no depende de variables `NEXT_PUBLIC_*` horneadas**.
- Imágenes: `ghcr.io/<OWNER>/aiworkforce-{backend,frontend,agent-runtime}:<TAG>`.

## Requisitos previos (manuales)

1. Servidor Linux con Docker Engine y el plugin Compose v2; puertos 80 y 443 abiertos en el firewall.
2. Dominio con registro DNS `A` (y `AAAA` si hay IPv6) apuntando al servidor. Let's Encrypt falla si el DNS no resuelve aún.
3. Paquetes GHCR accesibles: si son privados, `docker login ghcr.io` con un PAT con `read:packages`.
4. Secrets/variables del repositorio en GitHub (ver "CI").

## Pasos

```bash
git clone <repo> && cd <repo>
cp .env.prod.example .env.prod
chmod 600 .env.prod
# Generar secretos (hex, sin caracteres especiales):
openssl rand -hex 32   # POSTGRES_PASSWORD, REDIS_PASSWORD, JWT_SECRET (uno distinto cada vez)
$EDITOR .env.prod      # DOMAIN, ACME_EMAIL, ALLOWED_ORIGINS, OWNER, TAG, claves de proveedor de modelo...

# Validar antes de arrancar (falla si falta una variable obligatoria)
docker compose -f docker-compose.prod.yml --env-file .env.prod config -q

# Opción A: imágenes publicadas por CI
docker compose -f docker-compose.prod.yml --env-file .env.prod pull
# Opción B: build local
# docker compose -f docker-compose.prod.yml --env-file .env.prod build

docker compose -f docker-compose.prod.yml --env-file .env.prod up -d
docker compose -f docker-compose.prod.yml --env-file .env.prod ps   # todo "healthy"
curl -fsS https://$DOMAIN/ -o /dev/null && echo OK
```

Nota: `DATABASE_URL`/`REDIS_URL` incluyen las contraseñas en la URL; usa secretos en hex (o codifícalos con percent-encoding).

## Checklist de salida a producción

- [ ] DNS apunta al servidor; 80/443 abiertos; ningún otro puerto publicado (`docker compose ... ps` solo muestra caddy con puertos).
- [ ] `.env.prod` con permisos 600, fuera de git (`.gitignore` ya ignora `.env.*`).
- [ ] `JWT_SECRET`, `POSTGRES_PASSWORD`, `REDIS_PASSWORD` únicos y aleatorios (>= 32 hex).
- [ ] `ALLOWED_ORIGINS=https://<DOMAIN>` exacto; `AUTH_ENABLED=true`; `ENABLE_DEMO_RESET=false`; `SIMULATION=false`.
- [ ] Al menos un proveedor de modelo válido (`DEEPSEEK_API_KEY`, `ANTHROPIC_API_KEY` o `CUSTOM_LLM_BASE_URL`) y `ALLOWED_PROVIDERS` acorde a la política de datos de la empresa; `BUDGET_USD` definido.
- [ ] `TAG` apunta a un `sha-xxxxxxx` concreto (no `latest`) y es el que pasó el CI.
- [ ] Todos los servicios `healthy`; login funciona; WebSocket conecta (`wss://<DOMAIN>/ws`).
- [ ] Backup de Postgres programado y restauración probada al menos una vez.
- [ ] Rotación de logs activa (json-file 10m x 5, ya configurado) y límites de memoria/CPU adecuados al host.
- [ ] Actualizaciones del SO y de imágenes base planificadas.

## Proveedores de modelo

El runtime soporta tres proveedores y exige **al menos uno** (ya no solo DeepSeek): `deepseek` (`DEEPSEEK_API_KEY`), `anthropic` (`ANTHROPIC_API_KEY`) y `custom` (`CUSTOM_LLM_BASE_URL` + opcional `CUSTOM_LLM_API_KEY`/`CUSTOM_LLM_MODEL`; endpoint compatible OpenAI regional o local). Referencia completa de variables: `agent-runtime/README.md`.

- **Orden de respaldo**: `MODEL_PROVIDER_ORDER` (def. `deepseek,anthropic,custom`; use `anthropic,deepseek` para Anthropic primario). Ante error, timeout o límite de tasa se reintenta una vez y se pasa al siguiente proveedor permitido; la respuesta y el costo registran `provider` y `model`. Por rol: `MODEL_PROVIDER_ORDER_<ROL>` (p. ej. `MODEL_PROVIDER_ORDER_LEGAL=anthropic`).
- **Excluir proveedores**: `ALLOWED_PROVIDERS=anthropic,custom` impide que cualquier dato llegue a DeepSeek en todo el despliegue (ni siquiera como respaldo). Si ningún proveedor permitido está configurado o disponible, el runtime devuelve un error explícito (HTTP 422 `no_allowed_provider` o 502 `all_providers_failed`) y no envía nada. Por organización/solicitud existe el campo opcional `allowed_providers` en el contrato del runtime; **el backend Go todavía no lo envía** (pendiente de adopción).
- **Pendiente en la infraestructura** (fuera del runtime): `docker-compose.prod.yml` aún exige `DEEPSEEK_API_KEY` con `:?` y no pasa al runtime `CUSTOM_LLM_*`, `MODEL_PROVIDER_ORDER*`, `ALLOWED_PROVIDERS`, `*_MODEL`, `PRICE_*` ni `PROVIDER_*`; `.env.prod.example` solo lista DeepSeek. Hasta actualizarlos, esas variables no llegan al contenedor, y un endpoint `custom` debe ser alcanzable desde la red del runtime.

### Qué datos salen a cada proveedor

Salen únicamente los prompts de cada llamada, ya con secretos redactados (`redact.py`, segunda barrera tras el backend): texto de la solicitud del usuario, persona/responsabilidades/herramientas del agente, salidas de tareas dependientes, memoria del agente, contenido externo y resultados de herramientas (correos, documentos) ya filtrados por el backend, y las preguntas de consulta/síntesis. **No salen** credenciales, `connection_id`, URLs de API ni tokens (el contrato no los incluye). Un proveedor recibe esos datos solo si atiende la llamada o es un respaldo permitido:

| Proveedor | Destino | Nota |
|---|---|---|
| `deepseek` | API de DeepSeek | Tercero; revise sus condiciones de residencia y retención antes de enviar datos de clientes |
| `anthropic` | API de Anthropic | Revise el DPA y la retención del plan contratado |
| `custom` | `CUSTOM_LLM_BASE_URL` (lo que usted opere: regional, VPC, local) | Los datos no salen de su perímetro si el endpoint es propio |

Este documento no verifica las políticas de retención de ningún proveedor: valídelas contractualmente.

## Actualizar a un nuevo TAG

```bash
# 1) pg_dump (ver sección Backups, siempre antes de migrar)
sed -i 's/^TAG=.*/TAG=sha-NUEVO/' .env.prod
docker compose -f docker-compose.prod.yml --env-file .env.prod pull
docker compose -f docker-compose.prod.yml --env-file .env.prod up -d
```

## Backups (pg_dump)

```bash
C="docker compose -f docker-compose.prod.yml --env-file .env.prod"
mkdir -p backups
$C exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' \
  > backups/workforce-$(date +%F-%H%M).dump
```

Programar con cron (diario) y copiar fuera del servidor (S3, rsync, etc.); conservar N días.
Redis tiene AOF en el volumen `redisdata`; trátalo como caché/cola, el dato maestro es Postgres.

Restaurar:

```bash
$C stop backend
$C exec -T postgres sh -c 'dropdb -U "$POSTGRES_USER" --if-exists "$POSTGRES_DB" && createdb -U "$POSTGRES_USER" "$POSTGRES_DB"'
$C exec -T postgres sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner' < backups/ARCHIVO.dump
$C start backend
```

## Rollback

**Aplicación (sin cambios de esquema):** vuelve `TAG` al valor anterior y `pull` + `up -d`.

**Migraciones:** el backend aplica migraciones SQL embebidas al arrancar, solo hacia adelante (tabla `schema_migrations`, sin migraciones "down"). Por tanto:

1. Antes de cada despliegue con migraciones nuevas, haz `pg_dump` (arriba).
2. Si la nueva versión falla y el esquema ya cambió, hay dos caminos:
   - **Restaurar el dump** previo (se pierden los datos escritos después) y volver al `TAG` anterior.
   - **Migración correctiva**: escribir una migración nueva que revierta el cambio (preferible si ya hay datos nuevos) y desplegarla.
3. Si la migración es compatible hacia atrás (solo añade columnas/tablas), el `TAG` anterior puede seguir funcionando sin tocar la BD.
4. Para saber qué se aplicó: `$C exec postgres psql -U workforce -d workforce -c 'select * from schema_migrations order by applied_at'`.

## Rotar secretos

Siempre: editar `.env.prod` y recrear los servicios afectados (`up -d` recrea los que cambian).

- **JWT_SECRET**: cambiar y `up -d backend`. Invalida todas las sesiones/tokens; los usuarios deben volver a iniciar sesión.
- **DEEPSEEK_API_KEY / ANTHROPIC_API_KEY / CUSTOM_LLM_API_KEY**: crear la nueva clave en el proveedor, actualizar, `up -d agent-runtime`, revocar la antigua.
- **REDIS_PASSWORD**: cambiar, `up -d redis backend` (redis se reinicia con la nueva `requirepass`; el AOF se conserva).
- **POSTGRES_PASSWORD**: la variable solo se usa al inicializar el volumen; hay que cambiarla también en la BD:
  ```bash
  $C exec postgres psql -U workforce -d workforce -c "ALTER USER workforce PASSWORD 'NUEVA'"
  # luego actualizar POSTGRES_PASSWORD en .env.prod y: $C up -d backend
  ```
- **Secrets de CI (GitHub)**: regenerar el PAT/variables en Settings -> Secrets and variables.
- Tras una posible filtración: rotar todo lo anterior, revisar logs (`docker compose logs caddy backend`) y considerar restaurar desde backup limpio.

## CI (GitHub Actions)

`.github/workflows/ci.yml`: build/test, `govulncheck` y `npm audit` (no bloqueantes), `hadolint` y smoke. En push a `main` el job `publish` sube a GHCR las imágenes con tags `sha-<corto>` y `latest`.

Configurar en el repositorio (manual):
- Settings -> Actions -> General -> Workflow permissions: permitir que `GITHUB_TOKEN` escriba paquetes (el job declara `packages: write`).
- Variables (opcionales) `NEXT_PUBLIC_API_URL` y `NEXT_PUBLIC_WS_URL`: dejar **sin definir** para el despliegue de un solo dominio (rutas relativas).
- Tras el primer publish, en GitHub -> Packages, enlazar los paquetes al repo y ajustar visibilidad.

## Operación

```bash
$C logs -f backend          # logs
$C ps                       # salud
$C restart backend
$C down                     # detener (los volúmenes se conservan; NO usar -v salvo para borrar datos)
```
