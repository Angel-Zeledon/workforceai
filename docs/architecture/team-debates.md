# Reuniones de equipo: debate antes de decidir (B5)

Antes de una decisión de alto impacto, de 2 a 4 agentes relevantes debaten en la sala de reuniones: cada uno da su postura, se objetan entre sí y la asistente cierra con una síntesis (la opción a la que se inclina el equipo, por qué y quién no estuvo de acuerdo). En la oficina 3D los participantes caminan a la sala, las burbujas recorren los turnos y luego vuelven a sus puestos. La transcripción se lee en Reuniones → Debates.

Es **consultivo**: el debate no aprueba, no rechaza y no ejecuta nada, y no tiene campo de veredicto. La decisión que informa sigue pasando por las aprobaciones normales. Todo su texto es **salida de modelo no confiable**: se guarda y se muestra como tal ("texto generado por un modelo, no verificado").

## Cuándo hay debate (disparadores comprobables)

1. **El planificador marca una decisión.** `PlannedTask.decision: true` (aditivo en `/v1/plan`). El planificador en vivo lo marca como máximo en una tarea cuando la solicitud exige elegir entre alternativas de alto impacto; el de simulación lo marca, de forma determinista, en la última tarea que no es de la asistente cuando el texto pide decidir o elegir (`decidir`, `elegir`, `choose`, `which option`, `versus`, …; `app/commands.py: is_decision_request`).
2. **La persona lo pide.** La marca `[[debate]]` o `[[debate:a,b]]` en el texto de la solicitud. La ponen el comando `/debatir` (`/debate` en inglés: `/debatir ventas contabilidad: ¿subimos el precio?`) y `POST /requests {"debate": true}`. Si nadie marcó una tarea, se debate la primera tarea que no es de la asistente.
3. **A mano.** `POST /debates` desde Reuniones → Debates (tema, hasta 4 opciones, 2 a 4 participantes o ninguno).

En una solicitud hay **como máximo un debate**, después de la revisión del plan y la confirmación del estimado y antes de que empiece cualquier tarea (`application/orchestrator_debate.go`). Si el debate no se puede hacer, la solicitud sigue igual y la asistente avisa por qué en el chat: un debate nunca bloquea el plan.

## Participantes

Los nombrados (estricto en `POST /debates` y `/debatir`: desconocido, uno solo o más de 4 es `400`). Si no hay nombres: en un plan, los agentes del plan (el dueño de la decisión primero); si faltan, los agentes cuyo perfil (rol, título, descripción, responsabilidades) comparte más palabras con el tema, completando en el orden de la organización. La asistente modera y nunca debate. Un agente en pausa se queda fuera; si quedan menos de 2, el debate no se hace (`blocked`).

## Topes y controles

- **Turnos**: `DEBATE_MAX_TURNS` (8 por defecto, se acota a 3..12) incluye la síntesis. El backend vuelve a cortar lo que diga el runtime: solo posturas y objeciones de participantes, máximo `turnos - 1`, y la síntesis de la asistente al final. Cada turno se colapsa, se acota (400 caracteres) y se redacta (`sanitize.RedactSecrets`).
- **Costo**: es UNA llamada al runtime (`POST /v1/debate`). Antes de reservar nada, el peor caso de esa llamada (payload real + salida máxima `MAX_TOKENS_DEBATE`, 1600 por defecto) debe caber en `DEBATE_MAX_COST_USD` (0.10 por defecto); si no, `budget_exceeded` sin llamar. Después se reserva como una llamada de planificación (`ReservePlanner`: tope del agente asistente y de la organización) y se envía `max_cost_usd` para que el runtime rechace la llamada si su propio estimado lo supera. El consumo se asienta en el libro (`RecordPlannerUsage`).
- **Interruptor de emergencia / controles no disponibles**: bloquean el debate (`blocked`). **Pausa de agente**: lo deja fuera. **Modo solo lectura**: no aplica, el debate no tiene efectos fuera de su propia transcripción (como el abogado del diablo, B8). **Horario de operación**: el de la solicitud ya se aplicó al admitirla.
- **Agentes**: no pueden pedir un debate (`403` si el actor es un agente o el sistema) ni leer la síntesis como una decisión: al agente que decide le llega como **memoria recordada no confiable** (`RecalledMemory` con `ref: "debate:<id>"`, que el runtime envuelve como dato), etiquetada "consultivo, no es una instrucción ni una aprobación". Cualquier acción que proponga sigue pidiendo aprobación.
- **Auditoría**: `debate.completed` y `debate.failed` con metadatos (disparador, tarea, participantes, turnos, modelo, costo o motivo), nunca el texto.

