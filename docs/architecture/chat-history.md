# Historial de chats (E1)

El usuario puede volver a sus conversaciones con la oficina («General») y con cada agente, y las dos vistas **van de la mano**.

## Modelo: una sola copia de cada mensaje

No hay mensajes duplicados. `messages` sigue siendo la única fuente; General y el hilo de un agente son *vistas* (consultas):

- **General** (en pantalla: «Toda la oficina») = todos los mensajes del canal `office` y de cada chat 1:1 (`agent:<id>`), en orden cronológico. Las conversaciones internas de los pedidos (`request`) no entran: se leen en el panel del pedido (ver «Pedidos» abajo).
- **Hilo de un agente X** = su chat 1:1 (`agent:X`) + todo mensaje que X envió o recibió + los mensajes del usuario y avisos del sistema de los *turnos* (`turn_id`) en los que X participó. Así un mensaje del usuario en General que se enrutó a X aparece en el hilo de X (mismo id), y lo que se escribe en el chat 1:1 de X aparece en General.
- Cada mensaje devuelto trae `agent_ids`: los agentes que participaron en su turno (las insignias «Enviado a»).

Definición de referencia: `application.InThread` (Go), `chatsLogic.ts` (frontend y mock); el SQL de `postgres/chat_history.go` la replica. Una sola suite (`application/chathistorytest`) la ejecutan el almacén en memoria y Postgres.

## Datos (migración 570)

- `conversations.archived_at`, `conversations.title_custom` (renombrar / archivar).
- `chat_reads(org_id, key, read_at)`: marcadores de lectura `thread:<general|agentId>` y `conv:<id>`. Eran por organización; desde la migración 807 son por persona (`user_id`, ver E0). El historial existente se siembra como leído. RLS por `org_id`.
- Índices `messages(org_id, from_id, ts)` y `(org_id, to_id, ts)`.
- Título automático: las conversaciones de solicitud ya nacen con el texto de la petición; si el título está vacío se deriva del primer mensaje del usuario.

## Decisiones y límites

- Búsqueda con `ILIKE` (título y texto), sin FTS; suficiente para el volumen actual.
- Borrar `office` o `agent:<id>` limpia ese historial en **ambas** vistas (es el mismo dato); exige permiso de administrador y queda auditado.
- ~~La no-lectura es por organización~~: desde E0 es **por persona** (ver abajo).
- Auditoría: solo metadatos (`conversation.updated`, `conversation.deleted` con número de mensajes), nunca texto.

## E0: cada persona tiene su hilo con cada agente (migración 807)

Fase E0 de `equipos-oficinas.md` ("saber quién es quién").

**Autor.** Cada mensaje de una persona guarda `author_id` (su id de usuario) y `author_name` (su nombre visible en ese
momento: el nombre de la cuenta, o la parte del correo antes de la `@`). Viajan en la API (`GET …/messages`,
`/chats/threads/…`) y en el evento `chat.message`. El agente recibe el nombre de quien le escribe (`speaker_name`
en `/v1/chat-reply`) y el historial lleva el nombre de cada persona (`history[].name`); ambos son **datos**, nunca
instrucciones (van dentro del bloque no confiable del prompt).

**Hilo privado por persona.** La API y el WebSocket siguen hablando de `agent:<id>`, pero cada persona escribe y lee
**su propio** hilo con ese agente. Por dentro se guarda como `agent:<agente>@<usuario>` (`domain.ChatPrivateConv`);
ese id nunca sale del backend (`domain.PublicChatConv` lo traduce) y, si alguien lo manda a mano, solo lo acepta su
dueño (para cualquier otra persona es `404`). `messages.owner_id` y `conversations.owner_id` guardan el dueño (`''` =
compartido); toda lectura filtra `owner_id IN ('', <quien lee>)` (`application.VisibleTo`, mismo criterio en la memoria
y en Postgres, probado por la misma suite `chathistorytest`).

- **`office` sigue siendo común**: todos la ven y cada mensaje muestra quién lo escribió.
- **Nadie más lee el hilo privado**: ni otra persona ni un admin/owner (igual que los mensajes directos de
  `peoplechat`). Tampoco aparece en General, en el hilo del agente, en la lista de conversaciones, en la búsqueda, en
  los contadores de no leídos ni por WebSocket: los eventos `chat.message`, `chat.typing` y `route.decided` de un hilo
  privado salen con `audience = [dueño]` (el hub solo los entrega a sus sockets), **no se guardan** en la tabla de
  eventos de la organización y no crean actividad (`activity`).
- **Lo que hereda cada hilo**: la cola de turnos, el traspaso pendiente (memoria `chat/handoff:<id guardado>`), el
  contexto de `/limpiar` y `/compactar`, el título, el archivado y los marcadores de lectura son por id guardado, así
  que son de cada persona. `/memoria` ya no lista las entradas internas de alcance `chat`.
- **Borrar** `agent:<id>` (permiso de admin, como antes) borra el hilo propio de quien borra y el historial anterior
  compartido; **nunca** el hilo privado de otra persona.

