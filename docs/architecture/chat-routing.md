# Chat conversacional con enrutamiento

Estado: implementado (backend Go + agent-runtime). Contrato final. Resuelve el problema "les pongo *hola* y se arma una solicitud con plan y tareas": ahora todo texto del usuario es primero **conversación**, y solo una **tarea** crea solicitud.

> Cambios respecto al contrato pedido (todos aditivos): ver sección 12.

## 1. Idea

```
usuario --POST /api/v1/messages--> backend --(POST /v1/route)--> runtime
                                     |  decide intent y quién responde (fallback: reglas locales)
                                     |--(POST /v1/chat-reply por responsable)--> runtime
                                     v
           WS: chat.message / chat.typing / route.decided
                                     |
        intent=task ──────────────> mensaje que explica quién y por qué ──> flujo existente (plan, aprobación humana, informe)
```

- `smalltalk` / `question`: **no** crean solicitud, plan ni tareas. Solo mensajes, costo del ledger (kind `chat`) y eventos.
- `task`: el agente confirma y se crea la solicitud normal (`request.received`, `plan.created`, `task.created`…), con revisión de plan y aprobaciones humanas intactas.
- `POST /requests` y los eventos existentes no cambian (el escenario de $50,000 de smoke/e2e sigue igual).

## 2. Conversaciones

| id | Qué es | Quién responde |
|---|---|---|
| `office` | Canal general | Router: saludo = la asistente primero + 1-2 compañeros (no todos); tema = el dueño del tema y, como mucho, 1 contribuyente |
| `agent:<id>` | Chat 1:1 | **Solo ese agente** (regla dura del backend, aunque el router diga otra cosa) |

El id guardado es el mismo string. En Postgres la clave de `conversations` pasa a `(org_id, id)` (migración `260_chat.sql`). Se crean al primer mensaje; un chat que nunca empezó se lee como lista vacía (no 404). `GET /conversations` los lista junto a las conversaciones de solicitudes.

## 3. API REST

### `POST /api/v1/messages` (permiso `conversations:write`)

```json
{ "conversation": "office", "text": "hola" }
```
`conversation` es opcional (`office`). → `202 {"message_id": "...", "turn_id": "..."}`. Errores: `400` texto vacío (o solo caracteres invisibles), de más de 4000 caracteres, conversación inválida o `Idempotency-Key` de más de 128 caracteres; `404` agente desconocido; `413` cuerpo mayor a 1 MB. El resto llega por WebSocket.

El texto se **sanitiza** antes de guardarse, mostrarse al equipo o entrar en un prompt: UTF-8 inválido, caracteres de control (NUL, ESC…), sobrescrituras bidireccionales y caracteres de ancho cero se eliminan (se conservan saltos de línea, tabulaciones y emojis compuestos). `POST /conversations/{id}/messages` (conversaciones de solicitudes) aplica el mismo saneado y el mismo tope.

**Idempotencia**: con el header `Idempotency-Key` (o `client_message_id` en el cuerpo) un reintento o doble clic con la misma clave en la misma conversación devuelve el turno original (`202` + header `Idempotent-Replayed: true`) en vez de publicar y responder dos veces; la clave vive 10 minutos.

**Orden**: los turnos de una misma conversación se ejecutan en cola, uno tras otro, en el orden de llegada. Las respuestas no se entrelazan entre turnos (varios mensajes rápidos) y cada turno ve en `history` lo que dijo el anterior (nunca su propio mensaje ni los que el usuario escribió después).

### `GET /api/v1/conversations/{id}/messages`

Sin parámetros: todos los mensajes, más antiguo primero (igual que antes). Con `?limit=N` (1-200): los **N más recientes**; con `?before=<message_id>` (y `limit`, def. 50): los N anteriores a ese mensaje. El cuerpo sigue siendo un **array** (retrocompatible); los headers `X-Has-More: true|false` y `X-Next-Before: <id del mensaje más antiguo devuelto>` (solo si hay más) indican cómo seguir. `404` si el cursor no existe.

Mensaje (`domain.Message`, campos nuevos opcionales): `id, conversation_id, from, to, kind, text, task_id, ts` + `turn_id`, `reply_to` (id del mensaje del usuario al que responde), `request_id` (solicitud que creó una tarea del turno). `kind`: `chat | consult | answer | delegation`.

## 4. Eventos WebSocket (mismo envelope)

| Evento | Payload |
|---|---|
| `chat.message` | `{id, conversation, turn_id, from, to, kind, text, reply_to, ts, request_id?}` — `from`: `"user"`, id de agente o `"system"`; el mensaje del propio usuario también se emite |
| `chat.typing` | `{conversation, turn_id, agent_id, on}` — `on:true` mientras piensa/espera su turno; `on:false` al terminar (siempre par) |
| `route.decided` | `{turn_id, conversation, intent, topic, responders:[{agent_id, role, reason}], source, consult?}` |
| `task.created` | igual que antes + `assigned_reason` (también dentro de `task`) |