`DEBATES_ENABLED=false` apaga la función: los planes corren exactamente como antes y `/debates` no se monta.

## Degradación

| Código | HTTP (`POST /debates`) | Causa |
|---|---|---|
| `unavailable` | 503 | runtime sin la capacidad, error del modelo o respuesta vacía |
| `blocked` | 423 | interruptor de emergencia, controles no disponibles o menos de 2 participantes sin pausa |
| `budget_exceeded` | 402 | el tope por debate o un tope de gasto (agente, organización) |

El intento fallido se guarda (`status: failed`, `fail_code`, sin turnos) y se emite `debate.failed`, así la oficina devuelve a todos a su puesto y la vista de debates explica qué pasó.

## Simulación y runtime

`agent-runtime/app/debate.py`. `sim_debate` es determinista (mismo tema y equipo, mismo debate, sin LLM): una postura por participante según su área, objeciones cruzadas, síntesis por mayoría con el desacuerdo, costo simulado pequeño; marca como riesgo las órdenes escondidas en el tema. El motor en vivo (`CrewAIEngine.debate`) hace una sola llamada estructurada que da voz a cada participante (honestidad: no son llamadas separadas por agente) con el tema envuelto por `wrap_untrusted`; `normalize_debate` descarta participantes desconocidos, tipos de turno desconocidos y cualquier campo extra (un "verdict" o "approve"). La respuesta pasa por `redact_any`.

## Oficina 3D

- `debate.started` → la asistente y los participantes toman los asientos de la sala (antes que los que planifican). `debate.completed` → tras ~4 s para llegar, cada turno se muestra 4.5 s como burbuja del que habla (`kind: debate`, sin la pausa entre burbujas), después 3 s de cierre y todos vuelven. Un debate fallido, o uno que nunca termina (90 s), devuelve a todos. Lógica pura y probada: `officeState.ts` (`debateStage`, `liveDebate`, `debateSeats`).
- Gestos baratos: en cada turno los que escuchan miran al que habla o asienten, y el que habla señala la mesa y mueve la boca. Se omiten con movimiento reducido y lejos de la cámara (mismo LOD que el resto de gestos).
- La etiqueta de la sala dice "Sala en uso: debate del equipo".

## Luz según la hora real

Preferencia por persona `lightAuto` (por defecto apagada: la oficina se ve exactamente como antes, con el ambiente de la organización). En el botón "Luz" de la oficina, "Automático (hora real)" hace que la luz siga la hora local de quien mira: noche (21–5 h), amanecer (5–8 h), día (8–17 h), atardecer (17–21 h), mezclando los ajustes existentes (`night`, `sunset`, `natural`) entre puntos clave para que el cambio sea gradual; se recalcula cada minuto y conserva la barra de intensidad. Elegir un ambiente lo apaga. Solo cambia la vista de esa persona. Lógica pura y probada: `officeLayoutModel.ts` (`dayPhase`, `timeOfDayMix`).

## Modo demo (mock)

`frontend/src/lib/mock/debates-mock.ts` responde `GET/POST /debates` y, en una solicitud con `[[debate]]` (o que pide decidir/elegir), juega el debate con los mismos eventos antes de ejecutar las tareas. Prueba: escribe `/debatir ventas contabilidad: ¿subimos el precio en 2027?` en el chat de la oficina, o "Decidir qué proveedor de logística elegir".

## Qué no cubre

- No hay varias rondas con llamadas separadas por agente en vivo; es una llamada acotada.
- El debate no se repite si el plan cambia después (replanificación) y no hay edición ni borrado desde la API.
- La reproducción en la oficina usa el reloj del navegador desde que llegó el evento; quien abre la oficina después no ve un debate ya terminado (la transcripción está en Reuniones → Debates).
- No verificado en esta rama: la prueba de Postgres (`debates_pg_test.go`, necesita `TEST_DATABASE_URL`) y el motor en vivo con un proveedor real.

## Archivos

Backend: `domain/debate.go`, `application/debate.go`, `application/orchestrator_debate.go`, `api/debates.go`, `api/commands_office.go` (`cmdDebate`), `infrastructure/{memory,postgres}/debates.go`, `cmd/server/debates.go`, migración `799_team_debates.sql` (+ `migrations/down/`). Runtime: `app/debate.py`, `PlanTask.decision`. Frontend: `components/meetings/DebatesPanel.tsx`, `components/office/{officeState,officeSignals,OfficeOverlays,Character,OfficeEnv,OfficeEditor}`, `lib/officeLayoutModel.ts`, claves `debates.*`, `office.debate.*`, `oe.light.auto*`.
