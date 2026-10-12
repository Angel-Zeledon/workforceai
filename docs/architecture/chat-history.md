# Historial de chats (E1)

El usuario puede volver a sus conversaciones con la oficina («General») y con cada agente, y las dos vistas **van de la mano**.

## Modelo: una sola copia de cada mensaje

No hay mensajes duplicados. `messages` sigue siendo la única fuente; General y el hilo de un agente son *vistas* (consultas):

- **General** = todos los mensajes del canal `office` y de cada chat 1:1 (`agent:<id>`), en orden cronológico. Las conversaciones de solicitudes (`request`) no entran: se llega a ellas desde el enlace de la solicitud.
- **Hilo de un agente X** = su chat 1:1 (`agent:X`) + todo mensaje que X envió o recibió + los mensajes del usuario y avisos del sistema de los *turnos* (`turn_id`) en los que X participó. Así un mensaje del usuario en General que se enrutó a X aparece en el hilo de X (mismo id), y lo que se escribe en el chat 1:1 de X aparece en General.
- Cada mensaje devuelto trae `agent_ids`: los agentes que participaron en su turno (las insignias «Enviado a»).

Definición de referencia: `application.InThread` (Go), `chatsLogic.ts` (frontend y mock); el SQL de `postgres/chat_history.go` la replica. Una sola suite (`application/chathistorytest`) la ejecutan el almacén en memoria y Postgres.

## Datos (migración 570)

- `conversations.archived_at`, `conversations.title_custom` (renombrar / archivar).
- `chat_reads(org_id, key, read_at)`: marcadores de lectura **por organización** (no por usuario): `thread:<general|agentId>` y `conv:<id>`. El historial existente se siembra como leído. RLS por `org_id`.
- Índices `messages(org_id, from_id, ts)` y `(org_id, to_id, ts)`.
- Título automático: las conversaciones de solicitud ya nacen con el texto de la petición; si el título está vacío se deriva del primer mensaje del usuario.

## Decisiones y límites

- Búsqueda con `ILIKE` (título y texto), sin FTS; suficiente para el volumen actual.
- Borrar `office` o `agent:<id>` limpia ese historial en **ambas** vistas (es el mismo dato); exige permiso de administrador y queda auditado.
- La no-lectura es por organización: con varios usuarios en una misma organización comparten el marcador.
- Auditoría: solo metadatos (`conversation.updated`, `conversation.deleted` con número de mensajes), nunca texto.
