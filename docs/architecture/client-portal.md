# Portal del cliente (B13)

Un **enlace de sólo lectura** con el avance de UN proyecto y la marca del despacho. El cliente lo abre sin cuenta.
Lo crean, editan y revocan sólo propietarios y administradores (`org:manage`); los agentes no tienen ninguna
herramienta para esto y el portal no cambia nada del proyecto.

Código: `backend/internal/portal` (servicio, tokens, vista pública por allowlist, store en memoria),
`backend/internal/infrastructure/postgres/portal.go`, `backend/internal/api/portal.go`, `backend/cmd/server/portal.go`,
migración `798_client_portal.sql` (+ `migrations/down/798_client_portal_down.sql`).
UI: `frontend/src/app/portal/page.tsx`, `frontend/src/components/portal/PortalPage.tsx`,
`frontend/src/components/projects/ClientPortalModal.tsx` ("Compartir con el cliente" en el detalle del proyecto),
`frontend/src/components/settings/PortalBranding.tsx` (Ajustes → Marca del portal del cliente),
`frontend/src/lib/portalLogic.ts`, `frontend/src/lib/portalApi.ts`, `frontend/src/lib/mock/portal-mock.ts`.

## Enlaces

- Token de 256 bits (`crypto/rand`, base64url, 43 caracteres). Sólo se guarda su SHA-256 (`portal_links.token_hash`);
  el texto se devuelve una vez, al crear (el mismo estilo que las invitaciones, `internal/auth/invitations.go`).
- Vencimiento opcional (1..365 días o nunca), revocación inmediata e idempotente, máx. 20 enlaces activos por proyecto.
- `label` es una nota interna ("Ferretería López") y **nunca** se muestra en la página. `title` es lo que lee el cliente
  como título del proyecto (vacío = el nombre del proyecto).
- Auditoría (`entity = portal_link`): `portal_link.created`, `portal_link.updated`, `portal_link.revoked`,
  `portal_link.first_viewed` (actor `portal`), `portal_branding.updated`, `portal_branding.logo_set`,
  `portal_branding.logo_removed`. Nunca se audita el token. Se guarda `first_viewed_at` y `last_viewed_at`
  (como mucho una escritura por minuto y enlace).
- Un token desconocido, vencido o revocado da siempre la misma respuesta (`404 link_unavailable`).

### El token nunca viaja en una URL que llegue al servidor

La página es `/portal#<token>`: el fragmento (`#...`) no se envía en la petición ni en el `Referer`. La página lo lee en
el navegador y lo manda en el **cuerpo** de `POST /api/v1/public/portal/view` (y `/logo`), así que no aparece en los logs
de acceso de Caddy, en el historial del proxy ni en otros sitios. No existe una ruta GET con el token.

## Qué muestra y qué no (allowlist)

`portal.View` (`backend/internal/portal/public.go`) es un DTO propio que se llena campo a campo; nunca incrusta un
struct interno.

| Muestra | Nunca muestra |
|---|---|
| Nombre, color de acento y logotipo del despacho | Costos, presupuesto, estimaciones, gasto |
| Título del proyecto y estado grueso (`planning`, `in_progress`, `on_hold`, `completed`, `closed`) | Agentes, sus instrucciones, pensamientos o salidas |
| Avance general (tareas terminadas / total) | Aprobaciones y compuertas (`gate`), esperas (`wait`) |
| Etapas (objetivos) con sus tareas: sólo título y estado (`pending`, `in_progress`, `done`) | El objetivo/instrucción del proyecto (`goal`) |
| Entregables **aprobados por una persona** (`approved`/`sent`) que el despacho eligió: título, tipo y fecha | Notas internas, `label`, conexiones, otros proyectos |
| Fecha de la última actualización, vencimiento del enlace | Cualquier id (proyecto, nodo, tarea, artefacto, organización, usuario) |

Reglas de estado: `awaiting_approval` se muestra como "en curso" (las aprobaciones no son asunto del cliente);
`failed`, `blocked` y `paused` como "pendiente"; un proyecto `failed` o `paused` como "en pausa". Grupos, compuertas,
esperas y nodos reemplazados o cancelados no aparecen. Los entregables marcados que aún no están aprobados no se muestran
hasta que una persona los apruebe (y aprobados quedan bloqueados, así que no cambian a espaldas del despacho).