`source`: `llm` o `rules` (runtime), `local` (reglas del backend porque el runtime no respondió) o `handoff`. `consult` solo existe cuando el tema es de otra área (sección 7). Los mensajes `chat.message` **no** emiten `message.sent` (evita duplicados); sí aparece `agent.state_changed` (`talking`) mientras un agente ocioso responde.

Escalonado: el 2.º y 3.º responsable esperan `CHAT_STAGGER` (def. 900 ms) y las llamadas al runtime tienen `CHAT_TIMEOUT` (def. 30 s) con `chat.typing on` visible.

## 5. Contrato backend -> runtime

Ambos endpoints son opcionales para el backend (type-assert `ChatRuntime`): sin ellos, o ante cualquier error/timeout (`Config.ChatTimeout`, 30 s) o respuesta inválida, el backend usa **reglas locales** (`chat_local.go`) y líneas enlatadas honestas ("no puedo darte una buena respuesta ahora"), y lo audita (`chat.route_fallback`, `chat.reply_fallback`).

### `POST /v1/route`

Entrada: `{text, conversation, agents:[{id,role,title,name}], history:[{from,text}], locale, tone}` (solo `text` es obligatorio; también acepta `allowed_providers` / `preferred_providers` como el resto).

Salida: `{intent: smalltalk|question|task, topic, responders:[{agent_id, role: primary|contributor, reason}], consult?: {agent_id, reason}, source: rules|llm, usage?, provider?, model?, replies?}`. `replies` está reservado (el backend pide una respuesta por responsable).

- **Simulación**: reglas por tema y palabras clave (es/en, sin acentos): finanzas/balance/margen→`accounting`; contrato/legal→`legal`; contratar/personal→`hr`; ventas/propuesta/cliente→`sales`; datos/análisis→`analyst`; operaciones/capacidad→`operations`; saludos/gracias/ayuda→`assistant`. `task` si pide producir/ejecutar (prepara, haz, calcula, revisa, redacta, vende…, o "necesito un…", "¿puedes…?"); `question`/`smalltalk` si no. "Hablemos del balance" es conversación; "hola, ¿cómo va el balance?" es pregunta de finanzas.
- **Contribuyente**: solo si el texto menciona un área *relacionada* (tabla de relaciones reales) o hay una pista clara; máximo 1, habla breve y después.
- **Live**: clasificación con el LLM (proveedores con respaldo de `providers.py`, política de datos aplicada), salida JSON validada contra el roster y las reglas duras; cualquier fallo (proveedor caído, JSON inválido, `no_allowed_provider`) cae a las reglas, nunca falla.

### `POST /v1/chat-reply`

Entrada: `{agent:{id,role,title,name,persona}, text, conversation, intent, topic, responder_role, reason, agents, history, prior_replies, consult?:{from_agent_id,question}, slot, consult_to?, limit?, handoff?, locale, tone}`.
Salida: `{text, kind: chat|answer, consult?: {to_agent_id, question}, usage, provider?, model?}`.

- Simulación: guion variado en `sim_content.py` / `sim_content_en.py` (clave `CHAT`), con rotación: no repite una variante que el agente dijo recientemente (`history`/`prior_replies`). Tono regional: `ar` (voseo) en simulación; en live el tono va como instrucción.
- Live: LLM con presupuesto (`max_tokens` 350, 1-3 frases) y persona del agente; el historial va como datos no confiables.
- El texto sale siempre redactado de secretos.

## 6. Saludos, preguntas y 1:1

- Saludo en la oficina: responde primero la asistente; 1-2 compañeros (determinista por texto) saludan breve y escalonados. "Gracias"/ayuda: solo la asistente.
- Pregunta de finanzas: responde solo contabilidad; legal aporta una línea solo si el tema lo amerita.
- 1:1: responde solo ese agente, también ante saludos y tareas.
- Si el usuario nombra a **un** compañero al inicio del mensaje en la oficina ("Tomás, …"), responde solo esa persona.

## 7. Fuera de competencia: redirección, traspaso y reasignación

Cada agente tiene diálogo propio (contador preciso y algo seco, legal cauteloso, ventas entusiasta, RR. HH. empático, operaciones práctico, análisis curioso, asistente servicial). Hay 6 frases base por rol más frases específicas por par (p. ej. contador→ventas: *"Uy, eso le toca a Valeria Ríos, ella lleva las ventas…"*), y el área del compañero se inserta, así que cada par rol→rol tiene ≥ 4 variantes en es y en. El nombre mencionado es siempre el del compañero correcto (el del roster).

