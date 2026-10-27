# Importar desde un enlace

Una importación **genérica de tipo PULL**: un administrador pega cualquier URL que devuelva datos (una hoja de cálculo
publicada como CSV, un endpoint de exportación JSON/CSV de cualquier sistema, la URL de un archivo XLSX o PDF) y el
backend la descarga a Archivos (drive) como un archivo versionado, una vez o con frecuencia (cada hora, diaria,
semanal). No hay integración con ningún proveedor: el enlace es todo el contrato. Complementa las fuentes de API
genéricas (`custom_api`, que los agentes consultan en vivo), la subida manual de archivos y los buzones de datos
(PUSH, `internal/inboxes`).

Código: `backend/internal/linkimports` (servicio, descargador con guarda SSRF, programador, store en memoria),
`backend/internal/infrastructure/postgres/linkimports.go`, `backend/internal/api/linkimports.go`,
`backend/cmd/server/linkimports.go`, migración `804_link_imports.sql` (+ `migrations/down/804_link_imports_down.sql`).
UI: `frontend/src/components/connections/LinkImportsSection.tsx` (pantalla Conexiones, pestaña Conexiones, debajo de
las tarjetas), `frontend/src/lib/linkImportsApi.ts`, `frontend/src/lib/mock/linkimports-mock.ts` (modo demo).

## Reglas que no cambian

- **Sólo humanos con `org:manage`** crean, editan, ejecutan y eliminan importaciones. Los miembros (`connections:read`)
  ven que existen, su estado y su historial, con la *query string* del enlace oculta (los enlaces publicados suelen
  llevar un token ahí). Los agentes no tienen ninguna herramienta para esto.
- **La descarga pasa por la misma guarda de red** que la herramienta web de los agentes y las fuentes `custom_api`
  (`internal/webtool` `NetGuard`): sólo `https` (salvo `LINK_IMPORTS_ALLOW_HTTP=true`), puertos 80/443/8080/8443, sin
  usuario/contraseña en la URL, nombres internos rechazados (`localhost`, `*.local`, `*.internal`, un solo label…),
  **todas** las direcciones resueltas deben ser públicas (una sola privada rechaza el host), el dial va a la IP ya
  validada (sin segunda resolución que permita *DNS rebinding*) y el `Control` del dialer revisa otra vez la dirección
  final. Cada redirección (máx. 5) se valida de nuevo; `https → http` se rechaza. Sin proxy del entorno.
  `CUSTOM_API_ALLOW_PRIVATE=true` (el mismo interruptor de las fuentes de API, sólo para autoalojado en red de
  confianza) permite la LAN, pero no cambia el esquema, el tope ni los tipos.
- **Límites**: 60 s por descarga (todas las redirecciones), tamaño máximo `DRIVE_MAX_FILE_MB` (se corta al leer, también
  si el servidor no declara `Content-Length` o comprime), tipos permitidos `csv, tsv, json, xlsx, txt, pdf`. El tipo se
  decide por el `Content-Type` y, si es genérico (`application/octet-stream`), por el nombre sugerido
  (`Content-Disposition`), la ruta del enlace o los bytes mágicos; luego Archivos comprueba que el contenido coincide
  (`CheckContent`). Una respuesta `text/html` se rechaza como `web_page_not_data` (suele ser una página de inicio de
  sesión: el enlace no está publicado). **`.xls` (binario antiguo) no se importa**: Archivos no lo almacena (puede
  llevar macros y no se indexa); hay que exportarlo como XLSX o CSV.
- **Encabezado de autenticación opcional** (un nombre + un valor): el valor se sella con la bóveda
  (`vault.Seal`, clave de datos de la organización envuelta por `CONNECTIONS_KEK`, propósito
  `link_import_header:<id>`), **nunca se devuelve** (la API sólo dice `header_set` y `header_hint`, los últimos 4
  caracteres) y sólo se envía al **mismo origen** del enlace: una redirección a otro host no lo recibe. Sin KEK no se
  puede guardar un encabezado (`400`). No se aceptan nombres que controla el transporte (`Host`, `Content-Length`,
  `Transfer-Encoding`, `Proxy-*`, `Sec-*`…) ni valores con caracteres de control.
- **Versión nueva sólo si el contenido cambió**: se calcula el SHA-256; si coincide con el de la última importación y
  ese archivo aún lo contiene, la corrida queda `unchanged` sin escribir nada (y Archivos tampoco crea versión si el
  contenido es igual).
- **Autoría**: los archivos quedan atribuidos al principal `link_import` (`drive.LinkImportP(id)`), nunca a una
  persona. Como cualquier archivo que el agente no escribió, `fs.read` lo marca como escrito por otros (*tainted*), así
  que cualquier acción externa que el agente proponga después pide aprobación humana. El contenido es **DATO**, jamás
  instrucciones. Nunca se sobrescribe un archivo que escribió o tocó una persona o un agente: se usa el nombre con el
  sufijo de la importación (`ventas-1a2b3c.csv`).
- **Modo sólo lectura e interruptor de emergencia**: la corrida se omite (`skipped`, código `read_only_mode` o
  `kill_switch_active`) y queda registrada; un estado de controles ilegible se trata como sólo lectura (falla cerrado).
  La corrida programada consume su periodo, así que no se registra una omisión por minuto.
