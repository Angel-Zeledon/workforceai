# Presencia humana y chat entre personas

Extiende `03-eventos-realtime.md` y `chat-routing.md`. Hasta ahora la oficina mostraba solo agentes; aqui las PERSONAS de la organizacion estan en la misma oficina 3D, se ven entre si, se acercan y hablan. Todo es aditivo: un cliente que no conoce los eventos nuevos los ignora.

## 1. Presencia en tiempo real

Paquete `backend/internal/presence`. Estado en memoria (es vivo por naturaleza: muere con el socket), un espacio de nombres por organizacion. La identidad de cada persona sale del token del socket, nunca del frame.

- **Entrar / salir**: el WebSocket (`/ws`) hace `Join` al abrirse y `Leave` al cerrarse. Varias pestanas de la misma persona comparten una entrada (`presence.joined` solo la primera vez, `presence.left` al cerrar la ultima).
- **Frames del cliente** (JSON, limite 4 KiB): `{"type":"presence.move","x","z","zone"}`, `{"type":"presence.status","status"}`, `{"type":"presence.ping"}` (actividad sin movimiento). Los invalidos se ignoran.
- **Estados**: `available`, `focus` (no molestar), `meeting`, `away`. `GET /presence` da la foto (`people`, `self`); `PUT /presence/status` es la via REST para cambiar de estado.
- **Auto-away**: una persona `available` sin actividad 5 min pasa a `away` con `auto_away=true`; cualquier actividad (mover, ping, estado) la devuelve a `available`. `focus` y `meeting` son deliberados y nunca se tocan. Un `away` elegido a mano no se revierte solo.
- **Throttle**: el servidor guarda siempre la posicion actual, pero emite `presence.moved` como mucho cada 120 ms y solo si se movio >= 6 cm; lo retenido lo libera `Sweep` (ticker) para que el punto final siempre llegue. El cliente aplica el mismo throttle antes de enviar (`presence/logic.ts`, `createMoveThrottle`) y suaviza los avatares remotos. Las coordenadas se acotan al suelo (`DefaultBounds`, igual que `FLOOR` de `officeGeometry.ts`) y se rechazan NaN/Inf.
- **Aislamiento**: el hub indexa por `org`; cada evento lleva `org_id` y `events.Hub` solo lo entrega a los sockets de esa organizacion. El mismo `user_id` en dos organizaciones son dos personas distintas (test `TestOrgIsolation`).

### Eventos (todos a toda la organizacion)

| Tipo | Payload |
|---|---|
| `presence.joined` | `Person {user_id, name, status, x, z, zone, since}` |
| `presence.left` | `{user_id}` |
| `presence.moved` | `{user_id, x, z, zone}` |
| `presence.status` | `{user_id, status, auto_away}` |
| `presence.proximity` | `{a, b, near, distance, can_talk}` |

## 2. Proximidad y punto de enlace de la llamada

Con histeresis (entra a 2.5 m, sale a 3.2 m) para que quien esta en el borde no parpadee. `can_talk` es falso si cualquiera de los dos no esta `available` (focus/meeting/away): la UI no ofrece nada. La proximidad se recalcula en cada broadcast de posicion y al cambiar de estado.

- **Backend**: `Hub.OnProximity(func(org, Proximity))` es el punto de enlace del stream de llamada (se invoca fuera del lock). Ademas se emite `presence.proximity`.
- **Frontend**: `presence/logic.ts#nearby` (misma regla) calcula localmente con mi posicion real, muestra la oferta "Iniciar conversacion" (`PresenceHost`), y dispara `window` `CustomEvent("aiw:presence:proximity")`. `registerConversationStarter(fn)` (`presence/tap.ts`) sustituye la accion por defecto (abrir el DM) cuando exista la llamada.

## 3. Chat entre personas

Paquete `backend/internal/peoplechat`, migracion `730_people_chat.sql` (down en `backend/migrations/down`). Separado del chat de agentes (`conversations`), que no se toca.