Pruebas que fallan si algo se filtra: `backend/internal/portal/public_test.go` (recorre el tipo `View` por reflexión y
falla con cualquier clave JSON fuera de la allowlist o con un struct incrustado; arma un proyecto lleno de costos, agentes,
aprobaciones e ids "SECRET" y comprueba que ninguno sale), `backend/internal/api/portal_api_test.go` (por HTTP) y
`frontend/src/lib/portalLogic.test.mjs` (el espejo del modo demo).

**Límite conocido:** los títulos de las tareas se muestran tal como están en el plan. La plantilla genérica los arma con
el texto de la solicitud ("Entender y preparar: <solicitud>"), así que ese texto sí aparece. Por eso el diálogo insiste en
revisar la vista previa antes de enviar y permite cambiar el título del proyecto.

## Marca del despacho

`portal_branding` (una fila por organización): `display_name` (≤ 80; vacío = nombre de la organización),
`accent_color` (`#rrggbb`, validado en el servicio, en la base y otra vez en el navegador antes de llegar a un `style`) y
el logotipo. El logotipo se decodifica y re-codifica con `internal/imagenorm` (PNG/JPEG/GIF de hasta 2 MB → PNG/JPEG de
≤ 512 px y ≤ 256 KiB, sin metadatos); SVG y cualquier otra cosa se rechazan. Se sirve con `nosniff` y CSP `sandbox`.

## Seguridad de la ruta pública

- API (`/api/v1/public/portal/*`): límite de 60 peticiones por minuto y dirección (`auth.IPKey`, respeta
  `TRUSTED_PROXIES`; con Redis el límite es compartido), `Cache-Control: no-store`, `Referrer-Policy: no-referrer`,
  `X-Robots-Tag: noindex, nofollow, noarchive`, `X-Frame-Options: DENY`, `nosniff`, CSP `default-src 'none'; sandbox`.
- Página (`/portal`, `frontend/next.config.mjs`): los mismos `X-Robots-Tag`, `Referrer-Policy`, `X-Frame-Options`, y una
  CSP que sólo permite el propio origen (y el de la API si es absoluta), `img-src 'self' blob: data:`,
  `frame-ancestors 'none'`, `form-action 'none'`, `base-uri 'none'`. Next.js inyecta scripts en línea, así que
  `script-src` lleva `'unsafe-inline'` (no hay middleware de nonces). Además `<meta name="robots" content="noindex">` y
  `<meta name="referrer" content="no-referrer">`. El `Caddyfile` pone `Referrer-Policy: strict-origin-when-cross-origin`
  en todo el dominio; aun así el token va en el fragmento, que ningún navegador envía.
- Base de datos: RLS por organización en ambas tablas; para la búsqueda por token hay una política adicional de **sólo
  lectura** (`portal_lookup`) que muestra la única fila cuyo hash anunció la transacción (`app.portal_hash`), igual que
  `711_api_access.sql`. Todo lo demás (proyecto, entregables, marca) se lee después en una transacción normal de la
  organización. El rol de la app no puede borrar enlaces (se conserva el historial).

## Modo demo (`NEXT_PUBLIC_MOCK=true`)

Los enlaces y la marca se guardan en `localStorage`. Como la página pública se abre en otra pestaña (donde el mock de
proyectos arranca de cero), al crear un enlace (y cada vez que se listan) se guarda la vista ya filtrada por
`toPublicView`, el espejo de la allowlist del backend. `/portal#demo` muestra un proyecto de ejemplo con avance simulado.
En el demo no hay artefactos aprobados, así que las tareas terminadas hacen de entregables.

## No incluido / pendiente

- Descargar el contenido de los entregables desde el portal (hoy sólo título, tipo y fecha).
- Un enlace por cliente con varios proyectos (depende de B12 `customer_id`).
- Aviso por correo al cliente: el despacho copia el enlace y lo envía por su canal.