1. **Pregunta ajena** (1:1 o por nombre en la oficina): el router devuelve `consult:{agent_id}`; el agente consultado responde con la línea de redirección (nombra al compañero y ofrece pasárselo) y se emite un mensaje `consult` visible (agente→compañero) seguido de la respuesta `answer` del compañero. No se dispara a nadie más.
2. **Traspaso**: el backend recuerda la oferta (memoria `chat/handoff:<conversación>`, una por conversación, vive hasta el siguiente mensaje). Si el siguiente mensaje es una aceptación corta ("sí", "dale", "pásaselo", "yes, go ahead") o una insistencia ("de todos modos", "insisto"), `route.decided` sale con `source:"handoff"`: el agente se despide con su voz y el compañero responde la pregunta **original**. Cualquier otro mensaje cancela la oferta.
3. **Tarea ajena** (1:1 o por nombre): `route.decided.consult` significa "reasignar a". El agente la rechaza con diálogo propio nombrando al compañero; la solicitud se crea igual (aprobación humana intacta) y las tareas del plan asignadas al agente que declinó **y que no mencionan su propia área** pasan al dueño del tema con `assigned_reason`: *"Reasignada: Tomás Vidal no lleva este tema, así que la toma Valeria Ríos, que lleva la relación con el cliente…"* (auditado: `chat.task_reassigned`). La asistente nunca declina tareas: coordina.

## 8. Tareas desde el chat

El responsable (la asistente en la oficina, el agente en 1:1, o el nombrado) confirma con una línea corta y se llama al flujo de `Submit`. Cuando el plan existe, un único mensaje (`from` = quien confirmó, `request_id` en el payload) explica en lenguaje humano a quién asigna y por qué:

```
Listo, armé un plan de 5 tareas. Esto es lo que hará cada quien:
• Valeria Ríos (Gerente de Ventas): Analizar cliente y preparar propuesta. Por qué: conoce al cliente y es quien arma la propuesta comercial.
…
Antes de cualquier acción sensible te pediré aprobación. Puedes seguir el avance en Solicitudes.
```
Cada tarea lleva `assigned_reason` (`reason` opcional del planner `/v1/plan`; si falta, el backend lo deriva del rol). Al terminar el informe (o fallar la solicitud) el chat recibe el aviso correspondiente.

## 9. Límites reales ("no puedo hacer eso")

`chat-reply` con `limit: kill_switch | paused | budget | read_only | no_connection` devuelve la negativa con la voz del rol (3 aperturas por rol × 2 núcleos por límite, rotando; es y en). **Siempre guionado, también en live** (sin LLM y sin costo: un límite nunca debe gastar). El backend la usa para:

| Situación | Quién habla |
|---|---|
| Kill switch / controles no disponibles (todo el equipo) | el agente al que se preguntó (la asistente en la oficina); no se llama a nadie más y **no se crea solicitud** |
| Agente en pausa (el preguntado) | ese agente; un compañero pausado que era solo contribuyente se omite sin avisar |
| Tope de presupuesto del agente o de la organización | el agente que iba a responder; el turno termina |
| Modo solo lectura (tarea) | el agente lo menciona y el trabajo continúa (la política sigue bloqueando los efectos) |

`no_connection` está en el contrato del runtime (líneas listas) pero el backend aún no lo dispara: no hay un punto del chat que sepa que falta una conexión. Si el runtime no puede dar la línea, el backend publica un mensaje `from:"system"` con texto fijo localizado.

## 10. Costos, controles y auditoría

- Cada respuesta reserva presupuesto (`Budget.Reserve`, sin solicitud: topes de agente y de organización) y registra el uso en el ledger con `kind:"chat"` y `request_id` vacío. El tope de la organización también cuenta el gasto de chat (el total por solicitudes no lo incluye). Las líneas de límite y los traspasos no cuestan.
- Kill switch, pausa de agente y topes se respetan en cada respuesta (`ExecutionGuard.Admit`).
- Auditoría solo con metadatos: `chat.turn` (intent, topic, responders, source), `chat.route_fallback`, `chat.reply_fallback`, `chat.blocked`, `chat.budget_exceeded`, `chat.task_reassigned`. Nunca el texto.
- Demo reset borra las conversaciones.

## 11. i18n y tono

Textos visibles localizables (es por defecto, `en`) tanto en el runtime (`sim_content*.py`) como en el backend (`chat_local.go`: avisos, explicación del plan, títulos). El idioma y el tono salen de la configuración de la organización (`locale`, `tone`, tono por agente) y viajan en cada llamada.

## 12. Diferencias respecto al contrato pedido

