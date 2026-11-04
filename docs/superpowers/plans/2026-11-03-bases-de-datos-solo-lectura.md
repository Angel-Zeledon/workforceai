# Base de datos (solo lectura) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Una conexión genérica "Base de datos (solo lectura)" (PostgreSQL primero) que los agentes concedidos pueden consultar con `db.schema` y `db.query`, sin poder escribir nunca, aunque el usuario de la base tenga permisos de escritura.

**Architecture:** Un proveedor nuevo `database` dentro del marco de conexiones que ya existe (vault, grants, gateway, sanitizer, auditoría). La configuración no secreta (host, puerto, base, usuario, SSL, esquemas permitidos) va en una columna nueva `connections.database` (JSONB); la contraseña va al vault como cualquier credencial. El adaptador `internal/connections/database` abre una conexión por llamada, valida el SQL (una sentencia, solo SELECT/WITH/EXPLAIN, sin funciones peligrosas), corre en `BEGIN READ ONLY` con `SET LOCAL statement_timeout/idle_in_transaction_session_timeout/lock_timeout`, corta en el tope de filas o de bytes y siempre hace ROLLBACK.

**Tech Stack:** Go (pgx v5), Python (runtime, solo el prompt), Next.js/TypeScript (Conexiones, menú principal).

**Spec:** el encargo del dueño (2026-11-03), `docs/architecture/integrations-credentials.md`, `docs/architecture/link-imports.md`; la página nueva es `docs/architecture/database-sources.md`.

## Global Constraints

- Genérico, sin marca (regla v1): el proveedor `database` nunca se apaga con `VENDOR_INTEGRATIONS`, como `custom_api`.
- Contraseña solo en el vault (`CONNECTIONS_KEK`); nunca en logs, eventos, respuestas, mensajes del agente; sin `hint`.
- Topes: `statement_timeout` 15 s, `idle_in_transaction_session_timeout` 20 s, `lock_timeout` 3 s, 500 filas, 1 MiB por consulta.
- Una sentencia por llamada, protocolo extendido (`QueryExecModeDescribeExec`), solo `SELECT`/`WITH`/`EXPLAIN` (sin `ANALYZE`).
- Rechazo léxico (fuera de literales, identificadores entre comillas y comentarios): INSERT UPDATE DELETE MERGE CREATE ALTER DROP TRUNCATE GRANT REVOKE COPY CALL DO SET RESET LOCK LISTEN NOTIFY UNLISTEN VACUUM ANALYZE INTO EXECUTE PREPARE DEALLOCATE DECLARE FETCH BEGIN COMMIT ROLLBACK SAVEPOINT; `FOR SHARE`; funciones `pg_sleep*`, `dblink*`, `lo_*`, `pg_read_*`, `pg_ls_*`, `pg_stat_file`, `set_config`, `nextval`, `setval`, `pg_terminate_backend`, `pg_cancel_backend`, `pg_reload_conf`, `pg_advisory*`, `pg_notify`, `query_to_xml*`, `table_to_xml*`, `cursor_to_xml*`, `database_to_xml*`, `schema_to_xml*`, `pg_logical_*`, `pg_file_*`, `pg_switch_wal`, `pg_promote`, `pg_create_*`, `pg_drop_*`, `pg_replication_*`, `pg_backup_*`.
- Direcciones privadas rechazadas por defecto (igual que `custom_api`); `DATABASE_ALLOW_PRIVATE=true` solo para self-hosted.
- Migración aditiva `816_connection_database.sql` + `backend/migrations/down/816_connection_database_down.sql`.
- Textos es/en/pt; `data-testid` con prefijo `conn-`.
- Las pruebas automáticas usan un Postgres desechable propio (`postgres:16.6-alpine`, puerto 5440, `TEST_DATABASE_URL`); nunca la base del dueño.

## Review Focus

- Un usuario de la base con permisos de escritura y un `WITH x AS (INSERT …) SELECT` o una función VOLATILE que escribe: debe fallar en la base (READ ONLY) aunque pase el filtro léxico → `TestWritesFailEvenWithWritePrivileges`.
- `;` dentro de un literal o comentario no es "dos sentencias", y `SELECT 1; DROP TABLE t` sí → `TestCheckSQL`.
- Una consulta que devuelve millones de filas no se queda leyendo: corta en 500 filas / 1 MiB y marca `truncated` → `TestRowAndByteCaps`.
- Un error de SQL (tabla que no existe) le llega al agente como dato para corregir, sin la contraseña → `TestSQLErrorIsDataAndNeverCarriesThePassword`.
- La cadena de conexión pegada con contraseña: el servidor la separa; la contraseña va al vault y no vuelve en el GET → `api.TestDatabaseConnectionFromConnectionString`.

---

### Task 1: Configuración y validación (`connections.Database`)

**Files:** Create `backend/internal/connections/database_source.go`, test `backend/internal/connections/database_source_test.go`; modify `types.go` (campo `Database *Database \`json:"database,omitempty"\``), `service.go` (Create/Patch/view/Handle), migración 816 + down, `infrastructure/postgres/connections.go`.