- **Auditoría** de cada alta, edición, eliminación y corrida, sólo con metadatos: host (nunca la URL completa ni la
  query), tamaño, sha256, id del archivo, versión, código, estado HTTP, duración. Nunca el contenido ni el encabezado.

## Destino y clientes (B12)

Sin cliente, la carpeta es absoluta bajo `/Shared` (por defecto `/Shared/Imports/Links/<slug>`) o una carpeta de
proyecto existente `/Projects/<proyecto>/…`. Con `customer_id`, la carpeta es **relativa** a la carpeta del cliente
(`/Clients/<nombre>`, por defecto `Imports/Links/<slug>`) y se resuelve en cada corrida: renombrar la carpeta del
cliente no rompe la importación ni saca datos de ella; una ruta que intente salir (`../`) se rechaza. Si el cliente
se archiva, las corridas fallan con `customer_unavailable`. El destino y el cliente no cambian después de crear la
importación (otro destino = otra importación). El nombre de archivo es `file_stem` (o el slug) + la extensión del tipo
descargado.

## Programación

Patrón de los rituales (`cmd/server/rituals.go`): un *ticker* en proceso cada minuto recorre las organizaciones
(`OrgLister`) y, organización por organización (RLS), toma las importaciones activas y programadas con
`next_run_at <= ahora`. Antes de correr, **reclama** el periodo con un *compare-and-set* sobre `next_run_at`
(`UPDATE … WHERE next_run_at = <esperado>`), así dos réplicas nunca corren el mismo periodo; el siguiente es
`ahora + intervalo` (1 h, 24 h, 7 días). Una importación nueva programada vence de inmediato (primera corrida en el
próximo minuto). `once` sólo corre al pulsar «Importar ahora» (la UI lo hace al crearla). Pausar (`enabled: false`)
detiene el programador; «Importar ahora» sigue disponible. Una sola corrida a la vez por importación (`409`).

## Corridas

`status`: `ok` (archivo o versión nueva), `unchanged`, `error` (con `code`) o `skipped` (con `code`). Códigos estables:
`read_only_mode`, `kill_switch_active`, `customer_unavailable`, `header_unavailable`, `http_status` (+ `http_status`
numérico), `too_large`, `content_type_not_allowed`, `web_page_not_data`, `empty_response`, `insecure_redirect`,
`too_many_redirects`, `redirect_invalid`, `timeout`, `fetch_failed`, `content_mismatch`, `quota_exceeded`,
`drive_error` y los de la guarda (`url_invalid`, `url_scheme_not_allowed`, `url_credentials_not_allowed`,
`url_port_not_allowed`, `host_not_allowed`, `address_not_allowed`, `dns_failed`). Se guardan las últimas 200 corridas
por importación.

Evento en tiempo real cuando llegan datos nuevos (sólo `ok`): `link_import.data_landed`
`{import_id, import_name, run_id, customer_id, trigger, fetched_at, file{file_id, path, name, size, version}}`
(`docs/architecture/03-eventos-realtime.md`). La UI recarga la lista.

## Datos (migración 804)

`link_imports` (configuración, encabezado sellado en `header_sealed BYTEA`, resumen de la última corrida,
`next_run_at`) y `link_import_runs` (metadatos de cada corrida). RLS `tenant_isolation` por `org_id` como 803; el rol
`app_user` tiene `DELETE` (eliminar una importación borra su historial en cascada; los archivos se quedan en
Archivos). La bajada (`804_link_imports_down.sql`) borra ambas tablas: los encabezados sellados no se recuperan.

## Variables de entorno

| Variable | Por defecto | Efecto |
|---|---|---|
| `CUSTOM_API_ALLOW_PRIVATE` | `false` | compartida con `custom_api`: permite direcciones privadas/LAN (sólo autoalojado) |
| `LINK_IMPORTS_ALLOW_HTTP` | `false` | acepta enlaces `http://` (el enlace y su encabezado viajan sin cifrar) |
| `DRIVE_MAX_FILE_MB` | 25 | tope de tamaño de cada descarga |
| `CONNECTIONS_KEK` | — | sin ella no se pueden guardar encabezados |

## Pruebas y lo no verificado

`go test ./internal/linkimports/` (SSRF: IP privada, nombre que resuelve a privada, redirección a privada por el
transporte real, redirección a `http`/`file:`; tipos y tamaño; encabezado sólo al mismo origen; versión sólo si cambia;
encabezado sellado nunca devuelto; sólo lectura/interruptor omiten y registran; aislamiento entre organizaciones;
alcance de cliente; no sobrescribir archivos de personas), `go test -run TestLinkImportsOverHTTP ./internal/api/`
(roles, query oculta, aislamiento) y `internal/infrastructure/postgres/linkimports_pg_test.go` (RLS y *claim*; requiere
`TEST_DATABASE_URL`). **No verificado** en este cambio: la prueba de Postgres contra una base real, una descarga real
por Internet (todas las pruebas usan transportes falsos o un servidor local) y la UI en un navegador.
