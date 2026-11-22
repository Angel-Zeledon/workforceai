# Comentarios anclados a un párrafo (F7)

Una persona abre un documento de texto en el visor, **selecciona un pasaje**, lo comenta y, desde ese comentario, **le pide el cambio a un agente**. El agente edita con `fs.edit` (el camino de [agent-edit.md](agent-edit.md)); cuando entrega la versión nueva, el comentario se marca resuelto con un enlace a esa versión. Para una PYME, "cambia este párrafo" es más natural que reescribir toda la instrucción.

Inspiración: los comentarios anclados a pasajes con historial de revisiones de Paperclip.

## Qué se comenta

Los comentarios de artefactos (migración 340, [agent-workspaces.md](agent-workspaces.md)) cuelgan de `artifacts`. El visor de documentos y el "Pedir un cambio" trabajan sobre **archivos del Drive**, así que los comentarios anclados son de archivos del Drive: tabla `file_comments` (migración **828**), paquete `backend/internal/filecomments`.

La interfaz de selección cubre **Markdown y texto plano** (los tipos que el visor pinta como texto). El backend admite cualquier archivo cuyo texto se extraiga (docx incluido, por `drive.ReadText`); falta solo la selección en el visor de Word.

## El ancla

```
anchor = { quote, prefix, suffix, version, start, end }
```

- `quote`: la cita exacta (hasta 2000 caracteres), tal como la seleccionó la persona.
- `prefix` / `suffix`: hasta 48 caracteres antes y después, **calculados por el servidor** sobre el texto, no por el cliente.
- `version`: la versión del archivo sobre la que se comentó. `start` / `end`: desplazamientos (runas) en el texto de esa versión.

