# F7. Comentarios anclados a un párrafo — plan breve

**Meta:** una persona selecciona un pasaje de un documento de texto en el visor, lo comenta, y desde ese comentario pide el cambio al agente. El agente entrega una versión nueva y el comentario queda resuelto con enlace a esa versión.

**Decisión de alcance:** los comentarios de artefactos (migración 340) cuelgan de `artifacts`; el visor de documentos trabaja sobre archivos del Drive (`fs.edit`, "Pedir un cambio"). Por eso los comentarios anclados son de **archivos del Drive** (tabla nueva `file_comments`, migración 828). Se empieza por Markdown y texto plano; el backend admite cualquier archivo cuyo texto se extraiga (docx incluido), la interfaz de selección solo Markdown/texto.

## Tareas

1. `internal/filecomments`: ancla (cita exacta + contexto antes/después + versión + offsets), `Locate` (exacto, luego plegado de espacios/marcas de Markdown, desempate por contexto y cercanía), estados `intact | moved | lost`. Pruebas puras primero.
2. Servicio + almacén en memoria: añadir, listar (localizando contra la versión mostrada), resolver, pedir el cambio, resolver al entregar (`OnVersion`).
3. Gancho `drive.Service.SetVersionHook`: se llama tras cada versión nueva (agente, persona, edición).
4. Migración 828 + almacén Postgres (RLS por `org_id`) + rollback.
5. API `/drive/files/{id}/comments` (GET, POST), `PATCH .../{cid}` (resolver), `POST .../{cid}/request-change`. Sellado: `driveNeedAt` (404 si no ve la carpeta), el agente debe ser usable (`checkAgentUse`); la petición viaja por el chat (`PostChatKeyed`), o sea mismos permisos, aprobaciones, presupuesto y auditoría que "Pedir un cambio".
6. El texto del comentario y el pasaje se pasan como datos delimitados (`sanitize.Wrap`), nunca como instrucción.
7. Cableado en `cmd/server`.
8. Frontend: `lib/anchoredComments.ts` (puro, con pruebas), `CommentsPanel` + selección → "Comentar" + resaltado con CSS Custom Highlight API, teclado, i18n es/en/pt.
9. Docs: `docs/architecture/anchored-comments.md`, sección 52 de `08-api.md`.
