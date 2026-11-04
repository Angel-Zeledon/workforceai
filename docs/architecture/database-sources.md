# Bases de datos de solo lectura (`database`)

Estado: implementado (2026-11-03). Plan: `docs/superpowers/plans/2026-11-03-bases-de-datos-solo-lectura.md`.

Pedido del dueño: *"Quiero poder conectarme a dbs para consultar info también; no deberías escribir nada, solo usar
para consulta."* Es una **conexión genérica** de la organización, como la fuente de API (`custom_api`,
`integrations-credentials.md` 18.8): no está atada a un proveedor, así que respeta la regla v1 (solo importaciones
genéricas) y `VENDOR_INTEGRATIONS` nunca la apaga.

Primero **PostgreSQL**, que cubre Supabase, Neon, Amazon RDS, Google Cloud SQL, Azure y cualquier servidor propio.
La configuración ya lleva `engine`, así que MySQL se puede añadir sin migración (hoy `engine: "mysql"` responde
`400 engine_not_supported`).

## Qué puede hacer cada quien

| Quién | Qué |
|---|---|
| Admin (`connections:manage`) | Conexiones (menú principal, `/connections`; `/settings?s=connections` redirige ahí) → Conectar → **Base de datos (solo lectura)**: host, puerto, base, usuario, SSL (por defecto `require`) y contraseña, **o** una cadena de conexión `postgresql://…`. Probar conexión. Ver tablas. Consulta de prueba a mano. Cambiar la configuración, rotar la contraseña, suspender, revocar |
| Admin (`connections:grant`) | Elegir qué agentes la usan: al final del alta ("Todos" o elegidos) o en la matriz de permisos. **Conectar no es conceder** (D1) |
| Agente con permiso | `db.schema` (tablas y columnas que el usuario puede leer, con comentarios) y `db.query` (una consulta de lectura con filas acotadas) |

## Cómo se garantiza que solo lee

Tres cerrojos independientes, todos nuestros: valen **aunque el usuario de la base tenga permisos de escritura**.

1. **Filtro de SQL** (`internal/connections/database/sqlcheck.go`). Un analizador léxico de Postgres (comentarios
   anidados, `'…'`, `E'…'`, `$tag$…$tag$`, identificadores entre comillas) acepta **una sola sentencia** que empiece
   por `SELECT`, `WITH`, `EXPLAIN`, `VALUES` o `TABLE` y rechaza, fuera de literales y comentarios:
   - palabras de escritura o de control: `INSERT UPDATE DELETE MERGE CREATE ALTER DROP TRUNCATE GRANT REVOKE COPY CALL
     DO SET RESET LOCK LISTEN NOTIFY UNLISTEN VACUUM ANALYZE INTO EXECUTE PREPARE DEALLOCATE DECLARE BEGIN COMMIT
     ROLLBACK SAVEPOINT` y `FOR SHARE` (`sql_not_read_only`). Así caen `WITH x AS (INSERT …)`, `EXPLAIN ANALYZE`,
     `SELECT … INTO` y `FOR UPDATE`. Una columna que se llame así se escribe entre comillas;
   - funciones peligrosas por nombre, en cualquier esquema (`sql_forbidden_function`): `pg_sleep*`, `dblink*`, `lo_*`,
     `loread`, `lowrite`, `pg_read_*`, `pg_ls_*`, `pg_stat_file`, `pg_file_*`, `set_config`, `nextval`, `setval`,
     `pg_terminate_backend`, `pg_cancel_backend`, `pg_reload_conf`, `pg_advisory*`, `pg_notify`, `query_to_xml*`,
     `table_to_xml*`, `cursor_to_xml*`, `schema_to_xml*`, `database_to_xml*` (ejecutan SQL dado como texto),
     `pg_logical_*`, `pg_create_*`, `pg_drop_*`, `pg_replication_*`, `pg_backup_*`, `pg_switch_wal`, `pg_promote`,
     `pg_stat_reset*`, `txid_current`;
   - un `;` que no sea el final (`sql_multiple_statements`), literales o comentarios sin cerrar (`sql_invalid`),
     más de 20 000 caracteres (`sql_too_long`).
2. **Protocolo extendido** (`QueryExecModeExec`): el propio servidor rechaza una segunda sentencia en la misma llamada.
3. **Transacción `READ ONLY`** en una conexión abierta solo para esa llamada: `BEGIN ISOLATION LEVEL REPEATABLE READ
   READ ONLY`, y en un solo viaje `set_config(…, true)` (equivale a `SET LOCAL`) de `statement_timeout` 15 s,
   `idle_in_transaction_session_timeout` 20 s, `lock_timeout` 3 s y `standard_conforming_strings = on` (para que el
   servidor lea los literales como el filtro). Antes de la consulta se lee `current_setting('transaction_read_only')`:
   si el servidor no dice `on`, no se ejecuta nada (`db_read_only_not_enforced`). Al terminar, **siempre `ROLLBACK`**
   y se cierra la conexión: nunca hay `COMMIT`. Una escritura que pasara el filtro (por ejemplo una función propia de
   la base que inserta) falla en la base con SQLSTATE `25006` (`sql_not_read_only`).