Al crear, el servidor busca la cita en el texto de la versión; si no está, `400 anchor_not_in_text`. La búsqueda **pliega** el texto: junta espacios y saltos de línea y descarta las marcas de Markdown que la página renderizada no muestra (`*`, `_`, `` ` ``, `#`, `>`, viñetas, la sintaxis de enlaces), de modo que lo seleccionado en la página y el texto crudo del archivo se encuentran.

### Re-localizar tras un cambio

`Locate(texto, ancla)` busca la cita en el texto actual (o en la versión que se muestra, `?version=`):

| estado | significado |
|---|---|
| `intact` | la cita está donde se hizo |
| `moved` | está en otro sitio (se añadió o quitó texto alrededor) |
| `lost` | la cita ya no existe: el pasaje se reescribió |

Si la cita aparece varias veces gana la coincidencia cuyo contexto (prefijo y sufijo) se parece más y, en empate, la más cercana al desplazamiento original. Un comentario **perdido no se descarta**: conserva cita y texto, y la interfaz dice **"El texto cambió"**. Visto contra la versión original vuelve a estar `intact`.

## Pedir el cambio a un agente

`POST /drive/files/{id}/comments/{cid}/request-change {agent_id, lang}`:

1. La persona debe **poder editar** la carpeta del archivo (leer no basta: `403 read_only_folder`) y **poder usar** al agente (misma regla que el chat, D3/E2).
2. El comentario debe tener ancla, no estar resuelto, no haberse pedido ya y **el pasaje debe seguir existiendo** (`409 anchor_lost` si no: no se manda un pedido sobre texto que ya no está).
3. Se envía un **mensaje de chat normal** al agente (`Orchestrator.PostChatKeyed`) con el archivo adjunto. No hay transporte nuevo, así que valen los mismos permisos, aprobaciones, presupuesto y auditoría que "Pedir un cambio": el agente lee y llama a `fs.edit` con `expected_version`; fuera de su escritorio o de carpetas con permiso de edición, `fs.edit` genera una tarjeta de aprobación como siempre. La clave de idempotencia es el id del comentario (un doble clic no manda dos pedidos).
4. El comentario guarda `change = {agent_id, base_version, requested_by, requested_at, turn_id}`.

### El texto es dato, no instrucción

El texto del mensaje (es, en o pt según `lang`) lleva la instrucción fija ("cambia solo ese pasaje, usa `fs.edit`, `expected_version` N") y **dos bloques delimitados** con `sanitize.Wrap` (`<untrusted_data>`): el pasaje exacto del archivo y el hilo del comentario (cuerpo de la persona y respuestas). Ambos pasan por `SanitizeChatText` (sin caracteres de control ni invisibles) y `sanitize.Neutralize` (no pueden cerrar el delimitador ni colar imágenes o enlaces). El propio texto del mensaje introduce cada bloque diciendo que es un dato y no instrucciones del sistema. Quien escribió el comentario puede ser otra persona: nunca se interpreta como orden.

## Resolver al entregar

`drive.Service` llama a un gancho (`SetVersionHook`) tras guardar **cada versión nueva**. `filecomments.Service.OnVersion` resuelve los comentarios del archivo que cumplan todo:

- tienen un `change` pendiente y no están resueltos;
- la versión la escribió **el mismo agente al que se le pidió** (`author.kind = agent`, mismo id);
- la versión es posterior a `change.base_version`.

Queda `resolved = true`, `resolved_by = "agent:<id>"`, `resolved_version = N`. La interfaz muestra "Resuelto por {agente}" y el enlace **Ver la versión N**. Lo que no resuelve nada: guardar una persona, guardar otro agente, o un agente que no cambia nada (el drive no crea versión si el contenido es idéntico). Una persona puede **reabrir** el comentario (se borran `resolved_by` y `resolved_version`; `change` se conserva y no se puede volver a pedir el mismo cambio).

Decisión: resolver por "el agente pedido entregó una versión posterior", no por comprobar que el pasaje cambió. Es simple, predecible y reversible (reabrir); el enlace a la versión deja a la persona ver qué hizo.

## Permisos y sellado

| acción | permiso | nivel en la carpeta |
|---|---|---|
| leer comentarios | `files:read` | leer |
| comentar, responder, resolver | `artifacts:comment` (miembro o más; el rol lector no) | leer |
| pedir el cambio | `artifacts:comment` + `conversations:post` | **editar** |

Todas las rutas empiezan con la comprobación del drive (`driveFileFor`): un archivo de un departamento privado responde `404` a quien no es miembro, **también a un administrador**, igual que un archivo inexistente (no se revela que existe). Otra organización recibe `404` y además la tabla tiene RLS por `org_id` y una clave foránea `(org_id, file_id)` hacia `drive_files`. La aplicación no borra comentarios (`REVOKE DELETE`); se resuelven.

## Visor

`frontend/src/components/files/AnchoredComments.tsx` y el cableado en `FileViewerPanel.tsx`:

- Seleccionar texto en un `.md`/`.txt` muestra una barra **"Comentar"** sobre el documento (no empuja el texto). El botón "Comentarios (n)" de la barra de herramientas abre el panel lateral (debajo en móvil).
- Los pasajes de hilos abiertos se resaltan con la **CSS Custom Highlight API** (`::highlight(fc-comments)`, el activo más fuerte): no se toca el DOM del texto. El pasaje se vuelve a encontrar en la página por cita, prefijo y sufijo ignorando espacios.
- El panel muestra hilos (cita, comentario, respuestas), el aviso "El texto cambió", **Resolver / Reabrir**, **Responder** y **Pedir el cambio a {agente}** (agente elegible; por defecto el que editó el archivo por última vez). Mientras espera: "Pedido a {agente} sobre la versión N". Al entregar el agente, el visor recarga (el archivo cambió) y el hilo aparece resuelto con el enlace a la versión.
- Teclado: todo son botones y campos nativos con etiqueta; Ctrl+Enter envía; los avisos usan `role="status"`/`alert`. Quien no pueda seleccionar con el ratón tiene "Escribir el pasaje a mano" (pega el texto exacto; el servidor lo comprueba) y los comentarios sobre todo el archivo.

## Pruebas

- `filecomments/anchor_test.go`: ancla con cita, contexto y offsets; re-anclado tras editar encima; desempate por contexto; ancla perdida; Markdown renderizado frente a fuente.
- `filecomments/service_test.go`: ancla al comentar; re-anclado y perdida; petición con el pasaje exacto como dato (y un comentario que intenta cerrar el delimitador); sin ancla o con ancla perdida no se envía nada; resuelto solo cuando entrega el agente pedido (no una persona, no otro agente, no una edición sin cambios); aislamiento entre organizaciones.
- `api/drive_comments_api_test.go`: recorrido completo por HTTP (comentar, pedir el cambio, el mensaje llega al chat del agente con el archivo adjunto, entrega y resuelto con versión); sellado de un departamento privado en las cuatro rutas; permisos (lector, lectura sin edición); otra organización; ancla perdida.
- `infrastructure/postgres/file_comments_pg_test.go`: ida y vuelta, RLS, sin `DELETE`, clave foránea entre organizaciones (necesita `TEST_DATABASE_URL`).
- Frontend: `anchoredComments.test.mjs` (ayudantes puros).

## Pendiente

- Selección en el visor de Word (docx) y en otros tipos renderizados; hoy solo Markdown y texto plano.
- Seleccionar con el teclado sin ratón depende del modo de cursor del navegador; está el camino de escribir el pasaje.
- Menciones a personas y avisos de comentario nuevo.
- Los comentarios de artefactos (migración 340) siguen su propio camino; unificar los dos modelos queda para cuando haya un visor común.
- Tabla de nombres de personas: el panel muestra "Persona" para los autores humanos (el directorio de personas no está en el cliente del visor).