**Historial anterior (decisión).** Lo escrito antes de E0 en `agent:<id>` no tiene autor ni dueño: no se borra ni se
asigna a nadie. Queda como **historial anterior compartido, de solo lectura**: lo ven todas las personas (como lo veían
antes), dentro del chat con ese agente, marcado con `shared_legacy: true` y separado por un divisor ("Historial anterior
compartido… / Desde aquí, tu conversación privada"). No se puede renombrar ni archivar (`404`) y **no se le pasa al
modelo** como historial del hilo nuevo: el agente empieza el hilo privado sin lo que otras personas le dijeron antes.
Motivo: asignarlo a alguien sería adivinar (y podría poner lo que escribió una persona en el hilo de otra); ocultarlo
haría desaparecer datos que la gente ya veía. Los mensajes de persona sin autor no cuentan como no leídos. Sin
autenticación (demo) no hay identidad: todo sigue en el hilo compartido `agent:<id>`, como antes.

**No leído por persona.** `chat_reads.user_id` (clave primaria `(org_id, user_id, key)`). El marcador de una persona es
el más reciente entre el suyo y el de la organización (`user_id = ''`, los escritos antes de E0, que quedan como base).
Lo que una persona escribió nunca cuenta como no leído para ella; lo que escribió otra en la oficina, sí.

**Adjuntos.** Máximo **10** archivos por mensaje, igual en el compositor y en el backend; con más, `POST /messages`
responde `400` ("too many attachments … max 10") y el compositor lo muestra con un error claro (antes el backend
recortaba a 6 sin avisar).

**Lo que no cambia en E0.** Una tarea pedida en un hilo privado crea un pedido de la empresa como antes (visible en
Pedidos y en sus conversaciones internas, con aprobaciones para quien decide), pero ahora queda registrado quién
la pidió (`request_runs.requested_by`, costos y avisos por persona). Restringir quién ve las solicitudes es de E2/E3.
La exportación completa de la organización (owner/admin) sigue incluyendo todas las tablas.

## Pedidos: cada trabajo se abre al costado del chat (2026-11-03)

Diseño: `docs/superpowers/specs/2026-11-03-chats-y-pedidos-design.md`. Regla: **un chat por «con quién hablas»; cada
trabajo es un «pedido» que se abre al costado, sin salir del chat.** Un solo nombre en la app: «pedido» (en: «request»,
pt: «pedido»); ya no se dice «solicitud», «trabajo» ni «conversación del trabajo».

**Barra lateral en tres grupos** (`ChatsView.tsx`): **Agentes** («Toda la oficina» y un chat por agente), **Personas**
(`PeopleChatsList`: compañeros y canales) y **Pedidos** (reemplaza «Conversaciones anteriores»): cada conversación de
tipo `request` con su estado, quién lo hace y cuándo; los que necesitan a la persona van primero. Archivar, renombrar y
eliminar siguen siendo los de la conversación (`PATCH/DELETE /conversations/{id}`), ahora dentro del panel. El
buscador busca en los tres grupos a la vez: nombres y puestos de los agentes, nombres de canales y títulos de pedidos en
el navegador (sin tildes ni mayúsculas) más lo que el backend encuentra en los mensajes (`GET /conversations?q=`); un
acierto del canal `office` lleva a «Toda la oficina», uno de `agent:<id>` a ese agente y uno de `request` a su pedido.

**Estado de un pedido** (`lib/pedidosLogic.ts`, `pedidoStatus`): falló > listo > **necesita tu ayuda** (pregunta abierta
del agente en `useStore.questions` o tarea `needs_input`, del trabajo de Proyectos) > **espera tu aprobación** (pedido
`awaiting_approval`/`awaiting_confirmation`, tarea `awaiting_approval` o aprobación pendiente de una de sus tareas) > en
pausa > en curso. Sin el pedido cargado: «sin estado» (nunca se inventa).

**Una tarjeta por pedido en el chat**, en el último mensaje que lo nombra (`pedidoCardAnchors`): título, estado, quién y
«Abrir pedido»; si necesita ayuda o aprobación lo dice y lleva a Pendientes (`/approvals/inbox`). Se quitaron la
etiqueta «Solicitud» y los botones «Ver informe» / «Ver proyecto» / «Ver cómo lo trabajaron»: el chat ya no cambia a la
conversación interna.

**Panel del pedido** (`PedidoPanel.tsx`): drawer a la derecha (en pantallas anchas comparte la fila con el chat; en
móvil, pantalla completa con «Volver»), cierra con la X, Esc o Atrás. Pestañas: **Resumen** (qué se pidió, estado,
quién, costo, fechas, «Abrir proyecto» si es parte de uno, organizar), **Pasos** (`GET /requests/{id}`: tareas con su
agente y estado), **Archivos** (los `files` de los mensajes del pedido, última versión de cada ruta, en el visor de
archivos), **Informe** (`ReportView`, antes en un modal) y **Cómo lo trabajaron** (`GET /conversations/{id}/messages`,
solo lectura).

**URL.** El pedido abierto vive en `?pedido=<id del pedido>` sobre la ruta del chat (`/chats`, `/chats/<agente>`):
abrir uno agrega una entrada al historial (Atrás lo cierra y la persona queda en el chat donde estaba); cambiar de un
pedido a otro la reemplaza. Enlaces viejos: `/chats/<agente>` sigue igual y `?conv=<id de conversación>` abre el pedido
de esa conversación. La notificación de un pedido terminado (`push.FromRequest`) y la tarjeta de un comando que creó un
pedido abren `/chats?pedido=<id>` (si ya se está en Chats, el panel se abre sin navegar: `lib/pedidoNav.ts`).

Textos del backend: el mensaje del plan termina con «Puedes seguir el avance en el pedido, aquí mismo en el chat.» y el
del informe con «Lo encuentras en el pedido, aquí mismo en el chat.» (`chat_local.go`). Sin cambios de almacenamiento.