Topes: 500 filas, 1 MiB de valores y 2000 caracteres por celda. Al llegar al tope el lector cancela la consulta en el
servidor (no lee el resto) y marca `truncated` (`rows` o `bytes`). Los valores vuelven en la forma de texto del
servidor (lo que muestra `psql`); `NULL` es `null`.

Pruebas que lo demuestran (Postgres desechable, `TEST_DATABASE_URL`; el rol de prueba **es dueño** de sus tablas):

| Prueba | Qué demuestra |
|---|---|
| `database.TestWritesFailEvenWithWritePrivileges` | Con el filtro saltado a propósito, `INSERT`, `UPDATE`, `DELETE`, CTE con `INSERT`, una función `VOLATILE` que inserta, `nextval`, `CREATE TABLE`, `TRUNCATE` y `FOR UPDATE` fallan en la base; `transaction_read_only` no se puede apagar; el número de filas, la secuencia y las tablas no cambian; no queda ninguna sesión abierta. Si se quita el `READ ONLY` del código, esta prueba falla (verificado) |
| `database.TestCheckSQL` | Tabla de casos: lo aceptado (literales con `;`, `$$…$$`, comentarios anidados, `FETCH FIRST`, columnas `"update"`) y lo rechazado con su código |
| `database.TestRowAndByteCaps` | 25 millones de filas en streaming: corta en 500 en menos de 6 s; el tope de bytes también |
| `database.TestStatementTimeout` | Una función que duerme 5 s (el filtro no puede saberlo) se corta en el `statement_timeout` |
| `database.TestSchemaAllowlistOnQueries`, `TestSchemaListsTablesColumnsAndComments` | Esquemas permitidos, comentarios de tablas y columnas |
| `database.TestSQLErrorIsDataAndNeverCarriesThePassword` | Un error de SQL vuelve como dato corregible; la contraseña no aparece ni en mensajes ni en valores; contraseña incorrecta = `reauth_required` |
| `database.TestPrivateAddressesAreRefusedByDefault` | `127.0.0.1` y `localhost` se rechazan sin `DATABASE_ALLOW_PRIVATE` |
| `gateway.TestDatabase*` | Concesiones, alias obligatorio con varias bases, uso contado, bloques `<untrusted_data>`, contraseña fuera de bloques, auditoría y eventos (de punta a punta con la bóveda) |
| `api.TestDatabaseConnectionFromConnectionString` | La cadena se separa en el servidor, la contraseña no vuelve en ninguna respuesta (ni `hint`), una escritura se rechaza antes de marcar |

## Credenciales y red

- La **contraseña** va al vault como cualquier credencial (`credentials`, AES-256-GCM con la DEK de la organización
  envuelta por `CONNECTIONS_KEK`). La API nunca la devuelve, ni siquiera el `hint` de 4 caracteres. Se presta al
  adaptador solo mientras conecta (`Handle.Secret`, que usa `Vault.Use`) y se registra como sospechosa
  (`sanitize.Suspects`) para que ningún texto la lleve; además el adaptador la borra de los mensajes del servidor.
- Una **cadena de conexión** pegada se trata como secreto: el servidor la separa (`connections.ParseConnString`),
  guarda host, puerto, base, usuario y `sslmode` en `connections.database` y la contraseña en el vault. La cadena
  entera no se guarda.
- El texto del driver (que nombra host, usuario y base) nunca sale del adaptador: solo códigos estables
  (`db_unreachable`, `db_host_not_found`, `db_tls_failed`, `db_not_found`, `db_address_not_allowed`,
  `reauth_required`, `rate_limit`).
- **Red**: igual que `custom_api`, el host lo escribe un admin, así que por defecto no puede apuntar a la red del
  propio servidor. El resolvedor exige que todas las direcciones sean públicas y el dialer vuelve a comprobar la
  dirección final (sin ventana de DNS rebinding). `DATABASE_ALLOW_PRIVATE=true` (self-hosted en una red de
  confianza) lo permite; nunca relaja las reglas de solo lectura.
- `sslmode`: `require` (por defecto: cifrado, sin verificar el certificado, como libpq), `verify-ca`, `verify-full`
  (con las raíces del sistema; un proveedor con autoridad propia fallará) o `disable` (solo redes de confianza).

## Esquemas permitidos (opcional)