- **Canales** (`pc_channels`): `org` (uno por organizacion, todos), `department` y `project` (los crea admin/owner; uno por `scope_key`), `group` (privado, lo crea cualquier miembro no viewer) y `dm` (dos personas; `scope_key` = par ordenado, abrirlo dos veces devuelve el mismo).
- **Miembros** (`pc_members`): personas **y agentes**. `last_read_seq` por miembro. El canal `org` es implicito para toda persona (la fila con el marcador se crea al primer acceso).
- **Mensajes** (`pc_messages`): solo `INSERT` para la aplicacion (sin editar ni borrar). `seq` por canal se reparte con `UPDATE ... last_seq+1` en la misma transaccion.
- **No leidos**: `unread = last_seq - last_read_seq`; `mentions_unread` cuenta los mensajes ajenos que te mencionan. Escribir en un canal avanza tu marcador. Frontend: `chats/peopleLogic.ts` (idempotente por `seq`, el canal abierto y enfocado se lee al llegar).
- **Menciones**: `@nombre.apellido`, `@nombre_apellido`, `@id`, o el nombre solo si es inequivoco; sin acentos ni mayusculas; un correo no es mencion. Se resuelven contra los miembros del canal (en `org`: el directorio). Un agente miembro mencionado dispara `Service.OnAgentMention` (cableado en `cmd/server/people.go` con `peoplechat.MentionResponder`: una respuesta corta con tope de coste via `Orchestrator.PeopleChatReply`, limitada por canal; un mensaje de agente nunca dispara a otro) y puede responder con `PostAsAgent`.

### Permisos (`rules.go`, probados en `peoplechat_test.go`)

| | org | department / project | group | dm |
|---|---|---|---|---|
| leer | todos | miembros y admin/owner | solo miembros | solo los dos |
| escribir | no viewers | miembros | miembros | los dos |
| gestionar miembros | nadie | owner del canal y admin/owner | owner del canal y admin/owner | nadie |

Un canal ajeno responde 404 (no se revela que existe). Un viewer solo lee. Un admin NO lee grupos ni DMs de otros.

### Privacidad en el socket

`domain.Event` gano `Audience []string` (aditivo, `omitempty`). `events.Hub.RegisterUser(org, user)` identifica el socket; un frame con `audience` solo llega a esos usuarios de esa organizacion y el hub borra el campo antes de entregar. Los mensajes de DM/grupo se dirigen a sus miembros; los del canal `org` van a toda la organizacion. Una audiencia vacia no ensancha a la organizacion.

| Tipo | Payload | Destino |
|---|---|---|
| `pchat.message` | `{channel_id, channel_kind, message}` | miembros del canal (org: todos) |
| `pchat.channel` | `{channel: ChannelView}` | miembros (y el quitado) |
| `pchat.read` | `{channel_id, seq}` | solo quien leyo (sincroniza sus pestanas) |

### API REST

`GET /presence`, `PUT /presence/status`, `GET /people-chat/directory`, `GET|POST /people-chat/channels`, `POST /people-chat/dm`, `GET|POST /people-chat/channels/{id}/messages` (`?before=<seq>&limit=`), `POST .../read`, `POST .../members`, `DELETE .../members/{kind}/{id}`. Montadas en un solo bloque (`mountPresence`, `api/presence_people.go`); `Deps.Presence` / `Deps.PeopleChat` nulos las dejan apagadas. Sin base de datos el chat usa `MemoryStore` (demo).

## 4. Frontend

- `components/presence/`: `logic.ts` (puro: proximidad, throttle, paso, teclas), `store.ts`, `tap.ts` (puente con el unico socket de `lib/ws.ts`), `PeopleLayer.tsx` (avatares en la escena 3D; el propio se mueve con WASD/flechas o doble clic en el suelo), `PresenceHost.tsx` (pastilla "N en linea", panel de personas y agentes con lo que hace cada uno, selector de estado, oferta de conversacion).
- `components/chats/`: `PeopleChatsList` (seccion "Equipo" en la barra lateral de Chats, con insignias), `PeopleChatPane` (mensajes, menciones resaltadas, autocompletado `@`, miembros con agentes), `peopleChatStore`, `peopleLogic.ts`.
- i18n ES/EN/PT: claves `presence.*` y `pchat.*`.
- Tocado fuera de las carpetas nuevas (marcado y minimo): `lib/ws.ts` (2 llamadas al puente), `office/OfficeScene.tsx` y `office/OfficeView.tsx` (una linea cada una), `chats/ChatsView.tsx` (lista y panel), `api/router.go` y `api/ws.go` (bloques `>>> human presence`).

## 5. Pendiente / limites conocidos

- Con Redis el estado de presencia se comparte entre replicas (`presence.RedisRegistry`); sin Redis es por replica.
- Periodo de gracia en `Leave` (`PRESENCE_LEAVE_GRACE`, p. ej. `8s`; `-1s` lo desactiva) para no emitir `left`/`joined` al reconectar el socket.
- Los avatares no esquivan muebles (solo el limite del suelo); `editorWorld.ts` tiene las huellas para colisiones.
- El stream de llamada (voz/video).
- Insignia de no leidos en la navegacion principal (`usePeopleUnread` ya la expone).
- Persistir el estado elegido entre sesiones y respetar horarios (calendario).