- `GET …/messages` devuelve array + headers de paginación (no un objeto envoltorio) para no romper a los consumidores actuales.
- `route.decided` y `chat.message` añaden `source`, `consult`, `id`, `ts`, `request_id` (aditivos).
- `route.decided.consult` se usa también para tareas (significa "reasignar a").
- Mensajes nuevos de `chat-reply`: `limit`, `handoff`, `consult_to`; respuesta `kind` y `consult`.
- Un mensaje de tarea nombrando a un compañero lo confirma él (no la asistente); en 1:1 confirma y explica el agente.
- Los eventos de `chat.*` no se auditan uno a uno (solo `chat.turn`).

## 13. Barrido de comportamiento (calidad de los agentes)

Arnés: `agent-runtime/tests/behavior_sweep.py` (extremo a extremo, HTTP + WebSocket contra un backend y runtime reales; ver su docstring) con los escenarios de `tests/behavior_cases.py`, que también alimentan `tests/test_behavior_routing.py` (en proceso, sin red). Cubre saludos, cortesías, preguntas por tema, ambiguas, tareas, "hablemos de…", nombres, 1:1 propio/ajeno, traspasos, tareas ajenas, límites (pausa, solo lectura, kill switch, presupuesto), robustez (entradas raras, concurrencia, orden y duplicados de eventos, runtime caído), contexto, idioma y tono.

Reglas añadidas por el barrido:

- **Typos y jerga**: `fix_typos` corrige palabras de tema a una edición de distancia ("balanse", "contrado") sin tocar palabras reales ni verbos de tarea; jerga de dinero/ventas (*lana*, *plata*, *vetas*). Roles por nombre de oficio (*contador*, *analista*, *reclutador*…).
- **Intención**: "¿necesitamos un NDA?" (con `?`) es pregunta, "necesitamos un NDA" es tarea; "ayúdame con el contrato" / "help me with the budget" es tarea si nombra un área ("ayúdame a entender…" sigue siendo pregunta); "dile a Tomás que prepare el balance" / "tell Elena to review…" es tarea y la toma ese compañero.
- **Smalltalk**: nuevo tipo `ack` (topic `ack`) para "ok", "vale", emojis, risas ("jajaja") y elogios sueltos, distinto de `thanks`; 👋 es saludo. "Gracias por el balance" es agradecimiento (no una pregunta de finanzas). Un mensaje que es solo un nombre ("Tomás", "hola Tomás", "oye Valeria") lo contesta esa persona.
- **Contexto**: una pregunta sin tema que sigue a una respuesta ("¿y por qué?", "¿y eso?") va a quien contestó primero en el turno anterior; "gracias"/"ok" tras una respuesta los recibe quien respondió. Nunca cambia un chat 1:1.
- **Idioma**: en simulación, un mensaje claramente en inglés a una organización en español se contesta en inglés (y al revés); en live el idioma de la instrucción sigue al del mensaje. Voseo (`ar`) más completo; el sustantivo *cuentas* no se toca.
- **Guiones**: más variantes por rol (5 por tema), líneas de agenda para la asistente, `ack`, sin género fijo en las cortesías. Las líneas de límite son instantáneas y de costo cero. La consulta a un compañero no repite el nombre al que se dirigió el usuario.
- **Plan simulado**: sin escenario guionado, si el texto nombra un área el responsable de esa área trabaja la tarea (antes siempre asistente + analista).
- **Errores claros**: el aviso de fallo de una solicitud nacida en el chat no muestra URLs ni errores de red internos ("el servicio del equipo no responde…"). Sin runtime, un 1:1 sobre un tema ajeno nombra igualmente al compañero correcto.
- **Contrato con el runtime**: Go serializa los slices nil como `null`; el runtime acepta `null` como lista vacía en `agents`/`history`/`prior_replies`. (Antes, cada línea de límite fallaba con 422 y caía al aviso de sistema genérico.)

## 14. Piezas

Runtime: `app/routing.py` (reglas, validación, guiones), `sim_content.py` / `sim_content_en.py` (`CHAT`, `TASK_REASONS`), `main.py` (`/v1/route`, `/v1/chat-reply`), `crewai_engine.py` (live), `security.py` (prompts). Backend: `application/chat.go`, `chat_local.go`, `chat_handoff.go`, `api/chat.go`, `infrastructure/runtime/client.go`, migración `260_chat.sql` (+ down). Tests: `agent-runtime/tests/test_chat.py`, `agent-runtime/tests/test_behavior_routing.py` (+ `behavior_cases.py`, `behavior_sweep.py`), `backend/internal/application/chat_test.go`, `chat_internal_test.go`, `backend/internal/api/chat_test.go`.