`database.schemas` limita lo que **ven** los agentes: `db.schema` lista solo esos esquemas, el `search_path` de la
transacción queda en esos esquemas y se rechaza un nombre calificado con un esquema de la base que no esté en la lista
(`sql_schema_not_allowed`). Es un límite de visibilidad nuestro, no una frontera de seguridad (por ejemplo, las vistas
del catálogo de Postgres siguen accesibles sin calificar). **El límite duro son los permisos del usuario de la base**:
la pantalla recomienda un usuario de solo lectura con acceso solo a lo necesario.

## Herramientas del agente

Manifiesto `internal/connections/providers/database.json`: capacidad `db.read` (lectura, riesgo bajo, sin efectos),
herramientas `db.schema` y `db.query`, `never_offered: ["db.write"]`. Pasan por el Tool Gateway como todas: controles
(kill switch, pausa, solo lectura de la organización), concesión por agente, límites por conexión (por defecto 20/min,
300/h, 1500/día, 10 partes por llamada) y `connection_usage` + `audit_logs` con metadatos (para una consulta: un hash
corto del SQL y el número de filas, nunca el texto ni los valores).

```json
{"tool": "db", "action": "schema", "args": {"connection_alias": "ventas", "schema": "public", "table": "pedidos"}}
{"tool": "db", "action": "query",  "args": {"connection_alias": "ventas", "sql": "SELECT estado, count(*) FROM pedidos GROUP BY 1 LIMIT 50"}}
```

El runtime lo explica en la lista de fuentes (`agent-runtime/app/security.py`, `source_lines`): pedir `db.schema` si
no conoce las columnas, una sola sentencia con `LIMIT`, nada que escriba. Con varias bases concedidas,
`connection_alias` es obligatorio (`source_alias_required`).

Todo lo que viene de la base (filas, nombres de columnas, comentarios y mensajes de error) es **contenido externo no
confiable**: el gateway lo sanea, lo redacta con el perfil del grant y lo envuelve en `<untrusted_data
trust="external_untrusted">` como el resto de lecturas; la tarea queda contaminada (taint) y cualquier escritura
posterior necesita una persona. Las filas van en partes de hasta 3800 caracteres (`columnas: …` y una fila JSON por
línea) con metadatos de confianza (`rows_returned`, `rows_shown`, `truncated`). Un error que el agente puede corregir
(sintaxis, tabla o columna inexistente, valor inválido, permiso denegado) vuelve como dato (`la consulta fallo
(42P01): …`) para que corrija en la ronda siguiente; un rechazo de solo lectura o un timeout es un fallo con código.

## Pantalla

Conexiones, en el menú principal (`/connections`; `/settings?s=connections` redirige ahí). Componentes en `frontend/src/components/connections/`:

- **Alta** (`ConnectWizard` + `DatabaseForm.tsx`): motor (PostgreSQL; MySQL "próximamente"), "Por campos" o
  "Cadena de conexión", SSL, "¿Qué hay en esta base?", esquemas opcionales; contraseña o cadena en un campo no
  controlado que se vacía antes de enviar; "Probar con datos de ejemplo" (simulado, sin contraseña); prueba de conexión
  con el motivo exacto si falla; asignación a "Todos" o a elegidos.
- **Cajón de la conexión**: resumen (`usuario@host:puerto/base`, SSL, esquemas), editar la configuración, "Ver tablas"
  (columnas, tipos, comentarios, filas estimadas) y "Consulta de prueba" con tabla de resultados, tiempo, aviso de
  tope y el error del servidor si la consulta falla. La contraseña se rota en "Credencial".

## Datos

Migración `816_connection_database.sql`: columna `connections.database JSONB` (sin secretos; `NULL` para otros
proveedores; RLS de `240_connections.sql`). Bajada: `backend/migrations/down/816_connection_database_down.sql`. No hay
tablas nuevas: `internal/tenantdata` exporta la columna con la conexión (la contraseña está en `credentials`, que ya se
redacta).

## Probado con una base real (Supabase, 2026-11-03)

- El host directo `db.<ref>.supabase.co` solo publica direcciones IPv6; desde una máquina sin IPv6 no resuelve a nada
  alcanzable (`db_host_not_found`).
- Funciona el **pooler de Supabase en modo sesión**: host `aws-<n>-<región>.pooler.supabase.com`, puerto `5432`,
  usuario `postgres.<ref>`, `sslmode=require`. El servidor confirmó `transaction_read_only = on` a través del pooler.
  La región sale del panel de Supabase (Connect → Session pooler); una región equivocada responde un error genérico
  (`provider_error`).

## Pendiente

- MySQL (mismo contrato; el filtro léxico y la transacción `START TRANSACTION READ ONLY` cambian).
- Límite por tabla (hoy por esquema) y una lista de tablas visibles por agente.
- Un pool de conexiones por fuente si la latencia de conectar en cada llamada molesta (hoy se abre y se cierra por
  llamada, a propósito: la contraseña no vive en memoria más que lo necesario).