**Interfaces — Produces:**
- `type Database struct { Engine, Host string; Port int; Name, User, SSLMode, Description string; Schemas []string }`
- `func (d Database) Normalize() (Database, error)` — engine `postgres` (`mysql` → `engine_not_supported`), port 1..65535 (default 5432), sslmode ∈ {disable, require, verify-ca, verify-full} (default `require`), ≤ 20 esquemas `^[A-Za-z_][A-Za-z0-9_$]{0,62}$`.
- `func ParseConnString(s string) (Database, string, error)` — `postgres://` / `postgresql://` URL; devuelve config y contraseña.
- `const DatabaseProvider = "database"`; `Handle.Database *Database`; `Handle.Secret func(ctx, func([]byte) error) error`.

- [ ] Tests: rechaza host vacío, puerto 0, sslmode raro, esquema con comillas; `ParseConnString("postgresql://u.ref:p%40ss@h:6543/db?sslmode=verify-full")` → user `u.ref`, pass `p@ss`, port 6543.
- [ ] Implementar; `go test ./internal/connections/ -run Database` pasa.

### Task 2: Adaptador `internal/connections/database`

**Files:** Create `sqlcheck.go`, `postgres.go`, `provider.go`, tests `sqlcheck_test.go`, `pg_test.go` (con `TEST_DATABASE_URL`), manifest `providers/database.json` (capacidad `db.read`, herramientas `db.schema`, `db.query`, `never_offered: ["db.write"]`).

**Interfaces — Produces:** `func CheckSQL(q string) error` (códigos `sql_empty`, `sql_multiple_statements`, `sql_not_read_only`, `sql_forbidden_function`, `sql_too_long`); `func New(allowPrivate bool) (*Provider, error)`; `func (p *Provider) Schema(ctx, h) (SchemaResult, error)`; `func (p *Provider) Query(ctx, h, sql string, maxRows int) (QueryResult, error)`; `QueryResult{Columns []string; Rows [][]any; Truncated bool; SQLState, Message string}`.

- [ ] `TestCheckSQL` (tabla de casos de Global Constraints, literales con `;`, `$$…$$`, comentarios anidados).
- [ ] `TestWritesFailEvenWithWritePrivileges`: rol dueño de la tabla; con el filtro saltado (`queryUnchecked`) INSERT/UPDATE/CTE con INSERT/función VOLATILE/`nextval` fallan con SQLSTATE `25006`; el conteo de filas no cambia.
- [ ] `TestRowAndByteCaps`, `TestStatementTimeout` (`pg_sleep` vía función envoltorio → `57014`), `TestSchemaListsTablesColumnsAndComments`, `TestSchemaAllowlist`.
- [ ] `TestSQLErrorIsDataAndNeverCarriesThePassword`.

### Task 3: Gateway, API y runtime

**Files:** modify `gateway/gateway.go` (`fail` deja pasar `sql_*`), `gateway/sources.go` (descripción + esquemas en `SourceRef`), `api/connections.go` (create acepta `database` y `connection_string`; `GET /connections/{id}/database/schema`, `POST /connections/{id}/database/query`), `cmd/server/connections.go`; `agent-runtime/app/security.py` (`source_lines` explica `db.schema`/`db.query`), test `agent-runtime/tests/test_sources_prompt.py`; `gateway/database_test.go`.

- [ ] Gateway: la lectura sale como `<untrusted_data>`, cuenta en `connection_usage`, alias obligatorio si hay varias bases.
- [ ] API: la respuesta nunca trae contraseña ni `hint`; la consulta de prueba la audita con hash y largo del SQL, no el texto.
- [ ] Runtime: ejemplo `{"tool":"db","action":"query","args":{"connection_alias":"…","sql":"SELECT … LIMIT 50"}}`.

### Task 4: Pantalla (Conexiones, menú principal)

**Files:** Create `frontend/src/lib/connections/dbSource.ts` (+ `dbSource.test.mjs`), `frontend/src/components/connections/DatabaseForm.tsx`; modify `ConnectWizard.tsx`, `ConnectionDrawer.tsx`, `api.ts`, `types.ts`, `connections-mock.ts`, `i18n/{es,en,pt}.json`.

- [ ] Alta: campos o cadena de conexión (la cadena se trata como secreto: input no controlado), SSL `require` por defecto, esquemas opcionales; probar; asignar a todos o a elegidos.
- [ ] Cajón: resumen, "Ver tablas", consulta de prueba con tabla de resultados y aviso de tope.

### Task 5: Docs y verificación

- [ ] `docs/architecture/database-sources.md`, `08-api.md`, fila en `integrations-credentials.md` (tabla v1).
- [ ] `go build ./... && go vet ./...`, `go test` de los paquetes tocados con `TEST_DATABASE_URL`, `pytest -q`, `npx tsc --noEmit`, `npm run test:unit`, `node scripts/check-i18n.mjs`.
- [ ] Prueba manual de solo lectura contra la base del dueño (fuera del repo); capturas sin contraseñas en `docs/screenshots/db-solo-lectura-2026-11-03/`.
